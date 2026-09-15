#!/usr/bin/env bash
# Mac DHCP (dnsmasq) — 单一入口
# 用法: ./dhcp.sh <detect|install|start|stop|status|leases|uninstall|debug>
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
LABEL="com.local.mac-dhcp"
LAUNCHD="/Library/LaunchDaemons/${LABEL}.plist"
SYS_ETC="/usr/local/etc/mac-dhcp"
SYS_VAR="/usr/local/var/mac-dhcp"
SYS_LOG="/usr/local/var/log/mac-dhcp"
SYS_BIN="/usr/local/sbin"
ENV_FILE="${ROOT}/config/network.env"
HOSTS_FILE="${ROOT}/config/dhcp-hosts.conf"
# dnsmasq 实际租约（系统目录，launchd 可写；Desktop 受 TCC 限制不能写）
LEASES_DB="${SYS_VAR}/dnsmasq.leases"
# 本目录可读副本（leases / status 时同步过来）
LEASES_TXT="${ROOT}/leases.txt"
PID_FILE="${SYS_VAR}/dnsmasq.pid"

info() { printf '\033[34m➜ %s\033[0m\n' "$*"; }
ok()   { printf '\033[32m✓ %s\033[0m\n' "$*"; }
warn() { printf '\033[33m! %s\033[0m\n' "$*"; }
err()  { printf '\033[31m✗ %s\033[0m\n' "$*"; }

need_root() {
  [[ "$(id -u)" -eq 0 ]] || { err "请使用 sudo"; exit 1; }
}

dnsmasq_bin() {
  local arch bundled
  arch="$(uname -m)"
  bundled="${ROOT}/bin/${arch}/dnsmasq"
  [[ -x "$bundled" ]] && { echo "$bundled"; return; }
  [[ -x "${SYS_BIN}/dnsmasq" ]] && { echo "${SYS_BIN}/dnsmasq"; return; }
  err "未找到 dnsmasq: bin/${arch}/dnsmasq"; exit 1
}

# 让二进制在本机可执行：清隔离属性 + 确保有 adhoc 签名。
# 解决跨机器拷贝后 macOS 直接 SIGKILL(Killed: 9)的问题。
heal_binary() {
  local f="$1"
  [[ -e "$f" ]] || return 0
  xattr -dr com.apple.quarantine "$f" 2>/dev/null || true
  # 已有有效签名就不动；否则补一个 adhoc 签名
  if ! codesign --verify "$f" 2>/dev/null; then
    codesign --force --sign - "$f" 2>/dev/null || true
  fi
}

# 一进脚本就把整个包(bin/ui/.command/.app 等)的隔离属性清掉。
# 下载/解压/AirDrop 会给整包打 com.apple.quarantine，Gatekeeper 会拦。
# 注意：这清不掉「正在启动本脚本的那个 .app/.command」自身的拦截
# （那需要用户先右键→打开一次，或先手动 xattr 整个文件夹）。
heal_bundle() {
  [[ -n "${ROOT:-}" && -d "$ROOT" ]] || return 0
  xattr -dr com.apple.quarantine "$ROOT" 2>/dev/null || true
}

# 某个 en 网卡是否为 Wi-Fi（按硬件端口名判断，跨机器通用）
is_wifi_iface() {
  local dev="$1" hw
  hw="$(find_hardware_port_by_device "$dev" 2>/dev/null || true)"
  [[ "$hw" == *"Wi-Fi"* || "$hw" == *"AirPort"* || "$hw" == *"Wireless"* ]]
}

# 自动挑选一张可用于 DHCP 的有线网卡（配置里的 INTERFACE 在本机不存在时用）。
# 优先级：active 的非 Wi-Fi 有线口 > 任意非 Wi-Fi 有线口（USB 网卡拔出前也在列）。
autodetect_interface() {
  local dev st best_active="" best_any=""
  while read -r dev; do
    [[ -z "$dev" || "$dev" == "lo0" ]] && continue
    case "$dev" in
      en*) ;; *) continue ;;                          # 只看 enX
    esac
    case "$dev" in
      awdl*|llw*) continue ;;                          # 苹果无线协处理接口
    esac
    is_wifi_iface "$dev" && continue                   # 跳过 Wi-Fi，避免抢正在上网的口
    st="$(ifconfig "$dev" 2>/dev/null | awk '/status:/{print $2; exit}')"
    [[ -z "$best_any" ]] && best_any="$dev"
    if [[ "$st" == "active" && -z "$best_active" ]]; then
      best_active="$dev"
    fi
  done < <(ifconfig -l | tr ' ' '\n')
  if [[ -n "$best_active" ]]; then echo "$best_active"; return 0; fi
  if [[ -n "$best_any" ]]; then echo "$best_any"; return 0; fi
  return 1
}

# 安全解析 network.env：逐行按 KEY=VALUE 处理，不用裸 source。
# 这样即使值含空格 / 斜杠（如 USB 10/100/1000 LAN）且未加引号，也不会被当命令执行。
parse_env_file() {
  local file="$1" line key val
  [[ -f "$file" ]] || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"                              # 去掉 Windows CRLF
    [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
    [[ "$line" != *=* ]] && continue
    key="${line%%=*}"
    val="${line#*=}"
    key="${key//[[:space:]]/}"                        # 去掉 key 周围空格
    [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
    val="${val#"${val%%[![:space:]]*}"}"              # ltrim
    val="${val%"${val##*[![:space:]]}"}"              # rtrim
    # 去掉成对的首尾引号（单/双）
    if [[ "$val" == \"*\" && ${#val} -ge 2 ]]; then val="${val:1:${#val}-2}"
    elif [[ "$val" == \'*\' && ${#val} -ge 2 ]]; then val="${val:1:${#val}-2}"
    fi
    printf -v "$key" '%s' "$val"
    export "$key"
  done < "$file"
}

load_env() {
  [[ -f "$ENV_FILE" ]] || { err "缺少 $ENV_FILE"; exit 1; }
  parse_env_file "$ENV_FILE"
  : "${SERVER_IP:?}" "${NETMASK:?}"
  : "${RANGE_START:?}" "${RANGE_END:?}" "${ROUTER:?}" "${DNS_SERVERS:?}"
  DOMAIN_NAME="${DOMAIN_NAME:-lan}"
  LEASE_TIME="${LEASE_TIME:-12h}"
  RUN_AT_LOAD="${RUN_AT_LOAD:-false}"
  ENABLE_DNS="${ENABLE_DNS:-false}"
  DNS_PORT="${DNS_PORT:-0}"
  LOG_DHCP="${LOG_DHCP:-true}"
  # 可选：手动指定「网络服务」名（如 UGREEN）；留空则自动按 INTERFACE 解析
  NETWORK_SERVICE="${NETWORK_SERVICE:-}"

  # 跨机器适配：配置里的 INTERFACE 在本机不存在时（如别的 Mac 没有 en5），
  # 自动挑一张有线网卡，并作废对不上的 NETWORK_SERVICE，避免安装/启动直接失败。
  if [[ -z "${INTERFACE:-}" ]] || ! ifconfig "${INTERFACE}" &>/dev/null; then
    local picked
    if picked="$(autodetect_interface)"; then
      if [[ -n "${INTERFACE:-}" ]]; then
        warn "配置网卡 ${INTERFACE} 在本机不存在，自动改用 ${picked}"
      else
        info "INTERFACE 未配置，自动选用 ${picked}"
      fi
      INTERFACE="$picked"
      NETWORK_SERVICE=""     # 换了网卡，旧服务名多半对不上，交给自动解析
    else
      : "${INTERFACE:?未配置 INTERFACE 且未探测到可用有线网卡；请插入 USB 网卡或运行 detect}"
    fi
  fi
  export INTERFACE NETWORK_SERVICE
}

dns_csv() {
  local out="" d
  for d in ${DNS_SERVERS}; do
    [[ -z "$out" ]] && out="$d" || out="${out},${d}"
  done
  echo "$out"
}

is_true() {
  local v; v="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')"
  [[ "$v" == "true" || "$v" == "1" || "$v" == "yes" ]]
}

service_loaded() { launchctl print "system/${LABEL}" &>/dev/null; }

write_conf() {
  local port_line="port=0" log_line="# log-dhcp off" conf
  conf="${1}"
  is_true "$ENABLE_DNS" && port_line="port=${DNS_PORT}"
  is_true "$LOG_DHCP" && log_line="log-dhcp"
  # 用 listen-address 绑定本机服务 IP，避免 USB 网卡 interface= 触发 unknown interface
  # DHCP 只会在与 dhcp-range 同网段的接口上响应；并禁止在 Wi-Fi(en0) 上提供 DHCP
  cat > "$conf" <<EOF
${port_line}
listen-address=${SERVER_IP}
bind-interfaces
except-interface=lo0
no-dhcp-interface=en0
no-dhcp-interface=awdl0
no-dhcp-interface=llw0
no-resolv
no-poll
domain=${DOMAIN_NAME}
dhcp-range=${RANGE_START},${RANGE_END},${NETMASK},${LEASE_TIME}
dhcp-option=option:router,${ROUTER}
dhcp-option=option:dns-server,$(dns_csv)
dhcp-option=option:domain-name,${DOMAIN_NAME}
dhcp-option=option:netmask,${NETMASK}
dhcp-authoritative
dhcp-hostsfile=${SYS_ETC}/dhcp-hosts.conf
dhcp-leasefile=${LEASES_DB}
pid-file=${PID_FILE}
${log_line}
log-facility=${SYS_LOG}/dnsmasq.log
user=root
EOF
}

write_plist() {
  local run_xml="<true/>"
  is_true "$RUN_AT_LOAD" || run_xml="<false/>"
  cat > "$1" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>${LABEL}</string>
	<key>ProgramArguments</key>
	<array>
		<string>${SYS_BIN}/dnsmasq</string>
		<string>--keep-in-foreground</string>
		<string>--conf-file=${SYS_ETC}/dnsmasq.conf</string>
	</array>
	<key>RunAtLoad</key>${run_xml}
	<key>KeepAlive</key><true/>
	<key>StandardOutPath</key><string>${SYS_LOG}/dnsmasq.out.log</string>
	<key>StandardErrorPath</key><string>${SYS_LOG}/dnsmasq.err.log</string>
	<key>WorkingDirectory</key><string>${SYS_VAR}</string>
</dict>
</plist>
EOF
}

# ---------- 网卡 / 网络服务名适配（跨机器） ----------
# macOS 里三者经常不一致：
#   Device          = en5          ← dnsmasq / ifconfig 用这个
#   Hardware Port   = USB 10/100…  ← listallhardwareports
#   Network Service = UGREEN       ← networksetup -setmanual 必须用这个

# 按 Device(enX) 从 listnetworkserviceorder 取「网络服务」名
find_net_service_by_device() {
  local want="$1" svc="" line dev
  while IFS= read -r line; do
    # "(1) UGREEN" / "(*) UGREEN"
    if [[ "$line" =~ ^\([0-9]+\)[[:space:]]+(.*)$ ]]; then
      svc="${BASH_REMATCH[1]}"
      svc="$(printf '%s' "$svc" | sed 's/^[*[:space:]]*//;s/[[:space:]]*$//')"
    elif [[ "$line" =~ ^\(\*\)[[:space:]]+(.*)$ ]]; then
      svc="${BASH_REMATCH[1]}"
      svc="$(printf '%s' "$svc" | sed 's/^[*[:space:]]*//;s/[[:space:]]*$//')"
    elif [[ "$line" == *"Device:"* ]]; then
      # 行如: (Hardware Port: USB ..., Device: en5)
      dev="$(printf '%s' "$line" | sed -n 's/.*Device:[[:space:]]*\([a-zA-Z0-9]*\).*/\1/p')"
      if [[ "$dev" == "$want" && -n "$svc" ]]; then
        echo "$svc"
        return 0
      fi
    fi
  done < <(networksetup -listnetworkserviceorder 2>/dev/null || true)
  return 1
}

# 按 Device 取 Hardware Port 名
# 用 awk 解析（bash 转义空格正则在部分系统上捕获组为空，不可靠）
find_hardware_port_by_device() {
  local want="$1" out
  out="$(networksetup -listallhardwareports 2>/dev/null | awk -v want="$want" '
    /^Hardware Port:/ { sub(/^Hardware Port:[ \t]*/, ""); port=$0 }
    /^Device:/        { if ($2==want) { print port; found=1; exit } }
    END { if (!found) exit 1 }
  ')" || return 1
  [[ -n "$out" ]] && { printf '%s\n' "$out"; return 0; }
  return 1
}

# 解析最终用于 networksetup 的服务名（多策略）
# 输出到 stdout；同时把候选列表打印到 stderr 便于日志
resolve_net_service() {
  local iface="$1"
  local candidates=() c hw

  # 1) 配置文件显式指定
  if [[ -n "${NETWORK_SERVICE:-}" ]]; then
    candidates+=("${NETWORK_SERVICE}")
  fi
  # 2) 网络服务名（UGREEN 等）— 正确做法
  if c="$(find_net_service_by_device "$iface")"; then
    candidates+=("$c")
  fi
  # 3) Hardware Port 名（部分机器二者相同，可作回退）
  if hw="$(find_hardware_port_by_device "$iface")"; then
    candidates+=("$hw")
  fi
  # 4) 直接用 Device 名碰运气（极少情况）
  candidates+=("$iface")

  # 去重并验证是否在 listallnetworkservices 里
  local seen="|" out="" s
  for s in "${candidates[@]}"; do
    [[ -z "$s" ]] && continue
    [[ "$seen" == *"|$s|"* ]] && continue
    seen="${seen}${s}|"
    if networksetup -listallnetworkservices 2>/dev/null | grep -Fxq "$s"; then
      echo "$s"
      return 0
    fi
    # 禁用的服务名带星号前缀列出时，去掉星号再比
    if networksetup -listallnetworkservices 2>/dev/null | sed 's/^\*//' | grep -Fxq "$s"; then
      echo "$s"
      return 0
    fi
    out="${out}${s}, "
  done

  # 全部未在服务列表中：仍返回第一个候选，交给 setmanual / ifconfig 回退
  if [[ ${#candidates[@]} -gt 0 && -n "${candidates[0]}" ]]; then
    echo "${candidates[0]}"
    return 0
  fi
  return 1
}

# 打印 Device ↔ 服务名 对照表（detect / install 用）
print_iface_map() {
  echo "【网卡映射 Device → 网络服务 → Hardware Port】"
  printf "%-8s %-22s %-28s %-16s %s\n" "IFACE" "NETWORK_SERVICE" "HARDWARE_PORT" "IPv4" "STATUS"
  printf "%-8s %-22s %-28s %-16s %s\n" "-----" "---------------" "-------------" "----" "------"
  local iface svc hw ip st
  while read -r iface; do
    [[ -z "$iface" || "$iface" == "lo0" ]] && continue
    # 跳过明显无关的虚拟口，减少噪音
    case "$iface" in
      gif*|stf*|anpi*|ap*|awdl*|llw*|utun*|bridge*|vmnet*|vnic*) continue ;;
    esac
    svc="$(find_net_service_by_device "$iface" 2>/dev/null || true)"
    hw="$(find_hardware_port_by_device "$iface" 2>/dev/null || true)"
    ip="$(ipconfig getifaddr "$iface" 2>/dev/null || echo -)"
    st="$(ifconfig "$iface" 2>/dev/null | awk '/status:/{print $2; exit}')"
    [[ -z "$st" ]] && st="?"
    printf "%-8s %-22s %-28s %-16s %s\n" \
      "$iface" "${svc:--}" "${hw:--}" "$ip" "$st"
  done < <(ifconfig -l | tr ' ' '\n')
  echo
  echo "说明: network.env 填 INTERFACE=enX（Device）；脚本会自动解析网络服务名。"
  echo "      若自动失败，可在 network.env 增加: NETWORK_SERVICE=\"服务名\""
}

set_static_ip() {
  local svc="" tried=() name ok_set=0
  local router_arg="$ROUTER"

  # networksetup -setmanual 的 router 若与本机同网段异常，仍尝试；失败再用 ifconfig
  info "为 ${INTERFACE} 配置静态 IP ${SERVER_IP}/${NETMASK}（网关 ${ROUTER}）"

  if svc="$(resolve_net_service "$INTERFACE")"; then
    info "解析到网络服务候选优先: 「${svc}」"
  fi

  # 依次尝试：显式配置、自动服务名、Hardware Port、Device
  local names=() n=""
  [[ -n "${NETWORK_SERVICE:-}" ]] && names+=("${NETWORK_SERVICE}")
  n="$(find_net_service_by_device "$INTERFACE" 2>/dev/null || true)"
  [[ -n "$n" ]] && names+=("$n")
  n="$(find_hardware_port_by_device "$INTERFACE" 2>/dev/null || true)"
  [[ -n "$n" ]] && names+=("$n")
  names+=("$INTERFACE")

  local seen="|"
  for name in "${names[@]}"; do
    [[ -z "$name" ]] && continue
    [[ "$seen" == *"|$name|"* ]] && continue
    seen="${seen}${name}|"
    tried+=("$name")
    info "尝试 networksetup -setmanual \"${name}\" ..."
    if networksetup -setmanual "$name" "$SERVER_IP" "$NETMASK" "$router_arg" 2>/dev/null; then
      ok "静态 IP 已设置（网络服务「${name}」→ ${SERVER_IP}/${NETMASK}）"
      ok_set=1
      break
    fi
    # 部分环境 router 参数敏感：再试一次用 SERVER_IP 当 router
    if [[ "$router_arg" != "$SERVER_IP" ]]; then
      if networksetup -setmanual "$name" "$SERVER_IP" "$NETMASK" "$SERVER_IP" 2>/dev/null; then
        ok "静态 IP 已设置（网络服务「${name}」→ ${SERVER_IP}/${NETMASK}）"
        warn "networksetup 使用的 router 临时为 SERVER_IP；DHCP 下发网关仍以 network.env 的 ROUTER 为准"
        ok_set=1
        break
      fi
    fi
  done

  # networksetup 在链路 inactive 时常常“成功”但不把 IP 挂到网卡上；
  # dnsmasq 要求接口上有地址，否则会报 unknown interface。必须再 ifconfig 一次。
  if ifconfig "$INTERFACE" inet "$SERVER_IP" netmask "$NETMASK" up 2>/dev/null; then
    ok "ifconfig ${INTERFACE} → ${SERVER_IP}/${NETMASK}"
  else
    warn "ifconfig 设置 ${INTERFACE} 失败"
    if [[ "$ok_set" -eq 0 ]]; then
      print_iface_map
      return 1
    fi
  fi

  sleep 0.3
  local now st
  now="$(ipconfig getifaddr "$INTERFACE" 2>/dev/null || true)"
  [[ -z "$now" ]] && now="$(ifconfig "$INTERFACE" 2>/dev/null | awk '/inet /{print $2; exit}')"
  st="$(ifconfig "$INTERFACE" 2>/dev/null | awk '/status:/{print $2; exit}')"
  if [[ "$now" == "$SERVER_IP" ]]; then
    ok "校验通过: ${INTERFACE} = ${now}（链路 ${st:-?}）"
  else
    warn "当前 ${INTERFACE} IPv4=${now:--}（期望 ${SERVER_IP}），链路=${st:-?}"
  fi
  if [[ "${st}" == "inactive" ]]; then
    warn "网线未接通（inactive）：服务或许能启动，但客户端要在插上网线后才能拿到地址"
  fi
  return 0
}


# 把系统租约同步到本目录 leases.txt（用户可读）
sync_leases_txt() {
  local tmp
  {
    echo "# dnsmasq 租约（已分配 IP）— 由 ./dhcp.sh leases 同步"
    echo "# 格式: <过期epoch> <MAC> <IP> <主机名> <客户端ID>"
    echo "# 源文件: ${LEASES_DB}"
    if [[ -f "$LEASES_DB" ]]; then
      cat "$LEASES_DB"
    else
      echo "# （尚无租约）"
    fi
  } > "${LEASES_TXT}.tmp" 2>/dev/null && mv -f "${LEASES_TXT}.tmp" "$LEASES_TXT" 2>/dev/null || true
}

cmd_detect() {
  echo "========================================"
  echo "  Mac DHCP — 网卡探测（跨机器适配）"
  echo "========================================"
  echo
  print_iface_map
  echo "【全部硬件端口原始信息】"
  networksetup -listallhardwareports 2>/dev/null || true
  echo
  echo "【网络服务列表】"
  networksetup -listallnetworkservices 2>/dev/null || true
  echo
  ok "dnsmasq: $(dnsmasq_bin)"
  if [[ -f "$ENV_FILE" ]]; then
    parse_env_file "$ENV_FILE"
    local mapped eff
    mapped="$(find_net_service_by_device "${INTERFACE:-}" 2>/dev/null || echo -)"
    echo
    echo "【当前 config/network.env】"
    echo "  INTERFACE=${INTERFACE:-（空）}  →  自动解析服务名: ${mapped:--}"
    echo "  NETWORK_SERVICE=${NETWORK_SERVICE:-（空=自动）}"
    echo "  SERVER_IP=${SERVER_IP:-}  池=${RANGE_START:-}~${RANGE_END:-}"
    # 若配置的网卡本机不存在，提示安装时会自动改用哪张
    if [[ -z "${INTERFACE:-}" ]] || ! ifconfig "${INTERFACE}" &>/dev/null; then
      if eff="$(autodetect_interface)"; then
        warn "配置网卡 ${INTERFACE:-（空）} 在本机不可用 → 安装时将自动改用 ${eff}"
      else
        warn "配置网卡 ${INTERFACE:-（空）} 不可用，且未探测到有线网卡；请插入 USB 网卡"
      fi
    fi
  fi
}

cmd_install() {
  need_root
  load_env

  # 安装前把即将下发给客户端的网段/地址池打印出来，避免用错网段而不自知。
  echo "========================================"
  echo "  即将安装并下发以下 DHCP 配置"
  echo "========================================"
  echo "  网卡 INTERFACE   : ${INTERFACE}"
  echo "  本机 SERVER_IP   : ${SERVER_IP}/${NETMASK}"
  echo "  地址池 RANGE     : ${RANGE_START} ~ ${RANGE_END}"
  echo "  下发网关 ROUTER  : ${ROUTER}"
  echo "  下发 DNS         : ${DNS_SERVERS}"
  echo "  域名 / 租约      : ${DOMAIN_NAME:-lan} / ${LEASE_TIME:-12h}"
  echo "----------------------------------------"
  echo "  确认这些值与目标网络一致；如需修改请编辑 config/network.env"
  echo "========================================"
  # 仅在交互式终端里等待确认；UI / 自动化(非 TTY)直接继续，避免卡住
  if [[ -t 0 ]]; then
    local reply
    read -r -p "确认无误并继续安装？[Y/n] " reply
    case "$reply" in
      ""|y|Y|yes|YES) ;;
      *) warn "已取消安装（未改动系统）"; exit 0 ;;
    esac
  fi

  local bin tmp_conf tmp_plist
  bin="$(dnsmasq_bin)"
  mkdir -p "$SYS_ETC" "$SYS_VAR" "$SYS_LOG" "$SYS_BIN"
  touch "$LEASES_DB"
  chmod 644 "$LEASES_DB"
  # 日志也给当前用户可读，方便排查
  touch "${SYS_LOG}/dnsmasq.log" "${SYS_LOG}/dnsmasq.err.log"
  chmod 644 "${SYS_LOG}/dnsmasq.log" "${SYS_LOG}/dnsmasq.err.log" 2>/dev/null || true
  sync_leases_txt
  [[ -f "$HOSTS_FILE" ]] || printf '# MAC,IP[,hostname]\n' > "$HOSTS_FILE"

  tmp_conf="$(mktemp)"
  tmp_plist="$(mktemp)"
  write_conf "$tmp_conf"
  write_plist "$tmp_plist"

  info "安装二进制"
  cp "$bin" "${SYS_BIN}/dnsmasq"
  chmod 755 "${SYS_BIN}/dnsmasq"
  # 跨机器拷贝(zip/AirDrop/下载)会打上隔离属性、或使签名失效；
  # macOS(尤其 Apple 芯片)会对无有效签名的二进制直接 SIGKILL(Killed: 9)。
  # 这里清隔离属性并补 adhoc 签名，避免 --version / 启动时被内核杀掉。
  heal_binary "${SYS_BIN}/dnsmasq"
  if ! "${SYS_BIN}/dnsmasq" --version 2>/dev/null | head -n1; then
    err "dnsmasq 无法执行(可能被 macOS 拦截/签名无效)。"
    err "已尝试自动清隔离属性并 adhoc 签名，仍失败。请手动执行："
    err "  sudo xattr -dr com.apple.quarantine \"${SYS_BIN}/dnsmasq\""
    err "  sudo codesign --force --sign - \"${SYS_BIN}/dnsmasq\""
    exit 1
  fi

  info "安装配置"
  cp "$tmp_conf" "${SYS_ETC}/dnsmasq.conf"
  cp "$HOSTS_FILE" "${SYS_ETC}/dhcp-hosts.conf"
  chmod 644 "${SYS_ETC}/dnsmasq.conf" "${SYS_ETC}/dhcp-hosts.conf"
  rm -f "$tmp_conf"

  echo
  print_iface_map
  set_static_ip || true

  info "注册 launchd"
  if service_loaded; then
    launchctl bootout "system/${LABEL}" 2>/dev/null || launchctl unload "$LAUNCHD" 2>/dev/null || true
  fi
  cp "$tmp_plist" "$LAUNCHD"
  rm -f "$tmp_plist"
  chown root:wheel "$LAUNCHD"
  chmod 644 "$LAUNCHD"
  # RunAtLoad=false 时只注册，不自动跑；避免旧错误配置反复 KeepAlive
  launchctl bootout "system/${LABEL}" 2>/dev/null || true
  launchctl enable "system/${LABEL}" 2>/dev/null || true
  launchctl bootstrap system "$LAUNCHD" 2>/dev/null || launchctl load "$LAUNCHD" 2>/dev/null || true

  # 清空旧错误日志，避免干扰排查
  : > "${SYS_LOG}/dnsmasq.err.log" 2>/dev/null || true

  ok "安装完成 → sudo $0 开启"
  warn "局域网内请勿同时存在其他 DHCP 服务器"
  if ! ifconfig "$INTERFACE" 2>/dev/null | grep -q 'status: active'; then
    warn "网卡 ${INTERFACE} 当前 inactive：请插上网线后再开启"
  fi
}

cmd_start() {
  need_root
  [[ -f "$LAUNCHD" ]] || { err "请先: sudo $0 install"; exit 1; }
  load_env

  # 启动前检查网卡是否存在（USB 未插时 en5 会消失 → dnsmasq: unknown interface）
  if ! ifconfig "$INTERFACE" &>/dev/null; then
    err "网卡 ${INTERFACE} 当前不存在"
    err "请插入 USB 网卡，或在界面「识别到的网卡」里改选后：保存 → 安装 → 开启"
    ifconfig -l | tr ' ' '\n' | grep -E '^en' | sed 's/^/  可见: /' || true
    exit 1
  fi
  ifconfig "$INTERFACE" up 2>/dev/null || true

  # 刷新系统配置（应用最新 network.env，并改用 bind-dynamic）
  mkdir -p "$SYS_ETC" "$SYS_VAR" "$SYS_LOG"
  local tmp_conf
  tmp_conf="$(mktemp)"
  write_conf "$tmp_conf"
  cp "$tmp_conf" "${SYS_ETC}/dnsmasq.conf"
  chmod 644 "${SYS_ETC}/dnsmasq.conf"
  rm -f "$tmp_conf"
  if [[ -f "$HOSTS_FILE" ]]; then
    cp "$HOSTS_FILE" "${SYS_ETC}/dhcp-hosts.conf"
  fi

  # 尽量保证服务网卡有 SERVER_IP（必须挂到内核，不能只靠 networksetup）
  set_static_ip || true
  local cur_ip
  cur_ip="$(ifconfig "$INTERFACE" 2>/dev/null | awk '/inet /{print $2; exit}')"
  if [[ "$cur_ip" != "$SERVER_IP" ]]; then
    err "无法在 ${INTERFACE} 上设置 ${SERVER_IP}（当前=${cur_ip:--}）"
    err "请检查 USB 网卡是否识别；可用: ifconfig ${INTERFACE}"
    exit 1
  fi

  touch "$LEASES_DB"
  chmod 644 "$LEASES_DB"
  : > "${SYS_LOG}/dnsmasq.err.log" 2>/dev/null || true

  # 先停掉可能在崩溃循环的旧进程
  if service_loaded; then
    launchctl bootout "system/${LABEL}" 2>/dev/null || launchctl unload "$LAUNCHD" 2>/dev/null || true
  fi
  pgrep -lf dnsmasq 2>/dev/null | grep -q "${SYS_ETC}/dnsmasq.conf" \
    && pkill -f "dnsmasq.*${SYS_ETC}/dnsmasq.conf" 2>/dev/null || true
  sleep 0.3

  launchctl bootstrap system "$LAUNCHD" 2>/dev/null || launchctl load "$LAUNCHD" 2>/dev/null || true
  launchctl kickstart -k "system/${LABEL}" 2>/dev/null || true

  sleep 1
  if pgrep -x dnsmasq >/dev/null; then
    ok "已启动"
    sync_leases_txt
    cmd_status
  else
    err "启动失败，见 ${SYS_LOG}/dnsmasq.err.log"
    if grep -q "unknown interface" "${SYS_LOG}/dnsmasq.err.log" 2>/dev/null; then
      err "原因: 网卡名不可见。请插稳 USB，执行 ifconfig 确认 ${INTERFACE} 存在后重试"
    fi
    if grep -qiE "address already in use|failed to bind|cannot bind" "${SYS_LOG}/dnsmasq.err.log" 2>/dev/null; then
      err "原因: 无法绑定 ${SERVER_IP}。请确认 ifconfig ${INTERFACE} 已有该地址，且 67 端口未被占用"
    fi
    tail -n 20 "${SYS_LOG}/dnsmasq.err.log" 2>/dev/null || true
    exit 1
  fi
}

cmd_stop() {
  need_root
  service_loaded && { launchctl bootout "system/${LABEL}" 2>/dev/null || launchctl unload "$LAUNCHD" 2>/dev/null || true; }
  if [[ -f "$PID_FILE" ]]; then
    kill "$(cat "$PID_FILE")" 2>/dev/null || true
  fi
  pgrep -lf dnsmasq 2>/dev/null | grep -q "${SYS_ETC}/dnsmasq.conf" \
    && pkill -f "dnsmasq.*${SYS_ETC}/dnsmasq.conf" 2>/dev/null || true
  ok "已停止"
}

cmd_status() {
  sync_leases_txt
  echo "=== Mac DHCP (dnsmasq) ==="
  [[ -x "${SYS_BIN}/dnsmasq" ]] && ok "二进制 ${SYS_BIN}/dnsmasq" || warn "未安装二进制"
  [[ -f "$LAUNCHD" ]] && ok "LaunchDaemon 已安装" || warn "未安装服务"
  service_loaded && ok "launchd 已加载" || warn "launchd 未加载"
  if pgrep -x dnsmasq >/dev/null; then
    ok "dnsmasq 运行中"
    pgrep -lf dnsmasq | sed 's/^/  /'
  else
    warn "dnsmasq 未运行"
  fi
  if [[ -f "$ENV_FILE" ]]; then
    parse_env_file "$ENV_FILE"
    echo "网卡 ${INTERFACE}: $(ipconfig getifaddr "$INTERFACE" 2>/dev/null || echo -)  ($(ifconfig "$INTERFACE" 2>/dev/null | awk '/status:/{print $2; exit}'))"
  fi
  echo "租约库: ${LEASES_DB}"
  echo "可读副本: ${LEASES_TXT}"
  for f in "${SYS_LOG}/dnsmasq.err.log" "${SYS_LOG}/dnsmasq.log"; do
    [[ -f "$f" && -s "$f" ]] && { echo "日志 $f:"; tail -n 3 "$f" | sed 's/^/  /'; }
  done
}

cmd_leases() {
  sync_leases_txt
  echo "租约库: ${LEASES_DB}"
  echo "已同步到: ${LEASES_TXT}"
  if [[ ! -f "$LEASES_DB" ]] || ! grep -qE '^[0-9]' "$LEASES_DB" 2>/dev/null; then
    warn "尚无租约（客户端获取地址后会出现）"
    exit 0
  fi
  printf "%-16s %-20s %-12s %s\n" "IP" "MAC" "HOST" "EXPIRY"
  awk 'NF>=4 && $1!~/^#/{printf "%-16s %-20s %-12s %s\n",$3,$2,$4,$1}' "$LEASES_DB"
}

cmd_uninstall() {
  need_root
  cmd_stop 2>/dev/null || true
  rm -f "$LAUNCHD"
  if [[ "${1:-}" == "--purge" ]]; then
    rm -rf "$SYS_ETC" "$SYS_VAR" "$SYS_LOG"
    [[ -f "${SYS_BIN}/dnsmasq" && ! -L "${SYS_BIN}/dnsmasq" ]] && rm -f "${SYS_BIN}/dnsmasq"
    ok "已清除系统文件"
  else
    warn "配置仍保留在 ${SYS_ETC}（加 --purge 可清除）"
  fi
  ok "卸载完成"
}

cmd_debug() {
  need_root
  load_env
  local bin conf
  bin="$(dnsmasq_bin)"
  heal_binary "$bin"                 # 直接跑打包的二进制，先清隔离属性+补签名，防 Killed:9
  conf="${SYS_ETC}/dnsmasq.conf"
  [[ -f "$conf" ]] || { conf="$(mktemp)"; write_conf "$conf"; }
  warn "请先 stop；Ctrl+C 退出"
  exec "$bin" --no-daemon --log-queries --conf-file="$conf"
}

cmd_setip() {
  need_root
  load_env
  echo "========================================"
  echo "  为网卡配置静态 IP"
  echo "========================================"
  echo "  网卡:     ${INTERFACE}"
  echo "  IP:       ${SERVER_IP}"
  echo "  掩码:     ${NETMASK}"
  echo "  网关:     ${ROUTER}"
  echo

  if ! ifconfig "$INTERFACE" &>/dev/null; then
    err "网卡 ${INTERFACE} 不存在，请插入 USB 网卡或修改 INTERFACE"
    print_iface_map
    exit 1
  fi

  if set_static_ip; then
    echo
    ifconfig "$INTERFACE" | awk '/inet |status:|ether /{print "  "$0}'
    local st
    st="$(ifconfig "$INTERFACE" 2>/dev/null | awk '/status:/{print $2; exit}')"
    if [[ "$st" == "inactive" ]]; then
      warn "链路仍为 inactive：请插上网线；IP 已写入网卡，接通后即可用"
    fi
    ok "网卡 IP 配置完成"
  else
    err "网卡 IP 配置失败"
    exit 1
  fi
}

cmd_ui() {
  local py="" server="${ROOT}/ui/server.py" url="http://127.0.0.1:8787/"
  if command -v python3 >/dev/null 2>&1; then
    py="python3"
  elif command -v python >/dev/null 2>&1; then
    py="python"
  else
    err "需要 python3 才能启动可视化界面"
    exit 1
  fi
  [[ -f "$server" ]] || { err "未找到 $server"; exit 1; }

  # 已在跑：只打开浏览器，避免端口冲突
  if curl -fsS --max-time 1 "$url" >/dev/null 2>&1 \
    || nc -z 127.0.0.1 8787 >/dev/null 2>&1; then
    info "控制台已在运行 → $url"
    open "$url" >/dev/null 2>&1 || true
    return 0
  fi

  info "启动可视化控制台 → $url"
  exec "$py" "$server"
}

usage() {
  cat <<EOF
用法: $0 <命令>

  ui / 界面            打开可视化控制台（浏览器）
  detect              探测网卡
  setip / 配IP        给 INTERFACE 配置 SERVER_IP（需 sudo）
  install             安装二进制 + 配置 + launchd（需 sudo）
  开启 / on / start   开启 DHCP 服务（需 sudo）
  关闭 / off / stop   关闭 DHCP 服务（需 sudo）
  status              查看状态
  leases              查看租约
  debug               前台调试（需 sudo）
  uninstall [--purge] 卸载（--purge 清除系统文件）

编辑配置: config/network.env
静态绑定: config/dhcp-hosts.conf   # 格式 MAC,IP[,hostname]

示例:
  $0 ui
  sudo $0 setip
  sudo $0 开启
  sudo $0 关闭
EOF
}

# 每次运行先清整包隔离属性（对已能启动本脚本的情况有效）
heal_bundle

case "${1:-}" in
  ui|界面|gui|web)     cmd_ui ;;
  detect)              cmd_detect ;;
  setip|配IP|set-ip)   cmd_setip ;;
  install)             cmd_install ;;
  开启|on|start|--on|--start)   cmd_start ;;
  关闭|关机|off|stop|--off|--stop) cmd_stop ;;
  status)              cmd_status ;;
  leases)              cmd_leases ;;
  debug)               cmd_debug ;;
  uninstall)           shift; cmd_uninstall "${1:-}" ;;
  *)                   usage; exit 1 ;;
esac

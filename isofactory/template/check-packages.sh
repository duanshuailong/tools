#!/bin/bash
# ============================================================================
# check-packages.sh
# 检测本机安装了镜像清单里的哪些工具包 / GPU 驱动栈，哪些没装。
# 基于 template/README.md 第 4、5 节的采集清单。
# 只读检测，不改动系统。用法：sudo bash check-packages.sh
#        只看缺失：bash check-packages.sh --missing
# ============================================================================

ONLY_MISSING=0
[ "$1" = "--missing" ] && ONLY_MISSING=1

GREEN=$'\033[32m'; RED=$'\033[31m'; YEL=$'\033[33m'; CYA=$'\033[36m'; RST=$'\033[0m'
# 无 tty 时关掉颜色
[ -t 1 ] || { GREEN=; RED=; YEL=; CYA=; RST=; }

TOTAL=0; OK=0; MISS=0
MISSING_LIST=""

hdr() { echo; echo "${CYA}==== $* ====${RST}"; }

# 检查一个 apt 包是否已安装（真正 installed，不含 rc 残留）
check_pkg() {
  local pkg="$1"
  TOTAL=$((TOTAL+1))
  local st ver
  st=$(dpkg-query -W -f='${db:Status-Status}' "$pkg" 2>/dev/null)
  if [ "$st" = "installed" ]; then
    ver=$(dpkg-query -W -f='${Version}' "$pkg" 2>/dev/null)
    OK=$((OK+1))
    [ "$ONLY_MISSING" = "0" ] && printf "  ${GREEN}[✓]${RST} %-32s %s\n" "$pkg" "$ver"
  else
    MISS=$((MISS+1))
    MISSING_LIST="$MISSING_LIST $pkg"
    printf "  ${RED}[✗]${RST} %-32s ${YEL}未安装${RST}\n" "$pkg"
  fi
}

# 批量检查一组包
check_group() { for p in "$@"; do check_pkg "$p"; done; }

# ── 4.1 通用包 ──────────────────────────────────────────────────────────
hdr "4.1 通用包（编译/网络/服务/自动化）"
check_group \
  build-essential make gcc debhelper bison flex dkms pkg-config libtool gfortran \
  cron dmidecode dnsutils git ipmitool iputils-ping lldpd net-tools openssh-server \
  rsyslog tcpdump vim conntrack linux-tools-common hwdata kdump-tools \
  nfs-kernel-server nfs-common squid tuned dos2unix numactl \
  keyutils rpcbind unzip isc-dhcp-client socat sshpass docker.io chrony

# ── 4.2 内核专属包 ──────────────────────────────────────────────────────
hdr "4.2 内核专属包（跟随当前内核 $(uname -r)）"
check_pkg "linux-tools-$(uname -r)"
check_pkg "linux-headers-$(uname -r)"

# ── 4.3 运维补充包（存储/硬件/监控排障）────────────────────────────────
hdr "4.3 运维补充包（存储/硬件/网络测试/监控）"
check_group \
  mdadm lvm2 parted gdisk xfsprogs e2fsprogs nvme-cli smartmontools hdparm multipath-tools \
  pciutils usbutils ethtool lm-sensors \
  htop iotop sysstat lsof strace ltrace gdb stress-ng \
  traceroute mtr iperf3 nmap netcat-openbsd bridge-utils vlan \
  rsync tree ncdu pigz zip p7zip-full pv \
  tmux screen curl wget jq bash-completion expect \
  python3-pip python3-venv cmake automake autoconf \
  acl uuid-runtime

# ── 4.4 OFED 编译依赖 ───────────────────────────────────────────────────
hdr "4.4 OFED 编译依赖（debs/mellanox）"
check_group quilt tk graphviz swig libfuse2t64 chrpath libnl-route-3-dev libnl-3-dev

# ── 4.5 其他可选补充 ────────────────────────────────────────────────────
hdr "4.5 其他可选补充"
check_group \
  ca-certificates gnupg apt-transport-https software-properties-common \
  efibootmgr dosfstools mtools grub2-common \
  psmisc moreutils parallel at logrotate needrestart whois file lsb-release \
  lsscsi sg3-utils iftop nethogs nload bmon dstat nmon memtester freeipmi-tools \
  cifs-utils sshfs autofs nftables iptables \
  python3-dev python3-setuptools valgrind ccache ninja-build meson clang

# ── GPU / 驱动栈（不完全是 apt 包，单独探测）───────────────────────────
hdr "GPU / 驱动栈（特殊检测）"

# NVIDIA 驱动
if command -v nvidia-smi >/dev/null 2>&1; then
  drv=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1)
  if [ -n "$drv" ]; then
    printf "  ${GREEN}[✓]${RST} %-32s driver=%s\n" "NVIDIA 驱动" "$drv"
  else
    printf "  ${YEL}[!]${RST} %-32s nvidia-smi 在但无法查询（模块未加载？）\n" "NVIDIA 驱动"
  fi
else
  printf "  ${RED}[✗]${RST} %-32s ${YEL}nvidia-smi 未找到${RST}\n" "NVIDIA 驱动"
fi

# 内核模块是否加载
if lsmod 2>/dev/null | grep -q "^nvidia"; then
  printf "  ${GREEN}[✓]${RST} %-32s %s\n" "nvidia 内核模块" "$(modinfo nvidia 2>/dev/null | awk -F': *' '/^version/{print $2; exit}')"
  # open 还是 proprietary
  lic=$(modinfo nvidia 2>/dev/null | awk -F': *' '/^license/{print $2; exit}')
  printf "      模块类型 license: %s %s\n" "$lic" "$(echo "$lic" | grep -qi GPL && echo '(open)' || echo '(proprietary)')"
else
  printf "  ${RED}[✗]${RST} %-32s ${YEL}未加载${RST}\n" "nvidia 内核模块"
fi

# CUDA toolkit
if command -v nvcc >/dev/null 2>&1; then
  printf "  ${GREEN}[✓]${RST} %-32s %s\n" "CUDA toolkit (nvcc)" "$(nvcc --version 2>/dev/null | awk '/release/{print $5,$6}' | tr -d ,)"
elif [ -d /usr/local/cuda ]; then
  printf "  ${YEL}[!]${RST} %-32s /usr/local/cuda 存在但 nvcc 不在 PATH\n" "CUDA toolkit"
else
  printf "  ${RED}[✗]${RST} %-32s ${YEL}未安装${RST}\n" "CUDA toolkit"
fi

# Fabric Manager（服务 + 包）
if systemctl list-unit-files 2>/dev/null | grep -q nvidia-fabricmanager; then
  fmst=$(systemctl is-active nvidia-fabricmanager 2>/dev/null)
  fmver=$(dpkg-query -W -f='${Version}' nvidia-fabricmanager 2>/dev/null)
  printf "  ${GREEN}[✓]${RST} %-32s ver=%s service=%s\n" "nvidia-fabricmanager" "${fmver:-?}" "$fmst"
else
  printf "  ${RED}[✗]${RST} %-32s ${YEL}未安装（无 service 单元）${RST}\n" "nvidia-fabricmanager"
fi

# 其余 NVIDIA / DCGM apt 包
for p in nvidia-fabricmanager-dev nvlsm nvidia-container-toolkit nvidia-container-toolkit-base \
         libnvidia-container1 libnvidia-container-tools; do
  st=$(dpkg-query -W -f='${db:Status-Status}' "$p" 2>/dev/null)
  if [ "$st" = "installed" ]; then
    printf "  ${GREEN}[✓]${RST} %-32s %s\n" "$p" "$(dpkg-query -W -f='${Version}' "$p" 2>/dev/null)"
  else
    printf "  ${RED}[✗]${RST} %-32s ${YEL}未安装${RST}\n" "$p"
  fi
done
# DCGM 和 nscq 包名带版本后缀，用通配匹配
dcgm=$(dpkg-query -W -f='${Package} ${Version}\n' 'datacenter-gpu-manager*' 2>/dev/null | grep -v 'no packages' | head -1)
[ -n "$dcgm" ] && printf "  ${GREEN}[✓]${RST} %-32s %s\n" "DCGM" "$dcgm" || printf "  ${RED}[✗]${RST} %-32s ${YEL}未安装${RST}\n" "DCGM"
nscq=$(dpkg-query -W -f='${Package} ${Version}\n' 'libnvidia-nscq*' 2>/dev/null | grep -v 'no packages' | head -1)
[ -n "$nscq" ] && printf "  ${GREEN}[✓]${RST} %-32s %s\n" "libnvidia-nscq" "$nscq" || printf "  ${RED}[✗]${RST} %-32s ${YEL}未安装${RST}\n" "libnvidia-nscq"

# OFED / MLNX
hdr "InfiniBand / OFED"
if command -v ofed_info >/dev/null 2>&1; then
  printf "  ${GREEN}[✓]${RST} %-32s %s\n" "MLNX_OFED" "$(ofed_info -s 2>/dev/null)"
else
  printf "  ${RED}[✗]${RST} %-32s ${YEL}ofed_info 未找到${RST}\n" "MLNX_OFED"
fi
if command -v ibstat >/dev/null 2>&1; then
  printf "  ${GREEN}[✓]${RST} %-32s\n" "ibstat 可用（IB 工具已装）"
else
  printf "  ${RED}[✗]${RST} %-32s ${YEL}未安装${RST}\n" "IB 工具 (ibstat)"
fi
# OFED 内核模块是否为当前内核编译（前面踩过的坑）
if find /usr/src -name Module.symvers -path '*ofa*' 2>/dev/null | grep -q .; then
  printf "  ${GREEN}[✓]${RST} %-32s\n" "OFED 内核模块 Module.symvers 存在"
else
  printf "  ${YEL}[!]${RST} %-32s ${YEL}缺失（NVIDIA 驱动会因此编译失败）${RST}\n" "OFED Module.symvers"
fi

# ── 汇总 ────────────────────────────────────────────────────────────────
hdr "汇总"
echo "  apt 包检查总数: $TOTAL   已装: ${GREEN}$OK${RST}   未装: ${RED}$MISS${RST}"
if [ "$MISS" -gt 0 ]; then
  echo
  echo "  ${YEL}未安装的 apt 包（可直接复制安装）：${RST}"
  echo "  sudo apt install -y$MISSING_LIST"
fi
echo

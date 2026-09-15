#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Mac DHCP 本地可视化控制台（仅标准库，无额外依赖）"""
from __future__ import print_function

import json
import os
import platform
import re
import shutil
import subprocess
import sys
import time
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse

UI_DIR = Path(__file__).resolve().parent
ROOT = UI_DIR.parent
DHCP_SH = ROOT / "dhcp.sh"
ENV_FILE = ROOT / "config" / "network.env"
HOSTS_FILE = ROOT / "config" / "dhcp-hosts.conf"
LEASES_DB = Path("/usr/local/var/mac-dhcp/dnsmasq.leases")
LEASES_TXT = ROOT / "leases.txt"
SYS_CONF = Path("/usr/local/etc/mac-dhcp/dnsmasq.conf")
LAUNCHD = Path("/Library/LaunchDaemons/com.local.mac-dhcp.plist")
LOG_ERR = Path("/usr/local/var/log/mac-dhcp/dnsmasq.err.log")
LOG_DHCP = Path("/usr/local/var/log/mac-dhcp/dnsmasq.log")
# 提权执行时不能直接跑 Desktop 上的脚本（TCC 会 Operation not permitted）
RUN_BUNDLE = Path("/tmp/mac-dhcp-ui-bundle")
HOST = "127.0.0.1"
PORT = 8787


def run(cmd, timeout=60):
    try:
        p = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=timeout,
        )
        return p.returncode, p.stdout or ""
    except Exception as e:
        return 1, str(e)


def sync_run_bundle():
    """把 dhcp.sh / config / bin 同步到 /tmp，供管理员权限脚本执行（避开 Desktop TCC）。"""
    if RUN_BUNDLE.exists():
        shutil.rmtree(RUN_BUNDLE, ignore_errors=True)
    RUN_BUNDLE.mkdir(parents=True, exist_ok=True)

    shutil.copy2(DHCP_SH, RUN_BUNDLE / "dhcp.sh")
    os.chmod(RUN_BUNDLE / "dhcp.sh", 0o755)

    shutil.copytree(ROOT / "config", RUN_BUNDLE / "config")

    arch = platform.machine()
    src = ROOT / "bin" / arch / "dnsmasq"
    dst_dir = RUN_BUNDLE / "bin" / arch
    dst_dir.mkdir(parents=True, exist_ok=True)
    if src.exists():
        shutil.copy2(src, dst_dir / "dnsmasq")
        os.chmod(dst_dir / "dnsmasq", 0o755)
    # 另一架构也拷一份，换机器更稳
    other = "x86_64" if arch == "arm64" else "arm64"
    src2 = ROOT / "bin" / other / "dnsmasq"
    if src2.exists():
        d2 = RUN_BUNDLE / "bin" / other
        d2.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src2, d2 / "dnsmasq")
        os.chmod(d2 / "dnsmasq", 0o755)

    return RUN_BUNDLE / "dhcp.sh"


def run_privileged(args):
    """执行 dhcp.sh 子命令；非 root 时用 macOS 管理员授权弹窗。"""
    # 始终从 /tmp 副本执行，避免 Desktop/文稿 被 TCC 拦截
    script = sync_run_bundle()
    if os.geteuid() == 0:
        return run(["/bin/bash", str(script)] + list(args), timeout=120)

    # 参数必须 ensure_ascii=False，否则中文会变成 \uXXXX，bash 认不到
    cmd_line = "cd / && /bin/bash %s %s" % (
        json.dumps(str(script), ensure_ascii=False),
        " ".join(json.dumps(a, ensure_ascii=False) for a in args),
    )
    osa = [
        "osascript",
        "-e",
        "do shell script %s with administrator privileges"
        % json.dumps(cmd_line, ensure_ascii=False),
    ]
    return run(osa, timeout=180)


def load_env():
    env = {}
    if not ENV_FILE.exists():
        return env
    for line in ENV_FILE.read_text(encoding="utf-8", errors="replace").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        v = v.strip().strip('"').strip("'")
        env[k.strip()] = v
    return env


def save_env(updates):
    """更新 network.env 中已有键；保留注释与顺序。"""
    allowed = {
        "INTERFACE",
        "NETWORK_SERVICE",
        "SERVER_IP",
        "NETMASK",
        "NET_ADDRESS",
        "RANGE_START",
        "RANGE_END",
        "ROUTER",
        "DNS_SERVERS",
        "DOMAIN_NAME",
        "LEASE_TIME",
        "RUN_AT_LOAD",
        "ENABLE_DNS",
        "DNS_PORT",
        "LOG_DHCP",
    }
    updates = {k: str(v) for k, v in updates.items() if k in allowed}
    if not ENV_FILE.exists():
        raise FileNotFoundError(str(ENV_FILE))

    lines = ENV_FILE.read_text(encoding="utf-8", errors="replace").splitlines()
    out = []
    seen = set()
    for line in lines:
        raw = line
        s = line.strip()
        if s and not s.startswith("#") and "=" in s:
            k = s.split("=", 1)[0].strip()
            if k in updates:
                val = updates[k]
                if k == "DNS_SERVERS" and not (val.startswith('"') or val.startswith("'")):
                    val = '"%s"' % val
                out.append("%s=%s" % (k, val))
                seen.add(k)
                continue
        out.append(raw)
    for k, val in updates.items():
        if k not in seen:
            if k == "DNS_SERVERS" and not (val.startswith('"') or val.startswith("'")):
                val = '"%s"' % val
            out.append("%s=%s" % (k, val))
    ENV_FILE.write_text("\n".join(out) + "\n", encoding="utf-8")


def iface_info(name):
    if not name:
        return {"ip": "-", "status": "?", "mac": "-"}
    code, out = run(["ifconfig", name])
    ip = "-"
    status = "?"
    mac = "-"
    m = re.search(r"inet (\d+\.\d+\.\d+\.\d+)", out)
    if m:
        ip = m.group(1)
    m = re.search(r"status:\s*(\w+)", out)
    if m:
        status = m.group(1)
    m = re.search(r"ether ([0-9a-f:]+)", out, re.I)
    if m:
        mac = m.group(1)
    return {"ip": ip, "status": status, "mac": mac}


SKIP_IFACES = re.compile(
    r"^(lo\d*|gif\d*|stf\d*|anpi\d*|ap\d*|awdl\d*|llw\d*|utun\d*|bridge\d*|vmnet\d*|vnic\d*)$"
)


def parse_hardware_ports():
    """Device -> Hardware Port 名"""
    code, out = run(["networksetup", "-listallhardwareports"])
    mapping = {}
    port = ""
    for line in out.splitlines():
        if line.startswith("Hardware Port:"):
            port = line.split(":", 1)[1].strip()
        elif line.startswith("Device:"):
            dev = line.split(":", 1)[1].strip()
            if port and dev:
                mapping[dev] = port
            port = ""
    return mapping


def parse_network_services():
    """Device -> 网络服务名（如 UGREEN）"""
    code, out = run(["networksetup", "-listnetworkserviceorder"])
    mapping = {}
    svc = ""
    for line in out.splitlines():
        m = re.match(r"^\((\d+|\*)\)\s+(.*)$", line.strip())
        if m:
            svc = m.group(2).strip().lstrip("* ").strip()
            continue
        if "Device:" in line and svc:
            m = re.search(r"Device:\s*([A-Za-z0-9]+)", line)
            if m:
                mapping[m.group(1)] = svc
    return mapping


def list_interfaces():
    """可供配置的网卡列表（含映射）"""
    code, out = run(["ifconfig", "-l"])
    if code != 0:
        return []
    hw = parse_hardware_ports()
    svc_map = parse_network_services()
    env = load_env()
    current = env.get("INTERFACE", "")
    rows = []
    for name in out.split():
        if SKIP_IFACES.match(name):
            continue
        info = iface_info(name)
        service = svc_map.get(name, "")
        hardware = hw.get(name, "")
        # 无硬件端口且无服务名的虚拟口再过滤一层
        if not service and not hardware and info["status"] in ("?", ""):
            continue
        recommend = bool(service) and "Wi-Fi" not in (service + hardware) and "Wi-Fi" not in hardware
        rows.append(
            {
                "iface": name,
                "service": service or "-",
                "hardware": hardware or "-",
                "ip": info["ip"],
                "mac": info["mac"],
                "status": info["status"],
                "selected": name == current,
                "recommend": recommend and info["status"] == "active",
            }
        )
    # active 优先，其次有服务名
    def sort_key(r):
        return (
            0 if r["status"] == "active" else 1,
            0 if r["service"] != "-" else 1,
            r["iface"],
        )

    rows.sort(key=sort_key)
    return rows


def dnsmasq_running():
    code, out = run(["pgrep", "-x", "dnsmasq"])
    return code == 0 and bool(out.strip())


def parse_leases():
    path = LEASES_DB if LEASES_DB.exists() else LEASES_TXT
    rows = []
    if not path.exists():
        return rows
    try:
        text = path.read_text(encoding="utf-8", errors="replace")
    except PermissionError:
        code, text = run(["cat", str(path)])
        if code != 0:
            return rows
    now = int(time.time())
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split()
        if len(parts) < 4 or not parts[0].isdigit():
            continue
        exp = int(parts[0])
        rows.append(
            {
                "expiry": exp,
                "expiry_text": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(exp)),
                "mac": parts[1],
                "ip": parts[2],
                "hostname": parts[3] if parts[3] != "*" else "",
                "active": exp > now,
            }
        )
    rows.sort(key=lambda r: r["ip"])
    return rows


def strip_ansi(text):
    return re.sub(r"\x1b\[[0-9;]*m", "", text or "")


def read_log_tail(path, n=40):
    if not path.exists():
        return ""
    try:
        lines = path.read_text(encoding="utf-8", errors="replace").splitlines()
    except PermissionError:
        code, out = run(["tail", "-n", str(n), str(path)])
        return out if code == 0 else ""
    return "\n".join(lines[-n:])


def get_status():
    env = load_env()
    iface = env.get("INTERFACE", "")
    server_ip = env.get("SERVER_IP", "")
    info = iface_info(iface)
    leases = parse_leases()
    # ifconfig -l 判断网卡是否存在
    code, all_if = run(["ifconfig", "-l"])
    iface_exists = bool(iface) and iface in all_if.split()
    ip_ok = bool(server_ip) and info["ip"] == server_ip
    warnings = []
    if not iface:
        warnings.append("未配置 INTERFACE")
    elif not iface_exists:
        warnings.append("网卡 %s 不存在：请插入 USB 网卡，或在下方列表改选后保存并安装" % iface)
    elif info["status"] == "inactive":
        warnings.append("链路 inactive（未插网线）：可尝试开启服务，但客户端需插线后才能拿到地址")
    if iface_exists and server_ip and not ip_ok:
        warnings.append(
            "网卡实际 IP 为 %s，与 SERVER_IP %s 不一致：请点「开启」让脚本自动 ifconfig，或先「安装 / 应用配置」"
            % (info["ip"], server_ip)
        )
    return {
        "ok": True,
        "running": dnsmasq_running(),
        "installed": LAUNCHD.exists() and SYS_CONF.exists(),
        "interface": iface,
        "iface_exists": iface_exists,
        "iface_ip": info["ip"],
        "iface_status": info["status"] if iface_exists else "missing",
        "iface_mac": info["mac"],
        "server_ip": env.get("SERVER_IP", ""),
        "ip_ok": ip_ok,
        "range": "%s ~ %s" % (env.get("RANGE_START", ""), env.get("RANGE_END", "")),
        "router": env.get("ROUTER", ""),
        "dns": env.get("DNS_SERVERS", ""),
        "lease_count": len(leases),
        "active_leases": sum(1 for r in leases if r["active"]),
        "warnings": warnings,
        "env": env,
        "ifaces": list_interfaces(),
        "time": time.strftime("%Y-%m-%d %H:%M:%S"),
    }


class Handler(SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(UI_DIR), **kwargs)

    def log_message(self, fmt, *args):
        sys.stderr.write("[ui] " + (fmt % args) + "\n")

    def _json(self, code, obj):
        data = json.dumps(obj, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def _read_json(self):
        n = int(self.headers.get("Content-Length") or 0)
        if n <= 0:
            return {}
        raw = self.rfile.read(n).decode("utf-8", errors="replace")
        return json.loads(raw) if raw else {}

    def do_GET(self):
        path = urlparse(self.path).path
        if path == "/api/status":
            return self._json(200, get_status())
        if path == "/api/ifaces":
            return self._json(200, {"ok": True, "ifaces": list_interfaces()})
        if path == "/api/leases":
            return self._json(200, {"ok": True, "leases": parse_leases()})
        if path == "/api/config":
            hosts = ""
            if HOSTS_FILE.exists():
                hosts = HOSTS_FILE.read_text(encoding="utf-8", errors="replace")
            return self._json(
                200,
                {
                    "ok": True,
                    "env": load_env(),
                    "hosts": hosts,
                    "env_path": str(ENV_FILE),
                    "hosts_path": str(HOSTS_FILE),
                },
            )
        if path == "/api/logs":
            return self._json(
                200,
                {
                    "ok": True,
                    "dhcp": read_log_tail(LOG_DHCP),
                    "err": read_log_tail(LOG_ERR),
                },
            )
        if path == "/api/detect":
            code, out = run(["bash", str(DHCP_SH), "detect"], timeout=30)
            return self._json(200, {"ok": code == 0, "output": strip_ansi(out)})
        if path in ("/", "/index.html"):
            self.path = "/index.html"
        return SimpleHTTPRequestHandler.do_GET(self)

    def do_POST(self):
        path = urlparse(self.path).path
        body = self._read_json()
        if path == "/api/action":
            action = (body.get("action") or "").strip()
            mapping = {
                # 用英文子命令，避免 osascript 传递中文参数异常
                "start": ["start"],
                "stop": ["stop"],
                "install": ["install"],
                "setip": ["setip"],
                "detect": ["detect"],
            }
            if action not in mapping:
                return self._json(400, {"ok": False, "error": "未知操作"})
            # 配 IP / 安装 / 开启前，若请求带了表单 env，先写入 network.env
            if action in ("setip", "install", "start") and isinstance(body.get("env"), dict):
                try:
                    save_env(body["env"])
                except Exception as e:
                    return self._json(500, {"ok": False, "error": "保存配置失败: %s" % e})
            if action == "detect":
                code, out = run(["bash", str(DHCP_SH), "detect"], timeout=30)
            else:
                code, out = run_privileged(mapping[action])
            return self._json(
                200,
                {
                    "ok": code == 0,
                    "action": action,
                    "code": code,
                    "output": strip_ansi(out),
                    "status": get_status(),
                },
            )
        if path == "/api/config":
            try:
                if "env" in body and isinstance(body["env"], dict):
                    save_env(body["env"])
                if "hosts" in body and isinstance(body["hosts"], str):
                    HOSTS_FILE.write_text(body["hosts"], encoding="utf-8")
                return self._json(200, {"ok": True, "env": load_env()})
            except Exception as e:
                return self._json(500, {"ok": False, "error": str(e)})
        return self._json(404, {"ok": False, "error": "not found"})


def main():
    if not DHCP_SH.exists():
        print("未找到 dhcp.sh:", DHCP_SH, file=sys.stderr)
        sys.exit(1)
    # 工作目录离开 Desktop，避免子进程 getcwd / TCC 异常
    try:
        os.chdir("/tmp")
    except Exception:
        pass
    try:
        httpd = ThreadingHTTPServer((HOST, PORT), Handler)
    except TypeError:
        httpd = ThreadingHTTPServer((HOST, PORT), Handler)
    url = "http://%s:%d/" % (HOST, PORT)
    print("========================================")
    print("  Mac DHCP 可视化控制台")
    print("  %s" % url)
    print("  目录: %s" % ROOT)
    print("  提权执行目录: %s" % RUN_BUNDLE)
    print("  Ctrl+C 退出")
    print("========================================")
    try:
        subprocess.Popen(["open", url], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except Exception:
        pass
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\n已退出")
        httpd.server_close()


if __name__ == "__main__":
    main()

#!/bin/bash
# 从 .app 定位到包旁边的 DHCP 根目录
APP_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
ROOT="$(cd "$APP_DIR/.." && pwd)"
cd "$ROOT" || exit 1
chmod +x ./dhcp.sh ./bin/*/dnsmasq 2>/dev/null || true
# 已在跑则只打开浏览器；否则后台启动
if curl -fsS --max-time 1 http://127.0.0.1:8787/ >/dev/null 2>&1 || nc -z 127.0.0.1 8787 >/dev/null 2>&1; then
  open "http://127.0.0.1:8787/"
  exit 0
fi
# 后台启动（server.py 自己也会 open 浏览器）
nohup ./dhcp.sh ui >/tmp/mac-dhcp-ui.log 2>&1 &
# 等端口就绪再确保打开一次
for i in 1 2 3 4 5 6 7 8 9 10; do
  if curl -fsS --max-time 1 http://127.0.0.1:8787/ >/dev/null 2>&1; then
    open "http://127.0.0.1:8787/"
    exit 0
  fi
  sleep 0.25
done
open "http://127.0.0.1:8787/"

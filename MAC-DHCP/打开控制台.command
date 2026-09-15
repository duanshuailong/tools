#!/bin/bash
# 双击此文件即可打开 Mac DHCP 可视化控制台
cd "$(dirname "$0")" || exit 1
chmod +x ./dhcp.sh ./bin/*/dnsmasq 2>/dev/null || true
exec ./dhcp.sh ui

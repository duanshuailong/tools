#!/bin/bash
# ============================================================================
# IsoFactory 云主机一键部署脚本（阿里云 Ubuntu 22.04/24.04）
#
# 在目标云主机上，以有 sudo 权限的用户运行：
#   sudo bash deploy/setup.sh
#
# 它会：安装依赖(xorriso/go/node/nginx) → 建 isofactory 用户 → 编译前后端
# → 装 systemd 单元 + nginx 站点 → 启动服务。
#
# 幂等：可重复运行。不改动 /opt/isofactory/template 里已放入的大文件。
# ============================================================================
set -euo pipefail

# ── 可调变量 ────────────────────────────────────────────────────────────
APP_DIR="${APP_DIR:-/opt/isofactory}"          # 部署根目录
APP_USER="${APP_USER:-isofactory}"             # 运行服务的专用用户
REPO_DIR="${REPO_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"  # 本仓库路径
GO_VERSION="${GO_VERSION:-1.23.1}"             # 未装 go 时下载的版本

log() { echo -e "\033[36m[setup]\033[0m $*"; }
die() { echo -e "\033[31m[setup] 错误:\033[0m $*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "请用 root / sudo 运行"

# ── 1. 系统依赖 ─────────────────────────────────────────────────────────
log "安装系统依赖 (xorriso, nginx, curl, nodejs, npm)…"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y xorriso nginx curl ca-certificates
# Node.js：优先系统源，够用即可（前端只在构建期需要）
if ! command -v node >/dev/null 2>&1; then
    apt-get install -y nodejs npm
fi

# ── 2. Go 工具链 ────────────────────────────────────────────────────────
if ! command -v go >/dev/null 2>&1; then
    log "安装 Go ${GO_VERSION}…"
    ARCH=$(dpkg --print-architecture)   # amd64 / arm64
    curl -fsSL "https://mirrors.aliyun.com/golang/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o /tmp/go.tgz \
        || curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o /tmp/go.tgz
    rm -rf /usr/local/go
    tar -C /usr/local -xzf /tmp/go.tgz
    ln -sf /usr/local/go/bin/go /usr/local/bin/go
fi
log "Go 版本：$(go version)"

# ── 3. 专用用户 + 目录 ──────────────────────────────────────────────────
if ! id "$APP_USER" >/dev/null 2>&1; then
    log "创建用户 $APP_USER…"
    useradd --system --create-home --shell /usr/sbin/nologin "$APP_USER"
fi
mkdir -p "$APP_DIR"

# ── 4. 同步代码（template/backend/frontend）────────────────────────────
log "同步代码到 $APP_DIR…"
for d in backend frontend template; do
    mkdir -p "$APP_DIR/$d"
    # -a 保留权限；--delete 只清理代码目录，template 里每次构建材料化的大文件用 --exclude 保护
    if [ "$d" = "template" ]; then
        rsync -a "$REPO_DIR/$d/" "$APP_DIR/$d/" \
            --exclude 'ubuntu*.iso' --exclude 'new-ubuntu.iso' \
            --exclude 'ubuntu-files*' --exclude 'drivers/'
    else
        rsync -a --delete "$REPO_DIR/$d/" "$APP_DIR/$d/" --exclude 'node_modules' --exclude 'dist' --exclude 'data'
    fi
done

# 版本化资产库（基础镜像/驱动）与 template 同级，便于硬链接材料化。
mkdir -p "$APP_DIR/assets"

# ── 5. 编译后端 ─────────────────────────────────────────────────────────
log "编译后端…"
( cd "$APP_DIR/backend" && GOFLAGS=-mod=mod go build -o isofactory ./cmd/isofactory )

# ── 6. 构建前端 ─────────────────────────────────────────────────────────
log "构建前端…"
( cd "$APP_DIR/frontend" && npm install --no-audit --no-fund && npm run build )

# ── 6b. NVIDIA CUDA apt 源（独立驱动/CUDA 版本下载所需）───────────────────
# 服务以非 root 运行，无法写 /etc 与 /usr，故在此以 root 一次性配置官方源；
# 服务运行时只读取（apt-cache madison / apt-get download）。幂等。
log "配置 NVIDIA CUDA apt 源…"
NV_REPO="https://developer.download.nvidia.cn/compute/cuda/repos/ubuntu2404/x86_64"
if [ ! -f /usr/share/keyrings/isofactory-nvidia-cuda.gpg ]; then
    curl -fsSL "$NV_REPO/3bf863cc.pub" | gpg --dearmor -o /usr/share/keyrings/isofactory-nvidia-cuda.gpg
fi
echo "deb [signed-by=/usr/share/keyrings/isofactory-nvidia-cuda.gpg] $NV_REPO/ /" \
    > /etc/apt/sources.list.d/isofactory-nvidia-cuda.list
apt-get update >/dev/null 2>&1 || log "警告：apt-get update 有告警，NVIDIA 源可能未完全就绪"

# ── 6c. DOCA-OFED apt 源（当前维护的 OFED，替代已停更的 MLNX_OFED）─────────
log "配置 DOCA-OFED apt 源…"
DOCA_VER="3.5.0"
DOCA_REPO="https://linux.mellanox.com/public/repo/doca/${DOCA_VER}/ubuntu24.04/x86_64"
if [ ! -f /usr/share/keyrings/isofactory-doca.gpg ]; then
    # DOCA ships an already-dearmored binary keyring; download it directly.
    curl -fsSL "$DOCA_REPO/doca_keyring.gpg" -o /usr/share/keyrings/isofactory-doca.gpg
fi
echo "deb [signed-by=/usr/share/keyrings/isofactory-doca.gpg] $DOCA_REPO/ /" \
    > /etc/apt/sources.list.d/isofactory-doca.list
apt-get update >/dev/null 2>&1 || log "警告：apt-get update 有告警，DOCA 源可能未完全就绪"

# ── 7. 权限 ─────────────────────────────────────────────────────────────
mkdir -p "$APP_DIR/backend/data" "$APP_DIR/assets"
chown -R "$APP_USER:$APP_USER" "$APP_DIR"

# ── 8. systemd 单元 ─────────────────────────────────────────────────────
log "安装 systemd 单元…"
install -m 0644 "$REPO_DIR/deploy/isofactory.service" /etc/systemd/system/isofactory.service
systemctl daemon-reload
systemctl enable isofactory
systemctl restart isofactory

# ── 9. nginx 站点 ───────────────────────────────────────────────────────
log "配置 nginx…"
install -m 0644 "$REPO_DIR/deploy/nginx.conf" /etc/nginx/sites-available/isofactory
ln -sf /etc/nginx/sites-available/isofactory /etc/nginx/sites-enabled/isofactory
rm -f /etc/nginx/sites-enabled/default
nginx -t
systemctl reload nginx

log "完成。"
echo
echo "  后端状态： systemctl status isofactory"
echo "  后端日志： journalctl -u isofactory -f"
echo "  访问：     http://<本机公网IP>/"
echo
echo "  ⚠ 首次构建会自动下载对应版本基础镜像（约 3GB）到 $APP_DIR/assets/base/<os>/<版本>/。"
echo "  ⚠ 需含驱动时，按版本放入资产库（本地优先，无法在线下载）："
echo "       NVIDIA/CUDA: $APP_DIR/assets/nvidia/<驱动版本>/<cuda版本>/cuda_*.run"
echo "       OFED:        $APP_DIR/assets/ofed/<版本>/MLNX_OFED_LINUX-*.tgz"
echo "  ⚠ nginx 默认无鉴权/TLS，公网暴露前请在 nginx.conf 里加 auth_basic + certbot。"

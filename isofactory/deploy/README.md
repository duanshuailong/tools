# IsoFactory 部署（阿里云）

把平台部署到一台阿里云 Linux 主机，从裸机到能出 ISO 的完整步骤。

## 0. 主机准备

| 项 | 建议 |
|----|------|
| 系统 | Ubuntu 22.04 或 24.04 LTS |
| CPU/内存 | ≥ 2 核 4G（打包解包 + 校验） |
| 磁盘 | **≥ 60G**（源 ISO 3G + 解包树 ~8G + CUDA 拷贝 >4G + 产物，留足余量） |
| 安全组 | 放行 80（HTTP）；如加 TLS 再放 443。**不要**直接放行后端 8080 |

## 1. 一键部署

把本仓库传到主机（`scp -r` 或 `git clone`），然后：

```bash
sudo bash deploy/setup.sh
```

脚本会：装依赖（xorriso/go/node/nginx）→ 建 `isofactory` 专用用户 → 编译后端 + 构建前端 → 装 systemd 单元 + nginx 站点 → 启动。幂等，可重复跑。

完成后访问 `http://<公网IP>/`。

## 2. 放入驱动大文件（仅在需要 NVIDIA/OFED 时）

源 Ubuntu ISO 会**自动下载**，无需手动放。但 CUDA/OFED 大文件需自备：

```bash
sudo cp cuda_13.2.2_595.71.05_linux.run        /opt/isofactory/template/drivers/
sudo cp MLNX_OFED_LINUX-24.04-0.6.6.0-*.tgz    /opt/isofactory/template/drivers/
sudo chown -R isofactory:isofactory /opt/isofactory/template/drivers
```

> ⚠ `drivers/` 只放文件，不要放子目录（pack.sh 的 `md5sum ./drivers/*` 遇目录会中断）。
> 离线 deb 包同理放入 `/opt/isofactory/template/debs/<分组>/`，参考 `template/README.md`。

## 3. 触发首次构建

- **网页**：打开站点 → 勾选是否含 NVIDIA/OFED → 确认/修改 ISO 名称 → 开始构建 → 看日志 → 下载。
- **命令行**（等价）：
  ```bash
  curl -X POST http://localhost/api/build -d '{"include_nvidia":true,"include_ofed":true}'
  curl http://localhost/api/jobs                       # 看状态
  curl -OJ http://localhost/api/jobs/<id>/download     # 下载产物
  ```

首次构建会自动下载 Ubuntu 24.04.4（约 3G，进度写进构建日志）。

## 4. 运维

```bash
systemctl status isofactory        # 服务状态
journalctl -u isofactory -f        # 实时日志
systemctl restart isofactory       # 重启（历史任务会从磁盘恢复）
```

产物与元数据在 `/opt/isofactory/backend/data/`（`isos/<id>.iso` + `jobs/<id>.json|.log`）。

## 5. 上线前加固（公网暴露必做）

后端**无鉴权**，靠 nginx 兜。编辑 `/etc/nginx/sites-available/isofactory`：

```nginx
# 基础鉴权
auth_basic "IsoFactory";
auth_basic_user_file /etc/nginx/.htpasswd;   # htpasswd -c 创建
```

TLS 用 certbot：
```bash
sudo apt-get install -y certbot python3-certbot-nginx
sudo certbot --nginx -d your.domain
```

改完 `sudo nginx -t && sudo systemctl reload nginx`。

## 文件

- `setup.sh` — 一键部署脚本
- `isofactory.service` — 后端 systemd 单元
- `nginx.conf` — 托管前端 + 反代 API（TLS/鉴权入口）

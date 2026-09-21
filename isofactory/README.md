# IsoFactory

ISO 制作平台。在已有的命令行定制流水线（`template/`）之上，提供 Web 界面与后端服务，把"提交构建 → 排队 → 查看进度 → 下载 ISO"做成可操作的产品。

面向场景：定制 Ubuntu Server 24.04 无人值守装机盘（含 NVIDIA/CUDA、OFED/InfiniBand 驱动栈，BIOS+UEFI 双引导）。

## 结构

```
IsoFactory/
├── template/     # ★ 现有 CLI 定制流水线（pack.sh + 离线包 + autoinstall），平台复用
├── backend/      # Go 后端：封装 pack.sh、构建队列、任务 API
├── frontend/     # React 前端：配置构建、进度、下载
├── deploy/       # systemd + nginx 部署配置
└── PROGRESS.md   # 进度记录
```

## 本地运行

前置：`xorriso`（打包）、Go 1.23+、Node 18+。

用 Makefile（`make help` 看全部）：

```bash
make deps            # 安装前端依赖（首次）
make backend         # 终端1：后端 :8080
make frontend        # 终端2：前端 dev server http://localhost:5173（/api 代理到 :8080）
make test            # 后端测试（-race）
make build           # 产物：backend/isofactory + frontend/dist
```

或手动：

```bash
cd backend  && go run ./cmd/isofactory -addr :8080 -template ../template
cd frontend && npm install && npm run dev
```

源 Ubuntu ISO **无需手动放入**——本地缺失时构建会自动从阿里云镜像下载并校验 SHA256（可用 `-no-fetch` 关闭）。若要含驱动，把 `cuda_*.run` / `MLNX_*.tgz` 放进 `template/drivers/`，离线 deb 放进 `template/debs/<分组>/`，详见 [`template/操作步骤.md`](./template/操作步骤.md)。

## 构建生产产物

```bash
cd backend  && go build -o isofactory ./cmd/isofactory
cd frontend && npm run build     # 产物在 frontend/dist
```

## 云主机部署（阿里云）

一键部署：把仓库传到主机后 `sudo bash deploy/setup.sh`（装依赖 → 编译前后端 → systemd + nginx → 启动）。完整分步指南（主机规格、驱动放置、首次构建、鉴权/TLS 加固）见 [`deploy/README.md`](./deploy/README.md)。

## 安全

后端**无内置鉴权**，只应跑在可信内网或反向代理（nginx）之后，由代理层负责 TLS 与访问控制。切勿把后端端口直接暴露公网。

## 进度

见 [`PROGRESS.md`](./PROGRESS.md)。

# IsoFactory 进度记录

> ISO 制作平台建设跟踪文档
> 最后更新：2026-09-20

---

## 一、项目概述

在**已有的命令行 ISO 定制流水线**（`template/`）之上，搭建一套 ISO 制作平台，覆盖三个层面：

- **服务器侧部署**：把现有 `pack.sh` 打包流程搬到云主机，做成可被调用的构建服务。
- **Web 搭建**：面向用户的操作界面，提交制作任务、配置参数、查看进度、下载成品。
- **优化**：构建速度、并发、缓存、离线包管理、镜像体积、页面性能等。

> ⚠ 重要认知：本项目**不是从零开始**。`template/` 下已有一套成熟、可运行的 CLI 定制系统（面向 Ubuntu Server 24.04 + NVIDIA/CUDA/OFED 服务器装机场景）。平台层是对它的**封装与产品化**，不是重写。

## 二、现有基础（template/ 已完成部分）

这是**已经跑通**的核心资产，平台建设的地基：

| 组件 | 文件 | 作用 | 状态 |
|------|------|------|------|
| 打包脚本 | `template/pack.sh` | 解包原 ISO → 注入 debs/drivers → 拆分 CUDA(绕过 4GB 限制) → `xorriso replay` 重封装，保留 BIOS+UEFI 双引导 | ✅ 可运行 |
| 离线源索引 | `template/debs/gen_packages.sh` | `dpkg-scanpackages` 生成 `Packages`/`Packages.gz`，把 `debs/` 变离线 APT 源 | ✅ |
| 包检测 | `template/check-packages.sh` | 只读检测本机已装/缺失的清单包与 GPU 驱动栈 | ✅ |
| autoinstall | `template/user-data`(855行) 及 `-single`/`-single-boot4g` 变体 | 无人值守装机：动态选盘、RAID1、离线装包、CUDA/FM/DCGM、网卡命名 | ✅ 多变体 |
| 参考手册 | `template/README.md` | 目录结构 / 包清单 / 版本对齐 / pack.sh 解析 / 原理 | ✅ |
| 操作 SOP | `template/操作步骤.md` | 采集→打包→验证→装机 全流程步骤 | ✅ |

**技术底座**（由现有系统决定，平台需沿用）：
- 基础镜像：Ubuntu Server 24.04.4 LTS amd64（内核 6.8.0-111）
- 引导：BIOS(El Torito) + UEFI 双引导（`xorriso -boot_image any replay` 复用原引导）
- 构建工具：**xorriso**（本机已装 1.5.8）
- 驱动基准：CUDA 13.2.2 / 驱动 595.71.05 / OFED 24.04-0.6.6.0

**现有系统已知待办**（来自 README，与平台层无关但需记录）：
- ⚠ `debs/nvidia/` 缺运行时 `nvidia-fabricmanager_595.71.05`（只有 `-dev`）
- ⚠ `libnvidia-nscq` 仍为 580，需升到 595.71.05 与驱动对齐
- ⚠ `drivers/` 下不能放子目录（`md5sum ./drivers/*` 遇目录会中断 pack.sh）

## 三、平台建设阶段规划

| 阶段 | 目标 | 状态 |
|------|------|------|
| 0. 规划 | 明确需求、技术选型、理清现有基础 | ✅ 完成 |
| 1. 服务器基础环境 | 云主机装 xorriso 等依赖，跑通 `pack.sh` | ⬜ 未开始 |
| 2. 构建服务封装 | 把 pack.sh 封装成可调用服务（参数化：是否含 NVIDIA/OFED、选 user-data 变体） | ✅ 完成（NVIDIA/OFED 参数化已做；user-data 变体待做） |
| 3. 后端服务 | Go：任务 API、构建队列、产物存储、版本化资产管理 | 🟡 API+队列+持久化+Preflight+版本化资产库(本地优先/缺失下载)+自动命名+进度上报+产物隔离 可运行；驱动上传界面待做 |
| 4. Web 前端 | React：版本选择、配置构建、进度、下载 | 🟡 版本选择(系统/驱动/CUDA/OFED)+缺失预览+进度条+日志+下载 可用；驱动上传待做 |
| 5. 联调与部署 | 前后端打通，云主机上线 | 🟡 本地联调通过；一键部署脚本+指南就绪（setup.sh/systemd/nginx）；只差在阿里云主机上执行 |
| 6. 优化 | 并发构建、缓存解包结果、包体积、页面性能 | ⬜ 未开始 |

图例：⬜ 未开始 / 🟡 进行中 / ✅ 完成

## 四、技术选型（已确定 2026-09-20）

| 维度 | 选型 | 说明 |
|------|------|------|
| ISO 类型 | 通用可引导 ISO（当前实为 Ubuntu 24.04 定制装机盘） | 沿用现有 template |
| 构建工具链 | **xorriso**（现有 pack.sh 已用） | 保留 replay 双引导方式 |
| 部署环境 | **云主机**（Linux，建议 Ubuntu 22.04/24.04） | 需 ≥20GB 磁盘余量 |
| 后端 | **Go** | 封装 pack.sh、任务调度、存储 |
| 前端 | **React** | 任务提交、进度、下载界面 |

### 已确认（2026-09-20 追加）
- **云厂商**：阿里云（与现有 autoinstall 用的 `mirrors.aliyun.com` 源一致）。
- **构建范围**：**通用化** —— 除现有 Ubuntu 装机盘外，支持"任意目录打包成可引导/数据 ISO"。
- **优先方向**：云上真实跑通一次（需阿里云主机访问，本地先做能做的准备）。

### 仍需确认
1. **阿里云主机规格**：CPU/内存/磁盘（打包解包 + CUDA 拷贝需 ≥20GB 余量）、系统版本、访问方式（SSH）。
2. ~~原版 ISO 来源~~ → **已定：缺失时自动联网下载**（Ubuntu 24.04.4，阿里云镜像源，SHA256 校验）。大文件（CUDA/OFED）来源仍待定（>4GB，影响存储与上传）。
3. **是否需要多用户 / 鉴权**：影响后端账号体系设计。
4. **构建产物存储**：本地磁盘、对象存储（OSS/S3）还是两者。

## 四·五、后端 API（已实现）

Go 后端骨架已跑通，位于 `backend/`。单 worker 串行执行构建（因 pack.sh 解包到共享 `ubuntu-files/`，并发会互相污染）。

| 方法 | 路径 | 作用 |
|------|------|------|
| GET | `/api/health` | 健康检查 |
| GET | `/api/preflight` | 环境就绪检查，返回 `{"ready":bool,"reason":str,"will_fetch":bool}`（pack.sh/xorriso 是否就位；`will_fetch`=构建会先自动下载源 ISO） |
| GET | `/api/inventory` | 构建输入清单：源镜像文件名、`debs/` 各分组（deb 数 + 顶层包清单）、`drivers/` 文件（名+大小）、离线源索引是否已生成 |
| GET | `/api/catalog` | 可选基础镜像（os/version）+ `debs/` 分组清单，供前端做版本选择 |
| GET | `/api/default-name` | 预览自动命名，query `os`/`system_version`/`include_nvidia`/`nvidia_version`/`cuda_version`/`include_ofed`/`ofed_version`，返回 `{"default_name":str,"versions":{...},"missing":[{kind,path,downloadable,note}]}` |
| POST | `/api/build` | 提交构建，body 可选 `{os,system_version,include_nvidia,nvidia_version,cuda_version,include_ofed,ofed_version,name}`（name 留空则自动命名） |
| GET | `/api/jobs` | 列出所有任务（最新在前，含 `output_name` + 进度 `phase`/`percent`/`phase_msg`） |
| GET | `/api/jobs/{id}` | 查询单个任务状态（含进度字段） |
| GET | `/api/jobs/{id}/log` | 拉取该任务的构建日志（纯文本） |
| GET | `/api/jobs/{id}/download` | 下载产物 ISO（未就绪返回 409） |

任务状态机：`queued → running → done | failed`；进度阶段 `queued→resolving→downloading→building→archiving→done/failed`。

**版本化资产库 + 本地优先**（`internal/assets`）：大文件按版本分层存放，构建先读本地、缺失才在线拉取：
```
assets/
├── base/<os>/<系统版本>/<iso>              # 基础镜像（缺失时按 catalog 在线下载 + SHA256 校验）
├── nvidia/<驱动版本>/<cuda版本>/cuda_*.run  # NVIDIA 驱动+CUDA（本地优先，无在线源→报缺失）
└── ofed/<ofed版本>/MLNX_OFED_LINUX-*.tgz    # OFED（同上）
```
`Resolve` 报告命中/缺失（区分"可在线下载"与"须手动放入"）；`Ensure` 下载缺失的基础镜像（进度经 `progress.Reporter` 上报），驱动缺失则明确报错。构建时把解析到的资产**材料化**进 template 工作区：基础 ISO 用符号链接（pack.sh 只读），驱动文件用硬链接（跨盘回退拷贝，因 pack.sh 会 `cp -r drivers/`，符号链接会悬空）；每次构建前清理旧版本材料，避免串味。

**进度上报**（`internal/progress`）：无依赖的 phase+percent 模型，builder 在 resolving/downloading/building 阶段上报，queue 存到 job，API 经 `phase`/`percent`/`phase_msg` 暴露，前端渲染进度条。下载进度（百分比）与打包进度（阶段）都在 Web 可见。

**ISO 自动命名**（`internal/builder/versions.go`）：默认名 `ubuntu<系统>-<内核>-nvidia<驱动>-cuda<版本>-ofed<版本>.iso`，各段取自 selection 版本（内核仍从 `debs/linux-*` 探测）；未勾选/未知的段省略。用户可自定义 `name`（自动补 `.iso`、防路径穿越）。

**产物存储隔离**：pack.sh 固定写 `template/new-ubuntu.iso`，构建成功后队列移到每任务独立路径 `<data>/isos/<id>.iso`，下载用 `output_name` 命名。

启动：`cd backend && go run ./cmd/isofactory -addr :8080 -template ../template -assets ../assets`

⚠ **安全**：后端当前**无鉴权**，仅可在可信内网或反向代理后运行，勿直接暴露公网。

## 五、目录结构规划

```
IsoFactory/
├── PROGRESS.md            # 本进度文档
├── template/              # ★ 现有 CLI 定制流水线（构建配方：pack.sh/debs/user-data）
│   ├── pack.sh            #   drivers/ 与 ubuntu*.iso 每次构建由 assets/ 材料化进来
│   ├── debs/  user-data*  ...
├── assets/                # ✅ 版本化资产库（本地优先，缺失在线拉取；大文件，gitignore）
│   ├── base/<os>/<版本>/*.iso
│   ├── nvidia/<驱动>/<cuda>/cuda_*.run
│   └── ofed/<版本>/MLNX_OFED_LINUX-*.tgz
├── README.md              # ✅ 顶层说明（结构/运行/部署/安全）
├── backend/               # ✅ Go 后端服务
│   ├── cmd/isofactory/    # main：serve（HTTP）+ genpack（通用构建 CLI）
│   ├── internal/
│   │   ├── api/           # ✅ HTTP 路由与 handler（9 端点）
│   │   ├── assets/        # ✅ 版本化资产库 + catalog + 本地优先解析 + 缺失下载
│   │   ├── builder/       # ✅ 封装 pack.sh + Preflight + Inventory + 版本探测/自动命名 + 资产材料化
│   │   ├── fetcher/       # ✅ 通用校验下载器（SHA256 + 原子改名 + 镜像 fallback）
│   │   ├── generic/       # ✅ 通用构建：任意目录 → 可引导/数据 ISO（xorriso mkisofs，已验证）
│   │   ├── progress/      # ✅ 无依赖 phase+percent 进度模型（builder↔queue 共享）
│   │   ├── queue/         # ✅ 内存单 worker 队列 + 持久化 + 进度 + 产物隔离
│   │   └── storage/       # ✅ 任务元数据/日志/产物 ISO 磁盘持久化
│   └── go.mod
├── frontend/              # ✅ React 前端（Vite）
│   ├── index.html  vite.config.js  package.json
│   └── src/               # main.jsx / App.jsx（版本选择+进度条）/ api.js / styles.css
└── deploy/                # ✅ 部署配置
    ├── setup.sh           # 阿里云一键部署脚本（装依赖→编译→systemd→nginx）
    ├── isofactory.service # 后端 systemd 单元
    ├── nginx.conf         # 托管前端 dist + 反代 /api + TLS/鉴权入口
    └── README.md          # 阿里云部署分步指南（含加固、驱动放置、首次构建）
```

## 六、下一步行动

### 已完成（本地）
- [x] 搭建 Go 后端（builder + queue + storage + api），任务队列、持久化、Preflight 齐备
- [x] 搭建 React 前端（配置构建 / 任务列表 / 日志轮询 / 下载 / 就绪告警）
- [x] 前后端本地联调、单元测试、`-race` 竞态修复
- [x] 部署配置（systemd + nginx）与顶层 README

### 待办（需推进 / 需确认）
- [ ] **确认第四节"仍需确认"事项**（云主机厂商规格、构建范围、大文件来源、鉴权、产物存储）——直接影响后续架构
- [ ] **云主机首次真跑**：装 xorriso、同步 `template/`、放入原版 `ubuntu-*.iso`（及 CUDA/OFED），手工 `pack.sh` 产出一次 `new-ubuntu.iso`，再经后端 `/api/build` 端到端产出一次
- [ ] 后端离线包/驱动管理：查看 `debs/` 各目录、上传/替换 deb、跑 `gen_packages.sh` 生成索引（对应现有 CLI 能力的产品化）
- [ ] 前端素材上传：允许上传原版 ISO / 大文件（或改为服务端托管选择），配合大文件分片
- [ ] user-data 变体选择（`user-data` / `-single` / `-single-boot4g`）——注：user-data 不打进 ISO，属装机侧配置，需确认是否纳入平台管理
- [ ] 阶段 6 优化：并发构建（需 per-job 隔离 template 副本，见 queue 包注释）、缓存原 ISO 解包结果、产物清理策略
- [ ] 上线前必做：反向代理层加 TLS + 鉴权（后端无内置鉴权）

---

## 七、进度日志

按时间倒序记录每次实际改动。

### 2026-09-20（深夜续：DOCA-OFED + 完整驱动栈 + UI 横版/删资产页 + 并发构建）
- **DOCA-OFED（替代停更的 MLNX_OFED）**：新增 `internal/doca`——DOCA apt 仓库（linux.mellanox.com/public/repo/doca，实测最新 3.5.0 内含 OFED 26.07）。构建页 OFED 来源可切 DOCA-OFED(新)/MLNX_OFED(旧)。踩坑修复：key 文件是 `doca_keyring.gpg`（二进制 keyring，非 ASCII armor，不用 dearmor）；签名 key RSA4096 `70454...41B9CC50`。
- **完整驱动栈下载("下载得齐")**：nvapt `DownloadTo` 除驱动本体外，按驱动分支带上 `nvidia-fabricmanager-<分支>` + `libnvidia-nscq-<分支>`（三段号自动对齐，仅 550-575 分支有）+ DCGM + container-toolkit（4 个包全在依赖闭包，实测确认）。
- **前端**：① 横版布局（max-w-6xl + 驱动/OFED 两栏并排 + 分区标题 + 卡片 hover）；② 常用工具包加"全选(N/M)";③ 删除「资产清单」页（自动下载后已冗余，导航+代码+文件全移除）。
- **并发构建（版本化缓存 + 独立工作区 + N worker）**：
  - **版本化缓存**：nvapt/doca/toolpkgs 下载改为落到 `<data>/cache/{nvidia,doca,tools}/<版本键>/`，同版本复用、per-key 锁防并发重复下、不同版本并行；**顺带修掉驱动版本混装 bug**（旧模型 debs 累积，先 590 后 570 会都打进镜像）。各 manager 不再生成索引。
  - **独立工作区**：`builder.Run` 为每个任务在 `<data>/work/build-<随机>/` 建独立工作区（recipe 拷入、基础 ISO 符号链接、驱动/缓存 deb 硬链接进 debs/<子目录>、重建离线索引、pack.sh 在此运行），返回 ISO 路径 + cleanup;队列存完 ISO 后清工作区。
  - **N worker**：`queue.New(runner, store, workers)` 起 N 个并发 worker（`-workers` flag，默认 3）。
  - **单测**：queue 加并发隔离测试（假 runner + 原子计数验证 maxSeen≥2 真并行）+ cleanup 调用测试;全后端 `go test -race` 全绿（8 包）。
- **修 pack.sh 空 debs bug**：`md5sum ./debs/*/*` 在无 deb（drivers-off/无工具包）时 glob 不展开→`set -e` 挂;改用 `find ./debs -name '*.deb' -exec md5sum`。
- **✅ 串行真跑验证通过**：drivers-off 构建走完整隔离工作区路径 → 产出 3.4GB 可引导 ISO（`file` 确认 bootable）、工作区自动清理、下载名正确。⚠ **并发压测（同时多个大构建）未验证**——需你有空时同时触发 2-3 个真实构建观察，隔离逻辑已用单测覆盖但未经真实多 GB 并发负载。
- 已全部部署上线 39.99.33.153（重装后完整 provisioning + 后续增量部署）。
- **待办（用户认领）**：用户将提供正确的软件包下载路径（怀疑现有部分 URL 不准）。

### 2026-09-20（傍晚续：全资产在线下载 + 多版本 + UI 重做 + 上线阿里云）
- **纠正"驱动/OFED 无法在线下载"的错误判断**：实测 CUDA `.run`（developer.download.nvidia.com，跳转 .cn CDN，206）、OFED（content.mellanox.com，200/206）、ISO（302）全部可在线下载。改为**全部在线下载 + 多版本可选**。
- **catalog 从 map 重构为结构体** `internal/assets/catalog.go`：`Images`（Ubuntu 24.04.3/.4/.5）、`CUDA`（5 组 cuda/driver 对，URL 由版本号推导）、`OFED`（多版本 + OS 标签，URL 推导）。全部**实测 206** 才入库，不编 URL。`Ensure` 现在也下载 CUDA/OFED（不止基础镜像），仍带进度。
- **常用工具包在线下载** `internal/toolpkgs`：8 个分组（编译链/监控/存储/网络/硬件/终端/服务/OFED依赖，取自 README §4）。`apt-cache depends --recurse` 解析完整依赖闭包 → `apt-get download` 拉全量 deb → `dpkg-scanpackages` 重建离线索引。`Manager` 单并发 + 每组状态/日志。API：`GET /api/toolpkgs`、`POST /api/toolpkgs/{group}/download`、`GET /api/toolpkgs/{group}/log`。
- **前端整体重做**：引入 Tailwind v3 + shadcn 风格设计令牌（浅色企业后台）。左侧导航分「新建构建 / 构建任务 / 资产清单」三页；构建页版本全部改为**下拉选择**（修复"只有一种"）；任务页进度条 + 日志；资产页展示可下载清单 + 工具包分组（含在线下载按钮/状态/日志）。UI 组件 `components/ui.jsx`（Card/Field/Select/Badge/Progress/Alert）。
- **部署上线阿里云** `39.99.33.153:/data/isofactory/`（Ubuntu 24.04.4，与基础镜像同版本）：装 xorriso/Go 1.23/Node 18/nginx；systemd `isofactory` 服务 + nginx 反代（路径改为 `/data/isofactory`，非仓库默认 `/opt`）；前端 dist 由 nginx 托管。外网 http://39.99.33.153/ 可访问。
- **实测真实下载跑通**：服务器上触发 `term` 工具包分组下载 → 解析闭包 → 从阿里云云内镜像拉取 **87 个 deb（21.9MB / 9s）** → 重建 `Packages`/`Packages.gz`（87 条），全链路正常。
- 修复 rsync `--exclude 'assets'` 未加锚点误删 `backend/internal/assets/` 源码包的坑（改 `--exclude '/assets'`）。后端 9 包测试全绿（新增 assets/toolpkgs 测试）。
- ⚠ **安全**：站点公网可达且无鉴权，上线正式用前需在 nginx 加 Basic Auth/TLS 或安全组限 IP（待办）。
- **纠正"版本硬编码"**：实测发现版本清单已过时（Ubuntu 已出 26.04/26.04.1，CUDA 驱动已到 580.178.04）。新增 `internal/assets/catalog_live.go`：从阿里云镜像目录**实时抓取** Ubuntu live-server 版本 + SHA256SUMS，启动即拉、每小时刷新，合并覆盖 curated 列表（`StartAutoRefresh`）；CUDA/OFED 因目录列表被禁（NVIDIA/Mellanox 不可 browse），保持 curated 但更新到当前实测可下版本（CUDA 加 13.0.1/12.9.1/12.8.1）。Store 加读写锁保护 catalog 并发刷新，`go test -race` 通过。
- **发起首次真实构建验证**（drivers off，仅基础镜像）：`24.04.4` 镜像从阿里云云内镜像下载（3.4GB，实时进度条到 100%）。
- **发现并修复真实流水线 bug**：下载完成、xorriso 解包正常，但 pack.sh 在 `cp -r debs ubuntu-files/` 报 **Permission denied**——因 xorriso 保留 ISO9660 只读目录权限（root 目录 0555），非 root 的 `isofactory` 服务用户无法写入。原 pack.sh 一直以 root 手工运行故从未暴露。修复：解包后加 `chmod -R u+w "$ISO_PATH"`（root 运行时无害空操作）。
- **✅ 首次端到端成功产出可引导 ISO**：重新部署（含 pack.sh 修复）→ ISO 已缓存跳过下载 → 材料化 → pack.sh → 归档，job `done`；产物 `20260920-165004-1.iso` **4.58GB**，`file` 识别为 `ISO 9660 ... (bootable)`，下载端点 HTTP 206 + 正确文件名 `ubuntu24.04.4.iso`。**catalog→下载→材料化→pack.sh→归档→下载 全链路验证通过**。
- **修正 OFED 陈旧 + CUDA 版本扩充**：OFED 更新到当前实测最新 `24.10-3.2.5.0`（原止步 24.10-1.1.4.0），加 24.10-2.1.8.0/24.07 等 8 个版本；CUDA `.run` 对扩到 17 组（加模板基线 13.2.2/595.71.05 及 12.3~12.9 多版本，全部实测 206）。
- **修复并上线实时版本发现的 bug**：`catalog_live.go` 的正则要求 `href="24.04/"` 带尾斜杠，但阿里云镜像实际是 `href="24.04"`（无斜杠），且误把点发布目录（24.04.4/）当版本、又与每条 ISO 行叉乘 → 版本/文件/URL 错位。改为只扫「系列目录」（24.04、26.04），版本从 ISO 文件名解析、URL 指向文件实际所在的系列目录、按版本去重。上线后 catalog 实时显示 **26.04.1 / 26.04 / 25.10 / 24.04.5/.4/.3 / 22.04.5**（每小时自动刷新）。
- **✅ 独立驱动/CUDA 版本选择 + 在线下载（新增 `internal/nvapt`）**：经 NVIDIA 官方 CUDA apt 源，驱动（`cuda-drivers`，45 个版本 550→615.71.09）与 CUDA（`cuda-toolkit-XX-Y`，9 个 12.5→13.4）**可独立组合**（不同于 `.run` 绑定对）。`EnsureRepo`（源就绪则只读、否则 root 装源）+ `Versions`（apt-cache madison 列版本）+ `Download`（apt-cache depends 解闭包 → apt-get download → 重建离线索引）+ `Manager`（单并发 + 版本缓存 30min）。API：`GET /api/nvapt/versions|status|log`、`POST /api/nvapt/download`。前端资产页加"独立选择版本下载"卡片（驱动/CUDA 两个下拉 + 下载 + 状态 + 日志）。
- **踩坑与修复**：① systemd `ProtectSystem=full` + `/etc` root 属主 → 服务（非 root）无法写 apt 源/keyring。改为**部署期以 root 一次性配 NVIDIA 源**（`deploy/setup.sh` §6b：keyring + source + apt update），服务运行时**只读**（madison/depends/download），与工具包下载同模式；`EnsureRepo` 加 `repoReady` 快速路径，源已就绪即 no-op。② `sync.Once` 会永久缓存首次失败 → 改为只缓存成功、失败可重试。
- **✅ 实测独立下载跑通**：服务器上以 root 配好 NVIDIA 源后，触发驱动 595.71.05 下载 → 解析 211 包闭包 → 拉取 **226 个 deb（488MB）** 归入 `template/debs/nvidia-apt/` 并重建离线索引（1202 条），state=done。驱动与 CUDA 独立选择、在线下载全链路验证通过。
- ⚠ 说明：apt 离线装驱动的**装机端**（把这些 deb 在目标机离线 apt 安装）尚未在真实 GPU 机验证；下载与离线源生成已验证，装机验证需 GPU 硬件。

### 2026-09-20（夜间续：独立选择搬到构建页 + 深色 UI + 已完成菜单）
- **独立驱动/CUDA 选择搬到「新建构建」页**：此前只在资产页，构建页还是旧的 17 组固定 `.run` 绑定对（所以选不到 590 / 13.1）。现构建页 NVIDIA 区改为**两个独立下拉**（驱动 45 版 / CUDA 9 版，来自 nvapt 实时 apt 源），版本号去掉 `-0ubuntu1` 后缀只显示 `590.48.01`。选定后构建时经 `Manager.DownloadSync` 把 driver+toolkit deb 及闭包下到 `debs/nvidia-apt/` 打进镜像。
- **贯穿改动**：`assets.Selection` 加 `NVDriver`/`NVToolkit`；`Builder` 加 `NVDownloader` 接口 + 构建阶段下载步骤；`DefaultISOName` apt 选择优先（`cleanAptDriver`/`toolkitLabel`）；API `buildRequest`/`selectionFrom`/`handleDefaultName` 加 `nv_driver`/`nv_toolkit`（apt 路径与 `.run` 路径互斥）；main.go `nvAdapter` 桥接类型。实测 `default-name` 返回 `ubuntu24.04.4-nvidia590.48.01-cuda13.1.iso`。
- **UI 改深色**：`styles.css` 令牌换成 shadcn zinc 深色主题（背景/卡片/边框/输入等），保留浅色时的层次与配色语义。
- **左侧导航加「已完成镜像」页** `CompletedPage`：只列构建成功且有 ISO 的任务，直接下载。
- **答疑 ISO 命名**：产物名确实变了（下载名按 `output_name`，如 `ubuntu24.04.4.iso`）；日志里 `Done: .../new-ubuntu.iso` 是 pack.sh 的内部临时名，平台下载时按任务重命名。之前那次没选驱动故无驱动段；现构建页选了驱动/CUDA 就会带上。
- **OFED 版本**：更新到当前实测最新 `24.10-3.2.5.0`（25.x / 24.10-3.4 均 404，不存在）；Mellanox 无可 browse 的目录索引，故 curated。
- 全部 `go build`/`vet`/`test` 通过，前端构建通过，已部署上线 39.99.33.153。

### 2026-09-20（服务器重装后重新部署 + 构建页加工具包勾选）
- **服务器被重装**（SSH 主机密钥变更 + 80 端口无服务 + 工具链全空确认）。用户确认是主动重装 → 清旧密钥、接受新密钥，**从零完整 provisioning**：同步代码 → 装 xorriso/nginx/node/npm/dpkg-dev/Go1.23.1 → 编译前后端 → 建 isofactory 用户/目录 → 配 NVIDIA CUDA apt 源 → systemd + nginx（均 `/data/isofactory` 路径）。外网验证全绿：镜像 26.04.1→22.04.5、OFED 24.10-3.2.5.0、驱动 45×CUDA 9（含 590/13.1）。产物/缓存（226 个驱动 deb、旧 ISO）随重装丢失，属可重下项。
- **工具包选择搬到构建页**：此前只在资产页（且是"下过就打进每次构建"的累积模型）。现构建页加 8 个分组勾选（编译链/监控/存储/网络/硬件/终端/服务/OFED依赖），显示各组已下 deb 数。`toolpkgs.Manager` 加 `EnsureGroupsSync`（构建期同步下载缺失分组、已有则跳过）；`Builder` 加 `ToolDownloader` 接口 + 构建下载步骤；`assets.Selection` 加 `ToolGroups`；API `buildRequest`/`selectionFrom` 加 `tool_groups`。全链路 build/vet/test 通过，已上线验证（8 分组正常返回）。

### 2026-09-20（下午续：版本化资产库 + 本地优先 + 进度显示）
- **按版本重构目录层级** `internal/assets`：大文件按 `base/<os>/<系统版本>/`、`nvidia/<驱动>/<cuda>/`、`ofed/<版本>/` 分层存放。新增 `Selection`（选版本）、`Resolved`/`Missing`（解析结果，区分"可在线下载"与"须手动放入"）、`Catalog`（可下载基础镜像清单：Ubuntu 24.04.3/.4/.5 阿里云主源 + 南大备源）。
- **本地优先、缺失才下载**：`Resolve` 只读本地；`Ensure` 下载缺失的基础镜像（驱动无在线源→明确报缺失）。构建时把资产**材料化**进 template：基础 ISO 用符号链接、驱动用硬链接（跨盘回退拷贝，规避 pack.sh `cp -r` 悬空符号链接），每次构建前清理旧版本材料。
- **进度模型** `internal/progress`（无依赖 phase+percent）：builder 在 resolving/downloading/building 阶段上报，下载回调换算百分比；queue 存到 job；API 经 `phase`/`percent`/`phase_msg` 暴露。前端加**进度条**（下载百分比 + 打包阶段实时显示）。
- **重构 builder/queue/storage/api 贯穿 Selection + 进度**：`Runner.Run(ctx, Selection, logOut, Reporter)`；job 带 `Sel`/`Progress`；storage.Record 增 os/version/nvidia/cuda/ofed 版本字段；API 新增 `/api/catalog`，`/api/default-name` 与 `/api/build` 改为按 selection 选版本；`/api/jobs` 暴露进度。
- **前端版本选择**：基础系统/版本下拉（来自 catalog）、NVIDIA 驱动/CUDA/OFED 版本输入、缺失资产预览、自动命名随选择刷新、任务卡片进度条。
- 清理孤立的 `fetcher.DefaultSource`（catalog 已移入 assets）；`deploy/`（setup.sh/service）与 `.gitignore` 适配 `assets/` 布局与新 flag（`-assets`/`-default-os`/`-default-version`）。
- 验证：`go build`/`go vet`/`gofmt -l`/`go test -race ./...` 全绿（8 包 43 测试）；catalog/default-name/缺失预览经 Vite 代理端到端实测正确；前端构建通过。

### 2026-09-20
- 创建进度文档 `PROGRESS.md`，确立三层规划（服务器侧 / Web / 优化）与阶段表。
- 确定技术选型：xorriso 构建 + 云主机 + Go 后端 + React 前端。
- **通读 `template/` 现有资产**，纠正"从零开始"的初始误判：现有 CLI 定制流水线（pack.sh + 离线源 + autoinstall + 文档）已成熟可运行，平台层定位为对其封装产品化。
- 重写文档第二节，登记现有基础与已知待办；据此调整阶段规划与目录结构。
- **搭建 Go 后端骨架** `backend/`：builder（封装 pack.sh，含 Preflight 检查）+ queue（内存单 worker 串行队列）+ api（HTTP 接口）+ main。
- 本机安装 Go 1.27.1（brew）；后端 `go build`/`go vet` 通过；起服务冒烟测试 6 个端点全部符合预期（含无 ISO 时的优雅 Preflight 失败、产物未就绪返回 409）。
- 补充文档"四·五 后端 API"章节，登记接口清单、状态机、启动命令与无鉴权安全提示。
- **搭建 React 前端** `frontend/`（Vite + React 18）：配置构建（NVIDIA/OFED 开关）、任务列表（3s 轮询状态）、日志查看（2s 轮询）、产物下载。`npm run build` 通过（JS 145KB / gzip 47KB）。
- **前后端本地联调通过**：起后端(:8080) + Vite dev(:5173)，经 Vite 代理 `/api → :8080`，health/submit/首页全部正常。
- **补齐部署层** `deploy/`：`isofactory.service`（systemd，含基础加固与 ReadWritePaths）+ `nginx.conf`（托管前端 dist、反代 API、8g body 上限、长超时、TLS/鉴权 TODO 入口）。
- 新增顶层 `README.md`（结构/本地运行/生产构建/部署/安全）。
- 增补根目录 `.gitignore`（排除大文件产物：ISO、cuda.run、ubuntu-files 工作目录、node_modules/dist）。
- **后端加任务持久化** `internal/storage`：任务元数据原子写 JSON（`<data>/jobs/<id>.json`）+ 构建日志落盘（`<id>.log`）。队列启动时 reload 历史；进程重启前处于 queued/running 的任务标记为 `interrupted by backend restart`。构建日志经 `io.MultiWriter` 同时进内存缓冲（实时轮询）与磁盘（持久化）。
- **新增 `/api/preflight` 端点**：构建前检查 pack.sh/ubuntu ISO/xorriso 是否就位，前端加载即调用，未就绪在"新建构建"卡片顶部黄条告警。
- 验证：`go build`/`go vet` 通过；提交任务→重启后端→历史正确 reload；preflight 正确报告缺 ISO；前端 `npm run build` 通过。
- **后端加单元测试**：builder（Preflight 缺 pack.sh / 缺 ISO 两条失败路径）、storage（存取往返、最新在前排序、日志追加读取）、queue（成功/失败生命周期、重启标记 interrupted、列表排序）。queue 引入 `Runner` 接口，用 fake 替身测试，不依赖真实 xorriso/ISO。
- **`go test -race` 发现并修复两处数据竞争**：① 构建日志 `bytes.Buffer` 被 worker 写 + 轮询读并发访问 → 换成带锁的 `syncBuffer`；② `Get`/`List` 返回活的 `*Job` 指针，外部读取 worker 正在改写的字段 → 改为在锁内返回快照拷贝。修复后 `go test -race ./...` 全绿。
- 最终整机冒烟（更新后二进制）：health/preflight/submit/job/log/download(409) 全部符合预期，任务元数据正确落盘。
- **新增构建输入可见性** `/api/inventory` + 前端"构建输入"卡片：扫描 `template/` 展示源镜像、`debs/` 各分组（deb 数 + `package` 顶层清单）、`drivers/` 文件（名+大小）、离线源索引是否已生成——把 README 里"哪些包进哪个目录"的信息在打包前可视化。附单元测试（扫描/空模板/humanSize；drivers 子目录正确忽略，对齐 pack.sh 的 md5sum 限制）。`go test -race` 全绿，前端构建通过。
- 新增 `Makefile`（backend/frontend/test/vet/build/deps/clean）与 README 运行说明；`/api/inventory` 经 Vite 代理端到端验证通过。至此本地全栈骨架（后端+前端+部署配置+测试+文档）完整可运行。
- **确认云厂商=阿里云、构建范围=通用化、优先=云上真跑一次**（阿里云主机待提供）。
- **新增源 ISO 自动下载** `internal/fetcher`：本地无 `ubuntu*.iso` 时构建先从阿里云镜像下载 Ubuntu 24.04.4（校验 SHA256 `e907d92e...8433`，`.part`+原子改名，已存在且校验通过则复用），进度写入构建日志。`Builder` 集成 `Source`/`WillFetch`/`ensureSourceISO`；`Preflight` 不再因缺 ISO 报错（改由自动下载兜底）；`/api/preflight` 增 `will_fetch` 字段；前端加"将自动下载基础 ISO"蓝色提示。main 增 `-source-iso-url/-name/-sha256`、`-no-fetch` flag。
- 验证：fetcher 单测（校验通过/失败/复用/进度，用本地 httptest）；builder 测试改为反映"配源即可 Preflight 通过 + WillFetch"；`go test -race ./...` 全绿；`/api/preflight` 实测返回 `ready:true,will_fetch:true`；前端构建通过。
- **fetcher 加镜像 fallback**：`Source` 增 `Mirrors []string`，`Fetch` 按序尝试主源→镜像，任一成功即止、全失败才报错。`DefaultSource` 主源阿里云 + 备源南京大学（NJU，实测同版本 SHA256 完全一致）。新增单测：fallback 到镜像成功、全部失败报错。校验过的镜像 URL 与 SHA256 存入记忆 `iso-source-mirrors.md`。
- **修复产物覆盖 bug**：pack.sh 固定写 `template/new-ubuntu.iso`，原设计下第二次构建会覆盖第一次任务的产物、导致下载错文件。storage 增 `isos/` 目录 + `StoreISO`（rename，跨盘回退 copy+remove），队列构建成功后把产物移到 `<data>/isos/<id>.iso`。附单测验证两任务产物互不覆盖。
- **通用构建器** `internal/generic`：任意目录 → ISO（`xorriso -as mkisofs`），支持数据盘/BIOS/UEFI/双引导（可配卷标、引导镜像路径）。`Args` 与 `Run` 分离便于测试；测试实际产出一个 ISO 并用 `xorriso -find` 读回文件校验。（尚未接入队列/前端，作为独立能力先落地并验证。）
- **ISO 自动命名 + 自定义名**：新增 `internal/builder/versions.go`（版本探测 + `DefaultISOName` + `SanitizeISOName`），`/api/default-name` 预览、`POST /api/build` 加 `name` 字段、job 加 `OutputName` 贯穿队列/持久化/下载。前端加 ISO 名称输入框（占位显示自动名，随驱动开关刷新）+ 任务列表显示名称列。附单测（版本探测/默认名全量与去驱动/清理防穿越）。
- 验证：`go build`/`go vet`/`go test -race ./...` 全绿（builder/fetcher/generic/queue/storage）；`/api/default-name` 与自定义/自动命名经 Vite 代理端到端实测正确；前端构建通过。
- **补 API 层测试** `internal/api`（此前只有 curl 冒烟）：用 `httptest` + 真实队列（fake runner）+ 真实 builder（临时模板）覆盖 health/preflight/default-name/build(自动名与自定义名)/download/404。测试暴露并修复**第三处竞态**：`Submit` 返回活 `*Job` 指针，handler 经 `toJobResponse` 读取时 worker 已在改写 → 改为在锁内返回快照（与 `Get`/`List` 一致）。至此后端 6 包共 39 个测试，`-race` 全绿。
- **阿里云部署工具就绪**：`deploy/setup.sh`（一键：装 xorriso/go/node/nginx → 建专用用户 → 编译后端 + 构建前端 → 装 systemd + nginx → 启动，幂等）+ `deploy/README.md`（分步指南：主机规格、驱动大文件放置、首次构建、运维、鉴权/TLS 加固）。`bash -n` 语法检查通过。
- 发现并修复 `go.mod` 与部署脚本的 Go 版本不一致（go.mod 声明 1.27 但脚本装 1.23）→ 因只用到 1.22+ 的方法路由，`go.mod` 降到 `go 1.23`，`go build`/`go test` 仍全绿。
- **通用构建接入命令行** `genpack` 子命令：`isofactory genpack -src <目录> -out <iso> [-volume -boot none|bios|uefi|both -bios-image -efi-image]`，把 `internal/generic` 能力暴露给 CLI（无需 Web 上传流程）。main 改为子命令分发（默认 `serve`，兼容显式 `serve`）。实测：genpack 产出真实 ISO（`ISO 9660 ... 'MYCLI'`，`xorriso -find` 读回文件正确）；默认/显式 serve、genpack 三条入口均正常。

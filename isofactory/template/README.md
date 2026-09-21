# ISO 制作说明与参考手册（template）

本目录是定制 ISO 的**构建骨架 + 知识库**。把服务器侧真实内容同步进来后，运行 `pack.sh` 产出可引导的 `new-ubuntu.iso`。

- 照着做的执行步骤 👉 [`操作步骤.md`](./操作步骤.md)
- 本文档 = 目录结构 / 包清单 / 版本对齐 / pack.sh 解析 / 原理（参考手册）

> 基础镜像：Ubuntu-Server 24.04.4 LTS amd64（内核 6.8.0-111）
> 机型 autoinstall：`QY-B300-IB/user-data`（装机时单独用，不打进 ISO）

---

## 1. 目录结构

### 当前骨架
```
template/
├── pack.sh                 # 打包脚本：解包原 ISO→注入 debs/drivers→拆 CUDA→重封装
├── debs/
│   └── gen_packages.sh     # 生成离线 APT 仓库索引(Packages/Packages.gz)，由服务器侧同步
└── drivers/                # 驱动目录（当前空）
```

### 同步 + 采集完成后的完整形态
```
template/
├── ubuntu-*.iso            # ★ 原版 Ubuntu Server 24.04 ISO（pack.sh 输入，需自行放入）
├── pack.sh
├── debs/                   # 离线 deb 包，按用途分子目录
│   ├── gen_packages.sh
│   ├── <各子目录>/         # base、build-all、doca、nvidia、dcgm-*、linux-tools…
│   │   ├── *.deb
│   │   └── package         # 该目录的顶层包清单（gen_packages 的输入）
│   └── README.MD
├── drivers/
│   ├── cuda_*.run          # ★ 完整 CUDA 安装包（>4GB，未拆；pack.sh 打包时自动拆分）
│   └── [MLNX_*.tgz]        # OFED tgz（可选）
└── new-ubuntu.iso          # ← 产物
```

> ⚠ `drivers/` 按设计只放 `cuda*.run` 和 `MLNX*.tgz` 两类**文件**，不要放子目录——
> `pack.sh` 的 `md5sum ./drivers/*` 遇到子目录会报 `Is a directory` 并因 `set -e` 中断。
> 那些 container/fabricmanager 的 deb 应放 `debs/nvidia/`，不是 `drivers/nvidia/`。

---

## 2. 各文件职责

| 路径 | 职责 | 来源 |
|------|------|------|
| `ubuntu-*.iso` | 原版基础镜像，提供内核/引导/系统树 | 官方下载，自行放入 |
| `pack.sh` | 一键打包：解包→注入→拆 CUDA→`xorriso replay` 重封装 | 已提供 |
| `debs/<dir>/package` | 该目录要下载哪些顶层包（空格分隔） | 服务器侧同步 |
| `debs/<dir>/*.deb` | 采集好的离线包（含依赖闭包） | gen_packages 生成 |
| `debs/gen_packages.sh` | 在 `debs/` 下跑 `dpkg-scanpackages` 生成 `Packages`/`Packages.gz` 索引，把 `debs/` 变成离线 APT 仓库 | 服务器侧同步 |
| `drivers/cuda_*.run` | NVIDIA 驱动 + CUDA 静默安装包 | 自行放入完整文件 |
| `QY-B300-IB/user-data` | 机型 autoinstall 无人值守配置 | 装机时用，不参与打包 |

---

## 3. debs 目录映射（package 清单 = 权威归档表）

`debs/` 下每个子目录带一个 `package` 文件，内容是**喂给 apt 的顶层包名**——采集时据此 `apt install --download-only` 下载并归档到该目录。这就是"哪些包进哪个目录"的权威映射：

| 目录 | `package` 内容 | 装机安装方式 |
|------|---------------|--------------|
| `build-all` | build-essential make gcc debhelper bison flex dkms pkg-config libtool gfortran | `dpkg -i /cdrom/debs/build-all/*.deb` |
| `mellanox` | quilt tk graphviz swig libfuse2t64 chrpath libnl-route-3-dev libnl-3-dev | OFED 编译依赖 |
| `doca` | doca-all | `dpkg -i /cdrom/debs/doca/*.deb` |
| `linux-tools` | linux-tools-common hwdata kdump-tools **linux-tools-$(uname -r)** | 内核工具 |
| `net-tools` | net-tools dnsutils | — |
| `chrony` | chrony | 时间同步 |
| `conntrack` | conntrack | — |
| `dhcp-client` | isc-dhcp-client | — |
| `ipmitool` | ipmitool | 带外管理 |
| `lldpd` | lldpd | `systemctl enable lldpd` |
| `nfs` | nfs | — |
| `socat` | socat | — |
| `sshpass` | sshpass | — |
| `tuned` | tuned | 性能调优 |

> 另有 `base`、`ansible`、`fio`、`ksh`、`iputils-arping`、`squid`、`nvidia`、`dcgm-*` 等目录（部分无 `package`，直接放 deb）。
> 目录名必须与 `user-data` 里 `dpkg -i /cdrom/debs/<目录>/*.deb` 路径一致。

### 3.1 生成离线 APT 仓库索引（gen_packages.sh）

deb 归档好后，在 `debs/` 下运行 `gen_packages.sh` 生成仓库索引：
```bash
dpkg-scanpackages -m . /dev/null > Packages          # 扫描所有 .deb 生成索引（-m 保留多版本）
dpkg-scanpackages -m . /dev/null | gzip -9c > Packages.gz
```
生成的 `Packages`/`Packages.gz` 让 `debs/` 成为可被 apt 消费的**离线源**，装机时可加一行本地源自动解析依赖：
```bash
deb [trusted=yes] file:///cdrom/debs ./
```
💡 比逐目录 `dpkg -i *.deb`（不自动补依赖、装序敏感）更稳；`-m` 保证同名多版本都进索引。

---

## 4. 离线包采集命令与用途

💡 **原理**：在纯净同版本机上 `apt install --download-only`，只会下载"本机尚未安装"的包并连同依赖下到 `/var/cache/apt/archives/`。用干净系统才能保证依赖闭包完整。

### 4.1 通用包（与内核无关）
```bash
apt install --download-only \
  build-essential make gcc debhelper bison flex dkms pkg-config libtool gfortran \
  cron dmidecode dnsutils git ipmitool iputils-ping lldpd net-tools openssh-server \
  rsyslog tcpdump vim ansible conntrack linux-tools-common hwdata kdump-tools \
  nfs-server nfs-client squid tuned dos2unix numactl \
  keyutils rpcbind unzip isc-dhcp-client socat sshpass docker.io chrony
```

### 4.2 内核专属包（必须在 24.04 上跑）
```bash
apt install --download-only linux-tools-$(uname -r)   # 24.04 自动取 6.8.0-111
```
> ⚠ `linux-tools-5.15.0-94-generic` 是 22.04（内核 5.15）的包，**不可用于 24.04**。

### 4.3 运维补充包（★ 对本机型重要）
```bash
apt install --download-only \
  mdadm lvm2 parted gdisk xfsprogs e2fsprogs nvme-cli smartmontools hdparm multipath-tools \
  pciutils usbutils ethtool lm-sensors ipmitool \
  htop iotop sysstat lsof strace ltrace gdb stress-ng \
  traceroute mtr iperf3 nmap netcat-openbsd bridge-utils vlan \
  rsync tree ncdu pigz zip p7zip-full pv \
  tmux screen curl wget jq bash-completion expect \
  python3-pip python3-venv cmake automake autoconf \
  acl uuid-runtime
```
> ⚠ InfiniBand/RDMA 包（rdma-core、ibverbs-utils、infiniband-diags、perftest）**不在此采集**，由 DOCA/OFED 提供（`debs/doca`），apt 源版本会冲突。

### 4.4 OFED 编译依赖（对应 debs/mellanox）
```bash
apt install --download-only quilt tk graphviz swig libfuse2t64 chrpath libnl-route-3-dev libnl-3-dev
```
> ★ `libnl-3-dev`/`libnl-route-3-dev` 是 IB 驱动栈编译硬依赖。
> ⚠ FUSE 包名：24.04 用 `libfuse2t64`，22.04 用 `libfuse2`。

### 4.5 其他补充（可选）
```bash
apt install --download-only \
  ca-certificates gnupg apt-transport-https software-properties-common \
  efibootmgr dosfstools mtools grub2-common \
  psmisc moreutils parallel at logrotate needrestart whois file lsb-release \
  lsscsi sg3-utils iftop nethogs nload bmon dstat nmon memtester freeipmi-tools \
  cifs-utils sshfs autofs nftables iptables \
  python3-dev python3-setuptools valgrind ccache ninja-build meson clang
```
> `nvidia-container-toolkit` 需先加 NVIDIA 容器源，见 `操作步骤.md` 步骤 2c。

### 4.6 包用途速查
| 类别 | 代表包 | 用途 |
|------|--------|------|
| 编译工具链 | build-essential, gcc, gfortran, debhelper, bison, flex, pkg-config, libtool, cmake, meson, ninja-build | 编译源码 / 打 deb / HPC |
| 内核模块/工具 | dkms, linux-tools-common, linux-tools-$(uname -r), kdump-tools | 模块重编、perf、崩溃转储 |
| 硬件/BMC | dmidecode, ipmitool, hwdata, numactl, pciutils, usbutils, ethtool, lm-sensors | 硬件信息 / 带外 / NUMA / 温度 |
| 存储/RAID★ | mdadm, lvm2, parted, gdisk, xfsprogs, nvme-cli, smartmontools, multipath-tools, lsscsi, sg3-utils | RAID/分区/SSD 健康 |
| 网络工具/测试 | net-tools, dnsutils, tcpdump, conntrack, socat, lldpd, traceroute, mtr, iperf3, nmap | 抓包/链路/带宽 |
| 网络服务 | openssh-server, rsyslog, nfs-*, rpcbind, squid, isc-dhcp-client, chrony | SSH/日志/NFS/代理/时间 |
| 自动化/容器 | ansible, docker.io | 批量运维 / 容器 |
| 监控排障 | htop, iotop, sysstat, lsof, strace, gdb, stress-ng, memtester | 性能定位 / 压测 |
| 终端/传输 | tmux, screen, curl, wget, jq, rsync, tree, pv, unzip | 会话/下载/同步 |
| OFED 编译依赖 | quilt, tk, graphviz, swig, libfuse2t64, chrpath, libnl-3-dev, libnl-route-3-dev | IB 驱动构建 |

---

## 5. 驱动 / GPU 版本对齐

### 5.1 CUDA 拆分（原理关键）
`drivers/` 放**完整**的 `cuda_*.run`（>4GB）。pack.sh 打包时 `split -b 3072000000` 切成 `.run`+`.run_a`，装机 `dd oflag=append conv=notrunc` 拼回后 `--silent --toolkit --driver` 安装。

💡 **为什么拆**：ISO9660 单文件上限 **4 GiB**，CUDA 装机包超限，必须切两段、装机拼回。

### 5.2 版本对齐清单（当前基准 595.71.05）
| 组件 | 文件 / 来源 | 版本 | 状态 |
|------|------------|------|------|
| NVIDIA 驱动 | `drivers/cuda_13.2.2_595.71.05_linux.run` | **595.71.05** (CUDA 13.2.2) | ✅ 基准 |
| Fabric Manager（运行时） | `debs/nvidia/nvidia-fabricmanager_595.71.05` | 595.71.05 | ⚠ **当前缺失**（只有 -dev），需补 |
| Fabric Manager-dev | `debs/nvidia/nvidia-fabricmanager-dev_595.71.05` | 595.71.05 | ✅ |
| NSCQ 库 | `debs/dcgm-*/libnvidia-nscq-*` | 580.159.03 | ⚠ **未对齐**，应升 595.71.05 |
| DCGM | `debs/dcgm-*/datacenter-gpu-manager-4-* 4.5.3` | 4.5.3 | ✅ 版本无关 |
| container-toolkit | `debs/nvidia/nvidia-container-toolkit_1.19.0` | 1.19.0 | ✅ 与驱动无关 |
| OFED | `drivers/MLNX_OFED_LINUX-24.04-0.6.6.0-...tgz` | 24.04-0.6.6.0 | ✅ |

💡 **对齐规则**：Fabric Manager 管理 NVSwitch，与驱动**三段号必须完全一致**，否则 `nvidia-fabricmanager.service` 报 `version does not match` 拒绝启动，NVLink 不可用。`libnvidia-nscq` 供 DCGM 查 NVSwitch，同样需匹配。

**待办**：① 补运行时 `nvidia-fabricmanager_595.71.05`（`-dev` 装不出服务单元）；② `nscq` 升到 595.71.05；③ 若目录名 `dcgm-580` 与实际版本不符，同步改名并改 `user-data` 路径。

### 5.3 官方源
```
https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2404/x86_64/
  nvidia-fabricmanager_595.71.05-1ubuntu1_amd64.deb
  libnvidia-nscq-595_595.71.05-1_amd64.deb
```

---

## 6. autoinstall 无人值守（QY-B300-IB/user-data）

💡 **配置与镜像解耦**：`pack.sh` 不把 user-data 打进 ISO。装机时由 nocloud 数据源单独提供（每机型一份），Subiquity 把合并后的配置写到 `/autoinstall.yaml`，脚本 `sed -i .../autoinstall.yaml` 即改此文件。改配置无需重封 ISO。

- **early-commands**：读 BMC IP 末段定主机名/管理网 IP → 自动选盘（300–2000G、排除 U 盘/安装介质、优先 2 块同容量 SATA/SAS 其次 NVMe）→ 清 RAID → 替换 storage 里 DISK0/DISK1 占位符。
- **storage**：两盘 **RAID1** —— `/boot`(md0) + `/`(md1) + boot/EFI(fat32)。
- **late-commands**：注入 SSH 公钥、允许 root 登录、sysctl(ARP/rp_filter)、按目录 `dpkg -i` 离线包、装 DOCA、屏蔽 nouveau + 拼装 CUDA 安装、装 fabricmanager/DCGM 并 enable、写 bond0 netplan、CX5/CX6 网卡 udev 重命名、生成 `config_ipmi.sh`。

---

## 7. pack.sh 逐段解析

| 步骤 | 命令 | 作用 |
|------|------|------|
| 匹配源 ISO | `ISO_FILE_NAME=$(ls\|grep "^ubuntu.*iso$")` | 找原版镜像 |
| 解包 | `xorriso -osirrox on -indev $ISO -extract / ubuntu-files` | 完整解到 `ubuntu-files/` |
| 注入包 | `cp -r debs/ ubuntu-files/` | 离线包进镜像树 |
| 注入驱动 | `cp -r drivers/ ubuntu-files/`（INCLUDE_NVIDIA/OFED=yes） | cuda.run 等进镜像 |
| 校验 | `md5sum ./debs/*/* >> md5sum.txt` | 追加包校验和 |
| 拆 CUDA | `split -b 3072000000` → `.run`+`.run_a`；再 `md5sum ./drivers/*` | 绕过 4GB 限制 |
| 重封装 | `xorriso -indev $ISO -outdev new-ubuntu.iso -boot_image any replay -overwrite on -pathspecs on -map ubuntu-files/ / -commit` | 复用原引导 + 注入新树 |
| 清理 | `find ubuntu-files -delete` / 重命名残留 | 清工作目录 |

💡 **`-boot_image any replay` 是可引导的关键**：从源 ISO 原样复制 El Torito(BIOS)+EFI 引导记录，无需手写 `-b`/`-e` 等引导参数；`-map ubuntu-files/ /` 把修改后的整棵树覆盖进去，保持原版 BIOS+UEFI 双引导。

> ⚠ 已知坑：`md5sum ./drivers/*` 遇到 `drivers/` 下的子目录会报 `Is a directory`，`set -e` 下脚本中断、不产出 ISO。保持 `drivers/` 只放文件即可规避。

---

## 8. 关键原理小结（学习要点）

1. **离线仓库 = 依赖闭包**：纯净机 + `--download-only`，`package` 清单 + `md5sum.txt` 保证可复现可校验。
2. **ISO9660 4GB 单文件限制** → CUDA 必须 split 两段，装机 `dd append` 拼回。
3. **El Torito 引导 replay** → 复用原 BIOS+UEFI 引导，避免手工引导参数出错。
4. **配置与镜像解耦** → user-data 走 nocloud 装机时提供；运行期合并到 `/autoinstall.yaml` 可被 sed 动态改写（读 BMC IP、选盘替换占位符）。
5. **驱动栈版本强绑定** → 驱动/FM/NSCQ 围绕同一版本号，任一错配都会在服务启动或 NVSwitch 查询处暴露。
6. **RAID1 + 动态选盘** → early-commands 先探测真实盘、清 RAID、替换占位符，实现"一份配置适配不同硬盘布局"。

---

## 变更记录
- 2026-08-31：合并原 `镜像制作文档.md` 的包清单大表、版本对齐、pack.sh 解析、原理入本文件；版本基准更新为实际的 595.71.05（CUDA 13.2.2 / OFED 24.04-0.6.6.0）；标注运行时 fabricmanager 缺失、nscq 待升级、drivers 子目录导致 md5sum 中断的坑。仅保留 `README.md` + `操作步骤.md` 两份文档。

# Mac DHCP（dnsmasq）

拷贝本目录到任意 Mac 即可。含 arm64 / x86_64 二进制，无需 Homebrew。

```
DHCP/
├── 打开 Mac DHCP.app    # 双击打开网页控制台（推荐）
├── 打开控制台.command   # 同上（会弹出终端窗口）
├── dhcp.sh              # 命令行入口
├── ui/                  # 可视化控制台
│   ├── server.py        # 本机 HTTP + API（127.0.0.1:8787）
│   ├── index.html
│   ├── app.js
│   └── style.css
├── leases.txt           # 已分配 IP 可读副本
├── config/
│   ├── network.env      # 网段 / 地址池
│   └── dhcp-hosts.conf  # 静态 MAC↔IP（可选）
└── bin/
    ├── arm64/dnsmasq
    └── x86_64/dnsmasq
```

---

## 从 GitLab 克隆后使用

本工具收录在 `tools` 仓库的 `MAC-DHCP/` 子目录下。在新的 Mac 上：

### 1. 克隆并进入目录

```bash
git clone http://218.94.19.74:8081/duanshuailong/tools.git
cd tools/MAC-DHCP
```

> 只想拿这一个工具、不想下整个仓库，可用稀疏检出：
>
> ```bash
> git clone --filter=blob:none --sparse http://218.94.19.74:8081/duanshuailong/tools.git
> cd tools
> git sparse-checkout set MAC-DHCP
> cd MAC-DHCP
> ```

### 2. 补齐可执行权限

git 一般会保留可执行位，但保险起见执行一次（无害）：

```bash
chmod +x dhcp.sh bin/*/dnsmasq "打开控制台.command"
```

> **相比 AirDrop / 下载的好处**：从内网 GitLab `git clone` 下来的文件**不会**被 macOS 打上隔离属性（quarantine），所以通常**不需要**再跑 `xattr -dr com.apple.quarantine`，`.app` / `.command` 双击一般能直接打开。若个别情况仍被 Gatekeeper 拦，再在图标上**右键 → 打开**一次，或执行 `xattr -dr com.apple.quarantine .`。

### 3. 探测网卡并改配置

每台 Mac 网卡名不同，务必重新探测：

```bash
./dhcp.sh detect            # 看「网卡映射」表，找到连客户端的那张网卡（如 en5）
open -e config/network.env  # 改 INTERFACE=enX；NETWORK_SERVICE 建议留空由脚本自动解析
```

### 4. 安装并开启

```bash
sudo ./dhcp.sh install      # 安装 + 应用配置
sudo ./dhcp.sh 开启          # 开启 DHCP
./dhcp.sh status            # 查看状态
```

或直接用可视化控制台：`./dhcp.sh ui`（详见下方「可视化界面」）。

> 后续拉取更新：`cd tools && git pull`。改动本工具后回推：`git add -A && git commit -m "..." && git push`。

---

## 拷贝到其他机器

整个 `DHCP` 文件夹是自包含的，**把文件夹整体拷过去即可**，无需 Homebrew、无需联网安装。

### 必须带上

| 项 | 说明 |
|----|------|
| `dhcp.sh` | 主脚本 |
| `bin/arm64/dnsmasq` + `bin/x86_64/dnsmasq` | **两个架构都要**，脚本自动按芯片选（Apple 芯片走 arm64，Intel 走 x86_64），不用管对方是什么 Mac |
| `ui/`（整个目录） | 可视化控制台，缺了控制台打不开 |
| `config/network.env` | 配置，到新机器要改（见下方步骤） |
| `config/dhcp-hosts.conf` | 静态 MAC↔IP 绑定 |
| `打开控制台.command` 或 `打开 Mac DHCP.app` | 双击启动 UI，二选一即可 |
| `README.md` | 本说明（建议带上） |

### 不要拷贝

- `dhcp.sh.bak` — 旧版本，别误分发。
- `leases.txt` — 上一台机器的租约记录（owner 为 root），拷过去无意义，新机运行会自动生成。
- `.DS_Store` — macOS 系统垃圾文件。

> dnsmasq 运行时会安装到系统目录 `/usr/local/.../mac-dhcp`，那是 `install` 时生成的，**不在本文件夹内，也不用拷**。

### 建议的打包方式（保住可执行权限）

```bash
cd ~/Desktop
zip -r DHCP.zip DHCP -x "DHCP/dhcp.sh.bak" "DHCP/leases.txt" "DHCP/.DS_Store"
```

U 盘 / AirDrop 直接拖文件夹也行；若拖拽后权限丢失，双击 `打开控制台.command` 会自动 `chmod` 修复。

### 到新机器后按此三步

每台 Mac 的网卡名不同，务必重新探测：

```bash
cd /到/DHCP                 # 进入拷贝过去的目录
./dhcp.sh detect            # 1) 看「网卡映射」表，找到连客户端的那张网卡（如 en5）
open -e config/network.env  # 2) 改 INTERFACE=enX；NETWORK_SERVICE 建议留空由脚本自动解析
sudo ./dhcp.sh install      # 3) 安装 + 应用配置
sudo ./dhcp.sh 开启          #    开启 DHCP
```

> **自动适配（无需手改也能装）**：`install` / `start` / `setip` 时若发现 `network.env` 里的 `INTERFACE` 在本机不存在，脚本会**自动挑一张有线网卡**（优先已接通的、跳过 Wi-Fi）并作废对不上的 `NETWORK_SERVICE`。所以拷到其他机器可直接 `install`；想指定具体网卡时再按 `detect` 结果改 `INTERFACE`。
>
> **换机器坑 ①（网卡名）**：`NETWORK_SERVICE` 里残留了上一台机器的服务名（如 `UGREEN`），新机器上没有这个服务 → 设 IP 失败。**留空**最省心，脚本会按 `INTERFACE` 自动解析。详见下方「跨机器网卡适配」。
>
> **换机器第一步（强烈建议）**：下载 / 解压 / AirDrop 后，macOS 会给**整个文件夹**打上隔离属性，可能导致 `.app`、`.command` 双击打不开或被 Gatekeeper 拦。到新机器解压后，先对整个目录清一次（只需一次）：
>
> ```bash
> xattr -dr com.apple.quarantine ~/Downloads/DHCP    # 换成你的实际解压路径
> ```
>
> 脚本每次运行时也会自动清一次整包隔离属性（`heal_bundle`），但它**清不掉“正在启动它自己的那个 .app/.command”**——所以启动器被拦时，仍需上面这条命令，或在图标上**右键 → 打开**一次。
>
> **换机器坑 ②（`Killed: 9`）**：若 `install` 时报 `dnsmasq ... Killed: 9`，是 macOS 拦截了未签名 / 被隔离的二进制（Apple 芯片尤其严格）。`install` 已会自动清隔离属性并补 adhoc 签名；若仍失败，手动执行一次：
>
> ```bash
> sudo xattr -dr com.apple.quarantine bin/*/dnsmasq
> sudo codesign --force --sign - bin/arm64/dnsmasq
> sudo codesign --force --sign - bin/x86_64/dnsmasq
> ```
>
> 用 `zip` 打包（而非直接拖拽/AirDrop）可减少被打隔离属性的概率。

---

## 可视化界面（推荐日常使用）

**双击打开（推荐）：**

- `打开 Mac DHCP.app` — 后台启动并打开浏览器，不挂着终端  
- 或 `打开控制台.command` — 双击后会开一个终端窗口  

若提示无法打开：在图标上 **右键 → 打开** 一次即可。

也可以在终端：

```bash
cd ~/Desktop/DHCP
./dhcp.sh ui
```

访问地址：`http://127.0.0.1:8787/`。若控制台已在运行，再次执行 / 双击只会重新打开浏览器，不会端口冲突。

### 页面能做什么

| 区域 | 作用 |
|------|------|
| 顶部状态 | DHCP 开/关、网卡、链路（已接通/未接通）、实际 IP / 期望 IP、地址池、租约、安装状态；有风险时显示黄色提示 |
| 工具栏 | **开启 / 关闭 DHCP**、**配置网卡 IP**、**安装 / 应用配置**、**探测网卡** |
| 识别到的网卡 | 列表点选 → 自动填入 `INTERFACE` / `NETWORK_SERVICE`（勿选正在上网的 Wi-Fi） |
| 已分配 IP | 租约表，约每 5 秒刷新 |
| 网络参数 | 在线编辑 `network.env`（按网卡 / 本机地址 / DHCP 池分组） |
| 输出 / 日志 | 命令输出；可「拉取日志」看 dnsmasq 日志 |

### UI 推荐操作顺序

1. **探测网卡** 或在列表里点「选用」目标网卡（如 `en5` / UGREEN）。  
2. 在「网络参数」填好 `SERVER_IP`、掩码、地址池等 → **保存配置**。  
3. 点 **配置网卡 IP**（把 `SERVER_IP` 写到网卡；会弹管理员密码）。  
4. 点 **安装 / 应用配置**（首次或改完配置后；会弹密码）。  
5. 点 **开启 DHCP**。  
6. 插上网线，看链路变为「已接通」；客户端拿到地址后，「已分配 IP」会出现租约。

> **保存配置** 只写入本目录 `config/network.env`，不会立刻改系统。  
> 真正落到系统的是：**配置网卡 IP**、**安装 / 应用配置**、**开启 DHCP**。

### UI 注意

- 仅监听本机 `127.0.0.1:8787`，不对外网开放；需要本机 `python3`。  
- 包若在「桌面」，提权时会先同步到 `/tmp/mac-dhcp-ui-bundle` 再执行（避开 macOS 桌面权限限制）。  
- 提权走系统密码框；子命令用英文（`start` / `stop` / `setip`），避免中文参数被破坏。  
- 改完前端后若页面异常，**硬刷新**（⌘⇧R）或重启 `./dhcp.sh ui`（静态资源带版本号如 `?v=4`）。  
- 链路「未接通」时服务仍可开启，但客户端需插线后才能拿地址。

---

## 第一次使用

按下面步骤做一次即可，之后日常只需「开启 / 关闭」（也可用 UI）。

### 1. 进入目录并赋予执行权限

```bash
cd ~/Desktop/DHCP
chmod +x dhcp.sh bin/*/dnsmasq
```

### 2. 查看要用哪张网卡

```bash
./dhcp.sh detect
# 或: ./dhcp.sh ui → 看「识别到的网卡」
```

记下连接客户端设备的那张网卡名（如 `en5`），后面填到配置里。**不要**选已在上网的 Wi-Fi。

### 3. 编辑网络参数

打开并修改 `config/network.env`（见下方「配置文件说明」），或用 UI「网络参数」保存：

```bash
open -e config/network.env
```

改完后必须再 **安装 / 应用配置**（或 CLI `install`）才会进系统侧配置。

（可选）固定某台设备的 IP：编辑 `config/dhcp-hosts.conf`。

### 4. 给网卡写静态 IP（建议单独做一次）

```bash
sudo ./dhcp.sh setip
# 或 UI：「配置网卡 IP」
```

网线未插时也可能写入成功，但链路会显示 inactive / 未接通。

### 5. 安装到系统（仅首次 / 改配置后）

```bash
sudo ./dhcp.sh install
```

会安装 dnsmasq、写入配置、尝试设静态 IP，并注册服务（**默认不开机自启**）。

### 6. 开启 DHCP

```bash
sudo ./dhcp.sh 开启
# 或: sudo ./dhcp.sh start / on
./dhcp.sh status
```

### 7. 客户端验证

把设备接到同一网络（交换机或直连网线），网卡设为自动获取 IP，应能分到地址池内的地址。

查看已分配 IP：

```bash
./dhcp.sh leases
# 或 UI「已分配 IP」，或打开本目录 leases.txt
```

### 8. 用完关闭

```bash
sudo ./dhcp.sh 关闭
# 或: sudo ./dhcp.sh stop / off
```

---

## 日常命令

```bash
./dhcp.sh ui                       # 可视化控制台
sudo ./dhcp.sh setip               # 仅给网卡配置 SERVER_IP
sudo ./dhcp.sh 开启                 # 或: on / start
sudo ./dhcp.sh 关闭                 # 或: 关机 / off / stop
./dhcp.sh status
./dhcp.sh leases
sudo ./dhcp.sh uninstall --purge   # 彻底卸载系统侧文件
```

改过 `config/network.env` 后，需要再执行：

```bash
sudo ./dhcp.sh install
sudo ./dhcp.sh 开启
```

（UI 对应：保存配置 → 安装 / 应用配置 → 开启 DHCP；若只改了本机 IP，可再点「配置网卡 IP」。）

---

## 配置文件说明

### `config/network.env`（必改）

| 参数 | 作用 | 填写说明 | 示例 |
|------|------|----------|------|
| `INTERFACE` | 在哪张网卡上提供 DHCP | 填 **Device** 名（`en5`），用 `detect` / UI 列表看映射；**不要**填 Wi-Fi | `en5` |
| `NETWORK_SERVICE` | （可选）网络服务名 | 一般留空由脚本自动解析（如 en5→UGREEN）；自动失败时再填 | `UGREEN` |
| `SERVER_IP` | 本机（DHCP 服务器）在该网卡上的静态 IP | `setip` / `install` / 开启时会尝试写入；**不要**落在地址池内 | `10.144.31.254` |
| `NETMASK` | 子网掩码 | 与网段匹配 | `255.255.255.0` |
| `NET_ADDRESS` | 网段地址（对照用） | 写成网段起始，如 `10.144.31.0`，**不是**主机 IP | `10.144.31.0` |
| `RANGE_START` | 动态地址池起始 | 客户端自动获取时从这里开始分配 | `10.144.31.172` |
| `RANGE_END` | 动态地址池结束 | 与 START 组成可分配区间；勿包含 `SERVER_IP` | `10.144.31.200` |
| `ROUTER` | 下发给客户端的默认网关 | 网关是本机时填 `SERVER_IP`；否则填真实网关 | `10.144.31.254` |
| `DNS_SERVERS` | 下发给客户端的 DNS | 多个用空格分隔，**必须加引号** | `"8.8.8.8 1.1.1.1"` |
| `DOMAIN_NAME` | 域名后缀 | 可保持默认 | `lan` |
| `LEASE_TIME` | 地址租约时长 | 如 `12h`、`24h`、秒数 | `12h` |
| `RUN_AT_LOAD` | 是否开机自启 | `false` = 不自启，需手动开启 | `false` |
| `ENABLE_DNS` | 是否启用 dnsmasq 内置 DNS | 推荐 `false`（只做 DHCP，不抢系统 53 端口） | `false` |
| `DNS_PORT` | 内置 DNS 端口 | `ENABLE_DNS=false` 时保持 `0` | `0` |
| `LOG_DHCP` | 是否记录 DHCP 详细日志 | `true` / `false` | `true` |

**相互约束（重要）：**

1. `SERVER_IP`、`ROUTER`、`RANGE_*` 必须在同一网段（由 `NETMASK` 决定）。  
2. `SERVER_IP` **不能**落在 `RANGE_START`～`RANGE_END` 之间。  
3. 静态绑定的 IP（见下）建议放在地址池**之外**。  
4. 修改本文件后：`install`（或 UI「安装 / 应用配置」）→ 再开启；改本机 IP 时建议再跑一次 `setip`。

### 跨机器网卡适配（重要）

macOS 上同一块网卡有三个名字，常不一致：

| 概念 | 例子 | 谁用 |
|------|------|------|
| Device | `en5` | `INTERFACE`、dnsmasq、`ifconfig` |
| 网络服务名 | `UGREEN` | `networksetup -setmanual`（设静态 IP） |
| Hardware Port | `USB 10/100/1G/2.5G LAN` | 系统硬件列表（**不能**直接当服务名） |

换 Mac / 换 USB 网卡时：

1. 先跑 `./dhcp.sh detect` 或打开 UI 网卡列表，看 **Device → 网络服务**。  
2. `network.env` 只改 `INTERFACE=enX` 即可，脚本会按 Device 自动解析服务名。  
3. 若设 IP 仍失败，把映射表里的服务名写入：

```bash
NETWORK_SERVICE=UGREEN
```

完整示例：

```bash
INTERFACE=en5
# NETWORK_SERVICE=UGREEN   # 可选；自动解析失败再填
SERVER_IP=10.144.31.254
NETMASK=255.255.255.0
NET_ADDRESS=10.144.31.0
RANGE_START=10.144.31.172
RANGE_END=10.144.31.200
ROUTER=10.144.31.254
DNS_SERVERS="8.8.8.8 1.1.1.1"
DOMAIN_NAME=lan
LEASE_TIME=12h
RUN_AT_LOAD=false
ENABLE_DNS=false
DNS_PORT=0
LOG_DHCP=true
```

### `config/dhcp-hosts.conf`（可选）

给指定设备固定 IP（MAC ↔ IP）：

```text
# 格式：MAC,IP[,主机名]
00:11:22:33:44:55,10.144.31.10,printer1
aa:bb:cc:dd:ee:ff,10.144.31.11,camera01
```

改完后同样需要重新 `install` 并开启。

### `leases.txt`（可读副本）

真实租约由 dnsmasq 写在 `/usr/local/var/mac-dhcp/dnsmasq.leases`（系统服务不能直接写 Desktop）。  
执行 `./dhcp.sh leases` 或 `status` 时会同步到本目录 **`leases.txt`**。  
格式：`<过期epoch> <MAC> <IP> <主机名> <客户端ID>`

---

## 注意

- 提供 DHCP 的网卡须为静态 IP（`setip` / `install` / 开启时会尝试自动设置；UI 也可单独「配置网卡 IP」）
- 同一局域网不要开多个 DHCP（含路由器自带的 DHCP）
- 默认只做 DHCP（`ENABLE_DNS=false`），不抢系统 53 端口
- 默认不开机自启（`RUN_AT_LOAD=false`），需手动开启
- `install` 只会写入 `/usr/local/.../mac-dhcp` 与专用 LaunchDaemon，可用 `uninstall --purge` 清干净
- USB 网卡链路 inactive 时：IP 仍可写上，但客户端需插线后才能拿到租约

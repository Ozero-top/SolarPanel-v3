<div align=center>
<img src="https://updates.ozero.top/SolarPanel.ico" width="64" height="64"> 
</div>

<h1 align=center>SolarPanel V3 - 轻量自托管导航页面板</h1>

[![Version](https://img.shields.io/badge/Version-v3.3.8-5B8DEF?logo=semanticrelease)](#)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](https://go.dev)
[![Architecture](https://img.shields.io/badge/Architecture-chi%2Bsqlite-50C878)](https://github.com/go-chi/chi)
[![Multi-Arch](https://img.shields.io/badge/Arch-amd64__arm64__armv7-7A51F5)](https://hub.docker.com/r/ovitor/solarpanel-v3)
[![Image](https://img.shields.io/badge/Docker-14MB-2496ED?logo=docker)](https://hub.docker.com/r/ovitor/solarpanel-v3)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

> 轻量自托管导航面板。Go 纯后端 · 零 CGO 单二进制 · 前端内嵌 · 三架构 Docker 镜像约 14MB。

- GitHub：<https://github.com/Ozero-top/SolarPanel-v3>
- Docker Hub：<https://hub.docker.com/r/ovitor/solarpanel-v3>
- DEMO：<https://demo.ozero.top>

## ✨ 特性

- **零依赖单二进制** — Go + 纯 Go SQLite（`modernc.org/sqlite`），无 CGO、无外部数据库、无 PHP；三架构镜像约 14MB，单文件即可运行
- **空库启动** — 首次启动仅建表 + 默认设置 + 管理员账号，不注入任何预置内容；「恢复初始状态」同样只清数据，不生成演示项
- **两级分组导航** — 父分组折叠、子分组缩进；实时搜索按「卡片 → 子分组 → 父分组」三级命中，排序与显隐仅在同级生效
- **十套主题** — 液态玻璃（默认）/ 柔和浮雕 / 工程蓝图 / 清透极简 / 柔和工具 / 动态极光 / IRIS 深空 / 铅笔手绘 / 复古怀旧 / 企业简洁；明暗模式、卡片样式与圆角留白可独立配置
- **热点新闻聚合** — 24 个内置源（微博 / 知乎 / B站 / GitHub Trending / IT之家 / V2EX / 少数派 / 掘金 等）+ 自定义 RSS，支持勾选、排序与独立入口
- **NAS 运行状态面板** — 主机名 / 内网 IP / CPU / 内存 / 存储池 / 网络 / 温度 / 容器；四种放置方式 + 近 10 分钟曲线 + 详情弹窗；兼容群晖 DSM、绿联 UGOS、飞牛 fnOS、威联通 QTS、极空间 ZOS、iStoreOS / OpenWrt / ImmortalWrt
- **天气与壁纸** — 顶部时钟与天气组件；每日壁纸多源合并、失败重试、图库管理与上传；后台可按源随机预览 10 张再固定；内置壁纸只读不可删除，删除操作仅作用于自己上传的壁纸
- **内外网自动切换** — 卡片可同时配置公网与内网地址，页面按访问者网段或后台配置的内网主机名自动选路，顶栏按钮可手动切换
- **字体系统** — 页面字体与时钟/天气字体各 12 款免费开源设计字体，按需加载、互不干扰，清空即跟随系统
- **搜索增强** — 多搜索引擎切换、搜索历史与常用搜索（置顶收藏）、快捷键 `/` 或 `Ctrl+K` 聚焦搜索框
- **卡片与图标** — 图标上传、域名自动抓取 favicon、标题与描述自动获取；深色模式自动适配图标轮廓；右键菜单可新标签页 / 当前页打开、快速编辑或删除
- **键盘快捷键** — `/`、`Ctrl+K` 聚焦搜索，`t` 切深浅色，`n` 新闻视图，`b`/`h` 导航站视图，`g` 分组目录，`?` 帮助，`Esc` 逐级退出
- **favicon 三级缓存** — 内存 inflight 去重 → 磁盘 MD5 缓存 → 三源并发抓取（主站 / Google s2 / DuckDuckGo）；`ETag` + `immutable` 协商，二次访问 304
- **安全** — Session + CSRF 双令牌、接口限流、四级角色权限、TOTP 双因子、受信设备与审计日志
- **PWA** — Service Worker 离线缓存，可安装为应用；资源变更自动刷新缓存版本
- **有序幂等迁移 + 旧库兼容** — `schema_versions` 记录已应用版本；启动时另按 `PRAGMA table_info` 检测并补齐旧库缺失字段，避免 `no such column` 直接 500
- **备份与迁移** — JSON 一键导出/导入（含上传文件 base64），分组按 `parent_id` 排序并重建 ID 映射；支持 Chrome / Edge 书签 HTML 导入

## 🏗 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.25 · [chi/v5](https://github.com/go-chi/chi) v5.4.0 · [modernc.org/sqlite](https://modernc.org/sqlite) v1.34.5 |
| 前端 | 原生 JS · CSS 变量主题系统 · PWA Service Worker |
| 构建 | Node 24（生成主题 CSS + 注入 SHA1 资源戳） · Go embed |
| 部署 | Docker 三阶段构建 · Alpine 3.22 |

直接依赖仅 3 个：

```
github.com/go-chi/chi/v5   — HTTP 路由 + 中间件
golang.org/x/crypto        — bcrypt / sha256 / TOTP
modernc.org/sqlite         — 纯 Go SQLite driver（零 CGO）
```

## 🚀 快速开始

### 方式一：Docker（推荐）

镜像 `ovitor/solarpanel-v3:latest`，支持 `linux/amd64` · `linux/arm64` · `linux/arm/v7`。

#### docker run

```bash
docker run -d \
  --name solarpanel-v3 \
  --restart unless-stopped \
  -p 10086:10086 \
  -v $(pwd)/data:/app/data \
  -v /:/host:ro \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e SERVER_PORT=10086 \
  -e NAS_HOST_ROOT=/host \
  -e DOCKER_SOCKET=/var/run/docker.sock \
  ovitor/solarpanel-v3:latest
```

> 后两个挂载仅用于 **NAS 运行状态面板**（读取宿主 /proc、/sys、存储池与容器数据）。不需要该功能时删掉 `-v /:/host:ro`、`-v /var/run/docker.sock:...` 与两个 `-e` 即可，面板会自动隐藏。

Windows PowerShell：

```powershell
docker run -d --name solarpanel-v3 --restart unless-stopped -p 10086:10086 -v ${PWD}/data:/app/data -v /:/host:ro -v /var/run/docker.sock:/var/run/docker.sock -e SERVER_PORT=10086 -e NAS_HOST_ROOT=/host -e DOCKER_SOCKET=/var/run/docker.sock ovitor/solarpanel-v3:latest
```

#### 本地构建

```bash
git clone https://github.com/Ozero-top/SolarPanel-v3.git
cd SolarPanel-v3
docker build -t solarpanel-v3:local .
docker run -d --name solarpanel-v3 -p 10086:10086 -v $(pwd)/data:/app/data solarpanel-v3:local
```

#### docker compose

```yaml
services:
  solarpanel:
    image: ovitor/solarpanel-v3:latest   # 本地构建改成 build: .
    container_name: solarpanel-v3
    restart: unless-stopped
    ports:
      - "10086:10086"
      # - "18443:18443"                  # HTTPS_PORT 非空时启用
    volumes:
      - ./data:/app/data
      - /:/host:ro                              # NAS 监控：宿主 /proc、/sys、存储池
      - /var/run/docker.sock:/var/run/docker.sock   # NAS 监控：容器列表与占用
    environment:
      - TZ=Asia/Shanghai
      - SERVER_PORT=10086
      - NAS_HOST_ROOT=/host
      - DOCKER_SOCKET=/var/run/docker.sock
      # - HTTPS_PORT=18443               # 启用内置自签名 HTTPS
    # NAS 监控需要「整机网卡」口径时改用 host 网络（ports 映射会失效，直接访问 10086）：
    # network_mode: host
```

```bash
docker compose up -d          # 启动
docker compose logs -f        # 查看日志
docker compose down           # 停止
docker compose up -d --build  # 重新构建
```

启动后访问 <http://localhost:10086>。数据目录 `./data/` 自动创建，包含 `solarpanel.db` 与 `uploads/`。

> 镜像内**未声明 `VOLUME`**，挂载到 `/app/data` 即持久化；不挂载则重建容器后数据随之丢失。

#### ARM 设备 / OpenWrt 系（iStoreOS · ImmortalWrt）

适用于 iStoreOS、ImmortalWrt 与带 Docker 的原生 OpenWrt（ARMv7 / ARM64），镜像会按设备架构自动匹配。先在「应用商店 / 软件包」中安装 Docker（iStoreOS 为「iStore 应用商店 → Docker」），再执行：

```bash
docker run -d --name solarpanel-v3 --restart unless-stopped \
  -p 10086:10086 \
  -v /volume1/docker/solarpanel/data:/app/data \
  -v /:/host:ro \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e NAS_HOST_ROOT=/host -e DOCKER_SOCKET=/var/run/docker.sock \
  ovitor/solarpanel-v3:latest
```

- 数据目录按设备实际存储位置填写，例如 `/mnt/sda1/docker/solarpanel/data` 或 `/overlay/docker/solarpanel/data`
- 面板会自动识别 OpenWrt 系系统名与 `/overlay` 可用容量；不需要容器列表时可去掉 `docker.sock` 挂载与 `DOCKER_SOCKET`
- 需要整机网速口径时追加 `--network host` 并去掉 `-p 10086:10086`，直接访问 `http://设备IP:10086`
- 低配设备（内存 ≤ 512MB）建议把后台「刷新间隔」调到 5~10 秒，减少采集开销

### ⚠️ 关于端口 10086

早期版本默认使用 `10080`，但该端口在 Chromium 的**受限端口列表**（`net/base/port_util.cc` 的 `kRestrictedPorts`，10080 属于 Amanda 备份服务）中，浏览器会直接以 `ERR_UNSAFE_PORT` 拦截，页面表现为「打不开」。因此默认端口改为 `10086`。

若需换端口，修改 `SERVER_PORT` 与端口映射即可，但需避开 Chromium 受限端口（完整列表见 Chromium 源码 `net/base/port_util.cc` 中的 `kRestrictedPorts`）。

### 📊 NAS 运行状态面板（可选）

在后台 **站点设置 → 🖥️ NAS 监控** 开启后，主页展示宿主机与容器状态：

| 放置方式 | 说明 |
|---|---|
| 右侧悬浮（默认） | 贴内容区右侧，可收起为竖条 |
| 左侧悬浮 | 贴内容区左侧，与左侧分组目录互斥 |
| 左右双侧 | 左主机指标 / 右容器列表，两侧同步收起 |
| 卡片下方状态条 | 单行概览 + CPU 迷你曲线，整条可点开详情 |

移动端（≤768px）统一自动使用状态条；任一形式都可通过「详情」打开完整弹窗（存储池、近 10 分钟网络曲线、容器端口 / CPU / 内存 / 网络 / 运行时长）。

数据来源与限制：

- 主机指标读取挂载的宿主 `/proc`、`/sys`、`/etc`：CPU、内存、负载、运行时长、存储池、温度
- 容器指标通过 `/var/run/docker.sock` 读取（无 Docker 时只显示主机信息，不影响使用）
- **系统识别**：群晖 DSM、绿联 UGOS、飞牛 fnOS、威联通 QTS、极空间 ZOS、iStoreOS / OpenWrt / ImmortalWrt，其余归为 Linux
- **存储自动识别**：`/volume*`、`/vol*`、`/share/CACHEDEV*_DATA`、`/mnt/*`、`/media/*`、`/overlay`；特殊路径可在后台手填
- **网络口径**：默认 bridge 网络下只能统计容器网卡，面板会标注「容器网卡」；需要整机速率请改用 `network_mode: host`
- **温度**：物理机正常读取 thermal/hwmon；虚拟机（KVM/ESXi）不暴露传感器，面板显示「虚拟机」
- 磁盘与温度依赖 `/:/host:ro` 挂载；未挂载时相应字段为空，不影响其他功能

### 方式二：单二进制

```bash
# 交叉编译（产物为静态链接，无运行时依赖）
CGO_ENABLED=0 GOOS=linux   go build -ldflags="-s -w" -o solarpanel .
CGO_ENABLED=0 GOOS=darwin  go build -ldflags="-s -w" -o solarpanel .
CGO_ENABLED=0 GOOS=windows go build -ldflags="-s -w" -o solarpanel.exe

# 运行（当前工作目录下自动创建 data/）
./solarpanel
```

> 程序以**当前工作目录**为数据根：`data/solarpanel.db`、`data/uploads/`。

### 方式三：源码开发

```bash
# 1. 生成前端产物（Node 24+）
node scripts/build-themes.js      # themes.json → themes.css + 主题白名单
node scripts/build-frontend.js    # 注入 SHA1 资源戳 + 生成 sw.js 版本

# 2. 后端直接跑（前端已 //go:embed all:frontend）
go run .
```

## ⚙️ 环境变量

全部可选，未设置时使用默认值：

| 变量 | 默认 | 说明 |
|---|---|---|
| `SERVER_PORT` | `10086` | HTTP 监听端口 |
| `NAS_HOST_ROOT` | `/host`（不存在则留空） | NAS 监控读取的宿主机根目录；容器内由 `-v /:/host:ro` 提供，单二进制运行留空即读本机 |
| `DOCKER_SOCKET` | `/var/run/docker.sock` | Docker 守护进程 socket，用于容器列表与占用 |
| `HTTPS_PORT` | （空，不启用） | HTTPS 端口，设置后自动生成自签名证书并同时监听 |
| `CERT_FILE` | `data/cert.pem` | HTTPS 证书路径 |
| `KEY_FILE` | `data/key.pem` | HTTPS 私钥路径 |

数据库路径、上传目录、Session 有效期（24h）、Cookie 名（`sp_session` / `csrf_token`）在 `internal/config/config.go` 中固定，不通过环境变量覆盖。

## 🔑 默认账号与角色

| 角色 | 权限 |
|---|---|
| `admin` | 全部功能，含用户管理、审计清理、备份恢复 |
| `editor` | 内容管理（分组 / 卡片 / 上传 / 导入），不可管理用户 |
| `viewer` | 只读，可查看后台不可修改 |
| `guest` | 访客，仅主页展示；后台开启「访客访问」后凭访客密码进入 |

- 初始管理员：`admin` / `admin123`，**首次登录后请立即修改密码**
- 支持 TOTP 双因子（`auth.php?action=2fa_setup / 2fa_enable / 2fa_disable`）与受信设备管理
- 系统保证至少保留一个 `admin`，不允许降级/删除最后一个管理员

## 📁 项目结构

```
SolarPanel/
├── main.go                      # 入口：DB 初始化 + 路由注册 + HTTP/HTTPS 服务 + 预置壁纸落地
├── go.mod / go.sum
├── Dockerfile                   # 三阶段：Node 前端 → Go builder → Alpine 运行时
├── docker-compose.yml
├── scripts/
│   ├── build-themes.js          # themes.json → themes.css + 主题白名单
│   └── build-frontend.js        # SHA1 资源戳注入 · sw.js 版本号
├── frontend/                    # 前端源码（构建产物 embed 进二进制）
│   ├── themes.json              # 主题配置唯一事实源
│   ├── index.html / admin.html / login.html / sw.js
│   ├── uploads/weather/         # 7 张预置天气壁纸（首次启动复制到 data/uploads/weather）
│   └── assets/
│       ├── css/                 # common.css · index.css · themes.css · auth-admin.css
│       └── js/
│           ├── api.js           # APP_VERSION + APP_CHANGELOG + 请求封装
│           ├── index.js         # 主页逻辑（public.php → render）
│           ├── admin.js         # 后台管理
│           └── login.js
├── internal/
│   ├── config/config.go         # 环境变量 → Config
│   ├── settings/defaults.go     # 设置默认值唯一事实源
│   ├── auth/                    # Session · CSRF · RateLimit · Audit · TOTP · RequireLogin
│   ├── db/
│   │   ├── db.go                # DSN/连接池 · DDL · 旧库补列 · 默认设置/管理员初始化 · 孤儿分组清理
│   │   └── migrations.go        # schema_versions 有序幂等迁移
│   ├── handler/                 # 各业务 handler（见「路由一览」）
│   └── nas/                     # NAS 采集：宿主 /proc、/sys、存储池、Docker API、系统识别
├── presets/                     # 示例备份数据（仅仓库提供，不参与构建与镜像）
│   └── nav-cards-backup.json    # 通用导航卡片：10 个分组 / 150 张卡片
└── data/                        # 运行时数据（自动创建，勿提交）
    ├── solarpanel.db            # SQLite 数据库
    └── uploads/                 # icons / logos / wallpapers / favicon / weather
```

## 🗄️ 数据库

- **类型**：SQLite（WAL 模式），`modernc.org/sqlite` 纯 Go driver
- **连接池**：`SetMaxOpenConns(15)`；`journal_mode=WAL` · `synchronous=NORMAL` · `busy_timeout=15000` · `foreign_keys=ON` · `cache_size=-20000` · `temp_store=MEMORY`
  - 这些 PRAGMA 通过 DSN 的 `_pragma=` 参数下发，**每条连接独立生效**；若只在启动时 `DB.Exec` 一次，池内其余连接仍是默认值（`busy_timeout=0`），并发下会直接 `SQLITE_BUSY` 导致接口 500
  - 启动日志会打印实际生效的 `journal_mode`；若显示 `delete` 而非 `wal`，说明数据目录所在文件系统（SMB / NFS 等网络盘）不支持 WAL
- **表**：`users` · `settings` · `item_groups` · `items` · `custom_feeds` · `audit_logs` · `schema_versions` · `rate_limits` · `trusted_devices`
- **迁移**：`schema_versions` 记录已应用版本；新增 DDL/DML 追加到 `migrations` 列表末尾，`Version` 必须严格递增且不得修改历史项
- **旧库补列**：`CREATE TABLE IF NOT EXISTS` 对已存在的表不生效，迁移也只覆盖少数几列。启动时会执行 `ensureColumns()`，按 `PRAGMA table_info` 比对 `items` / `item_groups` / `users` / `settings` / `audit_logs` / `custom_feeds` 的期望列，缺失即 `ALTER TABLE ADD COLUMN` 补齐（幂等、保留原有数据）；补列会打印 `[db] 旧库补列 表.列 ✅`
- **初始化**：空库只写入 `internal/settings/defaults.go` 的默认设置与 `admin/admin123`
- **重置**：后台「恢复初始状态」= 恢复默认设置 + 清空分组/卡片 + 清空用户上传（保留 `uploads/weather` 预置壁纸），**不会重新生成任何演示内容**
- **孤儿分组清理**：启动时若 `parent_id` 指向不存在的分组，自动重置为 `0`（顶级）

## 🎨 主题定制

主题唯一事实源为 `frontend/themes.json`，当前十套：`liquid-glass`（默认，仅浅色）、`soft`、`blueprint`、`clarity`、`soft-utility`、`aurora`、`iris`、`sketch-style`、`retro-vintage` 与 `corporate-clean`。

```json
{
  "theme_id": {
    "display": "theme_id",
    "light": { "--css-var": "value" },
    "dark":  { "--css-var": "value" },
    "extra_css": ["额外 CSS 片段"]
  }
}
```

新增主题后执行 `node scripts/build-themes.js`，自动同步：

- `frontend/assets/css/themes.css`（主题样式）
- `api.js` 中的主题白名单
- `settings_handler.go` 的取值白名单 `settingValueWhitelist`（需手动追加主题名）

默认明暗模式由设置项 `default_theme`（`light` / `dark`）控制，主题风格由 `theme_style` 控制。

## 🔌 路由一览

所有接口挂在 `/api/` 下，历史原因保留 `.php` 后缀。

### 公共路由（无需登录）

| 方法 | 路径 | 限流 | 说明 |
|---|---|---|---|
| GET | `/api/public.php` | 60/min | 主页数据：设置 + 分组 + 卡片 + 站点信息 |
| GET | `/api/install.php` | — | 首次安装状态 |
| POST | `/api/install.php` | — | 执行首次安装 |
| GET | `/api/weather.php` | 10/min | 天气数据 |
| GET / POST | `/api/news.php` | 30/min | 新闻聚合调度 |
| GET | `/api/news/meta` | 30/min | 新闻源元信息列表 |
| GET | `/api/news/all` | — | 全部源聚合结果 |
| GET | `/api/news/source` | — | 单源新闻 |
| POST | `/api/wallpaper.php` | 10/min | 每日壁纸调度 |
| GET / POST | `/api/auth.php` | — | 登录 / 登出 / 双因子 / 受信设备 / 访客登录 |
| GET | `/api/favicon.php?host=xxx` | 120/min | 站点图标代理（三源抓取 · 磁盘缓存 · ETag 协商） |
| GET | `/api/nas.php` | 240/min | NAS 主机与容器状态（需后台开启；访客可见性由设置控制） |
| GET | `/api/version.php` | 30/min | 版本检查：返回当前 / 最新版本与是否有更新；`?refresh=1` 跳过缓存（后台「立即检查」用） |

### 登录后路由（`RequireLogin` + CSRF）

| 方法 | 路径 | `action` | 说明 |
|---|---|---|---|
| GET / POST | `/api/settings.php` | `list` / `save` | 设置读取与保存 |
| GET / POST | `/api/groups.php` | `list` `edit` `delete` `sort` `sort_batch` `visible` | 分组 CRUD、排序、显示隐藏 |
| GET / POST | `/api/items.php` | `list` `edit` `delete` `sort` `sort_batch` `toggle_hidden` `favicon` `fetch_meta` | 卡片 CRUD、排序、隐藏、抓图标、抓标题 |
| POST | `/api/upload.php` | `type=icon\|logo\|wallpaper` | 图标 5MB / Logo 5MB / 壁纸 10MB |
| GET / POST | `/api/gallery.php` | GET 列表 · POST `delete` | 壁纸图库（用户 `wallpapers` + 预置 `weather`）；预置项只读，删除仅对用户上传生效 |
| GET / POST | `/api/backup.php` | `export` / `import` / `reset` | 备份导出、导入、恢复初始状态 |
| GET / POST | `/api/user.php` | `list` `create` `update` `delete` `change_password` `set_role` `set_password` | 用户管理 |
| GET / POST | `/api/audit.php` | `list` / `clear` | 审计日志 |
| GET / POST | `/api/feeds.php` | `list` `save` `delete` `toggle` | 自定义 RSS 源 |
| POST | `/api/import.php` | `bookmarks` | 浏览器书签 HTML 导入 |

### 页面与静态资源

| 路径 | 说明 |
|---|---|
| `GET /` | 主页（`frontend/index.html`） |
| `GET /login.html` | 登录页；已登录非 guest 会被重定向到 `/admin.html` |
| `GET /admin.html` | 后台页；未登录重定向到 `/login.html`，guest 角色禁止访问 |
| `GET /favicon.ico` | 站点图标 |
| `/uploads/*` · `/frontend/uploads/*` | 用户上传目录（磁盘直读） |
| 其他路径 | embed 静态资源兜底（`/assets/...`） |

响应统一格式：`{"code": 0, "data": ..., "msg": ""}`，`code != 0` 表示业务失败。

## 🛠️ 后台功能

后台 6 个主模块：

| 模块 | 能力 |
|---|---|
| **设置** | 站点信息（标题 / Logo / 公告 / 备案 / 内外网访问）、外观与卡片（主题 + 明暗 + 卡片样式 + 布局留白 + 页面字体 + 时钟天气字体 + 壁纸）、导航与搜索（顶栏开关 + 搜索栏 + 搜索引擎清单与默认引擎）、访问控制（访客密码）、热点新闻（开关 + 24 源勾选排序 + 自定义 RSS）、NAS 监控（开关 / 放置方式 / 刷新间隔 / 访客可见 / 自定义存储路径 / 弹窗不透明度） |
| **卡片** | 新增/编辑/删除、拖拽排序、显示隐藏、图标上传或自动抓取、标题自动抓取 |
| **分组** | 两级分组 CRUD、拖拽排序、显示隐藏、图标与描述 |
| **账号** | 修改本人密码、TOTP 双因子、受信设备管理；管理员可增删用户与改角色 |
| **备份** | 导出 JSON、导入 JSON、恢复初始状态；书签 HTML 导入 |
| **审计** | 操作日志分页查看、按需清空（保留策略由 `auth.AuditCleanup` 每小时执行） |

前端体验：后台保存后，已打开的主页通过 `localStorage` 的 storage 事件自动拉新数据重渲染，无需手动刷新。

## 💾 备份格式

`GET /api/backup.php?action=export` 返回：

```json
{
  "version": "1.0",
  "time": "2026-10-23 19:34:00",
  "settings": [{ "id": 1, "config_name": "site_title", "config_value": "SolarPanel" }],
  "groups": [{ "id": 1, "parent_id": 0, "title": "...", "icon": "...", "description": "...", "sort": 1, "is_visible": 1, "user_id": 1, "created_at": "" }],
  "items": [{ "id": 1, "group_id": 1, "title": "...", "url": "...", "lan_url": "", "description": "...", "icon_type": "image", "icon_value": "...", "icon_bg": "", "open_method": 2, "sort": 1, "is_hidden": 0, "user_id": 1, "created_at": "" }],
  "files": { "wallpapers/xxx.jpg": "<base64>" }
}
```

- `files` 的键是**相对 `data/uploads` 的路径**；`weather/` 下的预置壁纸不导出；单个文件超过 20MB 跳过
- 默认以 JSON 下载，导入同时兼容「base64 包裹的 JSON」与纯 JSON

导入规则：

1. `version` 仅作记录，不做严格校验；缺失的 `settings` / `groups` / `items` 视为空集
2. 导入是**整体替换**：先清空 `items` / `item_groups` / `settings`，再按备份重建；用户账号与已有上传文件不受影响
3. 分组先按 `parent_id` 升序处理，保证父分组先落库，再建立 `旧 ID → 新 ID` 映射；找不到父分组映射的子分组降级为顶级
4. 卡片的 `group_id` 经映射重写；所属分组映射不存在的卡片会被**直接跳过**
5. `files` 写入前校验路径（拒绝 `..`、限制在 `uploads` 内），`.svg` 会先做安全清洗，同名覆盖

## 🧩 示例数据（可选导入）

`presets/nav-cards-backup.json` 是一份**通用导航站配置备份**，包含 **10 个分组 / 150 张卡片**（AI 工具、视频、社交、开发、云盘等常用站点），可用于快速建站或演示：

1. 后台 → **系统备份 → 导入配置**，选择该文件
2. 导入为整体替换，会覆盖当前分组 / 卡片 / 设置，操作前请先导出自己的备份
3. 文件不含账号、访客密码与上传图片，导入后用户账号不受影响
4. 导入后可按需修改分组名称、卡片地址与图标

> `presets/` 仅随仓库提供，已通过 `.dockerignore` 排除，不参与 Docker 构建，也不会打入镜像。

## 🧪 开发注意事项

- **WAL 快照隔离**：`DB.Exec` 归还连接池后再 `DB.QueryRow` 可能落到另一条连接，读不到刚写入的数据。写后读**必须放在同一个 `db.Tx` 内**（`groups_handler` 的保存与 `items_handler` 的 `toggle_hidden` 已按此实现）
- **嵌套查询死锁**：SQLite 连接池较小时，`rows.Next()` 遍历期间再发起 `QueryRow` 会阻塞。代码统一先收集结果并 `rows.Close()` 后再查
- **列与 Scan 必须一一对应**：`scanItemGroup` 有 9 个目标（含 `parent_id`），SELECT 少一列会让 `Scan` 报错并被误读为「记录不存在」
- **`schema_versions` 是 OTA 关键表**，禁止手动删除；历史迁移条目不可修改
- **新增列必须同步三处**：`schemaDDL`、`expectedColumns`（旧库补列）与 `migrations` 追加项；只改建表语句对已部署的旧库无效
- **迁移要校验结果**：`ALTER TABLE ... ADD COLUMN` 遇到「列已存在」会报错，不要把该错误当成成功并打标，否则会出现「日志显示已迁移、实际缺列」的假象
- **SQLite PRAGMA 走 DSN**：`DB.Exec("PRAGMA ...")` 只影响连接池中的一条连接，新增连接仍用默认值；需要全局生效的参数一律通过 `buildDSN()` 的 `_pragma=` 传入
- **设置默认值只改 `internal/settings/defaults.go`**，`db.go` 初始化与 `backup_handler.go` 重置必须同源，避免两处漂移
- **新增主题**只改 `frontend/themes.json` 并跑 `build-themes.js`，不要手改 `themes.css`
- **前端资源变更**后必须跑 `build-frontend.js`，否则浏览器仍命中旧 SHA1 缓存
- **Docker 重建**：`docker compose down && docker compose up -d --build`；若镜像未更新，加 `--no-cache` 排除 buildx 缓存干扰

## 📋 更新日志

完整变更明细见 [updates.md](./updates.md)。维护约定：每次升版只更新 `updates.md` 与 `frontend/assets/js/api.js` 的 `APP_VERSION` / `APP_CHANGELOG`，README 不再维护版本条目。

## ⬆️ 从 V2.1 系列升级到 V3

V3 不自动迁移 V2.1 的数据库，请按「先导出、后导入」的顺序升级：

1. 在 **V2.1 系列后台 → 系统备份 → 导出配置**，保存导出的备份文件
2. 部署 V3（建议使用新的数据目录），首次启动为空库，使用 `admin` / `admin123` 登录
3. 在 **V3 后台 → 系统备份 → 导入配置**，选择第 1 步的备份文件完成恢复

注意事项：

- 导入为**整体替换**：V3 现有的分组、卡片与设置会被备份内容覆盖
- 备份中的上传文件（图标 / Logo / 壁纸）随备份一起导入，无需单独拷贝 `uploads`
- 升级后立即修改默认管理员密码；旧版浏览器缓存建议强制刷新一次

> 直接让 V3 复用旧数据目录时，启动会尝试补齐旧库缺失字段以兼容读取；但旧版字段语义可能不同（例如隐藏标记、图标字段命名），**推荐仍按上面的「导出 → 导入」流程升级**，以保证分组、卡片与设置完整还原。

## 📝 License

MIT

<div align=center>
<img src="https://github.com/Ozero-top/SolarPanel-v3/blob/main/frontend/assets/img/icon-192.png" width="64" height="64"> 
</div>

<h1 align=center>SolarPanel V3 - 轻量自托管导航页面板</h1>

[![Version](https://img.shields.io/badge/Version-v3.3.9-5B8DEF?logo=semanticrelease)](#)
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

- **零依赖单二进制** — Go + 纯 Go SQLite（`modernc.org/sqlite`），无 CGO、无外部数据库；三架构镜像约 14MB
- **空库启动** — 首次启动只建表、写默认设置与管理账号，不注入演示内容；「恢复初始状态」同样只清数据
- **两级分组导航** — 父分组折叠、子分组缩进；搜索按「卡片 → 子分组 → 父分组」三级命中
- **十套主题** — 液态玻璃（默认）/ 柔和浮雕 / 工程蓝图 / 清透极简 / 柔和工具 / 动态极光 / IRIS 深空 / 铅笔手绘 / 复古怀旧 / 企业简洁；明暗模式、卡片样式与圆角留白独立配置
- **热点新闻聚合** — 24 个内置源 + 自定义 RSS，支持勾选、排序与独立入口
- **NAS 运行状态面板** — 主机名 / 内网 IP / CPU / 内存 / 存储池 / 网络 / 温度 / 容器；四种放置方式 + 近 10 分钟曲线 + 详情弹窗
- **天气与壁纸** — 顶部时钟与天气；每日壁纸多源合并、失败重试、图库管理与上传
- **内外网自动切换** — 卡片可同时配置公网与内网地址，按访问者网段自动选路，顶栏可手动切换
- **字体与搜索** — 页面字体与时钟/天气字体各 12 款开源字体；多引擎切换、搜索历史与常用搜索、`/` 或 `Ctrl+K` 聚焦
- **卡片与图标** — 图标上传、favicon 自动抓取、标题与描述自动获取；右键菜单可快速编辑或删除
- **键盘快捷键** — `t` 深浅色，`n` 新闻，`b` / `h` 导航站，`g` 分组目录，`?` 帮助，`Esc` 逐级退出
- **剪藏（Clipper）** — 扩展 / iOS 快捷指令 / Android PWA 分享 / 网页入口共用 `/api/v1/clips`；服务端抓取标题、图标与封面，支持正文快照、全文检索、PDF 文本快照、AI 摘要与自动标签、整页截图、标签与访问令牌
- **安全** — Session + CSRF 双令牌、接口限流、四级角色权限、TOTP 双因子、受信设备与审计日志
- **PWA** — Service Worker 离线缓存，可安装为应用，并提供 Android 分享目标
- **备份与迁移** — JSON / HTML 双格式导出、自动与手动备份、Chrome / Edge 书签 HTML 导入

## 🏗 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.25 · [chi/v5](https://github.com/go-chi/chi) v5.4.0 · [modernc.org/sqlite](https://modernc.org/sqlite) v1.34.5 |
| 剪藏解析 | go-readability（正文提取）· html-to-markdown · ledongthuc/pdf（PDF 文本）· SQLite FTS5（全文索引） |
| 前端 | 原生 JS · CSS 变量主题系统 · PWA Service Worker |
| 构建 | Node 24（生成主题 CSS、注入 SHA1 资源戳、打包扩展）· Go embed |
| 部署 | Docker 三阶段构建 · Alpine 3.22 |

`go.mod` 直接依赖仅 3 个：`chi/v5`（路由）、`golang.org/x/crypto`（bcrypt / TOTP）、`modernc.org/sqlite`（纯 Go driver）；readability、html-to-markdown、pdf 等均为间接依赖。

## 🚀 快速开始

### 方式一：Docker（推荐）

镜像 `ovitor/solarpanel-v3:latest`，支持 `linux/amd64` · `linux/arm64` · `linux/arm/v7`。

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

> 后两个挂载仅供 **NAS 运行状态面板**（默认关闭，需在后台「站点设置 → NAS 监控」开启）。不需要时可删除 `-v /:/host:ro`、`-v /var/run/docker.sock:...` 与对应两个 `-e`。

Windows PowerShell 只需把 `$(pwd)` 换成 `${PWD}`。

`docker compose`（`docker-compose.yml` 已在仓库中）：

```yaml
services:
  solarpanel:
    image: ovitor/solarpanel-v3:latest
    container_name: solarpanel-v3
    restart: unless-stopped
    ports:
      - "10086:10086"
    volumes:
      - ./data:/app/data
      - /:/host:ro                                   # NAS 监控：宿主 /proc、/sys、存储池
      - /var/run/docker.sock:/var/run/docker.sock     # NAS 监控：容器列表
    environment:
      - TZ=Asia/Shanghai
      - SERVER_PORT=10086
      - NAS_HOST_ROOT=/host
      - DOCKER_SOCKET=/var/run/docker.sock
```

```bash
docker compose up -d          # 启动（首次可加 --build 本地构建）
docker compose logs -f        # 查看日志
docker compose down           # 停止
```

启动后访问 <http://localhost:10086>。数据目录 `./data/` 自动创建（`solarpanel.db`、`backups/`、`uploads/`），挂载到 `/app/data` 即持久化。

ARM / OpenWrt 系（iStoreOS · ImmortalWrt）安装 Docker 后沿用上面命令，只需把数据目录换成实际存储路径（如 `/mnt/sda1/docker/solarpanel/data` 或 `/overlay/docker/solarpanel/data`）；需要整机网速口径时改用 `--network host` 并去掉端口映射。

> **端口说明**：早期默认端口 `10080` 属于 Chromium 受限端口，浏览器会直接以 `ERR_UNSAFE_PORT` 拦截，因此改为 `10086`。换端口时同样需避开 Chromium 的 `kRestrictedPorts`。

### 方式二：单二进制

```bash
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o solarpanel .   # 也可替换 GOOS 为 darwin / windows
./solarpanel                                                        # 数据写入当前目录 data/
```

### 方式三：源码开发

```bash
node scripts/build-themes.js      # themes.json → themes.css
node scripts/build-frontend.js    # 注入 SHA1 资源戳 + sw.js 版本
go run .                          # 前端通过 //go:embed 打包进二进制
```

### NAS 运行状态面板（可选）

后台 **站点设置 → 🖥️ NAS 监控** 开启后，主页展示主机与容器状态，可选择「右侧悬浮 / 左侧悬浮 / 左右双侧 / 卡片下方状态条」，移动端统一使用状态条，均可打开详情弹窗。

- 主机指标读取挂载的宿主机 `/proc`、`/sys`、`/etc`：CPU、内存、负载、存储池、温度
- 容器指标来自 `/var/run/docker.sock`，无 Docker 时仅显示主机信息
- 自动识别群晖 DSM、绿联 UGOS、飞牛 fnOS、威联通 QTS、极空间 ZOS、iStoreOS / OpenWrt；存储自动识别 `/volume*`、`/vol*`、`/share/CACHEDEV*_DATA`、`/mnt/*`、`/media/*`、`/overlay`
- bridge 网络下只统计容器网卡；需要整机速率请用 `network_mode: host`
- 磁盘与温度依赖 `/:/host:ro` 挂载，未挂载时相应字段为空

### 一键发版

`scripts/release.ps1` 会同步版本号 → 构建校验 → 提交推送 → 打 tag → 创建 GitHub Release（附扩展 zip 与 `SHA256SUMS.txt`）→ 推送多架构镜像。

```powershell
.\scripts\release.ps1 3.3.9
```

发版前在 `updates.md` 顶部写好对应版本小节，并保持工作区干净、当前分支为 `main`。

## ⚙️ 环境变量

全部可选，未设置时使用默认值：

| 变量 | 默认 | 说明 |
|---|---|---|
| `SERVER_PORT` | `10086` | HTTP 监听端口 |
| `NAS_HOST_ROOT` | `/host`（目录不存在则留空） | NAS 监控读取的宿主机根目录 |
| `DOCKER_SOCKET` | `/var/run/docker.sock` | Docker socket，用于容器列表 |
| `HTTPS_PORT` | （空，不启用） | 设置后自动生成自签名证书并同时监听 |
| `CERT_FILE` / `KEY_FILE` | `data/cert.pem` / `data/key.pem` | HTTPS 证书与私钥路径 |

数据库、上传与备份目录、Session 有效期、Cookie 名均在 `internal/config/config.go` 中固定；剪藏与备份等业务开关都在后台设置中。

## 🔑 默认账号与角色

| 角色 | 权限 |
|---|---|
| `admin` | 全部功能，含用户管理与系统备份 |
| `editor` | 内容管理（分组 / 卡片 / 上传 / 书签导入 / 剪藏与令牌） |
| `viewer` | 只读，可查看后台不可修改 |
| `guest` | 访客，仅主页展示；后台开启「访客访问」后凭密码进入 |

- 初始管理员：`admin` / `admin123`，**首次登录后请立即修改密码**
- 支持 TOTP 双因子与受信设备，系统保证至少保留一个 `admin`
- 剪藏接口仅 `admin` / `editor` 可用

## 📁 项目结构

```
SolarPanel/
├── main.go                      # 入口：DB 初始化 + 路由注册 + HTTP/HTTPS 服务
├── Dockerfile                   # 三阶段：Node 前端 → Go builder → Alpine 运行时
├── docker-compose.yml
├── clipper-extension/           # Chrome / Edge 剪藏扩展源码（MV3）
├── scripts/
│   ├── build-themes.js          # themes.json → themes.css
│   ├── build-frontend.js        # SHA1 资源戳 · sw.js 版本号
│   ├── build-clipper.js         # 扩展打包 → dist/*.zip + SHA256SUMS.txt
│   └── release.ps1              # 一键发版
├── frontend/                    # 前端源码（embed 进二进制）
│   ├── themes.json              # 主题唯一事实源
│   ├── index.html / admin.html / login.html
│   ├── clip.html / clip-read.html / offline.html / manifest.json / sw.js
│   ├── uploads/weather/         # 预置天气壁纸
│   └── assets/{css,js}/         # common、index、themes、admin、clip 等
├── internal/
│   ├── config/                  # 环境变量 → Config
│   ├── settings/                # 设置默认值唯一事实源
│   ├── auth/                    # Session · CSRF · RateLimit · Audit · TOTP · Token
│   ├── db/                      # DSN / DDL / 旧库补列 / 有序迁移
│   ├── handler/                 # 业务 handler（含 clip_handler / clip_content / clip_token 等）
│   ├── urlutil/                 # 链接规范化与查重键
│   └── nas/                     # NAS 采集：/proc、/sys、存储池、Docker API
├── presets/                     # 示例备份（不参与构建与镜像）
├── dist/                        # 扩展打包产物（.gitignore 排除）
└── data/                        # 运行时数据（自动创建，勿提交）
    ├── solarpanel.db
    ├── backups/auto|manual/     # 各保留 5 份
    └── uploads/                 # icons / logos / wallpapers / favicon / weather / clips/shots
```

## 🗄️ 数据库

- **SQLite（WAL 模式）**，`modernc.org/sqlite` 纯 Go driver，连接池 `SetMaxOpenConns(15)`
- PRAGMA 通过 DSN 的 `_pragma=` 下发，每条连接独立生效（`journal_mode=WAL` · `synchronous=NORMAL` · `busy_timeout=15000` · `foreign_keys=ON`）；启动日志打印实际 `journal_mode`，非 `wal` 说明数据目录所在文件系统（SMB / NFS 等）不支持
- **表**：
  - 导航与系统：`users` · `settings` · `item_groups` · `items` · `custom_feeds` · `audit_logs` · `schema_versions` · `rate_limits` · `trusted_devices`
  - 剪藏：`api_tokens`（`token_plain` 明文便于再复制，校验用 `token_hash`）· `clip_requests`（24 小时幂等）· `clip_contents`（正文快照，含 `summary` / `auto_tags` / `screenshot_path`）· `item_tags` · `clip_fts`（FTS5 虚表）
- **迁移**：`schema_versions` 记录已应用版本，当前 v1–v8；新增条目追加到末尾，`Version` 严格递增且不得修改历史项
- **全文索引**：`clip_fts` 优先 trigram（中文可用），不支持时回退 `unicode61`；`EnsureClipFTS()` 启动时比对并重建；检索词少于 3 个字符退回 `LIKE`
- **旧库兼容**：`ensureColumns()` 按 `PRAGMA table_info` 补齐缺失列，避免 `no such column` 直接 500
- **启动自愈**：`BackfillURLKeys()` 补算查重键、`EnsureClipFTS()` 校验索引、孤儿分组重置为顶级
- **定时任务**：审计日志清理、剪藏幂等记录清理（每小时）、自动备份
- **重置**：后台「恢复初始状态」清空分组 / 卡片 / RSS / 剪藏快照 / 标签 / 令牌与用户上传（保留 `uploads/weather`），不生成演示内容

## 🎨 主题定制

主题唯一事实源为 `frontend/themes.json`，当前十套：`liquid-glass`（默认）、`soft`、`blueprint`、`clarity`、`soft-utility`、`aurora`、`iris`、`sketch-style`、`retro-vintage`、`corporate-clean`。

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

新增主题后执行 `node scripts/build-themes.js`，自动同步 `themes.css`、`api.js` 的 `SP_STYLES` 白名单与 `admin.html` 下拉；另需把主题名加入 `settings_handler.go` 的 `settingValueWhitelist`。默认明暗模式由 `default_theme` 控制，风格由 `theme_style` 控制。

## 🔌 路由一览

导航接口挂在 `/api/` 下（历史原因保留 `.php` 后缀），剪藏接口为 `/api/v1/`。响应统一为 `{"code": 0, "data": ..., "msg": ""}`，`code != 0` 表示业务失败。

### 公共路由（无需登录）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/public.php` | 主页数据：设置 + 分组 + 卡片 + 站点信息（60/min） |
| GET / POST | `/api/install.php` | 安装状态 / 默认管理员提示 |
| GET | `/api/weather.php` | 天气数据（30/min） |
| GET / POST | `/api/news.php` · `/api/news/meta` · `/api/news/all` · `/api/news/source` | 新闻聚合与源数据（30/min） |
| GET / POST | `/api/wallpaper.php` | 每日壁纸调度（`daily` / `preview` / `pin` / `unpin`，30/min） |
| GET / POST | `/api/auth.php` | 登录 / 双因子 / 访客登录 / 登出 / 受信设备 |
| GET | `/api/favicon.php?host=xxx` | 图标代理（三源抓取 · 磁盘缓存 · ETag，120/min） |
| GET | `/api/nas.php` | NAS 主机与容器状态（需后台开启，240/min） |
| GET | `/api/version.php` | 版本检查（`?refresh=1` 跳过缓存，30/min） |

### 剪藏 API v1（Bearer Token 或登录会话）

CORS 允许 `*`，`OPTIONS` 直接返回 204；IP 限流 120/min，Bearer 另有 60/min 的令牌限流。Bearer 请求不走 Cookie / CSRF，会话写操作需带 `X-CSRF-Token`。

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/clips` | 新建剪藏；`url` 必填，可选 `title` / `note` / `group_id` / `capture` / `force` / `client_id` |
| DELETE | `/api/v1/clips` | 撤销剪藏（`{"id":N}`），仅本人或管理员 |
| GET | `/api/v1/clips/search?q=&limit=&offset=` | 正文快照全文检索 |
| GET | `/api/v1/clips/{id}` | 卡片详情 + 快照 + 标签 |
| POST | `/api/v1/clips/{id}/refetch` | 重新抓取正文 |
| POST | `/api/v1/clips/{id}/screenshot` | 上传整页截图（≤15MB） |
| DELETE | `/api/v1/clips/{id}/content` | 删除快照与截图（保留卡片） |
| GET | `/api/v1/me` · `/api/v1/groups` | 当前身份 / 可见分组 |
| POST | `/api/v1/preview` | 只抓元数据不写库 |
| GET / POST / DELETE | `/api/v1/tokens` | 令牌列表（含明文）/ 创建 / 吊销（物理删除） |

### 登录后路由（`RequireLogin`，写操作校验 CSRF）

| 方法 | 路径 | `action` |
|---|---|---|
| GET / POST | `/api/settings.php` | `list` / `save`（`save` 仅 `admin`） |
| GET / POST | `/api/groups.php` | `list` `edit` `delete` `sort` `sort_batch` `visible` |
| GET / POST | `/api/items.php` | `list` `edit` `delete` `sort` `sort_batch` `toggle_hidden` `favicon` `fetch_meta` |
| POST | `/api/upload.php` | `type=icon\|logo\|wallpaper`（5MB / 5MB / 10MB） |
| GET / POST | `/api/gallery.php` | 列表 / `delete`（预置项只读） |
| GET / POST | `/api/backup.php` | `export` `import` `reset` `create` `restore` `delete` 等（仅 `admin`） |
| GET / POST | `/api/user.php` | 用户管理（`change_password` 本人可用，其余仅 `admin`） |
| GET / POST | `/api/audit.php` | `list` / `clear`（仅 `admin`） |
| GET / POST | `/api/feeds.php` | `list` `save` `delete` `toggle` |
| POST | `/api/import.php` | 浏览器书签导入（`merge` / `overwrite`）/ JSON 导入 |

### 页面与静态资源

| 路径 | 说明 |
|---|---|
| `GET /` | 主页 |
| `GET /login.html` · `GET /admin.html` | 登录页 / 后台页（按登录态互相重定向） |
| `GET /clip` · `GET /clip-share` | 剪藏网页入口 / Android PWA 分享目标 |
| `GET /read/{id}` | 正文快照阅读页（需登录） |
| `/uploads/*` | 用户上传目录（CSP `sandbox` 限制脚本） |
| 其他路径 | embed 静态资源兜底，后台与登录页保持登录保护 |

## 🛠️ 后台功能

| 模块 | 能力 |
|---|---|
| **站点设置** | 六个分区：站点信息、外观与卡片（主题 / 明暗 / 字体 / 壁纸）、导航与搜索、访问控制、热点新闻、NAS 监控 |
| **卡片** | CRUD、拖拽排序、显隐、图标上传或自动抓取、标题自动获取、标签维护（≤20 个） |
| **分组** | 两级分组 CRUD、排序、显隐、图标与描述；页面底部为浏览器书签导入 |
| **账号** | 修改密码、TOTP 双因子、受信设备；管理员可增删用户与改角色 |
| **系统备份**（仅管理员） | JSON / HTML 导出与导入、恢复初始状态、自动与手动备份（各保留 5 份） |
| **剪藏与接入** | 五个分区：基础设置 / 摘要与图片 / 快照检索 / 令牌与地址 / 各端接入 |
| **审计日志** | 分页查看与清空 |

后台保存后，已打开的主页通过 `storage` 事件自动重渲染。剪藏在后台标注「尚未经过完整测试」，问题可通过 GitHub Issues 反馈。

## 💾 备份格式

`GET /api/backup.php?action=export&format=json`（默认）返回：

```json
{
  "version": "1.0",
  "time": "2026-10-29 12:00:00",
  "settings": [{ "id": 1, "config_name": "site_title", "config_value": "SolarPanel" }],
  "groups": [{ "id": 1, "parent_id": 0, "title": "...", "icon": "...", "description": "...", "sort": 1, "is_visible": 1, "user_id": 1 }],
  "items": [{ "id": 1, "group_id": 1, "title": "...", "url": "...", "lan_url": "", "description": "...", "icon_type": "image", "icon_value": "...", "open_method": 2, "sort": 1, "is_hidden": 0 }],
  "feeds": [{ "id": 1, "title": "...", "url": "...", "sort": 1, "enabled": 1 }],
  "url_keys": { "1": "example.com/article" },
  "tokens": [{ "id": 1, "user_id": 1, "name": "...", "token_hash": "...", "token_plain": "...", "scopes": "clip:write", "expires_at": "" }],
  "clip_contents": [{ "item_id": 1, "url_key": "...", "excerpt": "...", "content_md": "...", "summary": "...", "auto_tags": "a,b" }],
  "item_tags": [{ "item_id": 1, "tag": "..." }],
  "files": { "wallpapers/xxx.jpg": "<base64>" }
}
```

- `format=html` 导出 Netscape 书签格式，仅含可见分组与未隐藏卡片
- `files` 为相对 `data/uploads` 的路径，单个文件超过 20MB 跳过；导入时跳过 `weather/` 前缀，保留预置壁纸
- 导入同时兼容纯 JSON 与 base64 包裹的 JSON，上限 128MB

导入规则：`version` 仅作记录；导入为**整体替换**（用户账号不受影响）；分组按 `parent_id` 顺序落库并建立 ID 映射，父分组缺失的子分组降级为顶级；卡片 `group_id`、快照与标签的 `item_id` 均按映射重写，映射失败则跳过；`url_key` 缺失时按 URL 现算；导入后重建全文索引。

备份文件位于 `data/backups/auto/` 与 `data/backups/manual/`，各保留最新 5 份。

## 🧲 浏览器扩展（剪藏）

源码位于 `clipper-extension/`（MV3，仅申请 `activeTab` / `scripting` / `contextMenus` / `storage`），打包产物在 [Releases](https://github.com/Ozero-top/SolarPanel-v3/releases/latest)（`solarpanel-clipper-v1.0.0.zip` 与 `SHA256SUMS.txt`），也可运行 `node scripts/build-clipper.js` 生成到 `dist/`。

**安装**：解压 zip 到固定目录 → 打开 `chrome://extensions` 或 `edge://extensions` → 开启「开发者模式」→「加载已解压的扩展程序」。

**配置**：后台「剪藏与接入 → 令牌与地址」生成 Token → 扩展「打开设置」填写站点地址、Token 与默认分组 → 可勾选「同时保存正文快照」「同时保存整页截图」。

**使用**：

- 点击图标确认标题 / 分组 / 备注后保存；勾选截图时自动滚动逐屏拼接，失败降级为可视区截图
- 右键菜单可保存页面 / 链接 / 选中文本；快捷键 `Alt+Shift+S` 保存当前页
- 断网时最多缓存 50 条，下次启动自动重试；重复链接可选择打开已有卡片或「仍要另存」
- 错误码：401 Token 失效 / 403 无权限或剪藏已关闭 / 429 请求过于频繁

Token 仅保存在本机 `chrome.storage.local`，扩展不收集、不上报数据。Firefox 仅代码兼容，未签名扩展重启后失效。

## 📱 其他剪藏入口

| 入口 | 说明 |
|---|---|
| 网页快速添加 `/clip` | 粘贴链接自动补全标题并选择分组，断网写入 IndexedDB 队列 |
| Android 系统分享 | Chrome 打开本站并「添加到主屏幕」后出现在分享面板（HTTP 局域网通常不支持） |
| iOS 快捷指令 | `POST /api/v1/clips`，请求头 `Authorization: Bearer <Token>`，请求体 `{"url":"...","title":"..."}` |
| 书签小工具 | 后台「各端接入」提供书签代码，点击打开 `/clip` |
| 阅读页 `/read/{id}` | 查看正文快照、摘要、标签与截图，可重抓或删除（需登录） |

## 🧩 示例数据（可选导入）

`presets/nav-cards-backup.json` 为通用导航站备份（10 个分组 / 150 张卡片），可在 **系统备份 → 导入配置** 中整体替换当前配置；文件不含账号与图片，用户账号不受影响。该目录不参与 Docker 构建与镜像。

## 🧪 开发注意事项

- **写后读放同一事务**：`DB.Exec` 归还连接后再 `QueryRow` 可能落到另一条连接，读不到刚写入的数据
- **避免嵌套查询死锁**：`rows.Next()` 遍历期间不要发起新的查询，先收集结果并 `rows.Close()`
- **列与 Scan 一一对应**：SELECT 少一列会让 `Scan` 报错并被误读为「记录不存在」
- **`schema_versions` 禁止手动删除**，历史迁移条目不可修改
- **新增列同步三处**：`schemaDDL`、`expectedColumns`、`migrations`；`ALTER TABLE` 报「列已存在」不能当作成功
- **PRAGMA 走 DSN**：`DB.Exec("PRAGMA ...")` 只影响一条连接，全局参数一律由 `buildDSN()` 下发
- **FTS5 同步**：增删快照都要更新 `clip_fts`，改索引列结构需删表重建（启动时由 `EnsureClipFTS()` 兜底）
- **默认值只改 `internal/settings/defaults.go`**；新增主题只改 `themes.json` 并跑 `build-themes.js`，另需更新 `settingValueWhitelist`
- **前端资源变更后跑 `build-frontend.js`**，扩展变更后跑 `build-clipper.js` 并同步 `manifest.json` 版本

## 📋 更新日志

完整变更见 [updates.md](./updates.md)。每次升版更新 `updates.md` 与 `frontend/assets/js/api.js` 的 `APP_VERSION` / `APP_CHANGELOG`，README 只保留顶部版本徽章。

## ⬆️ 从 V2.1 升级到 V3

1. 在 V2.1 后台「系统备份 → 导出配置」保存备份
2. 部署 V3（建议使用新数据目录），用 `admin` / `admin123` 登录
3. 在 V3 后台「系统备份 → 导入配置」选择备份文件恢复

导入为整体替换，上传文件随备份一并导入。直接复用旧数据目录虽会补齐缺失字段，但旧字段语义可能不同，仍建议按上述流程升级。

## 📝 License

MIT

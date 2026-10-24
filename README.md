<div align=center>
<img src="https://updates.ozero.top/SolarPanel.ico" width="64" height="64"> 
</div>

<h1 align=center>SolarPanel V3 - 轻量自托管导航面板</h1>

[![Version](https://img.shields.io/badge/Version-v3.2.3-5B8DEF?logo=semanticrelease)](#)
[![Go](https://img.shields.io/badge/Go-1.23-00ADD8?logo=go)](https://go.dev)
[![Architecture](https://img.shields.io/badge/Architecture-chi%2Bsqlite-50C878)](https://github.com/go-chi/chi)
[![Multi-Arch](https://img.shields.io/badge/Arch-amd64__arm64__armv7-7A51F5)](https://hub.docker.com/r/ovitor/solarpanel-v3)
[![Image](https://img.shields.io/badge/Docker-14MB-2496ED?logo=docker)](https://hub.docker.com/r/ovitor/solarpanel-v3)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

> 轻量自托管导航面板。Go 纯后端 · 零 CGO 单二进制 · 前端内嵌 · 三架构 Docker 镜像约 14MB。

- GitHub：<https://github.com/Ozero-top/SolarPanel-v3>
- Docker Hub：<https://hub.docker.com/r/ovitor/solarpanel-v3>
- DEMO：<https://demo.ozero.top>

## ⬆️ 从 V2.1 系列升级到 V3

V3 不自动迁移 V2.1 的数据库，请按「先导出、后导入」的顺序升级：

1. 在 **V2.1 系列后台 → 系统备份 → 导出配置**，保存导出的备份文件
2. 部署 V3（建议使用新的数据目录），首次启动为空库，使用 `admin` / `admin123` 登录
3. 在 **V3 后台 → 系统备份 → 导入配置**，选择第 1 步的备份文件完成恢复

注意事项：

- 导入为**整体替换**：V3 现有的分组、卡片与设置会被备份内容覆盖
- 备份中的上传文件（图标 / Logo / 壁纸）随备份一起导入，无需单独拷贝 `uploads`
- 升级后立即修改默认管理员密码；旧版浏览器缓存建议强制刷新一次

## ✨ 特性

- **零依赖单二进制** — Go + `modernc.org/sqlite`（纯 Go SQLite 实现），无 CGO、无外部数据库、无 PHP
- **空库启动** — 首次启动只建表 + 写入默认设置 + 管理员账号，**不注入任何预置分组与卡片**，内容完全由用户自建
- **两级分组导航** — 支持父子嵌套，侧边栏自动展开/折叠，实时搜索过滤
- **十主题** — `liquid-glass` 液态玻璃（默认 · 磨砂透光 · 流动光斑 · 仅浅色）· `soft` 柔和浮雕 · `blueprint` 工程蓝图 · `clarity` 清透极简 · `soft-utility` 柔和工具 · `aurora` 动态极光 · `iris` 深空 HUD · 铅笔手绘 · `retro-vintage` 复古怀旧（羊皮纸 · 粗棕框 · 衬线大写）· `corporate-clean` 企业简洁（白卡细边 · 蓝主色 · 焦点环）
- **热点新闻聚合** — 24 个内置源（微博 / 知乎 / B站 / GitHub Trending / IT之家 / V2EX / 少数派 / 掘金 等）+ 自定义 RSS
- **天气 + 每日壁纸** — 顶部时钟、天气组件；壁纸源支持 `bing_cn`（默认）/ `picsum`
- **卡片图标智能获取** — 手输域名自动抓取 favicon，也可上传本地图标
- **favicon 三级缓存** — 内存 inflight 去重 → 磁盘 MD5 缓存 → 三源并发抓取（主站 `favicon.ico` / Google s2 / DuckDuckGo），响应 `ETag` + `Cache-Control: immutable`，二次访问 304 零 body
- **安全** — Session + CSRF 双令牌 + 接口限流 + 角色权限 + TOTP 双因子 + 受信设备 + 审计日志
- **PWA** — Service Worker 离线缓存，可安装为应用
- **有序幂等迁移** — `schema_versions` 表追踪，新增 schema 变更只需在迁移列表末尾追加
- **备份迁移** — 一键导出/导入 JSON（含上传文件 base64），分组按 `parent_id` 排序 + 新旧 ID 重映射
- **浏览器书签导入** — 直接上传 Chrome/Edge 导出的 `bookmarks.html`

## 🏗 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.23 · [chi/v5](https://github.com/go-chi/chi) v5.2.1 · [modernc.org/sqlite](https://modernc.org/sqlite) v1.34.5 |
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
  -e SERVER_PORT=10086 \
  ovitor/solarpanel-v3:latest
```

Windows PowerShell：

```powershell
docker run -d --name solarpanel-v3 --restart unless-stopped -p 10086:10086 -v ${PWD}/data:/app/data -e SERVER_PORT=10086 ovitor/solarpanel-v3:latest
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
    environment:
      - TZ=Asia/Shanghai
      - SERVER_PORT=10086
      # - HTTPS_PORT=18443               # 启用内置自签名 HTTPS
```

```bash
docker compose up -d          # 启动
docker compose logs -f        # 查看日志
docker compose down           # 停止
docker compose up -d --build  # 重新构建
```

启动后访问 <http://localhost:10086>。数据目录 `./data/` 自动创建，包含 `solarpanel.db` 与 `uploads/`。

> 镜像内**未声明 `VOLUME`**，挂载到 `/app/data` 即持久化；不挂载则重建容器后数据随之丢失。

### ⚠️ 关于端口 10086

早期版本默认使用 `10080`，但该端口在 Chromium 的**受限端口列表**（`net/base/port_util.cc` 的 `kRestrictedPorts`，10080 属于 Amanda 备份服务）中，浏览器会直接以 `ERR_UNSAFE_PORT` 拦截，页面表现为「打不开」。因此默认端口改为 `10086`。

若需换端口，修改 `SERVER_PORT` 与端口映射即可，但需避开 Chromium 受限端口（完整列表见 Chromium 源码 `net/base/port_util.cc` 中的 `kRestrictedPorts`）。

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
│   │   ├── db.go                # DDL · 连接池 · 默认设置/管理员初始化 · 孤儿分组清理
│   │   └── migrations.go        # schema_versions 有序幂等迁移
│   └── handler/                 # 各业务 handler（见「路由一览」）
└── data/                        # 运行时数据（自动创建，勿提交）
    ├── solarpanel.db            # SQLite 数据库
    └── uploads/                 # icons / logos / wallpapers / favicon / weather
```

## 🗄️ 数据库

- **类型**：SQLite（WAL 模式），`modernc.org/sqlite` 纯 Go driver
- **连接池**：`SetMaxOpenConns(15)`；`journal_mode=WAL` · `synchronous=NORMAL` · `busy_timeout=15000` · `foreign_keys=ON` · `cache_size=-20000` · `temp_store=MEMORY`
- **表**：`users` · `settings` · `item_groups` · `items` · `custom_feeds` · `audit_logs` · `schema_versions` · `rate_limits` · `trusted_devices`
- **迁移**：`schema_versions` 记录已应用版本；新增 DDL/DML 追加到 `migrations` 列表末尾，`Version` 必须严格递增且不得修改历史项
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

### 登录后路由（`RequireLogin` + CSRF）

| 方法 | 路径 | `action` | 说明 |
|---|---|---|---|
| GET / POST | `/api/settings.php` | `list` / `save` | 设置读取与保存 |
| GET / POST | `/api/groups.php` | `list` `edit` `delete` `sort` `sort_batch` `visible` | 分组 CRUD、排序、显示隐藏 |
| GET / POST | `/api/items.php` | `list` `edit` `delete` `sort` `sort_batch` `toggle_hidden` `favicon` `fetch_meta` | 卡片 CRUD、排序、隐藏、抓图标、抓标题 |
| POST | `/api/upload.php` | `type=icon\|logo\|wallpaper` | 图标 5MB / Logo 5MB / 壁纸 10MB |
| GET / POST | `/api/gallery.php` | GET 列表 · POST `delete` | 壁纸图库（用户 `wallpapers` + 预置 `weather`） |
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
| **设置** | 基础（站点标题 / Logo / 公告 / 页脚 / 时钟 / 备案 / 访客访问）、主题（明暗 + 风格 + 卡片样式 + 布局留白）、搜索（宽度 + 搜索引擎清单 + 默认引擎）、新闻（开关 + 24 源勾选排序） |
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

## 🧪 开发注意事项

- **WAL 快照隔离**：`DB.Exec` 归还连接池后再 `DB.QueryRow` 可能落到另一条连接，读不到刚写入的数据。写后读**必须放在同一个 `db.Tx` 内**（`groups_handler` 的保存与 `items_handler` 的 `toggle_hidden` 已按此实现）
- **嵌套查询死锁**：SQLite 连接池较小时，`rows.Next()` 遍历期间再发起 `QueryRow` 会阻塞。代码统一先收集结果并 `rows.Close()` 后再查
- **列与 Scan 必须一一对应**：`scanItemGroup` 有 9 个目标（含 `parent_id`），SELECT 少一列会让 `Scan` 报错并被误读为「记录不存在」
- **`schema_versions` 是 OTA 关键表**，禁止手动删除；历史迁移条目不可修改
- **设置默认值只改 `internal/settings/defaults.go`**，`db.go` 初始化与 `backup_handler.go` 重置必须同源，避免两处漂移
- **新增主题**只改 `frontend/themes.json` 并跑 `build-themes.js`，不要手改 `themes.css`
- **前端资源变更**后必须跑 `build-frontend.js`，否则浏览器仍命中旧 SHA1 缓存
- **Docker 重建**：`docker compose down && docker compose up -d --build`；若镜像未更新，加 `--no-cache` 排除 buildx 缓存干扰

## 📋 更新日志

完整变更明细见 [updates.md](./updates.md)。维护约定：每次升版同时更新 `updates.md` 与本节，并与 `frontend/assets/js/api.js` 的 `APP_VERSION` / `APP_CHANGELOG` 保持一致。

### v3.2.3（2026-10-24）

- **修复图标白色背景**：部分浏览器（如 Chrome）中图片类卡片图标会露出白色方块，现已统一为透明
- **深色模式可见性**：深色图标保留轻量轮廓，不再依赖底板

### v3.2.2（2026-10-24）

- **内置壁纸可删除**：删除后重启不会自动恢复
- **优化壁纸体积与加载性能**

### v3.2.1（2026-10-24）

- **内置壁纸补齐**：5 张新增壁纸此前未随版本发布，现已随镜像提供

### v3.2.0（2026-10-24）

- **字体系统**：页面字体与时钟字体各 12 款免费设计字体，按需加载 Google Fonts 镜像
- **设置重构**：站点设置归并为 5 个一级分区（站点信息 / 外观与卡片 / 导航与搜索 / 访问控制 / 热点新闻）
- **每日壁纸重写**：并发合并 + 失败重试 + 跨源兜底，壁纸源设置即时生效
- **壁纸预览弹窗**：每个源随机 10 张、可刷新重随机，点选后才固定为前端壁纸
- **审计日志增强**：每页 20 条、服务端筛选、记录访客 IP 与归属地、保留 30 天
- **壁纸图库分区**：区分「内置」与「上传」两个目录
- **资源瘦身**：预置壁纸全量转 WebP，体积下降约 75%，分辨率不变
- **卡片与图标**：图标背景透明、底色透明度默认 0，Soft 主题卡片透明度联动生效
- **弹窗统一**：前后台编辑卡片弹窗结构、分组顺序与按钮布局对齐
- **主题打磨**：补齐弹窗主题适配，导航栏胶囊按钮改为微 3D 并打磨白色硬边
- **取数优化**：添加 / 编辑卡片时自动获取标题、描述与图标的逻辑与效率优化

### v3.1.2（2026-10-24）

- **主题扩展**：新增清透极简、柔和工具、动态极光、IRIS 光圈、复古怀旧、企业简洁六套主题，共九套
- **动态光效**：动态主题支持鼠标跟随背景光晕与卡片悬停聚光
- **深色图标适配**：深色模式下自动为深色图标添加浅色玻璃底板
- **修复热点新闻入口显隐**：关闭功能后主页不再残留「热点新闻」按钮
- **修复热点新闻开启判定**：全选数据源保存后不再被误判为「未开启」
- **修复天气组件偶发不显示**：刷新多次才出现的问题
- **数据源批量操作**：后台热点新闻数据源支持「全选 / 反选」
- **默认主题**：切换为 `liquid-glass` 液态玻璃（仅浅色），默认明暗模式为浅色

### v3.1.1（2026-10-24）

- **安全性加固**：完善接口权限、上传与备份校验、访问控制
- **问题修复**：修复分组、卡片、设置与备份相关的若干异常行为
- **性能优化**：优化列表查询与首页渲染，清理冗余代码
- **样式更新**：主题与界面细节调整

### v3.1.0（2026-10-23）

- **空库启动**：首次启动不再注入预置分组与卡片，只写入默认设置与管理员账号；「恢复初始状态」同样只恢复默认设置
- **默认端口 10080 → 10086**：规避 Chromium 受限端口导致的 `ERR_UNSAFE_PORT`
- **分组管理树形化**：二级分组紧跟所属一级分组展示，排序与显示/隐藏均在同级内生效，跨级拖动会被拒绝
- **修复分组「上移」无效**：后端此前只读 `dir`，前端传的是 `direction`
- **改挂父级自动排序**：二级分组换到另一个一级分组时，`sort` 自动排到新父级末尾
- **修复审计日志无时间**：写入时补齐 `created_at`，列表按 `created_at DESC, id DESC` 稳定排序
- **备份导入明确为整体替换**：清空 `items` / `item_groups` / `settings` 后按备份重建，分组 ID 重映射，文件路径限制在 `uploads` 内
- **favicon 协商缓存**：新增 `ETag` / `Last-Modified`，命中返回 304
- **镜像**：移除 `initdb` 预置库构建步骤，三架构仍约 14MB（amd64 13.9MB / arm64 13.8MB / armv7 13MB）
- **文档**：README 全量重写（完整路由表、端口说明、备份格式、开发注意事项）

### v3.0.0（2026-10-21）

- Go 纯后端架构重写（chi + `modernc.org/sqlite`，零 CGO 单二进制），彻底废弃 PHP 版本
- 主题体系精简为 `soft`（默认）+ `blueprint`
- 热点新闻 24 源、天气组件、每日壁纸、PWA、CSRF + Session + 限流 + TOTP 受信设备
- 设置中心化、前端 SHA1 资源戳构建链、`schema_versions` 有序幂等迁移

## 📝 License

MIT

# SolarPanel v3

[![Version](https://img.shields.io/badge/Version-v3.0.0-5B8DEF?logo=semanticrelease)
[![Go](https://img.shields.io/badge/Go-1.23-00ADD8?logo=go)](https://go.dev)
[![Architecture](https://img.shields.io/badge/Architecture-chi+sqlite-50C878)](https://github.com/go-chi/chi)
[![Image](https://img.shields.io/badge/Docker-18MB-2496ED?logo=docker)](https://hub.docker.com/)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

> 一个轻量自托管导航面板。Go 纯后端，零 CGO 单二进制，内嵌前端，Docker 镜像仅 ~18MB。

## ✨ 特性

- **零依赖单二进制** — Go + `modernc.org/sqlite`（纯 Go SQLite 实现），无 CGO，无外部数据库
- **两级分组导航** — 支持父子嵌套，侧边栏自动展开/折叠，实时搜索过滤
- **144 张预置卡片** — 灵感采集 / 素材资源 / 常用工具 / 学习教程 / 社区资讯 / UED团队
- **双主题** — `soft` 柔和浮雕（默认）· `blueprint` 工程蓝图
- **热点新闻聚合** — 微博、知乎、B站、GitHub Trending、IT之家、V2EX、少数派等 24 源
- **天气 + 壁纸** — 顶部时钟 + 天气组件，后台可切换每日壁纸源
- **安全** — CSRF 令牌 + Session 管理 + 接口限流 + 受信设备 + 审计日志
- **PWA** — Service Worker 离线缓存，可安装为应用
- **有序幂等迁移** — `schema_versions` 表追踪，新增 schema 变更只需在迁移列表末尾追加

## 🏗 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.23 · [chi/v5](https://github.com/go-chi/chi) · [modernc.org/sqlite](https://modernc.org/sqlite) |
| 前端 | 原生 JS · CSS 变量主题系统 · PWA Service Worker |
| 构建 | Node 24（前端 SHA1 资源戳注入） · Go embed 静态产物 |
| 部署 | Docker · Alpine 3.22 |

依赖极简：

```
github.com/go-chi/chi/v5       — HTTP 路由 + 中间件
golang.org/x/crypto            — bcrypt / sha256 / TOTP
modernc.org/sqlite             — 纯 Go SQLite driver（零 CGO）
```

## 🚀 快速开始

### 方式一：Docker（推荐）

镜像名 `ovitor/solarpanel-v3:latest`，数据持久化挂载到宿主机 `./data`。

#### docker run（单命令）

```bash
# 直接拉取镜像并运行
docker run -d \
  --name solarpanel \
  --restart unless-stopped \
  -p 18080:18080 \
  -v $(pwd)/data:/app/data \
  -e SERVER_PORT=18080 \
  ovitor/solarpanel-v3:latest

# Windows PowerShell
docker run -d --name solarpanel-v3 --restart unless-stopped -p 18080:18080 -v ${PWD}/data:/app/data -e SERVER_PORT=18080 ovitor/solarpanel-v3:latest
```

#### docker run（本地构建）

```bash
git clone https://github.com/Ozero-top/SolarPanel-v3.git
cd SolarPanel-v3
docker build -t solarpanel-v3:local .
docker run -d --name solarpanel-v3 -p 18080:18080 -v $(pwd)/data:/app/data solarpanel-v3:local
```

#### docker compose（完整）

```bash
# docker-compose.yml
services:
  solarpanel:
    image: ovitor/solarpanel-v3:latest  # 用本地构建替换为 build: .
    container_name: solarpanel-v3
    restart: unless-stopped
    ports:
      - "18080:18080"
    volumes:
      - ./data:/app/data
    environment:
      - SERVER_PORT=18080
      # - HTTPS_PORT=18443              # 启用 HTTPS（自动生成自签名证书）

# 启动
docker compose pull     # 拉取最新镜像（可选）
docker compose up -d

# 查看日志
docker compose logs -f

# 停止 / 重新构建
docker compose down
docker compose up -d --build
```

浏览器访问 `http://localhost:18080`。数据目录 `./data/` 自动创建，包含 `solarpanel.db`（SQLite 数据库）和 `uploads/`（用户上传文件）。

### 方式二：单二进制

```bash
# 编译（跨平台交叉编译示例）
CGO_ENABLED=0 GOOS=linux   go build -ldflags="-s -w" -o solarpanel .
CGO_ENABLED=0 GOOS=darwin  go build -ldflags="-s -w" -o solarpanel .
CGO_ENABLED=0 GOOS=windows go build -ldflags="-s -w" -o solarpanel.exe .

# 运行（当前目录自动创建 data/）
./solarpanel
```

### 方式三：源码开发

```bash
# 1. 构建前端（Node 24+，自动注入资源戳 + 生成主题 CSS）
node scripts/build-themes.js
node scripts/build-frontend.js

# 2. 后端直接跑（前端产物已 embed）
go run .
```

## ⚙️ 环境变量

全部可选，未设置时使用默认值：

| 变量 | 默认 | 说明 |
|---|---|---|
| `SERVER_PORT` | `18080` | HTTP 监听端口 |
| `HTTPS_PORT` | （空，不启用） | HTTPS 端口，启用时自动生成自签名证书 |
| `CERT_FILE` | `data/cert.pem` | HTTPS 证书路径 |
| `KEY_FILE` | `data/key.pem` | HTTPS 私钥路径 |

## 🔑 默认账号

- 管理员：`admin` / `admin123`
- 首次启动后请立即在后台修改密码

## 📁 项目结构

```
SolarPanel/
├── main.go                      # 入口：DB 初始化 + 路由注册 + HTTP 服务
├── go.mod / go.sum
├── Dockerfile                   # 三阶段构建：Node 前端 → Go builder → Alpine 运行时
├── docker-compose.yml
├── scripts/
│   ├── build-themes.js          # 从 themes.json 生成 themes.css + 主题白名单
│   └── build-frontend.js       # SHA1 资源戳注入 · sw.js 版本号
├── frontend/                    # 前端源码（构建后 embed 进二进制）
│   ├── themes.json              # 主题配置唯一事实源
│   ├── index.html / admin.html / login.html
│   ├── sw.js                    # PWA Service Worker
│   └── assets/
│       ├── css/                 # common.css · index.css · themes.css · auth-admin.css
│       └── js/
│           ├── api.js           # 版本号 + APP_CHANGELOG + 公共工具
│           ├── index.js         # 主页逻辑（boot → public.php → render）
│           ├── admin.js         # 后台管理
│           └── login.js
├── internal/
│   ├── config/config.go         # 环境变量 → Config 结构体
│   ├── settings/defaults.go     # 设置默认值唯一事实源
│   ├── auth/                    # Session · CSRF · RateLimit · Audit · TOTP
│   ├── db/
│   │   ├── db.go                # DDL · 种子注入 · 连接池配置
│   │   ├── migrations.go        # schema_versions 有序幂等迁移
│   │   └── data/seed_groups.json # 预置分组 + 卡片种子（go:embed）
│   └── handler/                 # 各业务 handler（公共路由 + 登录后路由）
└── data/                        # 运行时数据（自动创建）
    ├── solarpanel.db            # SQLite 数据库
    └── uploads/                 # 用户上传文件（壁纸等）
```

## 🗄️ 数据库

- **类型**：SQLite，WAL 模式，`modernc.org/sqlite` 纯 Go driver
- **连接池**：`SetMaxOpenConns(15)` · `busy_timeout=15s` · `cache_size=-20000`
- **迁移**：`schema_versions` 表追踪已应用版本。新增 DDL/DML 在 `migrations` 列表末尾追加，`Version` 必须严格递增
- **种子**：首次安装时从 `data/seed_groups.json` 注入 26 个分组 + 144 张卡片。重置初始状态会完整恢复

## 🎨 主题定制

主题唯一事实源为 `frontend/themes.json`，结构：

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

添加新主题后执行 `node scripts/build-themes.js`，自动同步生成：
- `frontend/assets/css/themes.css`（主题 CSS）
- `api.js` 中的 `SP_STYLES` 数组（前端主题白名单）
- `settings_handler.go` 中的 `allowedSettingKeys` 自动放行

## 🔌 路由一览

全部挂在 `/api/` 下，历史原因保留 `.php` 后缀：

| 路径 | 认证 | 说明 |
|---|---|---|
| `GET /api/public.php` | 无需 | 主页数据：设置 + 分组 + 卡片 + 用户 |
| `POST /api/auth.php` | 无需 | 登录 / 登出 / 改密 / MFA / 受信设备 |
| `GET /api/install.php` | 无需 | 首次安装状态检查 |
| `GET /api/weather.php` | 无需 | 天气数据 |
| `GET /api/news.php` | 无需 | 热点新闻源列表 / 聚合 |
| `POST /api/settings.php` | 登录 + CSRF | 设置读取 / 保存 |
| `POST /api/groups.php` | 登录 + CSRF | 分组增删改查 |
| `POST /api/items.php` | 登录 + CSRF | 卡片增删改查 / 排序 |
| `POST /api/backup.php` | 登录 + CSRF | 导出 / 导入 / 重置初始状态 |
| `GET  /api/backup.php?action=export` | 登录 | 导出 JSON |
| `POST /api/backup.php?action=import` | 登录 + CSRF | 导入 JSON（分组按 parent_id 排序 + ID 重映射） |

## 🧪 开发注意事项

- SQLite `rows.Next()` 遍历期间**不能**发起嵌套 `QueryRow`，否则低连接池下会死锁。代码已全部正确 Close 或 `defer rows.Close()`
- `schema_versions` 是 OTA 关键表，**禁止手动删除**
- 备份导入时系统会对分组按 `parent_id` 排序、且做新旧 ID 重映射，避免父子关系断裂
- Docker 镜像重建：`docker compose down && docker compose up -d --build`

## 📝 License

MIT

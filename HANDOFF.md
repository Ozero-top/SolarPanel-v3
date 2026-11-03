# SolarPanel v3.7.6 开发交接文档

> **交接时间**：2026-11-03
> **接手人**：（待填写）
> **协作方式**：按 v2.1 → v3.0 → v3.7 三阶段架构演进理解代码，遇到历史决策先查本文档「八、技术决策溯源」

---

## 一、项目目标

### 1.1 产品定位
轻量自托管导航面板（轻量书签 + 常用导航站 + 每日壁纸 + 剪藏知识管理 + NAS 监控）。

### 1.2 当前版本定位
v3.7.6 是 v3.0 架构重写后的稳定迭代版，核心变化：
- **从双版本（Go + PHP）→ Go 单版本**：彻底废弃 PHP 版，解决了跨版本 Copy-Item 事故、双重白名单同步、API_BASE 分歧等大量维护痛点
- **从纯导航面板 → 导航 + 剪藏 + NAS 监控三合一**：剪藏 API v1 + Chrome 扩展 / NAS 监控面板 + Tab 标签页主题

### 1.3 关键指标
| 维度 | 数据 |
|------|------|
| 后端 API | 21 个旧版 PHP 风格端点 + 13 个剪藏 REST 端点 |
| 数据库表 | 12 张 |
| 主题数 | 11 套（soft 默认 + 液态玻璃 + Tab标签页 + 工程蓝图 + IRIS + 手绘 + 极光 + 复古 + 企业 + 清透 + 柔和工具 + 新野兽派） |
| Docker 镜像 | ~14MB（alpine），linux/amd64 · arm64 · arm/v7 三架构 |
| 前端文件 | 6 个 HTML + 9 个 JS + 8 个 CSS（embed 内嵌） |
| 剪藏扩展 | Chrome / Edge 扩展 v1.0.10 |

### 1.4 外部依赖（零 CGO）
- `github.com/go-chi/chi/v5`（路由）
- `modernc.org/sqlite`（纯 Go SQLite driver）
- `golang.org/x/crypto`（bcrypt + TOTP）
- go-readability / html-to-markdown / ledongthuc/pdf（间接，剪藏解析）

---

## 二、已完成功能清单

### 2.1 核心导航（v0.10 → v2.1 完成）
- [x] 两级分组导航（parent_id 树形 + 同级排序 + 可见性）
- [x] 卡片 CRUD（标题 / URL / 内外网双地址 / 图标 / 描述 / 隐藏 / 标签）
- [x] 卡片样式（nav 网格 / navapp 分组内小图标网格）
- [x] 搜索（站内卡片 + 引擎切换 + `/` / `Ctrl+K` 聚焦 + 历史下拉 + 按分组过滤）
- [x] 内外网自动切换（isLikelyLAN() + lan_url 配置）
- [x] 快捷图标（favicon 自动抓取 + 上传 + 服务端代理 + 预热 + 迁移兜底）

### 2.2 主题与 UI（v2.0 → v3.7 完成）
- [x] 11 套主题（CSS 变量 + extra_css，构建链 themes.json）
- [x] 明暗三态（light / dark / auto）
- [x] 两套后台并存（admin.html 经典 + admin-console.html 新控制台）
- [x] Tab 标签页主题（wetab：壁纸主视觉 + 左侧分组条 + 底部 Dock + 滚轮切分组）
- [x] 液态玻璃（liquid-glass：毛玻璃 + 鼠标跟随 + 卡片悬停聚光）
- [x] 移动端自适应（圆形图标滑动分组条 + 顶栏隐藏 iOS max-height 方案）
- [x] 右下角快捷菜单（返回顶部 / 安装应用 / 检查更新 / 快捷键 / 切主题）

### 2.3 新闻 / 天气 / 壁纸（v2.0 → v3.2 完成）
- [x] 24 内置热点新闻源 + 自定义 RSS + 勾选排序
- [x] 新闻首屏快照缓存（20ms 出图）+ 后台定时刷新
- [x] 天气组件（多城市 + 时钟 + 18 款字体 × 时钟/天气 × 整页字体）
- [x] 每日壁纸多源（bing_cn / nasa / picsum）+ 图库管理 + 上传

### 2.4 剪藏系统（v3.3 → v3.5 完成）
- [x] **剪藏 REST API v1**（/api/v1）：CRUD / 搜索 / 导出 / 重复检测 / 截图 / 正文抓取 / AI 摘要 / 标签
- [x] **Chrome 剪藏扩展**：快捷保存 / 正文快照 / 整页截图 / 右键菜单 / Alt+Shift+S
- [x] **剪藏库 /clips**：搜索 / 筛选（未读/收藏/正文/截图）/ 分组标签分面 / 网格列表视图
- [x] **剪藏阅读页 /read/{id}**：字号 / 夜间 / Markdown 复制 / PDF / 截图灯箱
- [x] **剪藏 Token 鉴权**：Bearer Token + 登录会话双鉴权
- [x] **剪藏 AI**：本地模型（Ollama）/ API 两种 Provider

### 2.5 NAS 监控（v3.3 → v3.7 完成）
- [x] 主机名 / 内网 IP / CPU / 内存 / 存储池 / 网络 / 温度 / 容器
- [x] 四种放置方式（右侧 / 左侧 / 双侧 / 卡片下方状态条）
- [x] 近 10 分钟 / 1 小时 / 24 小时历史曲线（每 5 分钟落盘）
- [x] **阈值告警**（v3.7.2）：存储池使用率 / CPU 温度 / 容器停止 → 飞书或通用 Webhook
- [x] 访客视角脱敏：隐藏 IP / 主机名 / 容器与温度风扇 / 存储池真实路径

### 2.6 安全（v2.0 → v3.0 完成）
- [x] Session + CSRF 双令牌
- [x] 接口限流（public 60/min、favicon 300/min、nas 240/min、clip 120/min）
- [x] TOTP 双因子 + 受信设备（30 天过期 + SHA-256 哈希）
- [x] 四级角色（admin / editor / viewer / guest）+ 隐藏卡片权限
- [x] SSRF 防护（自定义 RSS 保存/抓取 DNS 解析后校验私网 IP）
- [x] 审计日志（动作 / 目标 / IP / 归属地 / 保留期可配置 7-90 天）

### 2.7 运维（v3.0 → v3.7 完成）
- [x] 自动备份（1/3/5/7/9 天，最多保留 5 份）
- [x] 手动备份（JSON + 上传文件 base64）
- [x] 书签导入（Chrome/Edge HTML，增量/覆盖）
- [x] Sun Panel 备份导入（自动转换分组/卡片/图标/内外网地址）
- [x] 版本检查（GitHub Release 跳转，缓存 6/12/24 小时）
- [x] HTTPS 自签名（ECDSA P256，SAN 含内网 IP + localhost）
- [x] PWA Service Worker 离线缓存

### 2.8 V2→V3 迁移
- [x] schema_versions 有序幂等迁移
- [x] ensureColumns() 启动补齐旧库缺失列（items.url_key / users.admin_ui 等）
- [x] 启动时回填旧库 created_at 空值（防分组消失 + 卡片不存在）
- [x] 备份导入：整体替换 + 分组 ID 重映射 + 文件路径限制在 uploads 内 + .svg 清洗

---

## 三、架构概览

### 3.1 目录结构

```
SolarPanel/
├── main.go                         ⭐ 入口：路由注册 + HTTPS + favicon 别名 + 定时任务
├── go.mod                          依赖：chi/v5 + modernc.org/sqlite + golang.org/x/crypto
├── Dockerfile                      多阶段：npm 前端构建 → go build → alpine runtime（~14MB）
├── docker-compose.yml              标准配置 + NAS 监控挂载可选
├── internal/
│   ├── auth/                       ⭐ session + CSRF + rate_limit + audit + totp + geo + token
│   ├── config/config.go            环境变量（SERVER_PORT=10086 / HTTPS_PORT / NAS_HOST_ROOT / DOCKER_SOCKET）
│   ├── db/                         ⭐ db.go（schemaDDL + ensureColumns + 种子） + migrations.go（schema_versions）
│   ├── handler/                    ⭐ 30+ handler（关键见下）
│   ├── nas/                        NAS 探测（disk_unix / disk_windows / docker）
│   ├── settings/defaults.go        ⭐ 默认设置中心化
│   └── urlutil/normalize.go        URL 标准化
├── frontend/                       go:embed 内嵌，纯静态
│   ├── index.html                  主页（导航面板）
│   ├── admin.html                  经典后台
│   ├── admin-console.html          新控制台（工作台 + 统计卡）
│   ├── login.html                  登录页 + 2FA
│   ├── clip.html / clips.html / clip-read.html  剪藏三页
│   ├── assets/
│   │   ├── js/
│   │   │   ├── api.js              ⭐ 版本事实源：const APP_VERSION = 'v3.7.6'（唯一改版本处）
│   │   │   ├── index.js            主页逻辑 + wetab + 主题切换 + isLikelyLAN()
│   │   │   ├── admin.js            旧后台 + SETTING_SECTIONS 注册
│   │   │   ├── admin-console.js    新控制台
│   │   │   └── clip*.js            剪藏三页
│   │   └── css/ (themes.css + common.css + index.css + auth-admin.css + ...)
│   ├── themes.json                 ⭐ 主题元数据构建产物（11 套 × light/dark + extra_css）
│   ├── manifest.json / sw.js       PWA
│   └── uploads/                    embed 预置（weather 壁纸）
├── clipper-extension/              Chrome 剪藏扩展（独立构建）
├── scripts/                        build-frontend.js / build-themes.js / build-clipper.js / release.ps1
├── docs/                           图标方案对比 / Tab主题说明
└── updates.md                      更新日志（给用户看的）
```

### 3.2 路由注册（main.go）

```
/public                     → 主页（embed index.html）
/login.html                 → 登录页（protectLoginHTML：已登录跳转 admin）
/admin.html                 → 经典后台（protectAdminHTML：未登录跳转 login）
/admin-classic.html         → 同上
/clip                       → 剪藏创建（PWA 分享入口）
/clips                      → 剪藏库（需登录）
/clip-share                 → 剪藏分享落地页（公网可访问）
/read/{id}                  → 剪藏阅读页（需登录）

/api/*                      → chi 路由（旧版 PHP 风格，公共 + 登录后合并）
/api/v1/*                   → chi 路由（剪藏 REST，Bearer Token + 会话双鉴权，CORS *）

/frontend/uploads/*         → 用户上传文件（磁盘直读 + CSP sandbox + favicon 别名）
/uploads/*                  → 同上（双路由历史原因）
```

### 3.3 数据库（SQLite）

**12 表**：users / settings / item_groups / items / custom_feeds / audit_logs / schema_versions / rate_limits / trusted_devices / clips / clip_contents / clip_tokens / nas_history

**关键设计**：
- `schema_versions` 表实现有序幂等迁移（RunMigrations）—— **新功能加列/改表不要直接 ALTER，用 RunMigrations 注册**
- `ensureColumns()` 启动时按 PRAGMA table_info 检测并 ALTER TABLE 补齐旧库缺失列（兼容 V2.x 导入）
- **DSN PRAGMA**：`busy_timeout=15000` + `journal_mode=WAL` + `foreign_keys=ON` + `cache_size=-20000` —— **必须写进 DSN，不能 DB.Exec()，否则只作用于那一条连接**
- 连接池 maxOpenConns=15，闲置 5 分钟关闭

### 3.4 设置白名单（settings_handler.go）

**allowedSettingKeys**（40+ 键）—— 后端校验入口，**新增设置键必须同时改**：
1. `internal/handler/settings_handler.go` allowedSettingKeys
2. `internal/handler/settings_handler.go` settingValueWhitelist（如果有限制值）
3. `frontend/assets/js/admin.js` SETTING_SECTIONS
4. `frontend/assets/js/api.js`（如果是 SP_STYLES / SP_FONTS 等前端枚举）
5. `frontend/themes.json`（如果影响主题）
6. `db/migrations.go`（如果需要新 migration）

**settingValueWhitelist**：theme_style（11 套主题）/ card_style（nav/navapp）/ default_lan_mode / nas_layout / auto_backup_interval（1/3/5/7/9）/ audit_retention_days（7/15/30/60/90）/ clock_font（18 款）/ page_font（18 款）

**漏改白名单的后果**：静默回退默认值 = "保存成功但不生效"，v2.1.01 教训

### 3.5 前端主题系统

**主题注册三端**：
1. `frontend/themes.json` 构建产物中的 theme key（build-themes.js 从 themes.css 提取）
2. `settings_handler.go` settingValueWhitelist["theme_style"]
3. `api.js` SP_STYLES 数组（如果前端需要枚举）

**CSS 变量位置敏感**：**必须写在 `:root { }` 块内部**，声明在块外会被浏览器完全忽略（v2.1.25 登录页布局错乱教训）

**主题切换**：`data-style="xxx"` + `data-theme="light|dark|auto"`

### 3.6 剪藏 API v1

```
POST   /api/v1/clips              创建（支持 tags 追加）
GET    /api/v1/clips              列表（筛选 / 排序 / 分页）
DELETE /api/v1/clips              批量删除
PATCH  /api/v1/clips/{id}         更新
GET    /api/v1/clips/{id}         详情
POST   /api/v1/clips/{id}/refetch 重新抓取正文
POST   /api/v1/clips/{id}/screenshot 重新截图
DELETE /api/v1/clips/{id}/content  删除快照
GET    /api/v1/clips/search       全文搜索（FTS5）
GET    /api/v1/clips/export       ZIP 批量导出（Markdown + clips.csv）
GET    /api/v1/clips/duplicates   重复链接检测
GET    /api/v1/me                 当前用户
GET    /api/v1/groups             分组列表
POST   /api/v1/preview            URL 预览（meta / favicon）
GET/POST/DELETE /api/v1/tokens    Token 管理
```

**鉴权**：Bearer Token（扩展端）+ 登录会话（浏览器端）双鉴权，CORS 允许 *

**幂等**：clip_requests 表 24 小时清理

### 3.7 Docker 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| SERVER_PORT | 10086 | 监听端口（**10080 被 Chromium 列入受限端口，不要改**） |
| HTTPS_PORT | 空 | 非空时启用自签名 ECDSA P256 证书 |
| DB_PATH | /app/data/solarpanel.db | SQLite 文件路径 |
| UPLOAD_DIR | /app/data/uploads | 用户上传文件目录 |
| NAS_HOST_ROOT | — | NAS 监控需要：宿主根目录挂载路径 |
| DOCKER_SOCKET | — | NAS 监控需要：Docker socket 路径 |

---

## 四、核心硬约束（必须延续，不可破）

### 🔴 4.1 设置白名单双端同步
新增设置键必须同时改：allowedSettingKeys + settingValueWhitelist + admin.js SETTING_SECTIONS + api.js（如果是枚举）+ themes.json（如果影响主题）+ migrations.go（如果需要迁移）

### 🔴 4.2 新增主题三端注册
1. themes.json 构建产物中的 theme key
2. settings_handler.go settingValueWhitelist["theme_style"]
3. api.js SP_STYLES（如果需要）

**任何一处漏改 → 静默回退默认主题 soft**

### 🟡 4.3 CSS 变量必须在 `:root {}` 块内
声明在块外会被浏览器完全忽略，v2.1.25 登录页布局错乱教训。**写 CSS 变量前检查括号匹配**。

### 🟡 4.4 iOS 滚动冲突
- iOS Safari `position: sticky` 与 `transform: translateY()` 渲染冲突
- **必须用 `max-height:0 + padding:0 + margin:0 + opacity:0` 组合方案**
- iOS 滚动动量期间 `scroll` 事件被节流 → 增加 `touchmove` 事件监听

### 🟡 4.5 SQLite DSN PRAGMA
PRAGMA 必须写进 DSN（不能 DB.Exec()），否则只作用于那一条连接，其他连接还是默认值，并发时随机 500。

### 🟡 4.6 数据库迁移有序幂等
- 旧库（V2.x 导入）缺列 → ensureColumns() 启动补齐（幂等，自动）
- schema_versions 表追踪迁移版本
- **不要手动 ALTER TABLE**，用 RunMigrations 注册

### 🟡 4.7 版本号单源
`const APP_VERSION = 'v3.7.6'` 在 `frontend/assets/js/api.js` 是唯一版本源，Go 编译时 `extractVersion()` 从这里读。**改版本号只改这一处**。

### 🟡 4.8 favicon 别名
图标迁移（.ico/.png → .webp）会让数据库里的旧扩展名 URL 失效，resolveFaviconAlias() 按 md5 前缀找实际落盘文件。改图标扩展名记得同步改这里。

---

## 五、经验教训

### 5.1 最痛的 5 个事故

| 日期 | 事故 | 根因 | 解法 |
|------|------|------|------|
| v2.0.15 | 线上卡片 404 | PowerShell Copy-Item 跨版本覆盖 | **⛔ Go 单版本后不再有此问题** |
| v2.1.01 | 升级包白名单不同步 | 双版本 build_upgrade/upgrade 两端 | **⛔ Go 单版本后不再有此问题** |
| v2.1.25 | 登录页布局错乱 | CSS variables 声明在 `:root {}` 花括号外 | **CSS 变量严格写块内部** |
| v2.1.26 | 壁纸完全失效 | Bing API 地址 `.php` → `.aspx` 笔误 | **第三方 API 本地 curl 验证后提交** |
| v3.0.0 | 端口被 Chromium 拦截 | 10080 受限端口 | **改用 10086** |

### 5.2 静默错误模式

| 问题 | 根因 | 排查方法 |
|------|------|----------|
| 主题保存不生效 → 自动回 soft | settings.go/theme_style 白名单漏新主题 | 搜索 settingValueWhitelist["theme_style"] |
| 字段提交值丢失 | admin.js SETTING_SECTIONS 未注册 | 对比 allowedSettingKeys 和 SETTING_SECTIONS |
| 接口 500（NAS 等慢速存储 + 并发） | SQLite busy_timeout 不够 | 查 DSN PRAGMA + journal_mode |
| 分组列表消失 | 旧库 created_at 为空被 NULL 扫描跳过 | db.go 已自动回填 created_at |
| NAS 容器 CPU 长期 0% | 新版 Docker stats 不提供上一采样 | v3.7.2 已修复 |
| favicon 显示白色背景 | 深色模式下图标没做浅色底板 | themes.css .card .icon.has-img 处理 |

### 5.3 IDE 缓冲回退
Trae 自动保存/恢复可能把 Edit 写入的新内容用陈旧缓冲覆盖。**关键修改后尽早编译验证**，长时间编辑注意保存状态。

### 5.4 Docker 坑
- **项目目录直接挂载进容器**：install.php 自删逻辑会删掉本机文件（v0.10 教训）
- **zip 文件被锁定**：Docker 文件共享层缓存，宿主机删/改名失败。换新文件名输出
- **arm/v7 链接解释器**：必须指向 ld-musl-armhf.so.1，否则镜像无法启动（本次交接前刚修复）
- **Docker Hub push 超时**：network layer 拦截 auth.docker.io:443，但 BuildKit 会自动重试，等 "manifest push" 行确认成功

---

## 六、发版流程（Go 单版本）

```
1. 改代码（Go 后端 / 前端 HTML/CSS/JS）
2. bump api.js APP_VERSION（唯一版本源）
3. bump 所有 HTML 的 ?v= 资源戳（防 SW 缓存）
4. go build → docker buildx build（多架构推送）
   docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 \
     -t ovitor/solarpanel-v3:latest -t ovitor/solarpanel-v3:vX.Y.Z --push .
5. 上传剪藏扩展 zip 到 GitHub Release
6. 更新 Docker Hub README / updates.md 更新日志
7. 更新 GitHub Release Notes
```

---

## 七、已知问题与下一步计划

### 7.1 代码债务
- admin-classic.html 旧版后台保留，admin-console.js 新版后台，两套代码有重复逻辑 → 长期应统一
- 旧版兼容 API 用 /api/*.php 路径名 → 命名不规范，可逐步改为 /api/v1 风格
- favicon_proxy 已开发但前端需确认全部走代理路径 → 还在迁移中
- 剪藏 AI 功能需要本地 Ollama 配置 → 用户可选项，后端已支持两种 Provider

### 7.2 文档待更新
- README.md changelog 需同步到 v3.7.6
- V2→V3 升级路径（备份导入）在 updates.md 有，README 里需更醒目
- NAS 监控依赖条件需明确（/host 挂载 + docker.sock + 环境变量）

### 7.3 架构改进方向
- **版本号自动化**：当前手动改 api.js + ?v=，可脚本化
- **迁移脚本集中化**：schema_versions + RunMigrations 已经是好模式，新功能加表/改列都走这条
- **设置校验单点化**：allowedSettingKeys + settingValueWhitelist + SETTING_SECTIONS 三处同步可以用构建脚本生成，减少漏改
- **剪藏与导航整合**：clips 表和 items 表有部分重叠概念（收藏 / 标签），可以考虑深层联动

### 7.4 安全待加固点
- HTTPS 自签名证书：SAN 包含所有内网 IP + localhost + solarpanel.local，已覆盖 NAS 直访场景
- 默认密码 admin / admin123：首次登录未强制改密码，需增强提示
- 剪藏扩展 HTTP 服务器：自动申请访问权限，已修复

---

## 八、技术决策溯源

### 8.1 废弃 PHP 转 Go（v3.0.0）
**决策**：v3.0.0 彻底废弃 PHP 版本，Go 单版本成为唯一主线
**理由**：
- 双版本维护负担（8 表 / 39 设置 / 18+ API / 20 主题同步改）
- API_BASE 分歧、资源戳 42 处、三重注册、双白名单、Copy-Item 事故等
- Go 版已完整复刻 PHP 版全部功能
**遗留**：
- 备份格式不同（Go JSON + base64 上传文件 vs PHP MySQL dump）
- V2→V3 需手动备份导入（非自动迁移）
- 剪藏系统（Go 独有）不影响升级

### 8.2 废弃在线升级（设计决策 → 沿用至今）
- PHP 版 OTA 完整（build_upgrade.php + upgrade.php + versions.json + CDN）
- Go 版无在线升级，靠 Docker restart policy
- **v3.0 废弃 PHP 后**：也废弃了在线升级，统一靠拉镜像
- 用户反馈良好：Docker restart 比 OTA 更稳定、更可控

### 8.3 chi/v5 vs Gin（v3.0 选型）
- 早期 Go 版用 Gin + GORM
- v3.0 架构重写改用 chi/v5 + 原生 database/sql
- **理由**：chi 更轻量、纯函数式中间件、内嵌静态资源直接用 chi.FileServer + 自定义 NotFound handler
- 实际效果：main.go 600 行含路由注册 + HTTPS + favicon 别名 + 定时任务，可读性好

### 8.4 纯 Go SQLite（modernc.org/sqlite）
- 零 CGO 依赖，单二进制
- 镜像从 51MB 缩到 ~14MB
- **注意**：SQLite 不是为高并发设计的，WAL + busy_timeout + 连接池是标配。NAS 等慢速存储挂 SMB/NFS 时 WAL 可能失效退回 delete 模式（main.go 有日志提示）

### 8.5 剪藏 API v1 双鉴权设计
- **Bearer Token**：Chrome 扩展跨端调用，需用户在后台「剪藏与接入 → 令牌与地址」生成
- **登录会话**：浏览器端直接调用（Cookie 会话）
- **理由**：扩展端无法共享 Cookie，只能用 Token；浏览器端已有 Session，双鉴权保证两种场景都能工作

### 8.6 剪藏 AI 双 Provider
- **local**：本地 Ollama（默认），`clip_ai_provider: local`
- **api**：远程 API，`clip_ai_provider: api` + `clip_ai_base_url` + `clip_ai_api_key` + `clip_ai_model`
- 后端白名单已配置，前端 admin.js SETTING_SECTIONS 已注册

### 8.7 新旧后台并存
- admin.html（经典）+ admin-console.html（新控制台）同时保留
- 登录后弹窗选择默认 → users.admin_ui 字段保存选择
- **理由**：迁移期用户习惯经典版，但新控制台功能更强大。v3.7.x 新控制台已基本覆盖经典版，可考虑未来统一。

---

## 九、快速检索索引

| 问题 | 位置 |
|------|------|
| 设置保存成功但不生效 → 静默回退 | 4.1 节 / 5.2 节 |
| 新主题保存不生效 | 4.2 节 |
| CSS 变量写在哪里 | 4.3 节 |
| iOS 顶栏隐藏方案 | 4.4 节 |
| SQLite 并发 500 | 4.5 节 |
| 改了数据库表结构但旧库没生效 | 4.6 节 |
| 改版本号改一处 | 4.7 节 |
| favicon 旧链接 404 | 4.8 节 / main.go resolveFaviconAlias() |
| 端口为什么是 10086 不是 10080 | 5.1 节（Chromium 受限端口） |
| NAS 面板不显示 | 6 节 Docker 环境变量 + docker-compose.yml（需要挂载 / 和 docker.sock） |
| arm/v7 镜像启动失败 | 5.4 节（链接解释器 ld-musl-armhf.so.1） |
| 剪藏扩展怎么配置 | 3.6 节（生成 Token → 扩展设置填站点地址 + Token） |
| V2 备份怎么导入 V3 | updates.md 开头（先 V2 导出备份 → V3 后台导入配置） |
| Docker 镜像怎么推 | 6 节发版流程 |

---

## 十、关键文件速查

| 文件 | 作用 | 改它的时机 |
|------|------|------------|
| `main.go` | 路由注册 + HTTPS + 定时任务 + favicon 别名 | 加新 API / 改路由 / 改中间件 |
| `frontend/assets/js/api.js` | **版本事实源** + 所有 API 封装 + SP_STYLES | **每次发版改这里**（唯一版本源） |
| `internal/handler/settings_handler.go` | 配置白名单 | **加新设置键必改** |
| `internal/db/db.go` | schemaDDL + ensureColumns + 种子数据 | 改建表 DDL / 加默认设置 |
| `internal/db/migrations.go` | schema_versions 迁移 | **改数据库结构必改**（有序幂等） |
| `frontend/themes.json` | 主题元数据（构建产物） | 加新主题 → 改 themes.css → 跑 build-themes.js |
| `frontend/assets/js/admin.js` | 后台 + SETTING_SECTIONS 注册 | **加新设置键必改** |
| `frontend/assets/js/index.js` | 主页 + wetab + 主题切换 | 改主页 UI / 加主题动效 |
| `internal/handler/*_handler.go` | 各业务模块 | 改对应功能 |
| `Dockerfile` | 多阶段构建 | 改依赖 / 加构建步骤 |
| `scripts/build-frontend.js` | 前端构建（SHA1 资源戳） | 加新 HTML / 改资源戳逻辑 |

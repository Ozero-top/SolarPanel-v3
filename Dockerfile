ARG BUILDPLATFORM

# ===== Stage 1: 前端构建（零依赖 Node 脚本） =====
# 按顺序：① 主题构建（从 themes.json 生成 themes.css + 同步 SP_STYLES + admin.html option）
#          ② 资源戳注入（SHA1 短 hash 改写 ?v= + sw.js 版本号）
# 彻底消除硬约束 3.1（18 处手动同步）+ 3.2（主题双端注册）
# 前端产物与 CPU 架构无关，锁死在构建机原生平台执行一次即可，
# 同时避开 node:24-alpine 缺少 linux/arm/v7 变种的问题。
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend-builder

WORKDIR /build

COPY scripts/build-themes.js scripts/build-frontend.js ./scripts/
COPY frontend/ ./frontend/

# 主题构建必须在资源戳注入之前（生成的 themes.css 要参与 hash 计算）
RUN node scripts/build-themes.js \
 && node scripts/build-frontend.js

# ===== Stage 2: Go builder =====
FROM golang:1.23-alpine AS builder

ENV CGO_ENABLED=0
ENV GOOS=linux
ARG TARGETARCH
ENV GOARCH=${TARGETARCH}

WORKDIR /build

# 利用 Docker cache 层：先复制 go.mod / go.sum
COPY go.mod go.sum ./
RUN go mod download

# 先复制全部源码（含原始 frontend/），再用 node 阶段产出的带 hash frontend/ 覆盖
COPY . .
COPY --from=frontend-builder /build/frontend ./frontend

# -s -w 剥离符号表
RUN go build -ldflags="-s -w" -o /solarpanel .

# 用 initdb 子命令生成带完整预置种子的 SQLite DB（30 个分组 + 144 张卡片 + 迁移 + backfill）
# 然后把这份 DB COPY 进最终镜像，运行时不再做种子注入
RUN /solarpanel initdb /data/solarpanel.db

# ===== Stage 3: 最小运行时 =====
FROM alpine:3.22

# 证书 + 时区 + tzdata（天气/新闻 API 需要）
RUN apk add --no-cache ca-certificates tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone

RUN mkdir -p /app/data/uploads

WORKDIR /app

COPY --from=builder /solarpanel /app/solarpanel
# 预置 DB：镜像自带完整种子数据，容器启动后直接可用；
# 若用户挂载空的 /app/data，运行时 db.Init 会检测到空库自动补种子（兜底机制仍保留）
COPY --from=builder /data/solarpanel.db /app/data/solarpanel.db

EXPOSE 10080

# 注意：不显式声明 VOLUME，避免匿名 volume 覆盖镜像里 bake 的预置 DB。
# 用户可在 docker run / docker compose 中用 -v 显式挂载覆盖；
# 未挂载时直接使用镜像内 /app/data/solarpanel.db（已含完整预置种子）。

ENTRYPOINT ["/app/solarpanel"]


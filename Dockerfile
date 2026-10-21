# ===== Stage 1: 前端构建（零依赖 Node 脚本） =====
# 按顺序：① 主题构建（从 themes.json 生成 themes.css + 同步 SP_STYLES + admin.html option）
#          ② 资源戳注入（SHA1 短 hash 改写 ?v= + sw.js 版本号）
# 彻底消除硬约束 3.1（18 处手动同步）+ 3.2（主题双端注册）
FROM node:24-alpine AS frontend-builder

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

# ===== Stage 3: 最小运行时 =====
FROM alpine:3.22

# 证书 + 时区 + tzdata（天气/新闻 API 需要）
RUN apk add --no-cache ca-certificates tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone

RUN mkdir -p /app/data/uploads

WORKDIR /app

COPY --from=builder /solarpanel /app/solarpanel

EXPOSE 18080

VOLUME ["/app/data"]

ENTRYPOINT ["/app/solarpanel"]


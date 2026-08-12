# fries 后端镜像：多阶段构建，最终镜像不含编译器、不含源码、不用 root 跑。
#
# 构建上下文是**仓库根目录**（因为要同时拷 backend/ 和 config/）：
#   docker build -f deploy/backend.Dockerfile -t fries-backend .

# ---------- 编译 ----------
FROM golang:1.25-alpine AS build

WORKDIR /src

# 先只拷依赖清单，依赖没变时这层缓存能复用。
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./

ARG VERSION=dev
# CGO 关掉才能拿到静态二进制，-trimpath 让构建可复现。
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/server ./cmd/server

# ---------- 运行 ----------
FROM alpine:3.22

# ca-certificates：调用外部 HTTPS 服务要用；tzdata：容器内时区统一 UTC。
RUN apk add --no-cache ca-certificates tzdata wget && \
    adduser -D -u 10001 -h /app fries

ENV TZ=UTC
WORKDIR /app

COPY --from=build /out/server /app/server
# 迁移文件跟着镜像走，`make migrate` 在容器里也能跑。
COPY backend/db/migrations /app/db/migrations

USER fries

EXPOSE 8080

# 存活探针不查库：库挂了不该让编排系统反复重启后端。
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/app/server"]

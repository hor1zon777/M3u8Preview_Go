# syntax=docker/dockerfile:1.7

# ====================================================================
# Stage 1: 编译前端（web/shared + web/client → web/client/dist）
# ====================================================================
FROM node:20-alpine AS web-builder
WORKDIR /web

# 预拷贝 package.json 以利用层缓存
COPY web/package.json ./package.json
COPY web/shared/package.json ./shared/package.json
COPY web/client/package.json ./client/package.json

# npm 自带的 workspace 解析
RUN npm install --workspaces --include-workspace-root

# 拷贝源码并构建
COPY web/ ./
RUN npm run build:shared && npm run build:client

# ====================================================================
# Stage 2: 编译 Go 二进制
# ====================================================================
FROM golang:1.25-alpine AS go-builder
WORKDIR /src

# 预拷贝 go.mod + go.sum 以利用层缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 纯 Go SQLite（glebarez/sqlite）→ 无需 CGO，交叉编译友好
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ====================================================================
# Stage 3: Runtime，体积 ≈ 90MB（alpine + ffmpeg + su-exec + 前端 dist）
# ====================================================================
FROM alpine:3.20 AS runner
WORKDIR /app

# ffmpeg: thumbnail 生成；curl: HEALTHCHECK；su-exec: root → appuser 权限下降
# tzdata: 容器内时间戳/日志本地化；ca-certificates: safeFetch HTTPS 握手信任链
RUN apk add --no-cache ffmpeg curl su-exec tzdata ca-certificates

# 非 root 运行（对齐 R 版的 appuser/appgroup）
RUN addgroup -S appgroup && adduser -S appuser -G appgroup \
 && mkdir -p /data /app/uploads/posters /app/uploads/thumbnails /app/web/dist /app/web/dist-image \
 && chown -R appuser:appgroup /data /app/uploads /app/web

COPY --from=go-builder /out/server /app/server
# 前端构建产物：同时拷到 dist 与 dist-image。
# dist-image 是镜像内的只读副本，entrypoint 每次启动会同步到挂载的 client-dist volume，
# 这样重建镜像后 volume 里的旧 dist 也会被刷新（Docker 命名卷仅首次从镜像初始化）。
COPY --from=web-builder /web/client/dist /app/web/dist
COPY --from=web-builder /web/client/dist /app/web/dist-image

COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh /app/server

ENV PORT=3000 \
    BIND_ADDRESS=0.0.0.0 \
    NODE_ENV=production \
    DATABASE_URL=file:/data/m3u8preview.db \
    UPLOADS_DIR=/app/uploads \
    DATA_DIR=/data \
    WEB_DIST_DIR=/app/web/dist \
    TZ=UTC

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
  CMD curl -sf http://127.0.0.1:3000/api/health > /dev/null || exit 1

# 这里保持 root 启动，由 docker-entrypoint.sh 完成 volume 权限修正 + 前端 dist 同步后
# 再通过 su-exec 切到 appuser，避免 bind-mount 宿主 uid 不匹配导致的写入失败。
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["/app/server"]

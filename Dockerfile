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

# 版本信息注入。
# VERSION 文件在构建上下文里可以直接读；git commit 与构建时间只能由外部传入——
# .dockerignore 排除了 .git，构建阶段没有仓库历史可查（CI 会传 --build-arg，见
# .github/workflows/docker-build.yml）。未传时保持 unknown，比编个假值诚实。
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

# 纯 Go SQLite（glebarez/sqlite）→ 无需 CGO，交叉编译友好
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

RUN VERSION="$(cat VERSION)" \
 && PKG=github.com/hor1zon777/m3u8-preview-go/internal/version \
 && go build -trimpath \
      -ldflags="-s -w \
        -X ${PKG}.Version=${VERSION} \
        -X ${PKG}.Commit=${GIT_COMMIT} \
        -X ${PKG}.BuildTime=${BUILD_TIME}" \
      -o /out/server ./cmd/server

# ====================================================================
# Stage 2.5: Release 产物导出。
# 仅 CI 的 release job 以 --target artifacts 提取（server 二进制 + 前端 dist，
# 打包进 GitHub Release 供应用内自更新下载）；默认构建目标仍是最后的 runner，
# 本 stage 对镜像构建零影响。
# ====================================================================
FROM scratch AS artifacts
COPY --from=go-builder /out/server /server
COPY --from=web-builder /web/client/dist /web-dist

# ====================================================================
# Stage 3: Runtime，体积 ≈ 90MB（alpine + ffmpeg + su-exec + 前端 dist）
# ====================================================================
FROM alpine:3.20 AS runner
WORKDIR /app

# LiteFS：主备高可用的 SQLite 复制层（见 docs/ha-failover.md）。
# 官方部署模型就是"与应用同容器"，直接从官方镜像取二进制即可，宿主无需安装任何东西。
# 未配置 LITEFS_DIR 时这个二进制不会被执行，单机部署完全不受影响。
COPY --from=flyio/litefs:0.5 /usr/local/bin/litefs /usr/local/bin/litefs

# ffmpeg: thumbnail 生成；curl: HEALTHCHECK；su-exec: root → appuser 权限下降
# tzdata: 容器内时间戳/日志本地化；ca-certificates: safeFetch HTTPS 握手信任链
# fuse3: LiteFS 挂载 FUSE 文件系统所需的用户态库
RUN apk add --no-cache ffmpeg curl su-exec tzdata ca-certificates fuse3

# user_allow_other：LiteFS 以 root 挂载 FUSE，而 server 降权到 appuser 运行，
# 不开这个选项 appuser 会被 FUSE 直接拒绝访问挂载点。
RUN echo "user_allow_other" >> /etc/fuse.conf

# 非 root 运行（对齐 R 版的 appuser/appgroup）
# /litefs 是 FUSE 挂载点（必须为空目录）；/var/lib/litefs 存 LiteFS 内部数据与 LTX 事务文件。
RUN addgroup -S appgroup && adduser -S appuser -G appgroup \
 && mkdir -p /data /app/uploads/posters /app/uploads/thumbnails /app/web/dist /app/web/dist-image \
             /litefs /var/lib/litefs \
 && chown -R appuser:appgroup /data /app/uploads /app/web

COPY --from=go-builder /out/server /app/server
# 镜像自带的版本号副本：entrypoint 的自更新装载日志用（权威判定在
# `server update-preflight` 子命令里，它直接用 ldflags 注入的版本）。
COPY VERSION /app/VERSION
# 前端构建产物：同时拷到 dist 与 dist-image。
# dist-image 是镜像内的只读副本，entrypoint 每次启动会同步到挂载的 client-dist volume，
# 这样重建镜像后 volume 里的旧 dist 也会被刷新（Docker 命名卷仅首次从镜像初始化）。
COPY --from=web-builder /web/client/dist /app/web/dist
COPY --from=web-builder /web/client/dist /app/web/dist-image

COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
# LiteFS 的 exec 目标：由它决定以 root 还是 appuser 运行 server（见脚本内注释）。
COPY litefs-exec.sh /usr/local/bin/litefs-exec.sh
# LiteFS 配置。角色相关字段用 ${} 占位，由 entrypoint 的开机决议结果填充。
COPY litefs.yml /etc/litefs.yml
RUN chmod +x /usr/local/bin/docker-entrypoint.sh /usr/local/bin/litefs-exec.sh /app/server

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

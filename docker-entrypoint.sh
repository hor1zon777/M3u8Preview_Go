#!/bin/sh
# docker-entrypoint.sh
# 对齐 M3u8Preview_R/docker-entrypoint.sh：
#   1) 补全缺失目录；
#   2) 把前端 dist-image 同步到挂载的 client-dist volume（保证重建镜像后也更新）；
#   3) 修复挂载卷的 ownership；
#   4) 降权到 appuser 后 exec 主进程。
# 纯 sh，避免对 bash 的依赖。
set -eu

DATA_DIR="${DATA_DIR:-/data}"
UPLOADS_DIR="${UPLOADS_DIR:-/app/uploads}"
WEB_DIST_DIR="${WEB_DIST_DIR:-/app/web/dist}"
WEB_DIST_IMAGE_DIR="/app/web/dist-image"

# 1) 目录兜底（docker volume 首次挂载时可能是空）
mkdir -p "$DATA_DIR" "$UPLOADS_DIR/posters" "$UPLOADS_DIR/thumbnails" "$WEB_DIST_DIR"

# 2) 同步前端构建产物到 volume。
#    Docker 命名卷仅在首次从镜像初始化，后续重建镜像不会自动更新；
#    这里每次启动都 rsync 式覆盖，确保 nginx 挂到的 volume 永远是最新一次 build。
if [ -d "$WEB_DIST_IMAGE_DIR" ]; then
  rm -rf "${WEB_DIST_DIR:?}"/*
  cp -a "$WEB_DIST_IMAGE_DIR"/. "$WEB_DIST_DIR"/
fi

# 3) 非 root 场景：直接 exec（已被 USER appuser 切换）
if [ "$(id -u)" -ne 0 ]; then
  exec "$@"
fi

# 4) root 场景：修正卷权限后 drop 到 appuser
chown -R appuser:appgroup "$DATA_DIR" "$UPLOADS_DIR" "$WEB_DIST_DIR" || true
exec su-exec appuser:appgroup "$@"

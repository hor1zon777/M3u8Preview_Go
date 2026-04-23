#!/bin/sh
# docker-entrypoint.sh
# 对齐 M3u8Preview_R/docker-entrypoint.sh：
#   1) 补全缺失目录；
#   2) 把前端 dist-image 同步到挂载的 client-dist volume（保证重建镜像后也更新）；
#   3) 修复挂载卷的 ownership（bind mount 友好：只改 root 拥有的文件，保留用户已有属主）；
#   4) 降权到 appuser 后 exec 主进程。
# 纯 sh，避免对 bash 的依赖。
set -eu

DATA_DIR="${DATA_DIR:-/data}"
UPLOADS_DIR="${UPLOADS_DIR:-/app/uploads}"
WEB_DIST_DIR="${WEB_DIST_DIR:-/app/web/dist}"
WEB_DIST_IMAGE_DIR="/app/web/dist-image"
SKIP_CHOWN="${SKIP_CHOWN:-0}"

# 1) 目录兜底（docker volume / bind mount 首次挂载时可能是空）
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
#
# 核心：bind mount 挂进来的宿主目录可能已有用户自己的文件与属主（host UID/GID）。
# 旧实现 `chown -R appuser:appgroup "$DATA_DIR" ...` 会把宿主机的整棵树一并改写，
# 使宿主用户（常见 UID 1000）再也无法直接在 host 上编辑 / rsync 这些目录。
#
# 新策略：只把"当前属主是 root 的文件"改给 appuser（初次目录创建时的兜底），
# 其它属主一律保留——相当于 `chown --from=root` 的 BusyBox 等价实现。
# 用户可设 SKIP_CHOWN=1 完全跳过本步骤（已用 initContainer/外部脚本管理权限的场景）。
if [ "$SKIP_CHOWN" = "1" ]; then
  echo "[entrypoint] SKIP_CHOWN=1, bypass ownership fix"
else
  for d in "$DATA_DIR" "$UPLOADS_DIR" "$WEB_DIST_DIR"; do
    if [ -d "$d" ]; then
      # find -uid 0 -exec chown 可选；BusyBox 的 find 支持 -uid 与 {} +。
      # 错误静默：bind mount 下若宿主 FS 是 ntfs/fat/CIFS，chown 可能 EPERM，不阻塞启动。
      find "$d" -uid 0 -exec chown appuser:appgroup {} + 2>/dev/null || true
    fi
  done
fi

exec su-exec appuser:appgroup "$@"

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

# 1.5) bind-mount 场景下，宿主机 docker compose up 自动创建的目录默认 root:root 755，
#      容器降权到 appuser 后无法写入（SQLite 报 out of memory / 备份恢复 permission denied）。
#      在 root 阶段直接放开权限，确保 appuser 可读写。
chmod -R 777 "$DATA_DIR" "$UPLOADS_DIR" 2>/dev/null || true

# 2) 同步前端构建产物到 volume。
#    Docker 命名卷仅在首次从镜像初始化，后续重建镜像不会自动更新；
#    这里每次启动都 rsync 式覆盖，确保 nginx 挂到的 volume 永远是最新一次 build。
if [ -d "$WEB_DIST_IMAGE_DIR" ]; then
  rm -rf "${WEB_DIST_DIR:?}"/*
  cp -a "$WEB_DIST_IMAGE_DIR"/. "$WEB_DIST_DIR"/
fi

# 3) 非 root 场景：直接 exec（已被 USER appuser 切换）
if [ "$(id -u)" -ne 0 ]; then
  if [ -n "${LITEFS_DIR:-}" ]; then
    echo "[entrypoint] 错误: LiteFS 需要 root 挂载 FUSE，当前 uid=$(id -u)" >&2
    exit 1
  fi
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

# 5) LiteFS 主备模式（见 docs/ha-failover.md）
#
# 未设置 LITEFS_DIR 时整段跳过，单机部署行为与改造前完全一致。
#
# 关键点：开机角色决议必须在 litefs mount **之前**完成——static 租约的 candidate
# 是在 litefs.yml 里静态声明的，挂载后无法更改。而决议本身又不能沿用上次的角色：
# 旧主崩溃期间领导权可能已被切走，它回归时若直接起为主就是双主。因此每次容器
# 启动都重新向 Cloudflare 租约与对端 /api/health 求证一次。
if [ -n "${LITEFS_DIR:-}" ]; then
  # 把对端的自签 CA 装进容器信任链。
  # LiteFS 自身没有任何认证机制，节点间通道靠 nginx 的 TLS + IP 白名单保护；
  # 装 CA 而不是关闭校验，是为了让这条通道不可被中间人劫持——它同时也是
  # "对端是否存活"的判定依据之一，被劫持等于把切主决策交给了攻击者。
  if [ -n "${HA_PEER_CA_FILE:-}" ] && [ -f "$HA_PEER_CA_FILE" ]; then
    cp "$HA_PEER_CA_FILE" /usr/local/share/ca-certificates/ha-peer.crt
    update-ca-certificates >/dev/null 2>&1 || \
      echo "[entrypoint] 警告: 安装对端 CA 失败，LiteFS 复制可能因证书校验失败而无法建立" >&2
  fi

  ROLE_FILE="${HA_ROLE_FILE:-$DATA_DIR/litefs-role}"

  if /app/server ha-resolve-role; then
    # shellcheck source=/dev/null
    . "$ROLE_FILE"
  else
    # 决议失败的兜底：区分两种部署形态，避免一刀切造成不必要的只读。
    #   配了 Cloudflare 租约 → 双节点，保守起为 replica（宁可只读也不冒双主风险）
    #   没配                → 单节点用 LiteFS，起为 primary 才能正常提供服务
    LITEFS_SELF_HOST="${HA_NODE_ID:-$(hostname)}"
    if [ -n "${CF_API_TOKEN:-}" ]; then
      echo "[entrypoint] 角色决议失败，保守起为只读副本" >&2
      LITEFS_CANDIDATE=false
      LITEFS_PRIMARY_URL="${HA_PEER_ADVERTISE_URL:-}"
    else
      echo "[entrypoint] 角色决议失败且未配置租约仲裁，起为 primary" >&2
      LITEFS_CANDIDATE=true
      LITEFS_PRIMARY_URL="${HA_SELF_ADVERTISE_URL:-}"
    fi
  fi

  export LITEFS_CANDIDATE LITEFS_SELF_HOST LITEFS_PRIMARY_URL
  echo "[entrypoint] LiteFS 启动: candidate=${LITEFS_CANDIDATE} host=${LITEFS_SELF_HOST} primary=${LITEFS_PRIMARY_URL}"

  # litefs mount 作为 supervisor：挂载 FUSE、连上集群后再按 litefs.yml 的 exec
  # 启动 server，并把信号转发给子进程。
  # 注意 CMD（"$@"）在这条路径上不生效，启动命令由 litefs-exec.sh 决定。
  exec litefs mount
fi

exec su-exec appuser:appgroup "$@"

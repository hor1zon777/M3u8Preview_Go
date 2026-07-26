#!/bin/sh
# litefs-exec.sh —— LiteFS 挂载完成后真正启动 server 的包装脚本。
#
# 为什么需要这一层：LiteFS 官方推荐以 root 运行并用 su 把应用降权，但 LiteFS 没有
# 提供 FUSE 的 uid/gid 映射选项，挂载点里的文件由 root 呈现。appuser 能否在 /litefs
# 下正常读写数据库，取决于 LiteFS 呈现的文件权限位，这一点必须在目标机器上实测
# （见 docs/ha-failover.md §14 上线前验证清单）。
#
# 因此这里保留本项目一贯的降权姿态，同时留一个逃生开关：
# 实测发现 appuser 无法写入时，设 LITEFS_RUN_AS_ROOT=1 即可改为 root 运行。
set -eu

# RUN_SERVER 由 docker-entrypoint.sh 导出（litefs mount 会把环境透传到这里）：
# 自更新装载生效时指向容器层的 /app/server-staged，否则回退镜像内 /app/server。
if [ "${LITEFS_RUN_AS_ROOT:-0}" = "1" ] || [ "$(id -u)" -ne 0 ]; then
  exec "${RUN_SERVER:-/app/server}"
fi

exec su-exec appuser:appgroup "${RUN_SERVER:-/app/server}"

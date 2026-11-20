#!/bin/sh
# 容器以 root 启动，只做一件事：把数据目录交给 memory 用户，然后降权执行服务。
# 宿主机挂进来的 ./data 通常是 root:root 0755，而服务以 uid 10001 运行，
# 不处理就会在 mkdir /data/projects 时 permission denied。
# 首次部署和已有数据目录都在这里统一改属主，使用者不需要手工 chown。
DATA_DIR="${DATA_DIR:-/data}"
APP_UID="${APP_UID:-10001}"
APP_GID="${APP_GID:-10001}"

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DATA_DIR" 2>/dev/null || echo "warn: 无法创建 $DATA_DIR" >&2
  # chown 失败（NFS root_squash、只读挂载等）不拦启动：降权后若仍不可写，
  # 服务会输出明确的 permission denied，比入口脚本直接退出好排查。
  if ! chown -R "$APP_UID:$APP_GID" "$DATA_DIR" 2>/dev/null; then
    echo "warn: 无法修改 $DATA_DIR 的属主，继续以 uid=$APP_UID 启动" >&2
  fi
  exec su-exec "$APP_UID:$APP_GID" /usr/local/bin/team-memory
fi

# 已经以非 root 启动（例如 compose 里写了 user:），不做 chown，直接运行。
exec /usr/local/bin/team-memory

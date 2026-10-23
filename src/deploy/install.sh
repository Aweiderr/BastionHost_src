#!/usr/bin/env bash
#
# 堡垒机 Linux 一键部署脚本（在目标服务器上以 root 执行）
#
#   ./install.sh
#
# 部署完成后：
#   管理控制台   http://<服务器IP>:8080
#   SSH 网关     ssh -p 2222 <用户名>@<服务器IP>
#
# 设计取舍：
#   * 数据库用 Docker 跑并**显式限制内存**，避免在内存紧张的机器上拖垮业务；
#   * 二进制为纯静态（CGO_ENABLED=0），因此对 glibc 版本无要求；
#   * 全程不修改系统防火墙与网络配置，只用 host 网络绑定 127.0.0.1。
#
set -euo pipefail

BASE_DIR=${BASE_DIR:-/opt/bastion}
PG_CONTAINER=${PG_CONTAINER:-bastion-pg}
PG_MEMORY=${PG_MEMORY:-384m}
PG_PASSWORD=${PG_PASSWORD:-bastion}
JWT_SECRET=${JWT_SECRET:-$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')}
INTERNAL_SECRET=${INTERNAL_SECRET:-$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')}
PG_IMAGE=${PG_IMAGE:-postgres:16-alpine}

log() { echo -e "\033[36m[部署]\033[0m $*"; }
die() { echo -e "\033[31m[失败]\033[0m $*" >&2; exit 1; }

[[ $(id -u) -eq 0 ]] || die "请以 root 执行"

log "1/7 检查依赖"
command -v docker >/dev/null || die "未安装 docker"
docker info >/dev/null 2>&1 || die "docker 服务未运行"

log "2/7 准备目录"
mkdir -p "$BASE_DIR"/{bin,etc,data/records,data/server-keys,client/web/dist} /var/log/bastion
chmod 700 "$BASE_DIR/etc"

log "3/7 启动 PostgreSQL 容器（内存上限 $PG_MEMORY）"
if docker ps -a --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
  log "    容器已存在，跳过创建"
else
  # 用 host 网络而非端口映射：老版本 docker（如 18.09）在部分国产系统上
  # 会因 iptables 链缺失导致端口映射失败，host 网络可绕开该问题，
  # 同时通过 listen_addresses 限定只监听本机。
  docker run -d --name "$PG_CONTAINER" \
    --network=host \
    --memory="$PG_MEMORY" --memory-swap="$PG_MEMORY" \
    -e POSTGRES_USER=bastion -e POSTGRES_PASSWORD="$PG_PASSWORD" -e POSTGRES_DB=bastion \
    -e POSTGRES_INITDB_ARGS="--encoding=UTF8" \
    -v "$BASE_DIR/data/pg":/var/lib/postgresql/data \
    "$PG_IMAGE" \
    -c listen_addresses=127.0.0.1 -c shared_buffers=48MB -c work_mem=2MB \
    -c max_connections=20 -c max_wal_size=256MB >/dev/null
fi
for i in $(seq 1 30); do
  docker exec "$PG_CONTAINER" pg_isready -h 127.0.0.1 -U bastion >/dev/null 2>&1 && break
  sleep 2
done
docker exec "$PG_CONTAINER" pg_isready -h 127.0.0.1 -U bastion >/dev/null 2>&1 || die "PostgreSQL 未就绪"

log "4/7 写入环境配置"
cat > "$BASE_DIR/etc/manager.env" <<EOF
BASTION_JWT_SECRET=$JWT_SECRET
BASTION_INTERNAL_SECRET=$INTERNAL_SECRET
BASTION_DB_HOST=127.0.0.1
BASTION_DB_PORT=5432
BASTION_DB_USER=bastion
BASTION_DB_PASSWORD=$PG_PASSWORD
BASTION_DB_NAME=bastion
BASTION_WEB_DIR=$BASE_DIR/client/web/dist
EOF
cat > "$BASE_DIR/etc/server.env" <<EOF
BASTION_INTERNAL_SECRET=$INTERNAL_SECRET
BASTION_MANAGER_URL=http://127.0.0.1:8080
BASTION_MANAGER_TIMEOUT=10s
BASTION_SSH_LISTEN=:2222
BASTION_RECORD_DIR=$BASE_DIR/data/records
BASTION_KEYSTORE_DIR=$BASE_DIR/data/server-keys
BASTION_MASTER_KEY_FILE=$BASE_DIR/data/server-master.key
EOF
chmod 600 "$BASE_DIR"/etc/*.env

log "5/7 初始化数据库"
cd "$BASE_DIR" && set -a && . "$BASE_DIR/etc/manager.env" && set +a
"$BASE_DIR/bin/migrate" up || die "数据库迁移失败"

log "6/7 安装 systemd 服务"
if [[ -d /run/systemd/system ]]; then
  SRC_DIR=$(cd "$(dirname "$0")" && pwd)
  install -m 0644 "$SRC_DIR/bastion-manager.service" /etc/systemd/system/
  install -m 0644 "$SRC_DIR/bastion-server.service" /etc/systemd/system/
  systemctl daemon-reload
  systemctl enable --now bastion-manager bastion-server
  log "    已通过 systemd 启动"
else
  log "    未检测到 systemd，请手工启动 bin/manager 与 bin/server"
fi

log "7/7 健康检查"
sleep 5
curl -s --max-time 8 http://127.0.0.1:8080/healthz || true
echo
curl -s --max-time 8 http://127.0.0.1:8081/readyz || true
echo

cat <<EOF

部署完成
  管理控制台   http://<服务器IP>:8080   （首次访问进入初始化向导）
  SSH 网关     ssh -p 2222 <用户名>@<服务器IP>
  配置目录     $BASE_DIR/etc
  录像目录     $BASE_DIR/data/records
  日志         /var/log/bastion/

提示：服务间密钥与 JWT 密钥已随机生成，请妥善备份 $BASE_DIR/etc/*.env
EOF

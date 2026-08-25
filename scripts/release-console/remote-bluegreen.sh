#!/usr/bin/env bash
set -euo pipefail

ACTION=${1:?缺少操作}
IMAGE=${2:-}
APP_DIR=/home/docker/sub2api
NGINX_DIR=/home/web/conf.d
API_CONF="$NGINX_DIR/api.xnkaixin.eu.cc.conf"
RELAY_CONF="$NGINX_DIR/relay-api.xnkaixin.eu.cc.conf"
ACTIVE_FILE="$NGINX_DIR/sub2api-active-upstream.inc"
RUNTIME_ENV="$APP_DIR/.bluegreen-runtime.env"
CANDIDATE_FILE="$APP_DIR/.bluegreen-candidate-image"
BLUE_IMAGE_FILE="$APP_DIR/.bluegreen-blue-image"
CANDIDATE_SLOT_FILE="$APP_DIR/.bluegreen-candidate-slot"
PREVIOUS_ACTIVE_FILE="$APP_DIR/.bluegreen-previous-active"
BLUE_PORT=8080
GREEN_PORT=18081
CANARY_PORT=18082

log() { printf '[%s] %s\n' "$(date '+%F %T')" "$*"; }
health() { curl -fsS --max-time 10 "http://127.0.0.1:$1/health" >/dev/null; }
nginx_reload() { docker exec nginx nginx -t >/dev/null && docker exec nginx nginx -s reload; }
active_port() { sed -nE 's/^[[:space:]]*server[[:space:]]+127\.0\.0\.1:([0-9]+);.*/\1/p' "$ACTIVE_FILE" | head -n 1; }
container_for_port() {
  case "$1" in
    "$BLUE_PORT") printf 'sub2api\n' ;;
    "$GREEN_PORT") printf 'sub2api-green\n' ;;
    "$CANARY_PORT") printf 'sub2api-canary\n' ;;
    *) return 1 ;;
  esac
}
record_previous_active() {
  local port container image
  port=$(active_port)
  container=$(container_for_port "$port")
  health "$port"
  image=$(docker inspect "$container" --format '{{.Config.Image}}')
  printf '%s|%s|%s\n' "$port" "$container" "$image" > "$PREVIOUS_ACTIVE_FILE"
  chmod 600 "$PREVIOUS_ACTIVE_FILE"
}
ensure_runtime_env() {
  if [[ ! -f "$RUNTIME_ENV" ]]; then
    umask 077
    docker inspect sub2api --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -Ev '^(DEPLOYMENT_ROLE|DATABASE_MAX_OPEN_CONNS|DATABASE_MAX_IDLE_CONNS)=' > "$RUNTIME_ENV"
  fi
  chmod 600 "$RUNTIME_ENV"
}
run_api_slot() {
	local name=$1 port=$2 image=$3 active
	active=$(active_port || true)
	[[ "$active" != "$port" ]] || { log "拒绝替换正在承接流量的候选端口: $port"; return 1; }
	ensure_runtime_env
	docker rm -f "$name" >/dev/null 2>&1 || true
	docker run -d --name "$name" --restart unless-stopped --network sub2api_sub2api-network -v sub2api_sub2api_data:/app/data -p "127.0.0.1:$port:8080" --security-opt no-new-privileges:true --ulimit nofile=100000:100000 --env-file "$RUNTIME_ENV" -e DEPLOYMENT_ROLE=api_only -e DATABASE_MAX_OPEN_CONNS=20 -e DATABASE_MAX_IDLE_CONNS=5 "$image" >/dev/null
	for _ in $(seq 1 24); do health "$port" && return; sleep 5; done
	docker logs --tail 80 "$name" >&2 || true
	return 1
}
write_active() {
  local port=$1 tmp
  tmp=$(mktemp "$NGINX_DIR/.sub2api-active.XXXXXX")
  printf 'server 127.0.0.1:%s;\n' "$port" > "$tmp"
  mv "$tmp" "$ACTIVE_FILE"
}
prepare_nginx() {
  [[ -f "$API_CONF" && -f "$RELAY_CONF" ]] || { log '缺少现有 Nginx 站点配置'; exit 1; }
  cp -n "$API_CONF" "$API_CONF.pre-bluegreen" || true
  cp -n "$RELAY_CONF" "$RELAY_CONF.pre-bluegreen" || true
  grep -q 'sub2api-active-upstream.inc' "$API_CONF" || sed -i '/upstream backend_UvbsfVUm {/,/^[[:space:]]*}/{s|server 127.0.0.1:8080;|include /etc/nginx/conf.d/sub2api-active-upstream.inc;|}' "$API_CONF"
  grep -q 'sub2api-active-upstream.inc' "$RELAY_CONF" || sed -i '/upstream relay_backend {/,/^[[:space:]]*}/{s|server 127.0.0.1:8080;|include /etc/nginx/conf.d/sub2api-active-upstream.inc;|}' "$RELAY_CONF"
  [[ -f "$ACTIVE_FILE" ]] || write_active 8080
  nginx_reload
}
candidate_image() { [[ -f "$CANDIDATE_FILE" ]] && cat "$CANDIDATE_FILE" || docker inspect sub2api --format '{{.Config.Image}}'; }
blue_image() { [[ -f "$BLUE_IMAGE_FILE" ]] && cat "$BLUE_IMAGE_FILE" || docker inspect sub2api --format '{{.Config.Image}}'; }

backup_release() {
  local timestamp backup_dir image rollback_tag
  timestamp=$(date +%Y%m%d-%H%M%S)
  backup_dir="$APP_DIR/backups/bluegreen-$timestamp"
  mkdir -p "$backup_dir"
  chmod 700 "$backup_dir"
  cp -p "$APP_DIR/docker-compose.yml" "$backup_dir/docker-compose.yml"
  [[ ! -f "$APP_DIR/.env" ]] || { cp -p "$APP_DIR/.env" "$backup_dir/.env"; chmod 600 "$backup_dir/.env"; }
  cp -p "$API_CONF" "$backup_dir/api.xnkaixin.eu.cc.conf"
  cp -p "$RELAY_CONF" "$backup_dir/relay-api.xnkaixin.eu.cc.conf"
  cp -p /home/web/nginx.conf "$backup_dir/nginx.conf"
  image=$(docker inspect sub2api --format '{{.Config.Image}}')
  rollback_tag="weishaw/sub2api:rollback-$timestamp"
  docker tag "$image" "$rollback_tag"
  printf '%s\n' "$image" > "$backup_dir/running-image.txt"
  printf '%s\n' "$rollback_tag" > "$backup_dir/rollback-image.txt"
  docker exec sub2api-postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner --no-acl' > "$backup_dir/postgres.dump"
  chmod 600 "$backup_dir"/postgres.dump "$backup_dir"/*.txt
  (cd "$backup_dir" && sha256sum postgres.dump running-image.txt rollback-image.txt > SHA256SUMS)
  log "发布备份已完成: $backup_dir"
}

case "$ACTION" in
  backup-release)
    backup_release
    ;;
  load-candidate)
    [[ "$IMAGE" =~ ^weishaw/sub2api:[A-Za-z0-9._-]+$ ]] || { log '候选镜像标签无效'; exit 1; }
    cd "$APP_DIR"
    sha256sum -c .bluegreen-candidate.tar.gz.sha256
    gzip -dc .bluegreen-candidate.tar.gz | docker load
    docker image inspect "$IMAGE" >/dev/null
    printf '%s\n' "$IMAGE" > "$CANDIDATE_FILE"
    rm -f .bluegreen-candidate.tar.gz .bluegreen-candidate.tar.gz.sha256
    log "候选镜像已校验并导入"
    ;;
  prepare-bluegreen)
    prepare_nginx
    docker inspect sub2api --format '{{.Config.Image}}' > "$BLUE_IMAGE_FILE"
	active=$(active_port)
	if [[ "$active" == "$GREEN_PORT" ]]; then
		candidate_name=sub2api-canary
		candidate_port=$CANARY_PORT
	else
		candidate_name=sub2api-green
		candidate_port=$GREEN_PORT
	fi
    image=$(candidate_image)
	if [[ "$active" == "$candidate_port" ]]; then
		log '候选端口正在承接流量，拒绝覆盖'
		exit 1
	fi
	run_api_slot "$candidate_name" "$candidate_port" "$image"
	printf '%s|%s\n' "$candidate_name" "$candidate_port" > "$CANDIDATE_SLOT_FILE"
	log "候选 api_only 实例健康: $candidate_name:$candidate_port，现网流量未切换"
    ;;
  canary-green)
	[[ -f "$CANDIDATE_SLOT_FILE" ]] || { log '没有已预热候选'; exit 1; }
	IFS='|' read -r candidate_name candidate_port < "$CANDIDATE_SLOT_FILE"
	[[ "$candidate_name" =~ ^sub2api-(green|canary)$ && "$candidate_port" =~ ^(18081|18082)$ ]] || { log '候选槽位无效'; exit 1; }
	health "$candidate_port"
	record_previous_active
	write_active "$candidate_port"; nginx_reload
    curl -fsS --max-time 10 https://api.xnkaixin.eu.cc/health >/dev/null
    log "新请求已平滑切至候选 api_only: $candidate_name:$candidate_port；上一次健康实例保留作即时回退"
    ;;
  return-blue)
    health "$BLUE_PORT"
    write_active "$BLUE_PORT"; nginx_reload
    curl -fsS --max-time 10 https://api.xnkaixin.eu.cc/health >/dev/null
    log '新请求已平滑切回稳定蓝色；绿色 api_only 继续保温'
    ;;
  rollback)
    if [[ -f "$PREVIOUS_ACTIVE_FILE" ]]; then
      IFS='|' read -r previous_port previous_name previous_image < "$PREVIOUS_ACTIVE_FILE"
      [[ "$previous_port" =~ ^(8080|18081|18082)$ ]] || { log '历史活跃槽位无效'; exit 1; }
      health "$previous_port"
      write_active "$previous_port"; nginx_reload
    else
      health "$BLUE_PORT"
      write_active "$BLUE_PORT"; nginx_reload
    fi
    curl -fsS --max-time 10 https://api.xnkaixin.eu.cc/health >/dev/null
    log '请求已平滑回切至上一次健康活跃实例；候选 api_only 保留供排查'
    ;;
  *) log '不允许的远程操作'; exit 1 ;;
esac

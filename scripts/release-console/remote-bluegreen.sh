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
PRIMARY_NAME=sub2api
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
    local active source_container
    umask 077
    # primary 丢失时从当前健康流量槽位恢复运行环境，避免自愈依赖已丢失容器。
    active=$(active_port)
    source_container=$(container_for_port "$active")
    health "$active"
    docker inspect "$source_container" --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -Ev '^(DEPLOYMENT_ROLE|DATABASE_MAX_OPEN_CONNS|DATABASE_MAX_IDLE_CONNS)=' > "$RUNTIME_ENV"
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
ensure_primary_slot() {
  local image=$1 active role current_image
  [[ "$image" =~ ^weishaw/sub2api:[A-Za-z0-9._-]+$ ]] || { log 'primary 镜像标签无效'; return 1; }
  if docker inspect "$PRIMARY_NAME" >/dev/null 2>&1; then
    role=$(docker inspect "$PRIMARY_NAME" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^DEPLOYMENT_ROLE=//p' | head -n 1)
    current_image=$(docker inspect "$PRIMARY_NAME" --format '{{.Config.Image}}')
    if [[ "$role" == primary ]] && [[ "$current_image" == "$image" ]] && health "$BLUE_PORT"; then
      log "primary 后台节点健康且版本一致，保持现有实例: $PRIMARY_NAME"
      return
    fi
    active=$(active_port || true)
    [[ "$active" != "$BLUE_PORT" ]] || { log '拒绝替换正在承接流量的 primary'; return 1; }
    # 流量已由健康 API 槽位承接；后台节点统一通过 Compose 更新，保留项目运行配置。
  fi
  ensure_runtime_env
  local compose_tmp
  compose_tmp=$(mktemp "$APP_DIR/.compose-candidate.XXXXXX")
  python3 - "$APP_DIR/docker-compose.yml" "$compose_tmp" "$image" <<'PYCOMPOSE'
import re,sys
src,out,image=sys.argv[1:]
text=open(src).read()
match=re.search(r'(?m)^  sub2api:\s*$',text)
if not match: raise SystemExit('未找到 sub2api Compose 服务')
end=re.search(r'(?m)^  [a-zA-Z0-9_-]+:\s*$',text[match.end():])
stop=match.end()+end.start() if end else len(text)
section,n=re.subn(r'(?m)^(    image:) .+$',lambda m:m[1]+' '+image,text[match.start():stop])
if n!=1: raise SystemExit('sub2api 镜像配置不唯一')
with open(out,'w') as f:f.write(text[:match.start()]+section+text[stop:])
PYCOMPOSE
  chmod --reference="$APP_DIR/docker-compose.yml" "$compose_tmp"
  mv "$compose_tmp" "$APP_DIR/docker-compose.yml"
  (cd "$APP_DIR" && docker compose up -d --no-deps --force-recreate --pull never sub2api)
  for _ in $(seq 1 24); do
    if health "$BLUE_PORT"; then
      log "唯一 primary 后台节点已恢复: $PRIMARY_NAME:$BLUE_PORT"
      return
    fi
    sleep 5
  done
  docker logs --tail 80 "$PRIMARY_NAME" >&2 || true
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
candidate_image() { [[ -f "$CANDIDATE_FILE" ]] && cat "$CANDIDATE_FILE" || docker inspect "$(container_for_port "$(active_port)")" --format '{{.Config.Image}}'; }
blue_image() { [[ -f "$BLUE_IMAGE_FILE" ]] && cat "$BLUE_IMAGE_FILE" || docker inspect "$(container_for_port "$(active_port)")" --format '{{.Config.Image}}'; }

backup_release() {
  local timestamp backup_dir active active_container image rollback_tag
  timestamp=$(date +%Y%m%d-%H%M%S)
  backup_dir="$APP_DIR/backups/bluegreen-$timestamp"
  mkdir -p "$backup_dir"
  chmod 700 "$backup_dir"
  cp -p "$APP_DIR/docker-compose.yml" "$backup_dir/docker-compose.yml"
  [[ ! -f "$APP_DIR/.env" ]] || { cp -p "$APP_DIR/.env" "$backup_dir/.env"; chmod 600 "$backup_dir/.env"; }
  cp -p "$API_CONF" "$backup_dir/api.xnkaixin.eu.cc.conf"
  cp -p "$RELAY_CONF" "$backup_dir/relay-api.xnkaixin.eu.cc.conf"
  cp -p /home/web/nginx.conf "$backup_dir/nginx.conf"
  active=$(active_port)
  active_container=$(container_for_port "$active")
  health "$active"
  image=$(docker inspect "$active_container" --format '{{.Config.Image}}')
  rollback_tag="weishaw/sub2api:rollback-$timestamp"
  docker tag "$image" "$rollback_tag"
  printf '%s\n' "$image" > "$backup_dir/running-image.txt"
  printf '%s\n' "$rollback_tag" > "$backup_dir/rollback-image.txt"
  docker exec sub2api-postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner --no-acl' > "$backup_dir/postgres.dump"
  chmod 600 "$backup_dir"/postgres.dump "$backup_dir"/*.txt
  (cd "$backup_dir" && sha256sum postgres.dump running-image.txt rollback-image.txt docker-compose.yml api.xnkaixin.eu.cc.conf relay-api.xnkaixin.eu.cc.conf nginx.conf > SHA256SUMS; if [[ -f .env ]]; then sha256sum .env >> SHA256SUMS; fi)
  log "发布备份已完成: $backup_dir"
}

is_complete_backup() {
  local dir=$1
  [[ -f "$dir/SHA256SUMS" && -f "$dir/postgres.dump" ]] || return 1
  (cd "$dir" && sha256sum -c SHA256SUMS >/dev/null 2>&1)
}

cleanup_release() {
  local latest_backup='' latest_entry dir keep_rollback active active_container current_image previous_port previous_name previous_image image_id tag referenced
  while IFS= read -r latest_entry; do
    dir=${latest_entry#* }
    if is_complete_backup "$dir"; then
      latest_backup=$dir
      break
    fi
  done < <(find "$APP_DIR/backups" -mindepth 1 -maxdepth 1 -type d -name 'bluegreen-*' -printf '%T@ %p\n' | sort -nr)
  [[ -n "$latest_backup" ]] || { log '没有可验证的完整发布备份，拒绝清理'; return 1; }
  keep_rollback=$(tr -d '[:space:]' < "$latest_backup/rollback-image.txt")
  active=$(active_port)
  active_container=$(container_for_port "$active")
  health "$active"
  [[ -f "$PREVIOUS_ACTIVE_FILE" ]] || { log '缺少上一版本活跃实例记录，拒绝清理'; return 1; }
  IFS='|' read -r previous_port previous_name previous_image < "$PREVIOUS_ACTIVE_FILE"
  [[ "$previous_name" =~ ^sub2api(-green|-canary)?$ && "$previous_port" =~ ^(8080|18081|18082)$ ]] || { log '上一版本活跃实例记录无效'; return 1; }
  health "$previous_port"
  curl -fsS --max-time 10 https://api.xnkaixin.eu.cc/health >/dev/null
  current_image=$(docker inspect "$active_container" --format '{{.Config.Image}}')
  ensure_primary_slot "$current_image"

  # primary 专门承担后台任务，不能按非流量槽位清理。
  for slot in "$PRIMARY_NAME" sub2api-green sub2api-canary; do
    [[ "$slot" == "$PRIMARY_NAME" ]] && continue
    [[ "$slot" == "$active_container" || "$slot" == "$previous_name" ]] && continue
    docker inspect "$slot" >/dev/null 2>&1 || continue
    docker rm -f "$slot" >/dev/null
    log "已删除未承流量的旧应用容器: $slot"
  done

  # 只删除未被任何容器引用的旧应用镜像标签，当前、上一版本和最新回滚始终保留。
  for tag in $(docker image ls 'weishaw/sub2api:*' --format '{{.Repository}}:{{.Tag}}' | sort -u); do
    [[ "$tag" == "$current_image" || "$tag" == "$previous_image" || "$tag" == "$keep_rollback" ]] && continue
    image_id=$(docker image inspect "$tag" --format '{{.Id}}')
    referenced=0
    while read -r container_id; do
      [[ -n "$container_id" ]] || continue
      if [[ "$(docker inspect "$container_id" --format '{{.Image}}')" == "$image_id" ]]; then referenced=1; break; fi
    done < <(docker ps -aq --filter 'name=^/sub2api')
    if [[ "$referenced" == 1 ]]; then
      log "保留仍被容器引用的镜像标签: $tag"
    else
      docker rmi "$tag" >/dev/null
      log "已删除旧镜像标签: $tag"
    fi
  done

  # 只保留最近一次校验通过的完整备份目录，旧目录逐个精确删除。
  while IFS= read -r dir; do
    [[ "$dir" == "$latest_backup" ]] && continue
    is_complete_backup "$dir" || continue
    rm -rf -- "$dir"
    log "已删除旧发布备份: $dir"
  done < <(find "$APP_DIR/backups" -mindepth 1 -maxdepth 1 -type d -name 'bluegreen-*' -print)
  log "发布资产清理完成，保留最新完整备份: $latest_backup"
}

case "$ACTION" in
  ensure-primary)
    active=$(active_port)
    active_container=$(container_for_port "$active")
    health "$active"
    image=$(docker inspect "$active_container" --format '{{.Config.Image}}')
    ensure_primary_slot "$image"
    ;;
  backup-release)
    backup_release
    ;;
  cleanup-release)
    cleanup_release
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
		active=$(active_port)
		active_container=$(container_for_port "$active")
		image=$(docker inspect "$active_container" --format '{{.Config.Image}}')
		ensure_primary_slot "$image"
		printf '%s\n' "$image" > "$BLUE_IMAGE_FILE"
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
	active=$(active_port)
	active_container=$(container_for_port "$active")
	image=$(candidate_image)
	ensure_primary_slot "$image"
	record_previous_active
	write_active "$candidate_port"; nginx_reload
    curl -fsS --max-time 10 https://api.xnkaixin.eu.cc/health >/dev/null
    log "新请求已平滑切至候选 api_only: $candidate_name:$candidate_port；上一次健康实例保留作即时回退"
    ;;
  return-blue)
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
    log '新请求已平滑切回上一版本健康实例；候选 api_only 继续保温'
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

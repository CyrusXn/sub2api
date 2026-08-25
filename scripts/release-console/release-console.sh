#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
STATE_DIR="$ROOT_DIR/.release-console"
CONFIG_FILE="$ROOT_DIR/sub2api-production-server.local"
REMOTE_SCRIPT="$ROOT_DIR/scripts/release-console/remote-bluegreen.sh"
mkdir -p "$STATE_DIR"

log() { printf '[%s] %s\n' "$(date '+%F %T')" "$*"; }
fail() { log "错误: $*" >&2; exit 1; }
require_config() { [[ -f "$CONFIG_FILE" ]] || fail '缺少本机生产连接配置'; source "$CONFIG_FILE"; }
ssh_remote() { ssh -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=15 "${SUB2API_PRODUCTION_SSH_USER}@${SUB2API_PRODUCTION_HOST}" "$@"; }
remote_action() { require_config; ssh_remote 'bash -s' -- "$@" < "$REMOTE_SCRIPT"; }

status() {
  require_config
  ssh_remote 'bash -s' <<'REMOTE'
set -euo pipefail
printf '外部健康: '; curl -fsS --max-time 10 https://api.xnkaixin.eu.cc/health; printf '\n'
printf '应用容器:\n'; docker ps --filter 'name=^/sub2api' --format '{{.Names}}|{{.Image}}|{{.Status}}'
printf '资源:\n'; ids=$(docker ps -q --filter 'name=^/sub2api'); [ -z "$ids" ] || docker stats --no-stream --format '{{.Name}}|CPU={{.CPUPerc}}|内存={{.MemUsage}}' $ids
printf 'Nginx: '; docker exec nginx nginx -t >/dev/null && printf '配置有效\n'
REMOTE
}

build() {
  [[ -z "$(git -C "$ROOT_DIR" status --porcelain)" ]] || fail '工作区存在未提交改动，拒绝构建候选'
  git -C "$ROOT_DIR" fetch origin main --tags
  local head version edition image archive checksum
  head=$(git -C "$ROOT_DIR" rev-parse --short HEAD)
  version=$(tr -d '[:space:]' < "$ROOT_DIR/backend/cmd/server/VERSION")
  edition="xnkaixin.$(date +%Y%m%d)"
  image="weishaw/sub2api:${edition}-${version}-${head}-full"
  archive="$STATE_DIR/${image##*:}.tar.gz"
  checksum="$archive.sha256"
  log "构建候选镜像 $image"
  docker buildx build --builder colima --platform linux/amd64 --load --build-arg "VERSION=$version" --build-arg BUILD_TYPE=custom --build-arg "EDITION=$edition" -t "$image" "$ROOT_DIR"
  docker save "$image" | gzip > "$archive"
  shasum -a 256 "$archive" > "$checksum"
  node -e 'const fs=require("fs");const [sha]=fs.readFileSync(process.argv[2],"utf8").split(/\s+/);fs.writeFileSync(process.argv[1],JSON.stringify({status:"ready",image:process.argv[3],archive:process.argv[4],sha256:sha,commit:process.argv[5]},null,2))' "$STATE_DIR/candidate.json" "$checksum" "$image" "$archive" "$head"
  log "候选已准备: $image"
}

stage_candidate() {
  local candidate="$STATE_DIR/candidate.json"
  [[ -f "$candidate" ]] || fail '没有已构建候选'
  local image archive sha
  image=$(node -p "require(process.argv[1]).image" "$candidate")
  archive=$(node -p "require(process.argv[1]).archive" "$candidate")
  sha=$(node -p "require(process.argv[1]).sha256" "$candidate")
  [[ -f "$archive" && "$sha" =~ ^[a-fA-F0-9]{64}$ ]] || fail '候选归档或 SHA256 无效'
  require_config
  scp -o BatchMode=yes -o StrictHostKeyChecking=yes "$archive" "${SUB2API_PRODUCTION_SSH_USER}@${SUB2API_PRODUCTION_HOST}:/home/docker/sub2api/.bluegreen-candidate.tar.gz"
  printf '%s  %s\n' "$sha" '.bluegreen-candidate.tar.gz' | ssh_remote 'cat > /home/docker/sub2api/.bluegreen-candidate.tar.gz.sha256'
  remote_action load-candidate "$image"
}

deploy() {
  build
  remote_action backup-release
  stage_candidate
  remote_action prepare-bluegreen
  remote_action canary-green
  status
  remote_action cleanup-release
  cleanup_local_assets
}

cleanup_local_assets() {
  local candidate archive checksum file
  candidate="$STATE_DIR/candidate.json"
  [[ -f "$candidate" ]] || fail '没有候选记录，拒绝清理本地发布资产'
  archive=$(node -p "require(process.argv[1]).archive" "$candidate")
  checksum="$archive.sha256"
  for file in "$STATE_DIR"/*.tar.gz "$STATE_DIR"/*.tar.gz.sha256; do
    [[ -e "$file" ]] || continue
    [[ "$file" == "$archive" || "$file" == "$checksum" ]] || rm -f -- "$file"
  done
  log "本地旧发布归档已清理，保留当前候选归档"
}

case "${1:-}" in
  status) status ;;
  build) build ;;
  deploy) deploy ;;
  stage-candidate|prepare-bluegreen|canary-green|return-blue|rollback|cleanup-release) [[ "$1" == stage-candidate ]] && stage_candidate || remote_action "$1" ;;
  *) fail '允许操作: status, build, deploy, stage-candidate, prepare-bluegreen, canary-green, return-blue, rollback, cleanup-release' ;;
esac

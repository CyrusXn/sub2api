#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
APP_DIR=${SUB2API_APP_DIR:-/home/docker/sub2api}
STATE_DIR="$APP_DIR/.server-release"
BUILDER=sub2api-release
REPOSITORY=https://github.com/CyrusXn/sub2api.git
BRANCH=xnkaixin
BLUEGREEN="$SCRIPT_DIR/remote-bluegreen.sh"
PUBLIC_URL=https://api.xnkaixin.eu.cc

log() { printf '[%s] %s\n' "$(date '+%F %T')" "$*"; }
fail() { log "错误: $*" >&2; exit 1; }
bluegreen() { bash "$BLUEGREEN" "$@"; }
valid_commit() { [[ "$1" =~ ^[a-f0-9]{40}$ ]]; }
active_port() { sed -nE 's/^[[:space:]]*server[[:space:]]+127\.0\.0\.1:([0-9]+);.*/\1/p' /home/web/conf.d/sub2api-active-upstream.inc; }
infra_state() { docker inspect nginx sub2api-postgres sub2api-redis --format '{{.Name}}|{{.Id}}|{{.State.StartedAt}}|{{.RestartCount}}'; }

check_resources() {
  local available_kb available_mb
  available_kb=$(df -Pk "$APP_DIR" | awk 'NR==2 {print $4}')
  available_mb=$(awk '/^MemAvailable:/ {print int($2/1024)}' /proc/meminfo)
  (( available_kb >= 20 * 1024 * 1024 )) || fail '磁盘可用空间不足 20 GiB，停止构建'
  (( available_mb >= 4608 )) || fail '可用内存不足 4.5 GiB，停止构建以保护线上服务'
  curl -fsS --max-time 15 "$PUBLIC_URL/health" >/dev/null
}

ensure_builder() {
  # 独立缓存与资源上限使编译不会占满生产主机，也不清理其他项目的缓存。
  if ! docker buildx inspect "$BUILDER" >/dev/null 2>&1; then
    printf '[worker.oci]\n  max-parallelism = 1\n' > "$STATE_DIR/buildkitd.toml"
    docker buildx create --name "$BUILDER" --driver docker-container \
      --driver-opt memory=3g --driver-opt memory-swap=3g \
      --driver-opt cpu-period=100000 --driver-opt cpu-quota=200000 \
      --buildkitd-config "$STATE_DIR/buildkitd.toml" >/dev/null
  fi
  docker buildx inspect "$BUILDER" --bootstrap >/dev/null
  docker inspect "buildx_buildkit_${BUILDER}0" --format '{{json .HostConfig}}' | python3 -c '
import json,sys
c=json.load(sys.stdin)
assert 0 < c["Memory"] <= 3*1024**3, "构建器内存限制无效"
assert c["MemorySwap"] == c["Memory"], "构建器交换空间限制无效"
assert c["CpuPeriod"] > 0 and 0 < c["CpuQuota"]/c["CpuPeriod"] <= 2, "构建器 CPU 限制无效"
'
}

build_candidate() {
  local commit=${1:-} version image source
  valid_commit "$commit" || fail '请指定已审核的完整 40 位定制提交 SHA'
  check_resources
  if [[ ! -d "$STATE_DIR/repo.git" ]]; then
    git init --bare "$STATE_DIR/repo.git" >/dev/null
  fi
  GIT_TERMINAL_PROMPT=0 git --git-dir="$STATE_DIR/repo.git" fetch --no-tags "$REPOSITORY" "+refs/heads/$BRANCH:refs/heads/$BRANCH"
  git --git-dir="$STATE_DIR/repo.git" merge-base --is-ancestor "$commit" "refs/heads/$BRANCH" || fail '提交不属于已发布的定制分支'
  version=$(git --git-dir="$STATE_DIR/repo.git" show "$commit:backend/cmd/server/VERSION" | tr -d '[:space:]')
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail '版本号必须为纯语义版本'
  image="weishaw/sub2api:xnkaixin-$version-server-${commit:0:12}"
  source="$STATE_DIR/source-$commit"
  if docker image inspect "$image" >/dev/null 2>&1; then
    finish_candidate "$image" "$version" "$commit"
    log '复用已校验的同提交镜像，不覆盖已有镜像标签'
    return
  fi
  [[ ! -e "$source" ]] || fail '存在上次失败构建的源码目录，请先执行 cleanup-build'
  mkdir "$source"
  git --git-dir="$STATE_DIR/repo.git" archive "$commit" | tar -x -C "$source"
  # 只调整构建进程资源，不改业务源码；大体积 ent 包需要更积极回收内存。
  python3 - "$source/Dockerfile" "$source/.server-release.Dockerfile" <<'PYBUILD'
from pathlib import Path
import sys
text = Path(sys.argv[1]).read_text()
marker = 'CGO_ENABLED=0 GOOS='
if text.count(marker) != 1:
    raise SystemExit('Dockerfile 构建结构已变化，请审核资源限制适配后重试')
Path(sys.argv[2]).write_text(text.replace(marker,
    'GOMEMLIMIT=1800MiB GOGC=30 GOMAXPROCS=2 GOFLAGS=-p=1 ' + marker))
PYBUILD
  ensure_builder
  log "服务器开始构建: $image"
  docker buildx build --builder "$BUILDER" --platform linux/amd64 --load \
    --build-arg "VERSION=$version" --build-arg "COMMIT=$commit" \
    --build-arg BUILD_TYPE=custom --build-arg "EDITION=xnkaixin.$(date +%Y%m%d)" \
    --build-arg GOPROXY=https://proxy.golang.org,direct --build-arg GOSUMDB=sum.golang.org \
    --build-arg NPM_CONFIG_REGISTRY=https://registry.npmjs.org \
    --label "io.sub2api.release.commit=$commit" --label "io.sub2api.release.version=$version" \
    -f "$source/.server-release.Dockerfile" -t "$image" "$source"
  finish_candidate "$image" "$version" "$commit"
}

finish_candidate() {
  local image=$1 version=$2 commit=$3 binary_info
  [[ "$(docker image inspect "$image" --format '{{index .Config.Labels "io.sub2api.release.commit"}}')" == "$commit" ]] || fail '已有镜像提交标签不匹配'
  # -version 在初始化服务前退出，禁用网络可证明此验证不会访问生产数据库。
  binary_info=$(docker run --rm --network none --entrypoint /app/sub2api "$image" -version 2>&1)
  [[ "$binary_info" == *"Sub2API $version "* && "$binary_info" == *"commit: $commit,"* ]] || fail '候选二进制的版本或提交不匹配'
  printf '%s|%s|%s\n' "$image" "$version" "$commit" > "$STATE_DIR/candidate.tmp"
  mv "$STATE_DIR/candidate.tmp" "$STATE_DIR/candidate"
  log "服务器候选已构建并校验: $image"
}

read_candidate() {
  local record=${1:-$STATE_DIR/candidate}
  [[ -f "$record" ]] || fail '缺少候选记录，禁止发布或恢复'
  IFS='|' read -r CANDIDATE_IMAGE CANDIDATE_VERSION CANDIDATE_COMMIT < "$record"
  valid_commit "$CANDIDATE_COMMIT" || fail '候选提交记录无效'
  [[ "$CANDIDATE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail '候选版本记录无效'
  [[ "$CANDIDATE_IMAGE" == "weishaw/sub2api:xnkaixin-$CANDIDATE_VERSION-server-${CANDIDATE_COMMIT:0:12}" ]] || fail '候选镜像记录无效'
  [[ "$(docker image inspect "$CANDIDATE_IMAGE" --format '{{index .Config.Labels "io.sub2api.release.commit"}}')" == "$CANDIDATE_COMMIT" ]] || fail '候选镜像标签的提交不匹配'
}

verify_version() {
  local url=$1 version=$2
  curl -fsS --max-time 15 "$url/health" >/dev/null || return 1
  curl -fsS --max-time 15 "$url/api/v1/settings/public" | python3 -c '
import json,sys
assert json.load(sys.stdin)["data"]["version"] == sys.argv[1], "公开版本不匹配"
' "$version"
}

verify_deployment() {
  local image=$1 version=$2 port name current_id
  port=$(active_port)
  case "$port" in 18081) name=sub2api-green;; 18082) name=sub2api-canary;; *) fail '流量必须由 API 槽位承接';; esac
  current_id=$(docker image inspect "$image" --format '{{.Id}}')
  [[ "$(docker inspect "$name" --format '{{.Image}}')" == "$current_id" ]] || fail '活跃槽位镜像与发布记录不一致'
  [[ "$(docker inspect sub2api --format '{{.Image}}')" == "$current_id" ]] || fail '后台节点镜像与发布记录不一致'
  verify_version "http://127.0.0.1:$port" "$version" || return 1
  verify_version http://127.0.0.1:8080 "$version" || return 1
  verify_version "$PUBLIC_URL" "$version" || return 1
  docker exec nginx nginx -t >/dev/null
}

cleanup_build() {
  local directory base
  if docker buildx inspect "$BUILDER" >/dev/null 2>&1; then
    docker buildx prune --builder "$BUILDER" --all --force
    docker buildx stop "$BUILDER"
  fi
  for directory in "$STATE_DIR"/source-*; do
    [[ -d "$directory" && ! -L "$directory" ]] || continue
    base=${directory##*/}
    [[ "$base" =~ ^source-[a-f0-9]{40}$ ]] || fail '构建源码目录名称异常，拒绝清理'
    rm -rf -- "$directory"
  done
  log '已清理 Sub2API 专用构建缓存与临时源码，构建器已停止'
}

cleanup_success() {
  local image version commit
  [[ -f "$STATE_DIR/last-success" ]] || fail '缺少成功发布记录，拒绝轮转备份'
  IFS='|' read -r image version commit < "$STATE_DIR/last-success"
  valid_commit "$commit" || fail '成功发布记录无效'
  verify_deployment "$image" "$version"
  bluegreen cleanup-release
  cleanup_build
  verify_deployment "$image" "$version"
  log '发布清理完成：保留当前版本与上一版本完整回滚资产'
}

finish_release() {
  local original_port
  read_candidate "$STATE_DIR/pending-candidate"
  [[ "$CANDIDATE_COMMIT" == "$(cat "$STATE_DIR/pending-commit")" ]] || fail '恢复候选与待完成提交不匹配'
  [[ -f "$STATE_DIR/original-port" && -f "$STATE_DIR/infra-before" ]] || fail '发布恢复记录不完整，禁止宣称成功'
  original_port=$(cat "$STATE_DIR/original-port")
  if ! (verify_deployment "$CANDIDATE_IMAGE" "$CANDIDATE_VERSION" &&
        infra_state > "$STATE_DIR/infra-after" &&
        cmp -s "$STATE_DIR/infra-before" "$STATE_DIR/infra-after"); then
    bluegreen restore-active "$original_port" || fail '发布验证失败，自动回切未成功；已保留全部回滚资产'
    fail '发布验证失败，已回切原健康流量槽位，未清理任何回滚资产'
  fi
  cp "$STATE_DIR/pending-candidate" "$STATE_DIR/last-success.tmp"
  mv "$STATE_DIR/last-success.tmp" "$STATE_DIR/last-success"
  cleanup_success
  rm -f "$STATE_DIR/pending-commit" "$STATE_DIR/pending-candidate"
  log "服务器发布完成: $CANDIDATE_IMAGE"
}

deploy() {
  valid_commit "${1:-}" || fail '请指定已审核的完整提交 SHA'
  if [[ -f "$STATE_DIR/pending-commit" ]]; then
    [[ "$(cat "$STATE_DIR/pending-commit")" == "$1" ]] || fail '上一发布尚未完成，请先恢复原提交的验收与清理'
    if already_deployed "$1"; then
      finish_release
    else
      [[ "$(active_port)" == "$(cat "$STATE_DIR/original-port")" ]] || fail '当前流量与待恢复发布记录不符，停止以保护回滚资产'
      # 回切后的重试复用冻结候选和原备份，不能再次备份覆盖真实上一版本。
      activate_candidate
    fi
    return
  fi
  if [[ -f "$STATE_DIR/last-success" ]]; then
    local saved_image saved_version saved_commit
    IFS='|' read -r saved_image saved_version saved_commit < "$STATE_DIR/last-success"
    if [[ "$saved_commit" == "$1" ]] && already_deployed "$1"; then
      cleanup_success
      log '该提交已上线，已重新验收并补齐发布清理'
      return
    fi
  fi
  if already_deployed "$1"; then
    curl -fsS --max-time 15 "http://127.0.0.1:$(active_port)/health" >/dev/null
    curl -fsS --max-time 15 http://127.0.0.1:8080/health >/dev/null
    curl -fsS --max-time 15 "$PUBLIC_URL/health" >/dev/null
    log '该提交已由健康主节点和活跃槽位运行，不重复部署或轮转上一版本备份'
    return
  fi
  build_candidate "${1:-}"
  read_candidate
  infra_state > "$STATE_DIR/infra-before"
  bluegreen backup-release
  local original_port
  original_port=$(active_port)
  printf '%s\n' "$original_port" > "$STATE_DIR/original-port"
  cp "$STATE_DIR/candidate" "$STATE_DIR/pending-candidate"
  printf '%s\n' "$CANDIDATE_COMMIT" > "$STATE_DIR/pending-commit"
  activate_candidate
}

activate_candidate() {
  local original_port slot port
  read_candidate "$STATE_DIR/pending-candidate"
  original_port=$(cat "$STATE_DIR/original-port")
  bluegreen stage-candidate "$CANDIDATE_IMAGE"
  bluegreen prepare-bluegreen
  IFS='|' read -r slot port < "$APP_DIR/.bluegreen-candidate-slot"
  verify_version "http://127.0.0.1:$port" "$CANDIDATE_VERSION"
  if ! bluegreen canary-green; then
    bluegreen restore-active "$original_port" || fail '发布验证失败，自动回切未成功；已保留全部回滚资产'
    fail '发布切流失败，已回切原健康流量槽位，未清理任何回滚资产'
  fi
  finish_release
}

already_deployed() {
  local commit=$1 port name version_info
  port=$(active_port)
  case "$port" in 18081) name=sub2api-green;; 18082) name=sub2api-canary;; *) return 1;; esac
  [[ "$(docker inspect "$name" --format '{{.Image}}')" == "$(docker inspect sub2api --format '{{.Image}}')" ]] || return 1
  version_info=$(docker exec sub2api /app/sub2api -version 2>&1) || return 1
  [[ "$version_info" == *"commit: $commit,"* ]] || return 1
  # 身份与健康分别判断，短暂探测失败不能触发重复部署并覆盖上一版本。
}

main() {
  local action=${1:-} commit=${2:-}
  umask 077
  mkdir -p "$STATE_DIR"
  # 锁覆盖构建、切流和清理全过程，两个发布入口不能互相删除候选资产。
  exec 9>"$STATE_DIR/release.lock"
  flock -n 9 || fail '已有服务器发布或清理正在执行'
  case "$action" in
    status)
      df -h "$APP_DIR"; free -h
      docker ps --filter 'name=^/sub2api' --format '{{.Names}}|{{.Image}}|{{.Status}}'
      curl -fsS --max-time 15 "$PUBLIC_URL/health"; printf '\n'
      ;;
    build) build_candidate "$commit" ;;
    deploy) deploy "$commit" ;;
    cleanup-build) cleanup_build ;;
    cleanup-release) cleanup_success ;;
    stage-candidate) read_candidate; bluegreen stage-candidate "$CANDIDATE_IMAGE" ;;
    prepare-bluegreen|canary-green|return-blue|rollback|ensure-primary) bluegreen "$action" ;;
    *) fail '用法: sub2api-release {status|build <SHA>|deploy <SHA>|cleanup-build|cleanup-release|rollback}' ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi

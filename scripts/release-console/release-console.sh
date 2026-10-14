#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
ACTION=${1:-status}
COMMIT=${2:-}
case "$ACTION" in
  build|deploy)
    [[ -z "$(git -C "$ROOT_DIR" status --porcelain)" ]] || { printf '工作区存在未提交改动，拒绝发布\n' >&2; exit 1; }
    COMMIT=${COMMIT:-$(git -C "$ROOT_DIR" rev-parse HEAD)}
    ;;
esac
# 本机只传递已确认的提交，不再运行 Docker、编译或导出镜像。
exec python3 "$ROOT_DIR/scripts/release-console/remote-client.py" "$ACTION" "$COMMIT"

"""使用临时目录及命令替身验证发布失败和清理边界，不连接服务器。"""
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ReleaseSafetyTests(unittest.TestCase):
    def test_docker_go_version_satisfies_module_requirement(self):
        project = ROOT.parent.parent
        image_version = re.search(r'^ARG GOLANG_IMAGE=golang:(\d+\.\d+\.\d+)-',
                                  (project / 'Dockerfile').read_text(), re.M)
        module_version = re.search(r'^go (\d+\.\d+\.\d+)$',
                                   (project / 'backend/go.mod').read_text(), re.M)
        self.assertIsNotNone(image_version)
        self.assertIsNotNone(module_version)
        self.assertGreaterEqual(tuple(map(int, image_version[1].split('.'))),
                                tuple(map(int, module_version[1].split('.'))),
                                '发布镜像 Go 版本不能低于 go.mod 要求')

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.tmp = Path(self.directory.name)
        self.env = dict(os.environ, TEST_DIR=str(self.tmp), SERVER=str(ROOT / 'server-release.sh'),
                        REMOTE=str(ROOT / 'remote-bluegreen.sh'), SUB2API_APP_DIR=str(self.tmp))

    def run_shell(self, body, ok=True):
        result = subprocess.run(['bash', '-c', 'set -euo pipefail\n' + body],
                                env=self.env, capture_output=True, text=True)
        if ok:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)
        return result

    def test_invalid_commit_stops_before_remote_or_build(self):
        result = self.run_shell('source "$SERVER"\nbuild_candidate main', ok=False)
        self.assertIn('40 位', result.stderr)

    def test_build_cleanup_scopes_builder_and_preserves_other_files(self):
        state = self.tmp / '.server-release'
        state.mkdir()
        source = state / ('source-' + 'a' * 40)
        source.mkdir()
        (source / 'file').write_text('disposable')
        outside = self.tmp / 'database'
        outside.mkdir()
        (state / ('source-' + 'b' * 40)).symlink_to(outside, target_is_directory=True)
        (state / 'candidate').write_text('keep')
        self.run_shell('''source "$SERVER"
docker() { printf '%s\\n' "$*" >> "$TEST_DIR/commands"; }
cleanup_build
''')
        commands = (self.tmp / 'commands').read_text()
        self.assertIn('buildx prune --builder sub2api-release --all --force', commands)
        self.assertIn('buildx stop sub2api-release', commands)
        self.assertNotIn('system prune', commands)
        self.assertFalse(source.exists())
        self.assertTrue(outside.exists())
        self.assertEqual((state / 'candidate').read_text(), 'keep')

    def deployment(self, failure):
        self.env['FAILURE'] = failure
        return self.run_shell('''source "$SERVER"
mkdir -p "$STATE_DIR"
record() { printf '%s\\n' "$1" >> "$TEST_DIR/events"; }
already_deployed() { return 1; }
active_port() { printf '18082\\n'; }
build_candidate() { record build; [[ "$FAILURE" != build ]]; }
read_candidate() {
 CANDIDATE_IMAGE=test; CANDIDATE_VERSION=1.0.0; CANDIDATE_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
 printf 'candidate\\n' > "$STATE_DIR/candidate"
}
infra_state() { printf 'unchanged\\n'; }
bluegreen() {
 record "$1"
 if [[ "$1" == prepare-bluegreen ]]; then printf 'sub2api-green|18081\\n' > "$APP_DIR/.bluegreen-candidate-slot"; fi
}
verify_version() { record verify-candidate; [[ "$FAILURE" != candidate ]]; }
verify_deployment() { record verify-live; [[ "$FAILURE" != live ]]; }
cleanup_success() { record cleanup; }
deploy aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
''', ok=not failure)

    def test_failed_build_never_touches_backup_or_traffic(self):
        self.deployment('build')
        self.assertEqual((self.tmp / 'events').read_text().splitlines(), ['build'])

    def test_failed_candidate_never_switches_or_cleans(self):
        self.deployment('candidate')
        events = (self.tmp / 'events').read_text()
        self.assertNotIn('canary-green', events)
        self.assertNotIn('cleanup', events)

    def test_failed_live_verification_preserves_backups(self):
        self.deployment('live')
        self.assertNotIn('cleanup', (self.tmp / 'events').read_text())
        self.assertIn('restore-active', (self.tmp / 'events').read_text())
        self.assertFalse((self.tmp / '.server-release/last-success').exists())

    def test_success_cleans_only_after_validation(self):
        self.deployment('')
        events = (self.tmp / 'events').read_text().splitlines()
        self.assertEqual(events[-2:], ['verify-live', 'cleanup'])
        self.assertTrue((self.tmp / '.server-release/last-success').exists())

    def retry_deployment(self, state, failure=''):
        self.env.update(RETRY_STATE=state, FAILURE=failure)
        return self.run_shell('''source "$SERVER"
mkdir -p "$STATE_DIR"
record() { printf '%s\\n' "$1" >> "$TEST_DIR/events"; }
already_deployed() { [[ "$RETRY_STATE" != restored ]]; }
active_port() { printf '18082\\n'; }
build_candidate() { record unexpected-build; return 99; }
read_candidate() {
 IFS='|' read -r CANDIDATE_IMAGE CANDIDATE_VERSION CANDIDATE_COMMIT < "$1"
}
printf 'test|1.0.0|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\n' > "$STATE_DIR/candidate"
cp "$STATE_DIR/candidate" "$STATE_DIR/pending-candidate"
printf 'new-candidate|2.0.0|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\\n' > "$STATE_DIR/candidate"
printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\n' > "$STATE_DIR/pending-commit"
printf '18082\\n' > "$STATE_DIR/original-port"
printf 'unchanged\\n' > "$STATE_DIR/infra-before"
if [[ "$RETRY_STATE" == success ]]; then cp "$STATE_DIR/pending-candidate" "$STATE_DIR/last-success"; fi
infra_state() { printf 'unchanged\\n'; }
verify_deployment() { record verify-live; [[ "$1" == test && "$FAILURE" != live ]]; }
verify_version() { record verify-candidate; }
bluegreen() {
 record "$1"
 if [[ "$1" == prepare-bluegreen ]]; then printf 'sub2api-green|18081\\n' > "$APP_DIR/.bluegreen-candidate-slot"; fi
}
cleanup_success() { record cleanup; [[ "$FAILURE" != cleanup ]]; }
deploy aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
''', ok=not failure)

    def test_retry_pending_release_finishes_validation_and_cleanup(self):
        self.retry_deployment('pending')
        self.assertEqual((self.tmp / 'events').read_text().splitlines(), ['verify-live', 'cleanup'])
        self.assertTrue((self.tmp / '.server-release/last-success').exists())
        self.assertFalse((self.tmp / '.server-release/pending-commit').exists())

    def test_retry_successful_release_retries_cleanup_without_backup(self):
        self.retry_deployment('success')
        self.assertEqual((self.tmp / 'events').read_text().splitlines(), ['verify-live', 'cleanup'])
        self.assertFalse((self.tmp / '.server-release/pending-commit').exists())

    def test_retry_after_automatic_restore_reuses_original_backup(self):
        self.retry_deployment('restored')
        events = (self.tmp / 'events').read_text().splitlines()
        self.assertEqual(events, ['stage-candidate', 'prepare-bluegreen', 'verify-candidate',
                                  'canary-green', 'verify-live', 'cleanup'])
        self.assertEqual((self.tmp / '.server-release/last-success').read_text(),
                         'test|1.0.0|' + 'a' * 40 + '\n')

    def test_new_release_cannot_overwrite_pending_recovery(self):
        self.run_shell('''source "$SERVER"
mkdir -p "$STATE_DIR"
printf 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\\n' > "$STATE_DIR/pending-commit"
build_candidate() { touch "$TEST_DIR/unexpected-build"; }
deploy aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
''', ok=False)
        self.assertFalse((self.tmp / 'unexpected-build').exists())

    def test_historical_success_after_manual_rollback_can_deploy_again(self):
        state = self.tmp / '.server-release'
        state.mkdir()
        (state / 'last-success').write_text('test|1.0.0|' + 'a' * 40 + '\n')
        self.deployment('')
        events = (self.tmp / 'events').read_text().splitlines()
        self.assertIn('backup-release', events)
        self.assertIn('canary-green', events)

    def test_retry_verification_failure_restores_traffic_and_retains_state(self):
        self.retry_deployment('pending', 'live')
        self.assertEqual((self.tmp / 'events').read_text().splitlines(), ['verify-live', 'restore-active'])
        self.assertFalse((self.tmp / '.server-release/last-success').exists())
        self.assertTrue((self.tmp / '.server-release/pending-commit').exists())

    def test_retry_cleanup_failure_is_not_success(self):
        self.retry_deployment('success', 'cleanup')
        self.assertTrue((self.tmp / '.server-release/pending-commit').exists())

    def test_same_commit_unhealthy_never_replaces_previous_version(self):
        self.run_shell('''source "$SERVER"
mkdir -p "$STATE_DIR"
active_port() { printf '18082\\n'; }
docker() {
 if [[ "$1" == exec ]]; then printf 'Sub2API 1.0.0 (commit: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa, date: test)\\n';
 else printf 'sha256:same\\n'; fi
}
curl() { return 1; }
build_candidate() { touch "$TEST_DIR/unexpected-build"; }
deploy aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
''', ok=False)
        self.assertFalse((self.tmp / 'unexpected-build').exists())

    def backups(self, latest_valid=True):
        for date in ('20261009-010101', '20261010-010101'):
            path = self.tmp / 'backups' / ('bluegreen-' + date)
            path.mkdir(parents=True)
            (path / 'valid').touch()
            (path / 'rollback-image.txt').write_text('weishaw/sub2api:rollback-20261010-010101\n')
        if not latest_valid:
            (self.tmp / 'backups/bluegreen-20261010-010101/valid').unlink()
        (self.tmp / 'previous').write_text('18081|sub2api-green|weishaw/sub2api:previous\n')

    def cleanup(self, ok=True):
        return self.run_shell('''source "$REMOTE" library
APP_DIR="$TEST_DIR"; PREVIOUS_ACTIVE_FILE="$TEST_DIR/previous"
is_complete_backup() { [[ -f "$1/valid" ]]; }
active_port() { printf '18082\\n'; }
health() { :; }
curl() { :; }
candidate_image() { printf 'weishaw/sub2api:candidate\\n'; }
docker() {
 case "$1 $2" in
  'image ls') printf 'weishaw/sub2api:%s\\n' current previous rollback-20261010-010101 old referenced candidate ;;
  'image inspect')
    case "$3" in
     *rollback-*) printf 'sha256:previous\\n';;
     *referenced) printf 'sha256:referenced\\n';;
     *) printf 'sha256:old\\n';;
    esac ;;
  'inspect sub2api-canary')
    if [[ "$*" == *Config.Image* ]]; then printf 'weishaw/sub2api:current\\n'; else printf 'sha256:current\\n'; fi ;;
  'inspect sub2api') printf 'sha256:current\\n' ;;
  'inspect sub2api-green') printf 'sha256:previous\\n' ;;
  'inspect referenced-container') printf 'sha256:referenced\\n' ;;
  'ps -aq') printf 'referenced-container\\n' ;;
  'rmi weishaw/sub2api:old') printf '%s\\n' "$2" >> "$TEST_DIR/deleted-images" ;;
  *) printf 'Unexpected Docker command: %s\\n' "$*" >&2; return 99 ;;
 esac
}
cleanup_release
''', ok=ok)

    def test_cleanup_retains_latest_backup_candidate_and_referenced_images(self):
        self.backups()
        self.cleanup()
        self.assertFalse((self.tmp / 'backups/bluegreen-20261009-010101').exists())
        self.assertTrue((self.tmp / 'backups/bluegreen-20261010-010101').exists())
        self.assertEqual((self.tmp / 'deleted-images').read_text(), 'weishaw/sub2api:old\n')

    def test_corrupt_latest_backup_blocks_all_deletion(self):
        self.backups(latest_valid=False)
        self.cleanup(ok=False)
        self.assertEqual(len(list((self.tmp / 'backups').iterdir())), 2)
        self.assertFalse((self.tmp / 'deleted-images').exists())

    def test_connection_probe_failure_never_replaces_api_or_primary(self):
        for operation in ('run_api_slot sub2api-green 18081', 'ensure_primary_slot'):
            with self.subTest(operation=operation):
                self.run_shell('''source "$REMOTE" library
active_port() { printf '18082\\n'; }
health() { :; }
ss() { return 1; }
ensure_runtime_env() { touch "$TEST_DIR/unexpected-replacement"; }
docker() {
 if [[ "$1" == inspect ]]; then return 1; fi
 touch "$TEST_DIR/unexpected-replacement"
}
''' + operation + ' weishaw/sub2api:test\n', ok=False)
                self.assertFalse((self.tmp / 'unexpected-replacement').exists())

    def test_truncated_backup_manifest_is_not_complete(self):
        backup = self.tmp / 'backup'
        backup.mkdir()
        for filename in ('postgres.dump', 'running-image.txt', 'rollback-image.txt'):
            (backup / filename).write_text('test')
        self.run_shell('''source "$REMOTE" library
cd "$TEST_DIR/backup"
shasum -a 256 postgres.dump running-image.txt rollback-image.txt > SHA256SUMS
is_complete_backup "$TEST_DIR/backup"
''', ok=False)


if __name__ == '__main__':
    unittest.main()

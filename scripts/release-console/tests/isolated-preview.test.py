"""隔离验收边界测试；所有 Docker/HTTP 操作均使用替身，不访问服务器。"""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch


SCRIPT = Path(__file__).resolve().parents[1] / 'isolated-preview.py'
sys.dont_write_bytecode = True


class PreviewTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location('isolated_preview', SCRIPT)
        cls.mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.mod)

    def manifest(self):
        return self.mod.manifest('sha256:' + 'a' * 64, 'sha256:' + 'b' * 64,
                                 'sha256:' + 'c' * 64, {
                                     'database': 'd' * 64, 'jwt': 'e' * 64,
                                     'totp': 'f' * 64, 'admin': 'secret-example',
                                 })

    def test_isolated_storage_network_and_loopback_only(self):
        config = self.manifest()
        self.assertEqual(config['name'], 'sub2api-preview')
        self.assertNotIn('external', json.dumps(config))
        app = config['services']['app']
        self.assertEqual(app['ports'], ['127.0.0.1:18090:8080'])
        self.assertEqual(app['environment']['DATABASE_HOST'], 'preview-db')
        self.assertEqual(app['environment']['DATABASE_DBNAME'], 'sub2api_preview')
        self.assertEqual(app['environment']['REDIS_HOST'], 'preview-redis')
        self.assertEqual(app['environment']['DEPLOYMENT_ROLE'], 'api_only')
        self.assertEqual(app['environment']['OPS_ENABLED'], 'false')
        self.assertEqual(app['environment']['OPS_REQUEST_ERRORS_ENABLED'], 'true')
        self.assertEqual(app['environment']['SECURITY_URL_ALLOWLIST_ENABLED'], 'true')
        self.assertEqual(app['environment']['SECURITY_URL_ALLOWLIST_ALLOW_PRIVATE_HOSTS'], 'false')
        self.assertEqual(app['environment']['SECURITY_URL_ALLOWLIST_UPSTREAM_HOSTS'], 'api.openai.com')
        self.assertFalse(config['networks']['database'].get('external', False))
        self.assertTrue(config['networks']['database']['internal'])
        for service in config['services'].values():
            self.assertNotIn('env_file', service)
            self.assertEqual(service['restart'], 'no')
            self.assertLessEqual(service['cpus'], 0.5)
            self.assertEqual(service['mem_limit'], service['memswap_limit'])
            self.assertTrue(all(':' not in volume.split(':')[0] and
                                '/' not in volume.split(':')[0]
                                for volume in service.get('volumes', [])))
        for name in ('preview-db', 'preview-redis'):
            self.assertNotIn('ports', config['services'][name])
            self.assertEqual(config['services'][name]['networks'], ['database'])

    def test_private_file_never_replaces_existing_configuration(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'compose.json'
            self.mod.write_private(path, {'password': 'example'})
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                self.mod.write_private(path, {'password': 'overwrite'})
            self.assertEqual(json.loads(path.read_text())['password'], 'example')

    def test_compose_preserves_dollar_sign_in_credentials(self):
        config = self.manifest()
        config['services']['app']['environment']['ADMIN_PASSWORD'] = 'example${PATH}$value'
        escaped = self.mod.compose_document(config)
        self.assertEqual(escaped['services']['app']['environment']['ADMIN_PASSWORD'],
                         'example$${PATH}$$value')
        self.assertEqual(config['services']['app']['environment']['ADMIN_PASSWORD'],
                         'example${PATH}$value')

    def test_source_mismatch_never_runs_binary_or_starts_services(self):
        with patch.object(self.mod, 'command', return_value='wrong'), \
                patch.object(self.mod, 'docker') as docker:
            with self.assertRaises(RuntimeError):
                self.mod.verify_candidate('a' * 40, 'weishaw/sub2api:test')
            docker.assert_not_called()

    def test_validate_inputs_before_any_external_command(self):
        with patch.object(self.mod, 'command') as command:
            for commit, image in [('main', 'image'), ('a' * 40, 'latest')]:
                with self.assertRaises(RuntimeError):
                    self.mod.verify_candidate(commit, image)
            command.assert_not_called()

    def test_verified_binary_only_runs_with_network_disabled(self):
        commit = 'a' * 40
        image = 'weishaw/sub2api:xnkaixin-0.2.17-server-' + commit[:12]
        calls = []
        def command(*args, **kwargs):
            if 'show' in args:
                return '0.2.17'
            return ''
        def docker(*args, **kwargs):
            calls.append(args)
            if args[:2] == ('image', 'inspect'):
                return commit if 'Labels' in args[3] else 'sha256:' + 'b' * 64
            return 'Sub2API 0.2.17 (commit: ' + commit + ', date: test)'
        with patch.object(self.mod, 'command', side_effect=command), \
                patch.object(self.mod, 'docker', side_effect=docker):
            self.assertEqual(self.mod.verify_candidate(commit, image), 'sha256:' + 'b' * 64)
        probe = calls[-1]
        self.assertIn('--network', probe)
        self.assertEqual(probe[probe.index('--network') + 1], 'none')
        self.assertEqual(probe[-1], '-version')

    def test_image_label_mismatch_never_starts_binary(self):
        commit = 'a' * 40
        def command(*args, **kwargs):
            return '0.2.17' if 'show' in args else ''
        with patch.object(self.mod, 'command', side_effect=command), \
                patch.object(self.mod, 'docker', return_value='wrong') as docker:
            with self.assertRaisesRegex(RuntimeError, '提交不匹配'):
                self.mod.verify_candidate(commit, 'weishaw/sub2api:xnkaixin-0.2.17-server-' + commit[:12])
        self.assertEqual(docker.call_count, 1)

    def test_guard_covers_resource_and_production_health_thresholds(self):
        guard = self.mod.Guard()
        self.assertIsNone(guard.check(0, 3000, True, True))
        self.assertIsNone(guard.check(5, 1400, False, True))
        self.assertIsNone(guard.check(10, 1400, False, True))
        self.assertIn('健康', guard.check(15, 1400, False, True))
        guard = self.mod.Guard()
        self.assertIsNone(guard.check(0, 1400, True, True))
        self.assertIsNone(guard.check(10, 1400, True, True))
        self.assertIn('内存', guard.check(15, 1400, True, True))
        self.assertIn('内存', self.mod.Guard().check(0, 900, True, True))
        self.assertIn('身份', self.mod.Guard().check(0, 5000, True, False))

    def test_stop_uses_frozen_owned_ids_and_never_removes_data(self):
        runtime = self.mod.Runtime(Path('/unused'))
        runtime.owned_ids = ['a' * 64, 'b' * 64]
        with patch.object(self.mod, 'docker', return_value='true|sub2api-preview') as docker:
            runtime.stop()
        calls = [call.args for call in docker.call_args_list]
        self.assertEqual(calls, [('inspect', '-f',
                                 '{{index .Config.Labels "io.sub2api.preview"}}|'
                                 '{{index .Config.Labels "com.docker.compose.project"}}', cid)
                                for cid in runtime.owned_ids] +
                               [('stop', '--time', '10', *runtime.owned_ids)])

    def test_unowned_container_is_never_stopped(self):
        runtime = self.mod.Runtime(Path('/unused'))
        runtime.owned_ids = ['a' * 64]
        with patch.object(self.mod, 'docker', return_value='false') as docker:
            with self.assertRaises(RuntimeError):
                runtime.stop()
        self.assertNotIn('stop', [call.args[0] for call in docker.call_args_list])

    def test_resources_with_foreign_owner_cannot_be_reused(self):
        def docker(*args, **kwargs):
            if args[:2] == ('volume', 'ls'):
                return 'sub2api-preview_preview-postgres'
            if args[:2] == ('volume', 'inspect'):
                return 'production'
            self.fail('发现外部卷后不应继续操作')
        with patch.object(self.mod, 'docker', side_effect=docker):
            with self.assertRaisesRegex(RuntimeError, '其他项目'):
                self.mod.verify_resources()

    def test_owned_resources_are_reusable_without_mutation(self):
        calls = []
        def docker(*args, **kwargs):
            calls.append(args)
            if args[1] == 'inspect':
                return 'sub2api-preview'
            if args[0] == 'volume':
                return 'sub2api-preview_preview-data\nsub2api-preview_preview-postgres'
            return 'sub2api-preview_database\nsub2api-preview_outbound'
        with patch.object(self.mod, 'docker', side_effect=docker):
            self.mod.verify_resources()
        self.assertTrue(all(args[1] in ('ls', 'inspect') for args in calls))

    def test_failed_start_stops_only_labelled_containers(self):
        runtime = self.mod.Runtime(Path('/unused'))
        process = Mock()
        process.poll.return_value = 1
        with patch.object(self.mod.subprocess, 'Popen', return_value=process), \
                patch.object(runtime, 'supervise', side_effect=RuntimeError('模拟启动失败')), \
                patch.object(runtime, 'capture_owned') as capture, \
                patch.object(runtime, 'stop') as stop:
            with self.assertRaisesRegex(RuntimeError, '模拟启动失败'):
                runtime.run('baseline')
            capture.assert_called_once()
            stop.assert_called_once()

    def test_inflight_start_is_terminated_before_stopping_containers(self):
        runtime = self.mod.Runtime(Path('/unused'))
        events = []
        process = Mock()
        process.poll.return_value = None
        process.terminate.side_effect = lambda: events.append('terminate')
        process.wait.side_effect = lambda **kwargs: events.append('wait')
        with patch.object(self.mod.subprocess, 'Popen', return_value=process), \
                patch.object(runtime, 'supervise', side_effect=KeyboardInterrupt), \
                patch.object(runtime, 'capture_owned', side_effect=lambda: events.append('capture')), \
                patch.object(runtime, 'stop', side_effect=lambda: events.append('stop')):
            with self.assertRaises(KeyboardInterrupt):
                runtime.run('baseline')
        self.assertEqual(events, ['terminate', 'wait', 'capture', 'stop'])


if __name__ == '__main__':
    unittest.main()

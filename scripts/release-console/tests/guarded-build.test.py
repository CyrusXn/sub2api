"""构建守护阈值及取消边界；不连接 Docker 或服务器。"""
import importlib.util
from contextlib import contextmanager, ExitStack
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('guarded_build', Path(__file__).resolve().parents[1] / 'guarded-build.py')
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class BuildGuardTests(unittest.TestCase):
    @contextmanager
    def build_environment(self, running=False):
        clock = SimpleNamespace(now=0)
        process = Mock(pid=12345, returncode=0)
        process.poll.side_effect = [None, None, 0] if running else None
        if not running:
            process.poll.return_value = 0
        counter = Mock()
        counter.sample.return_value = 0
        with ExitStack() as stack:
            patches = (
                (guard.os, 'geteuid', {'return_value': 0}),
                (guard, 'ErrorRate', {'return_value': counter}),
                (guard, 'builder_sample', {'return_value': None}),
                (guard.preview, 'memory_mb', {'return_value': 5000}),
                (guard.preview, 'docker', {'return_value': 'production|true'}),
                (guard, 'probe_health', {'return_value': True}),
                (guard, 'owned_buildx', {'return_value': running}),
                (guard.subprocess, 'Popen', {'return_value': process}),
                (guard.time, 'monotonic', {'side_effect': lambda: clock.now}),
                (guard.time, 'sleep', {'side_effect': lambda seconds: setattr(clock, 'now', clock.now + seconds)}),
            )
            for owner, name, options in patches:
                stack.enter_context(patch.object(owner, name, **options))
            printed = stack.enter_context(patch('builtins.print'))
            cancelled = stack.enter_context(patch.object(guard, 'cancel'))
            yield SimpleNamespace(clock=clock, process=process, counter=counter,
                                  printed=printed, cancelled=cancelled)

    def test_active_build_requires_first_cgroup_sample(self):
        with self.build_environment(running=True) as environment:
            with self.assertRaisesRegex(RuntimeError, '采样'):
                guard.build_with_lock('a' * 40, Mock())
            environment.cancelled.assert_called_once_with(environment.process, None)

    def test_baseline_records_sixty_seconds_of_complete_samples(self):
        with self.build_environment() as environment:
            guard.build_with_lock('a' * 40, Mock())
            records = [json.loads(call.args[0]) for call in environment.printed.call_args_list
                       if isinstance(call.args[0], str) and call.args[0].startswith('{')]
            baseline = [item for item in records if item.get('phase') == 'baseline']
            self.assertGreaterEqual(len(baseline), 13)
            self.assertEqual(environment.clock.now, 60)
            for record in baseline:
                for field in ('time', 'available_mib', 'memory', 'event_delta',
                              'production_healthy', 'error_rate'):
                    self.assertIn(field, record)
            environment.counter.stream.close.assert_called_once()

    def test_docker_inspection_failure_is_not_missing_builder(self):
        result = subprocess.CompletedProcess([], 1, '', 'Cannot connect to the Docker daemon')
        with patch.object(guard.subprocess, 'run', return_value=result):
            with self.assertRaisesRegex(RuntimeError, '探测'):
                guard.builder_sample()

    def test_confirmed_absent_builder_can_wait_for_bootstrap(self):
        result = subprocess.CompletedProcess([], 1, '', 'Error: No such object: ' + guard.BUILDER)
        with patch.object(guard.subprocess, 'run', return_value=result):
            self.assertIsNone(guard.builder_sample())

    def test_probe_budget_returns_without_waiting_for_stalled_probe(self):
        self.assertTrue(callable(getattr(guard, 'collect_sample', None)), '采样必须共享总预算')
        release = threading.Event()
        try:
            with patch.object(guard, 'SAMPLE_BUDGET', .05), \
                    patch.object(guard, 'builder_sample', side_effect=lambda: release.wait(2)), \
                    patch.object(guard, 'production_state', return_value='production|true'), \
                    patch.object(guard, 'probe_health', return_value=True):
                started = time.monotonic()
                with self.assertRaisesRegex(RuntimeError, '预算'):
                    guard.collect_sample(Mock())
                self.assertLess(time.monotonic() - started, .5)
        finally:
            release.set()

    def test_production_state_probe_has_short_timeout(self):
        self.assertTrue(callable(getattr(guard, 'production_state', None)), '状态探针需要独立的短超时')
        with patch.object(guard.preview, 'docker', return_value='production|true') as docker:
            self.assertEqual(guard.production_state(), 'production|true')
        self.assertGreater(docker.call_args.kwargs['timeout'], 0)
        self.assertLessEqual(docker.call_args.kwargs['timeout'], 3)

    def test_health_probe_requires_http_200_and_healthy_json(self):
        for output, expected in (('{\"status\":\"ok\"}\n200', True),
                                 ('{\"status\":\"ok\"}\n302', False),
                                 ('{\"status\":\"bad\"}\n200', False),
                                 ('{\"status\":\"ok\"}', False),
                                 ('[]\n200', False)):
            with self.subTest(output=output), patch.object(guard.subprocess, 'run',
                    return_value=subprocess.CompletedProcess([], 0, output, '')) as run:
                self.assertEqual(guard.probe_health('https://example.invalid/health'), expected)
                self.assertLessEqual(run.call_args.kwargs['timeout'], 3)

    def test_health_timeout_is_failed_sample(self):
        with patch.object(guard.subprocess, 'run', side_effect=subprocess.TimeoutExpired('curl', 3)):
            self.assertFalse(guard.probe_health('https://example.invalid/health'))

    def test_build_probe_timeout_cancels_child_and_closes_counter(self):
        collect = guard.collect_sample
        def timed_out(counter, group=None):
            if group is not None:
                raise RuntimeError('守护采样超出时间预算')
            return collect(counter)
        with self.build_environment(running=True) as environment, \
                patch.object(guard, 'collect_sample', side_effect=timed_out):
            with self.assertRaisesRegex(RuntimeError, '预算'):
                guard.build_with_lock('a' * 40, Mock())
            environment.cancelled.assert_called_once_with(environment.process, None)
            environment.counter.stream.close.assert_called_once()

    def test_unknown_build_phase_cancels_child(self):
        with self.build_environment(running=True) as environment, \
                patch.object(guard, 'owned_buildx', side_effect=OSError('phase unavailable')):
            with self.assertRaises(OSError):
                guard.build_with_lock('a' * 40, Mock())
            environment.cancelled.assert_called_once_with(environment.process, None)

    def test_empty_process_group_is_unknown_phase(self):
        with patch.object(guard, 'group_processes', return_value=[]):
            with self.assertRaisesRegex(RuntimeError, '阶段'):
                guard.owned_buildx(12345)

    def test_failed_baseline_never_starts_build_and_closes_counter(self):
        with self.build_environment() as environment, \
                patch.object(guard.preview, 'memory_mb', return_value=4000), \
                patch.object(guard.subprocess, 'Popen') as start:
            with self.assertRaisesRegex(RuntimeError, '60 秒'):
                guard.build_with_lock('a' * 40, Mock())
            start.assert_not_called()
            environment.cancelled.assert_not_called()
            environment.counter.stream.close.assert_called_once()

    def test_container_cgroup_includes_inheriting_init_children(self):
        container_id = 'a' * 64
        with tempfile.TemporaryDirectory() as directory:
            hierarchy = Path(directory).resolve()
            for relative in ('/system.slice/docker-' + container_id + '.scope/init',
                             '/docker/' + container_id + '/init',
                             '/docker/' + container_id):
                expected = hierarchy / relative.lstrip('/')
                if expected.name == 'init':
                    expected = expected.parent
                self.assertEqual(guard.container_cgroup_path(relative, container_id, hierarchy), expected)

    def test_container_cgroup_rejects_unowned_or_escaping_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            for relative in ('/system.slice/docker-other.scope/init', '/../../outside'):
                with self.assertRaises(RuntimeError):
                    guard.container_cgroup_path(relative, 'a' * 64, Path(directory))

    def test_sampling_loss_or_builder_change_fails_closed(self):
        guard.require_sample(None, None)
        guard.require_sample({'id': 'same'}, {'id': 'same'})
        for sample in (None, {'id': 'different'}):
            with self.assertRaises(RuntimeError):
                guard.require_sample(sample, {'id': 'same'})

    def check(self, limits, now=0, available=5000, current=10, maximum=100, events=None, healthy=True, rate=0):
        return limits.check(now, available, current, maximum, events or {}, healthy, rate)

    def test_host_memory_duration_and_immediate_floor(self):
        limits = guard.Limits({}, 0)
        self.assertIsNone(self.check(limits, available=1400))
        self.assertIsNone(self.check(limits, now=10, available=1400))
        self.assertIn('内存', self.check(limits, now=15, available=1400))
        self.assertIn('内存', self.check(guard.Limits({}, 0), available=900))

    def test_memory_recovery_resets_duration(self):
        limits = guard.Limits({}, 0)
        self.check(limits, available=1400)
        self.check(limits, now=10)
        self.assertIsNone(self.check(limits, now=20, available=1400))

    def test_historical_oom_is_ignored_but_new_oom_is_fatal(self):
        limits = guard.Limits({'oom': 2, 'oom_kill': 1}, 0)
        self.assertIsNone(self.check(limits, events={'oom': 2, 'oom_kill': 1}))
        self.assertIn('OOM', self.check(limits, events={'oom': 3, 'oom_kill': 1}))
        self.assertIn('OOM', self.check(guard.Limits({}, 0), events={'oom_kill': 1}))

    def test_high_usage_requires_sustained_limit_events(self):
        limits = guard.Limits({}, 0)
        for tick in range(0, 30, 5):
            self.assertIsNone(self.check(limits, now=tick, current=96, events={'max': tick + 1}))
        self.assertIn('cgroup', self.check(limits, now=30, current=96, events={'max': 31}))
        limits = guard.Limits({}, 0)
        for tick in range(0, 45, 5):
            self.assertIsNone(self.check(limits, now=tick, current=96, events={'max': 1}))

    def test_health_and_error_rate_need_three_consecutive_failures(self):
        limits = guard.Limits({}, .1)
        self.assertIsNone(self.check(limits, healthy=False))
        self.assertIsNone(self.check(limits, healthy=False))
        self.assertIn('健康', self.check(limits, healthy=False))
        limits = guard.Limits({}, .1)
        self.check(limits, rate=.2)
        self.check(limits, rate=.05)
        self.assertIsNone(self.check(limits, rate=.2))
        self.assertIsNone(self.check(limits, rate=.2))
        self.assertIn('5xx', self.check(limits, rate=.2))

    def test_error_counter_reads_only_new_lines_and_detects_rotation(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'access.log'
            path.write_bytes(b'old \"GET / HTTP/1.1\" 500 0\n')
            counter = guard.ErrorRate(path)
            self.addCleanup(counter.stream.close)
            with path.open('ab') as stream:
                stream.write(b'ip \"GET / HTTP/1.1\" 200 0\nip \"GET / HTTP/1.1\" 502 0\n')
            self.assertEqual(counter.sample(), .5)
            self.assertEqual(counter.sample(), 0)
            path.rename(path.with_suffix('.old'))
            path.touch()
            with self.assertRaisesRegex(RuntimeError, '轮转'):
                counter.sample()

    def test_error_counter_rejects_same_inode_truncation(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'access.log'
            path.write_bytes(b'old \"GET / HTTP/1.1\" 200 0\n' * 20)
            inode = path.stat().st_ino
            counter = guard.ErrorRate(path)
            self.addCleanup(counter.stream.close)
            path.write_bytes(b'new \"GET / HTTP/1.1\" 502 0\n')
            self.assertEqual(path.stat().st_ino, inode)
            with self.assertRaisesRegex(RuntimeError, '截断|轮转'):
                counter.sample()

    def test_error_counter_rejects_truncate_and_regrow(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'access.log'
            path.write_bytes(b'old \"GET / HTTP/1.1\" 200 0\n')
            counter = guard.ErrorRate(path)
            self.addCleanup(counter.stream.close)
            path.write_bytes(b'new \"GET / HTTP/1.1\" 502 0\n' * 10)
            with self.assertRaisesRegex(RuntimeError, '截断|轮转'):
                counter.sample()

    def test_error_counter_keeps_incomplete_lines_until_next_sample(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'access.log'
            path.touch()
            counter = guard.ErrorRate(path)
            self.addCleanup(counter.stream.close)
            with path.open('ab') as stream:
                stream.write(b'ip \"GET / HTTP/1.1\" ')
            self.assertEqual(counter.sample(), 0)
            with path.open('ab') as stream:
                stream.write(b'502 0\n')
            self.assertEqual(counter.sample(), 1)

    def test_unknown_phase_still_cancels_only_own_build_group(self):
        process = Mock(pid=12345)
        process.poll.return_value = None
        with patch.object(guard, 'owned_buildx', side_effect=OSError('probe failed')), \
                patch.object(guard, 'group_processes', return_value=[]), \
                patch.object(guard.os, 'killpg') as kill, \
                patch.object(guard.preview, 'docker') as docker:
            try:
                guard.cancel(process, {'id': 'a' * 64})
            except OSError:
                pass
        kill.assert_called_once_with(12345, signal.SIGTERM)
        docker.assert_not_called()

    def test_cancel_only_own_process_group_and_verified_builder_id(self):
        process = Mock(pid=12345)
        process.poll.return_value = None
        builder = {'id': 'a' * 64}
        with patch.object(guard, 'owned_buildx', return_value=True), \
                patch.object(guard, 'group_processes', return_value=[]), \
                patch.object(guard.os, 'killpg') as kill, \
                patch.object(guard.preview, 'docker', side_effect=['a' * 64, '']) as docker:
            guard.cancel(process, builder)
        kill.assert_called_once_with(12345, signal.SIGTERM)
        self.assertEqual(docker.call_args_list[-1].args, ('stop', '--time', '10', 'a' * 64))

    def test_cancel_never_stops_replaced_or_unowned_builder(self):
        for active, sampled in ((False, {'id': 'a'}), (True, {'id': 'different'})):
            process = Mock(pid=12345)
            process.poll.return_value = None
            with patch.object(guard, 'owned_buildx', return_value=active), \
                    patch.object(guard, 'group_processes', return_value=[]), \
                    patch.object(guard.os, 'killpg'), \
                    patch.object(guard.preview, 'docker', return_value=sampled['id']) as docker:
                guard.cancel(process, {'id': 'a'})
                self.assertNotIn('stop', [call.args[0] for call in docker.call_args_list])


if __name__ == '__main__':
    unittest.main()

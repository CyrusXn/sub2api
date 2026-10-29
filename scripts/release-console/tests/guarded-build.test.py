"""构建守护阈值及取消边界；不连接 Docker 或服务器。"""
import importlib.util
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('guarded_build', Path(__file__).resolve().parents[1] / 'guarded-build.py')
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class BuildGuardTests(unittest.TestCase):
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

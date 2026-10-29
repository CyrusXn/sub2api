#!/usr/bin/env python3
"""仅构建候选的资源守护入口；不备份、不预热、不切流。"""
import concurrent.futures
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

spec = importlib.util.spec_from_file_location('preview_guard', Path(__file__).with_name('isolated-preview.py'))
preview = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preview)
BUILDER = 'buildx_buildkit_sub2api-release0'
ACCESS_LOG = Path('/home/web/log/nginx/access.log')


class ErrorRate:
    def __init__(self, path):
        self.path = path
        self.stream = path.open('rb')
        self.inode = path.stat().st_ino
        self.stream.seek(0, os.SEEK_END)

    def sample(self):
        if self.path.stat().st_ino != self.inode:
            raise RuntimeError('访问日志轮转，无法持续比较 5xx 基线')
        total = errors = 0
        for line in self.stream:
            # 只提取状态码计数，不输出请求地址、用户标识或原始日志。
            match = re.search(rb'"\s+(\d{3})\s+', line)
            if match:
                total += 1
                errors += int(match[1]) >= 500
        return errors / total if total else 0


class Limits:
    def __init__(self, events, error_rate):
        self.events = events
        self.error_rate = error_rate
        self.low_since = self.high_since = self.warning_since = None
        self.high_max = None
        self.health_failures = self.error_failures = 0

    def check(self, now, available, current, maximum, events, healthy, error_rate):
        self.health_failures = 0 if healthy else self.health_failures + 1
        self.error_failures = self.error_failures + 1 if error_rate > self.error_rate else 0
        if self.health_failures >= 3 or self.error_failures >= 3:
            return '生产健康或 5xx 连续三次超过基线'
        if any(events.get(key, 0) > self.events.get(key, 0) for key in ('oom', 'oom_kill')):
            return '本次构建出现 OOM 事件'
        if available < 1536:
            if self.low_since is None:
                self.low_since = now
            if available < 1024 or now - self.low_since >= 15:
                return '宿主机可用内存不足'
        else:
            self.low_since = None
        ratio = current / maximum if maximum else 0
        if ratio >= .95:
            count = events.get('max', 0)
            if self.high_since is None or count <= self.high_max:
                self.high_since = now
            self.high_max = count
            if now - self.high_since >= 30:
                return '构建器高内存持续触及 cgroup 上限'
        else:
            self.high_since = self.high_max = None
        if available < 2048 or ratio >= .90:
            if self.warning_since is None:
                self.warning_since = now
            if now - self.warning_since >= 15:
                print('预警：内存压力持续 15 秒，禁止额外重型工作。', flush=True)
        else:
            self.warning_since = None
        return None


def container_cgroup_path(relative, container_id, hierarchy=Path('/sys/fs/cgroup')):
    hierarchy = hierarchy.resolve()
    leaf = (hierarchy / relative.lstrip('/')).resolve()
    if not leaf.is_relative_to(hierarchy):
        raise RuntimeError('构建器 cgroup 路径异常')
    # Docker 的 init 子组继承父组限额；采样必须覆盖整个容器及其所有子组。
    names = {container_id, 'docker-' + container_id + '.scope'}
    for parent in (leaf, *leaf.parents):
        if parent.is_relative_to(hierarchy) and parent.name in names:
            return parent
    raise RuntimeError('未找到与构建器完整容器 ID 对应的 cgroup')


def builder_sample():
    result = subprocess.run(['docker', 'inspect', BUILDER], capture_output=True, text=True, timeout=3)
    if result.returncode:
        return None
    container = json.loads(result.stdout)[0]
    if not container['State']['Running']:
        return None
    pid = container['State']['Pid']
    config = container['HostConfig']
    if not (0 < config['Memory'] <= 3 * 1024**3 and config['MemorySwap'] == config['Memory']
            and config['CpuPeriod'] > 0 and 0 < config['CpuQuota'] / config['CpuPeriod'] <= 2):
        raise RuntimeError('专用构建器资源限制不匹配')
    relative = Path(f'/proc/{pid}/cgroup').read_text().split('0::', 1)[1].strip()
    root = container_cgroup_path(relative, container['Id'])
    maximum = (root / 'memory.max').read_text().strip()
    if not maximum.isdecimal() or int(maximum) != config['Memory']:
        raise RuntimeError('构建器 cgroup 实际内存限额与 Docker 配置不一致')
    return {
        'id': container['Id'], 'current': int((root / 'memory.current').read_text()),
        'maximum': int(maximum),
        'events': {key: int(value) for key, value in
                   (line.split() for line in (root / 'memory.events').read_text().splitlines())},
    }


def require_sample(sample, previous):
    if previous and sample is None:
        raise RuntimeError('构建器采样丢失，取消构建以保留内存保护')
    if previous and sample['id'] != previous['id']:
        raise RuntimeError('构建期间专用构建器身份改变')


def group_processes(group):
    found = []
    for entry in Path('/proc').iterdir():
        if not entry.name.isdecimal():
            continue
        try:
            if os.getpgid(int(entry.name)) != group:
                continue
            args = (entry / 'cmdline').read_bytes().split(b'\0')
            if any(args):
                found.append((int(entry.name), args))
        except (ProcessLookupError, FileNotFoundError, PermissionError):
            continue
    return found


def owned_buildx(group):
    return any(b'build' in args and b'--builder' in args and b'sub2api-release' in args
               for _, args in group_processes(group))


def cancel(process, builder):
    # 唯一子进程固定为 build，不可能继续进入预热或切流；只取消本进程组。
    active = owned_buildx(process.pid)
    if process.poll() is None:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
    try:
        if active and builder:
            # 采样可能正因 cgroup 消失而失败；停止前仅复核 Docker 身份，不再依赖 cgroup。
            current_id = preview.docker('inspect', '-f', '{{.Id}}', BUILDER)
            if current_id == builder['id']:
                preview.docker('stop', '--time', '10', builder['id'])
    finally:
        if group_processes(process.pid):
            os.killpg(process.pid, signal.SIGKILL)
            deadline = time.monotonic() + 5
            while group_processes(process.pid):
                if time.monotonic() >= deadline:
                    raise RuntimeError('本次构建子进程仍未退出；保留现场，禁止重试')
                time.sleep(.1)


def production_health():
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        return all(pool.map(preview.healthy, (preview.PUBLIC_HEALTH, 'http://127.0.0.1:8080/health')))


def build_with_lock(commit, lock):
    if not re.fullmatch('[a-f0-9]{40}', commit) or os.geteuid() != 0:
        raise RuntimeError('仅允许 root 在服务器构建完整审核 SHA')
    baseline = preview.production_state()
    if '|false' in baseline or not production_health():
        raise RuntimeError('生产健康基线不满足')
    counter = ErrorRate(ACCESS_LOG)
    # 构建前持续 60 秒记录真实业务基线，并确认恢复资源门槛。
    rates = []
    for _ in range(12):
        if preview.memory_mb() < 4608 or not production_health():
            raise RuntimeError('可用内存和健康未持续满足 60 秒门槛')
        time.sleep(5)
        rates.append(counter.sample())
    before = builder_sample()
    limits = Limits(before['events'] if before else {}, max(rates))
    if preview.production_state() != baseline:
        raise RuntimeError('基线采样期间生产容器状态改变')
    environment = dict(os.environ, SUB2API_RELEASE_LOCK_FD=str(lock.fileno()))
    process = subprocess.Popen(['/usr/local/bin/sub2api-release', 'build', commit], start_new_session=True,
                               pass_fds=(lock.fileno(),), env=environment)
    builder = before
    try:
        while process.poll() is None:
            tick = time.monotonic()
            sample = builder_sample()
            require_sample(sample, builder)
            if sample:
                if builder is None:
                    # 新 cgroup 从零计数；首次采样出现 OOM 也必须识别。
                    limits.events = dict(before['events']) if before and before['id'] == sample['id'] else {}
                builder = sample
            available = preview.memory_mb()
            rate = counter.sample()
            health_ok = production_health()
            reason = limits.check(tick, available, sample['current'] if sample else 0,
                                  sample['maximum'] if sample else 0,
                                  sample['events'] if sample else limits.events, health_ok, rate)
            phase = 'buildx' if owned_buildx(process.pid) else 'build-prepare-or-verify'
            print(json.dumps({'time': time.strftime('%FT%T%z'), 'phase': phase,
                              'available_mib': available, 'memory': sample,
                              'event_delta': {key: value - limits.events.get(key, 0) for key, value in
                                              (sample['events'] if sample else {}).items()},
                              'production_healthy': health_ok, 'error_rate': rate}), flush=True)
            if reason or preview.production_state() != baseline:
                raise RuntimeError(reason or '生产容器状态改变')
            time.sleep(max(0, 5 - (time.monotonic() - tick)))
        final = builder_sample()
        require_sample(final, builder)
        if final and any(final['events'].get(key, 0) > limits.events.get(key, 0) for key in ('oom', 'oom_kill')):
            raise RuntimeError('最终采样发现本次构建 OOM，保留诊断')
        if process.returncode:
            raise RuntimeError('候选构建失败，保留构建日志和状态')
    finally:
        if process.poll() is None:
            cancel(process, builder)
        counter.stream.close()
    if preview.production_state() != baseline or not production_health():
        raise RuntimeError('构建后生产基线验证失败')
    print('候选构建完成且生产基线未变；未启动候选、未切流。', flush=True)


def main(commit):
    with (preview.STATE / 'release.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError('已有发布、清理或隔离验收任务运行') from None
        build_with_lock(commit, lock)


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))
    try:
        if len(sys.argv) != 2:
            raise RuntimeError('用法：guarded-build.py <完整SHA>')
        main(sys.argv[1])
    except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
        print(str(error) if isinstance(error, RuntimeError) else '构建守护失败；请检查服务器受保护诊断。', file=sys.stderr)
        sys.exit(1)

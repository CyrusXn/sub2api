#!/usr/bin/env python3
"""服务器隔离验收运行器；不构建、不连接生产数据库、不切换正式流量。"""
import argparse
import fcntl
import getpass
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import signal
import subprocess
import sys
import time
from urllib.request import urlopen


ROOT = Path('/home/docker/sub2api-preview')
STATE = Path('/home/docker/sub2api/.server-release')
PUBLIC_HEALTH = 'https://api.xnkaixin.eu.cc/health'
PROJECT = 'sub2api-preview'
PRODUCTION = ('sub2api', 'sub2api-green', 'sub2api-canary',
              'nginx', 'sub2api-postgres', 'sub2api-redis')
LABEL = 'io.sub2api.preview'
SAMPLE_SECONDS = 5


def command(*args, timeout=20):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    if result.returncode:
        # Docker/Compose 的诊断可能含连接配置；只输出失败操作，不转发原文。
        raise RuntimeError('外部操作失败：' + ' '.join(args[:2]))
    return result.stdout.strip()


def docker(*args, timeout=20):
    return command('docker', *args, timeout=timeout)


def manifest(image, postgres, redis, credentials, upstream_hosts=None):
    common = {
        'restart': 'no', 'cpus': 0.5, 'pids_limit': 256,
        'security_opt': ['no-new-privileges:true'],
        'labels': {LABEL: 'true'},
        'logging': {'driver': 'json-file', 'options': {'max-size': '5m', 'max-file': '2'}},
    }
    return {
        'name': PROJECT,
        'services': {
            'app': {
                **common, 'image': image, 'mem_limit': '768m', 'memswap_limit': '768m',
                'ports': ['127.0.0.1:18090:8080'],
                'volumes': ['preview-data:/app/data'],
                'networks': ['database', 'outbound'],
                'depends_on': {
                    'preview-db': {'condition': 'service_healthy'},
                    'preview-redis': {'condition': 'service_healthy'},
                },
                'environment': {
                    'AUTO_SETUP': 'true', 'SERVER_HOST': '0.0.0.0', 'SERVER_PORT': '8080',
                    'SERVER_MODE': 'release', 'DEPLOYMENT_ROLE': 'api_only',
                    'OPS_ENABLED': 'false', 'OPS_REQUEST_ERRORS_ENABLED': 'true', 'TZ': 'Asia/Shanghai',
                    'GOMAXPROCS': '1', 'GOMEMLIMIT': '512MiB',
                    'DATABASE_HOST': 'preview-db', 'DATABASE_PORT': '5432',
                    'DATABASE_USER': 'sub2api_preview', 'DATABASE_DBNAME': 'sub2api_preview',
                    'DATABASE_PASSWORD': credentials['database'], 'DATABASE_SSLMODE': 'disable',
                    'DATABASE_MAX_OPEN_CONNS': '10', 'DATABASE_MAX_IDLE_CONNS': '2',
                    'REDIS_HOST': 'preview-redis', 'REDIS_PORT': '6379', 'REDIS_DB': '0',
                    'REDIS_POOL_SIZE': '16', 'REDIS_MIN_IDLE_CONNS': '1',
                    'ADMIN_EMAIL': 'admin@example.com', 'ADMIN_PASSWORD': credentials['admin'],
                    'JWT_SECRET': credentials['jwt'], 'TOTP_ENCRYPTION_KEY': credentials['totp'],
                    # 验收只允许公开上游地址，避免误配到宿主机或私网数据库。
                    'SECURITY_URL_ALLOWLIST_ENABLED': 'true',
                    'SECURITY_URL_ALLOWLIST_ALLOW_PRIVATE_HOSTS': 'false',
                    'SECURITY_URL_ALLOWLIST_UPSTREAM_HOSTS': ','.join(upstream_hosts or ['api.openai.com']),
                },
                'healthcheck': {
                    'test': ['CMD', 'wget', '-q', '--spider', 'http://127.0.0.1:8080/health'],
                    'interval': '10s', 'timeout': '3s', 'retries': 6, 'start_period': '180s',
                },
            },
            'preview-db': {
                **common, 'image': postgres, 'mem_limit': '512m', 'memswap_limit': '512m',
                'networks': ['database'], 'volumes': ['preview-postgres:/var/lib/postgresql'],
                'environment': {'POSTGRES_USER': 'sub2api_preview',
                                'POSTGRES_DB': 'sub2api_preview',
                                'POSTGRES_PASSWORD': credentials['database']},
                'command': ['postgres', '-c', 'shared_buffers=32MB', '-c', 'max_connections=30'],
                'healthcheck': {
                    'test': ['CMD', 'pg_isready', '-U', 'sub2api_preview', '-d', 'sub2api_preview'],
                    'interval': '5s', 'timeout': '3s', 'retries': 12,
                },
            },
            'preview-redis': {
                **common, 'image': redis, 'mem_limit': '128m', 'memswap_limit': '128m',
                'networks': ['database'],
                'command': ['redis-server', '--maxmemory', '64mb', '--maxmemory-policy',
                            'noeviction', '--save', '', '--appendonly', 'no'],
                'healthcheck': {'test': ['CMD', 'redis-cli', 'ping'],
                                'interval': '5s', 'timeout': '3s', 'retries': 12},
            },
        },
        'networks': {'database': {'internal': True}, 'outbound': {}},
        'volumes': {'preview-data': {}, 'preview-postgres': {}},
    }


def write_private(path, value):
    with open(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write('\n')


def compose_document(value):
    # Compose 会解释美元符号；密码等原始值必须逐字传递给容器。
    if isinstance(value, str):
        return value.replace('$', '$$')
    if isinstance(value, list):
        return [compose_document(item) for item in value]
    if isinstance(value, dict):
        return {key: compose_document(item) for key, item in value.items()}
    return value


def verify_candidate(commit, image):
    if not re.fullmatch('[a-f0-9]{40}', commit):
        raise RuntimeError('需要已审核、已推送的完整 40 位 SHA')
    if not re.fullmatch(r'weishaw/sub2api:xnkaixin-\d+\.\d+\.\d+-server-' + commit[:12], image):
        raise RuntimeError('只允许服务器发布入口生成的固定提交镜像')
    repo = '--git-dir=' + str(STATE / 'repo.git')
    command('git', repo, 'merge-base', '--is-ancestor', commit, 'refs/heads/xnkaixin')
    version = command('git', repo, 'show', commit + ':backend/cmd/server/VERSION')
    if not re.fullmatch(r'\d+\.\d+\.\d+', version):
        raise RuntimeError('候选版本必须为纯语义版本')
    if docker('image', 'inspect', '-f', '{{index .Config.Labels "io.sub2api.release.commit"}}', image) != commit:
        raise RuntimeError('候选镜像提交不匹配')
    image_id = docker('image', 'inspect', '-f', '{{.Id}}', image)
    output = docker('run', '--rm', '--network', 'none', '--memory', '128m', '--cpus', '0.5',
                    '--entrypoint', '/app/sub2api', image_id, '-version')
    if f'Sub2API {version} ' not in output or f'commit: {commit},' not in output:
        raise RuntimeError('候选二进制与指定提交不匹配')
    return image_id


def production_state():
    return docker('inspect', '-f', '{{.Name}}|{{.Id}}|{{.State.StartedAt}}|'
                  '{{.RestartCount}}|{{.State.Running}}', *PRODUCTION)


def healthy(url):
    try:
        with urlopen(url, timeout=3) as response:
            return response.status == 200 and json.load(response).get('status') == 'ok'
    except Exception:
        return False


def memory_mb():
    data = Path('/proc/meminfo').read_text()
    return int(re.search(r'^MemAvailable:\s+(\d+)', data, re.M)[1]) // 1024


def verify_resources():
    # Compose 对同名的历史卷/网络可能只警告后复用；先检查所有权，避免误接其他项目。
    for kind, expected in (
        ('volume', ('preview-data', 'preview-postgres')),
        ('network', ('database', 'outbound')),
    ):
        existing = set(docker(kind, 'ls', '--format', '{{.Name}}').splitlines())
        for suffix in expected:
            name = PROJECT + '_' + suffix
            if name not in existing:
                continue
            owner = docker(kind, 'inspect', '-f',
                           '{{index .Labels "com.docker.compose.project"}}', name)
            if owner != PROJECT:
                raise RuntimeError('同名隔离资源属于其他项目，禁止复用')


class Guard:
    def __init__(self):
        self.low_since = None
        self.health_failures = 0

    def check(self, now, available_mb, health_ok, identity_ok):
        if not identity_ok:
            return '生产容器身份或运行状态变化'
        self.health_failures = 0 if health_ok else self.health_failures + 1
        if self.health_failures >= 3:
            return '生产健康连续三次检查失败'
        if available_mb < 1536:
            if self.low_since is None:
                self.low_since = now
            if available_mb < 1024 or now - self.low_since >= 15:
                return '宿主机内存余量不足'
        else:
            self.low_since = None
        return None


class Runtime:
    def __init__(self, directory):
        self.directory = directory
        self.owned_ids = []

    def capture_owned(self):
        output = docker('ps', '-aq', '--no-trunc', '--filter', f'label={LABEL}=true',
                        '--filter', f'label=com.docker.compose.project={PROJECT}')
        ids = output.splitlines()
        if any(not re.fullmatch('[a-f0-9]{64}', cid) for cid in ids) or len(ids) > 3:
            raise RuntimeError('测试容器范围异常，停止自动操作')
        self.owned_ids = ids

    def stop(self):
        # 冻结 ID 并逐个核验归属，避免同名替换或扩大到其他项目。
        for cid in self.owned_ids:
            owner = docker('inspect', '-f', '{{index .Config.Labels "io.sub2api.preview"}}|'
                           '{{index .Config.Labels "com.docker.compose.project"}}', cid)
            if owner != 'true|' + PROJECT:
                raise RuntimeError('容器归属发生变化，禁止自动停止')
        if self.owned_ids:
            docker('stop', '--time', '10', *self.owned_ids, timeout=45)

    def run(self, baseline):
        guard = Guard()
        startup = None
        try:
            # up 在后台子进程执行，守护从启动数据库前便开始采样。
            startup = subprocess.Popen(
                ['docker', 'compose', '--project-name', PROJECT, '--env-file', '/dev/null',
                 '--file', str(self.directory / 'compose.json'), 'up', '-d', '--pull', 'never'],
                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            self.supervise(startup, baseline, guard)
        finally:
            # 先终止启动客户端，防止停止后仍有后续服务被创建。
            if startup is not None and startup.poll() is None:
                startup.terminate()
                try:
                    startup.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    startup.kill()
                    startup.wait()
            self.capture_owned()
            self.stop()

    def supervise(self, startup, baseline, guard):
        started = time.monotonic()
        ready = False
        app_failures = 0
        while True:
            tick = time.monotonic()
            reason = guard.check(tick, memory_mb(),
                                 healthy(PUBLIC_HEALTH) and healthy('http://127.0.0.1:8080/health'),
                                 production_state() == baseline)
            if reason:
                raise RuntimeError(reason + '，仅停止隔离测试容器，保留测试数据')
            if shutil.disk_usage(self.directory).free < 20 * 1024**3:
                raise RuntimeError('磁盘余量不足 20 GiB，停止隔离测试')
            code = startup.poll()
            if code is not None and code != 0:
                raise RuntimeError('隔离服务启动失败；诊断保留在测试容器日志')
            if code == 0:
                app_ok = healthy('http://127.0.0.1:18090/health')
                if app_ok and not ready:
                    self.capture_owned()
                    if len(self.owned_ids) != 3:
                        raise RuntimeError('隔离服务容器数量不符')
                    print('隔离后端健康：127.0.0.1:18090；正式流量未切换。', flush=True)
                    ready = True
                app_failures = 0 if app_ok else app_failures + 1
                if ready and app_failures >= 3:
                    raise RuntimeError('隔离后端健康连续失败，保留日志并停止测试')
            if not ready and tick - started > 300:
                raise RuntimeError('隔离后端启动超过 300 秒，保留日志并停止测试')
            time.sleep(max(0, SAMPLE_SECONDS - (time.monotonic() - tick)))


def prepare_config(directory, image):
    if directory.is_symlink():
        raise RuntimeError('隔离目录不能是符号链接')
    directory.mkdir(mode=0o700, exist_ok=True)
    if directory.stat().st_uid != 0 or directory.stat().st_mode & 0o077:
        raise RuntimeError('隔离目录必须由 root 拥有且权限为 0700')
    secret_path = directory / 'credentials.json'
    if secret_path.exists():
        if secret_path.is_symlink() or secret_path.stat().st_mode & 0o077:
            raise RuntimeError('测试凭据文件权限不安全')
        credentials = json.loads(secret_path.read_text())
    else:
        if not sys.stdin.isatty():
            raise RuntimeError('首次启动需使用受保护的交互终端隐藏输入密码')
        password = getpass.getpass('隔离测试管理员初始密码（不会输出）：')
        if len(password) < 8:
            raise RuntimeError('测试管理员密码至少 8 位')
        credentials = {key: secrets.token_hex(32) for key in ('database', 'jwt', 'totp')}
        credentials['admin'] = password
        write_private(secret_path, credentials)
    postgres = docker('inspect', '-f', '{{.Image}}', 'sub2api-postgres')
    redis = docker('inspect', '-f', '{{.Image}}', 'sub2api-redis')
    # 仅复用已安装的数据库程序镜像，不复用容器、配置或数据卷。
    version = docker('run', '--rm', '--network', 'none', '--memory', '64m', '--cpus', '0.5',
                     '--entrypoint', 'postgres', postgres, '--version')
    if not re.search(r'PostgreSQL\) 18\.', version):
        raise RuntimeError('只支持已核验的 PostgreSQL 18 数据目录布局')
    hosts_file = directory / 'upstream-hosts.json'
    hosts = ['api.openai.com']
    if hosts_file.exists():
        if hosts_file.is_symlink() or hosts_file.stat().st_mode & 0o077:
            raise RuntimeError('测试上游域名文件权限不安全')
        hosts = json.loads(hosts_file.read_text())
        if not isinstance(hosts, list) or not hosts or any(not isinstance(host, str) or
                not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+', host)
                for host in hosts):
            raise RuntimeError('只允许显式审核的公开上游域名，不允许通配符或 URL')
    config = compose_document(manifest(image, postgres, redis, credentials, hosts))
    path = directory / 'compose.json'
    if path.exists():
        if path.is_symlink() or path.stat().st_mode & 0o077:
            raise RuntimeError('隔离配置文件权限不安全')
        if json.loads(path.read_text()) != config:
            raise RuntimeError('已有测试配置与候选不一致；保留现场，禁止覆盖')
    else:
        write_private(path, config)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('commit', help='已审核、已推送的完整提交 SHA')
    parser.add_argument('image', help='服务器 build 生成的镜像标签')
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise RuntimeError('仅允许在已核验的服务器上以 root 运行')
    # 持锁期间拒绝并发发布；验收结束退出本运行器即可释放。
    with (STATE / 'release.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError('已有发布、清理或隔离验收任务运行') from None
        if (STATE / 'pending-commit').exists():
            raise RuntimeError('存在未完成的正式发布，禁止开始隔离验收')
        builders = docker('ps', '-q', '--filter', 'name=^/buildx_buildkit_sub2api-release0$')
        if builders:
            raise RuntimeError('请先按 README 完成专用构建器清理，再启动验收')
        if memory_mb() < 3072 or shutil.disk_usage(STATE).free < 20 * 1024**3:
            raise RuntimeError('隔离验收至少需要 3 GiB 可用内存和 20 GiB 磁盘')
        baseline = production_state()
        if '|false' in baseline or not healthy(PUBLIC_HEALTH) or not healthy('http://127.0.0.1:8080/health'):
            raise RuntimeError('生产健康基线不满足，禁止启动')
        image = verify_candidate(args.commit, args.image)
        verify_resources()
        prepare_config(ROOT, image)
        runtime = Runtime(ROOT)
        runtime.capture_owned()
        if runtime.owned_ids:
            running = docker('ps', '-q', '--filter', f'label={LABEL}=true',
                             '--filter', f'label=com.docker.compose.project={PROJECT}')
            if running:
                raise RuntimeError('已有隔离容器运行，保留现场，不重复启动')
        runtime.run(baseline)


def interrupted(_signum, _frame):
    raise KeyboardInterrupt


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, interrupted)
    try:
        main()
    except KeyboardInterrupt:
        print('隔离验收已停止；测试数据保留。', file=sys.stderr)
        raise SystemExit(130)
    except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
        # 不输出包含配置内容的异常对象或 traceback。
        print(str(error) if isinstance(error, RuntimeError) else '隔离操作失败，请检查受保护的服务端诊断。',
              file=sys.stderr)
        raise SystemExit(1)

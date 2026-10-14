#!/usr/bin/env python3
"""通过已核验主机指纹的连接触发服务器发布，不在本机构建。"""
import importlib.util
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
ACTIONS = {
    'status', 'build', 'deploy', 'cleanup-build', 'cleanup-release',
    'stage-candidate', 'prepare-bluegreen', 'canary-green',
    'return-blue', 'rollback', 'ensure-primary',
}


def main():
    action = sys.argv[1] if len(sys.argv) > 1 else 'status'
    commit = sys.argv[2] if len(sys.argv) > 2 else ''
    if action not in ACTIONS:
        raise SystemExit('不允许的服务器发布操作')
    if action in {'build', 'deploy'} and not re.fullmatch('[a-f0-9]{40}', commit):
        raise SystemExit('构建和发布需要完整的定制提交 SHA')
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location('preview', ROOT / 'scripts/channel-monitor-preview.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    with tempfile.TemporaryDirectory(prefix='sub2api-release-ssh-') as directory:
        client = module.Preview(Path(directory) / 'known_hosts')
        read_fd, write_fd = os.pipe()
        try:
            os.write(write_fd, (client.config['SUB2API_PRODUCTION_SSH_PASSWORD'] + '\n').encode())
            os.close(write_fd)
            command = [part.replace('{fd}', str(read_fd)) for part in client.ssh]
            command.append(' '.join(shlex.quote(value) for value in [
                '/usr/local/bin/sub2api-release', action, commit,
            ]))
            return subprocess.run(command, pass_fds=(read_fd,)).returncode
        finally:
            os.close(read_fd)


if __name__ == '__main__':
    raise SystemExit(main())

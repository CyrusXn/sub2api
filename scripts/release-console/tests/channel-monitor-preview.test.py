"""发布 SSH 通道的本地回归测试，不连接生产服务器。"""
import base64
import hashlib
import importlib.util
from pathlib import Path
import subprocess
import time
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location(
    "channel_monitor_preview", ROOT / "scripts/channel-monitor-preview.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class PreviewSshTests(unittest.TestCase):
    def test_accepts_matching_key_when_keyscan_reports_nonzero(self):
        key = base64.b64encode(b"test-ed25519-key").decode()
        fingerprint = "SHA256:" + base64.b64encode(
            hashlib.sha256(base64.b64decode(key)).digest()
        ).decode().rstrip("=")
        result = subprocess.CompletedProcess(
            ["ssh-keyscan"], 1, f"192.0.2.1 ssh-ed25519 {key}\n", "connection closed\n"
        )
        with patch.object(MODULE.subprocess, "run", return_value=result):
            self.assertEqual(
                MODULE.scan_host_keys("192.0.2.1", fingerprint),
                [f"192.0.2.1 ssh-ed25519 {key}"],
            )

    def test_timed_command_kills_stalled_process_group(self):
        started = time.monotonic()
        with self.assertRaises(subprocess.TimeoutExpired):
            MODULE.run_timed(
                ["python3", "-c", "import time; time.sleep(10)"], timeout=0.1
            )
        self.assertLess(time.monotonic() - started, 2)


if __name__ == "__main__":
    unittest.main()

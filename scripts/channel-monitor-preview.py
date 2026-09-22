#!/usr/bin/env python3
"""本地只读验收：复用当前聚合 SQL 读取线上最近一小时，不启动应用后端。"""
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError
from urllib.parse import parse_qs, urlsplit
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[1]
UPSTREAM = "https://api.xnkaixin.eu.cc"


def preview_sql():
    source = (ROOT / "backend/internal/repository/channel_monitor_passive_repo.go").read_text()
    aggregate = re.search(r"const channelMonitorPassiveAggregateSQL = `(.+?)`", source, re.S).group(1)
    cards = re.search(r"const channelMonitorPassiveCardsSQL = `(.+?)`", source, re.S).group(1)
    ctes, insert = aggregate.split("INSERT INTO channel_monitor_passive_minutes", 1)
    select = "SELECT m.group_id" + insert.split("SELECT m.group_id", 1)[1].split("ON CONFLICT", 1)[0]
    # 沿用正式代码的分类与卡片 SQL，仅以只读 CTE 替代落库。
    select = select.replace("m.requests, m.successes, m.errors, m.first_token_ms,", "m.requests AS request_count, m.successes AS success_count, m.errors AS error_count, m.first_token_ms,")
    select = select.replace("ELSE 'degraded' END", "ELSE 'degraded' END AS status")
    end = "date_trunc('minute', NOW())"
    start = f"({end} - INTERVAL '1 hour')"
    ctes = re.sub(r"\$(\d+)\b", lambda m: {"1": start, "2": end}[m[1]], ctes)
    cards = re.sub(r"\$(\d+)\b", lambda m: {"1": end, "2": "FALSE", "3": "ARRAY[]::bigint[]", "4": "ARRAY[]::text[]", "5": start}[m[1]], cards)
    query = ctes + ", channel_monitor_passive_minutes AS (" + select + ")\n," + cards.removeprefix("\nWITH ")
    columns = "id, name, provider, primary_status, primary_latency_ms, success_rate, availability_7d, availability_15d, availability_30d, timeline"
    return "BEGIN READ ONLY; SET LOCAL statement_timeout = '10s'; SET LOCAL TIME ZONE 'UTC';\nSELECT COALESCE(json_agg(row_to_json(card)), '[]') FROM (" + query + ") AS card(" + columns + ");\nROLLBACK;"


class Preview:
    def __init__(self, known_hosts):
        self.config = {}
        for line in (ROOT / "sub2api-production-server.local").read_text().splitlines():
            if line.startswith("SUB2API_") and "=" in line:
                key, value = line.split("=", 1)
                self.config[key] = value.strip().strip('"').strip("'")
        host = self.config["SUB2API_PRODUCTION_IPV4"]
        scanned = subprocess.run(["ssh-keyscan", "-T", "5", "-t", "ed25519", host], capture_output=True, text=True, timeout=10, check=True)
        matched = []
        for line in scanned.stdout.splitlines():
            parts = line.split()
            if len(parts) == 3 and parts[1] == "ssh-ed25519":
                digest = base64.b64encode(hashlib.sha256(base64.b64decode(parts[2])).digest()).decode().rstrip("=")
                if "SHA256:" + digest == self.config["SUB2API_PRODUCTION_SSH_ED25519_SHA256"]:
                    matched.append(line)
        if not matched:
            raise RuntimeError("SSH 主机指纹不匹配，已停止只读预览")
        Path(known_hosts).write_text("\n".join(matched) + "\n")
        os.chmod(known_hosts, 0o600)
        self.ssh = ["sshpass", "-d", "{fd}", "ssh", "-o", "StrictHostKeyChecking=yes", "-o", f"UserKnownHostsFile={known_hosts}", "-o", "ConnectTimeout=8", self.config["SUB2API_PRODUCTION_SSH_USER"] + "@" + host]
        self.lock = threading.Lock()
        self.minute = -1
        self.items = []

    def collect(self):
        with self.lock:
            minute = int(time.time() // 60)
            if self.minute == minute:
                return self.items
            read_fd, write_fd = os.pipe()
            try:
                os.write(write_fd, (self.config["SUB2API_PRODUCTION_SSH_PASSWORD"] + "\n").encode())
                os.close(write_fd)
                command = [arg.replace("{fd}", str(read_fd)) for arg in self.ssh]
                command += ["docker exec -i sub2api-postgres sh -c 'exec psql -X -qAt -v ON_ERROR_STOP=1 -U \"$POSTGRES_USER\" -d \"$POSTGRES_DB\"'"]
                result = subprocess.run(command, input=preview_sql(), capture_output=True, text=True, timeout=20, pass_fds=(read_fd,))
            finally:
                os.close(read_fd)
            if result.returncode:
                raise RuntimeError("只读聚合失败，未执行任何迁移或写入")
            rows = json.loads(result.stdout)
            self.items = [dict(row, passive=True) for row in rows]
            if any(not isinstance(card.get("id"), int) or not isinstance(card.get("timeline"), list) or card.get("primary_status") not in {"operational", "degraded", "error"} for card in self.items):
                raise RuntimeError("预览聚合字段不匹配")
            self.minute = minute
            return self.items


def get_api(path, token):
    with urlopen(Request(UPSTREAM + path, headers={"Authorization": token}), timeout=10) as response:
        payload = json.load(response)
        if payload.get("code") != 0:
            raise RuntimeError("线上身份校验失败")
        return payload["data"]


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass  # 不记录请求头、令牌或用户信息。

    def reply(self, status, payload):
        data = json.dumps(payload, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if urlsplit(self.path).path != "/api/v1/channel-monitor-passive":
            return self.reply(404, {"code": 404, "message": "not found"})
        token = self.headers.get("Authorization", "")
        if not token.startswith("Bearer "):
            return self.reply(401, {"code": 401, "message": "请先登录"})
        try:
            user = get_api("/api/v1/auth/me", token)
            allowed = None
            if user.get("role") != "admin":
                allowed = {g["id"] for g in get_api("/api/v1/groups/available", token)}
            params = parse_qs(urlsplit(self.path).query)
            platforms = set(",".join(params.get("platform", [])).split(",")) - {""}
            items = [card for card in self.server.preview.collect() if (allowed is None or card["id"] in allowed) and (not platforms or card["provider"] in platforms)]
            self.reply(200, {"code": 0, "data": {"items": items}})
        except HTTPError as error:
            self.reply(error.code, {"code": error.code, "message": "线上身份校验失败，请重新登录"})
        except Exception:
            self.reply(503, {"code": 503, "message": "只读预览暂不可用"})


def verify_query():
    query = preview_sql()
    assert query.startswith("BEGIN READ ONLY;")
    assert query.endswith("ROLLBACK;")
    assert "statement_timeout = '10s'" in query
    assert not re.search(r"\b(INSERT|UPDATE|DELETE|CREATE|DROP|ALTER)\b|\$\d", query)
    assert "AS card(id, name, provider, primary_status" in query


if __name__ == "__main__":
    verify_query()
    with tempfile.TemporaryDirectory(prefix="sub2api-preview-") as directory:
        preview = Preview(Path(directory) / "known_hosts")
        cards = preview.collect()
        print(f"只读预览已就绪：最近一小时 {len(cards)} 个有记录分组；监听 127.0.0.1:3101", flush=True)
        server = ThreadingHTTPServer(("127.0.0.1", 3101), Handler)
        server.preview = preview
        server.serve_forever()

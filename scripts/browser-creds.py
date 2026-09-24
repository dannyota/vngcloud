#!/usr/bin/env python3
"""Serve IAM User credentials from .env to scripts/browser-login.js.

Playwright MCP code cannot read files, so this helper hands the values to the
browser session over 127.0.0.1 without them appearing in agent output. It
answers GET /creds once and GET /totp once, then exits, or exits after the
timeout. It rejects any Host header other than its own address, so a web page
cannot read it through DNS rebinding. It never prints a credential.
"""

import base64
import hashlib
import hmac
import http.server
import json
import os
import re
import struct
import sys
import time

PORT = 18765
TIMEOUT_SECONDS = 180
MAX_SERVED = {"/creds": 1, "/totp": 1}
HOSTS = {f"127.0.0.1:{PORT}", f"localhost:{PORT}"}


def load_env(path):
    env = {}
    with open(path, encoding="utf-8") as f:
        for line in f:
            m = re.match(r"^\s*(?:export\s+)?([A-Z_][A-Z0-9_]*)\s*=\s*(.*?)\s*$", line)
            if m:
                env[m.group(1)] = m.group(2).strip("\"'")
    return env


def totp(secret, now=None):
    s = secret.replace(" ", "").upper().rstrip("=")
    key = base64.b32decode(s + "=" * (-len(s) % 8))
    counter = int(now if now is not None else time.time()) // 30
    digest = hmac.new(key, struct.pack(">Q", counter), hashlib.sha1).digest()
    offset = digest[-1] & 0x0F
    code = struct.unpack(">I", digest[offset : offset + 4])[0] & 0x7FFFFFFF
    return "%06d" % (code % 1000000)


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    env = load_env(sys.argv[1] if len(sys.argv) > 1 else os.path.join(root, ".env"))
    served = {path: 0 for path in MAX_SERVED}

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if self.headers.get("Host") not in HOSTS:
                self.send_response(403)
                self.end_headers()
                return
            if self.path not in MAX_SERVED or served[self.path] >= MAX_SERVED[self.path]:
                self.send_response(404)
                self.end_headers()
                return
            served[self.path] += 1
            if self.path == "/creds":
                body = {
                    "rootEmail": env.get("VNGCLOUD_ROOT_EMAIL", ""),
                    "username": env.get("VNGCLOUD_USERNAME", ""),
                    "password": env.get("VNGCLOUD_PASSWORD", ""),
                }
            else:
                secret = env.get("VNGCLOUD_TOTP_SECRET", "")
                body = {"code": totp(secret) if secret else ""}
            data = json.dumps(body).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, *args):
            pass

    server = http.server.HTTPServer(("127.0.0.1", PORT), Handler)
    server.timeout = 1
    deadline = time.time() + TIMEOUT_SECONDS
    print(f"serving on 127.0.0.1:{PORT} for up to {TIMEOUT_SECONDS}s", flush=True)
    needs_totp = bool(env.get("VNGCLOUD_TOTP_SECRET"))
    while time.time() < deadline:
        if served["/creds"] and (served["/totp"] or not needs_totp):
            break
        server.handle_request()
    print("done", flush=True)


if __name__ == "__main__":
    main()

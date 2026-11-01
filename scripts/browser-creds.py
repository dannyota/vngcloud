#!/usr/bin/env python3
"""Serve IAM User credentials from .env or a CLI profile to scripts/browser-login.js.

Playwright MCP code cannot read files, so this helper hands the values to the
browser session over 127.0.0.1 without them appearing in agent output. It
answers GET /creds once and GET /totp once, then exits, or exits after the
timeout. It rejects any Host header other than its own address, so a web page
cannot read it through DNS rebinding. It never prints a credential.
"""

import argparse
import base64
import hashlib
import hmac
import http.server
import json
import os
import re
import stat
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


class IniParseError(ValueError):
    pass


def read_cli_ini(source):
    # Match internal/ini: no inherited defaults, continuations, or ':' delimiter.
    sections = {}
    section = None
    for number, line in enumerate(source):
        if number == 0:
            line = line.removeprefix("\ufeff")
        trimmed = line.strip()
        if not trimmed or trimmed.startswith(("#", ";")):
            continue
        if trimmed.startswith("["):
            if not trimmed.endswith("]"):
                raise IniParseError()
            section = trimmed[1:-1].strip()
            sections.setdefault(section, {})
        else:
            key, separator, value = line.partition("=")
            if not separator or section is None or not key.strip():
                raise IniParseError()
            sections[section][key.strip().lower()] = value.strip()
    return sections


def load_profile(path, profile):
    try:
        descriptor = os.open(path, os.O_RDONLY | os.O_NONBLOCK)
        try:
            info = os.fstat(descriptor)
            if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) & 0o077:
                raise ValueError(f"cannot read private credentials file: {path}")
            with os.fdopen(descriptor, encoding="utf-8", closefd=False) as source:
                sections = read_cli_ini(source)
        finally:
            os.close(descriptor)
    except OSError:
        raise ValueError(f"cannot read private credentials file: {path}") from None
    except (IniParseError, UnicodeError):
        raise ValueError(f"cannot parse credentials file: {path}") from None
    fields = ("root_email", "username", "password", "totp_secret")
    values = sections.get(profile, {})
    return {
        "VNGCLOUD_" + field.upper(): values.get(field, "")
        for field in fields
    }


def main(argv=None):
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("path", nargs="?", default=os.path.join(root, ".env"))
    parser.add_argument("--profile")
    parser.add_argument("--credentials", default="~/.vngcloud/credentials")
    args = parser.parse_args(argv)
    if args.profile is not None:
        try:
            env = load_profile(os.path.expanduser(args.credentials), args.profile)
        except ValueError as error:
            print(str(error), file=sys.stderr)
            return 2
    else:
        env = load_env(args.path)
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
    sys.exit(main())

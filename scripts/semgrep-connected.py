#!/usr/bin/env python3
"""Require connected product evidence from the pinned Semgrep CI output."""

import os
import re
import selectors
import signal
import subprocess
import sys
import time

ARGV = ("semgrep", "ci", "--code", "--supply-chain", "--secrets", "--no-suppress-errors")
PRODUCTS = ("Code", "Supply Chain", "Secrets")
MAX_BYTES = 16 * 1024 * 1024
MAX_SECONDS = 12 * 60
GRACE_SECONDS = 2
RECORD_LINES = 16
RECORD_CHARS = 2048
SGR = re.compile(r"\x1b\[[0-9;]*m")
NAMES = r"(?:Code|Supply Chain|Secrets)"
ENABLED = re.compile(rf"Enabled products: ({NAMES}(?:, {NAMES})*)")
HEADER = re.compile(r"Scanning ([0-9]{1,10}) (file|files) tracked by git with (.+):")
COUNT = re.compile(rf"([0-9]{{1,10}}) ({NAMES}) (rule|rules)")
SCANS = re.compile(r"\bScanning[ \t\n]+[0-9]+[ \t\n]+files?\b")
SUMMARY = re.compile(
    r"Scanning ([0-9]{1,10}) (file|files)"
    r"(?: with ([0-9]{1,10}) ([A-Za-z][A-Za-z0-9_+#-]{0,63}|<multilang>) (rule|rules))?\."
)
SECTIONS = {"CODE RULES": "Code", "SECRETS RULES": "Secrets"}
SECRETS_DETAIL = "AI augmented rules are active for secrets detection."


class Failure(Exception):
    """Carry only a fixed diagnostic label."""


def record(lines, start, pattern, label):
    text = ""
    for offset, line in enumerate(lines[start:start + RECORD_LINES]):
        # Rich wraps at spaces and pads each physical line. Keep token boundaries.
        text += (" " if text else "") + line.strip(" \t")
        if len(text) > RECORD_CHARS:
            break
        match = pattern.fullmatch(text)
        if match:
            return match, start + offset + 1
        if not line.strip(" \t"):
            break
    raise Failure(label)


def summary_section(lines, row, main_end):
    for i in range(row - 1, max(main_end - 1, row - RECORD_LINES), -1):
        product = SECTIONS.get(lines[i].strip(" \t"))
        if product:
            detail = " ".join(line.strip(" \t") for line in lines[i + 1:row]
                              if line.strip(" \t"))
            if len(detail) <= RECORD_CHARS and (not detail or
                    (product == "Secrets" and detail == SECRETS_DETAIL)):
                return product
            break
    raise Failure("rule-counts-invalid")


def scan_records(text, lines, main_start, main_end):
    seen_main = False
    summaries = set()
    candidates = 0
    for marker in SCANS.finditer(text):
        candidates += 1
        # The pinned formatter emits one main record and at most one native
        # file summary per Code/Secrets section. Keep global candidate checking.
        if candidates > 3:
            raise Failure("rule-counts-invalid")
        row = text.count("\n", 0, marker.start())
        if row == main_start and not seen_main:
            seen_main = True
            continue
        if row < main_end:
            raise Failure("rule-counts-invalid")
        product = summary_section(lines, row, main_end)
        summary, _end = record(lines, row, SUMMARY, "rule-counts-invalid")
        if product in summaries or (summary[2] == "file") != (int(summary[1]) == 1):
            raise Failure("rule-counts-invalid")
        if summary[3] is not None:
            count = int(summary[3])
            if count <= 0 or summary[4] in PRODUCTS or (summary[5] == "rule") != (count == 1):
                raise Failure("rule-counts-invalid")
        summaries.add(product)
    if not seen_main:
        raise Failure("rule-counts-invalid")


def coverage(raw):
    try:
        text = raw.decode("utf-8", errors="strict")
    except UnicodeError:
        raise Failure("format-invalid") from None
    text = SGR.sub("", text).replace("\r\n", "\n")
    if re.search(r"[\x00-\x08\x0b-\x1f\x7f]", text):
        raise Failure("format-invalid")
    lines = text.split("\n")
    # Count markers in all output, including findings and filenames. A second
    # record can only reject coverage, never replace incomplete scanner evidence.
    if text.count("Enabled products") != 1:
        raise Failure("enabled-products-invalid")
    enabled_lines = [i for i, line in enumerate(lines)
                     if line.strip(" \t").startswith("Enabled products:")]
    if len(enabled_lines) != 1:
        raise Failure("enabled-products-invalid")
    enabled, _end = record(lines, enabled_lines[0], ENABLED, "enabled-products-invalid")
    names = enabled[1].split(", ")
    if len(names) != len(PRODUCTS) or set(names) != set(PRODUCTS):
        raise Failure("enabled-products-invalid")

    if text.count("Scan Status") != 1:
        raise Failure("rule-counts-invalid")
    titles = [i for i, line in enumerate(lines) if line.strip(" \t") == "│ Scan Status │"]
    if len(titles) != 1:
        raise Failure("rule-counts-invalid")
    title = titles[0]
    if (title == 0 or title + 1 >= len(lines)
            or lines[title - 1].strip(" \t") != "┌─────────────┐"
            or lines[title + 1].strip(" \t") != "└─────────────┘"):
        raise Failure("rule-counts-invalid")
    start = title + 2
    while start < min(len(lines), title + RECORD_LINES) and not lines[start].strip(" \t"):
        start += 1
    header, end = record(lines, start, HEADER, "rule-counts-invalid")
    if (header[2] == "file") != (int(header[1]) == 1):
        raise Failure("rule-counts-invalid")
    counts = {}
    for part in header[3].split(", "):
        match = COUNT.fullmatch(part)
        if not match:
            raise Failure("rule-counts-invalid")
        count, product, unit = int(match[1]), match[2], match[3]
        if count <= 0 or product in counts or (unit == "rule") != (count == 1):
            raise Failure("rule-counts-invalid")
        counts[product] = count
    if set(counts) != set(PRODUCTS):
        raise Failure("rule-counts-invalid")
    scan_records(text, lines, start, end)
    return counts


def stop(child):
    # Semgrep can spawn an engine child. Stop the session even when its leader
    # has exited, so an inherited output pipe cannot outlive the time limit.
    for sig in (signal.SIGTERM, signal.SIGKILL):
        try:
            os.killpg(child.pid, sig)
        except ProcessLookupError:
            pass
        try:
            child.wait(timeout=GRACE_SECONDS)
        except subprocess.TimeoutExpired:
            continue
        if sig == signal.SIGKILL:
            return
    raise Failure("cleanup-failed")


def capture(child, cancelled):
    raw = bytearray()
    deadline = time.monotonic() + MAX_SECONDS
    os.set_blocking(child.stdout.fileno(), False)
    with selectors.DefaultSelector() as selector:
        selector.register(child.stdout, selectors.EVENT_READ)
        while True:
            if cancelled[0]:
                raise Failure("cancelled")
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise Failure("timeout")
            if not selector.select(timeout=min(remaining, 0.25)):
                continue
            chunk = os.read(child.stdout.fileno(), min(65536, MAX_BYTES - len(raw) + 1))
            if not chunk:
                break
            if len(raw) + len(chunk) > MAX_BYTES:
                raise Failure("output-limit")
            raw.extend(chunk)
    while child.poll() is None:
        if cancelled[0]:
            raise Failure("cancelled")
        if time.monotonic() >= deadline:
            raise Failure("timeout")
        time.sleep(0.05)
    if cancelled[0]:
        raise Failure("cancelled")
    return child.returncode, raw


def run():
    child = None
    handlers = {}
    label = None
    status = 1
    counts = None
    cancelled = [False]

    def cancel(_signum, _frame):
        # Do not interrupt Popen before the child is available for cleanup.
        cancelled[0] = True

    try:
        for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            handlers[sig] = signal.signal(sig, cancel)
        try:
            child = subprocess.Popen(ARGV, stdin=subprocess.DEVNULL,
                                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                     start_new_session=True)
        except OSError:
            raise Failure("launch-failed") from None
        scanner_status, raw = capture(child, cancelled)
        if scanner_status:
            status = scanner_status if scanner_status > 0 else 128 - scanner_status
            label = "scanner-failed"
        else:
            counts = coverage(raw)
            if cancelled[0]:
                raise Failure("cancelled")
            status = 0
    except Failure as failure:
        label = str(failure)
    except KeyboardInterrupt:
        label = "cancelled"
    except Exception:
        label = "capture-failed"
    finally:
        # Repeated cancellation must not interrupt termination and reaping.
        for sig in handlers:
            signal.signal(sig, signal.SIG_IGN)
        if child is not None:
            try:
                stop(child)
            except Exception:
                label = "cleanup-failed"
                status = status or 1
            finally:
                child.stdout.close()
        for sig, handler in handlers.items():
            signal.signal(sig, handler)
    if label:
        status = status or 1
        print("semgrep-connected: " + label, file=sys.stderr)
    elif counts is not None:
        print("semgrep-connected: coverage-ok")
        for product in PRODUCTS:
            print(f"{product} rules: {counts[product]}")
    return status


if __name__ == "__main__":
    if len(sys.argv) != 1:
        print("semgrep-connected: invalid-invocation", file=sys.stderr)
        sys.exit(1)
    sys.exit(run())

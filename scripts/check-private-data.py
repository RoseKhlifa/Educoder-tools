"""Audit the exact Git index without printing private values.

Local HAR files, when available, supply additional sensitive values to compare
against staged blobs. Only path/count summaries are printed.
"""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--pre-push", action="store_true", help="Read and audit the exact new commits from Git hook stdin")
args = parser.parse_args()
BLOCKED_PREFIXES = ("artifacts/", "node_modules/", "har_assets/", "design-demos/",
                    "resource-reuse/original/", "resource-reuse/reports/", "resource-reuse/verification/")
BLOCKED_SUFFIXES = (".har", ".log", ".pid", ".pem", ".key", ".exe")
PRIVATE_KEY = re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----")
secrets: set[bytes] = set()
for recording in ROOT.glob("*.har"):
    try:
        har = json.loads(recording.read_text(encoding="utf-8-sig"))
        for entry in har.get("log", {}).get("entries", []):
            for message in (entry.get("request", {}), entry.get("response", {})):
                for cookie in message.get("cookies", []):
                    value = cookie.get("value", "")
                    if isinstance(value, str) and len(value) >= 8:
                        secrets.add(value.encode())
                for header in message.get("headers", []):
                    if header.get("name", "").lower() in {"pc-authorization", "authorization"}:
                        value = header.get("value", "")
                        if isinstance(value, str) and len(value) >= 8:
                            secrets.add(value.encode())
            request = entry.get("request", {})
            if request.get("method") == "POST" and "/accounts/login.json" in request.get("url", ""):
                body = json.loads(request.get("postData", {}).get("text", "{}"))
                for key in ("login", "password"):
                    value = body.get(key)
                    if isinstance(value, str) and len(value) >= 8:
                        secrets.add(value.encode())
    except (ValueError, OSError, TypeError):
        raise SystemExit("PRIVACY=FAIL; local recording could not be inspected")

entries = []
commits = set()
if args.pre_push:
    for line in sys.stdin:
        fields = line.split()
        if len(fields) != 4:
            raise SystemExit("PRIVACY=FAIL; unexpected pre-push reference format")
        _, local_sha, _, remote_sha = fields
        if not re.fullmatch(r"[0-9a-f]{40}", local_sha) or not re.fullmatch(r"[0-9a-f]{40}", remote_sha):
            raise SystemExit("PRIVACY=FAIL; invalid object ID")
        if local_sha == "0" * 40:
            continue  # Reference deletion publishes no objects.
        command = ["git", "rev-list", local_sha]
        if remote_sha != "0" * 40:
            command.append("^" + remote_sha)
        commits.update(subprocess.check_output(command, cwd=ROOT).decode().splitlines())
    for commit in sorted(commits):
        tree = subprocess.check_output(["git", "ls-tree", "-r", "-z", commit], cwd=ROOT)
        for record in tree.split(b"\0"):
            if record:
                metadata, name = record.split(b"\t", 1)
                mode, kind, sha = metadata.split()
                if kind != b"blob":
                    raise SystemExit("PRIVACY=FAIL; review non-blob entry before publishing")
                entries.append((name.decode(), mode, sha.decode(), commit))
else:
    index = subprocess.check_output(["git", "ls-files", "--stage", "-z"], cwd=ROOT)
    for record in index.split(b"\0"):
        if record:
            metadata, name = record.split(b"\t", 1)
            mode, sha, stage = metadata.split()
            entries.append((name.decode(), mode, sha.decode(), stage))

problems = []
checked_blobs = set()
with subprocess.Popen(["git", "cat-file", "--batch"], cwd=ROOT, stdin=subprocess.PIPE, stdout=subprocess.PIPE) as blobs:
    for name, mode, sha, stage in entries:
        basename = Path(name).name
        if name.startswith(BLOCKED_PREFIXES) or name.lower().endswith(BLOCKED_SUFFIXES) or (basename.startswith(".env") and basename != ".env.example"):
            problems.append((name, "private/generated path"))
        if mode == b"120000":
            problems.append((name, "review symlink before publishing"))
        if sha in checked_blobs:
            continue
        checked_blobs.add(sha)
        blobs.stdin.write((sha + "\n").encode())
        blobs.stdin.flush()
        header = blobs.stdout.readline().split()
        if len(header) != 3 or header[1] != b"blob":
            raise SystemExit("PRIVACY=FAIL; index object could not be read")
        size = int(header[2])
        body = blobs.stdout.read(size)
        assert blobs.stdout.read(1) == b"\n"
        if PRIVATE_KEY.search(body):
            problems.append((name, "private key material"))
        if any(value in body for value in secrets):
            problems.append((name, "local account/session value"))
    blobs.stdin.close()

if problems:
    for path, reason in problems:
        print(f"PRIVACY_ISSUE path={path} category={reason}")
    raise SystemExit("PRIVACY=FAIL")
scope = f"PUSH_COMMITS={len(commits)}; TREE_ENTRIES={len(entries)}" if args.pre_push else f"STAGED_FILES={len(entries)}"
print(f"PRIVACY=PASS; {scope}; PRIVATE_VALUE_MATCHES=0; FORBIDDEN_PATHS=0")

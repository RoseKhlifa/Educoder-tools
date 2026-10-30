"""Inventory/download public static resource URLs recorded in a HAR.

Never replays HAR request headers, credentials, cookies, POSTs, or /api/ URLs.
Only `download` performs network requests; `inventory` is entirely offline.
The resulting files are original downloaded response bytes, not reconstructed UI.
"""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
from collections import deque
import hashlib
import json
import re
import sys
import time
from pathlib import Path, PurePosixPath
from urllib.parse import urljoin, urlsplit, urlunsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener

HOSTS = {"www.educoder.net", "www-cdn.educoder.net", "ali-cdn.educoder.net"}
EXTENSIONS = {".js", ".mjs", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".avif", ".woff", ".woff2", ".ttf", ".otf", ".eot", ".wasm", ".webmanifest"}
STARTUP_NAMES = {"p__index", "layouts__index", "layouts__LoginAndRegister__index", "p__User__Login__index",
                 "p__User__Detail__Classrooms__index", "p__User__Detail__id",
                 "p__Classrooms__Lists__ShixunHomeworks__index",
                 "p__Classrooms__Lists__ShixunHomeworks__Detail__index", "p__tasks__index"}
# Parent routes in the recorded Umi route table: root, classroom layout, user
# profile layout, task layout. The report includes the resolved chunk names.
STARTUP_PARENT_ROUTES = {"1", "109", "249", "337"}


def allowed(url: str) -> bool:
    p = urlsplit(url)
    return (p.scheme == "https" and p.hostname in HOSTS and p.port in (None, 443)
            and p.username is None and p.password is None
            and not p.path.lower().startswith("/api/")
            and not p.query and not p.fragment)


def static_path(url: str) -> bool:
    return PurePosixPath(urlsplit(url).path).suffix.lower() in EXTENSIONS


def read_inventory(har_path: Path) -> dict:
    har = json.loads(har_path.read_text(encoding="utf-8-sig"))
    items: dict[str, dict] = {}
    cached = 0
    for entry in har["log"]["entries"]:
        req, resp = entry.get("request", {}), entry.get("response", {})
        url = req.get("url", "")
        if req.get("method") != "GET" or not allowed(url) or not static_path(url):
            continue
        if resp.get("status") not in (200, 304):
            continue
        items[url] = {"url": url, "statusInHar": resp.get("status"),
                      "mimeType": resp.get("content", {}).get("mimeType")}
        cached += int(resp.get("status") == 304)
    return {"har": har_path.name, "harSha256": hashlib.sha256(har_path.read_bytes()).hexdigest(),
            "recordedStaticCount": len(items), "cachedEntries": cached,
            "resourceUrls": [items[url] for url in sorted(items)],
            "limitations": ["Observed static resources only; this is not a complete site manifest.",
                            "Lazy chunks not visited in the recording must be captured separately.",
                            "Bodies are fetched fresh, not reconstructed from HAR text.",
                            "Query-bearing, API, WebSocket and third-party resources are deliberately excluded."]}


class StaticRedirects(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if not allowed(newurl):
            raise ValueError("Redirect leaves the configured static resource hosts")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def dependencies(url: str, body: bytes, content_type: str) -> list[str]:
    if not any(kind in content_type for kind in ("html", "css", "javascript")) and PurePosixPath(urlsplit(url).path).suffix not in {".js", ".css", ".html"}:
        return []
    source = body.decode("utf-8", errors="replace")
    # Read explicit HTML, CSS and absolute asset references; do not execute JS.
    values = re.findall(r'''(?:src|href)\s*=\s*["']([^"']+)["']''', source, flags=re.I)
    values += re.findall(r'''url\(\s*["']?([^\s)"']+)["']?\s*\)''', source, flags=re.I)
    values += re.findall(r'''https://(?:www-cdn|ali-cdn)\.educoder\.net/[^\s"'<>`\\]+''', source)
    # Webpack emitted asset modules use `e.p+"static/name.hash.svg"`. This reads
    # string literals only; no downloaded JavaScript is evaluated.
    values += re.findall(r'''\.p\s*\+\s*["']((?:static|assets)/[^"']+)["']''', source)
    result = []
    for value in values:
        if any(token in value for token in ("${", "{", "}")):
            continue  # A JS template expression is not a concrete asset URL.
        resolved = urljoin(url, value)
        parts = urlsplit(resolved)
        resolved = urlunsplit((parts.scheme, parts.netloc, parts.path, parts.query, ""))
        if allowed(resolved) and static_path(resolved):
            result.append(resolved)
    return result


def runtime_manifest(source: str) -> dict | None:
    """Read the two literal webpack filename maps, without executing any JS."""
    result = {"js": {}, "css": {}, "names": {}}
    for marker, kind, suffix in (("__webpack_require__.u=", "js", ".async.js"),
                                  ("__webpack_require__.miniCssF=", "css", ".chunk.css")):
        start = source.find(marker)
        if start < 0:
            continue
        end = source.find("}}(),", start)
        if end < 0:
            raise ValueError("Unrecognized webpack filename function boundary")
        fragment = source[start:end]
        objects = re.findall(r'\{(?:\d+:"[^"\\]*",?)+\}', fragment)
        if len(objects) != 2 or suffix not in fragment:
            raise ValueError("Unrecognized webpack filename maps; no JavaScript was executed")
        names, hashes = [dict(re.findall(r'(\d+):"([^"\\]*)"', obj)) for obj in objects]
        result["names"].update(names)
        result[kind] = {key: f"{names.get(key, key)}.{value}{suffix}" for key, value in hashes.items()}
    if not result["js"]:
        return None
    selected = {key for key, name in result["names"].items() if name in STARTUP_NAMES}
    route_targets = set(selected)
    # Read each lazy route expression as text, keeping only selected startup
    # routes and their shared Promise.all dependencies.
    for match in re.finditer(r'(?<!\d)(\d+):[\w$]+\.lazy\(\(\)=>((?:Promise\.all\(\[[^\]]+\]\)|__webpack_require__\.e\(\d+\)))\.then', source):
        ids = set(re.findall(r'\.e\((\d+)\)', match.group(2)))
        if ids & route_targets or match.group(1) in STARTUP_PARENT_ROUTES:
            selected.update(ids)
    result["startupIds"] = sorted(selected, key=int)
    return result


def chunk_urls(manifest: dict, ids: set[str]) -> list[str]:
    return ["https://www-cdn.educoder.net/" + manifest[kind][key]
            for key in sorted(ids, key=int) for kind in ("js", "css") if key in manifest[kind]]


def fetch_static(url: str) -> tuple[bytes | None, str, str | None]:
    for attempt in range(3):
        try:
            opener = build_opener(StaticRedirects())  # No CookieJar or HAR headers.
            req = Request(url, headers={"User-Agent": "StaticResourceMirror/1.1", "Accept-Encoding": "identity"})
            with opener.open(req, timeout=30) as response:
                mime = response.headers.get("Content-Type", "")
                if response.headers.get("Content-Encoding", "identity").lower() != "identity":
                    raise ValueError("Compressed response despite Accept-Encoding: identity")
                body = response.read(32 * 1024 * 1024 + 1)
                if len(body) > 32 * 1024 * 1024:
                    raise ValueError("Static resource exceeds 32 MiB")
                if static_path(url) and "text/html" in mime.lower():
                    raise ValueError("Asset request returned HTML instead of an asset")
            return body, mime, None
        except Exception as exc:
            if attempt == 2:
                return None, "", str(exc)
            time.sleep(0.3 * (attempt + 1))


def local_path(root: Path, url: str) -> Path:
    p = urlsplit(url)
    relative = PurePosixPath(p.path.lstrip("/") or "index.html")
    if any(part in ("..", ".") for part in relative.parts) or "\\" in str(relative) or "%" in str(relative) or ":" in str(relative):
        raise ValueError("Non-canonical asset path")
    target = root / str(p.hostname) / Path(*relative.parts)
    if not target.resolve().is_relative_to(root.resolve()):
        raise ValueError("Asset path escapes output root")
    return target


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("inventory", "download"))
    parser.add_argument("--har", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--out", type=Path)
    parser.add_argument("--workers", type=int, default=8)
    parser.add_argument("--max-assets", type=int, default=1200)
    parser.add_argument("--resume", action="store_true", help="Reuse existing original bytes without overwriting them")
    args = parser.parse_args()
    if not 1 <= args.workers <= 16 or args.max_assets < 1:
        parser.error("--workers must be 1..16 and --max-assets must be positive")
    report = read_inventory(args.har)
    if args.mode == "download":
        if args.out is None:
            parser.error("download requires --out")
        if args.out.exists() and not args.resume:
            parser.error("--out must be a new directory to preserve original bytes")
        args.out.mkdir(parents=True, exist_ok=args.resume)
        queue = deque(["https://www.educoder.net/"] + [item["url"] for item in report["resourceUrls"]])
        seen, downloaded, failures = set(), [], []
        manifests = {}
        chunk_sources = []
        if args.resume:
            for path in sorted(args.out.rglob("*")):
                if not path.is_file():
                    continue
                relative = path.relative_to(args.out).as_posix()
                host, _, suffix = relative.partition("/")
                url = "https://" + host + "/" + ("" if suffix == "index.html" else suffix)
                if not allowed(url):
                    raise ValueError("Existing tree contains a non-static origin")
                body = path.read_bytes()
                seen.add(url)
                downloaded.append({"url": url, "path": relative, "bytes": len(body),
                                   "sha256": hashlib.sha256(body).hexdigest(), "reused": True})
                mime = {".html": "text/html", ".js": "application/javascript", ".css": "text/css"}.get(path.suffix, "")
                queue.extend(dependencies(url, body, mime))
                if path.suffix == ".js":
                    source = body.decode("utf-8", errors="replace")
                    chunk_sources.append(source)
                    manifest = runtime_manifest(source)
                    if manifest:
                        manifests[url] = manifest
            for manifest in manifests.values():
                startup = chunk_urls(manifest, set(manifest["startupIds"]))
                queue.extendleft(reversed(startup))
            for source in chunk_sources:
                if "__webpack_require__.u=" not in source:
                    for manifest in manifests.values():
                        queue.extend(chunk_urls(manifest, set(re.findall(r'\.e\((\d+)\)', source))))
        with ThreadPoolExecutor(max_workers=max(1, min(args.workers, 16))) as executor:
            while queue and len(seen) < args.max_assets:
                batch = []
                while queue and len(batch) < args.workers and len(seen) < args.max_assets:
                    url = queue.popleft()
                    if url not in seen:
                        seen.add(url)
                        batch.append(url)
                for url, (body, mime, error) in zip(batch, executor.map(fetch_static, batch)):
                    if error is not None:
                        failures.append({"url": url, "error": error})
                        print(f"FAILED {url}: {error}", file=sys.stderr, flush=True)
                        continue
                    target = local_path(args.out, url)
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(body)
                    downloaded.append({"url": url, "path": target.relative_to(args.out).as_posix(),
                                       "bytes": len(body), "sha256": hashlib.sha256(body).hexdigest()})
                    queue.extend(dependencies(url, body, mime))
                    if target.suffix == ".js":
                        source = body.decode("utf-8", errors="replace")
                        chunk_sources.append(source)
                        manifest = None
                        try:
                            manifest = runtime_manifest(source)
                            if manifest:
                                manifests[url] = manifest
                                queue.extend(chunk_urls(manifest, set(manifest["startupIds"])))
                        except ValueError as exc:
                            failures.append({"url": url, "error": str(exc), "kind": "runtime_manifest"})
                        # Nested async imports in selected chunks also need their
                        # shared chunks. Entry runtime itself contains all routes,
                        # so deliberately do not follow all imports from it.
                        if not manifest:
                            ids = set(re.findall(r'\.e\((\d+)\)', source))
                            for runtime in manifests.values():
                                queue.extend(chunk_urls(runtime, ids))
                    print(f"DOWNLOADED {len(downloaded)} {url}", flush=True)
        report["runtimeManifests"] = [{"url": url, "startupChunks": [
            {"id": key, "name": m["names"].get(key, key), "js": m["js"].get(key), "css": m["css"].get(key)}
            for key in m["startupIds"]]} for url, m in manifests.items()]
        report["missingStartupResources"] = [url for manifest in manifests.values()
            for url in chunk_urls(manifest, set(manifest["startupIds"]))
            if not local_path(args.out, url).is_file()]
        index = local_path(args.out, "https://www.educoder.net/")
        if index.is_file():
            report["entrypointVersions"] = re.findall(r'(?<=[/"\'])umi\.[a-f0-9]+\.(?:js|css)', index.read_text(encoding="utf-8", errors="replace"))
        report["pendingUrls"] = sorted(set(queue) - seen)
        report["downloaded"], report["failures"] = downloaded, failures
        report["downloadedCount"] = len(downloaded)
        report["downloadedBytes"] = sum(item["bytes"] for item in downloaded)
        report["limitations"].append("Startup routes and statically discoverable dependencies only; no API responses, account session, WebSocket or full-site route crawl.")
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"STATIC_RESOURCES={report['recordedStaticCount']}")
    print(f"REPORT={args.report.resolve()}")
    return 1 if report.get("failures") else 0


if __name__ == "__main__":
    raise SystemExit(main())

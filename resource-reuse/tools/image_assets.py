"""Mirror verified public image dependencies without replaying HAR credentials.

Reads only image references from public configuration, static files and optional
HAR response JSON. Does not copy API response bodies, cookies or request headers.
Extensionless image URLs and cache timestamp queries are supported. Existing
files are verified and preserved. No executable frontend resources are modified.
"""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
from urllib.parse import parse_qsl, urljoin, urlsplit, urlunsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener
import xml.etree.ElementTree as ET

HOSTS = {"www.educoder.net", "www-cdn.educoder.net", "ali-cdn.educoder.net",
         "data.educoder.net", "public.educoder.net"}
PUBLIC_ATTACHMENT_HOST = "prod-attachments.oss-accelerate.aliyuncs.com"
IMAGE_EXTENSIONS = {".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico", ".avif", ".bmp"}


def image_type(body: bytes) -> str:
    if body.startswith(b"\x89PNG\r\n\x1a\n"):
        return "image/png"
    if body.startswith(b"\xff\xd8\xff"):
        return "image/jpeg"
    if body.startswith((b"GIF87a", b"GIF89a")):
        return "image/gif"
    if body[:4] == b"RIFF" and body[8:12] == b"WEBP":
        return "image/webp"
    if body[:4] == b"\x00\x00\x01\x00":
        return "image/x-icon"
    if body.startswith(b"BM"):
        return "image/bmp"
    if body[4:8] == b"ftyp" and body[8:12] in {b"avif", b"avis"}:
        return "image/avif"
    head = body[:1024].lstrip(b"\xef\xbb\xbf\r\n\t ")
    if head.startswith((b"<svg", b"<?xml", b"<!--")):
        try:
            root = ET.fromstring(body)
            if root.tag.rsplit("}", 1)[-1] == "svg":
                return "image/svg+xml"
        except ET.ParseError:
            pass
    raise ValueError("Response is not a recognized image (binary/SVG validation)")


def normalize_url(value: str) -> str | None:
    value = value.replace("\\/", "/")
    if value.startswith(("images/", "/images/")):
        value = "https://data.educoder.net/" + value.lstrip("/")
    parts = urlsplit(value)
    if (parts.scheme != "https" or parts.hostname not in HOSTS or parts.username
            or parts.password or parts.port not in (None, 443)):
        return None
    # Only non-sensitive cache/version parameters are accepted, never signed URLs.
    if any(key not in {"t", "v", "version"} for key, _ in parse_qsl(parts.query)):
        return None
    path = re.sub(r"/+", "/", parts.path)
    if not path or any(p in {".", ".."} for p in path.split("/")) or any(c in path for c in ("\\", "%", ":")):
        return None
    if path.startswith("/api/"):
        return None
    if (PurePosixPath(path).suffix.lower() not in IMAGE_EXTENSIONS
            and not path.startswith(("/images/", "/PortalImage/", "/HomeDiscipline/"))):
        return None
    return urlunsplit(("https", parts.hostname, path, parts.query, ""))


class ImageRedirects(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if normalize_url(newurl) is None:
            raise ValueError("Image redirect leaves allowed public image URLs")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def fetch(url: str) -> tuple[bytes, str]:
    opener = build_opener(ImageRedirects())
    request = Request(url, headers={"User-Agent": "Mozilla/5.0 (PublicImageMirror/1.0)",
                                   "Referer": "https://www.educoder.net/",
                                   "Accept-Encoding": "identity"})
    with opener.open(request, timeout=25) as response:
        body = response.read(16 * 1024 * 1024 + 1)
        if len(body) > 16 * 1024 * 1024:
            raise ValueError("Image exceeds 16 MiB")
        return body, response.headers.get("Content-Type", "")


def image_strings(value):
    if isinstance(value, dict):
        for item in value.values():
            yield from image_strings(item)
    elif isinstance(value, list):
        for item in value:
            yield from image_strings(item)
    elif isinstance(value, str):
        url = normalize_url(value)
        if url:
            yield url


def public_config_attachments(value):
    """Only discover concrete image paths published by anonymous public config.

    Signed query values stay in memory for the GET and never enter a report.
    This is intentionally not called for HAR response bodies.
    """
    if isinstance(value, dict):
        for item in value.values():
            yield from public_config_attachments(item)
    elif isinstance(value, list):
        for item in value:
            yield from public_config_attachments(item)
    elif isinstance(value, str) and value.startswith("https://" + PUBLIC_ATTACHMENT_HOST + "/"):
        parts = urlsplit(value)
        if (not parts.username and not parts.password and parts.port in (None, 443)
                and re.fullmatch(r"/\d{4}/\d{2}/[a-fA-F0-9_]+\.(?:png|jpe?g|webp)", parts.path)):
            yield value


def discover(original: Path, har: Path | None) -> dict[str, dict]:
    urls: dict[str, dict] = {}

    def add(value, source):
        url = normalize_url(value)
        if not url:
            return
        parts = urlsplit(url)
        path = str(parts.hostname) + parts.path
        if path not in urls:
            urls[path] = {"url": url, "path": path, "sources": []}
        if source not in urls[path]["sources"]:
            urls[path]["sources"].append(source)
        # IMG_SERVER is the ali CDN, while setting.public_bucket_host is data.
        # Both are genuine references used by different existing components.
        if parts.hostname == "data.educoder.net" and parts.path.startswith("/images/avatars/"):
            add(url.replace("https://data.educoder.net/", "https://ali-cdn.educoder.net/", 1), source)

    for endpoint in ("/api/setting.json", "/api/home/index.json"):
        request = Request("https://data.educoder.net" + endpoint,
                          headers={"User-Agent": "Mozilla/5.0", "Accept-Encoding": "identity"})
        with build_opener().open(request, timeout=25) as response:
            config = json.load(response)
        for url in image_strings(config):
            add(url, "public-config:" + endpoint)
        for fetch_url in public_config_attachments(config):
            parts = urlsplit(fetch_url)
            path = PUBLIC_ATTACHMENT_HOST + parts.path
            urls[path] = {"url": urlunsplit((parts.scheme, parts.hostname, parts.path, "", "")),
                          "_fetchUrl": fetch_url, "path": path, "queryRedacted": True,
                          "sources": ["public-config:" + endpoint]}
    if har:
        log = json.loads(har.read_text(encoding="utf-8-sig"))
        for entry in log["log"]["entries"]:
            content = entry.get("response", {}).get("content", {})
            request = entry.get("request", {})
            if str(content.get("mimeType", "")).startswith("image/"):
                add(request.get("url", ""), "har-image")
            text = content.get("text", "")
            if content.get("encoding") or not text or "json" not in content.get("mimeType", ""):
                continue
            try:
                for url in image_strings(json.loads(text)):
                    add(url, "har-response-image-reference")
            except json.JSONDecodeError:
                continue
    for file in sorted(original.rglob("*")):
        if not file.is_file() or file.suffix not in {".js", ".css", ".html"}:
            continue
        text = file.read_text(encoding="utf-8", errors="replace")
        relative = file.relative_to(original).as_posix()
        host, _, path = relative.partition("/")
        base = "https://" + host + "/" + path
        values = re.findall(r'''url\(\s*["']?([^\s)"']+)["']?\s*\)''', text, flags=re.I)
        values += re.findall(r'''(?:src|href)\s*=\s*["']([^"']+)["']''', text, flags=re.I)
        values += re.findall(r'''https://(?:www-cdn|ali-cdn|data|public)\.educoder\.net/[^\s"'<>`\\]+''', text)
        values += re.findall(r'''\.p\s*\+\s*["']((?:static|assets)/[^"']+)["']''', text)
        for value in values:
            if not any(marker in value for marker in ("${", "{", "}")):
                add(urljoin(base, value), "static:" + relative)
    return urls


def mirror(item, original: Path, site: Path):
    item = dict(item)
    fetch_url = item.pop("_fetchUrl", item["url"])
    source = original / Path(*PurePosixPath(item["path"]).parts)
    deployed = site / Path(*PurePosixPath(item["path"]).parts)
    try:
        if source.exists():
            body, upstream_type = source.read_bytes(), "preserved-existing-file"
            item["status"] = "existing"
        else:
            body, upstream_type = fetch(fetch_url)
            item["status"] = "downloaded"
        detected = image_type(body)
        if not source.exists():
            source.parent.mkdir(parents=True, exist_ok=True)
            source.write_bytes(body)
        if deployed.exists():
            if deployed.read_bytes() != body:
                raise ValueError("Existing deployed image differs from preserved original")
        else:
            deployed.parent.mkdir(parents=True, exist_ok=True)
            deployed.write_bytes(body)
        # Reopen both claimed artifacts to verify written bytes and image format.
        for file in (source, deployed):
            if file.read_bytes() != body or image_type(file.read_bytes()) != detected:
                raise ValueError("Image byte verification failed")
        item.update(bytes=len(body), sha256=hashlib.sha256(body).hexdigest(),
                    detectedContentType=detected, upstreamContentType=upstream_type)
        return item, None
    except Exception as exc:
        return None, {"url": item["url"], "path": item["path"], "error": str(exc)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--original", type=Path, default=Path("resource-reuse/original"))
    parser.add_argument("--site", type=Path, default=Path("resource-reuse/site"))
    parser.add_argument("--har", type=Path)
    parser.add_argument("--report", type=Path, default=Path("resource-reuse/reports/images-download.json"))
    parser.add_argument("--workers", type=int, default=8)
    parser.add_argument("--inventory-only", action="store_true")
    args = parser.parse_args()
    items = discover(args.original, args.har)
    if args.inventory_only:
        report = {"imageCount": len(items), "images": [{k: v for k, v in item.items() if k != "_fetchUrl"}
                                                      for item in items.values()]}
    else:
        with ThreadPoolExecutor(max_workers=max(1, min(args.workers, 12))) as executor:
            results = list(executor.map(lambda item: mirror(item, args.original, args.site), items.values()))
        images = [image for image, _ in results if image]
        failures = [failure for _, failure in results if failure]
        report = {"imageCount": len(items), "downloaded": sum(i["status"] == "downloaded" for i in images),
                  "existing": sum(i["status"] == "existing" for i in images),
                  "verified": len(images), "images": images, "failures": failures,
                  "rootCauses": ["Original downloader excludes extensionless and query-bearing images",
                                 "API response image references are not JS/CSS dependencies",
                                 "ali-cdn image requests require the official Referer header",
                                 "Some upstream image Content-Type values are text/plain or application/octet-stream"],
                  "queryStorage": "Cache/version query does not change the mirrored host/path; image bytes are verified",
                  "excluded": "Unlisted third-party URLs, HAR signed attachment URLs, API attachment URLs and non-image bodies",
                  "publicAttachmentPolicy": "Only image host/path references from the current anonymous public configuration; signed query stays in memory and is redacted from reports"}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    reopened = json.loads(args.report.read_text(encoding="utf-8"))
    print("IMAGE_CANDIDATES=" + str(reopened["imageCount"]))
    for key in ("downloaded", "existing", "verified"):
        if key in reopened:
            print("IMAGES_" + key.upper() + "=" + str(reopened[key]))
    print("IMAGE_FAILURES=" + str(len(reopened.get("failures", []))))
    print("REPORT=" + str(args.report.resolve()))


if __name__ == "__main__":
    main()

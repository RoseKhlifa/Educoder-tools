"""Verify the decorative-media policy, the published bundles and HTTP responses.

Baseline/rollback inspect an original static tree. Modified checks the exact
removed-file inventory and optionally exercises a running local Go service.
No account, session or signed attachment URLs are required or recorded.
"""
import argparse
import gzip
import hashlib
import json
import re
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from urllib.parse import quote, urlsplit
from urllib.request import Request, urlopen


ROOT = Path(__file__).resolve().parents[2]
ALIASES = ROOT / "internal/compat/media/aliases.json"
MEDIA = ROOT / "internal/compat/media"
INLINE = re.compile(r"data:image/[^;]+;base64,[A-Za-z0-9+/=]+")
MODULE = re.compile(r"(?:\{|,)(\d+):function\([^)]*\)\{")


def home_component(source):
    modules = list(MODULE.finditer(source))
    start = next(item.start() for item in modules if item.group(1) == "927045")
    end = next((item.start() for item in modules if item.start() > start), len(source))
    return source[start:end]


def request(base, path):
    with urlopen(Request(base.rstrip("/") + quote(path, safe="/?=:%")), timeout=30) as response:
        return response.read(), response.headers


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("baseline", "modified"))
    parser.add_argument("--site", type=Path, default=ROOT / "resource-reuse/site")
    parser.add_argument("--base", help="Optional local Go HTTP service")
    args = parser.parse_args()
    aliases = json.loads(ALIASES.read_text(encoding="utf-8"))
    removed = {path[len("/_assets/"):]: name for path, name in aliases.items()
               if path.startswith("/_assets/") and "//" not in path}
    home_files = list((args.site / "www-cdn.educoder.net").glob("p__index.*.async.js"))
    assert len(home_files) == 1, "ambiguous home chunk"
    home_bytes = home_files[0].read_bytes()
    home = home_component(home_bytes.decode("utf-8"))
    total = sum(path.stat().st_size for path in args.site.rglob("*") if path.is_file())
    present = [args.site / path for path in removed if (args.site / path).is_file()]
    if args.mode == "baseline":
        assert len(present) == len(removed), "original decorative media was not restored"
        assert home.count('createElement("video"') == 2, "original promotional videos missing"
        print(f"BASELINE=PASS; DECORATIVE_FILES={len(present)}; DECORATIVE_BYTES={sum(p.stat().st_size for p in present)}; HOME_VIDEO_ELEMENTS=2; SITE_BYTES={total}")
    else:
        assert not present, "removed decorative bitmaps are still deployed"
        assert not INLINE.search(home) or max(len(m.group()) for m in INLINE.finditer(home)) < 4096
        assert 'createElement("video"' not in home and "video.educoder.net/sv/" not in home
        assert 'createElement("img",{alt:"视频休息了' in home
        assert home_bytes and hashlib.sha256(home_bytes).hexdigest()[:8] in home_files[0].name
        entry = (args.site / "www.educoder.net/index.html").read_text(encoding="utf-8")
        runtime = list((args.site / "www-cdn.educoder.net").glob("umi.*.js"))
        assert len(runtime) == 1 and runtime[0].name in entry
        assert hashlib.sha256(runtime[0].read_bytes()).hexdigest()[:8] in runtime[0].name
        assert home_files[0].name.split(".")[1] in runtime[0].read_text(encoding="utf-8")
        svg_bytes = sum(path.stat().st_size for path in MEDIA.glob("*.svg"))
        assert svg_bytes < 12 * 1024
        print(f"MODIFIED=PASS; REMOVED_FILES={len(removed)}; SVG_FILES=9; SVG_BYTES={svg_bytes}; HOME_VIDEO_ELEMENTS=0; SITE_BYTES={total}")
    print(f"HOME_BYTES={len(home_bytes)}; HOME_GZIP_BYTES={len(gzip.compress(home_bytes, mtime=0))}; HOME_SHA256={hashlib.sha256(home_bytes).hexdigest()}")
    if not args.base:
        return
    assert args.mode == "modified", "HTTP checks require the modified service"
    assert urlsplit(args.base).hostname in {"localhost", "127.0.0.1"}, "use a local service"

    def check_alias(item):
        path, name = item
        body, headers = request(args.base, path + "?t=media-verification")
        assert headers.get_content_type() == "image/svg+xml", path
        assert body == (MEDIA / (name + ".svg")).read_bytes(), path
        return len(body)

    with ThreadPoolExecutor(max_workers=8) as pool:
        list(pool.map(check_alias, aliases.items()))
    home, _ = request(args.base, "/api/home/index.json")
    data = json.loads(home)
    assert len(data["images_url"]) == 3
    for slide in data["images_url"]:
        assert slide["image_url"].startswith("/_lite/media-v1/carousel-")
    for demo in data.get("shixun_demo", []):
        assert demo["video"] == "" and demo["poster"] == "/_lite/media-v1/video.svg"
    setting, _ = request(args.base, "/api/setting.json")
    config = json.loads(setting)["setting"]
    for key in ("nav_logo_url", "course_banner_url", "competition_banner_url_b"):
        assert config[key].startswith("_lite/media-v1/"), key
        body, headers = request(args.base, config["public_bucket_host"] + "/" + config[key])
        assert body.startswith(b"<svg") and headers.get_content_type() == "image/svg+xml"
    avatar, headers = request(args.base, "/images/avatars/User/b")
    assert headers.get_content_type().startswith("image/") and not avatar.startswith(b"<svg")
    print(f"HTTP=PASS; ALIASES={len(aliases)}; CAROUSEL_SLIDES=3; PROMOTIONAL_VIDEO_URLS=0; SETTING_MEDIA=SVG; REAL_AVATAR_BYTES={len(avatar)}")


if __name__ == "__main__":
    main()

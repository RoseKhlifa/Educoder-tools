"""Apply the v1 decorative-media policy to the original compiled home resources.

This patches the existing home component, not the task editor or attachment
player. Changed files receive content hashes and the original webpack runtime
and HTML entrypoint are updated together to bypass immutable browser caches.
Run against a copied deployment tree; published versions remain rollback copies.
"""
import argparse
import base64
import hashlib
import json
import re
import struct
from pathlib import Path


MEDIA = "/_lite/media-v1/"
MARKER = "/* educoder decorative media lite v1 */"
INLINE_IMAGE = re.compile(r"data:image/[^;]+;base64,[A-Za-z0-9+/=]+")
MODULE = re.compile(r"(?:\{|,)(\d+):function\([^)]*\)\{")


def replace_once(text, before, after):
    if text.count(before) != 1:
        raise ValueError(f"Expected one original signature: {before[:90]}")
    return text.replace(before, after, 1)


def lightweight_inline(match):
    value = match.group()
    if len(value) < 4096:
        return value
    # Wide home section headings use the banner canvas; other decorations use
    # the generic cover. Small UI icons and all inline fonts retain their bytes.
    raw = base64.b64decode(value.split(",", 1)[1])
    if raw.startswith(b"\x89PNG\r\n\x1a\n"):
        width, height = struct.unpack(">II", raw[16:24])
        if width > height * 3:
            return MEDIA + "banner.svg"
    return MEDIA + "cover.svg"


def patch_home(text):
    if MARKER in text:
        return text
    modules = list(MODULE.finditer(text))
    target = next((m for m in modules if m.group(1) == "927045"), None)
    if target is None:
        raise ValueError("Original compiled home module 927045 was not found")
    start = target.start()
    end = next((m.start() for m in modules if m.start() > start), len(text))
    home = text[start:end]
    image_loader_start = home.index('(0,e.useEffect)(()=>{var Jr,hi;(hi=(Jr=Oa.current)')
    image_loader_end = home.index('},[ve])', image_loader_start) + len('},[ve])')
    image_loader = home[image_loader_start:image_loader_end]
    if 'document.querySelectorAll("img[data-src]")' not in image_loader:
        raise ValueError("Original home image lazy loader was not found")
    home = INLINE_IMAGE.sub(lightweight_inline, home)
    home, count = re.subn(r'video:"https://video\.educoder\.net/sv/[^"\s]+\.mp4"', 'video:""', home)
    if count != 5:
        raise ValueError(f"Expected five promotional fallback videos, found {count}")

    image_props = 'alt:"视频休息了，脑细胞请开工",loading:"lazy",decoding:"async",style:{width:"100%",height:"auto",clipPath:"none",marginTop:0,display:"block"},src:'
    home = replace_once(
        home,
        '$.createElement("video",{preload:"metadata",id:`video-${Jr==null?void 0:Jr.clazz}`,onEnded:()=>{},controls:!0,controlsList:"nodownload",muted:!0,src:Jr.clazz===Vs?Jr==null?void 0:Jr.video:""})',
        '$.createElement("img",{' + image_props + '(Jr==null?void 0:Jr.poster)||"' + MEDIA + 'video.svg"})',
    )
    home = replace_once(
        home,
        'e.createElement("video",{autoPlay:!0,preload:"auto",id:`video-${zr==null?void 0:zr.clazz}`,onEnded:()=>{if(zr.clazz===Jr){if((zr==null?void 0:zr.position)===(Rn==null?void 0:Rn.length)){es((Rn==null?void 0:Rn[0])||{});return}es(Rn[zr==null?void 0:zr.position])}},controls:!0,controlsList:"nodownload",muted:!0,src:zr==null?void 0:zr.video})',
        'e.createElement("img",{' + image_props + '(zr==null?void 0:zr.poster)||"' + MEDIA + 'video.svg"})',
    )
    home, count = re.subn(
        r'const Ps=Jr=>\{const hi=document\.getElementById\(`video-.*?\},ao=Jr=>\{.*?\},Xs=Jr=>',
        'const Ps=Jr=>{},ao=Jr=>{_r(Jr)},Xs=Jr=>', home, count=1,
    )
    if count != 1:
        raise ValueError("Original home video tab handler was not found")
    video_observer_start = '(0,e.useEffect)(()=>{var Jr,hi;(hi=(Jr=dn.current)==null?void 0:Jr.disconnect)==null||hi.call(Jr),rn.current=!1;'
    home, count = re.subn(
        re.escape(video_observer_start) + r'const es=document\.getElementById\(`video-\$\{Vs\}`\).*?\},\[Vs,zn\]\);',
        '(0,e.useEffect)(()=>{},[Vs,zn]);', home, count=1,
    )
    if count != 1:
        raise ValueError("Original home promotional video observer was not found")
    home, count = re.subn(
        r'es=zr=>\{var \$n,Oi,mi,yi;ys\(zr\).*?os\.current\.play\(\)\},500\)\)\},la=zr=>',
        'es=zr=>{ys(zr)},la=zr=>', home, count=1,
    )
    if count != 1:
        raise ValueError("Original alternate home video tab handler was not found")
    if home.count(image_loader) != 1:
        raise ValueError("Home image lazy loader must remain byte-for-byte unchanged")
    if 'createElement("video"' in home or 'video.educoder.net/sv/' in home:
        raise ValueError("A promotional video still remains in the home component")
    return text[:start] + home + text[end:] + "\n" + MARKER + "\n"


def fingerprint(path, content, report):
    before = path.read_bytes()
    after = content.encode("utf-8")
    if before == after:
        return None
    old_hash = re.search(r"\.([0-9a-f]{8})\.", path.name)
    if old_hash is None:
        raise ValueError(f"Expected a hashed compiled filename: {path.name}")
    new_hash = hashlib.sha256(after).hexdigest()[:8]
    name = path.name[:old_hash.start(1)] + new_hash + path.name[old_hash.end(1):]
    new_path = path.with_name(name)
    if new_path.exists() and new_path != path:
        raise ValueError(f"Fingerprint destination exists: {new_path}")
    new_path.write_bytes(after)
    if new_path != path:
        path.unlink()
    report.append({"old": str(path), "new": str(new_path), "before_bytes": len(before),
                   "after_bytes": len(after), "before_sha256": hashlib.sha256(before).hexdigest(),
                   "after_sha256": hashlib.sha256(after).hexdigest()})
    return old_hash.group(1), new_hash


def replace_aliases(content, aliases):
    for old, new in aliases.items():
        # Compiled JS uses webpack publicPath + "static/name.png";
        # CSS uses ./static/name.png. Keep those expressions valid by
        # changing the whole URL expression rather than its basename.
        prefix = "/_assets/www-cdn.educoder.net/"
        if old.startswith(prefix):
            relative = old[len(prefix):]
            content = re.sub(r'\b[A-Za-z_$][\w$]*\.p\+"' + re.escape(relative) + r'"', json.dumps(new), content)
            content = content.replace("./" + relative, new)
        content = content.replace(old, new)
    return content


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--site", type=Path, default=Path(__file__).resolve().parents[1] / "site")
    parser.add_argument("--aliases", type=Path, help="Optional JSON object mapping asset URL paths to SVG URL paths")
    parser.add_argument("--report", type=Path)
    args = parser.parse_args()
    site = args.site.resolve()
    cdn = site / "www-cdn.educoder.net"
    entry = site / "www.educoder.net/index.html"
    report = []
    replacements = {}
    aliases = json.loads(args.aliases.read_text(encoding="utf-8")) if args.aliases else {}
    if not isinstance(aliases, dict):
        raise ValueError("--aliases must contain a JSON object")

    # Change only the original promotional surfaces and global shell styles.
    patterns = ["p__index.*.async.js", "p__index.*.chunk.css", "layouts__index.*.async.js",
                "layouts__SimpleLayouts.*.async.js", "layouts__index.*.chunk.css",
                "layouts__SimpleLayouts.*.chunk.css", "umi.*.css"]
    for pattern in patterns:
        paths = list(cdn.glob(pattern))
        if len(paths) > 1:
            raise ValueError(f"Ambiguous deployment files for {pattern}")
        for path in paths:
            content = path.read_text(encoding="utf-8")
            if pattern == "p__index.*.async.js":
                content = patch_home(content)
            if path.suffix == ".css":
                content = INLINE_IMAGE.sub(lightweight_inline, content)
            content = replace_aliases(content, aliases)
            result = fingerprint(path, content, report)
            if result:
                replacements[result[0]] = result[1]

    runtime_paths = list(cdn.glob("umi.*.js"))
    if len(runtime_paths) != 1:
        raise ValueError("Expected one webpack runtime")
    runtime = runtime_paths[0]
    runtime_content = replace_aliases(runtime.read_text(encoding="utf-8"), aliases)
    for old, new in replacements.items():
        runtime_content = runtime_content.replace('"' + old + '"', '"' + new + '"')
    result = fingerprint(runtime, runtime_content, report)
    if result:
        replacements[result[0]] = result[1]
    html_before = entry.read_bytes()
    html_after = INLINE_IMAGE.sub(lightweight_inline, replace_aliases(html_before.decode("utf-8"), aliases)).encode("utf-8")
    for old, new in replacements.items():
        html_after = html_after.replace(("." + old + ".").encode(), ("." + new + ".").encode())
    if html_after != html_before:
        entry.write_bytes(html_after)
        report.append({"old": str(entry), "new": str(entry), "before_bytes": len(html_before),
                       "after_bytes": len(html_after), "before_sha256": hashlib.sha256(html_before).hexdigest(),
                       "after_sha256": hashlib.sha256(html_after).hexdigest()})
    output = {"changed_files": report, "saved_bytes": sum(r["before_bytes"] - r["after_bytes"] for r in report)}
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(output, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(output, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()

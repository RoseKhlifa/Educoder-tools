"""Make a separate deployment tree with only endpoint/asset-origin replacements.

Original bytes and UI implementation are preserved in --source. A new --dest is
required on every run. Rollback: select the previous release directory in the
Go service configuration. Does not write UI components.
"""
import argparse
import hashlib
import json
import shutil
from pathlib import Path

ORIGINS = {
    b"https://data.educoder.net": b"",  # API paths become same-origin /api/*.
    b"https://www-cdn.educoder.net": b"/_assets/www-cdn.educoder.net",
    b"https://ali-cdn.educoder.net": b"/_assets/ali-cdn.educoder.net",
}
TEXT_EXTENSIONS = {".js", ".mjs", ".css", ".html", ".json", ".webmanifest"}
NETWORK_TIP_FUNCTION = b"window.showNetworkErrorTip =function (error) {"
NETWORK_TIP_DISABLED = NETWORK_TIP_FUNCTION + b"return;/* local deployment: global network banner disabled */"


def disable_network_banner(content: bytes) -> bytes:
    # Keep the original callable hook so request failures still follow the
    # bundle's regular error path, but never prepend the global banner.
    if NETWORK_TIP_DISABLED in content:
        return content
    if content.count(NETWORK_TIP_FUNCTION) != 1:
        raise ValueError("Expected exactly one original network banner hook")
    return content.replace(NETWORK_TIP_FUNCTION, NETWORK_TIP_DISABLED, 1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--dest", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    source, destination = args.source.resolve(), args.dest.resolve()
    if not source.is_dir() or destination.exists():
        parser.error("source must exist; destination must be a new directory")
    if destination.is_relative_to(source) or source.is_relative_to(destination):
        parser.error("source and destination must be separate directory trees")
    shutil.copytree(source, destination)
    changed = []
    for path in sorted(destination.rglob("*")):
        if not path.is_file() or path.suffix.lower() not in TEXT_EXTENSIONS:
            continue
        before = path.read_bytes()
        after = before
        replacements = {}
        for old, new in ORIGINS.items():
            for needle, value in ((old, new), (old.replace(b"/", b"\\/"), new.replace(b"/", b"\\/"))):
                count = after.count(needle)
                if count:
                    replacements[needle.decode()] = count
                    after = after.replace(needle, value)
        if path.relative_to(destination).as_posix() == "www.educoder.net/index.html":
            after = disable_network_banner(after)
            replacements["disable global showNetworkErrorTip banner"] = 1
        if after != before:
            path.write_bytes(after)
            changed.append({"path": path.relative_to(destination).as_posix(),
                            "beforeSha256": hashlib.sha256(before).hexdigest(),
                            "afterSha256": hashlib.sha256(after).hexdigest(),
                            "replacements": replacements})
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps({"source": str(source), "destination": str(destination),
                                       "changedFiles": changed,
                                       "notRewritten": ["WebSocket origins", "API response body URLs", "SRI integrity attributes", "CSP headers", "service-worker caches"],
                                       "rollback": "Set REUSE_STATIC_DIR to the previous deployment tree; original source tree is unchanged."},
                                      indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"CHANGED_FILES={len(changed)}")
    print(f"REPORT={args.report.resolve()}")


if __name__ == "__main__":
    main()

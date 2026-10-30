"""Check downloaded images through the local Go service, including body hashes."""
import argparse
import hashlib
import json
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from urllib.parse import quote, urlsplit
from urllib.request import urlopen

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--base", default="http://127.0.0.1:8081")
parser.add_argument("--manifest", type=Path, default=Path("resource-reuse/reports/images-download.json"))
parser.add_argument("--report", type=Path, default=Path("resource-reuse/reports/banner-images-fix/http-images.json"))
args = parser.parse_args()
if urlsplit(args.base).hostname not in {"127.0.0.1", "localhost"}:
    parser.error("Verification target must be local")
manifest = json.loads(args.manifest.read_text(encoding="utf-8"))
images = [entry for entry in manifest["images"] if entry.get("status") in {"downloaded", "existing"}]
media_dir = Path(__file__).resolve().parents[2] / "internal/compat/media"
aliases = json.loads((media_dir / "aliases.json").read_text(encoding="utf-8"))


def verify(entry):
    path = "/_assets/" + quote(entry["path"].replace("\\", "/"), safe="/")
    result = {"path": path}
    replacement = aliases.get(path)
    expected = hashlib.sha256((media_dir / (replacement + ".svg")).read_bytes()).hexdigest() if replacement else entry["sha256"]
    try:
        with urlopen(args.base.rstrip("/") + path + "?t=verification", timeout=15) as response:
            data, mime, status = response.read(), response.headers.get("Content-Type", ""), response.status
        digest = hashlib.sha256(data).hexdigest()
        if status != 200 or not mime.startswith("image/") or digest != expected:
            raise ValueError(f"HTTP={status}; MIME={mime}; SHA_MATCH={digest == expected}")
        result.update({"result": "PASS", "http": status, "contentType": mime, "bytes": len(data), "sha256": digest})
    except Exception as exc:
        result.update({"result": "FAIL", "error": str(exc)})
    return result


with ThreadPoolExecutor(max_workers=8) as executor:
    results = list(executor.map(verify, images))
failed = [item for item in results if item["result"] != "PASS"]
report = {"result": "FAIL" if failed else "PASS", "tested": len(results), "passed": len(results) - len(failed), "failed": len(failed), "images": results}
args.report.parent.mkdir(parents=True, exist_ok=True)
args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(f"IMAGES_HTTP={report['result']}; TESTED={report['tested']}; PASSED={report['passed']}; FAILED={report['failed']}")
print("IMAGE_MIME_AND_SHA256=" + report["result"])
print(f"REPORT={args.report.resolve()}")
if failed:
    for item in failed:
        print(item["path"], item["error"])
    raise SystemExit(1)

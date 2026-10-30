"""Check raw bundle/deployment bundle address constants without running the UI."""
import argparse
import hashlib
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("mode", choices=("baseline", "modified"))
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
original = root / "original/www-cdn.educoder.net/umi.c7ec315c.js"
entries = list((root / "site/www-cdn.educoder.net").glob("umi.*.js"))
if len(entries) != 1:
    raise ValueError("Expected exactly one deployed runtime")
modified = entries[0]
before, after = original.read_bytes(), modified.read_bytes()
if args.mode == "baseline":
    assert b'https://data.educoder.net' in before
    assert b'https://www-cdn.educoder.net/' in before
    print("BASELINE=PASS; API_ORIGIN=official; PUBLIC_PATH=official")
    print("ORIGINAL_SHA256=" + hashlib.sha256(before).hexdigest())
else:
    assert b'https://data.educoder.net' not in after
    assert b'https://www-cdn.educoder.net/' not in after
    assert b'/_assets/www-cdn.educoder.net/' in after
    assert before != after
    print("MODIFIED=PASS; API_ORIGIN=same-origin; PUBLIC_PATH=local")
    print("MODIFIED_SHA256=" + hashlib.sha256(after).hexdigest())

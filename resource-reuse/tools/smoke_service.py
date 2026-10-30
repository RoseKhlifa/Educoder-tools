"""Verify the self-hosted compiled-resource service using a supplied HAR login.

The login request body is read only in memory and sent to the local service.
No password, Cookie value or raw API response is logged or saved.
"""
import argparse
import hashlib
import http.cookiejar
import json
from pathlib import Path
from urllib.parse import urlencode, urlsplit
from urllib.request import HTTPCookieProcessor, Request, build_opener


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--har", type=Path, required=True)
    parser.add_argument("--base", default="http://127.0.0.1:8081")
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    base = args.base.rstrip("/")
    if urlsplit(base).hostname not in {"127.0.0.1", "localhost"}:
        parser.error("smoke test target must be a local service")
    har = json.loads(args.har.read_text(encoding="utf-8-sig"))
    login_entry = next(e for e in har["log"]["entries"] if e["request"]["method"] == "POST" and urlsplit(e["request"]["url"]).path == "/api/accounts/login.json")
    # Use the captured encrypted password bytes as the original browser does.
    login_body = login_entry["request"]["postData"]["text"].encode("utf-8")
    jar = http.cookiejar.CookieJar()
    opener = build_opener(HTTPCookieProcessor(jar))
    results = []

    def request(path, body=None):
        method = "POST" if body is not None else "GET"
        with opener.open(Request(base + path, data=body, headers={"Content-Type": "application/json"}), timeout=45) as response:
            raw = response.read()
            status = response.status
        results.append({"method": method, "path": path.split("?")[0], "status": status})
        return json.loads(raw)

    assert request("/healthz")["status"] == "ok"
    user = request("/api/accounts/login.json", login_body)
    assert user.get("login") and "data" not in user
    profile = request("/api/users/get_user_info.json?school=1")
    assert profile["login"] == user["login"]
    classes = request(f"/api/users/{user['login']}/courses.json?status=processing&page=1&per_page=15")
    assert len(classes["courses"]) > 0
    first = next(c for c in classes["courses"] if "/NTHPZ75R/" in c.get("first_category_url", ""))
    course = first["first_category_url"].split("/")[2]
    homeworks = request(f"/api/courses/{course}/homework_commons.json?type=4&status=0&limit=20&order=0")
    homework = next(h for h in homeworks["homeworks"] if h["homework_id"] == 3985881)
    shixun = homework["shixun_identifier"]
    execution = request(f"/api//shixuns/{shixun}/shixun_exec.json?" + urlencode({"id": shixun, "homework_common_id": homework["homework_id"]}))
    game = execution["game_identifier"]
    task = request(f"/api/tasks/{game}.json?homework_common_id={homework['homework_id']}")
    assert task["game"]["identifier"] == game
    assert task["challenge"]["subject"]
    myshixun = task["myshixun"]["identifier"]
    repository = request(f"/api/myshixuns/{myshixun}/version_repository_delete.json")
    assert repository.get("status") == 0
    out = {"result": "PASS", "base": base, "harSha256": hashlib.sha256(args.har.read_bytes()).hexdigest(),
           "courses": len(classes["courses"]), "homeworks": len(homeworks["homeworks"]),
           "course": course, "homeworkId": homework["homework_id"], "gameIdentifier": game,
           "challenge": task["challenge"]["subject"], "requests": results,
           "note": "No browser rendering assertion; HTTP assertions only. No credential values stored."}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print("LIVE_SERVICE=PASS")
    print(f"COURSES={out['courses']}")
    print(f"HOMEWORKS={out['homeworks']}")
    print(f"GAME_IDENTIFIER={game}")
    print(f"REQUESTS={len(results)}; ALL_HTTP_200")
    print(f"REPORT={args.report.resolve()}")


if __name__ == "__main__":
    main()

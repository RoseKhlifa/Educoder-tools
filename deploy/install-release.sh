#!/usr/bin/env bash
set -euo pipefail
version="${1:?Usage: install-release.sh VERSION COMMIT}"
commit="${2:?Commit SHA required}"
release_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
case "$version" in
  *[!0-9.]*|'') printf '%s\n' 'Invalid release version' >&2; exit 1 ;;
esac
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || { printf '%s\n' 'Invalid commit SHA' >&2; exit 1; }
[[ "$(tr -d '\r\n' < "$release_root/VERSION")" == "$version" ]] || { printf '%s\n' 'Archive VERSION mismatch' >&2; exit 1; }
command -v docker >/dev/null
command -v curl >/dev/null
command -v python3 >/dev/null
docker compose version >/dev/null
cd -- "$release_root"

# This file is outside every release archive and is mounted read-only. Empty
# credentials keep browsing available while the UI reports the model unavailable.
secret_root=/opt/educoder-tools/secrets
mkdir -p -- "$secret_root"
chmod 700 -- "$secret_root"
export DEEPSEEK_SECRET_FILE="${DEEPSEEK_SECRET_FILE:-$secret_root/deepseek_api_key}"
if [[ ! -e "$DEEPSEEK_SECRET_FILE" ]]; then
  install -m 0400 /dev/null "$DEEPSEEK_SECRET_FILE"
fi
chmod 0400 -- "$DEEPSEEK_SECRET_FILE"

# Build before replacing the live container. A failed build leaves it untouched.
export APP_VERSION="$version" VCS_REF="$commit" WEB_COOKIE_SECURE=true
docker compose --project-name educoder-tools build
docker compose --project-name educoder-tools up -d --no-build
for attempt in $(seq 1 30); do
  if curl --fail --silent --max-time 3 http://127.0.0.1:11454/healthz > /tmp/educoder-tools-health.json; then
    if python3 -c 'import json,sys; d=json.load(open("/tmp/educoder-tools-health.json")); sys.exit(0 if d.get("status")=="ok" and d.get("version")==sys.argv[1] and d.get("commit")==sys.argv[2] else 1)' "$version" "$commit"; then
      printf 'DEPLOYED_VERSION=%s\nDEPLOYED_COMMIT=%s\n' "$version" "$commit"
      docker compose --project-name educoder-tools ps
      exit 0
    fi
  fi
  sleep 2
done
printf '%s\n' 'Health/version verification failed; inspect docker compose logs.' >&2
exit 1

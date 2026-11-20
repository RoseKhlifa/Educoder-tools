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
python3 - <<'PY'
import os, sys
disk = os.statvfs('/opt/educoder-tools')
available = disk.f_bavail * disk.f_frsize
if available < 1024 ** 3:
    sys.exit('Deployment stopped: less than 1 GiB available to the application filesystem.')
print('DISK_PREFLIGHT=PASS')
PY
export APP_VERSION="$version" VCS_REF="$commit" WEB_COOKIE_SECURE=true
# Operator settings live outside immutable release archives, so a later update
# does not silently remove the numeric administrator allowlist or card policy.
compose_args=(--project-name educoder-tools)
service_config=/opt/educoder-tools/config/service.env
if [[ -f "$service_config" ]]; then
  compose_args+=(--env-file "$service_config")
fi
docker compose "${compose_args[@]}" build
# Probe the actual data mount as the image's unprivileged user before replacing
# the live service. Root's reserved disk blocks do not make SQLite writable.
docker compose "${compose_args[@]}" run --rm --no-deps --entrypoint sh app -ec 'probe="$(mktemp /data/.deploy-write.XXXXXX)"; rm -f -- "$probe"'
docker compose "${compose_args[@]}" up -d --no-build

# Bounded retention, run only after a verified deploy so an unattended host
# does not fill its disk with old release trees, pre-deploy backups and images.
# Only this application's own artifacts with exact name patterns are removed,
# newest first by the version or timestamp in the name (never by mtime); the
# release being installed is always kept. Failures only warn: cleanup must
# never turn a verified deploy into a failure.
prune_artifacts() {
  local root="${EDUCODER_TOOLS_ROOT:-/opt/educoder-tools}" keep_releases=10 keep_backups=20 keep_images=5
  local current n entry
  current="$(cd -- "$release_root" && pwd -P)" || return 1
  n=0
  while IFS= read -r entry; do
    n=$((n + 1))
    (( n <= keep_releases )) && continue
    [[ "$(cd -- "$root/releases/$entry" && pwd -P)" == "$current" ]] && continue
    rm -rf -- "${root:?}/releases/$entry"
  done < <(ls -1 -- "$root/releases" 2>/dev/null | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | sort -t. -k1,1nr -k2,2nr -k3,3nr)
  n=0
  while IFS= read -r entry; do
    n=$((n + 1))
    (( n <= keep_backups )) && continue
    rm -rf -- "${root:?}/backups/$entry"
  done < <(ls -1 -- "$root/backups" 2>/dev/null | grep -E '^pre-v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{8}T[0-9]{6}Z$' | sort -t- -k3,3r)
  if [[ -z "${EDUCODER_TOOLS_SKIP_IMAGE_PRUNE:-}" ]]; then
    n=0
    while IFS= read -r entry; do
      [[ "$entry" == "$version" ]] && continue
      n=$((n + 1))
      (( n < keep_images )) && continue
      docker image rm -- "educoder-tools:$entry" >/dev/null 2>&1 || true
    done < <(docker images --format '{{.Tag}}' educoder-tools 2>/dev/null | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | sort -t. -k1,1nr -k2,2nr -k3,3nr)
  fi
}

for attempt in $(seq 1 30); do
  if curl --fail --silent --max-time 3 http://127.0.0.1:11454/healthz > /tmp/educoder-tools-health.json; then
    if python3 -c 'import json,sys; d=json.load(open("/tmp/educoder-tools-health.json")); sys.exit(0 if d.get("status")=="ok" and d.get("version")==sys.argv[1] and d.get("commit")==sys.argv[2] else 1)' "$version" "$commit"; then
      printf 'DEPLOYED_VERSION=%s\nDEPLOYED_COMMIT=%s\n' "$version" "$commit"
      docker compose "${compose_args[@]}" ps
      prune_artifacts || printf '%s\n' 'Warning: artifact retention did not complete.' >&2
      exit 0
    fi
  fi
  sleep 2
done
printf '%s\n' 'Health/version verification failed; inspect docker compose logs.' >&2
exit 1

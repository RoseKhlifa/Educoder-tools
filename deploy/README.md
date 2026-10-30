# Server deployment

The application listens on `11454` and serves both original compiled resources and compatible API handlers. The deployment does not install or change any reverse proxy. In 1Panel, point `touge.mom` to the application's HTTP port `11454`, and terminate HTTPS in 1Panel.

## Docker

Build and start a clean checkout:

```bash
export APP_VERSION="$(cat VERSION)"
export VCS_REF="$(git rev-parse HEAD)"
docker compose --project-name educoder-tools build
docker compose --project-name educoder-tools up -d --no-build
curl --fail http://127.0.0.1:11454/healthz
```

Compose uses a versioned image, a non-root user, a read-only filesystem, automatic restart and bounded logs. The server only needs the Go executable, CA certificates and `resource-reuse/site`. Node.js, HAR recordings, local verification reports and account credentials are not shipped.

The active server is accessed through `ssh main`. Deployment archives are created with `git archive` from a published commit, so ignored local recordings and secrets never enter the archive. Releases live under `/opt/educoder-tools/releases/<version>`; the Compose project name stays `educoder-tools` across releases.

When deploying a prepared archive:

```bash
bash deploy/install-release.sh VERSION FULL_COMMIT_SHA
```

The version and commit arguments must exactly identify the published archive. `/healthz` and `/version` expose the running version and commit. Login sessions are in memory and expire across service replacements.

User and teacher avatars are fetched by a dedicated fixed-source image reader with an in-memory cache. They are not baked into the image or persisted in the repository/container filesystem; runtime HTTPS access to the official avatar source is required. Responses use verified image MIME types and private/no-store browser caching.

## Rollback

Keep the previous release directory and Docker image. From that directory, set its `APP_VERSION` and `VCS_REF`, then run:

```bash
docker compose --project-name educoder-tools up -d --no-build
curl --fail http://127.0.0.1:11454/healthz
```

No external account or session data is stored in the repository or image. `REUSE_COOKIE_SECURE=true` is intended for HTTPS through `touge.mom`; local plain-HTTP testing can override it explicitly.

## Small releases

`VERSION` is the source of truth. Every completed small change gets a patch increment (`0.1.1` to `0.1.2`), a changelog entry, validation, one commit, an annotated tag and an SSH push. Do not rewrite published tags.

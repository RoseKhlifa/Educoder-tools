# Educoder HAR protocol reproduction

Current Web entry: `go run ./cmd/reuse`, then open [http://127.0.0.1:11454/](http://127.0.0.1:11454/). This single Go service hosts the downloaded original compiled UI and serves compatible `/api/*.json` application routes. It does not use Nginx, a gateway, or browser request interception. See [resource-reuse/README.md](resource-reuse/README.md) for the active architecture and [deploy/README.md](deploy/README.md) for Docker deployment. The earlier hand-written `web/` UI is historical and is not used by `cmd/reuse`.

The service version comes from `VERSION`; `/healthz` and `/version` report the running version and commit. Each completed small change gets a new patch version, changelog entry, validated commit, annotated tag and SSH push:

```powershell
.\scripts\release.ps1 -Message 'Describe the completed change'
```

Before publishing, the script checks the staged files for local HAR account/session values, private-key material and forbidden private/generated paths. HAR recordings, credentials, cookies, reports and logs remain local and are not included in Docker build contexts or Git deployment archives.

This workspace contains a terminal-first Go and TypeScript implementation of the minimum authenticated chain observed in `educoder_from_login_to_get_homework.har`.

## Protocol extracted from the HAR

1. `POST https://data.educoder.net/api/accounts/login.json` with JSON `{login, password, autologin:true}`. The login popup encrypts the trimmed password with AES-128-CBC, PKCS#7, UTF-8 key and IV `5183666c72eec9e4`, then sends one standard Base64 encoding.
2. Save the two `Set-Cookie` values. Every later data request sends `_educoder_session` again as `Pc-Authorization`.
3. Add these dynamic headers to each data request: `X-EDU-Type: pc`, millisecond `X-EDU-Timestamp`, `X-EDU-Signature`, `X-Original-*`, and a UUID `X-Request-Id`. The signature is `MD5(Base64(UTF8("method=" + uppercase(METHOD) + "&ak=" + AK + "&sk=" + SK + "&time=" + timestamp)))`.
4. `GET /api/users/{login}/courses.json?...` returns classrooms. The classroom route identifier is parsed from `courses[].first_category_url`, for example `/classrooms/NTHPZ75R/announcement` -> `NTHPZ75R`.
5. `GET /api/courses/{identifier}/homework_commons.json?limit=20&status=0&id={identifier}&type=4&order=0` returns `homeworks[]`; each item supplies `homework_id` and `shixun_identifier`.
6. `GET /api/shixuns/{shixun_identifier}/shixun_exec.json?id=...&homework_common_id=...` returns `game_identifier`.
7. `GET /api/tasks/{game_identifier}.json?homework_common_id=...` returns the task/challenge detail.

The HAR also contains browser assets, OPTIONS requests, cache replays, images, and unrelated Chrome autofill requests. They are not required for this minimum chain.

## Go CLI (terminal first)

```powershell
go test ./...
$env:EDUCODER_LOGIN = 'your-login'
$env:EDUCODER_PASSWORD = 'your-password'
go run ./cmd/educoder login --trace
go run ./cmd/educoder courses --trace
go run ./cmd/educoder tasks --course NTHPZ75R --trace
go run ./cmd/educoder enter --course NTHPZ75R --homework 3985881 --trace
# End-to-end listing; with --course and --homework, flow enters the selected task.
go run ./cmd/educoder flow --trace
go run ./cmd/educoder flow --course NTHPZ75R --homework 3985881 --trace
```

`--password-stdin` is available when the password should not appear in a command argument:

```powershell
'password-from-prompt' | go run ./cmd/educoder login --login your-login --password-stdin
```

The client keeps credentials, cookies, and tokens in memory only. It never prints password, cookies, or authorization headers. `--trace` prints only method, path, and status.

## Go Web application

The browser application uses the same EduCoder protocol client through a server-side session adapter. Build the TypeScript bundle and start the Go server from the repository root:

```powershell
npm run web:check
go run ./cmd/web
```

Open [http://localhost:8080/](http://localhost:8080/). The web flow is:

```text
登录 -> 我的课堂 -> 选择课堂 -> 任务列表 -> 任务详情 -> 打开官方实训页面
```

The Go server keeps the upstream EduCoder client and cookies in an in-memory session keyed by an HttpOnly `educoder_web_session` cookie. Browser responses contain reduced DTOs only. The UI follows the current EduCoder information architecture: blue top navigation, classroom rail, classroom cards, homework list, status pills, search, and a task detail drawer. The official task page opens in a new tab so the student completes the exercise there.

Web API routes:

```text
POST /api/auth/login
GET  /api/auth/me
POST /api/auth/logout
GET  /api/courses
GET  /api/courses/:identifier/homeworks
GET  /api/courses/:identifier/homeworks/:homeworkId/enter
GET  /healthz
```

Configuration is available through `HTTP_ADDR`, `WEB_ORIGIN`, `EDUCODER_API_BASE_URL`, `SESSION_TTL`, `WEB_STATIC_DIR`, and `WEB_COOKIE_SECURE`. The server does not implement automatic answer entry, automatic submission, or score manipulation.

## TypeScript CLI

The TypeScript CLI has the same commands and environment variables:

```powershell
npm test
$env:EDUCODER_LOGIN = 'your-login'
$env:EDUCODER_PASSWORD = 'your-password'
npm run cli -- login --trace
npm run cli -- courses --trace
npm run cli -- tasks --course NTHPZ75R --trace
npm run cli -- enter --course NTHPZ75R --homework 3985881 --trace
```

## Offline analysis artifacts

- `HAR_ANALYSIS_AGENT.md` is the redacted route/field/order report.
- `protocol-constants.json` contains the public access/signature constants and password algorithm metadata. It does not contain the supplied account password, session cookies, or HAR cookie values.
- `educoder_from_login_to_get_homework.har` is kept unchanged.

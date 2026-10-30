# Educoder HAR protocol reproduction

Current Web entry: `go run ./cmd/reuse`, then open [http://127.0.0.1:11454/](http://127.0.0.1:11454/). This single Go service hosts the downloaded original compiled UI and serves compatible `/api/*.json` application routes. It does not use Nginx, a gateway, or browser request interception. See [resource-reuse/README.md](resource-reuse/README.md) for the active architecture and [deploy/README.md](deploy/README.md) for Docker deployment. The earlier hand-written `web/` UI is historical and is not used by `cmd/reuse`.

The service version comes from `VERSION`; `/healthz` and `/version` report the running version and commit. Each completed small change gets a new patch version, changelog entry, validated commit, annotated tag and SSH push:

```powershell
.\scripts\release.ps1 -Message 'Describe the completed change'
```

Before publishing, the script checks the staged files for local HAR account/session values, private-key material and forbidden private/generated paths. HAR recordings, credentials, cookies, reports and logs remain local and are not included in Docker build contexts or Git deployment archives.

This workspace contains modular Go and TypeScript clients for the authenticated classroom/task flow and the code-question chain observed in the supplied local recordings.

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

## Original compiled web application

The active browser application is `cmd/reuse`, listening on port `11454`. It hosts the existing compiled resources and uses the same signed protocol client with a server-side session. Its exact compatibility routes now include code saving, self-test startup, evaluation startup and evaluation polling. Code and evaluation responses bypass caches. No replacement frontend is generated.

```powershell
go run ./cmd/reuse
```

Deployment instructions remain in `deploy/README.md`; the user manages the external reverse proxy separately.

## Programming questions

The new coding protocol is documented with request order, field dependencies and zero-based HAR entry references in [docs/programming-protocol.md](docs/programming-protocol.md). It classifies the observed code-question discriminators explicitly and keeps other question types unsupported. Question Markdown, exact editor filenames/extensions, UTF-8 code, self-test output, final test results and homework passage states are separate values.

Go adds these explicit operations to the existing client: `LoadTask`, `ReadProgramming`, `ReadCodeFile`, `SaveCode`, `SelfTest`, `BuildCode`, `WaitEvaluation`, `SubmitCode` and `HomeworkProgress`. Each submission uses its own save response, including commit ID and evaluation key. Self-test uses the observed WebSocket subscription and Base64 stream; a matching self-test never triggers submission. A returned `next_game` requires an explicit subsequent load.

After setting the existing login environment variables, use values returned by the selected task; `--path` is the exact server path and `--source` is a local UTF-8 code file:

```powershell
go run ./cmd/educoder read --course COURSE --homework HOMEWORK_ID --game GAME --json
go run ./cmd/educoder save --course COURSE --homework HOMEWORK_ID --game GAME --path 'SERVER_FILE.cpp' --source 'LOCAL_FILE.cpp'
go run ./cmd/educoder self-test --course COURSE --homework HOMEWORK_ID --game GAME --path 'SERVER_FILE.cpp' --source 'LOCAL_FILE.cpp' --stdin-file 'INPUT.txt' --expected-file 'EXPECTED.txt' --json
go run ./cmd/educoder submit --course COURSE --homework HOMEWORK_ID --game GAME --path 'SERVER_FILE.cpp' --source 'LOCAL_FILE.cpp' --timeout 2m --json
go run ./cmd/educoder progress --course COURSE --homework HOMEWORK_ID --json
```

The TypeScript module `typescript/programming.ts` exposes `ProgrammingClient` over the existing `EducoderClient` and transport. Its `task`, `readFile`, `save`, `selfTest`, `startEvaluation`, `pollEvaluation` and `progress` methods provide the same protocol stages. Save and self-test are explicit separate calls in TypeScript; the Go `SelfTest` convenience method saves first.

Replay the supplied recording locally without credentials or network requests:

```powershell
node typescript/cli.ts coding-replay --har openwork_to_finishwork.har
```

The command sorts by request timestamp, matches method/path/query/body, consumes repeated responses and 304 entries in order, and replays the recorded WebSocket messages. It reports two self-test matches, two successful evaluations and refreshed `passed_count` from 0 to 2. It reuses recorded submitted code as evidence and does not generate answers. The first level has no captured save before self-test, and the last exit autosave has no captured response body; the report preserves those gaps. The captured `shixun_exec` 304 also has no response body, so replay starts with the actual task responses.

Editor content is the current workspace code, not a guaranteed pristine template. The first level already contains filled code at entry; the second contains a blank function template. This recording contains one `.cpp` per level, with no observed directory tree or `.h` requests. Other question types, multiple-file evaluation and upstream failure/queue/retry branches need further recordings.

## TypeScript CLI

The TypeScript CLI retains `login`, `courses`, `tasks`, `enter` and `flow` with the same credential environment variables, and adds the offline `coding-replay` command described above:

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

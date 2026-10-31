# In-process answer jobs

`Manager` is the application execution boundary. It calls an `Executor` Go
function on a bounded worker pool; it never starts a CLI or a subprocess.

```go
manager, err := jobs.Open(ctx, jobs.Config{Path: "/data/jobs.sqlite"})
job, reused, err := manager.Submit(requestContext, ownerHash, request, executor)
```

Accepted work uses a manager-owned context. HTTP disconnection, page navigation
and logout do not stop it. Call `Cancel` explicitly to request cancellation.
Executors must propagate their context into model, HTTP and WebSocket calls.

Defaults are sixteen workers, 256 waiting jobs, sixteen outstanding jobs per owner,
four running jobs per owner, a thirty-minute execution deadline, seven-day history,
64 events per job and a 256 KiB result limit. A worker scans past jobs whose
owner is already running, allowing other accounts to use available capacity.

SQLite holds only the validated request and public lifecycle/result records.
Account sessions, cookies, model keys and executor closures stay in memory and
are released when execution terminates. Owner values are supplied as opaque
account hashes and never appear in the serialized `Job`. Get, list and cancel
operations always require the same owner.

Idempotency keys are scoped to an owner. Reusing a key with a different request
returns `idempotency_conflict`. An active target/mode coalesces onto its existing
job, including when a second idempotency key was supplied; those aliases survive
completion and restart. Terminal history and its keys expire together.

Report `saving`, `submitting` or `evaluating` **before** the first corresponding
upstream mutation. That boundary is durable and sticky. Cancellation, timeout or
an execution error after it yields `uncertain`, because canceling a Go context
cannot undo a write already received upstream. A known failed evaluation may
instead return an explicit failed outcome with its public result.

At startup, old queued and running records become `interrupted`; records beyond
the mutation boundary become `uncertain`. Work is never replayed automatically,
because no credentials were saved and replaying an accepted submission can
duplicate a write. A client must inspect the original job/upstream state before
creating a new request.

`Close` rejects new admission, cancels waiting and active work, and waits up to
its caller's context deadline. It keeps the database open until workers have
returned, even if the wait expires. It cannot forcibly terminate executor code
that ignores context. The application must close its answer/question-bank
resources after workers stop.

Run concurrency and persistence validation with:

```text
go test -race ./internal/jobs
```

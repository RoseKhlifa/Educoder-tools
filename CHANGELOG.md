# Changelog

## 0.1.115 - 2026-11-07

- Count only assigned required levels in course homework summaries

## 0.1.114 - 2026-11-07

- Preserve pre-formal failure states and validate historical score probe locks

## 0.1.113 - 2026-11-07

- Reuse ranked historical score baselines for one-question objective probes

## 0.1.112 - 2026-11-07

- Align objective failure history with bounded submission budget

## 0.1.111 - 2026-11-07

- Enforce full Go and browser release gates before metadata mutation, and verify deliberate gate failures are rejected.
- Scope unknown-write guards to formal evidence and individual levels; keep self-test/pre-write failures retryable and recompute final counters from level state.
- Canonicalize compact external multi-select answers, preserve legacy imports, repair score-feedback baselines, persist score-delta locks, and add bounded graphical retry/status handling.
- Remove obsolete automatic programming-repair-round configuration and align async-job documentation with the bounded per-level budgets and explicit retry flow.
- Defer objective media fetches until text-bank lookup, retain unverified platform-observed collection candidates, and scope no-progress evidence to reusable proofs for this homework.

## 0.1.110 - 2026-11-06

- Use verified and precisely matched external objective answers as bounded first candidates without promoting them before platform proof.
- Let one Flash call return ranked objective alternatives, then use comparable platform score deltas for bounded one-question feedback while stopping on unknown or changed evidence.
- Preserve legacy objective records, add strict ranked-answer materialization, and tighten graphic submit baseline reads after environment handoff.

## 0.1.109 - 2026-11-06

- Continue confirmed per-level failures only when the upstream task explicitly enables `skip_level` and `task_pass`; unknown writes, cancellations and unconfirmed results still stop.
- Isolate roster and preflight failures per level, reconcile every required level before reporting completion, and keep exact required/completed counters.
- Add owner/course/homework/version/evidence-bound no-progress cooldowns and unknown-submission guards to prevent wasteful duplicate runs.

## 0.1.108 - 2026-11-06

- Bound programming and objective attempts, model profiles and provider recovery to finite per-level budgets; stop repeated candidates without automatic paid continuation
- Serialize graphic and MySQL mutations per account, classify pre-write and saved-not-submitted outcomes, and release each environment owned by the run on every path.
- Add cancelable resource backoff and preserve unknown submissions as non-replayable

## 0.1.107 - 2026-11-06

- Align deployment JOB_TIMEOUT defaults and documentation with production two-hour batch limit

## 0.1.106 - 2026-11-06

- Distinguish per-decision combinations, feasible global input-predicate combinations and runtime short-circuit observations in objective coverage analysis
- Require domain partitions, dependency constraints, concrete witnesses and explicit local/global interpretation checks instead of assuming independent predicates or inventing control flow for prose specifications
- Preserve strict output slots, prior full-score skips, complete question context, hard budgets and normal platform score verification

## 0.1.105 - 2026-11-06

- Make objective output slots explicit for focused review, including original positions, exact array length and single/multiple-choice string shapes
- Carry only sanitized structural output feedback into a retry of the same question; never reuse rejected answer values or scores and clear feedback before the next question
- Diagnose rejected objective responses by whitelisted count/shape metadata while preserving strict validation, complete-context review, existing budgets and durable usage accounting

## 0.1.104 - 2026-11-06

- Break repeated objective-answer anchoring with one bounded clean per-question review sweep inside existing model and evaluation limits
- Retain the exact full original roster, shared material and all inspected media during focused review; assemble and submit only a complete vector while preserving verified locks
- Keep duplicate-failure suppression, unknown-submission stops, current-snapshot revalidation, and per-call durable usage accounting unchanged

## 0.1.103 - 2026-11-06

- Clarify generic decision-coverage reasoning for compound predicates, nested branches, loops and recursive re-evaluation without embedding task-specific answers

## 0.1.102 - 2026-11-06

- Keep objective repair in a compact Flash profile after any observed truncation, preventing Pro reasoning from alternating back into repeated 8192-token failures
- Preserve compact mode across malformed follow-up responses until a valid candidate is produced, while retaining normal reasoning escalation when no truncation occurred

## 0.1.101 - 2026-11-06

- Preserve objective repair reasoning with image attachments; limit compact-output fallback to the single call after an observed truncation rather than downgrading the entire run
- Propagate exact-bound refreshed per-question grading into repair history while retaining validated answer locks and rejecting ambiguous results
- Add independent semantic and dynamic-execution checks for objective questions without widening strict JSON output, increasing retries, or inferring answers from aggregate scores

## 0.1.100 - 2026-11-06

- Adapt legacy objective answers non-destructively through scoped context aliases, original collection-scope recovery, and exact legacy references; preserve answer, verification, contribution and receipt identities
- Expand verified historical answer vectors into individual questions idempotently; reject conflicting or newer-failed evidence across legacy and current IDs without inflating alias inventory
- Validate explicit upstream field presence and complete rosters before bank matching, refresh full question and media snapshots before submission, and bind hidden-result full-score vectors to original single-answer records

## 0.1.99 - 2026-11-06

- Add context-complete per-question objective reuse with fail-closed fingerprints, fixed verified answer slots, and one full-vector platform submission

## 0.1.98 - 2026-11-06

- Stop reusing objective candidates after a later current-run partial score invalidates their historical full-score verification

## 0.1.97 - 2026-11-06

- Keep image objective repairs in compact answer mode so reasoning cannot consume the JSON response window

## 0.1.96 - 2026-11-06

- Allow multiple distinct images in one shared objective stem while keeping one provider image per unique asset and preserving every question mapping

## 0.1.95 - 2026-11-06

- Classify upstream graphic resource pressure separately and retry it with a cancellable backoff; keep graphical environments alive between read and submit so the formal evaluation can reopen the runtime

## 0.1.94 - 2026-11-06

- Release confirmed graphical environments through the official lifecycle endpoint, classify upstream quota exhaustion without retrying, support MySQL DESCRIBE artifacts with durable uncertainty evidence, and accept explicit .mdj file suffix wording

## 0.1.93 - 2026-11-06

- Allow cold graphic runtimes a bounded six readiness attempts with 3.75 seconds of cancellable backoff before opening the workspace
- Block direct job replay when a self-test stream is unconfirmed, including nested course and level results

## 0.1.92 - 2026-11-05

- Preserve confirmed objective failures across recovery, keep image reasoning enabled, and use bounded feedback history to avoid repeating failed answer vectors
- Recover graphic pre-build interruptions from exact saved-source evidence without replaying uncertain formal submissions

## 0.1.91 - 2026-11-05

- Honor configured time limits for long course batches and default workers to two hours, preserving bounded requests, model calls and individual homework

## 0.1.90 - 2026-11-05

- Align graphic tasks with the official evaluation preparation protocol, recover assigned subset rosters, and classify VNC placeholders safely

## 0.1.89 - 2026-11-05

- Treat graphic status=0 after formal build as a queued non-terminal state and continue polling until a bounded terminal result

## 0.1.88 - 2026-11-05

- Retry missing graphic evaluation keys with bounded read-only polling before marking a saved file unsubmitted

## 0.1.87 - 2026-11-05

- Retry eventually consistent graphic runtime bindings with bounded readiness checks before opening the workspace

## 0.1.86 - 2026-11-05

- Carry explicit graphic saved-before-submission evidence from homework results to the affected challenge during recovery

## 0.1.85 - 2026-11-05

- Resume graphic tasks safely when the persisted failure proves the file was saved before any formal submission

## 0.1.84 - 2026-11-04

- Read and display both course experiments and common text/image homework tabs, preserve explicit personal submission state, and keep unsupported common homework out of the shixun execution queue

## 0.1.83 - 2026-11-04

- Execute mixed Hadoop workspace candidates through the observed tab4 terminal while preserving editor placeholders

## 0.1.82 - 2026-11-04

- Start Hadoop through bound terminal readiness checks and wait for complete evaluation lifecycle without repeated model repairs

## 0.1.81 - 2026-11-04

- Reconcile uncertain formal results with read-only platform evidence and classify provider payment gates

## 0.1.80 - 2026-11-03

- Separate current course work from historical attempts and revalidate stale lists with strict metadata and terminal-version evidence

## 0.1.79 - 2026-11-03

- Allow audited admin retries with the original user live session shared idempotency and clear pending controls

## 0.1.78 - 2026-11-03

- Back off transient model failures with cancellable jitter and bounded Retry-After across programming objective and exercise generation

## 0.1.77 - 2026-11-03

- Retry explicit read-only course and roster metadata without multiplying collection attempts or treating missing pages as empty work

## 0.1.76 - 2026-11-03

- Preserve exact relative HTML references and inspect target-effect examples during source-bound repair

## 0.1.75 - 2026-11-03

- Evaluate explicit operator proposals through fresh selftest and formal gates without promoting unresolved or unmarked candidates

## 0.1.74 - 2026-11-03

- Bind each selftest observation to its actual candidate and source hash and report recovery model behavior accurately

## 0.1.73 - 2026-11-03

- Retry only transient homework progress reads with bounded attempts and private operator diagnostics while retaining fresh-score requirements

## 0.1.72 - 2026-11-03

- Keep model service availability failures out of code-quality escalation while preserving charged calls and durable recovery budgets

## 0.1.71 - 2026-11-03

- Fall back once from a stalled enhanced model within existing budgets and preserve HTML attribute repair evidence

## 0.1.70 - 2026-11-03

- Distinguish interrupted model response bodies and allow four minutes without replaying unknown billed calls

## 0.1.69 - 2026-11-03

- Retest exact historical formal environment failures before model repair while preserving source-bound evidence

## 0.1.68 - 2026-11-03

- Resume proven preformal selftest failures and detect stale browser resources with explicit recovery outcomes

## 0.1.67 - 2026-11-03

- Expose recovery reconciliation state and preserve source progress while fixing Docker build allowlist

## 0.1.66 - 2026-11-03

- Clarify current task progress, move completed records to history, improve live details and unify user-facing copy

## 0.1.65 - 2026-11-03

- Reuse durable verified collection checkpoints across logins and add reconciled user and admin continuation actions

## 0.1.64 - 2026-11-03

- Correct paused service statuses and prefer fresh zero unfinished course results over historical failures

## 0.1.63 - 2026-11-03

- Recheck source-bound contradictory empty selftests before model repair and retain explicit stdout stream evidence

## 0.1.62 - 2026-11-03

- Normalize external Python editor whitespace before candidate hashing while preserving strings provenance and exact readback verification

## 0.1.61 - 2026-11-03

- Classify completed formal environment startup failures as unknown and retry the same bound candidate once within evaluation limits

## 0.1.60 - 2026-11-03

- Support terminal-only Linux tasks with single-use receipts, full-score verification and workspace-only bank evidence

## 0.1.59 - 2026-11-02

- Adapt lazy homework runtimes and exact LF bank candidates; preserve effective repair state and separate runtime failures

## 0.1.58 - 2026-11-02

- Unify question bank inventory counts across objective programming and graphic questions

## 0.1.57 - 2026-11-02

- Show verified graphic questions and imported graphic references in the question bank overview

## 0.1.56 - 2026-11-02

- Import pinned public programming and StarUML answer references into the Go question bank and execute matched graphic tasks through the direct SSH/SFTP protocol

## 0.1.55 - 2026-11-02

- Run verified collection to exhaustion without overall deadlines or aggregate caps using a dedicated worker lane

## 0.1.54 - 2026-11-02

- Simplify user guidance and keep every dialog close control visible while content scrolls

## 0.1.53 - 2026-11-02

- Synchronize unfinished homework counts and course completion with session-scoped detail refreshes

## 0.1.52 - 2026-11-02

- Allow users to stop and restart interrupted homework tasks with owner-bound idempotent retries

## 0.1.51 - 2026-11-02

- Fix verified image question bank reuse across accounts

## 0.1.50 - 2026-11-02

- Add feedback tickets service and contribution statistics guidelines credits and progress fixes

## 0.1.49 - 2026-11-02

- Prefetch course details and show real daily completions, instructors and student counts

## 0.1.48 - 2026-11-02

- Add verified question bank summary and service notes to the user sidebar

## 0.1.47 - 2026-11-02

- Retire access cards and add clear login user guidelines with data reuse disclosure

## 0.1.46 - 2026-11-02

- Make brand logo background transparent and remove white UI backplate

## 0.1.45 - 2026-11-01

- Use supplied Touge Mom logo in login, user, admin and browser icons

## 0.1.44 - 2026-11-01

- Adopt warm paper template UI for consumer and admin workspaces

## 0.1.43 - 2026-11-01

- Add Go online-exam protocol, verified answer save and submission workflow with classroom exam UI

## 0.1.42 - 2026-11-01

- Show compact current consumer progress and fold superseded arrangements into history

## 0.1.41 - 2026-11-01

- Repair failed programming attempts automatically with durable feedback and show current services separately from history

## 0.1.40 - 2026-11-01

- Clarify that the phase in batch task details belongs to the whole batch

## 0.1.39 - 2026-11-01

- Explain collection outcomes and skip reasons; bind admin assignment details and preserve precise batch stop causes

## 0.1.38 - 2026-11-01

- Show readable user course homework and failure details in admin

## 0.1.37 - 2026-11-01

- Exclude optional levels from execution and required-work progress

## 0.1.36 - 2026-10-31

- Exclude expired homework from active service lists and execution

## 0.1.35 - 2026-10-31

- Handle URL-safe self-test frames and failed hidden-test evaluation feedback

## 0.1.34 - 2026-10-31

- Check non-root disk space and data writes before service replacement

## 0.1.33 - 2026-10-31

- Fix release changelog encoding across Windows shells

## 0.1.32 - 2026-10-31

- A服务界面、头歌平台身份隔离、管理端与课程批处理

## 0.1.31 — 2026-10-31

- Supply objective image bytes, reject repeated failed answers, and refresh module graph caches

## 0.1.30 — 2026-10-31

- Allow ten bounded model repairs for persistently non-full objective levels

## 0.1.29 — 2026-10-31

- Repair newline-only selftest failures, retry model outputs, and expose precise homework stop reasons

## 0.1.28 — 2026-10-31

- Raise bounded account and model concurrency for independent homework jobs

## 0.1.27 — 2026-10-31

- Handle subset homework rosters and prevent objective answer truncation

## 0.1.26 — 2026-10-31

- Treat legacy submit requests as whole-homework batches and bust stale UI assets

## 0.1.25 — 2026-10-31

- Finalize whole-homework score verification and preserve uncertain batch results

## 0.1.24 — 2026-10-31

- Allow whole-homework batches to run within their bounded thirty-minute job timeout

## 0.1.23 — 2026-10-31

- Complete all selected homework levels from one asynchronous action

## 0.1.22 — 2026-10-31

- Resolve existing completed games through read-only runtime rosters and verify hidden-result full-score answer sheets

## 0.1.21 — 2026-10-31

- Explain collection scan coverage and skip reasons without implying answers were imported

## 0.1.20 — 2026-10-31

- Collect proven completed answers after login using bounded owner-isolated read-only jobs

## 0.1.19 — 2026-10-31

- Expose homework deadlines and complete task detail metadata in the workbench

## 0.1.18 — 2026-10-31

- Complete course batches with verified objective caching, image inputs, recoverable solver state and API accounting

## 0.1.17 — 2026-10-31

- Treat an unavailable optional model secret as browse-only mode

## 0.1.16 — 2026-10-31

- Keep the workbench available when the optional model secret is absent

## 0.1.15 — 2026-10-31

- Run programming and objective answers asynchronously in bounded Go workers

## 0.1.14 — 2026-10-31

- Verify objective full scores using exact decimal progress in every output mode

## 0.1.13 — 2026-10-31

- Require exact full score before advancing objective-question levels

## 0.1.12 — 2026-10-31

- Include the objective protocol dependency in Docker builds and keep task action labels on one line

## 0.1.11 — 2026-10-31

- Restore the handwritten workbench, keep Go as the sole protocol backend, and remove original compiled UI resources

## 0.1.10 — 2026-10-31

- Add isolated objective-question generation, choice submission and type routing

## 0.1.9 — 2026-10-31

- Mark the programming-only AI prompt boundary explicitly

## 0.1.8 — 2026-10-31

- Clarify suffix-specific source and header semantics in AI prompts

## 0.1.7 — 2026-10-31

- Expose exact filename and extension in AI programming prompts

## 0.1.6 — 2026-10-31

- Add AI code-answer generation, bounded repair state and SQLite verified question bank

## 0.1.5 — 2026-10-31

- Reproduce recorded programming-question files, self-tests and evaluations in Go and TypeScript

## 0.1.4 — 2026-10-30

- Replace decorative media with lightweight SVGs, remove promotional video loading, and refresh compiled asset fingerprints

## 0.1.3 — 2026-10-30

- Speed up page navigation with session read caching, request coalescing and compressed static assets

## 0.1.2 — 2026-10-30

- Fix user and teacher avatars with verified image responses and memory-only caching

## 0.1.1 — 2026-10-30

- Default service port is now 11454, with application version/commit information available from health and version endpoints.
- Added a non-root Docker image, Compose deployment and a release archive installer for the `main` SSH host.
- Added a patch release workflow with version updates, privacy checks, validation, commits, annotated tags and SSH pushes.
- Deployment uses only the application listener. Domain routing for `touge.mom` remains managed by 1Panel.

## 0.1.0 — 2026-10-30

- Initial import of the Go protocol client and original compiled-resource service.
- Session-based login, classroom lists, homework lists and task entry compatibility.
- Original compiled static resources with local API and image address mapping.
- Network banner removal, verified image delivery and Windows background launcher.

Local HAR recordings, account data, runtime sessions, logs, verification reports and downloaded source backups are excluded from the repository.

# Changelog

## 0.1.32 鈥?2026-10-31

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

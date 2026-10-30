# Changelog

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

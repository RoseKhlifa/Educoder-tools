# Repository working rules

- The active application is `cmd/web`: one Go HTTP service hosting the handwritten TypeScript workbench in `web/` and its own application API. The original compiled UI and the Node/TypeScript protocol client have been removed at the user's request.
- Keep protocol, AI and question-bank logic in Go. TypeScript is for browser UI only; do not recreate a second protocol backend.
- Preserve concurrent work in shared protocol/answer modules. Use an isolated worktree for UI/deployment changes and integrate only the intended paths.
- Keep account credentials, HAR recordings, upstream cookies, signed resource queries, session stores and runtime logs out of Git, images and deployment archives.
- Use the SSH Git remote `git@github.com:RoseKhlifa/Educoder-tools.git`.
- Every completed small change must increment the patch version in `VERSION`, update `CHANGELOG.md`, pass the relevant checks, receive its own commit and annotated `vVERSION` tag, and be pushed with its tag. User instructions in the active chat take precedence.
- Never amend or force-push published releases. Fixes receive another patch release.
- Deployment listens on port `11454`. The user manages the `touge.mom` reverse proxy in 1Panel; do not create or edit Nginx/OpenResty configuration.
- Deployment host is reached using the local SSH alias `main`. Do not overwrite unrelated services, containers or directories.

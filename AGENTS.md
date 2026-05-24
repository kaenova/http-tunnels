# AGENTS.md

Before doing any work in this repository, **read `.agents/guidelines/README.md` first**.

If implementation patterns drift from the documented guidance, update the guideline documents in `.agents/guidelines/` as part of the same change.

---

## Repository structure

- `http_tunnels.go` - tunnel client CLI entrypoint
- `cmd/server` - Go tunnel server entrypoint and embedded admin assets
- `cmd/server/web` - Bun/Vite React admin dashboard source
- `internal/client` - client-side tunnel logic and self-update implementation
- `internal/server` - server routing, auth, admin API, streaming tunnel logic, and SQLite persistence
- `internal/protocol` - shared websocket streaming protocol helpers
- `.agents/guidelines` - repository implementation patterns
- `.agents/reference-files` - reference files used while implementing the admin UI and guidelines
- `.github/workflows` - CI and release automation

---

## Working expectations

- Keep streaming support intact for SSE and binary transfers.
- Preserve the split between request-response analytics and tunnel creation analytics.
- Use the documented admin routing, authentication, and data-fetching patterns.
- Rebuild the admin frontend (`cmd/server/web`) before building the Go server when UI code changes.
- If you change route structure, auth behavior, architecture, or frontend data flow, update the matching guideline file.

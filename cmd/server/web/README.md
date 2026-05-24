# Admin Web Interface

This directory contains the Bun/Vite React admin interface for `http-tunnels`.

## Stack

- React
- React Router
- TanStack Query
- ShadCN UI
- Recharts via the ShadCN chart primitives
- Bun for package management and local development

## Commands

```bash
bun install
bun dev
bun run build
```

## Routes

- `/admin/auth/login`
- `/admin`
- `/admin/tunnels`
- `/admin/tunnels/:tunnelId`
- `/admin/request-activity`
- `/admin/request-activity/:requestId`

## API dependencies

The frontend reads from the Go server admin API:

- `GET /api/admin/auth/session`
- `POST /api/admin/auth/login`
- `GET /api/admin/dashboard`
- `GET /api/admin/request-activity`
- `GET /api/admin/request-activity/:requestId`
- `GET /api/admin/tunnels`
- `GET /api/admin/tunnels/:tunnelId`
- `DELETE /api/admin/tunnels/:tunnelId`

## Notes

- Authentication is cookie-based and backed by `WEB_PASSWORD` on the Go server.
- The server embeds the built `dist/` output into the Go binary.
- Rebuild the frontend after UI changes so `cmd/server` embeds the latest assets.

```bash
bun run build
```

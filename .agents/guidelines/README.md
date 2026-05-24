# HTTP Tunnels Implementation Patterns

This directory documents the current implementation patterns for the `http-tunnels` repository.

**Read this file first** before changing the tunnel server, tunnel client, or admin dashboard.

---

## Index

| Pattern | File | Description |
|---|---|---|
| Architecture | [`architecture.md`](./architecture.md) | System overview for the tunnel server, tunnel client, websocket streaming protocol, SQLite analytics store, and embedded admin dashboard. |
| Routes | [`routes.md`](./routes.md) | Admin route structure, protected routing rules, server-side route handling, and SPA path ownership. |
| Authentication | [`authentication`](./authentication) | Login, logout, cookie sessions, `WEB_PASSWORD`, and how protected admin access works. |
| Data Fetching & Mutation | [`data-fetching-mutation.md`](./data-fetching-mutation.md) | TanStack Query usage, admin API calls, invalidation rules, polling, and destructive mutation patterns. |

---

## What to read next

### If you are changing tunnel behavior

1. `architecture.md`
2. `routes.md` only if the change affects public/admin route handling
3. `data-fetching-mutation.md` only if the change affects admin APIs or dashboard data flow

### If you are changing the admin dashboard

1. `routes.md`
2. `authentication`
3. `data-fetching-mutation.md`
4. `architecture.md` if the UI change also affects persistence, websocket flow, or analytics capture

### If you are changing login/logout or access control

1. `authentication`
2. `routes.md`
3. `data-fetching-mutation.md`

### If you are changing persistence or analytics tables

1. `architecture.md`
2. `data-fetching-mutation.md`
3. `routes.md` if the API or page contract changes

---

## Rules of thumb

- Keep the tunnel transport streaming. Avoid reintroducing full-body buffering for request or response forwarding.
- Preserve the SQLite analytics model unless there is a strong reason to migrate it.
- The admin dashboard is a Bun/Vite React SPA, but authentication is enforced by the Go server.
- When patterns drift, update these guideline files in the same change.

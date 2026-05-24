# Routes

## Route ownership

There are two route layers in this repository:

1. **Go server routing** in `internal/server`
2. **React Router SPA routing** in `cmd/server/web/src/App.tsx`

Always decide which layer owns the change before implementing it.

---

## Go server route rules

The Go server owns:

- tunnel registration and websocket endpoints
- admin JSON APIs
- auth cookie creation and clearing
- serving the embedded SPA entrypoint and built assets
- host-based tunnel request forwarding

### Current server route shape

- `/ping`
- `/new_tunnel`
- `/tunnel`
- `/api/admin/*`
- `/admin/*`
- `/assets/*`

### Precedence rule

If `r.Host` matches an active tunnel, the request is forwarded through the tunnel first.
Only non-active hosts fall through to the admin/server routes.

Do not break this precedence when adding new admin paths.

---

## Admin SPA route rules

The React app owns only the admin browser paths:

- `/admin/auth/login`
- `/admin`
- `/admin/tunnels`
- `/admin/tunnels/:tunnelId`
- `/admin/request-activity`
- `/admin/request-activity/:requestId`

### Current pattern

- `App.tsx` creates the router.
- `AdminLayout` is the protected shell for authenticated admin pages.
- `LoginPage` is the unauthenticated entrypoint.
- Unknown client-side paths redirect back to `/admin`.

### Protected routing rule

Do not rely on the frontend alone for protection.

The current pattern is:

1. Go server redirects unauthenticated `/admin/*` requests to `/admin/auth/login`.
2. React Router also checks the admin session via `/api/admin/auth/session`.
3. Protected admin routes render under `AdminLayout` only after the session is confirmed.

Both layers should remain aligned.

---

## Sidebar routing pattern

The sidebar currently exposes three hardcoded navigation entries:

- Dashboard
- Active Subdomain
- Request Activity

When adding a new admin page:

1. add the React Router route
2. add the sidebar entry
3. update breadcrumbs in the page header
4. ensure the route is covered by the server-side admin protection rules

---

## Detail route pattern

Tunnel detail pages use the stable tunnel record ID, not the domain string, as the route param:

- `/admin/tunnels/:tunnelId`

This avoids ambiguity when the same requested subdomain is reused across different tunnel records over time.

---

## Asset route pattern

Built frontend assets are served from `/assets/*`.
Keep the Vite build output compatible with being served from the root host.

Do not move the admin SPA under a different asset prefix without updating:

- the embedded asset serving path
- the Vite build assumptions
- the server-side SPA entrypoint handler

---

## When adding new routes

### Add a new admin page

1. update `cmd/server/web/src/App.tsx`
2. add the page component under `cmd/server/web/src/pages`
3. add sidebar navigation if needed
4. add or update the corresponding admin API route in Go if the page needs data
5. document the new route here if it becomes part of the stable pattern

### Add a new admin API route

1. add the handler under `internal/server/handlers_admin.go` or split a new handler file if needed
2. keep auth requirements explicit
3. return JSON consistently
4. add frontend API helpers in `cmd/server/web/src/lib/api.ts`
5. invalidate or refetch related queries in the frontend

### Request activity route pattern

The request activity surface is split into:

- `/admin/request-activity` for filtered, paginated list browsing
- `/admin/request-activity/:requestId` for a single request-response detail view

The matching API pattern is:

- `GET /api/admin/request-activity`
- `GET /api/admin/request-activity/:requestId`

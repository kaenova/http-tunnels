# Data Fetching & Mutation

## Overview

The admin dashboard uses **TanStack Query** for server data.

API helpers live in:

- `cmd/server/web/src/lib/api.ts`

Pages call those helpers through `useQuery` and `useMutation`.

---

## Current query pattern

### Query location

Keep page-level queries inside the page component unless the same query logic is reused enough to justify a custom hook.

### Query keys

Current stable query keys:

- `['admin-session']`
- `['dashboard', chartFilters]`
- `['request-activity', page, pageSize, filters]`
- `['request-activity-detail', requestId]`
- `['tunnels', page, pageSize]`
- `['tunnel-detail', tunnelId, page, pageSize, chartFilters]`

### Polling

The dashboard is meant to reflect active connections and recent request traffic.

Current pattern:

- dashboard data polls every 5 seconds
- tunnel list polls every 5 seconds
- tunnel detail polls every 5 seconds
- auth session does **not** poll aggressively

Avoid adding shorter polling intervals unless clearly necessary.

Request activity follows the same 5-second polling baseline because new logs can arrive continuously while the admin is observing the system.

---

## Mutation pattern

### Current destructive mutation

Tunnel deletion is implemented with `useMutation` and a ShadCN `AlertDialog` confirmation.

Pattern:

1. user opens dropdown action or detail-page delete action
2. UI opens a confirmation dialog
3. mutation calls `DELETE /api/admin/tunnels/:tunnelId`
4. on success, invalidate affected queries
5. show success toast
6. close dialog or redirect as needed

### Invalidation rules

After deleting a tunnel, invalidate at least:

- `['dashboard']`
- `['tunnels']`
- related `['tunnel-detail', ...]` state when applicable
- `['request-activity', ...]` if the deleted tunnel affects currently visible request listings

---

## Error handling pattern

API helper failures throw `ApiError`.

Current UI pattern:

- queries show a compact inline error state in the page body
- mutations show toast feedback
- login errors render inline field feedback on the form

Do not silently swallow API failures.

---

## API helper pattern

`api.ts` should:

- keep the fetch wrapper centralized
- include `credentials: 'same-origin'`
- parse JSON responses
- throw a typed `ApiError` on non-2xx responses

When adding a new admin API:

1. add the Go handler
2. add the frontend `api.ts` helper
3. use `useQuery` / `useMutation` in the page or shared hook
4. define the invalidation behavior explicitly

### Chart filter pattern

Dashboard and tunnel-detail charts use shared query params:

- `granularity`
- `range`
- `start`
- `end`

Keep chart aggregation server-side and include the applied chart filter object in the React Query key so polling stays aligned with the selected view.

---

## Data shape rules

- Keep tunnel list responses paginated.
- Keep tunnel detail responses bundled enough for the page to render without multiple parallel requests unless there is a clear reason to split them.
- Keep analytics and logs server-side aggregated where possible.
- Do not move heavy aggregation into the browser when SQLite can answer it efficiently.

---

## Mutation UX rules

- Destructive actions should require confirmation.
- Success should produce immediate visible feedback.
- Disabled/loading states should prevent double-submit.
- If a mutation changes a list page and a detail page, invalidate both views.

---

## If patterns drift

If you introduce:

- a new shared query key convention
- a custom hook layer
- optimistic updates
- websocket/live push instead of polling

update this guideline so future work follows the new baseline.

# Architecture

## Overview

The repository is split into three main runtime surfaces:

1. **Tunnel client** (`http_tunnels.go` → `internal/client`)
2. **Tunnel server** (`cmd/server` → `internal/server`)
3. **Admin dashboard** (`cmd/server/web`)

The Go server embeds the built admin dashboard assets from `cmd/server/web/dist`.

---

## Tunnel client

The client:

- registers a new tunnel with `POST /new_tunnel`
- connects to the server websocket at `/tunnel`
- receives streamed request frames from the server
- forwards them to the local destination server
- streams response frames back to the server
- exposes `http-tunnels update` for GitHub-release self-updates

### Important client rules

- Do **not** restore a fixed `http.Client.Timeout` for forwarded destination requests. That breaks SSE and long-lived streams.
- Request and response bodies are forwarded as websocket **frames**, not as one fully buffered JSON payload.
- Keep the root CLI entrypoint thin. Put logic in `internal/client`.

---

## Tunnel server

The server has four responsibilities:

1. create and validate tunnel registrations
2. proxy tunneled HTTP traffic over a single websocket connection per active domain
3. persist analytics and admin metadata in SQLite
4. serve the authenticated admin SPA and JSON admin API

### Public server routes

- `POST /new_tunnel` - create a tunnel registration
- `GET /tunnel` - websocket connection from the tunnel client
- `GET /ping` - health check
- `/<anything>` - proxied tunnel traffic when the host matches an active tunnel

### Admin server routes

- `/admin/*` - SPA shell, protected except login
- `/admin/auth/logout` - clears auth cookie and redirects
- `/api/admin/*` - authenticated JSON API except session/login

### Route precedence

If the incoming host matches an active tunnel session, tunneled traffic wins first.
That prevents admin or server paths from stealing requests that belong to a live subdomain.

---

## Streaming protocol

Shared protocol types live in `internal/protocol`.

The websocket transport uses typed frames over a **single long-lived websocket per active tunnel**:

- `register`
- `registered`
- `request_start`
- `request_body`
- `request_end`
- `request_cancel`
- `response_start`
- `response_body`
- `response_end`
- `response_error`
- `ping`
- `pong`

### Why this matters

The original implementation buffered whole responses before forwarding them. That broke:

- SSE (`text/event-stream`)
- large downloads
- any response where the caller needed incremental flush behavior

The current pattern is chunked streaming on both sides.

### Invariants

- keep forwarding order per request intact
- multiplex concurrent response streams fairly; the current pattern is **round-robin per request id** so one response cannot saturate the websocket
- flush streamed response chunks on the server side when the writer supports `http.Flusher`
- allow long-lived streams to remain open until the request context is cancelled or the stream ends
- keep request cancellation support so server-side disconnects can stop client-side destination requests
- keep heartbeat `ping` / `pong` support so idle tunnel connections stay alive and dead peers are detected

---

## Persistence

The Go server uses SQLite through `internal/server/store.go`.

### Tables

#### `tunnels`
Stores tunnel registrations and current state.

Key fields:

- `id` - stable hash of the normalized full domain; reused across reconnects for the same domain so admin analytics stay consolidated
- `domain`
- `requested_subdomain`
- `domain_key_hash`
- `state`

### Tunnel state reconciliation

The server keeps a lightweight reconciler loop for admin correctness:

- tunnels shown in the active-tunnels admin list should be truly `active`, not merely `pending`
- `pending` tunnels that never establish a websocket connection are expired to `disconnected`
- `active` tunnels that no longer have an in-memory websocket session are reconciled back to `disconnected`

This keeps the admin dashboard aligned with real websocket session state, especially after crashes or stale registrations.
- `created_at`
- `connected_at`
- `disconnected_at`
- `last_activity_at`
- `total_request_bytes`
- `total_response_bytes`
- `request_count`
- `deleted_at`

#### `request_response_logs`
Stores request-response analytics for each tunneled request.

Key fields:

- request metadata (`method`, `path`, headers)
- response metadata (`status_code`, headers)
- byte counts
- previews for text payloads only
- timing information
- optional error message

#### `tunnel_creation_logs`
Stores tunnel creation attempts separately from request logs.

This is the recommended analytics split:

- request-response analytics live in `request_response_logs`
- tunnel creation analytics live in `tunnel_creation_logs`

### Persistence rules

- Keep request-response analytics in their own table.
- Keep tunnel creation analytics in their own table.
- Do not store full binary payloads in SQLite.
- Text previews are okay when truncated and clearly optional.
- Deleting a tunnel from the admin side should close the active connection but keep history unless the product requirements explicitly change.

---

## Admin dashboard

The admin UI is a Bun/Vite React SPA.

### Current stack

- React Router for `/admin` routes
- TanStack Query for fetching, mutation, invalidation, and polling
- ShadCN UI for layout, tables, dialogs, cards, sidebar, charts, and form primitives
- embedded build output served by the Go server

### Dashboard goals

- show active tunnel connections
- show tunnel details
- show request-response logs in detail
- show analytics charts for status classes and traffic

---

## Embedding model

The frontend source lives in `cmd/server/web`.
The built output lives in `cmd/server/web/dist`.
The Go server embeds that output in `cmd/server/assets.go`.

### Important rule

If you change the admin UI, rebuild it before building the Go server locally:

```bash
cd cmd/server/web && bun run build
```

The Docker build already performs this automatically.

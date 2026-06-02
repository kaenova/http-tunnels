# Architecture

## Overview

The repository is split into three main runtime surfaces:

1. **Tunnel client** (`http_tunnels.go` → `internal/client`)
2. **Tunnel server** (`cmd/server` → `internal/server`)
3. **Admin dashboard** (`cmd/server/web`)

The tunnel transport is now **HTTP/2-first with websocket fallback**.

- **Websocket** keeps the existing protobuf frame protocol and round-robin scheduler.
- **HTTP/2** now uses a separate H2-native worker-stream protocol instead of reusing websocket control/session semantics.

The Go server embeds the built admin dashboard assets from `cmd/server/web/dist` and serves them over a **direct TLS listener**. If no certificate files are configured, the server generates a self-signed certificate for local/testing use.

---

## Tunnel client

The client:

- registers a new tunnel with `POST /new_tunnel`
- prefers native HTTP/2 worker streams at `/tunnel/h2/stream` when HTTP/2 is available
- keeps a small pool of idle HTTP/2 worker streams open on one reused H2 connection
- falls back to the websocket tunnel at `/tunnel` when HTTP/2 is unavailable
- forwards assigned tunneled requests to the local destination server
- streams response data back to the server
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
4. serve the authenticated admin SPA and JSON admin API over direct TLS

### Public server routes

These routes are served on the server's direct HTTPS listener.

- `POST /new_tunnel` - create a tunnel registration
- `GET /tunnel` - websocket connection from the tunnel client
- `POST /tunnel/h2` - HTTP/2 worker stream alias
- `POST /tunnel/h2/stream` - HTTP/2 worker stream endpoint
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

### Websocket path

The websocket transport keeps the existing protobuf frame protocol over a **single long-lived websocket per active tunnel**.

Frame types remain:

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

### HTTP/2 path

The HTTP/2 transport is intentionally separate from the websocket interface.

It requires the app to receive the request on its **direct TLS/H2 listener**. If deployment infrastructure downgrades upstream requests to HTTP/1.1 before they reach the app, the native H2 worker path will not activate and clients should fall back to websocket.

It uses a pool of **dedicated worker streams** on one reused H2 connection:

- the client opens idle `POST /tunnel/h2/stream` worker requests
- the server assigns **exactly one** tunneled HTTP request to each worker stream
- request metadata/body flow from server → client over the H2 response body
- response metadata/body flow from client → server over the H2 request body
- closing the worker stream ends that tunneled request; the client then opens a replacement worker

This keeps HTTP/2 straightforward and fully multiplexed without a separate control stream.

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
- keep transport selection separate from request scheduling
- websocket still relies on the frame scheduler for fairness across concurrent requests
- HTTP/2 assigns one tunneled request per worker stream and relies on native H2 multiplexing instead of websocket-style control/session frames
- analytics ingestion still happens on the server in the normal request logging path regardless of transport

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
- show which active tunnels are on HTTP/2 versus websocket fallback
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

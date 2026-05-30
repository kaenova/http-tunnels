# http-tunnels v4 — Implementation Plan

## 1. Overview

Rewrite http-tunnels to use **gRPC + TCP-level tunneling**. Instead of parsing HTTP at the server level, we forward raw TCP streams between the browser and the backend through a gRPC tunnel. This makes the tunnel **protocol-agnostic** — HTTP/1.1, HTTP/2, WebSocket, gRPC, SSH, or any TCP-based protocol works transparently.

### Why TCP-level?

| Feature | HTTP-level (v3.x) | TCP-level (v4) |
|---------|------------------|----------------|
| HTTP/1.1 | ✅ | ✅ |
| HTTP/2 | ❌ (needs dual-mode) | ✅ |
| WebSocket | ❌ (special handling) | ✅ |
| gRPC | ❌ | ✅ |
| SSH, FTP, DB protocols | ❌ | ✅ |
| NPMPlus config | Complex | Simple |
| Server complexity | High (parse HTTP) | Low (forward bytes) |

### Note on TCP Data & Streaming

gRPC `Tunnel(stream TunnelMessage) returns (stream TunnelMessage)` is already a **bidirectional stream**. `TcpData` messages are sent as part of this stream — each message is one chunk of raw TCP data. Multiple `TcpData` messages are sent for a single TCP connection, one after another, until `TcpClose` terminates it. The streaming is built into gRPC itself, not something we need to implement separately.

---

## 2. Architecture

```
┌──────────────┐     ┌──────────────┐     ┌──────────────────┐
│   Browser    │────▶│  NPMPlus     │────▶│  tunnel-server   │
│ (any proto)  │     │  :443        │     │  :8443           │
│              │     │              │     │                  │
│ HTTP/1.1     │     │  proxy_pass  │     │  gRPC endpoint   │
│ WebSocket    │     │  (single     │     │  TCP forwarder   │
│ HTTP/2       │     │   location)  │     │  Admin web       │
│ SSH          │     │              │     └────────┬─────────┘
└──────────────┘     └──────────────┘              │
                                                   │ gRPC stream
                                                   ▼
                                          ┌──────────────────┐
                                          │  tunnel-client   │
                                          │  (in LXC/VPS)    │
                                          │                  │
                                          │  ┌────────────┐  │
                                          │  │  Backend    │  │
                                          │  │  Service    │  │
                                          │  │  (TCP)      │  │
                                          │  └────────────┘  │
                                          └──────────────────┘
```

### How it works:

1. **Tunnel client** connects to server via gRPC (`tunnel.TunnelService.Tunnel()`), registers with a subdomain and backend address.
2. **Browser** sends any TCP-based request to `subdomain.t.kaenova.my.id:443`.
3. **NPMPlus** proxies the raw TCP stream (via HTTP/1.1 with `proxy_pass`) to `tunnel-container:8443`.
4. **Server** receives the TCP connection. If it's gRPC (`Content-Type: application/grpc`), it handles tunnel control. Otherwise, it opens a new TCP stream through the gRPC tunnel to the client.
5. **Client** receives `TcpOpen`, connects to the backend TCP address, and starts forwarding data bidirectionally.
6. **Data flows** in both directions as raw bytes (`TcpData` chunks) until the connection closes (`TcpClose`).

---

## 3. Endpoints

| Endpoint | Protocol | Purpose |
|----------|----------|---------|
| `t.kaenova.my.id` | gRPC (`/tunnel.TunnelService/`) | Tunnel negotiation & TCP stream control |
| `t.kaenova.my.id` | TCP (via HTTP proxy) | Incoming connections proxied via tunnel |
| `t.kaenova.my.id/admin/` | HTTP/1.1 | Admin web dashboard |

All three share the **same port** (`:8443` internally, `:443` via NPMPlus). The server detects gRPC vs regular TCP by checking the first bytes / `Content-Type` header.

---

## 4. Protocol — gRPC TCP Tunneling

### Protobuf Definition

```protobuf
syntax = "proto3";
package tunnel;

service TunnelService {
  // Bidirectional tunnel stream
  rpc Tunnel(stream TunnelMessage) returns (stream TunnelMessage);
}

message TunnelMessage {
  oneof payload {
    // Registration
    RegisterRequest register = 1;
    RegisterResponse register_ack = 2;
    
    // TCP stream control
    TcpOpen open = 10;      // Server → Client: new TCP connection incoming
    TcpData data = 11;      // Bidirectional: raw TCP data chunk
    TcpClose close = 12;    // Bidirectional: TCP connection closed
    
    // Health check
    Ping ping = 30;
    Pong pong = 31;
  }
}

message RegisterRequest {
  string subdomain = 1;        // Requested subdomain (optional)
  string backend_host = 2;     // e.g. "127.0.0.1"
  int32 backend_port = 3;      // e.g. 5000
}

message RegisterResponse {
  string assigned_subdomain = 1;  // e.g. "vanburen-jennifer-3842"
  bool success = 2;
  string error = 3;
}

message TcpOpen {
  string connection_id = 1;  // Unique ID for this TCP connection
}

message TcpData {
  string connection_id = 1;
  bytes data = 2;
}

message TcpClose {
  string connection_id = 1;
  optional string error = 2;
}

message Ping {}
message Pong {}
```

### Stream Lifecycle

```
Client (gRPC)                  Server
  │                               │
  │──── Tunnel(stream) ──────────▶│  Connect
  │                               │
  │──── RegisterRequest ─────────▶│  "I handle backend 127.0.0.1:5000"
  │◀─── RegisterResponse ────────│  "Assigned: vanburen-jennifer-3842"
  │                               │
  │                               │  Browser connects to t.kaenova.my.id
  │                               │  Server accepts TCP connection
  │◀─── TcpOpen{conn_id:"c1"} ────│  "New connection from browser"
  │                               │
  │  Client connects to backend   │
  │  127.0.0.1:5000              │
  │                               │
  │◀─── TcpData{conn_id:"c1"} ────│  Browser → bytes
  │──── TcpData{conn_id:"c1"} ───▶│  Backend → bytes
  │◀─── TcpData{conn_id:"c1"} ────│  Browser → bytes
  │──── TcpData{conn_id:"c1"} ───▶│  Backend → bytes
  │                               │  (bidirectional, concurrent)
  │                               │
  │──── TcpClose{conn_id:"c1"} ──▶│  Backend closed connection
  │◀─── TcpClose{conn_id:"c1"} ───│  Browser closed connection
  │                               │
  │──── Ping ────────────────────▶│  Health check (30s interval)
  │◀─── Pong ────────────────────│
```

### Connection Multiplexing

Multiple TCP connections are multiplexed over a single gRPC stream using `connection_id`:

```
gRPC Stream
├── conn_id:"c1" ← Browser HTTP request to backend
├── conn_id:"c2" ← Browser WebSocket to backend  
├── conn_id:"c3" ← Another HTTP request
└── ...
```

Each `connection_id` is unique per tunnel session. The client maintains a map of `connection_id → net.Conn` for the backend connections.

---

## 5. Server Components

### 5.1 gRPC Server (`internal/grpc/`)

- Listens on `:8443` (TLS)
- Implements `TunnelService` gRPC service
- Manages active tunnel sessions (map of subdomain → gRPC stream)
- Handles client registration and subdomain assignment

### 5.2 TCP Listener & Forwarder (`internal/tcp/`)

- Accepts incoming TCP connections on the same `:8443` port
- Detects gRPC vs regular TCP:
  - If `Content-Type: application/grpc` → route to gRPC server
  - Otherwise → treat as incoming tunnel connection
- For each incoming TCP connection:
  1. Generate unique `connection_id`
  2. Send `TcpOpen` to the matching tunnel client via gRPC
  3. Read data from TCP socket, send `TcpData` to client
  4. Receive `TcpData` from client, write to TCP socket
  5. On close, send/receive `TcpClose`

### 5.3 Admin Web (`cmd/server/web/`)

**Already exists from v2** — React + Vite + TanStack Query SPA. Reuse as-is.

Existing pages:
- `/admin/auth/login` — Login page
- `/admin/` — Dashboard (active tunnels, requests, data transferred)
- `/admin/tunnels` — Tunnel list with pagination
- `/admin/tunnels/:tunnelId` — Tunnel detail with charts
- `/admin/request-activity` — Request logs with filters
- `/admin/request-activity/:requestId` — Request detail

Existing API endpoints the web expects:
- `GET /api/admin/auth/session`
- `POST /api/admin/auth/login`
- `GET /api/admin/dashboard`
- `GET /api/admin/request-activity?page=&pageSize=&search=&subdomain=&method=&statusClass=`
- `GET /api/admin/request-activity/:requestId`
- `GET /api/admin/tunnels?page=&pageSize=`
- `GET /api/admin/tunnels/:tunnelId?page=&pageSize=`
- `DELETE /api/admin/tunnels/:tunnelId`

### 5.4 Admin REST API (`internal/admin/`)

Implement the API endpoints that the existing web SPA expects. Data sourced from the gRPC session manager.

### 5.5 Admin Auth

Simple auth matching v2 implementation — controlled via environment variables:

| Env Var | Description | Default |
|---------|-------------|---------|
| `ADMIN_USER` | Admin username | `admin` |
| `ADMIN_PASS` | Admin password | Auto-generated on first run (logged to stdout) |

Auth flow:
1. `GET /api/admin/auth/session` — Returns `{ authenticated: false }` if not logged in
2. `POST /api/admin/auth/login` — Accepts `{ password: "..." }`, sets session cookie
3. Session stored in cookie (signed), no database needed

Reference: v2 admin web `api.ts` expects `AdminSession { authenticated, configured, message }`.

### 5.6 Certificate Management (`internal/tls/`)

- **Self-signed cert** generated on first run by default (stored in `TLS_CERT_DIR`)
- If `TLS_CERT_FILE` and `TLS_KEY_FILE` env vars are set, use those instead
- No Let's Encrypt integration needed — TLS termination happens at NPMPlus edge

| Env Var | Description | Default |
|---------|-------------|---------|
| `TLS_CERT_DIR` | Directory for self-signed certs | `./tls` |
| `TLS_CERT_FILE` | Custom cert path (optional) | — |
| `TLS_KEY_FILE` | Custom key path (optional) | — |

---

## 6. Client Components

### 6.1 gRPC Tunnel Client (`cmd/client/`)

```
http-tunnels [flags] <backend_host:backend_port>

Flags:
  -host string       Tunnel server host (default "https://t.kaenova.my.id")
  -subdomain string  Request specific subdomain (optional)
  -verbose           Enable verbose logging
```

Flow:
1. Connect to server via gRPC (TLS)
2. Send `RegisterRequest` with backend host + port
3. Receive `RegisterResponse` with assigned subdomain
4. Enter stream loop:
   - Receive `TcpOpen` → dial backend TCP → add to connection map
   - Receive `TcpData` → write to corresponding backend connection
   - Receive `TcpClose` → close backend connection
   - Read from backend connections → send `TcpData` to server
   - Backend connection closed → send `TcpClose` to server
5. Health check: periodic `Ping` (30s interval)

---

## 7. NPMPlus Configuration

Single location block — NPMPlus proxies everything to the tunnel container:

```nginx
location / {
    proxy_pass https://tunnel-container:8443;
    proxy_ssl_verify off;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_request_buffering off;
    proxy_buffering off;
}
```

Key settings:
- `proxy_request_buffering off` — streams request body immediately (needed for WebSocket, large uploads)
- `proxy_buffering off` — streams response body immediately (needed for SSE, streaming)
- `proxy_http_version 1.1` — required for WebSocket Upgrade
- `proxy_set_header Upgrade $http_upgrade` — forwards WebSocket upgrade

No special `grpc_pass` location needed. The server detects gRPC vs regular TCP internally.

---

## 8. Subdomain Name Generator

### Source Data

Names extracted from `1000randomnames.com` data sources (US Census last names + SSA given names). Each multi-word name is concatenated into a single word.

### Name Pool Categories

| Category | Source | Count | Example |
|----------|--------|-------|---------|
| Last Names | US Census 2010 top 1000 | ~1000 | `Smith`, `Johnson`, `Williams` |
| Female Names | SSA 2020 top 1000 | ~1000 | `Mary`, `Patricia`, `Jennifer` |
| Male Names | SSA 2020 top 1000 | ~1000 | `James`, `Robert`, `John` |

### Name Processing Rules

1. Take names with **2+ words** (e.g., `Van Buren`, `De La Cruz`, `Mary Ann`)
2. Concatenate into single word: `vanburen`, `delacruz`, `maryann`
3. Lowercase only
4. Remove non-alphanumeric characters (hyphens, apostrophes, etc.)

### Subdomain Pattern

```
{kata1}-{kata2}-{4digit}.t.kaenova.my.id
```

Where:
- `kata1` = randomly selected last name (single-word form)
- `kata2` = randomly selected first name (single-word form)
- `4digit` = random 0000-9999
- Total possible combinations: ~1000 × ~1000 × 10000 = **10 billion**

### Example Subdomains

| Generated | Readable |
|-----------|----------|
| `vanburen-jennifer-3842.t.kaenova.my.id` | Van Buren Jennifer |
| `delacruz-james-0193.t.kaenova.my.id` | De La Cruz James |
| `macdonald-patricia-7712.t.kaenova.my.id` | MacDonald Patricia |

### Implementation

- Name data stored as embedded JSON in Go binary
- Generator function: `GenerateSubdomain() string`
- Deterministic seeding optional (for repeatable names)
- Server assigns subdomain on client registration

---

## 9. File Structure

```
http-tunnels/
├── cmd/
│   ├── client/             # gRPC tunnel client
│   │   └── main.go
│   └── server/             # gRPC + TCP server
│       ├── main.go
│       └── web/            # Admin SPA (existing from v2)
│           ├── package.json
│           ├── src/
│           │   ├── App.tsx
│           │   ├── pages/
│           │   ├── components/
│           │   ├── hooks/
│           │   └── lib/
│           └── dist/
├── internal/
│   ├── grpc/               # gRPC service implementation
│   │   ├── server.go
│   │   ├── client.go
│   │   └── tunnel.pb.go    # Generated protobuf
│   ├── tcp/                # TCP listener & forwarder
│   │   └── forwarder.go
│   ├── admin/              # Admin REST API
│   │   └── api.go
│   ├── names/              # Subdomain name generator
│   │   ├── names.go
│   │   └── data.go         # Embedded name lists
│   └── tls/                # TLS utilities
│       └── tls.go
├── proto/
│   └── tunnel.proto        # Protobuf definition
├── PLAN.md                 # This file
├── Makefile
├── Dockerfile
└── go.mod
```

---

## 10. Implementation Order

| Phase | Task | Est. Time |
|-------|------|-----------|
| 1 | Protobuf definition + code generation | 1 day |
| 2 | gRPC server + client core (Tunnel stream, register) | 2 days |
| 3 | TCP listener + forwarder (detect gRPC vs TCP, TcpOpen/Data/Close) | 2 days |
| 4 | Subdomain name generator | 0.5 day |
| 5 | Admin REST API (matching existing web SPA) | 1 day |
| 6 | Integration test + deploy | 0.5 day |
| 7 | Documentation + release v4 | 0.5 day |

**Total: ~7.5 days**

---

## 11. Decision Log

| Question | Decision |
|----------|----------|
| Backward compatibility with v3 | ❌ No — v4 is a clean break. v3 clients won't work with v4 servers. |
| Subdomain migration | ❌ No — old subdomains (`dl6`, `cron-picoclaw`, etc.) are discarded. New generator assigns fresh names. |
| Admin auth | Environment variables (`ADMIN_USER`, `ADMIN_PASS`). Default `admin` with auto-generated password on first run. Cookie-based session (signed). |
| TLS | Self-signed by default (generated on first run). Custom cert via `TLS_CERT_FILE` / `TLS_KEY_FILE` env vars. Let's Encrypt not needed — TLS termination at NPMPlus edge. |
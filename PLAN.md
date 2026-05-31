# http-tunnels v5 — Implementation Plan

## 1. Overview

v5 kembali ke **WebSocket-based tunneling** (dari v2), dengan perubahan arsitektur utama: **multiplexing via multiple WebSocket connections** per request, bukan single WS dengan JSON frame multiplexing.

### Kenapa Multiplex WS?

| Aspek | v2 (1 WS, frame multiplex) | v5 (N WS, per-request) |
|-------|---------------------------|------------------------|
| Concurrency | Channel-based, manual backpressure | Built-in — tiap request punya TCP channel sendiri |
| Cancel request | Kirim frame `request_cancel` | Tutup WS dedicated-nya aja |
| Backpressure | Manual di application layer | TCP backpressure natural |
| Debugging | 1 koneksi, frame inspection | N koneksi, bisa di-track per request |
| Large upload/download | Bisa block request lain | Isolated, gak ganggu request lain |
| Overhead | 0 handshake tambahan | 1 WS handshake per request |

### Architecture

```
┌──────────┐     ┌──────────────┐     ┌──────────────┐     ┌──────────┐
│ Browser  │────▶│   Server     │────▶│   Client     │────▶│ Backend  │
│          │     │              │     │              │     │          │
│ HTTP     │     │ Main WS      │     │ Main WS      │     │ HTTP     │
│ request  │     │ (signalling) │     │ (signalling) │     │ proxy    │
│          │     │              │     │              │     │          │
│          │     │ Dedicated WS │     │ Dedicated WS │     │          │
│          │     │ (per req)    │     │ (per req)    │     │          │
└──────────┘     └──────────────┘     └──────────────┘     └──────────┘
```

---

## 2. Protocol Design

> **Note:** Semua frame menggunakan **binary encoding (protobuf)** via WebSocket `BinaryMessage`.  
> Detail schema ada di [Section 7](#7-protocol--binary-frames-protobuf).  
> Di bawah ini adalah logical flow-nya.

### 2.1 Main WebSocket (Signalling Channel)

Satu persistent WebSocket antara client dan server. Digunakan hanya untuk:

1. **Client registration** (initial message)
2. **Request notification** — server push `REQUEST` ke client
3. **Health check** — `PING`/`PONG`

#### Registration Flow

```
Client                                Server
  │                                     │
  │──REGISTER──────────────────────────▶│  domain, domain_key
  │                                     │
  │◀──REGISTERED────────────────────────│  tunnel_id, domain, config, server_message
  │                                     │
  │  [Main WS loop: REQUEST, PING/PONG] │
```

`REGISTERED` response includes full `TunnelConfig`:
- `max_concurrent` — max dedicated WS connections
- `request_timeout_ms` — timeout menunggu client buka dedicated WS
- `backend_timeout_ms` — timeout proxy ke backend
- `reconnect` — reconnect strategy config

#### Request Notification Flow

```
Server                                Client
  │                                     │
  │──REQUEST──────────────────────────▶│  request_id, method, path, headers, content_length
  │                                     │
  │◀──REQUEST_ACK──────────────────────│  request_id (client mulai handle)
  │                                     │
  │         [Client buka dedicated WS]  │
  │                                     │
  │  ...atau jika gagal:                │
  │◀──REQUEST_ERROR────────────────────│  request_id, status, error
```

### 2.2 Dedicated WebSocket (Per-Request Channel)

Setelah client terima `REQUEST`, client membuka **WebSocket baru** ke server.

#### Connection: Client → Server

```
wss://host/tunnel-response?request_id=req_abc123&domain_key=abc123...
```

#### Request dengan Body

```
Server                                Client
  │                                     │
  │──BODY (chunk)──────────────────────▶│  request body chunk
  │──BODY (chunk)──────────────────────▶│  request body chunk
  │──BODY_END──────────────────────────▶│  request body complete
  │                                     │
  │         [Client proxy ke backend]   │
  │                                     │
  │◀──RESPONSE_START────────────────────│  status, response_headers
  │◀──RESPONSE_BODY (chunk)────────────│  response body chunk
  │◀──RESPONSE_BODY (chunk)────────────│  response body chunk
  │◀──RESPONSE_END─────────────────────│  response complete
  │                                     │
  │  [WS close]                         │
```

#### Request tanpa Body (GET, HEAD, DELETE)

```
Server                                Client
  │                                     │
  │  [Langsung proxy, gak ada BODY]     │
  │                                     │
  │◀──RESPONSE_START────────────────────│
  │◀──RESPONSE_BODY─────────────────────│
  │◀──RESPONSE_END──────────────────────│
```

#### Error Response

```
Server                                Client
  │                                     │
  │◀──RESPONSE_ERROR───────────────────│  status, error
  │                                     │
  │  [WS close]                         │
```

### 2.3 WebSocket Upgrade Tunneling

Untuk request WebSocket dari browser:

1. Server kirim `REQUEST` dengan headers `upgrade: websocket`
2. Client buka dedicated WS, lakukan TCP dial + upgrade request ke backend
3. Jika backend respond `101`:
   - Client kirim `RESPONSE_START` dengan `status=101`
   - Server upgrade browser connection
   - Bidirectional `WS_DATA` frames via dedicated WS
4. `WS_CLOSE` dari sisi manapun → close connection

---

## 3. Flow Diagram

### 3.1 Normal HTTP Request

```
Browser          Server          Client(Main)    Client(Dedicated)   Backend
  │                │                │                │                  │
  │──GET /api────▶│                │                │                  │
  │                │──request─────▶│                │                  │
  │                │                │──connect WS──▶│                  │
  │                │◀──────────────│                │                  │
  │                │──body (if)───▶│                │                  │
  │                │                │                │──proxy────────▶│
  │                │                │                │◀──response─────│
  │                │◀──resp_start──│                │                  │
  │                │◀──resp_body───│                │                  │
  │                │◀──resp_end────│                │                  │
  │◀──HTTP 200────│                │                │                  │
  │                │                │──close WS─────▶│                  │
```

### 3.2 WebSocket Upgrade Request

```
Browser          Server          Client(Main)    Client(Dedicated)   Backend
  │                │                │                │                  │
  │──WS upgrade──▶│                │                │                  │
  │                │──request─────▶│                │                  │
  │                │                │──connect WS──▶│                  │
  │                │                │                │──TCP dial──────▶│
  │                │                │                │──upgrade req───▶│
  │                │                │                │◀──101───────────│
  │                │◀──resp_start──│                │                  │
  │◀──101─────────│                │                │                  │
  │                │                │                │                  │
  │──ws data─────▶│──ws_data──────▶│──ws_data──────▶│──tcp data──────▶│
  │◀──ws data─────│◀──ws_data──────│◀──ws_data──────│◀──tcp data──────│
  │                │                │                │                  │
  │──close───────▶│──ws_close─────▶│──ws_close─────▶│──close─────────▶│
```

---

## 4. Server Components

### 4.1 HTTP Router (`internal/server/app.go`)

Routing tetap sama seperti v2:

| Path | Handler |
|------|---------|
| `/ping` | Health check |
| `/new_tunnel` | Create tunnel (POST) |
| `/tunnel` | Main WS upgrade (signalling) |
| `/tunnel-response` | Dedicated WS upgrade (per-request) |
| `/api/admin/*` | Admin API |
| `/admin/*` | Admin SPA |
| `*.<host>` | Tunnel HTTP handler |

### 4.2 Tunnel HTTP Handler (`internal/server/handlers_tunnel.go`)

Saat HTTP request masuk untuk tunnel domain:

1. Generate `request_id`
2. Cek apakah ini WebSocket upgrade request
   - Jika ya: handle sebagai WS upgrade tunnel
   - Jika tidak: handle sebagai normal HTTP
3. Push `request` frame ke client via main WS
4. Tunggu client buka dedicated WS di `/tunnel-response?request_id=...`
5. Stream request body ke dedicated WS
6. Baca response dari dedicated WS
7. Kirim response ke browser
8. Timeout jika client gak response dalam N detik

### 4.3 Main WS Handler

```go
func (a *App) handleTunnelWebSocket(w http.ResponseWriter, r *http.Request) {
    // 1. Validate domain + domain_key
    // 2. Upgrade to WebSocket
    // 3. Read register message from client
    // 4. Store session (main WS connection)
    // 5. Read loop: hanya handle request_ack, request_error, ping/pong
    // 6. On disconnect: cleanup semua dedicated WS untuk tunnel ini
}
```

### 4.4 Dedicated WS Handler

```go
func (a *App) handleTunnelResponseWebSocket(w http.ResponseWriter, r *http.Request) {
    // 1. Validate request_id + domain_key
    // 2. Upgrade to WebSocket
    // 3. Cari pending request by request_id
    // 4. Baca: response_start, response_body, response_end, response_error
    // 5. Kirim: body, body_end (jika ada request body)
    // 6. On close: cleanup
}
```

### 4.5 Pending Request Store

```go
type PendingRequest struct {
    ID          string
    TunnelID    string
    Method      string
    Path        string
    Headers     map[string][]string
    Body        *io.PipeReader  // untuk stream request body
    BodyWriter  *io.PipeWriter
    ResponseCh  chan *Response
    ErrorCh     chan error
    CreatedAt   time.Time
    Done        chan struct{}
}

type PendingStore struct {
    mu       sync.RWMutex
    requests map[string]*PendingRequest
}
```

---

## 5. Client Components

### 5.1 Client Entry (`http_tunnels.go` / `internal/client/app.go`)

Flow client:

1. `POST /new_tunnel` → dapet `domain` + `domain_key`
2. Connect main WS ke `/tunnel?domain=...&domain_key=...`
3. Kirim register frame
4. Main WS loop: handle `request` notifications
5. Untuk setiap `request` notification:
   a. Buka dedicated WS ke `/tunnel-response?request_id=...&domain_key=...`
   b. Jika ada request body: baca `body` chunks dari dedicated WS
   c. Proxy request ke backend via HTTP
   d. Kirim response via dedicated WS
   e. Close dedicated WS
6. Reconnect main WS jika putus

### 5.2 Request Handler (per-request goroutine)

```go
func (c *Client) handleRequest(req *RequestNotification) {
    // 1. Connect dedicated WS
    ws, err := c.connectDedicatedWS(req.RequestID)
    if err != nil {
        c.sendRequestError(req.RequestID, 502, err.Error())
        return
    }
    defer ws.Close()

    // 2. Kirim request_ack via main WS
    c.sendRequestAck(req.RequestID)

    // 3. Baca request body dari dedicated WS (jika ada)
    bodyReader := c.readRequestBody(ws, req.ContentLength)

    // 4. Proxy ke backend
    resp, err := c.proxyToBackend(req, bodyReader)
    if err != nil {
        c.sendResponseError(ws, 502, err.Error())
        return
    }

    // 5. Kirim response
    c.sendResponseStart(ws, resp.StatusCode, resp.Header)
    c.streamResponseBody(ws, resp.Body)
    c.sendResponseEnd(ws)
}
```

---

## 6. File Structure (v5)

```
http-tunnels/
├── proto/
│   └── frame.proto              # Protobuf schema
├── cmd/
│   └── server/
│       ├── main.go              # Server entry
│       ├── assets.go            # Embedded admin web
│       └── web/                 # Admin SPA (React)
├── http_tunnels.go              # Client entry
├── internal/
│   ├── client/
│   │   ├── app.go               # Client: register, main WS, request handling
│   │   ├── ws.go                # Dedicated WS connection helper
│   │   ├── reconnect.go         # Exponential backoff reconnect logic
│   │   └── update.go            # Self-update
│   ├── protocol/
│   │   ├── connection.go        # WebSocket connection wrapper (binary frames)
│   │   ├── frame.go             # Frame types (generated from proto)
│   │   ├── frame.pb.go          # Generated protobuf code
│   │   └── http.go              # HTTP helpers
│   └── server/
│       ├── app.go               # Server HTTP handler, routing
│       ├── config.go            # Env vars config + tunnel config
│       ├── auth.go              # Admin auth
│       ├── handlers_tunnel.go   # Tunnel handlers (new_tunnel, main WS, dedicated WS)
│       ├── handlers_admin.go    # Admin API
│       ├── handlers_config.go   # Tunnel config API
│       ├── models.go            # Data models
│       ├── store.go             # SQLite store
│       ├── pending.go           # Pending request store
│       ├── tunnel.go            # TunnelSession + active request tracking
│       └── preview.go           # Body capture
├── PLAN.md                      # This file
├── go.mod
├── go.sum
├── Makefile                     # proto generation
├── Dockerfile
└── README.md
```

---

## 7. Protocol — Binary Frames (Protobuf)

Semua WebSocket messages menggunakan **binary frames** dengan **Protocol Buffers**.

### 7.1 WebSocket Setup

```go
// Server
var upgrader = websocket.Upgrader{
    EnableCompression: true,  // permessage-deflate
    CheckOrigin:       func(r *http.Request) bool { return true },
}

// Client
dialer := websocket.Dialer{
    EnableCompression: true,
}
```

### 7.2 Frame Structure (Protobuf)

```protobuf
syntax = "proto3";
package protocol;
option go_package = "github.com/kaenova/http-tunnels/internal/protocol";

message Frame {
  FrameType type = 1;
  string request_id = 2;

  // Request notification
  string method = 3;
  string path = 4;
  map<string, StringList> headers = 5;
  int64 content_length = 6;

  // Response
  int32 status = 7;
  map<string, StringList> response_headers = 8;

  // Body / data chunk (raw bytes, no base64 needed)
  bytes chunk = 9;

  // Error
  string error = 10;

  // Registration
  string domain = 11;
  string domain_key = 12;
  string tunnel_id = 13;
  string server_message = 14;

  // Tunnel config (sent on registration)
  TunnelConfig config = 15;
}

message StringList {
  repeated string values = 1;
}

message TunnelConfig {
  int32 max_concurrent = 1;
  int32 request_timeout_ms = 2;
  int32 backend_timeout_ms = 3;
  ReconnectConfig reconnect = 4;
}

message ReconnectConfig {
  bool enabled = 1;
  int32 initial_delay_ms = 2;
  int32 max_delay_ms = 3;
  double multiplier = 4;
  int32 max_retries = 5;
  bool jitter = 6;
}

enum FrameType {
  FRAME_TYPE_UNSPECIFIED = 0;

  // Main WS (signalling)
  REGISTER = 1;
  REGISTERED = 2;
  REQUEST = 3;
  REQUEST_ACK = 4;
  REQUEST_ERROR = 5;
  PING = 6;
  PONG = 7;

  // Dedicated WS (per-request)
  BODY = 10;
  BODY_END = 11;
  RESPONSE_START = 12;
  RESPONSE_BODY = 13;
  RESPONSE_END = 14;
  RESPONSE_ERROR = 15;
  WS_DATA = 16;
  WS_CLOSE = 17;
}
```

### 7.3 Frame Type Reference

#### Main WS Frames

| Frame Type | Direction | Key Fields | Purpose |
|-----------|-----------|------------|---------|
| `REGISTER` | Client → Server | `domain`, `domain_key` | Register tunnel |
| `REGISTERED` | Server → Client | `tunnel_id`, `domain`, `config`, `server_message` | Registration ACK + tunnel config |
| `REQUEST` | Server → Client | `request_id`, `method`, `path`, `headers`, `content_length` | New request notification |
| `REQUEST_ACK` | Client → Server | `request_id` | Client acknowledged request |
| `REQUEST_ERROR` | Client → Server | `request_id`, `status`, `error` | Client failed before opening dedicated WS |
| `PING` | Bidirectional | — | Health check |
| `PONG` | Bidirectional | — | Health check response |

#### Dedicated WS Frames

| Frame Type | Direction | Key Fields | Purpose |
|-----------|-----------|------------|---------|
| `BODY` | Server → Client | `request_id`, `chunk` | Request body chunk |
| `BODY_END` | Server → Client | `request_id` | Request body complete |
| `RESPONSE_START` | Client → Server | `request_id`, `status`, `response_headers` | Response status + headers |
| `RESPONSE_BODY` | Client → Server | `request_id`, `chunk` | Response body chunk |
| `RESPONSE_END` | Client → Server | `request_id` | Response complete |
| `RESPONSE_ERROR` | Client → Server | `request_id`, `status`, `error` | Error response |
| `WS_DATA` | Bidirectional | `request_id`, `chunk` | WebSocket tunnel data |
| `WS_CLOSE` | Bidirectional | `request_id` | WebSocket tunnel close |

### 7.4 Read/Write Pattern

```go
// Write frame
func (c *Connection) Send(frame *pb.Frame) error {
    data, err := proto.Marshal(frame)
    if err != nil {
        return err
    }
    return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

// Read frame
func (c *Connection) ReadFrame() (*pb.Frame, error) {
    _, data, err := c.conn.ReadMessage()
    if err != nil {
        return nil, err
    }
    var frame pb.Frame
    if err := proto.Unmarshal(data, &frame); err != nil {
        return nil, err
    }
    return &frame, nil
}
```

### 7.5 Benefits over JSON

| Aspect | JSON (v2) | Protobuf (v5) |
|--------|-----------|---------------|
| Encoding | Text (base64 for binary) | Binary (raw bytes) |
| Message size | Larger (field names repeated) | Compact (field numbers) |
| Parsing speed | Slower (reflection-based) | Faster (code-generated) |
| Schema enforcement | None (duck typing) | Compile-time (proto file) |
| Chunk encoding | base64 (+33% overhead) | raw bytes (0% overhead) |
| Compression synergy | permessage-deflate on text | permessage-deflate on binary |

---

## 8. Error Handling & Edge Cases

### 8.1 Client doesn't open dedicated WS
- Server timeout setelah 10 detik
- Kirim 504 Gateway Timeout ke browser

### 8.2 Dedicated WS connection fails
- Client kirim `request_error` via main WS
- Server kirim error response ke browser

### 8.3 Main WS disconnect
- Server cleanup semua dedicated WS untuk tunnel itu
- Semua pending request dibatalkan (kirim 502 ke browser)

### 8.4 Client reconnect
- Client reconnect main WS dengan domain_key yang sama
- Server replace session lama dengan yang baru
- Request yang sudah pending di-batalkan

### 8.5 Backend timeout
- Client timeout setelah 30 detik
- Kirim `response_error` via dedicated WS
- Server kirim 504 ke browser

---

## 9. Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `LISTEN_ADDR` | `:80` | Server listen address |
| `DB_PATH` | `http-tunnels.db` | SQLite database path |
| `SERVER_MESSAGE` | — | Optional message on tunnel creation |
| `WEB_PASSWORD` | — | Admin dashboard password |
| `WEB_SESSION_SECRET` | `WEB_PASSWORD` | Admin session secret |
| `COOKIE_SECURE` | `false` | Secure cookie flag |
| `REQUEST_TIMEOUT` | `10s` | Timeout waiting for client dedicated WS |
| `BACKEND_TIMEOUT` | `30s` | Timeout for backend proxy |

---

## 10. Implementation Order

| Phase | Task | Est. |
|-------|------|------|
| 1 | Simplify protocol frames (remove v2 frame types, add v5 frames) | 0.5d |
| 2 | Implement `PendingStore` + dedicated WS handler on server | 1d |
| 3 | Refactor main WS handler (signalling only) | 0.5d |
| 4 | Refactor tunnel HTTP handler (push request, wait dedicated WS) | 1d |
| 5 | Refactor client: main WS + per-request dedicated WS | 1.5d |
| 6 | WebSocket upgrade tunneling via dedicated WS | 0.5d |
| 7 | Error handling, timeouts, cleanup | 0.5d |
| 8 | Admin dashboard (reuse v2, minimal changes) | 0.5d |
| 9 | Testing + bugfix | 1d |
| 10 | Documentation + PLAN.md | 0.5d |

**Total: ~7 days**

---

## 11. Migration from v2

### Breaking Changes
- Protocol completely different — v2 clients won't work with v5 server
- Frame types berubah total
- Client harus implement dedicated WS per request

### Kept from v2
- SQLite store structure (tunnels, request_response_logs, tunnel_creation_logs)
- Admin dashboard (React SPA)
- Admin API endpoints
- Auth mechanism (cookie-based)
- Config environment variables
- `POST /new_tunnel` endpoint
- Self-update mechanism

### Removed from v2
- `responseStream` channel-based response handling
- `handleTunnelHTTP` single-WS multiplex logic
- Frame types: `request_body`, `request_end`, `request_cancel` (diganti dedicated WS)
- `ws_upgrade`, `ws_data`, `ws_close`, `ws_error` via main WS (pindah ke dedicated WS)

---

## 12. Decisions on Open Questions

### 12.1 Max Concurrent Dedicated WS

**Per-tunnel limit**, bisa dikonfigurasi:

- **Default global**: `MAX_CONCURRENT_REQUESTS=500` (env var, admin-set)
- **Per-tunnel override**: saat client register, bisa minta custom limit
- Admin bisa update limit per tunnel via API

Server tracking:

```go
type TunnelSession struct {
    TunnelID       string
    Domain         string
    Conn           *protocol.Connection  // main WS
    MaxConcurrent  int                   // per-tunnel limit
    activeRequests sync.Map              // request_id → dedicated WS conn
}

func (s *TunnelSession) CanAcceptRequest() bool {
    count := 0
    s.activeRequests.Range(func(_, _ any) bool {
        count++
        return true
    })
    return count < s.MaxConcurrent
}
```

Jika limit tercapai, server kirim `503 Service Unavailable` ke browser.

**Admin API untuk manage limit:**

```
GET  /api/admin/tunnels/:id/config        → lihat config tunnel
PUT  /api/admin/tunnels/:id/config        → update max_concurrent, timeout, dll
```

### 12.2 WebSocket Compression

Menggunakan **permessage-deflate** (RFC 7692) di semua WebSocket connections:

- **Main WS**: compression untuk signalling frame (ringan)
- **Dedicated WS**: compression untuk body chunks (signifikan untuk text-based payload)

Server side (gorilla/websocket):

```go
var upgrader = websocket.Upgrader{
    EnableCompression: true,
    CheckOrigin:       func(r *http.Request) bool { return true },
}
```

Client side:

```go
dialer := websocket.Dialer{
    EnableCompression: true,
}
```

### 12.3 Binary Frames

Ganti dari **JSON text frames** ke **binary frames** menggunakan **Protocol Buffers (protobuf)**:

**Kenapa protobuf:**
- Compact binary encoding — lebih hemat bandwidth
- Schema-first — type safety
- Backward/forward compatible
- Go native support (`google.golang.org/protobuf`)

**Frame structure:**

```protobuf
syntax = "proto3";
package protocol;

message Frame {
  FrameType type = 1;
  string request_id = 2;

  // Request notification
  string method = 3;
  string path = 4;
  map<string, StringList> headers = 5;
  int64 content_length = 6;

  // Response
  int32 status = 7;
  map<string, StringList> response_headers = 8;

  // Body / data chunk
  bytes chunk = 9;

  // Error
  string error = 10;

  // Registration
  string domain = 11;
  string domain_key = 12;
  string tunnel_id = 13;
  string server_message = 14;
}

message StringList {
  repeated string values = 1;
}

enum FrameType {
  FRAME_TYPE_UNSPECIFIED = 0;

  // Main WS
  REGISTER = 1;
  REGISTERED = 2;
  REQUEST = 3;
  REQUEST_ACK = 4;
  REQUEST_ERROR = 5;
  PING = 6;
  PONG = 7;

  // Dedicated WS
  BODY = 10;
  BODY_END = 11;
  RESPONSE_START = 12;
  RESPONSE_BODY = 13;
  RESPONSE_END = 14;
  RESPONSE_ERROR = 15;
  WS_DATA = 16;
  WS_CLOSE = 17;
}
```

**WebSocket message type:** `BinaryMessage` (bukan `TextMessage`)

```go
// Write
data, _ := proto.Marshal(frame)
conn.WriteMessage(websocket.BinaryMessage, data)

// Read
_, data, _ := conn.ReadMessage()
var frame protocol.Frame
proto.Unmarshal(data, &frame)
```

### 12.4 Request Body Streaming

✅ Dedicated WS secara natural support ini. Flow:

1. Server mulai stream `BODY` chunks begitu dedicated WS terbentuk
2. Client baca chunks sambil mulai proxy ke backend
3. Gak perlu nunggu semua body selesai

### 12.5 Reconnect Strategy

**Exponential backoff dengan jitter**, configurable per-tunnel:

| Config | Default | Description |
|--------|---------|-------------|
| `reconnect_enabled` | `true` | Enable auto-reconnect |
| `reconnect_initial_delay` | `1s` | Initial delay before first retry |
| `reconnect_max_delay` | `60s` | Maximum delay between retries |
| `reconnect_multiplier` | `2.0` | Exponential backoff multiplier |
| `reconnect_max_retries` | `0` (unlimited) | Max retry attempts, 0 = unlimited |
| `reconnect_jitter` | `true` | Add random jitter to delay |

**Formula delay:**
```
delay = min(initial_delay * multiplier^attempt, max_delay)
if jitter:
    delay = delay * (0.5 + random(0, 0.5))
```

**Client reconnect flow:**
1. Main WS disconnect → mulai reconnect loop
2. Exponential backoff antar attempt
3. Setiap reconnect: kirim ulang `REGISTER` frame
4. Server replace session lama → pending requests dibatalkan
5. Jika berhasil reconnect → reset backoff counter
6. Jika `max_retries` tercapai → client exit dengan error

**Per-tunnel override via admin API:**

```json
// PUT /api/admin/tunnels/:id/config
{
  "max_concurrent": 100,
  "request_timeout_ms": 15000,
  "backend_timeout_ms": 30000,
  "reconnect": {
    "enabled": true,
    "initial_delay_ms": 2000,
    "max_delay_ms": 120000,
    "multiplier": 2.0,
    "max_retries": 10,
    "jitter": true
  }
}
```

---

## 13. Updated Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `LISTEN_ADDR` | `:80` | Server listen address |
| `DB_PATH` | `http-tunnels.db` | SQLite database path |
| `SERVER_MESSAGE` | — | Optional message on tunnel creation |
| `WEB_PASSWORD` | — | Admin dashboard password |
| `WEB_SESSION_SECRET` | `WEB_PASSWORD` | Admin session secret |
| `COOKIE_SECURE` | `false` | Secure cookie flag |
| `MAX_CONCURRENT_REQUESTS` | `500` | Global default max concurrent requests per tunnel |
| `DEFAULT_REQUEST_TIMEOUT` | `10s` | Default timeout waiting for client dedicated WS |
| `DEFAULT_BACKEND_TIMEOUT` | `30s` | Default timeout for backend proxy |
| `DEFAULT_RECONNECT_ENABLED` | `true` | Default auto-reconnect |
| `DEFAULT_RECONNECT_INITIAL_DELAY` | `1s` | Default initial reconnect delay |
| `DEFAULT_RECONNECT_MAX_DELAY` | `60s` | Default max reconnect delay |
| `DEFAULT_RECONNECT_MULTIPLIER` | `2.0` | Default backoff multiplier |
| `DEFAULT_RECONNECT_MAX_RETRIES` | `0` | Default max retries (0=unlimited) |

### Tunnel Config (stored in DB, per-tunnel)

```go
type TunnelConfig struct {
    MaxConcurrent      int              `json:"max_concurrent"`
    RequestTimeoutMs   int              `json:"request_timeout_ms"`
    BackendTimeoutMs   int              `json:"backend_timeout_ms"`
    Reconnect          ReconnectConfig  `json:"reconnect"`
}

type ReconnectConfig struct {
    Enabled        bool    `json:"enabled"`
    InitialDelayMs int     `json:"initial_delay_ms"`
    MaxDelayMs     int     `json:"max_delay_ms"`
    Multiplier     float64 `json:"multiplier"`
    MaxRetries     int     `json:"max_retries"`
    Jitter         bool    `json:"jitter"`
}
```

### Registration Response (updated)

Server kirim config tunnel saat registration:

```json
{
  "type": "registered",
  "tunnel_id": "...",
  "domain": "abc123.t.kaenova.my.id",
  "server_message": "...",
  "config": {
    "max_concurrent": 500,
    "request_timeout_ms": 10000,
    "backend_timeout_ms": 30000,
    "reconnect": {
      "enabled": true,
      "initial_delay_ms": 1000,
      "max_delay_ms": 60000,
      "multiplier": 2.0,
      "max_retries": 0,
      "jitter": true
    }
  }
}
```

---

## 14. Updated Implementation Order

| Phase | Task | Est. |
|-------|------|------|
| 1 | Protobuf schema + code generation | 0.5d |
| 2 | Refactor protocol layer: binary frames, compression, protobuf | 1d |
| 3 | Implement `PendingStore` + dedicated WS handler on server | 1d |
| 4 | Implement per-tunnel config (max_concurrent, timeouts, reconnect) | 0.5d |
| 5 | Refactor main WS handler (signalling only) | 0.5d |
| 6 | Refactor tunnel HTTP handler (push request, wait dedicated WS, limit check) | 1d |
| 7 | Refactor client: main WS, reconnect with backoff, per-request dedicated WS | 1.5d |
| 8 | WebSocket upgrade tunneling via dedicated WS | 0.5d |
| 9 | Admin API: tunnel config management | 0.5d |
| 10 | Error handling, timeouts, cleanup | 0.5d |
| 11 | Admin dashboard updates (tunnel config UI) | 1d |
| 12 | Testing + bugfix | 1d |
| 13 | Documentation + PLAN.md | 0.5d |

**Total: ~10 days**

---

## 15. Testing Strategy

### 15.1 Unit Tests

#### Protocol Layer (`internal/protocol/`)

| Test | What it verifies |
|------|-----------------|
| `TestFrameMarshalUnmarshal` | Protobuf marshal/unmarshal round-trip untuk semua frame types |
| `TestFrameBinaryMessage` | Frame dikirim sebagai `websocket.BinaryMessage` (bukan TextMessage) |
| `TestCompressionEnabled` | `permessage-deflate` nego berhasil di client & server |
| `TestConnectionWriteLoop` | Write loop handles control vs data priority |
| `TestConnectionReadLoop` | Read loop correctly dispatches frame types |
| `TestConnectionClose` | Close cleanup: channels, conn, pending frames |
| `TestHTTPHelpers` | `CloneHeaders`, `ApplyHeaders`, `BuildDestinationURL`, `NormalizeHost` |

#### Server Components

| Test | What it verifies |
|------|-----------------|
| `TestPendingStoreAddGet` | Insert & retrieve pending request by ID |
| `TestPendingStoreTimeout` | Pending request expired after timeout |
| `TestPendingStoreCleanup` | Cleanup on tunnel disconnect removes all pending |
| `TestTunnelSessionCanAccept` | `CanAcceptRequest` returns false when limit reached |
| `TestTunnelSessionActiveTracking` | `activeRequests` map tracks dedicated WS correctly |
| `TestConfigLoad` | Env vars parsed correctly with defaults |
| `TestConfigValidate` | Missing required vars returns error |
| `TestAuthPassword` | `authenticatePassword` constant-time comparison |
| `TestAuthSession` | Set/validate/clear admin session cookie |
| `TestBodyCapture` | Preview limit, text vs binary content detection |

#### Client Components

| Test | What it verifies |
|------|-----------------|
| `TestReconnectBackoff` | Exponential delay calculation with jitter |
| `TestReconnectMaxRetries` | Client exits after max retries reached |
| `TestReconnectReset` | Successful reconnect resets backoff counter |
| `TestParseConfig` | CLI args + env vars parse correctly |
| `TestRequestHandler` | Full request-response cycle via mock WS |

### 15.2 Integration Tests

#### Test Harness

Go test binary that spins up:
1. **Test backend** — simple HTTP server (echo, streaming, WebSocket echo)
2. **Tunnel server** — full v5 server with SQLite in-memory (`:memory:`)
3. **Tunnel client** — connects to test server, proxies to test backend
4. **HTTP client** — makes requests through tunnel

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  Test HTTP   │────▶│  Tunnel      │────▶│  Tunnel      │────▶│  Test        │
│  Client      │     │  Server      │     │  Client      │     │  Backend     │
│  (net/http)  │     │  (:random)   │     │  (in-proc)   │     │  (:random)   │
└──────────────┘     └──────────────┘     └──────────────┘     └──────────────┘
```

#### Test Cases

| # | Test | Steps | Assertions |
|---|------|-------|------------|
| 1 | **Basic GET** | HTTP GET /api/hello → tunnel → backend echo | Status 200, body matches, headers forwarded |
| 2 | **POST with body** | HTTP POST /api/data + JSON body | Request body streamed correctly, response OK |
| 3 | **Large POST** | HTTP POST 10MB body | Chunked streaming works, no OOM, response correct |
| 4 | **Large GET response** | HTTP GET /api/large → backend returns 50MB | Response streamed, no buffering, content-length matches |
| 5 | **Custom headers** | Request with X-Custom-Header | Header forwarded to backend, response headers back |
| 6 | **Query parameters** | GET /api/search?q=test&page=2 | Query params preserved |
| 7 | **404 from backend** | GET /api/notfound | Status 404 forwarded, error body preserved |
| 8 | **500 from backend** | GET /api/error → backend panics | Status 500 forwarded |
| 9 | **Backend timeout** | GET /api/slow → backend sleeps 60s | Client timeout triggers, 504 returned |
| 10 | **Backend down** | Kill backend, then GET | `REQUEST_ERROR` sent, 502 returned |
| 11 | **Dedicated WS timeout** | Server sends REQUEST, client doesn't open dedicated WS | 504 after request_timeout_ms |
| 12 | **WebSocket upgrade** | Browser WS upgrade → tunnel → backend WS echo | 101 response, bidirectional WS_DATA works |
| 13 | **WebSocket upgrade fail** | WS upgrade to non-WS backend endpoint | Upgrade failed, error returned to browser |
| 14 | **Concurrent requests** | 20 simultaneous GET requests | All complete correctly, no data mixing |
| 15 | **Max concurrent limit** | Send 5 requests when max_concurrent=3 | First 3 work, 4th+ get 503 |
| 16 | **Client disconnect** | Kill client mid-request | Pending requests get 502, cleanup works |
| 17 | **Client reconnect** | Kill client, restart, verify new requests work | Old pending cleaned, new tunnel active |
| 18 | **Reconnect backoff** | Kill server, verify client retries with increasing delays | Delay follows exponential curve with jitter |
| 19 | **Multiple tunnels** | Register 3 tunnels, send requests to each | Requests route to correct tunnel |
| 20 | **Subdomain routing** | Request to abc.t.example.com vs xyz.t.example.com | Host-based routing works |
| 21 | **SSE streaming** | Backend sends text/event-stream | Chunks streamed in real-time via RESPONSE_BODY |
| 22 | **Binary response** | Backend returns image/png | Binary data intact, protobuf bytes field handles it |
| 23 | **Compression** | Verify permessage-deflate negotiated | WS handshake includes Sec-WebSocket-Extensions: permessage-deflate |

### 15.3 Stress / Load Tests

Dijalankan terpisah (bukan CI), untuk validasi performance:

| Test | Scenario | Metrics |
|------|----------|---------|
| **High concurrency** | 1000 concurrent requests, max_concurrent=500 | Latency p50/p99, error rate, memory usage |
| **Sustained load** | 100 req/s for 5 minutes | CPU, memory, goroutine count stability |
| **Large body throughput** | 100MB file upload/download | Throughput MB/s, memory ceiling |
| **Connection churn** | Client reconnect every 5s for 10min | No goroutine leak, no memory leak |
| **Many tunnels** | 100 tunnels, 10 req/s each | DB performance, routing latency |

### 15.4 Test Infrastructure

```go
// test/integration/harness.go
type TestHarness struct {
    Backend     *httptest.Server
    TunnelSrv   *server.App
    TunnelAddr  string
    HTTPClient  *http.Client
}

func NewHarness(t *testing.T) *TestHarness {
    // 1. Start test backend (echo server)
    backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Echo handler with configurable behavior
    }))

    // 2. Start tunnel server (in-memory SQLite)
    cfg := server.Config{
        ListenAddr:    "127.0.0.1:0", // random port
        DBPath:        ":memory:",
        WebPassword:   "test-password",
        SessionSecret: "test-secret",
    }
    app, _ := server.NewApp(cfg, nil)
    go app.Run()

    // 3. Start tunnel client (connect to test server)
    client := client.NewApp(client.Config{
        TunnelServer:      tunnelURL,
        DestinationServer: backendURL,
    })
    go client.Run()

    // 4. Create tunnel via API
    tunnel := createTunnel(t)

    return &TestHarness{...}
}
```

### 15.5 CI Pipeline

```yaml
# .github/workflows/test.yaml
name: Test
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'

      # Unit tests
      - name: Unit Tests
        run: go test ./internal/... -v -count=1

      # Integration tests
      - name: Integration Tests
        run: go test ./test/... -v -count=1 -timeout 120s

      # Race detector
      - name: Race Detection
        run: go test ./... -race -count=1 -timeout 120s

      # Coverage
      - name: Coverage
        run: go test ./... -coverprofile=coverage.out -covermode=atomic
```

### 15.6 Manual Smoke Test Script

```bash
#!/bin/bash
# test/smoke_test.sh
set -e

SERVER="http://localhost:8080"

echo "=== 1. Health check ==="
curl -s $SERVER/ping | jq .

echo "=== 2. Create tunnel ==="
TUNNEL=$(curl -s -X POST $SERVER/new_tunnel)
DOMAIN=$(echo $TUNNEL | jq -r .domain)
KEY=$(echo $TUNNEL | jq -r .domain_key)
echo "Tunnel: $DOMAIN"

echo "=== 3. Start client (background) ==="
go run . -host $SERVER -subdomain $(echo $DOMAIN | cut -d. -f1) http://localhost:3000 &
CLIENT_PID=$!
sleep 2

echo "=== 4. HTTP GET through tunnel ==="
curl -s -H "Host: $DOMAIN" $SERVER/api/test

echo "=== 5. HTTP POST through tunnel ==="
curl -s -X POST -H "Host: $DOMAIN" -d '{"hello":"world"}' $SERVER/api/echo

echo "=== 6. WebSocket through tunnel ==="
# Use websocat or similar

echo "=== 7. Cleanup ==="
kill $CLIENT_PID
echo "✅ Smoke test passed"
```

### 15.7 Test Coverage Targets

| Package | Target Coverage |
|---------|----------------|
| `internal/protocol` | 90%+ |
| `internal/server` | 80%+ |
| `internal/client` | 75%+ |
| Integration (e2e) | All 23 test cases must pass |
# PLAN_SIMPLIFY.md — Merge HTTP_PORT + ADMIN_PORT

## Goal

Merge `ADMIN_PORT` into `HTTP_PORT`. The server listens on **two ports**:

| Port | Env Var | Default | Purpose |
|------|---------|---------|---------|
| gRPC | `GRPC_PORT` | 8443 | gRPC tunnel (client connect) |
| HTTP | `HTTP_PORT` | 8080 | HTTP proxy + Admin web (path-based routing) |

> **💡 Single-port mode:** Set `GRPC_PORT` and `HTTP_PORT` to the same value to run everything on one port. The server detects gRPC vs HTTP traffic automatically by peeking at the first bytes of each connection. Ideal for deployments without NPMPlus where you want a single exposed port.

No more separate `ADMIN_PORT`.

## Architecture

```
Browser / NPMPlus
    │
    │  t.kaenova.my.id:443      → HTTP_PORT (admin + tunnel proxy)
    │  *.t.kaenova.my.id:443    → HTTP_PORT (tunnel proxy only)
    │
    ▼
┌─────────────────────────────────────┐
│         tunnel-server               │
│                                     │
│  ┌──────────────┐  ┌─────────────┐  │
│  │  GRPC_PORT   │  │  HTTP_PORT  │  │
│  │  :8443       │  │  :8080      │  │
│  │              │  │             │  │
│  │  gRPC Server │  │ HTTP Router │  │
│  │  (TLS)       │  │ (plain TCP) │  │
│  └──────────────┘  └──┬──────┬───┘  │
│                       │      │      │
│               /admin/ │      │ other│
│                       │      │      │
│               ┌───────▼──┐ ┌─▼────┐ │
│               │  Admin   │ │Tunnel│ │
│               │  Handler │ │Proxy │ │
│               └──────────┘ └──────┘ │
└─────────────────────────────────────┘
```

## HTTP Router Logic

At `HTTP_PORT`, the server accepts plain TCP connections, reads the first HTTP request line, and routes:

```
GET /admin/...        → Admin handler (serve SPA + API)
GET /api/admin/...    → Admin API handler
POST /api/admin/...   → Admin API handler
anything else         → TCP forwarder (tunnel proxy)
```

## Implementation Changes

### 1. Remove `ADMIN_PORT` env var

Only `GRPC_PORT` and `HTTP_PORT` remain.

### 2. New HTTP Router (`internal/http/router.go`)

```go
package http

import (
    "bufio"
    "net"
    "strings"
    "net/http"
    
    "github.com/kaenova/http-tunnels/internal/admin"
    "github.com/kaenova/http-tunnels/internal/grpc"
    "github.com/kaenova/http-tunnels/internal/tcp"
)

type Router struct {
    adminHandler http.Handler
    forwarder    *tcp.Forwarder
}

func NewRouter(tunnelServer *grpc.Server, adminHandler http.Handler) *Router {
    return &Router{
        adminHandler: adminHandler,
        forwarder:    tcp.NewForwarder(tunnelServer),
    }
}

func (r *Router) Serve(lis net.Listener) error {
    for {
        conn, err := lis.Accept()
        if err != nil {
            return err
        }
        go r.handleConn(conn)
    }
}

func (r *Router) handleConn(conn net.Conn) {
    defer conn.Close()
    
    peeked := bufio.NewReader(conn)
    line, err := peeked.ReadString('\n')
    if err != nil {
        return
    }
    line = strings.TrimSpace(line)
    
    // Parse method and path from request line
    parts := strings.SplitN(line, " ", 3)
    if len(parts) < 2 {
        return
    }
    
    path := parts[1]
    
    if strings.HasPrefix(path, "/admin/") || strings.HasPrefix(path, "/api/admin/") {
        // Admin request — serve via HTTP
        r.serveHTTP(conn, line, peeked)
    } else {
        // Tunnel proxy
        r.forwarder.HandleConn(conn, peeked)
    }
}
```

### 3. Env Vars (simplified)

| Var | Default | Purpose |
|-----|---------|---------|
| `GRPC_PORT` | 8443 | gRPC tunnel server (TLS) |
| `HTTP_PORT` | 8080 | HTTP proxy + Admin web (plain TCP) |
| `ADMIN_USER` | admin | Admin username |
| `ADMIN_PASS` | auto-gen | Admin password |
| `TLS_CERT_DIR` | tls | TLS cert directory |
| `TLS_CERT_FILE` | — | Custom cert (optional) |
| `TLS_KEY_FILE` | — | Custom key (optional) |

### 4. NPMPlus Config

```nginx
# t.kaenova.my.id → HTTP_PORT (admin + tunnel proxy)
server {
    listen 443 ssl;
    server_name t.kaenova.my.id;
    
    location / {
        proxy_pass http://tunnel-container:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_request_buffering off;
        proxy_buffering off;
    }
}

# *.t.kaenova.my.id → HTTP_PORT (tunnel proxy only)
server {
    listen 443 ssl;
    server_name *.t.kaenova.my.id;
    
    location / {
        proxy_pass http://tunnel-container:8080;
        proxy_set_header Host $host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_request_buffering off;
        proxy_buffering off;
    }
}
```

Note: `HTTP_PORT` uses plain HTTP (no TLS) because NPMPlus handles TLS termination at the edge.

### 5. gRPC Client

Client tetap connect ke `GRPC_PORT` (8443) via TLS:

```
http-tunnels -host https://t.kaenova.my.id:8443 127.0.0.1:5000
```

Atau lewat NPMPlus kalo port 8443 gak exposed langsung:

NPMPlus proxy gRPC ke `GRPC_PORT`:
```nginx
location /tunnel.TunnelService/ {
    grpc_pass grpcs://tunnel-container:8443;
    grpc_ssl_verify off;
}
```

## Files to Change

| File | Change |
|------|--------|
| `cmd/server/main.go` | Remove `ADMIN_PORT`, use HTTP router on `HTTP_PORT` |
| `internal/http/router.go` | **New** — HTTP path-based router |
| `internal/tcp/forwarder.go` | Export `HandleConn(conn, peeked)` for HTTP router |
| `internal/admin/api.go` | No change |

## Test Plan

1. **gRPC tunnel**: client connect via `https://127.0.0.1:8443` → register → OK
2. **Admin web**: `curl http://127.0.0.1:8080/api/admin/auth/session` → `{"authenticated":false}`
3. **Tunnel proxy**: `curl http://127.0.0.1:8080/api/jobs` → backend JSON response
4. **All on HTTP_PORT**: verify admin + tunnel proxy work on same port 8080
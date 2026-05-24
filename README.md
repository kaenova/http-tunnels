# HTTP Tunnels

A lightweight Go-based HTTP tunneling service with:

- streaming tunnel support for **SSE** and **binary downloads**
- a self-updating CLI via `http-tunnels update`
- an authenticated **admin dashboard** built with **React + Bun + ShadCN**
- persistent analytics and request-response logs stored in **SQLite**

If you want to support the hosted public tunnel service, consider donating via [Trakteer](https://trakteer.id/kaenova/tip).

---

## Features

- Create tunnels with a custom or random subdomain.
- Stream responses through the tunnel without buffering the full body first.
- Support **SSE / event-stream** workloads.
- Support **binary streaming** for file downloads and other non-text payloads.
- Persist tunnel registrations, tunnel creation attempts, and request-response analytics.
- Browse active tunnels and detailed logs from `/admin`.
- Authenticate the admin dashboard with `WEB_PASSWORD`.
- Update the client binary in place with `http-tunnels update`.

---

## Repository layout

- `http_tunnels.go` - CLI entrypoint for the tunnel client.
- `cmd/server` - tunnel server entrypoint and embedded admin web assets.
- `cmd/server/web` - Bun/Vite React admin dashboard source.
- `internal/client` - tunnel client logic and self-update implementation.
- `internal/server` - tunnel server, admin API, auth, and SQLite store.
- `internal/protocol` - shared tunnel websocket streaming protocol.
- `.agents/guidelines` - implementation patterns for this repository.

Before changing the codebase, read `.agents/guidelines/README.md` and follow the documented patterns.

---

## Requirements

### Client

- Go 1.23+
- or a released binary from GitHub Releases

### Server

- Go 1.23+
- [Bun](https://bun.sh) to build the admin web assets locally
- wildcard DNS pointing to the tunnel server host

---

## Install the client

### Option 1: Install with Go

```bash
go install github.com/kaenova/http-tunnels@latest
```

### Option 2: Download a release binary

Download the matching archive from:

- <https://github.com/kaenova/http-tunnels/releases>

Available release assets follow this pattern:

- `http-tunnels-darwin-amd64.tar.gz`
- `http-tunnels-darwin-arm64.tar.gz`
- `http-tunnels-linux-amd64.tar.gz`
- `http-tunnels-linux-arm64.tar.gz`
- `http-tunnels-windows-amd64.zip`
- `http-tunnels-windows-arm64.zip`

---

## Use the client

By default the client points to the public hosted tunnel service.

```bash
http-tunnels http://localhost:3000
```

Use a custom tunnel host:

```bash
http-tunnels -host https://tunnel.example.com http://localhost:3000
```

Request a custom subdomain:

```bash
http-tunnels -host https://tunnel.example.com -subdomain myapp http://localhost:3000
```

### Self-update the client

```bash
http-tunnels update
```

The update command:

1. detects your current OS and architecture
2. fetches the latest GitHub release for `kaenova/http-tunnels`
3. downloads the matching release asset
4. replaces the running client binary in place

---

## Run the server locally

### 1. Build the admin dashboard assets

```bash
cd cmd/server/web
bun install
bun run build
cd ../../..
```

### 2. Build the server

```bash
go build -o http-tunnels-server ./cmd/server
```

### 3. Run the server

```bash
WEB_PASSWORD=change-me \
WEB_SESSION_SECRET=change-me-too \
./http-tunnels-server
```

Then open:

- `http://<your-host>/ping`
- `http://<your-host>/admin/auth/login`

---

## Run with Docker

The Docker build automatically builds the Bun admin app and embeds it into the Go server binary.

```bash
docker build -t http-tunnels .
```

```bash
docker run --rm \
  -p 80:80 \
  -e WEB_PASSWORD=change-me \
  -e WEB_SESSION_SECRET=change-me-too \
  -e DB_PATH=/data/http-tunnels.db \
  -v $(pwd)/data:/data \
  http-tunnels
```

---

## Admin dashboard

### Authentication

The admin UI is protected by `WEB_PASSWORD`.

Main admin routes:

- `/admin/auth/login` - login page
- `/admin/auth/logout` - clears the admin cookie and redirects to login
- `/admin` - dashboard overview
- `/admin/tunnels` - paginated active subdomain list
- `/admin/tunnels/:tunnelId` - tunnel detail, analytics, and request logs
- `/admin/request-activity` - filtered request activity list across all tunnels
- `/admin/request-activity/:requestId` - request-response detail view

### What the dashboard shows

- active connections
- registered / pending active tunnels
- request-response logs
- filtered request activity across all tunnels
- tunnel creation logs
- transferred bytes
- status code analytics (2XX / 3XX / 4XX / 5XX)
- inbound vs outbound traffic charts

---

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `TUNNEL_HOST` | `https://t.kaenova.my.id` | Default client tunnel host. |
| `LISTEN_ADDR` | `:80` | Server listen address. |
| `DB_PATH` | `http-tunnels.db` | SQLite database file path. |
| `SERVER_MESSAGE` | _empty_ | Optional message returned during tunnel registration. |
| `WEB_PASSWORD` | _empty_ | Required to log in to the admin dashboard. |
| `WEB_SESSION_SECRET` | `WEB_PASSWORD` | HMAC secret used for the admin auth cookie. |
| `COOKIE_SECURE` | `false` | Set `true` when serving the admin app behind HTTPS. |

---

## Streaming behavior

The tunnel protocol now streams request and response bodies in chunks over the websocket tunnel.

That means the service can forward:

- `text/event-stream` responses for SSE
- large binary downloads
- request uploads without buffering the full payload in memory first

Request-response analytics are still recorded while the stream is in flight.

---

## Development notes

### Admin frontend

```bash
cd cmd/server/web
bun install
bun dev
```

### Backend

```bash
go run ./cmd/server
```

### Client

```bash
go run . http://localhost:3000
```

If you change the admin frontend and want the Go server binary to embed the latest assets, rebuild the frontend first:

```bash
cd cmd/server/web && bun run build
```

---

## Documentation for contributors and agents

- Read `.agents/guidelines/README.md` first.
- If you change the established implementation pattern, update the guideline documents too.
- See `AGENTS.md` for repository-specific working rules.

---

## License

This project is licensed under the MIT License.

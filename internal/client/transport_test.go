package client

import (
	"context"
	"crypto/tls"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/protocol"
	"github.com/kaenova/http-tunnels/internal/server"
)

func TestConnectAndRegisterPrefersHTTP2(t *testing.T) {
	appServer, testServer := newTunnelTransportTestServer(t, true)
	defer appServer.Close()
	defer testServer.Close()

	app := newTransportTestClientApp(t, testServer)
	defer app.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	registration, err := app.createTunnel(ctx, "prefers-h2")
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	if err := app.connectAndRegister(ctx, registration); err != nil {
		t.Fatalf("connect and register: %v", err)
	}
	if app.transport != protocol.TransportHTTP2 {
		t.Fatalf("expected transport %q, got %q", protocol.TransportHTTP2, app.transport)
	}
}

func TestConnectAndRegisterFallsBackToWebSocketWhenHTTP2Unavailable(t *testing.T) {
	appServer, testServer := newTunnelTransportTestServer(t, false)
	defer appServer.Close()
	defer testServer.Close()

	app := newTransportTestClientApp(t, testServer)
	defer app.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	registration, err := app.createTunnel(ctx, "fallback-ws")
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	if err := app.connectAndRegister(ctx, registration); err != nil {
		t.Fatalf("connect and register: %v", err)
	}
	if app.transport != protocol.TransportWebSocket {
		t.Fatalf("expected transport %q, got %q", protocol.TransportWebSocket, app.transport)
	}
}

func newTunnelTransportTestServer(t *testing.T, enableHTTP2 bool) (*server.App, *httptest.Server) {
	t.Helper()

	config := server.Config{
		ListenAddr:                   "127.0.0.1:0",
		DBPath:                       ":memory:",
		WebPassword:                  "test-password",
		SessionSecret:                "test-secret",
		TunnelDomain:                 "example.test",
		MaxConcurrentRequests:        100,
		DefaultRequestTimeout:        10000,
		DefaultBackendTimeout:        30000,
		DefaultReconnectEnabled:      true,
		DefaultReconnectInitialDelay: 1000,
		DefaultReconnectMaxDelay:     60000,
		DefaultReconnectMultiplier:   2.0,
		DefaultReconnectMaxRetries:   0,
	}
	appServer, err := server.NewApp(config, fs.FS(nil))
	if err != nil {
		t.Fatalf("new server app: %v", err)
	}

	testServer := httptest.NewUnstartedServer(appServer.Handler())
	testServer.EnableHTTP2 = enableHTTP2
	testServer.StartTLS()
	return appServer, testServer
}

func newTransportTestClientApp(t *testing.T, srv *httptest.Server) *App {
	t.Helper()
	serverURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	backendURL, err := url.Parse("http://127.0.0.1:9999")
	if err != nil {
		t.Fatalf("parse backend url: %v", err)
	}

	transport := srv.Client().Transport.(*http.Transport)
	return &App{
		serverURL:  serverURL,
		backendURL: backendURL,
		httpClient: srv.Client(),
		wsDialer: &websocket.Dialer{
			EnableCompression: true,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				ServerName:         transport.TLSClientConfig.ServerName,
			},
		},
		outbound:   protocol.NewFrameScheduler(protocol.DefaultPerRequestFrameQueue),
		done:       make(chan struct{}),
		requests:   make(map[string]*activeRequest),
		h2Streams:  make(map[*protocol.H2TunnelStream]struct{}),
		lastActive: time.Now(),
	}
}

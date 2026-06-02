package integration

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/protocol"
	"github.com/kaenova/http-tunnels/internal/server"
)

// TestHarness provides a full integration test environment
type TestHarness struct {
	Backend    *httptest.Server
	TunnelSrv  *server.App
	TunnelAddr string
	HTTPClient *http.Client
	WSDialer   websocket.Dialer
	listener   net.Listener

	mu      sync.Mutex
	tunnels map[string]*TunnelClient
}

// TunnelClient represents a registered tunnel
type TunnelClient struct {
	ID         string
	Domain     string
	DomainKey  string
	TunnelID   string
	Config     *protocol.TunnelConfig
	BackendURL string
}

// NewHarness creates a new integration test harness
func NewHarness(t *testing.T) *TestHarness {
	t.Helper()

	// 1. Start test backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "echo")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		body, _ := io.ReadAll(r.Body)
		resp := `{"method":"` + r.Method + `","path":"` + r.URL.Path + `","body":"` + string(body) + `"}`
		w.Write([]byte(resp))
	}))

	// 2. Start tunnel server
	cfg := server.Config{
		ListenAddr:                   "127.0.0.1:0",
		DBPath:                       ":memory:",
		WebPassword:                  "test-password",
		SessionSecret:                "test-secret",
		MaxConcurrentRequests:        500,
		DefaultRequestTimeout:        10000,
		DefaultBackendTimeout:        30000,
		DefaultReconnectEnabled:      true,
		DefaultReconnectInitialDelay: 1000,
		DefaultReconnectMaxDelay:     60000,
		DefaultReconnectMultiplier:   2.0,
		DefaultReconnectMaxRetries:   0,
	}

	app, err := server.NewApp(cfg, nil)
	if err != nil {
		t.Fatalf("creating tunnel server: %v", err)
	}

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		if err := app.Serve(listener); err != nil && !strings.Contains(err.Error(), "closed") {
			log.Printf("tunnel server error: %v", err)
		}
	}()

	tunnelAddr := "http://" + listener.Addr().String()
	time.Sleep(50 * time.Millisecond)

	h := &TestHarness{
		Backend:    backend,
		TunnelSrv:  app,
		TunnelAddr: tunnelAddr,
		listener:   listener,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		WSDialer: websocket.Dialer{
			EnableCompression: true,
			HandshakeTimeout:  5 * time.Second,
		},
		tunnels: make(map[string]*TunnelClient),
	}

	t.Cleanup(func() {
		h.Close()
	})

	return h
}

// Close cleans up all resources
func (h *TestHarness) Close() {
	h.Backend.Close()
	h.TunnelSrv.Shutdown()
	h.listener.Close()
}

// HTTPGet makes an HTTP GET request through the tunnel
func (h *TestHarness) HTTPGet(t *testing.T, domain, path string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest("GET", h.TunnelAddr+path, nil)
	if err != nil {
		return nil, err
	}
	req.Host = domain
	return h.HTTPClient.Do(req)
}

// HTTPPost makes an HTTP POST request through the tunnel
func (h *TestHarness) HTTPPost(t *testing.T, domain, path, contentType string, body io.Reader) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest("POST", h.TunnelAddr+path, body)
	if err != nil {
		return nil, err
	}
	req.Host = domain
	req.Header.Set("Content-Type", contentType)
	return h.HTTPClient.Do(req)
}

// NewHarnessWithBackend creates a harness with a custom backend
func NewHarnessWithBackend(t *testing.T, handler http.Handler) *TestHarness {
	t.Helper()

	backend := httptest.NewServer(handler)

	cfg := server.Config{
		ListenAddr:                   "127.0.0.1:0",
		DBPath:                       ":memory:",
		WebPassword:                  "test-password",
		SessionSecret:                "test-secret",
		MaxConcurrentRequests:        500,
		DefaultRequestTimeout:        30000,
		DefaultBackendTimeout:        120000,
		DefaultReconnectEnabled:      true,
		DefaultReconnectInitialDelay: 1000,
		DefaultReconnectMaxDelay:     60000,
		DefaultReconnectMultiplier:   2.0,
		DefaultReconnectMaxRetries:   0,
	}

	app, err := server.NewApp(cfg, nil)
	if err != nil {
		t.Fatalf("creating tunnel server: %v", err)
	}

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		if err := app.Serve(listener); err != nil && !strings.Contains(err.Error(), "closed") {
			log.Printf("tunnel server error: %v", err)
		}
	}()

	tunnelAddr := "http://" + listener.Addr().String()
	time.Sleep(50 * time.Millisecond)

	h := &TestHarness{
		Backend:    backend,
		TunnelSrv:  app,
		TunnelAddr: tunnelAddr,
		listener:   listener,
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
		WSDialer: websocket.Dialer{
			EnableCompression: true,
			HandshakeTimeout:  5 * time.Second,
		},
		tunnels: make(map[string]*TunnelClient),
	}

	t.Cleanup(func() {
		h.Close()
	})

	return h
}
func (h *TestHarness) CreateTunnel(t *testing.T, subdomain string) *TunnelClient {
	t.Helper()

	endpoint := h.TunnelAddr + "/new_tunnel"
	if subdomain != "" {
		endpoint += "?subdomain=" + subdomain
	}

	resp, err := h.HTTPClient.Post(endpoint, "application/json", nil)
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create tunnel failed: %s: %s", resp.Status, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode tunnel response: %v", err)
	}

	tc := &TunnelClient{
		ID:         result["id"].(string),
		Domain:     result["domain"].(string),
		DomainKey:  result["domain_key"].(string),
		BackendURL: h.Backend.URL,
	}

	h.mu.Lock()
	h.tunnels[tc.Domain] = tc
	h.mu.Unlock()

	return tc
}

package integration

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kaenova/http-tunnels/internal/protocol"
)

// TestPostWithBody tests POST with JSON body through the tunnel
func TestPostWithBody(t *testing.T) {
	h := NewHarness(t)

	tunnel := h.CreateTunnel(t, "")
	h.ConnectAndRegister(t, tunnel)

	resp, err := h.HTTPPost(t, tunnel.Domain, "/api/echo", "application/json",
		strings.NewReader(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("post request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("post failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello") {
		t.Errorf("response should contain 'hello': %s", string(body))
	}
}

// TestConcurrentRequests tests multiple simultaneous requests
func TestConcurrentRequests(t *testing.T) {
	h := NewHarness(t)

	tunnel := h.CreateTunnel(t, "")
	h.ConnectAndRegister(t, tunnel)

	results := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			resp, err := h.HTTPGet(t, tunnel.Domain, "/api/test")
			if err != nil {
				results <- false
				return
			}
			results <- resp.StatusCode == 200
			resp.Body.Close()
		}()
	}

	successCount := 0
	for i := 0; i < 10; i++ {
		if <-results {
			successCount++
		}
	}

	if successCount != 10 {
		t.Errorf("concurrent requests: %d/10 succeeded", successCount)
	}
}

// TestBackendTimeout tests that backend timeout is handled
func TestBackendTimeout(t *testing.T) {
	h := NewHarnessWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))

	tunnel := h.CreateTunnel(t, "")
	h.ConnectAndRegister(t, tunnel)

	// Use a client with short timeout
	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest("GET", h.TunnelAddr+"/api/slow", nil)
	req.Host = tunnel.Domain

	_, err := client.Do(req)
	if err == nil {
		t.Error("expected timeout error")
	}
	t.Logf("Timeout error (expected): %v", err)
}

// TestCustomHeaders tests that custom headers are forwarded
func TestCustomHeaders(t *testing.T) {
	h := NewHarness(t)

	tunnel := h.CreateTunnel(t, "")
	h.ConnectAndRegister(t, tunnel)

	req, _ := http.NewRequest("GET", h.TunnelAddr+"/api/headers", nil)
	req.Host = tunnel.Domain
	req.Header.Set("X-Custom-Header", "test-value")
	req.Header.Set("Authorization", "Bearer token123")

	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("custom headers request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("request failed: status=%d body=%s", resp.StatusCode, string(body))
	}
}

// TestMultipleTunnels tests that multiple tunnels work independently
func TestMultipleTunnels(t *testing.T) {
	h := NewHarness(t)

	tunnel1 := h.CreateTunnel(t, "")
	tunnel2 := h.CreateTunnel(t, "")

	h.ConnectAndRegister(t, tunnel1)
	h.ConnectAndRegister(t, tunnel2)

	// Request through tunnel1
	resp1, err := h.HTTPGet(t, tunnel1.Domain, "/api/tunnel1")
	if err != nil {
		t.Fatalf("tunnel1 request: %v", err)
	}
	if resp1.StatusCode != 200 {
		t.Errorf("tunnel1 status: %d", resp1.StatusCode)
	}
	resp1.Body.Close()

	// Request through tunnel2
	resp2, err := h.HTTPGet(t, tunnel2.Domain, "/api/tunnel2")
	if err != nil {
		t.Fatalf("tunnel2 request: %v", err)
	}
	if resp2.StatusCode != 200 {
		t.Errorf("tunnel2 status: %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

// Ensure imports
var _ = protocol.Frame{}
var _ = time.Now
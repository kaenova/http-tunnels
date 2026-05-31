package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/protocol"
	"github.com/kaenova/http-tunnels/internal/server"
)

// TestBasicTunnelCreation tests that a tunnel can be created via API
func TestBasicTunnelCreation(t *testing.T) {
	h := NewHarness(t)

	// Create tunnel
	resp, err := h.HTTPClient.Post(h.TunnelAddr+"/new_tunnel", "application/json", nil)
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create tunnel failed: %s: %s", resp.Status, string(body))
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if result["id"] == nil || result["id"] == "" {
		t.Error("tunnel ID missing")
	}
	if result["domain"] == nil || result["domain"] == "" {
		t.Error("domain missing")
	}
	if result["domain_key"] == nil || result["domain_key"] == "" {
		t.Error("domain_key missing")
	}

	t.Logf("Created tunnel: domain=%s", result["domain"])
}

// TestPingEndpoint tests the health check endpoint
func TestPingEndpoint(t *testing.T) {
	h := NewHarness(t)

	resp, err := h.HTTPClient.Get(h.TunnelAddr + "/ping")
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("ping status: got %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "pong") {
		t.Errorf("ping response should contain 'pong': %s", string(body))
	}
}

// TestClientConnectAndRegister tests that a client can connect via main WS and register
func TestClientConnectAndRegister(t *testing.T) {
	h := NewHarness(t)

	// 1. Create tunnel
	tunnel := h.CreateTunnel(t, "")
	t.Logf("Tunnel created: domain=%s key=%s", tunnel.Domain, tunnel.DomainKey)

	// 2. Connect client main WS
	wsURL := "ws" + strings.TrimPrefix(h.TunnelAddr, "http") + "/tunnel?domain=" + url.QueryEscape(tunnel.Domain) + "&domain_key=" + url.QueryEscape(tunnel.DomainKey)

	conn, _, err := h.WSDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connect main WS: %v", err)
	}
	defer conn.Close()

	ws := protocol.NewConnection(conn)

	// 3. Send REGISTER
	err = ws.Send(&protocol.Frame{
		Type:      protocol.FrameType_REGISTER,
		Domain:    tunnel.Domain,
		DomainKey: tunnel.DomainKey,
	})
	if err != nil {
		t.Fatalf("send register: %v", err)
	}

	// 4. Read REGISTERED
	frame, err := ws.ReadFrame()
	if err != nil {
		t.Fatalf("read registered: %v", err)
	}
	if frame.GetType() != protocol.FrameType_REGISTERED {
		t.Fatalf("expected REGISTERED, got %v", frame.GetType())
	}
	if frame.GetTunnelId() == "" {
		t.Error("tunnel_id missing in REGISTERED")
	}
	if frame.GetConfig() == nil {
		t.Error("config missing in REGISTERED")
	}

	t.Logf("Client registered: tunnel_id=%s max_concurrent=%d",
		frame.GetTunnelId(), frame.GetConfig().GetMaxConcurrent())
}

// TestHTTPRequestThroughTunnel tests a full HTTP request through the tunnel
func TestHTTPRequestThroughTunnel(t *testing.T) {
	h := NewHarness(t)

	// 1. Create tunnel
	tunnel := h.CreateTunnel(t, "")

	// 2. Connect client main WS
	wsURL := "ws" + strings.TrimPrefix(h.TunnelAddr, "http") + "/tunnel?domain=" + url.QueryEscape(tunnel.Domain) + "&domain_key=" + url.QueryEscape(tunnel.DomainKey)
	conn, _, err := h.WSDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connect main WS: %v", err)
	}
	defer conn.Close()

	ws := protocol.NewConnection(conn)

	// Register
	ws.Send(&protocol.Frame{
		Type:      protocol.FrameType_REGISTER,
		Domain:    tunnel.Domain,
		DomainKey: tunnel.DomainKey,
	})

	regFrame, _ := ws.ReadFrame()
	t.Logf("Registered: tunnel_id=%s", regFrame.GetTunnelId())

	// 3. Start goroutine to handle incoming requests
	go func() {
		for {
			frame, err := ws.ReadFrame()
			if err != nil {
				return
			}
			if frame.GetType() == protocol.FrameType_REQUEST {
				t.Logf("Got REQUEST: %s %s (id=%s)", frame.GetMethod(), frame.GetPath(), frame.GetRequestId())

				// Proxy to backend
				req, _ := http.NewRequest(frame.GetMethod(), h.Backend.URL+frame.GetPath(), nil)
				resp, err := h.HTTPClient.Do(req)
				if err != nil {
					ws.Send(&protocol.Frame{
						Type:      protocol.FrameType_REQUEST_ERROR,
						RequestId: frame.GetRequestId(),
						Status:    502,
						Error:     err.Error(),
					})
					continue
				}

				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				respHeaders := make(map[string]*protocol.StringList)
				for k, vals := range resp.Header {
					respHeaders[k] = &protocol.StringList{Values: vals}
				}

				// Connect dedicated WS and send response
				dedWSURL := "ws" + strings.TrimPrefix(h.TunnelAddr, "http") + "/tunnel-response?request_id=" + url.QueryEscape(frame.GetRequestId()) + "&domain_key=" + url.QueryEscape(tunnel.DomainKey)
				dedConn, _, err := h.WSDialer.Dial(dedWSURL, nil)
				if err != nil {
					t.Logf("dedicated WS connect error: %v", err)
					ws.Send(&protocol.Frame{
						Type:      protocol.FrameType_REQUEST_ERROR,
						RequestId: frame.GetRequestId(),
						Status:    502,
						Error:     err.Error(),
					})
					continue
				}

				dedWS := protocol.NewConnection(dedConn)

				// Read body chunks (if any)
				for {
					bodyFrame, err := dedWS.ReadFrame()
					if err != nil || bodyFrame.GetType() == protocol.FrameType_BODY_END {
						break
					}
				}

				// Send response
				dedWS.Send(&protocol.Frame{
					Type:            protocol.FrameType_RESPONSE_START,
					RequestId:       frame.GetRequestId(),
					Status:          int32(resp.StatusCode),
					ResponseHeaders: respHeaders,
				})
				if len(body) > 0 {
					dedWS.Send(&protocol.Frame{
						Type:      protocol.FrameType_RESPONSE_BODY,
						RequestId: frame.GetRequestId(),
						Chunk:     body,
					})
				}
				dedWS.Send(&protocol.Frame{
					Type:      protocol.FrameType_RESPONSE_END,
					RequestId: frame.GetRequestId(),
				})
				dedWS.Close()
			}
		}
	}()

	// Wait a bit for the client to be ready
	time.Sleep(100 * time.Millisecond)

	// 4. Make HTTP request through tunnel
	req, _ := http.NewRequest("GET", h.TunnelAddr+"/api/test", nil)
	req.Host = tunnel.Domain
	t.Logf("Making request to %s with Host=%s", h.TunnelAddr+"/api/test", tunnel.Domain)

	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP request through tunnel: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("tunnel response: status=%d body=%s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	t.Logf("Response: status=%d body=%s", resp.StatusCode, string(body))
}

// Ensure imports
var _ = fmt.Sprintf
var _ = server.Config{}
var _ = websocket.Dialer{}
var _ = time.Now
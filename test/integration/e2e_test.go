package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
			if frame.GetType() == protocol.FrameType_REQUEST_START {
				t.Logf("Got REQUEST: %s %s (id=%s)", frame.GetMethod(), frame.GetPath(), frame.GetRequestId())

				// Proxy to backend
				req, _ := http.NewRequest(frame.GetMethod(), h.Backend.URL+frame.GetPath(), nil)
				resp, err := h.HTTPClient.Do(req)
				if err != nil {
					_ = ws.Send(&protocol.Frame{
						Type:      protocol.FrameType_RESPONSE_ERROR,
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

				_ = ws.Send(&protocol.Frame{
					Type:            protocol.FrameType_RESPONSE_START,
					RequestId:       frame.GetRequestId(),
					Status:          int32(resp.StatusCode),
					ResponseHeaders: respHeaders,
				})
				if len(body) > 0 {
					_ = ws.Send(&protocol.Frame{
						Type:      protocol.FrameType_RESPONSE_BODY,
						RequestId: frame.GetRequestId(),
						Chunk:     body,
					})
				}
				_ = ws.Send(&protocol.Frame{
					Type:      protocol.FrameType_RESPONSE_END,
					RequestId: frame.GetRequestId(),
				})
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

func TestWebSocketThroughTunnel(t *testing.T) {
	echoUpgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	h := NewHarnessWithBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := echoUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("backend upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		for {
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(msgType, append([]byte("echo:"), data...)); err != nil {
				return
			}
		}
	}))

	tunnel := h.CreateTunnel(t, "")

	wsURL := "ws" + strings.TrimPrefix(h.TunnelAddr, "http") + "/tunnel?domain=" + url.QueryEscape(tunnel.Domain) + "&domain_key=" + url.QueryEscape(tunnel.DomainKey)
	conn, _, err := h.WSDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connect main WS: %v", err)
	}
	defer conn.Close()
	ws := protocol.NewConnection(conn)
	if err := ws.Send(&protocol.Frame{Type: protocol.FrameType_REGISTER, Domain: tunnel.Domain, DomainKey: tunnel.DomainKey}); err != nil {
		t.Fatalf("send register: %v", err)
	}
	if frame, err := ws.ReadFrame(); err != nil || frame.GetType() != protocol.FrameType_REGISTERED {
		t.Fatalf("read registered: frame=%v err=%v", frame, err)
	}

	proxy := newTunnelWSProxy(ws, h.Backend.URL)
	go proxy.loop()
	defer proxy.close()

	time.Sleep(100 * time.Millisecond)

	publicWSURL := "ws" + strings.TrimPrefix(h.TunnelAddr, "http") + "/ws"
	publicDialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	header := http.Header{}
	header.Set("Host", tunnel.Domain)
	userConn, resp, err := publicDialer.Dial(publicWSURL, header)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("dial tunneled ws failed: %v (status=%s body=%s)", err, resp.Status, string(body))
		}
		t.Fatalf("dial tunneled ws failed: %v", err)
	}
	defer userConn.Close()

	if err := userConn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("write user ws: %v", err)
	}
	msgType, payload, err := userConn.ReadMessage()
	if err != nil {
		t.Fatalf("read user ws: %v", err)
	}
	if msgType != websocket.TextMessage {
		t.Fatalf("expected text message, got %d", msgType)
	}
	if string(payload) != "echo:hello" {
		t.Fatalf("unexpected tunneled ws payload: %q", string(payload))
	}
}

type tunnelWSProxy struct {
	ws         *protocol.Connection
	backendURL string
	mu         sync.Mutex
	requests   map[string]chan *protocol.Frame
	closed     chan struct{}
}

func newTunnelWSProxy(ws *protocol.Connection, backendURL string) *tunnelWSProxy {
	return &tunnelWSProxy{
		ws:         ws,
		backendURL: backendURL,
		requests:   make(map[string]chan *protocol.Frame),
		closed:     make(chan struct{}),
	}
}

func (p *tunnelWSProxy) close() {
	select {
	case <-p.closed:
		return
	default:
		close(p.closed)
	}
}

func (p *tunnelWSProxy) loop() {
	for {
		frame, err := p.ws.ReadFrame()
		if err != nil {
			p.close()
			return
		}
		switch frame.GetType() {
		case protocol.FrameType_REQUEST_START:
			ch := make(chan *protocol.Frame, 32)
			p.mu.Lock()
			p.requests[frame.GetRequestId()] = ch
			p.mu.Unlock()
			go p.handleRequest(frame, ch)
		default:
			if frame.GetRequestId() == "" {
				continue
			}
			p.mu.Lock()
			ch := p.requests[frame.GetRequestId()]
			p.mu.Unlock()
			if ch != nil {
				select {
				case ch <- frame:
				case <-p.closed:
					return
				}
			}
		}
	}
}

func (p *tunnelWSProxy) handleRequest(start *protocol.Frame, ch chan *protocol.Frame) {
	defer func() {
		p.mu.Lock()
		delete(p.requests, start.GetRequestId())
		close(ch)
		p.mu.Unlock()
	}()

	h := make(http.Header)
	for k, vals := range start.GetHeaders() {
		for _, v := range vals.GetValues() {
			h.Add(k, v)
		}
	}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	backendURL := p.backendURL + start.GetPath()
	backendURL = strings.Replace(backendURL, "http://", "ws://", 1)
	backendURL = strings.Replace(backendURL, "https://", "wss://", 1)
	// Remove hop-by-hop / websocket handshake headers that the dialer will add itself
	h.Del("Upgrade")
	h.Del("Connection")
	h.Del("Sec-Websocket-Key")
	h.Del("Sec-Websocket-Version")
	h.Del("Sec-Websocket-Extensions")
	h.Del("Sec-Websocket-Protocol")
	backendConn, resp, err := dialer.Dial(backendURL, h)
	if err != nil {
		status := http.StatusBadGateway
		msg := err.Error()
		if resp != nil {
			status = resp.StatusCode
			msg = msg + " status=" + resp.Status
		}
		_ = p.ws.Send(&protocol.Frame{Type: protocol.FrameType_RESPONSE_ERROR, RequestId: start.GetRequestId(), Status: int32(status), Error: msg})
		return
	}
	defer backendConn.Close()

	respHeaders := make(map[string]*protocol.StringList)
	if resp != nil {
		for k, vals := range resp.Header {
			respHeaders[k] = &protocol.StringList{Values: vals}
		}
	}
	if err := p.ws.Send(&protocol.Frame{Type: protocol.FrameType_RESPONSE_START, RequestId: start.GetRequestId(), Status: int32(http.StatusSwitchingProtocols), ResponseHeaders: respHeaders}); err != nil {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			msgType, data, err := backendConn.ReadMessage()
			if err != nil {
				_ = p.ws.Send(&protocol.Frame{Type: protocol.FrameType_WEBSOCKET_CLOSE, RequestId: start.GetRequestId(), WsCloseCode: int32(websocket.CloseNormalClosure)})
				return
			}
			frameType := protocol.FrameType_WEBSOCKET_BINARY
			if msgType == websocket.TextMessage {
				frameType = protocol.FrameType_WEBSOCKET_TEXT
			}
			if err := p.ws.Send(&protocol.Frame{Type: frameType, RequestId: start.GetRequestId(), Chunk: data}); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-p.closed:
			return
		case frame, ok := <-ch:
			if !ok {
				return
			}
			switch frame.GetType() {
			case protocol.FrameType_WEBSOCKET_TEXT:
				if err := backendConn.WriteMessage(websocket.TextMessage, frame.GetChunk()); err != nil {
					return
				}
			case protocol.FrameType_WEBSOCKET_BINARY:
				if err := backendConn.WriteMessage(websocket.BinaryMessage, frame.GetChunk()); err != nil {
					return
				}
			case protocol.FrameType_WEBSOCKET_CLOSE:
				code := int(frame.GetWsCloseCode())
				if code == 0 {
					code = websocket.CloseNormalClosure
				}
				_ = backendConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, frame.GetWsCloseText()))
				<-done
				return
			}
		}
	}
}

// Ensure imports
var _ = fmt.Sprintf
var _ = server.Config{}
var _ = websocket.Dialer{}
var _ = time.Now

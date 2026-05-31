package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/protocol"
)

var tunnelUpgrader = websocket.Upgrader{
	EnableCompression: true,
	CheckOrigin:       func(r *http.Request) bool { return true },
}

func (a *App) handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ping":"pong","active_tunnels":` + itoa(a.sessions.Count()) + `}`))
}

func (a *App) handleNewTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	requestedSubdomain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("subdomain")))
	domainKey := protocol.GenerateID(16)

	domain := requestedSubdomain
	if domain == "" {
		domain = "tunnel-" + protocol.GenerateID(8)
	}
	domain = protocol.NormalizeHost(domain + ".localhost")

	record, err := a.store.CreateTunnel(r.Context(), requestedSubdomain, domain, hashValue(domainKey), r.RemoteAddr, r.UserAgent())
	if err != nil {
		log.Printf("create tunnel error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{
		"id":         record.ID,
		"domain":     record.Domain,
		"domain_key": domainKey,
	}
	if a.config.ServerMessage != "" {
		resp["server_message"] = a.config.ServerMessage
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *App) handleTunnelWS(w http.ResponseWriter, r *http.Request) {
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	domainKey := strings.TrimSpace(r.URL.Query().Get("domain_key"))

	if domain == "" || domainKey == "" {
		http.Error(w, "domain and domain_key required", http.StatusBadRequest)
		return
	}

	record, err := a.store.FindTunnelForConnection(r.Context(), domain, hashValue(domainKey))
	if err != nil {
		http.Error(w, "invalid domain key", http.StatusForbidden)
		return
	}

	conn, err := tunnelUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}

	ws := protocol.NewConnection(conn)

	frame, err := ws.ReadFrame()
	if err != nil || frame.GetType() != protocol.FrameType_REGISTER {
		ws.Close()
		return
	}

	session := &TunnelSession{
		TunnelID:      record.ID,
		Domain:        protocol.NormalizeHost(domain),
		Conn:          ws,
		MaxConcurrent: a.config.MaxConcurrentRequests,
	}

	prev := a.sessions.Set(protocol.NormalizeHost(domain), session)
	if prev != nil {
		prev.Conn.Close()
	}

	_ = a.store.MarkTunnelActive(r.Context(), session.TunnelID, r.RemoteAddr, r.UserAgent())

	ws.Send(&protocol.Frame{
		Type:      protocol.FrameType_REGISTERED,
		TunnelId:  session.TunnelID,
		Domain:    domain,
		Config: &protocol.TunnelConfig{
			MaxConcurrent:    int32(a.config.MaxConcurrentRequests),
			RequestTimeoutMs: int32(a.config.DefaultRequestTimeout),
			BackendTimeoutMs: int32(a.config.DefaultBackendTimeout),
			Reconnect: &protocol.ReconnectConfig{
				Enabled:        a.config.DefaultReconnectEnabled,
				InitialDelayMs: int32(a.config.DefaultReconnectInitialDelay),
				MaxDelayMs:     int32(a.config.DefaultReconnectMaxDelay),
				Multiplier:     a.config.DefaultReconnectMultiplier,
				MaxRetries:     int32(a.config.DefaultReconnectMaxRetries),
				Jitter:         true,
			},
		},
	})

	log.Printf("Tunnel connected: domain=%s", domain)

	for {
		frame, err := ws.ReadFrame()
		if err != nil {
			break
		}
		switch frame.GetType() {
		case protocol.FrameType_PING:
			ws.Send(&protocol.Frame{Type: protocol.FrameType_PONG})
		case protocol.FrameType_REQUEST_ERROR:
			req, ok := a.pending.Get(frame.GetRequestId())
			if ok && req != nil {
				req.ErrorCh <- &requestError{status: int(frame.GetStatus()), msg: frame.GetError()}
				a.pending.Remove(frame.GetRequestId())
			}
		}
	}

	a.sessions.Delete(domain)
	a.pending.CleanupByTunnel(session.TunnelID)
	_ = a.store.MarkTunnelDisconnected(r.Context(), session.TunnelID)
	log.Printf("Tunnel disconnected: domain=%s", domain)
}

func (a *App) handleTunnelResponseWS(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	domainKey := strings.TrimSpace(r.URL.Query().Get("domain_key"))

	if requestID == "" || domainKey == "" {
		http.Error(w, "request_id and domain_key required", http.StatusBadRequest)
		return
	}

	req, ok := a.pending.Get(requestID)
	if !ok || req == nil {
		http.Error(w, "request not found or expired", http.StatusNotFound)
		return
	}

	conn, err := tunnelUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("dedicated ws upgrade error: %v", err)
		return
	}

	ws := protocol.NewConnection(conn)
	defer ws.Close()

	// Stream request body
	if req.BodyReader != nil {
		buf := make([]byte, 32*1024)
		for {
			n, err := req.BodyReader.Read(buf)
			if n > 0 {
				ws.Send(&protocol.Frame{
					Type:      protocol.FrameType_BODY,
					RequestId: requestID,
					Chunk:     buf[:n],
				})
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return
			}
		}
		ws.Send(&protocol.Frame{
			Type:      protocol.FrameType_BODY_END,
			RequestId: requestID,
		})
	}

	// Read response
	for {
		frame, err := ws.ReadFrame()
		if err != nil {
			req.ErrorCh <- err
			a.pending.Remove(requestID)
			return
		}

		switch frame.GetType() {
		case protocol.FrameType_RESPONSE_START:
			req.ResponseCh <- &PendingResponse{
				Status:  int(frame.GetStatus()),
				Headers: convertHeaders(frame.GetResponseHeaders()),
			}
		case protocol.FrameType_RESPONSE_BODY:
			select {
			case req.bodyCh <- frame.GetChunk():
			default:
			}
		case protocol.FrameType_RESPONSE_END:
			close(req.bodyCh)
			a.pending.Remove(requestID)
			return
		case protocol.FrameType_RESPONSE_ERROR:
			req.ErrorCh <- &requestError{status: int(frame.GetStatus()), msg: frame.GetError()}
			a.pending.Remove(requestID)
			return
		}
	}
}

func (a *App) handleTunnelHTTP(w http.ResponseWriter, r *http.Request) {
	host := protocol.NormalizeHost(r.Host)
	log.Printf("handleTunnelHTTP: host=%q path=%s", host, r.URL.Path)

	session, ok := a.sessions.Get(host)
	if !ok {
		// Try wildcard match
		for domain, sess := range a.sessions.GetAll() {
			if strings.HasSuffix(host, "."+domain) || host == domain {
				session = sess
				ok = true
				break
			}
		}
		if !ok {
			http.Error(w, "Tunnel not found", http.StatusNotFound)
			return
		}
	}

	// Check concurrent limit
	if !session.CanAcceptRequest() {
		http.Error(w, "Too many concurrent requests", http.StatusServiceUnavailable)
		return
	}
	session.IncrementActive()
	defer session.DecrementActive()

	requestID := protocol.GenerateID(16)

	if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
		http.Error(w, "WebSocket upgrade not yet implemented", http.StatusNotImplemented)
		return
	}

	bodyReader, bodyWriter := io.Pipe()
	req := &PendingRequest{
		ID:         requestID,
		TunnelID:   session.TunnelID,
		Method:     r.Method,
		Path:       r.URL.RequestURI(),
		Headers:    cloneHeaders(r.Header),
		ContentLen: r.ContentLength,
		BodyReader: bodyReader,
		ResponseCh: make(chan *PendingResponse, 1),
		ErrorCh:    make(chan error, 1),
		bodyCh:     make(chan []byte, 64),
		CreatedAt:  time.Now(),
	}
	a.pending.Add(req)

	headers := make(map[string]*protocol.StringList)
	for k, vals := range r.Header {
		headers[k] = &protocol.StringList{Values: vals}
	}

	session.Conn.Send(&protocol.Frame{
		Type:          protocol.FrameType_REQUEST,
		RequestId:     requestID,
		Method:        r.Method,
		Path:          r.URL.RequestURI(),
		Headers:       headers,
		ContentLength: r.ContentLength,
	})

	if r.Body != nil && r.ContentLength > 0 {
		go func() {
			io.Copy(bodyWriter, r.Body)
			bodyWriter.Close()
		}()
	} else {
		bodyWriter.Close()
	}

	select {
	case resp := <-req.ResponseCh:
		for k, vals := range resp.Headers {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.Status)
		for chunk := range req.bodyCh {
			w.Write(chunk)
		}
	case err := <-req.ErrorCh:
		if re, ok := err.(*requestError); ok {
			http.Error(w, re.msg, re.status)
		} else {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
	case <-r.Context().Done():
	}

	a.pending.Remove(requestID)
}

func (a *App) handleAdmin(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Admin panel"))
}

func (a *App) handleAdminAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

type requestError struct {
	status int
	msg    string
}

func (e *requestError) Error() string { return e.msg }

func cloneHeaders(h http.Header) map[string][]string {
	result := make(map[string][]string)
	for k, vs := range h {
		vals := make([]string, len(vs))
		copy(vals, vs)
		result[k] = vals
	}
	return result
}

func convertHeaders(h map[string]*protocol.StringList) map[string][]string {
	result := make(map[string][]string)
	for k, v := range h {
		result[k] = v.GetValues()
	}
	return result
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	if neg {
		digits = "-" + digits
	}
	return digits
}

func hashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
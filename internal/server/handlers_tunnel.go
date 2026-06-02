package server

import (
	"encoding/json"
	"errors"
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
	domain = domain + "." + a.config.TunnelDomain

	record, err := a.store.CreateTunnel(r.Context(), requestedSubdomain, domain, hashValue(domainKey), r.RemoteAddr, r.UserAgent())
	if err != nil {
		log.Printf("create tunnel error: %v", err)
		_ = a.store.LogTunnelCreation(r.Context(), "", domain, requestedSubdomain, r.RemoteAddr, r.UserAgent(), false, err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = a.store.LogTunnelCreation(r.Context(), record.ID, record.Domain, requestedSubdomain, r.RemoteAddr, r.UserAgent(), true, "")

	resp := map[string]interface{}{
		"id":         record.ID,
		"domain":     record.Domain,
		"domain_key": domainKey,
	}
	if a.config.ServerMessage != "" {
		resp["server_message"] = a.config.ServerMessage
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
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
		_ = ws.Close()
		return
	}

	session := NewTunnelSession(record.ID, protocol.NormalizeHost(domain), ws, a.config.MaxConcurrentRequests)
	prev := a.sessions.Set(protocol.NormalizeHost(domain), session)
	if prev != nil {
		_ = prev.Close()
	}

	_ = a.store.MarkTunnelActive(r.Context(), session.TunnelID, r.RemoteAddr, r.UserAgent())

	if err := ws.Send(&protocol.Frame{
		Type:     protocol.FrameType_REGISTERED,
		TunnelId: session.TunnelID,
		Domain:   domain,
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
	}); err != nil {
		_ = session.Close()
		return
	}
	log.Printf("Tunnel connected: domain=%s", domain)
	session.MarkActivity()
	session.Start()

	for {
		frame, err := ws.ReadFrame()
		if err != nil {
			break
		}
		session.MarkActivity()
		switch frame.GetType() {
		case protocol.FrameType_PING:
			_ = session.Enqueue(&protocol.Frame{Type: protocol.FrameType_PONG})
		case protocol.FrameType_PONG:
			session.AckPong()
		case protocol.FrameType_RESPONSE_START:
			if req, ok := a.pending.Get(frame.GetRequestId()); ok && req != nil {
				req.MarkResponseStarted()
				select {
				case req.ResponseCh <- &PendingResponse{Status: int(frame.GetStatus()), Headers: convertHeaders(frame.GetResponseHeaders())}:
				default:
				}
			}
		case protocol.FrameType_RESPONSE_BODY:
			if req, ok := a.pending.Get(frame.GetRequestId()); ok && req != nil {
				chunk := frame.GetChunk()
				if len(chunk) == 0 {
					continue
				}
				copied := make([]byte, len(chunk))
				copy(copied, chunk)
				req.bodyCh <- copied
			}
		case protocol.FrameType_RESPONSE_END:
			if req, ok := a.pending.Get(frame.GetRequestId()); ok && req != nil {
				req.CloseBody()
				a.pending.Remove(frame.GetRequestId())
			}
		case protocol.FrameType_RESPONSE_ERROR:
			if req, ok := a.pending.Get(frame.GetRequestId()); ok && req != nil {
				req.Fail(&requestError{status: int(frame.GetStatus()), msg: frame.GetError()})
				a.pending.Remove(frame.GetRequestId())
			}
		}
	}

	a.sessions.Delete(protocol.NormalizeHost(domain))
	a.pending.FailByTunnel(session.TunnelID, errors.New("tunnel connection closed"))
	_ = session.Close()
	_ = a.store.MarkTunnelDisconnected(r.Context(), session.TunnelID)
	log.Printf("Tunnel disconnected: domain=%s", domain)
}

func (a *App) handleTunnelHTTP(w http.ResponseWriter, r *http.Request) {
	host := protocol.NormalizeHost(r.Host)

	session, ok := a.sessions.Get(host)
	if !ok {
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

	if !session.CanAcceptRequest() {
		http.Error(w, "Too many concurrent requests", http.StatusServiceUnavailable)
		return
	}
	session.IncrementActive()
	defer session.DecrementActive()

	startedAt := time.Now().UTC()
	requestID := protocol.GenerateID(16)
	requestPath := buildRequestPath(r)
	requestHeaders := cloneHeaders(r.Header)
	forwardHeaders := protocol.MergeForwardedHeaders(nil, r)
	requestCapture := NewBodyCapture(contentTypeFromHeaders(requestHeaders))

	logEntry := &RequestResponseLog{
		ID:                 requestID,
		TunnelID:           session.TunnelID,
		Domain:             session.Domain,
		Method:             r.Method,
		Path:               requestPath,
		RequestHeaders:     requestHeaders,
		RequestContentType: contentTypeFromHeaders(requestHeaders),
		StartedAt:          startedAt,
	}

	var (
		responseHeaders map[string][]string
		responseCapture *BodyCapture
		statusCode      int
		responseErr     error
	)

	defer func() {
		completedAt := time.Now().UTC()
		logEntry.CompletedAt = &completedAt
		logEntry.DurationMs = completedAt.Sub(startedAt).Milliseconds()
		logEntry.StatusCode = statusCode
		logEntry.RequestPreview = requestCapture.Preview()
		logEntry.ResponseHeaders = responseHeaders
		if responseCapture != nil {
			logEntry.ResponsePreview = responseCapture.Preview()
			logEntry.ResponseContentType = contentTypeFromHeaders(responseHeaders)
		}
		if responseErr != nil {
			logEntry.ErrorMessage = responseErr.Error()
		}
		if err := a.store.RecordRequestLog(r.Context(), *logEntry); err != nil {
			a.logError("recording request log", err)
		}
	}()

	if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
		http.Error(w, "WebSocket upgrade not yet implemented", http.StatusNotImplemented)
		return
	}

	req := &PendingRequest{
		ID:         requestID,
		TunnelID:   session.TunnelID,
		Method:     r.Method,
		Path:       requestPath,
		Headers:    forwardHeaders,
		ContentLen: r.ContentLength,
		ResponseCh: make(chan *PendingResponse, 1),
		ErrorCh:    make(chan error, 1),
		bodyCh:     make(chan []byte, protocol.DefaultPerRequestFrameQueue),
		CreatedAt:  time.Now(),
		LogEntry:   logEntry,
	}
	a.pending.Add(req)
	defer a.pending.Remove(requestID)

	requestFrameHeaders := make(map[string]*protocol.StringList, len(forwardHeaders))
	for key, values := range forwardHeaders {
		requestFrameHeaders[key] = &protocol.StringList{Values: values}
	}
	if err := session.Enqueue(&protocol.Frame{
		Type:          protocol.FrameType_REQUEST_START,
		RequestId:     requestID,
		Method:        r.Method,
		Path:          requestPath,
		Headers:       requestFrameHeaders,
		ContentLength: r.ContentLength,
	}); err != nil {
		responseErr = err
		statusCode = http.StatusBadGateway
		http.Error(w, err.Error(), statusCode)
		return
	}

	go a.streamRequestBody(r, session, req, requestCapture, logEntry)

	select {
	case resp := <-req.ResponseCh:
		statusCode = resp.Status
		responseHeaders = resp.Headers
		responseCapture = NewBodyCapture(contentTypeFromHeaders(resp.Headers))
		protocol.ApplyHeaders(w.Header(), resp.Headers)
		w.WriteHeader(resp.Status)

		flusher, canFlush := w.(http.Flusher)
		if canFlush {
			flusher.Flush()
		}

		for {
			select {
			case chunk, ok := <-req.bodyCh:
				if !ok {
					return
				}
				logEntry.ResponseBytes += int64(len(chunk))
				responseCapture.Observe(chunk)
				_, _ = w.Write(chunk)
				if canFlush {
					flusher.Flush()
				}
			case err := <-req.ErrorCh:
				responseErr = err
				return
			case <-r.Context().Done():
				responseErr = r.Context().Err()
				_ = session.Enqueue(&protocol.Frame{Type: protocol.FrameType_REQUEST_CANCEL, RequestId: requestID, Error: responseErr.Error()})
				return
			}
		}
	case err := <-req.ErrorCh:
		responseErr = err
		if re, ok := err.(*requestError); ok {
			statusCode = re.status
			http.Error(w, re.msg, re.status)
		} else {
			statusCode = http.StatusBadGateway
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
	case <-r.Context().Done():
		responseErr = r.Context().Err()
		statusCode = 499
		_ = session.Enqueue(&protocol.Frame{Type: protocol.FrameType_REQUEST_CANCEL, RequestId: requestID, Error: responseErr.Error()})
	}
}

type requestError struct {
	status int
	msg    string
}

func (e *requestError) Error() string { return e.msg }

func (a *App) streamRequestBody(r *http.Request, session *TunnelSession, req *PendingRequest, requestCapture *BodyCapture, logEntry *RequestResponseLog) {
	defer func() {
		_ = session.Enqueue(&protocol.Frame{Type: protocol.FrameType_REQUEST_END, RequestId: req.ID})
	}()
	if r.Body == nil {
		return
	}
	defer r.Body.Close()

	buf := make([]byte, protocol.DefaultChunkSize)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			logEntry.RequestBytes += int64(n)
			requestCapture.Observe(chunk)
			if enqueueErr := session.Enqueue(&protocol.Frame{
				Type:      protocol.FrameType_REQUEST_BODY,
				RequestId: req.ID,
				Chunk:     chunk,
			}); enqueueErr != nil {
				req.Fail(enqueueErr)
				return
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			req.Fail(err)
			_ = session.Enqueue(&protocol.Frame{Type: protocol.FrameType_REQUEST_CANCEL, RequestId: req.ID, Error: err.Error()})
			return
		}
	}
}

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

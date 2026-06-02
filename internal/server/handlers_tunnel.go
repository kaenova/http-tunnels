package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	domain, domainKey, record, ok := a.resolveTunnelConnectionRequest(w, r)
	if !ok {
		return
	}

	log.Printf("Tunnel websocket upgrade requested: domain=%s remote=%s", domain, r.RemoteAddr)
	conn, err := tunnelUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Tunnel websocket upgrade failed: domain=%s remote=%s err=%v", domain, r.RemoteAddr, err)
		return
	}

	ws := protocol.NewConnection(conn)
	frame, err := ws.ReadFrame()
	if err != nil {
		log.Printf("Tunnel websocket register read failed: domain=%s remote=%s err=%v", domain, r.RemoteAddr, err)
		_ = ws.Close()
		return
	}
	if err := validateRegisterFrame(frame, domain, domainKey); err != nil {
		log.Printf("Tunnel websocket register frame invalid: domain=%s remote=%s err=%v", domain, r.RemoteAddr, err)
		_ = ws.Close()
		return
	}

	session := NewTunnelSession(record.ID, protocol.NormalizeHost(domain), protocol.TransportWebSocket, ws, a.config.MaxConcurrentRequests)
	prev := a.sessions.Set(protocol.NormalizeHost(domain), session)
	if prev != nil {
		_ = prev.Close()
	}
	_ = a.store.MarkTunnelActive(r.Context(), session.TunnelID, protocol.TransportWebSocket, r.RemoteAddr, r.UserAgent())

	if err := ws.Send(a.tunnelConfigFrame(session.TunnelID, domain)); err != nil {
		a.cleanupTunnelSession(r.Context(), session, domain, r.RemoteAddr)
		return
	}
	log.Printf("Tunnel websocket connected: domain=%s tunnel_id=%s remote=%s", domain, session.TunnelID, r.RemoteAddr)
	session.MarkActivity()
	session.Start()

	for {
		frame, err := ws.ReadFrame()
		if err != nil {
			log.Printf("Tunnel websocket disconnected: domain=%s tunnel_id=%s remote=%s err=%v", domain, session.TunnelID, r.RemoteAddr, err)
			break
		}
		session.MarkActivity()
		a.handlePendingResponseFrame(frame)
		switch frame.GetType() {
		case protocol.FrameType_PING:
			_ = session.Enqueue(&protocol.Frame{Type: protocol.FrameType_PONG})
		case protocol.FrameType_PONG:
			session.AckPong()
		}
	}

	a.cleanupTunnelSession(r.Context(), session, domain, r.RemoteAddr)
}

// handleTunnelH2 keeps backward compatibility and behaves the same as handleTunnelH2Stream.
func (a *App) handleTunnelH2(w http.ResponseWriter, r *http.Request) {
	a.handleTunnelH2Stream(w, r)
}

func (a *App) handleTunnelH2Stream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.ProtoMajor < 2 {
		http.Error(w, "HTTP/2 required", http.StatusHTTPVersionNotSupported)
		return
	}

	domain, _, record, ok := a.resolveTunnelConnectionRequest(w, r)
	if !ok {
		return
	}

	controller := http.NewResponseController(w)
	if err := controller.EnableFullDuplex(); err != nil {
		log.Printf("Tunnel http2 full duplex setup failed: domain=%s remote=%s err=%v", domain, r.RemoteAddr, err)
		http.Error(w, "HTTP/2 full duplex is not available", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	worker := NewHTTP2WorkerStream(protocol.NewH2TunnelStream(protocol.H2TunnelStreamOptions{
		Reader: r.Body,
		Writer: w,
		Flush:  controller.Flush,
		Close:  func() error { return r.Body.Close() },
	}))

	session, created, replaced := a.sessions.GetOrCreateHTTP2(protocol.NormalizeHost(domain), func() *TunnelSession {
		return NewTunnelSession(record.ID, protocol.NormalizeHost(domain), protocol.TransportHTTP2, nil, a.config.MaxConcurrentRequests)
	})
	if replaced != nil {
		_ = replaced.Close()
	}
	if created {
		log.Printf("Tunnel http2 session created: domain=%s tunnel_id=%s remote=%s", domain, session.TunnelID, r.RemoteAddr)
	}
	_ = a.store.MarkTunnelActive(r.Context(), session.TunnelID, protocol.TransportHTTP2, r.RemoteAddr, r.UserAgent())
	if err := session.RegisterHTTP2Worker(worker); err != nil {
		_ = worker.Close()
		http.Error(w, "HTTP/2 tunnel session is not available", http.StatusServiceUnavailable)
		return
	}
	session.MarkActivity()
	w.WriteHeader(http.StatusOK)
	_ = controller.Flush()

	defer func() {
		remaining := session.UnregisterHTTP2Worker(worker)
		_ = worker.Close()
		if remaining == 0 {
			a.scheduleHTTP2SessionCleanup(session, domain, r.RemoteAddr)
		}
	}()

	select {
	case <-worker.Done():
	case <-session.Done():
	}
}

func (a *App) resolveTunnelConnectionRequest(w http.ResponseWriter, r *http.Request) (string, string, TunnelRecord, bool) {
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	domainKey := strings.TrimSpace(r.URL.Query().Get("domain_key"))
	if domain == "" || domainKey == "" {
		http.Error(w, "domain and domain_key required", http.StatusBadRequest)
		return "", "", TunnelRecord{}, false
	}
	record, err := a.store.FindTunnelForConnection(r.Context(), domain, hashValue(domainKey))
	if err != nil {
		http.Error(w, "invalid domain key", http.StatusForbidden)
		return "", "", TunnelRecord{}, false
	}
	return domain, domainKey, record, true
}

func validateRegisterFrame(frame *protocol.Frame, domain, domainKey string) error {
	if frame == nil {
		return fmt.Errorf("register frame missing")
	}
	if frame.GetType() != protocol.FrameType_REGISTER {
		return fmt.Errorf("unexpected frame type %v", frame.GetType())
	}
	if value := strings.TrimSpace(frame.GetDomain()); value != "" && protocol.NormalizeHost(value) != protocol.NormalizeHost(domain) {
		return fmt.Errorf("register domain mismatch")
	}
	if value := strings.TrimSpace(frame.GetDomainKey()); value != "" && value != domainKey {
		return fmt.Errorf("register domain key mismatch")
	}
	return nil
}

func (a *App) tunnelConfigFrame(tunnelID, domain string) *protocol.Frame {
	return &protocol.Frame{
		Type:     protocol.FrameType_REGISTERED,
		TunnelId: tunnelID,
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
	}
}

func (a *App) handlePendingResponseFrame(frame *protocol.Frame) bool {
	switch frame.GetType() {
	case protocol.FrameType_RESPONSE_START:
		if req, ok := a.pending.Get(frame.GetRequestId()); ok && req != nil {
			req.MarkResponseStarted()
			select {
			case req.ResponseCh <- &PendingResponse{Status: int(frame.GetStatus()), Headers: convertHeaders(frame.GetResponseHeaders())}:
			default:
			}
		}
		return false
	case protocol.FrameType_RESPONSE_BODY:
		if req, ok := a.pending.Get(frame.GetRequestId()); ok && req != nil {
			chunk := frame.GetChunk()
			if len(chunk) == 0 {
				return false
			}
			copied := make([]byte, len(chunk))
			copy(copied, chunk)
			req.bodyCh <- copied
		}
		return false
	case protocol.FrameType_RESPONSE_END:
		if req, ok := a.pending.Get(frame.GetRequestId()); ok && req != nil {
			req.CloseBody()
			a.pending.Remove(frame.GetRequestId())
		}
		return true
	case protocol.FrameType_RESPONSE_ERROR:
		if req, ok := a.pending.Get(frame.GetRequestId()); ok && req != nil {
			req.Fail(&requestError{status: int(frame.GetStatus()), msg: frame.GetError()})
			a.pending.Remove(frame.GetRequestId())
		}
		return true
	default:
		return false
	}
}

func (a *App) scheduleHTTP2SessionCleanup(session *TunnelSession, domain, remoteAddr string) {
	go func() {
		timer := time.NewTimer(1 * time.Second)
		defer timer.Stop()
		<-timer.C
		current, ok := a.sessions.Get(protocol.NormalizeHost(domain))
		if !ok || current != session {
			return
		}
		if session.HTTP2WorkerCount() > 0 {
			return
		}
		a.cleanupTunnelSession(context.Background(), session, domain, remoteAddr)
	}()
}

func (a *App) cleanupTunnelSession(ctx context.Context, session *TunnelSession, domain, remoteAddr string) {
	if session == nil {
		return
	}
	a.sessions.Delete(protocol.NormalizeHost(domain))
	a.pending.FailByTunnel(session.TunnelID, errors.New("tunnel connection closed"))
	_ = session.Close()
	_ = a.store.MarkTunnelDisconnected(ctx, session.TunnelID)
	log.Printf("Tunnel session cleaned up: transport=%s domain=%s tunnel_id=%s remote=%s", session.Transport, domain, session.TunnelID, remoteAddr)
}

func (a *App) handleTunnelHTTP(w http.ResponseWriter, r *http.Request) {
	if a.shouldRedirectAdminHostRoot(r) {
		a.redirectAdminHostRoot(w, r)
		return
	}

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

	if session.SupportsHTTP2Workers() {
		a.handleTunnelHTTPOverHTTP2(w, r, session, requestPath, forwardHeaders, requestCapture, logEntry, &responseHeaders, &responseCapture, &statusCode, &responseErr)
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
	requestStartFrame := &protocol.Frame{
		Type:          protocol.FrameType_REQUEST_START,
		RequestId:     requestID,
		Method:        r.Method,
		Path:          requestPath,
		Headers:       requestFrameHeaders,
		ContentLength: r.ContentLength,
	}

	if err := session.Enqueue(requestStartFrame); err != nil {
		responseErr = err
		statusCode = http.StatusBadGateway
		http.Error(w, err.Error(), statusCode)
		return
	}
	go a.streamRequestBodyToSender(r, session.Enqueue, func(err error) {
		if err != nil {
			_ = session.Enqueue(&protocol.Frame{Type: protocol.FrameType_REQUEST_CANCEL, RequestId: requestID, Error: err.Error()})
		}
	}, req, requestCapture, logEntry)

	select {
	case resp := <-req.ResponseCh:
		statusCode = resp.Status
		responseHeaders = resp.Headers
		responseCapture = NewBodyCapture(contentTypeFromHeaders(resp.Headers))
		protocol.ApplyHeaders(w.Header(), resp.Headers)
		w.WriteHeader(resp.Status)

		flusher, canFlush := w.(http.Flusher)
		flushStreaming := canFlush && shouldFlushStreamingResponse(contentTypeFromHeaders(resp.Headers))
		if flushStreaming {
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
				if flushStreaming {
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

func (a *App) handleTunnelHTTPOverHTTP2(w http.ResponseWriter, r *http.Request, session *TunnelSession, requestPath string, forwardHeaders map[string][]string, requestCapture *BodyCapture, logEntry *RequestResponseLog, responseHeaders *map[string][]string, responseCapture **BodyCapture, statusCode *int, responseErr *error) {
	worker, err := session.AcquireHTTP2Worker()
	if err != nil {
		*responseErr = err
		*statusCode = http.StatusServiceUnavailable
		http.Error(w, "No HTTP/2 worker available", *statusCode)
		return
	}
	defer worker.Close()

	session.MarkActivity()
	stream := worker.Stream
	if err := stream.WriteRequestStart(protocol.H2RequestStart{
		Method:        r.Method,
		Path:          requestPath,
		Headers:       forwardHeaders,
		ContentLength: r.ContentLength,
	}); err != nil {
		*responseErr = err
		*statusCode = http.StatusBadGateway
		http.Error(w, err.Error(), *statusCode)
		return
	}

	requestSendDone := make(chan error, 1)
	go func() {
		requestSendDone <- a.streamRequestBodyToHTTP2Worker(r, stream, requestCapture, logEntry)
	}()

	go func() {
		<-r.Context().Done()
		_ = worker.Close()
	}()

	messageType, payload, err := stream.ReadMessage()
	if err != nil {
		if isExpectedHTTP2WorkerClose(err) || r.Context().Err() != nil {
			if r.Context().Err() != nil {
				*responseErr = r.Context().Err()
				*statusCode = 499
			} else {
				*responseErr = err
			}
			return
		}
		log.Printf("Tunnel http2 response start read failed: domain=%s path=%s tunnel_id=%s err=%v", session.Domain, requestPath, session.TunnelID, err)
		*responseErr = err
		*statusCode = http.StatusBadGateway
		http.Error(w, err.Error(), *statusCode)
		return
	}

	switch messageType {
	case protocol.H2MessageResponseError:
		remoteErr := protocol.H2ResponseError{Status: http.StatusBadGateway, Error: "upstream error"}
		if unmarshalErr := json.Unmarshal(payload, &remoteErr); unmarshalErr != nil {
			*responseErr = unmarshalErr
			*statusCode = http.StatusBadGateway
			http.Error(w, unmarshalErr.Error(), *statusCode)
			return
		}
		*responseErr = &requestError{status: remoteErr.Status, msg: remoteErr.Error}
		*statusCode = remoteErr.Status
		http.Error(w, remoteErr.Error, remoteErr.Status)
		return
	case protocol.H2MessageResponseStart:
		var start protocol.H2ResponseStart
		if err := json.Unmarshal(payload, &start); err != nil {
			*responseErr = err
			*statusCode = http.StatusBadGateway
			http.Error(w, err.Error(), *statusCode)
			return
		}
		*statusCode = start.Status
		*responseHeaders = start.Headers
		*responseCapture = NewBodyCapture(contentTypeFromHeaders(start.Headers))
		protocol.ApplyHeaders(w.Header(), start.Headers)
		w.WriteHeader(start.Status)
	default:
		*responseErr = fmt.Errorf("unexpected h2 response message: %d", messageType)
		*statusCode = http.StatusBadGateway
		http.Error(w, (*responseErr).Error(), *statusCode)
		return
	}

	flusher, canFlush := w.(http.Flusher)
	flushStreaming := canFlush && shouldFlushStreamingResponse(contentTypeFromHeaders(*responseHeaders))
	if flushStreaming {
		flusher.Flush()
	}

	for {
		messageType, payload, err = stream.ReadMessage()
		if err != nil {
			if isExpectedHTTP2WorkerClose(err) || r.Context().Err() != nil {
				if r.Context().Err() != nil && *responseErr == nil {
					*responseErr = r.Context().Err()
				}
				return
			}
			log.Printf("Tunnel http2 response body read failed: domain=%s path=%s tunnel_id=%s err=%v", session.Domain, requestPath, session.TunnelID, err)
			*responseErr = err
			return
		}
		switch messageType {
		case protocol.H2MessageResponseBody:
			if len(payload) == 0 {
				continue
			}
			logEntry.ResponseBytes += int64(len(payload))
			(*responseCapture).Observe(payload)
			_, _ = w.Write(payload)
			if flushStreaming {
				flusher.Flush()
			}
		case protocol.H2MessageResponseEnd:
			select {
			case err := <-requestSendDone:
				if err != nil && *responseErr == nil {
					*responseErr = err
				}
			default:
			}
			return
		case protocol.H2MessageResponseError:
			remoteErr := protocol.H2ResponseError{Status: http.StatusBadGateway, Error: "upstream error"}
			if err := json.Unmarshal(payload, &remoteErr); err != nil {
				*responseErr = err
				return
			}
			*responseErr = &requestError{status: remoteErr.Status, msg: remoteErr.Error}
			return
		default:
			*responseErr = fmt.Errorf("unexpected h2 response message: %d", messageType)
			return
		}
	}
}

func (a *App) streamRequestBodyToHTTP2Worker(r *http.Request, stream *protocol.H2TunnelStream, requestCapture *BodyCapture, logEntry *RequestResponseLog) error {
	defer func() {
		_ = stream.WriteRequestEnd()
	}()
	if r.Body == nil {
		return nil
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
			if sendErr := stream.WriteRequestBody(chunk); sendErr != nil {
				return sendErr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type requestError struct {
	status int
	msg    string
}

func (e *requestError) Error() string { return e.msg }

func (a *App) streamRequestBodyToSender(r *http.Request, send func(*protocol.Frame) error, cancel func(error), req *PendingRequest, requestCapture *BodyCapture, logEntry *RequestResponseLog) {
	defer func() {
		_ = send(&protocol.Frame{Type: protocol.FrameType_REQUEST_END, RequestId: req.ID})
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
			if sendErr := send(&protocol.Frame{Type: protocol.FrameType_REQUEST_BODY, RequestId: req.ID, Chunk: chunk}); sendErr != nil {
				req.Fail(sendErr)
				return
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			req.Fail(err)
			if cancel != nil {
				cancel(err)
			}
			return
		}
	}
}

func isExpectedHTTP2WorkerClose(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return errors.Is(err, io.EOF) || strings.Contains(message, "stream error") && strings.Contains(message, "cancel") || strings.Contains(message, "body closed by handler") || strings.Contains(message, "unexpected eof")
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

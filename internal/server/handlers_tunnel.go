package server

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/protocol"
)

var (
	tunnelUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	subdomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

func (a *App) handlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	summary, err := a.store.dashboardSummary(r.Context())
	if err != nil {
		a.logError("loading ping summary", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ping":               "pong",
		"active_tunnels":     summary.ActiveTunnels,
		"registered_tunnels": summary.RegisteredTunnels,
	})
}

func (a *App) handleNewTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	requestedSubdomain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("subdomain")))
	if requestedSubdomain != "" && !subdomainPattern.MatchString(requestedSubdomain) {
		_ = a.store.LogTunnelCreation(r.Context(), "", "", requestedSubdomain, r.RemoteAddr, r.UserAgent(), false, "invalid subdomain")
		http.Error(w, "invalid subdomain", http.StatusBadRequest)
		return
	}

	baseHost := normalizeRequestHost(r.Host)
	if baseHost == "" {
		_ = a.store.LogTunnelCreation(r.Context(), "", "", requestedSubdomain, r.RemoteAddr, r.UserAgent(), false, "missing host")
		http.Error(w, "missing host", http.StatusBadRequest)
		return
	}

	domain := ""
	if requestedSubdomain == "" {
		for attempts := 0; attempts < 16; attempts++ {
			candidate := randomSubdomain()
			candidateDomain := candidate + "." + baseHost
			exists, err := a.store.DomainExists(r.Context(), candidateDomain)
			if err != nil {
				a.logError("checking generated subdomain", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			if !exists {
				requestedSubdomain = candidate
				domain = candidateDomain
				break
			}
		}
		if domain == "" {
			_ = a.store.LogTunnelCreation(r.Context(), "", "", "", r.RemoteAddr, r.UserAgent(), false, "could not allocate random subdomain")
			http.Error(w, "could not allocate subdomain", http.StatusInternalServerError)
			return
		}
	} else {
		domain = requestedSubdomain + "." + baseHost
		exists, err := a.store.DomainExists(r.Context(), domain)
		if err != nil {
			a.logError("checking requested subdomain", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if exists {
			_ = a.store.LogTunnelCreation(r.Context(), "", domain, requestedSubdomain, r.RemoteAddr, r.UserAgent(), false, "subdomain already in use")
			http.Error(w, "subdomain already in use", http.StatusBadRequest)
			return
		}
	}

	domainKey := protocol.GenerateID(16)
	record, err := a.store.CreateTunnel(r.Context(), requestedSubdomain, domain, hashValue(domainKey), r.RemoteAddr, r.UserAgent())
	if err != nil {
		a.logError("creating tunnel", err)
		_ = a.store.LogTunnelCreation(r.Context(), "", domain, requestedSubdomain, r.RemoteAddr, r.UserAgent(), false, err.Error())
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := a.store.LogTunnelCreation(r.Context(), record.ID, record.Domain, requestedSubdomain, r.RemoteAddr, r.UserAgent(), true, ""); err != nil {
		a.logError("logging tunnel creation", err)
	}

	response := map[string]any{
		"id":         record.ID,
		"domain":     record.Domain,
		"domain_key": domainKey,
	}
	if a.config.ServerMessage != "" {
		response["server_message"] = a.config.ServerMessage
	}

	log.Printf("New domain registered: %s", record.Domain)
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleTunnelWebSocket(w http.ResponseWriter, r *http.Request) {
	domain := normalizeRequestHost(r.URL.Query().Get("domain"))
	domainKey := strings.TrimSpace(r.URL.Query().Get("domain_key"))
	if domain == "" || domainKey == "" {
		http.Error(w, "both domain and domain_key need provided", http.StatusBadRequest)
		return
	}

	record, err := a.store.FindTunnelForConnection(r.Context(), domain, hashValue(domainKey))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "invalid domain key", http.StatusForbidden)
			return
		}
		a.logError("validating tunnel connection", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	wsConn, err := tunnelUpgrader.Upgrade(w, r, nil)
	if err != nil {
		a.logError("upgrading websocket", err)
		return
	}

	connection := protocol.NewConnection(wsConn)
	session := NewTunnelSession(record, connection)
	previous := a.replaceSession(record.Domain, session)
	if previous != nil {
		previous.FailAll(errors.New("tunnel replaced by a newer connection"))
		_ = previous.Conn.Close()
	}

	if err := a.store.MarkTunnelActive(context.Background(), record.ID, r.RemoteAddr, r.UserAgent()); err != nil {
		a.deleteSession(record.Domain, session)
		session.FailAll(err)
		_ = session.Conn.Close()
		a.logError("marking tunnel active", err)
		return
	}

	log.Printf("Tunnel connected: %s", record.Domain)
	defer func() {
		a.deleteSession(record.Domain, session)
		session.FailAll(errors.New("tunnel disconnected"))
		_ = session.Conn.Close()
		if err := a.store.MarkTunnelDisconnected(context.Background(), record.ID); err != nil {
			a.logError("marking tunnel disconnected", err)
		}
	}()

	if err := session.Conn.ReadLoop(session.HandleFrame); err != nil {
		log.Printf("Tunnel disconnected for %s: %v", record.Domain, err)
	}
}

func (a *App) handleTunnelHTTP(session *TunnelSession, w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now().UTC()
	requestID := protocol.GenerateID(16)
	requestPath := buildRequestPath(r)
	requestHeaders := protocol.MergeForwardedHeaders(nil, r)
	requestCapture := NewBodyCapture(contentTypeFromHeaders(requestHeaders))

	logEntry := RequestResponseLog{
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
		if err := a.store.RecordRequestLog(context.Background(), logEntry); err != nil {
			a.logError("recording request log", err)
		}
	}()

	stream := newResponseStream()
	session.RegisterPending(requestID, stream)
	defer session.RemovePending(requestID)

	cancelOnce := sync.Once{}
	sendCancel := func() {
		cancelOnce.Do(func() {
			_ = session.Send(protocol.Frame{Type: protocol.FrameTypeRequestCancel, ID: requestID, Timestamp: time.Now().UTC()})
		})
	}

	requestDone := make(chan struct{})
	go func() {
		select {
		case <-r.Context().Done():
			sendCancel()
		case <-requestDone:
		}
	}()
	defer close(requestDone)

	if err := session.Send(protocol.Frame{
		Type:      protocol.FrameTypeRequestStart,
		ID:        requestID,
		Method:    r.Method,
		Path:      requestPath,
		Headers:   requestHeaders,
		Timestamp: startedAt,
	}); err != nil {
		statusCode = http.StatusBadGateway
		responseErr = err
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	buffer := make([]byte, protocol.DefaultChunkSize)
	for {
		readBytes, readErr := r.Body.Read(buffer)
		if readBytes > 0 {
			chunk := make([]byte, readBytes)
			copy(chunk, buffer[:readBytes])
			logEntry.RequestBytes += int64(readBytes)
			requestCapture.Observe(chunk)
			if err := session.Send(protocol.Frame{
				Type:      protocol.FrameTypeRequestBody,
				ID:        requestID,
				Chunk:     chunk,
				Timestamp: time.Now().UTC(),
			}); err != nil {
				statusCode = http.StatusBadGateway
				responseErr = err
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			statusCode = http.StatusBadRequest
			responseErr = readErr
			sendCancel()
			http.Error(w, readErr.Error(), http.StatusBadRequest)
			return
		}
	}

	if err := session.Send(protocol.Frame{Type: protocol.FrameTypeRequestEnd, ID: requestID, Timestamp: time.Now().UTC()}); err != nil {
		statusCode = http.StatusBadGateway
		responseErr = err
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var startFrame protocol.Frame
	select {
	case startFrame = <-stream.startCh:
	case responseErr = <-stream.errCh:
		statusCode = httpStatusForError(responseErr)
		http.Error(w, responseErr.Error(), statusCode)
		return
	case <-session.Conn.Closed():
		responseErr = errors.New("tunnel connection closed")
		statusCode = http.StatusBadGateway
		http.Error(w, responseErr.Error(), statusCode)
		return
	case <-r.Context().Done():
		responseErr = r.Context().Err()
		statusCode = 499
		sendCancel()
		return
	}

	statusCode = startFrame.Status
	responseHeaders = startFrame.Headers
	responseCapture = NewBodyCapture(contentTypeFromHeaders(responseHeaders))
	protocol.ApplyHeaders(w.Header(), responseHeaders)
	w.WriteHeader(startFrame.Status)

	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}

	for {
		select {
		case chunk, ok := <-stream.bodyCh:
			if !ok {
				return
			}
			if len(chunk) == 0 {
				continue
			}
			logEntry.ResponseBytes += int64(len(chunk))
			responseCapture.Observe(chunk)
			if _, err := w.Write(chunk); err != nil {
				responseErr = err
				sendCancel()
				return
			}
			if canFlush {
				flusher.Flush()
			}
		case responseErr = <-stream.errCh:
			if responseErr == nil {
				return
			}
			sendCancel()
			return
		case <-session.Conn.Closed():
			responseErr = errors.New("tunnel connection closed")
			sendCancel()
			return
		case <-r.Context().Done():
			responseErr = r.Context().Err()
			sendCancel()
			return
		}
	}
}


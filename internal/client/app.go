package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/protocol"
)

var Version = "dev"

type Options struct {
	Host       string
	BackendURL string
	Subdomain  string
}

type App struct {
	serverURL    *url.URL
	backendURL   *url.URL
	httpClient   *http.Client
	ws           *protocol.Connection
	connected    bool
	outbound     *protocol.FrameScheduler
	done         chan struct{}
	closeOnce    sync.Once
	activityMu   sync.Mutex
	lastActive   time.Time
	awaitingPong bool
	pingSentAt   time.Time

	requestsMu sync.RWMutex
	requests   map[string]*activeRequest
}

type createTunnelResponse struct {
	ID            string `json:"id"`
	Domain        string `json:"domain"`
	DomainKey     string `json:"domain_key"`
	ServerMessage string `json:"server_message"`
}

type activeRequest struct {
	id        string
	app       *App
	cancel    context.CancelFunc
	bodyCh    chan []byte
	bodyR     *io.PipeReader
	bodyW     *io.PipeWriter
	wsFrameCh chan *protocol.Frame

	closeBodyOnce sync.Once
}

func Run(ctx context.Context, options Options) error {
	serverURL, err := normalizeServerURL(options.Host)
	if err != nil {
		return err
	}
	backendURL, err := normalizeBackendURL(options.BackendURL)
	if err != nil {
		return err
	}

	app := &App{
		serverURL:  serverURL,
		backendURL: backendURL,
		httpClient: &http.Client{},
		outbound:   protocol.NewFrameScheduler(protocol.DefaultPerRequestFrameQueue),
		done:       make(chan struct{}),
		requests:   make(map[string]*activeRequest),
		lastActive: time.Now(),
	}
	defer app.Close()

	registration, err := app.createTunnel(ctx, options.Subdomain)
	if err != nil {
		return err
	}
	log.Printf("Tunnel created: %s", registration.Domain)
	if registration.ServerMessage != "" {
		log.Printf("Server message: %s", registration.ServerMessage)
	}

	if err := app.connectAndRegister(ctx, registration); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "[http-tunnels] websocket connected, final domain: %s\n", registration.Domain)
	log.Printf("Connected! Tunnel: %s", registration.Domain)
	log.Printf("Proxying to: %s", backendURL.String())

	go app.writeLoop()
	go app.heartbeatLoop()

	go func() {
		<-ctx.Done()
		app.Close()
	}()

	return app.readLoop()
}

func normalizeServerURL(host string) (*url.URL, error) {
	hostStr := strings.TrimSpace(host)
	if envHost := strings.TrimSpace(getenv("TUNNEL_HOST")); envHost != "" {
		hostStr = envHost
	}
	if hostStr == "" {
		hostStr = "https://t.kaenova.my.id"
	}
	parsed, err := url.Parse(hostStr)
	if err != nil {
		return nil, fmt.Errorf("invalid tunnel host: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid tunnel host: %s", hostStr)
	}
	return parsed, nil
}

func normalizeBackendURL(raw string) (*url.URL, error) {
	backend := strings.TrimSpace(raw)
	if !strings.HasPrefix(backend, "http://") && !strings.HasPrefix(backend, "https://") {
		backend = "http://" + backend
	}
	parsed, err := url.Parse(backend)
	if err != nil {
		return nil, fmt.Errorf("invalid backend url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid backend url: %s", raw)
	}
	return parsed, nil
}

func (a *App) createTunnel(ctx context.Context, subdomain string) (*createTunnelResponse, error) {
	createURL := fmt.Sprintf("%s://%s/new_tunnel", a.serverURL.Scheme, a.serverURL.Host)
	if subdomain = strings.TrimSpace(subdomain); subdomain != "" {
		createURL += "?subdomain=" + url.QueryEscape(subdomain)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create tunnel: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tunnel creation failed: %s: %s", resp.Status, string(body))
	}

	var result createTunnelResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode tunnel response: %w", err)
	}
	if result.Domain == "" || result.DomainKey == "" {
		return nil, fmt.Errorf("invalid tunnel response")
	}
	return &result, nil
}

func (a *App) connectAndRegister(ctx context.Context, registration *createTunnelResponse) error {
	wsScheme := "ws"
	if a.serverURL.Scheme == "https" {
		wsScheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/tunnel?domain=%s&domain_key=%s",
		wsScheme,
		a.serverURL.Host,
		url.QueryEscape(registration.Domain),
		url.QueryEscape(registration.DomainKey),
	)

	dialer := websocket.Dialer{EnableCompression: true}
	log.Printf("Dialing tunnel websocket: %s", wsURL)
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}
	log.Printf("Tunnel websocket connected: server=%s domain=%s", a.serverURL.Host, registration.Domain)
	ws := protocol.NewConnection(conn)
	if err := ws.Send(&protocol.Frame{
		Type:           protocol.FrameType_REGISTER,
		Domain:         registration.Domain,
		DomainKey:      registration.DomainKey,
		ClientVersion:  Version,
	}); err != nil {
		_ = ws.Close()
		return fmt.Errorf("register failed: %w", err)
	}

	frame, err := ws.ReadFrame()
	if err != nil {
		_ = ws.Close()
		return fmt.Errorf("registration failed: %w", err)
	}
	if frame.GetType() != protocol.FrameType_REGISTERED {
		_ = ws.Close()
		return fmt.Errorf("unexpected register response: %v", frame.GetType())
	}
	registration.ID = frame.GetTunnelId()
	a.ws = ws
	a.connected = true
	a.markActivity()
	return nil
}

func (a *App) readLoop() error {
	for {
		frame, err := a.ws.ReadFrame()
		if err != nil {
			select {
			case <-a.done:
				return nil
			default:
			}
			log.Printf("Tunnel websocket disconnected: %v", err)
			return err
		}
		a.markActivity()

		switch frame.GetType() {
		case protocol.FrameType_PING:
			_ = a.outbound.EnqueueControl(&protocol.Frame{Type: protocol.FrameType_PONG})
		case protocol.FrameType_PONG:
			a.clearPendingPing()
		case protocol.FrameType_REQUEST_START:
			a.handleRequestStart(frame)
		case protocol.FrameType_REQUEST_BODY:
			a.handleRequestBody(frame)
		case protocol.FrameType_REQUEST_END:
			a.handleRequestEnd(frame)
		case protocol.FrameType_REQUEST_CANCEL:
			a.handleRequestCancel(frame)
		case protocol.FrameType_WEBSOCKET_TEXT,
			protocol.FrameType_WEBSOCKET_BINARY,
			protocol.FrameType_WEBSOCKET_CLOSE,
			protocol.FrameType_WEBSOCKET_PING,
			protocol.FrameType_WEBSOCKET_PONG:
			a.handleWebSocketFrame(frame)
		}
	}
}

func (a *App) handleRequestStart(frame *protocol.Frame) {
	ctx, cancel := context.WithCancel(context.Background())
	bodyR, bodyW := io.Pipe()
	request := &activeRequest{
		id:        frame.GetRequestId(),
		app:       a,
		cancel:    cancel,
		bodyCh:    make(chan []byte, protocol.DefaultPerRequestFrameQueue),
		bodyR:     bodyR,
		bodyW:     bodyW,
		wsFrameCh: make(chan *protocol.Frame, protocol.DefaultPerRequestFrameQueue),
	}
	go request.bodyPump()

	a.requestsMu.Lock()
	a.requests[request.id] = request
	a.requestsMu.Unlock()

	go a.proxyRequest(ctx, request, frame)
}

func (a *App) handleRequestBody(frame *protocol.Frame) {
	request := a.getRequest(frame.GetRequestId())
	if request == nil {
		return
	}
	chunk := frame.GetChunk()
	if len(chunk) == 0 {
		return
	}
	copied := make([]byte, len(chunk))
	copy(copied, chunk)
	select {
	case request.bodyCh <- copied:
	case <-a.done:
	}
}

func (a *App) handleRequestEnd(frame *protocol.Frame) {
	if request := a.getRequest(frame.GetRequestId()); request != nil {
		request.closeBody(nil)
	}
}

func (a *App) handleRequestCancel(frame *protocol.Frame) {
	if request := a.getRequest(frame.GetRequestId()); request != nil {
		request.cancel()
		request.closeBody(context.Canceled)
		a.deleteRequest(frame.GetRequestId())
	}
}

func (a *App) handleWebSocketFrame(frame *protocol.Frame) {
	request := a.getRequest(frame.GetRequestId())
	if request == nil {
		return
	}
	select {
	case request.wsFrameCh <- frame:
	case <-a.done:
	}
}

func (a *App) proxyRequest(ctx context.Context, request *activeRequest, frame *protocol.Frame) {
	defer a.deleteRequest(request.id)
	defer request.cancel()
	defer request.closeBody(nil)

	destination, err := protocol.BuildDestinationURL(a.backendURL, frame.GetPath())
	if err != nil {
		a.enqueueResponseError(request.id, http.StatusBadGateway, err)
		return
	}

	// Detect WebSocket upgrade request
	headers := convertFrameHeaders(frame.GetHeaders())
	if strings.ToLower(getHeaderValue(headers, "Upgrade")) == "websocket" {
		a.proxyWebSocket(ctx, request, frame, destination.String(), headers)
		return
	}

	var body io.Reader
	if frame.GetContentLength() != 0 {
		body = request.bodyR
	}
	method := frame.GetMethod()
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, destination.String(), body)
	if err != nil {
		a.enqueueResponseError(request.id, http.StatusBadGateway, err)
		return
	}
	if frame.GetContentLength() > 0 {
		req.ContentLength = frame.GetContentLength()
	}
	protocol.ApplyHeaders(req.Header, headers)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.enqueueResponseError(request.id, http.StatusBadGateway, err)
		return
	}
	defer resp.Body.Close()

	responseHeaders := make(map[string]*protocol.StringList)
	for key, values := range protocol.CloneHeaders(resp.Header) {
		responseHeaders[key] = &protocol.StringList{Values: values}
	}
	if err := a.outbound.Enqueue(&protocol.Frame{
		Type:            protocol.FrameType_RESPONSE_START,
		RequestId:       request.id,
		Status:          int32(resp.StatusCode),
		ResponseHeaders: responseHeaders,
	}); err != nil {
		return
	}

	buf := make([]byte, protocol.DefaultChunkSize)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := a.outbound.Enqueue(&protocol.Frame{
				Type:      protocol.FrameType_RESPONSE_BODY,
				RequestId: request.id,
				Chunk:     chunk,
			}); err != nil {
				return
			}
		}
		if readErr == io.EOF {
			_ = a.outbound.Enqueue(&protocol.Frame{Type: protocol.FrameType_RESPONSE_END, RequestId: request.id})
			return
		}
		if readErr != nil {
			a.enqueueResponseError(request.id, http.StatusBadGateway, readErr)
			return
		}
	}
}

func (r *activeRequest) bodyPump() {
	defer r.bodyW.Close()
	for chunk := range r.bodyCh {
		if len(chunk) == 0 {
			continue
		}
		if _, err := r.bodyW.Write(chunk); err != nil {
			return
		}
	}
}

func (r *activeRequest) closeBody(err error) {
	r.closeBodyOnce.Do(func() {
		close(r.bodyCh)
		if err != nil {
			_ = r.bodyW.CloseWithError(err)
		}
	})
}

func getHeaderValue(headers map[string][]string, key string) string {
	for k, v := range headers {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func (a *App) proxyWebSocket(ctx context.Context, request *activeRequest, frame *protocol.Frame, destURL string, headers map[string][]string) {
	// Prepare dialer with headers from the original request
	dialer := websocket.Dialer{
		EnableCompression: true,
		HandshakeTimeout:  30 * time.Second,
	}

	backendURL, err := url.Parse(destURL)
	if err != nil {
		a.enqueueResponseError(request.id, http.StatusBadGateway, err)
		return
	}
	backendURL.Scheme = websocketSchemeForBackend(backendURL.Scheme)

	httpHeader := make(http.Header)
	for k, vals := range headers {
		for _, v := range vals {
			httpHeader.Add(k, v)
		}
	}

	log.Printf("Dialing backend websocket: %s", backendURL.String())
	backendConn, resp, err := dialer.DialContext(ctx, backendURL.String(), httpHeader)
	if err != nil {
		// Backend rejected the WebSocket upgrade or other error
		status := http.StatusBadGateway
		if resp != nil {
			status = resp.StatusCode
			// We could read the body and send it, but for simplicity send error
		}
		a.enqueueResponseError(request.id, status, fmt.Errorf("backend websocket dial %s failed: %w", backendURL.String(), err))
		return
	}
	defer backendConn.Close()

	// Send 101 RESPONSE_START to tunnel
	responseHeaders := make(map[string]*protocol.StringList)
	if resp != nil {
		for key, values := range protocol.CloneHeaders(resp.Header) {
			responseHeaders[key] = &protocol.StringList{Values: values}
		}
	}
	if err := a.outbound.Enqueue(&protocol.Frame{
		Type:            protocol.FrameType_RESPONSE_START,
		RequestId:       request.id,
		Status:          int32(http.StatusSwitchingProtocols),
		ResponseHeaders: responseHeaders,
	}); err != nil {
		return
	}

	// Bridge goroutine: backend -> tunnel
	go func() {
		for {
			msgType, data, err := backendConn.ReadMessage()
			if err != nil {
				code := websocket.CloseNormalClosure
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
					code = websocket.CloseInternalServerErr
				}
				_ = a.outbound.Enqueue(&protocol.Frame{
					Type:        protocol.FrameType_WEBSOCKET_CLOSE,
					RequestId:   request.id,
					WsCloseCode: int32(code),
				})
				return
			}
			var ft protocol.FrameType
			switch msgType {
			case websocket.TextMessage:
				ft = protocol.FrameType_WEBSOCKET_TEXT
			case websocket.BinaryMessage:
				ft = protocol.FrameType_WEBSOCKET_BINARY
			case websocket.CloseMessage:
				ft = protocol.FrameType_WEBSOCKET_CLOSE
				var code int32
				if len(data) >= 2 {
					code = int32(data[0])<<8 | int32(data[1])
				} else {
					code = int32(websocket.CloseNoStatusReceived)
				}
				text := ""
				if len(data) > 2 {
					text = string(data[2:])
				}
				_ = a.outbound.Enqueue(&protocol.Frame{
					Type:        ft,
					RequestId:   request.id,
					Chunk:       data,
					WsCloseCode: code,
					WsCloseText: text,
				})
				return
			case websocket.PingMessage:
				ft = protocol.FrameType_WEBSOCKET_PING
			case websocket.PongMessage:
				ft = protocol.FrameType_WEBSOCKET_PONG
			default:
				continue
			}
			if err := a.outbound.Enqueue(&protocol.Frame{
				Type:      ft,
				RequestId: request.id,
				Chunk:     data,
			}); err != nil {
				return
			}
		}
	}()

	// Main loop: tunnel -> backend
	for wsFrame := range request.wsFrameCh {
		var msgType int
		var data []byte
		switch wsFrame.Type {
		case protocol.FrameType_WEBSOCKET_TEXT:
			msgType = websocket.TextMessage
			data = wsFrame.GetChunk()
		case protocol.FrameType_WEBSOCKET_BINARY:
			msgType = websocket.BinaryMessage
			data = wsFrame.GetChunk()
		case protocol.FrameType_WEBSOCKET_CLOSE:
			msgType = websocket.CloseMessage
			code := int(wsFrame.GetWsCloseCode())
			if code == 0 {
				code = websocket.CloseNormalClosure
			}
			text := wsFrame.GetWsCloseText()
			if text == "" {
				text = wsFrame.GetError()
			}
			data = websocket.FormatCloseMessage(code, text)
			_ = backendConn.WriteMessage(msgType, data)
			return
		case protocol.FrameType_WEBSOCKET_PING:
			msgType = websocket.PingMessage
			data = wsFrame.GetChunk()
		case protocol.FrameType_WEBSOCKET_PONG:
			msgType = websocket.PongMessage
			data = wsFrame.GetChunk()
		default:
			continue
		}
		if err := backendConn.WriteMessage(msgType, data); err != nil {
			log.Printf("ws backend write failed: %v", err)
			return
		}
	}
}

func (a *App) getRequest(requestID string) *activeRequest {
	a.requestsMu.RLock()
	defer a.requestsMu.RUnlock()
	return a.requests[requestID]
}

func (a *App) deleteRequest(requestID string) {
	a.requestsMu.Lock()
	request := a.requests[requestID]
	delete(a.requests, requestID)
	a.requestsMu.Unlock()
	if request != nil {
		request.closeBody(nil)
	}
}

func (a *App) enqueueResponseError(requestID string, status int, err error) {
	if err == nil {
		return
	}
	_ = a.outbound.Enqueue(&protocol.Frame{
		Type:      protocol.FrameType_RESPONSE_ERROR,
		RequestId: requestID,
		Status:    int32(status),
		Error:     err.Error(),
	})
}

func (a *App) writeLoop() {
	for {
		frame, err := a.outbound.Next()
		if err != nil {
			return
		}
		if err := a.ws.Send(frame); err != nil {
			log.Printf("Tunnel websocket write failed: %v", err)
			a.Close()
			return
		}
		a.markActivity()
	}
}

func (a *App) heartbeatLoop() {
	ticker := time.NewTicker(protocol.DefaultPingPeriod / 2)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			if a.shouldCloseForMissedPong() {
				log.Printf("Tunnel heartbeat timed out, closing websocket")
				a.Close()
				return
			}
			if a.shouldPing() {
				a.notePingSent()
				if err := a.outbound.EnqueueControl(&protocol.Frame{Type: protocol.FrameType_PING}); err != nil {
					a.Close()
					return
				}
			}
		}
	}
}

func (a *App) Close() {
	a.closeOnce.Do(func() {
		if a.connected {
			fmt.Fprintln(os.Stderr, "[http-tunnels] websocket disconnected")
		}
		log.Printf("Closing tunnel websocket")
		close(a.done)
		a.outbound.Close()
		if a.ws != nil {
			_ = a.ws.Close()
		}
		a.requestsMu.Lock()
		for id, request := range a.requests {
			request.cancel()
			request.closeBody(context.Canceled)
			delete(a.requests, id)
		}
		a.requestsMu.Unlock()
	})
}

func (a *App) markActivity() {
	a.activityMu.Lock()
	defer a.activityMu.Unlock()
	a.lastActive = time.Now()
}

func (a *App) shouldPing() bool {
	a.activityMu.Lock()
	defer a.activityMu.Unlock()
	return !a.awaitingPong && time.Since(a.lastActive) >= protocol.DefaultPingPeriod
}

func (a *App) notePingSent() {
	a.activityMu.Lock()
	defer a.activityMu.Unlock()
	a.awaitingPong = true
	a.pingSentAt = time.Now()
}

func (a *App) clearPendingPing() {
	a.activityMu.Lock()
	defer a.activityMu.Unlock()
	a.awaitingPong = false
}

func (a *App) shouldCloseForMissedPong() bool {
	a.activityMu.Lock()
	defer a.activityMu.Unlock()
	return a.awaitingPong && time.Since(a.pingSentAt) > protocol.DefaultPongWait
}

func convertFrameHeaders(headers map[string]*protocol.StringList) map[string][]string {
	if headers == nil {
		return nil
	}
	result := make(map[string][]string, len(headers))
	for key, values := range headers {
		result[key] = values.GetValues()
	}
	return result
}

func websocketSchemeForBackend(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "https":
		return "wss"
	case "http", "":
		return "ws"
	case "ws", "wss":
		return scheme
	default:
		return "ws"
	}
}

func getenv(key string) string {
	return os.Getenv(key)
}

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/protocol"
)

const (
	transportAuto      = "auto"
	transportHTTP2     = protocol.TransportHTTP2
	transportWebSocket = protocol.TransportWebSocket
)

var Version = "dev"

type Options struct {
	Host               string
	BackendURL         string
	Subdomain          string
	PreferredTransport string
	HTTPClient         *http.Client
	WSDialer           *websocket.Dialer
}

type App struct {
	serverURL          *url.URL
	backendURL         *url.URL
	httpClient         *http.Client
	wsDialer           *websocket.Dialer
	conn               *protocol.Connection // websocket only
	transport          string
	preferredTransport string
	connected          bool
	registration       *createTunnelResponse
	appCtx             context.Context
	appCancel          context.CancelFunc
	outbound           *protocol.FrameScheduler
	done               chan struct{}
	closeOnce          sync.Once
	activityMu         sync.Mutex
	lastActive         time.Time
	awaitingPong       bool
	pingSentAt         time.Time
	h2Bootstrap        bool
	h2StreamsMu        sync.Mutex
	h2Streams          map[*protocol.H2TunnelStream]struct{}

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
	id     string
	app    *App
	cancel context.CancelFunc
	bodyCh chan []byte
	bodyR  *io.PipeReader
	bodyW  *io.PipeWriter
	send   func(*protocol.Frame) error

	finished      chan struct{}
	finishOnce    sync.Once
	closeBodyOnce sync.Once
}

type h2RequestBody struct {
	reader       *io.PipeReader
	writer       *io.PipeWriter
	closeOnce    sync.Once
	forwardingMu sync.RWMutex
	forwarding   bool
}

func newH2RequestBody() *h2RequestBody {
	reader, writer := io.Pipe()
	return &h2RequestBody{
		reader:     reader,
		writer:     writer,
		forwarding: true,
	}
}

func (b *h2RequestBody) Reader() io.ReadCloser { return b.reader }

func (b *h2RequestBody) WriteChunk(chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}
	b.forwardingMu.RLock()
	forwarding := b.forwarding
	b.forwardingMu.RUnlock()
	if !forwarding {
		return nil
	}
	_, err := b.writer.Write(chunk)
	if err != nil {
		b.StopForwarding()
		return nil
	}
	return nil
}

func (b *h2RequestBody) StopForwarding() {
	b.forwardingMu.Lock()
	b.forwarding = false
	b.forwardingMu.Unlock()
	_ = b.writer.Close()
}

func (b *h2RequestBody) Close(err error) {
	b.closeOnce.Do(func() {
		b.forwardingMu.Lock()
		b.forwarding = false
		b.forwardingMu.Unlock()
		if err != nil {
			_ = b.writer.CloseWithError(err)
			return
		}
		_ = b.writer.Close()
	})
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
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	wsDialer := options.WSDialer
	if wsDialer == nil {
		wsDialer = &websocket.Dialer{EnableCompression: true}
	}
	appCtx, appCancel := context.WithCancel(ctx)

	app := &App{
		serverURL:          serverURL,
		backendURL:         backendURL,
		httpClient:         httpClient,
		wsDialer:           wsDialer,
		preferredTransport: normalizePreferredTransport(options.PreferredTransport),
		appCtx:             appCtx,
		appCancel:          appCancel,
		outbound:           protocol.NewFrameScheduler(protocol.DefaultPerRequestFrameQueue),
		done:               make(chan struct{}),
		requests:           make(map[string]*activeRequest),
		h2Streams:          make(map[*protocol.H2TunnelStream]struct{}),
		lastActive:         time.Now(),
	}
	defer app.Close()

	registration, err := app.createTunnel(appCtx, options.Subdomain)
	if err != nil {
		return err
	}
	log.Printf("Tunnel created: %s", registration.Domain)
	if registration.ServerMessage != "" {
		log.Printf("Server message: %s", registration.ServerMessage)
	}

	if err := app.connectAndRegister(appCtx, registration); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "[http-tunnels] %s connected, final domain: %s\n", app.transportLabel(), registration.Domain)
	log.Printf("Connected! Tunnel: %s transport=%s", registration.Domain, app.transportLabel())
	log.Printf("Proxying to: %s", backendURL.String())

	go func() {
		<-ctx.Done()
		app.Close()
	}()

	if app.transport == protocol.TransportHTTP2 {
		app.startHTTP2StreamPool()
		<-app.done
		return nil
	}

	go app.writeLoop()
	go app.heartbeatLoop()
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

func normalizePreferredTransport(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", transportAuto:
		return transportAuto
	case transportHTTP2:
		return transportHTTP2
	case transportWebSocket:
		return transportWebSocket
	default:
		return transportAuto
	}
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
	a.registration = registration
	if a.preferredTransport == transportWebSocket {
		if err := a.connectWebSocketAndRegister(ctx, registration); err != nil {
			return fmt.Errorf("websocket connection failed: %w", err)
		}
		return nil
	}

	if a.shouldAttemptHTTP2() {
		if err := a.probeHTTP2(ctx); err == nil {
			if err := a.connectHTTP2AndRegister(ctx, registration); err == nil {
				return nil
			}
			log.Printf("Tunnel http2 connection failed, falling back to websocket")
		} else {
			log.Printf("Tunnel http2 probe failed, falling back to websocket: %v", err)
		}
	}

	if err := a.connectWebSocketAndRegister(ctx, registration); err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}
	return nil
}

func (a *App) probeHTTP2(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	probeURL := fmt.Sprintf("%s://%s/ping", a.serverURL.Scheme, a.serverURL.Host)
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, probeURL, nil)
	if err != nil {
		return err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.ProtoMajor < 2 {
		return fmt.Errorf("server responded with %s", resp.Proto)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server probe failed: %s", resp.Status)
	}
	return nil
}

func (a *App) shouldAttemptHTTP2() bool {
	if a == nil || a.serverURL == nil {
		return false
	}
	if a.preferredTransport == transportWebSocket {
		return false
	}
	return strings.EqualFold(a.serverURL.Scheme, "https")
}

func (a *App) connectHTTP2AndRegister(ctx context.Context, registration *createTunnelResponse) error {
	stream, err := a.openHTTP2WorkerStream(ctx)
	if err != nil {
		return err
	}
	a.h2Bootstrap = true
	a.transport = protocol.TransportHTTP2
	a.connected = true
	a.markActivity()
	log.Printf("Tunnel http2 transport ready: server=%s domain=%s", a.serverURL.Host, registration.Domain)
	go a.serveHTTP2WorkerLoop(0, stream)
	return nil
}

func (a *App) connectWebSocketAndRegister(ctx context.Context, registration *createTunnelResponse) error {
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

	dialer := a.websocketDialer()
	log.Printf("Dialing tunnel websocket: %s", wsURL)
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return err
	}
	stream := protocol.NewConnection(conn)
	if err := stream.Send(&protocol.Frame{
		Type:      protocol.FrameType_REGISTER,
		Domain:    registration.Domain,
		DomainKey: registration.DomainKey,
	}); err != nil {
		_ = stream.Close()
		return fmt.Errorf("register failed: %w", err)
	}

	frame, err := stream.ReadFrame()
	if err != nil {
		_ = stream.Close()
		return fmt.Errorf("registration failed: %w", err)
	}
	if frame.GetType() != protocol.FrameType_REGISTERED {
		_ = stream.Close()
		return fmt.Errorf("unexpected register response: %v", frame.GetType())
	}
	registration.ID = frame.GetTunnelId()
	a.conn = stream
	a.transport = protocol.TransportWebSocket
	a.connected = true
	a.markActivity()
	log.Printf("Tunnel websocket connected: server=%s domain=%s", a.serverURL.Host, registration.Domain)
	return nil
}

func (a *App) openHTTP2WorkerStream(ctx context.Context) (*protocol.H2TunnelStream, error) {
	if a.registration == nil {
		return nil, fmt.Errorf("tunnel registration is not available")
	}
	streamURL := fmt.Sprintf("%s://%s/tunnel/h2/stream?domain=%s&domain_key=%s",
		a.serverURL.Scheme,
		a.serverURL.Host,
		url.QueryEscape(a.registration.Domain),
		url.QueryEscape(a.registration.DomainKey),
	)

	bodyReader, bodyWriter := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, streamURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Cache-Control", "no-store")

	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := a.httpClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		if resp.ProtoMajor < 2 {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			errCh <- fmt.Errorf("server did not negotiate HTTP/2: %s %s", resp.Proto, strings.TrimSpace(string(body)))
			return
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			errCh <- fmt.Errorf("http2 worker stream failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
			return
		}
		respCh <- resp
	}()

	var resp *http.Response
	select {
	case <-ctx.Done():
		_ = bodyWriter.CloseWithError(ctx.Err())
		return nil, ctx.Err()
	case err := <-errCh:
		_ = bodyWriter.CloseWithError(err)
		return nil, err
	case resp = <-respCh:
	}

	stream := protocol.NewH2TunnelStream(protocol.H2TunnelStreamOptions{
		Reader: resp.Body,
		Writer: bodyWriter,
		Close: func() error {
			_ = bodyWriter.Close()
			return resp.Body.Close()
		},
	})
	a.registerHTTP2Stream(stream)
	return stream, nil
}

func (a *App) startHTTP2StreamPool() {
	count := a.http2StreamPoolSize()
	startIndex := 0
	if a.h2Bootstrap && count > 0 {
		startIndex = 1
	}
	for i := startIndex; i < count; i++ {
		go a.http2StreamWorker(i)
	}
	log.Printf("Started %d native HTTP/2 worker streams", count)
}

func (a *App) http2StreamPoolSize() int {
	const defaultStreams = 8
	if value := strings.TrimSpace(getenv("TUNNEL_HTTP2_STREAMS")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultStreams
}

func (a *App) http2StreamWorker(index int) {
	for {
		select {
		case <-a.done:
			return
		default:
		}
		stream, err := a.openHTTP2WorkerStream(a.appCtx)
		if err != nil {
			select {
			case <-a.done:
				return
			default:
			}
			log.Printf("HTTP/2 worker stream %d open failed: %v", index, err)
			time.Sleep(250 * time.Millisecond)
			continue
		}
		a.serveHTTP2WorkerLoop(index, stream)
	}
}

func (a *App) serveHTTP2WorkerLoop(index int, stream *protocol.H2TunnelStream) {
	defer func() {
		a.unregisterHTTP2Stream(stream)
		_ = stream.Close()
	}()
	if err := a.serveHTTP2WorkerStream(stream); err != nil {
		select {
		case <-a.done:
			return
		default:
		}
		log.Printf("HTTP/2 worker stream %d closed: %v", index, err)
	}
}

func (a *App) serveHTTP2WorkerStream(stream *protocol.H2TunnelStream) error {
	requestStart, err := stream.ReadRequestStart()
	if err != nil {
		return err
	}
	a.markActivity()

	requestCtx, cancel := context.WithCancel(a.appCtx)
	defer cancel()

	body := newH2RequestBody()
	bodyReadDone := make(chan error, 1)
	go func() {
		bodyReadDone <- a.consumeHTTP2RequestBody(stream, body, cancel)
	}()

	proxyErr := a.proxyHTTP2Request(requestCtx, stream, requestStart, body)
	select {
	case err := <-bodyReadDone:
		if proxyErr == nil && err != nil && !errorsIsContext(err) {
			return err
		}
	default:
	}
	return proxyErr
}

func (a *App) consumeHTTP2RequestBody(stream *protocol.H2TunnelStream, body *h2RequestBody, cancel context.CancelFunc) error {
	for {
		messageType, payload, err := stream.ReadMessage()
		if err != nil {
			body.Close(err)
			cancel()
			return err
		}
		a.markActivity()
		switch messageType {
		case protocol.H2MessageRequestBody:
			if err := body.WriteChunk(payload); err != nil {
				body.StopForwarding()
			}
		case protocol.H2MessageRequestEnd:
			body.Close(nil)
			return nil
		case protocol.H2MessageRequestCancel:
			body.Close(context.Canceled)
			cancel()
			return context.Canceled
		default:
			body.Close(fmt.Errorf("unexpected h2 request message: %d", messageType))
			cancel()
			return fmt.Errorf("unexpected h2 request message: %d", messageType)
		}
	}
}

func (a *App) proxyHTTP2Request(ctx context.Context, stream *protocol.H2TunnelStream, requestStart protocol.H2RequestStart, body *h2RequestBody) error {
	defer body.Close(nil)
	destination, err := protocol.BuildDestinationURL(a.backendURL, requestStart.Path)
	if err != nil {
		return stream.WriteResponseError(http.StatusBadGateway, err.Error())
	}

	method := requestStart.Method
	if method == "" {
		method = http.MethodGet
	}
	var requestBody io.Reader
	if requestStart.ContentLength != 0 {
		requestBody = body.Reader()
	}
	req, err := http.NewRequestWithContext(ctx, method, destination.String(), requestBody)
	if err != nil {
		return stream.WriteResponseError(http.StatusBadGateway, err.Error())
	}
	if requestStart.ContentLength > 0 {
		req.ContentLength = requestStart.ContentLength
	}
	protocol.ApplyHeaders(req.Header, requestStart.Headers)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return stream.WriteResponseError(http.StatusBadGateway, err.Error())
	}
	defer resp.Body.Close()
	body.StopForwarding()

	if err := stream.WriteResponseStart(protocol.H2ResponseStart{
		Status:  resp.StatusCode,
		Headers: protocol.CloneHeaders(resp.Header),
	}); err != nil {
		return err
	}

	buf := make([]byte, protocol.DefaultChunkSize)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := stream.WriteResponseBody(chunk); err != nil {
				return err
			}
			a.markActivity()
		}
		if readErr == io.EOF {
			return stream.WriteResponseEnd()
		}
		if readErr != nil {
			return stream.WriteResponseError(http.StatusBadGateway, readErr.Error())
		}
	}
}

func (a *App) readLoop() error {
	for {
		frame, err := a.conn.ReadFrame()
		if err != nil {
			select {
			case <-a.done:
				return nil
			default:
			}
			log.Printf("Tunnel %s disconnected: %v", a.transportLabel(), err)
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
		}
	}
}

func (a *App) handleRequestStart(frame *protocol.Frame) {
	ctx, cancel := context.WithCancel(a.appCtx)
	request := newActiveRequest(a, frame.GetRequestId(), cancel, func(frame *protocol.Frame) error {
		return a.outbound.Enqueue(frame)
	})
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

func (a *App) proxyRequest(ctx context.Context, request *activeRequest, frame *protocol.Frame) {
	defer request.finish()
	defer request.cancel()
	defer request.closeBody(nil)
	defer a.deleteRequest(request.id)

	destination, err := protocol.BuildDestinationURL(a.backendURL, frame.GetPath())
	if err != nil {
		a.sendResponseError(request, http.StatusBadGateway, err)
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
		a.sendResponseError(request, http.StatusBadGateway, err)
		return
	}
	if frame.GetContentLength() > 0 {
		req.ContentLength = frame.GetContentLength()
	}
	protocol.ApplyHeaders(req.Header, convertFrameHeaders(frame.GetHeaders()))

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.sendResponseError(request, http.StatusBadGateway, err)
		return
	}
	defer resp.Body.Close()

	responseHeaders := make(map[string]*protocol.StringList)
	for key, values := range protocol.CloneHeaders(resp.Header) {
		responseHeaders[key] = &protocol.StringList{Values: values}
	}
	if err := request.send(&protocol.Frame{
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
			if err := request.send(&protocol.Frame{
				Type:      protocol.FrameType_RESPONSE_BODY,
				RequestId: request.id,
				Chunk:     chunk,
			}); err != nil {
				return
			}
		}
		if readErr == io.EOF {
			_ = request.send(&protocol.Frame{Type: protocol.FrameType_RESPONSE_END, RequestId: request.id})
			return
		}
		if readErr != nil {
			a.sendResponseError(request, http.StatusBadGateway, readErr)
			return
		}
	}
}

func newActiveRequest(app *App, requestID string, cancel context.CancelFunc, send func(*protocol.Frame) error) *activeRequest {
	bodyR, bodyW := io.Pipe()
	return &activeRequest{
		id:       requestID,
		app:      app,
		cancel:   cancel,
		bodyCh:   make(chan []byte, protocol.DefaultPerRequestFrameQueue),
		bodyR:    bodyR,
		bodyW:    bodyW,
		send:     send,
		finished: make(chan struct{}),
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

func (r *activeRequest) finish() {
	r.finishOnce.Do(func() {
		close(r.finished)
	})
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

func (a *App) sendResponseError(request *activeRequest, status int, err error) {
	if err == nil || request == nil || request.send == nil {
		return
	}
	_ = request.send(&protocol.Frame{Type: protocol.FrameType_RESPONSE_ERROR, RequestId: request.id, Status: int32(status), Error: err.Error()})
}

func (a *App) writeLoop() {
	for {
		frame, err := a.outbound.Next()
		if err != nil {
			return
		}
		if err := a.conn.Send(frame); err != nil {
			log.Printf("Tunnel %s write failed: %v", a.transportLabel(), err)
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
				log.Printf("Tunnel heartbeat timed out, closing %s transport", a.transportLabel())
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
			fmt.Fprintf(os.Stderr, "[http-tunnels] %s disconnected\n", a.transportLabel())
		}
		log.Printf("Closing tunnel %s transport", a.transportLabel())
		if a.appCancel != nil {
			a.appCancel()
		}
		close(a.done)
		a.outbound.Close()
		if a.conn != nil {
			_ = a.conn.Close()
		}
		a.h2StreamsMu.Lock()
		for stream := range a.h2Streams {
			_ = stream.Close()
			delete(a.h2Streams, stream)
		}
		a.h2StreamsMu.Unlock()
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

func (a *App) registerHTTP2Stream(stream *protocol.H2TunnelStream) {
	if stream == nil {
		return
	}
	a.h2StreamsMu.Lock()
	defer a.h2StreamsMu.Unlock()
	a.h2Streams[stream] = struct{}{}
}

func (a *App) unregisterHTTP2Stream(stream *protocol.H2TunnelStream) {
	if stream == nil {
		return
	}
	a.h2StreamsMu.Lock()
	defer a.h2StreamsMu.Unlock()
	delete(a.h2Streams, stream)
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

func errorsIsContext(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

func (a *App) transportLabel() string {
	if strings.TrimSpace(a.transport) == "" {
		return protocol.TransportWebSocket
	}
	return a.transport
}

func (a *App) websocketDialer() *websocket.Dialer {
	if a.wsDialer != nil {
		clone := *a.wsDialer
		clone.EnableCompression = true
		return &clone
	}
	return &websocket.Dialer{EnableCompression: true}
}

func getenv(key string) string {
	return os.Getenv(key)
}

package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
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

const DefaultTunnelHost = "https://t.kaenova.my.id"

var Version = "dev"

type Config struct {
	Subdomain         string
	TunnelServer      *url.URL
	DestinationServer *url.URL
	Verbose           bool
}

type tunnelConfig struct {
	ID        string
	Domain    string
	DomainKey string
}

type tunnelRegistrationResponse struct {
	ID            string  `json:"id"`
	Domain        string  `json:"domain"`
	DomainKey     string  `json:"domain_key"`
	ServerMessage *string `json:"server_message"`
}

type inboundRequest struct {
	ID      string
	Method  string
	Path    string
	Headers map[string][]string
	Body    *io.PipeWriter
	Cancel  context.CancelFunc
}

type App struct {
	config     *Config
	httpClient *http.Client
	conn       *protocol.Connection
	requests   sync.Map
}

func (a *App) verbose(format string, v ...any) {
	if a.config.Verbose {
		log.Printf("[verbose] "+format, v...)
	}
}

func Run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "update":
			return RunUpdate()
		case "help", "-h", "--help":
			printUsage()
			return nil
		}
	}

	config, err := parseConfig(args)
	if err != nil {
		return err
	}

	app := &App{
		config: config,
		httpClient: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}

	tunnel, err := app.requestNewTunnel()
	if err != nil {
		return err
	}

	return app.runTunnel(tunnel)
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "http-tunnels - a simple HTTP tunnel client")
	fmt.Fprintln(os.Stderr, "Github : https://github.com/kaenova/http-tunnels")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  http-tunnels [options] <destination_server>")
	fmt.Fprintln(os.Stderr, "  http-tunnels update")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  -host string")
	fmt.Fprintf(os.Stderr, "    \tPublic tunnel host address (default %q or TUNNEL_HOST env var)\n", DefaultTunnelHost)
	fmt.Fprintln(os.Stderr, "  -subdomain string")
	fmt.Fprintln(os.Stderr, "    \tSubdomain to use for the tunnel")
	fmt.Fprintln(os.Stderr, "  -verbose")
	fmt.Fprintln(os.Stderr, "    \tEnable verbose request/response logging")
}

func parseConfig(args []string) (*Config, error) {
	tunnelHost := os.Getenv("TUNNEL_HOST")
	if strings.TrimSpace(tunnelHost) == "" {
		tunnelHost = DefaultTunnelHost
	}

	fs := flag.NewFlagSet("http-tunnels", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	subdomain := fs.String("subdomain", "", "Subdomain to use for the tunnel")
	host := fs.String("host", tunnelHost, "Public tunnel host address")
	verbose := fs.Bool("verbose", false, "Enable verbose request/response logging")
	if err := fs.Parse(args); err != nil {
		printUsage()
		return nil, err
	}

	destinationServer := strings.TrimSpace(fs.Arg(0))
	if destinationServer == "" {
		printUsage()
		return nil, errors.New("destination server is required")
	}

	tunnelURL, err := url.Parse(*host)
	if err != nil {
		return nil, fmt.Errorf("invalid tunnel host URL: %w", err)
	}
	if tunnelURL.Scheme != "http" && tunnelURL.Scheme != "https" {
		return nil, fmt.Errorf("invalid tunnel host URL: %s", *host)
	}
	if tunnelURL.Host == "" {
		return nil, fmt.Errorf("invalid tunnel host URL: %s", *host)
	}

	destinationURL, err := url.Parse(destinationServer)
	if err != nil {
		return nil, fmt.Errorf("invalid destination server URL: %w", err)
	}
	if destinationURL.Scheme != "http" && destinationURL.Scheme != "https" {
		return nil, fmt.Errorf("invalid destination server URL: %s", destinationServer)
	}
	if destinationURL.Host == "" {
		return nil, fmt.Errorf("invalid destination server URL: %s", destinationServer)
	}

	return &Config{
		Subdomain:         strings.TrimSpace(*subdomain),
		TunnelServer:      tunnelURL,
		DestinationServer: destinationURL,
		Verbose:           *verbose,
	}, nil
}

func (a *App) requestNewTunnel() (*tunnelConfig, error) {
	rawQuery := ""
	if a.config.Subdomain != "" {
		rawQuery = "subdomain=" + url.QueryEscape(a.config.Subdomain)
	}
	endpoint := url.URL{
		Scheme:   a.config.TunnelServer.Scheme,
		Host:     a.config.TunnelServer.Host,
		Path:     "/new_tunnel",
		RawQuery: rawQuery,
	}

	resp, err := a.httpClient.Post(endpoint.String(), "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("submitting new domain error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, fmt.Errorf("failed to create tunnel: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var registration tunnelRegistrationResponse
	if err := json.NewDecoder(resp.Body).Decode(&registration); err != nil {
		return nil, fmt.Errorf("decoding tunnel response error: %w", err)
	}

	log.Printf("Tunnel created with domain: %s", registration.Domain)
	log.Printf("Domain key: %s", registration.DomainKey)
	if registration.ServerMessage != nil && strings.TrimSpace(*registration.ServerMessage) != "" {
		log.Printf("Server message: %s", strings.TrimSpace(*registration.ServerMessage))
	}

	return &tunnelConfig{
		ID:        registration.ID,
		Domain:    registration.Domain,
		DomainKey: registration.DomainKey,
	}, nil
}

func (a *App) runTunnel(tunnel *tunnelConfig) error {
	wsScheme := "ws"
	if a.config.TunnelServer.Scheme == "https" {
		wsScheme = "wss"
	}

	endpoint := url.URL{
		Scheme: wsScheme,
		Host:   a.config.TunnelServer.Host,
		Path:   "/tunnel",
		RawQuery: url.Values{
			"domain":     []string{tunnel.Domain},
			"domain_key": []string{tunnel.DomainKey},
		}.Encode(),
	}

	log.Printf("Connecting to websocket: %s", endpoint.String())

	wsConn, resp, err := websocket.DefaultDialer.Dial(endpoint.String(), nil)
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
			return fmt.Errorf("failed to connect to tunnel server: %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("failed to connect to tunnel server: %w", err)
	}

	a.conn = protocol.NewConnection(wsConn)
	log.Printf("Connected to tunnel server")
	defer a.closePendingRequests(errors.New("tunnel connection closed"))

	if err := a.conn.ReadLoop(a.handleFrame); err != nil {
		return err
	}
	return nil
}

func (a *App) handleFrame(frame protocol.Frame) error {
	switch frame.Type {
	case protocol.FrameTypeRequestStart:
		return a.startRequest(frame)
	case protocol.FrameTypeRequestBody:
		return a.writeRequestChunk(frame)
	case protocol.FrameTypeRequestEnd:
		return a.finishRequest(frame, nil)
	case protocol.FrameTypeRequestCancel:
		return a.finishRequest(frame, context.Canceled)
	case protocol.FrameTypeWebSocketUpgrade:
		return a.startWebSocketTunnel(frame)
	case protocol.FrameTypeWebSocketData:
		return a.writeWebSocketData(frame)
	case protocol.FrameTypeWebSocketClose:
		return a.closeWebSocketTunnel(frame, nil)
	case protocol.FrameTypeWebSocketError:
		return a.closeWebSocketTunnel(frame, errors.New(frame.Error))
	default:
		return nil
	}
}

func (a *App) startRequest(frame protocol.Frame) error {
	a.verbose("REQUEST_START %s %s (id=%s)", frame.Method, frame.Path, frame.ID[:8])
	ctx, cancel := context.WithCancel(context.Background())
	bodyReader, bodyWriter := io.Pipe()
	request := &inboundRequest{
		ID:      frame.ID,
		Method:  frame.Method,
		Path:    frame.Path,
		Headers: frame.Headers,
		Body:    bodyWriter,
		Cancel:  cancel,
	}
	a.requests.Store(frame.ID, request)

	go a.executeRequest(ctx, request, bodyReader)
	return nil
}

func (a *App) writeRequestChunk(frame protocol.Frame) error {
	value, ok := a.requests.Load(frame.ID)
	if !ok {
		return nil
	}
	request := value.(*inboundRequest)
	_, err := request.Body.Write(frame.Chunk)
	return err
}

func (a *App) finishRequest(frame protocol.Frame, finishErr error) error {
	value, ok := a.requests.Load(frame.ID)
	if !ok {
		return nil
	}
	request := value.(*inboundRequest)
	if finishErr != nil {
		request.Cancel()
		_ = request.Body.CloseWithError(finishErr)
	} else {
		_ = request.Body.Close()
	}
	return nil
}

func (a *App) executeRequest(ctx context.Context, request *inboundRequest, bodyReader *io.PipeReader) {
	defer a.requests.Delete(request.ID)
	defer request.Cancel()
	defer bodyReader.Close()
	defer a.verbose("REQUEST_END %s %s (id=%s)", request.Method, request.Path, request.ID[:8])
	defer a.conn.RemoveStream(request.ID)

	startTime := time.Now()

	localURL, err := protocol.BuildDestinationURL(a.config.DestinationServer, request.Path)
	if err != nil {
		a.sendGatewayError(request.ID, err)
		return
	}

	a.verbose("REQUEST_FORWARD %s %s → %s", request.Method, request.Path, localURL.String())

	httpRequest, err := http.NewRequestWithContext(ctx, request.Method, localURL.String(), bodyReader)
	if err != nil {
		a.sendGatewayError(request.ID, err)
		return
	}
	protocol.ApplyHeaders(httpRequest.Header, request.Headers)

	response, err := a.httpClient.Do(httpRequest)
	if err != nil {
		a.verbose("REQUEST_ERR %s %s: %v", request.Method, request.Path, err)
		a.sendGatewayError(request.ID, err)
		return
	}
	defer response.Body.Close()

	a.verbose("RESPONSE_START %s %s → %d (%s)", request.Method, request.Path, response.StatusCode, time.Since(startTime).Round(time.Millisecond))

	if err := a.conn.Send(protocol.Frame{
		Type:      protocol.FrameTypeResponseStart,
		ID:        request.ID,
		Status:    response.StatusCode,
		Headers:   protocol.CloneHeaders(response.Header),
		Timestamp: time.Now().UTC(),
	}); err != nil {
		return
	}

	var totalBytes int64
	buffer := make([]byte, protocol.DefaultChunkSize)
	for {
		readBytes, readErr := response.Body.Read(buffer)
		if readBytes > 0 {
			totalBytes += int64(readBytes)
			chunk := make([]byte, readBytes)
			copy(chunk, buffer[:readBytes])
			if err := a.conn.Send(protocol.Frame{
				Type:      protocol.FrameTypeResponseBody,
				ID:        request.ID,
				Chunk:     chunk,
				Timestamp: time.Now().UTC(),
			}); err != nil {
				return
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			log.Printf("response stream error for %s: %v", request.ID, readErr)
			break
		}
	}

	a.verbose("RESPONSE_DONE %s %s → %d (%s, %s)", request.Method, request.Path, response.StatusCode, time.Since(startTime).Round(time.Millisecond), formatBytes(totalBytes))

	_ = a.conn.Send(protocol.Frame{
		Type:      protocol.FrameTypeResponseEnd,
		ID:        request.ID,
		Timestamp: time.Now().UTC(),
	})
}

func (a *App) sendGatewayError(requestID string, err error) {
	_ = a.conn.Send(protocol.Frame{
		Type:      protocol.FrameTypeResponseStart,
		ID:        requestID,
		Status:    http.StatusBadGateway,
		Headers:   map[string][]string{"Content-Type": {"text/plain; charset=utf-8"}},
		Timestamp: time.Now().UTC(),
	})
	_ = a.conn.Send(protocol.Frame{
		Type:      protocol.FrameTypeResponseBody,
		ID:        requestID,
		Chunk:     []byte(err.Error()),
		Timestamp: time.Now().UTC(),
	})
	_ = a.conn.Send(protocol.Frame{
		Type:      protocol.FrameTypeResponseEnd,
		ID:        requestID,
		Timestamp: time.Now().UTC(),
	})
}

// WebSocket tunnel state
type wsTunnel struct {
	ID     string
	conn   net.Conn
	mu     sync.Mutex
	closed bool
}

var wsTunnels sync.Map

func (a *App) startWebSocketTunnel(frame protocol.Frame) error {
	a.verbose("WS_UPGRADE %s %s (id=%s)", frame.Method, frame.Path, frame.ID[:8])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Build the destination URL
	localURL, err := protocol.BuildDestinationURL(a.config.DestinationServer, frame.Path)
	if err != nil {
		a.sendWebSocketError(frame.ID, err)
		return nil
	}

	// Dial the destination via raw TCP (for WebSocket upgrade to work properly)
	host := localURL.Host
	if !strings.Contains(host, ":") {
		if localURL.Scheme == "https" || localURL.Scheme == "wss" {
			host = host + ":443"
		} else {
			host = host + ":80"
		}
	}

	dialer := net.Dialer{Timeout: 10 * time.Second}
	destConn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		a.sendWebSocketError(frame.ID, err)
		return nil
	}

	// Build HTTP upgrade request
	upgradeReq := fmt.Sprintf("GET %s HTTP/1.1\r\n", localURL.Path)
	upgradeReq += fmt.Sprintf("Host: %s\r\n", localURL.Host)
	for k, vals := range frame.Headers {
		if strings.EqualFold(k, "Host") {
			continue
		}
		for _, v := range vals {
			upgradeReq += k + ": " + v + "\r\n"
		}
	}
	upgradeReq += "\r\n"

	// Send upgrade request
	if _, err := destConn.Write([]byte(upgradeReq)); err != nil {
		destConn.Close()
		a.sendWebSocketError(frame.ID, err)
		return nil
	}

	a.verbose("WS_UPGRADE_SENT %s → %s", frame.Path, host)

	// Read response status line
	respReader := bufio.NewReader(destConn)
	statusLine, err := respReader.ReadString('\n')
	if err != nil {
		destConn.Close()
		a.sendWebSocketError(frame.ID, err)
		return nil
	}

	// Parse status code
	parts := strings.SplitN(strings.TrimSpace(statusLine), " ", 3)
	statusCode := 502
	if len(parts) >= 2 {
		if code, err := strconv.Atoi(parts[1]); err == nil {
			statusCode = code
		}
	}

	// Read response headers
	respHeaders := make(map[string][]string)
	for {
		line, err := respReader.ReadString('\n')
		if err != nil {
			destConn.Close()
			a.sendWebSocketError(frame.ID, err)
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := http.CanonicalHeaderKey(strings.TrimSpace(parts[0]))
			val := strings.TrimSpace(parts[1])
			respHeaders[key] = append(respHeaders[key], val)
		}
	}

	// Send response start back to server
	if err := a.conn.Send(protocol.Frame{
		Type:      protocol.FrameTypeResponseStart,
		ID:        frame.ID,
		Status:    statusCode,
		Headers:   respHeaders,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		destConn.Close()
		return nil
	}

	if statusCode != http.StatusSwitchingProtocols {
		// Upgrade failed
		a.verbose("WS_UPGRADE_FAILED %s → %d", frame.Path, statusCode)
		body, _ := io.ReadAll(respReader)
		if len(body) > 0 {
			_ = a.conn.Send(protocol.Frame{
				Type:      protocol.FrameTypeResponseBody,
				ID:        frame.ID,
				Chunk:     body,
				Timestamp: time.Now().UTC(),
			})
		}
		_ = a.conn.Send(protocol.Frame{
			Type:      protocol.FrameTypeResponseEnd,
			ID:        frame.ID,
			Timestamp: time.Now().UTC(),
		})
		destConn.Close()
		return nil
	}

	// WebSocket upgrade successful
	a.verbose("WS_UPGRADE_OK %s → 101 Switching Protocols", frame.Path)
	tunnel := &wsTunnel{ID: frame.ID, conn: destConn}
	wsTunnels.Store(frame.ID, tunnel)

	var wg sync.WaitGroup
	wg.Add(2)

	// Destination -> Tunnel server
	var destToServerBytes int64
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := respReader.Read(buf)
			if err != nil {
				a.verbose("WS_DEST_CLOSED %s → sent %s to server", frame.Path, formatBytes(destToServerBytes))
				_ = a.conn.Send(protocol.Frame{
					Type: protocol.FrameTypeWebSocketClose,
					ID:   frame.ID,
				})
				return
			}
			destToServerBytes += int64(n)
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := a.conn.Send(protocol.Frame{
				Type:      protocol.FrameTypeWebSocketData,
				ID:        frame.ID,
				Chunk:     chunk,
				Timestamp: time.Now().UTC(),
			}); err != nil {
				a.verbose("WS_SEND_ERR %s: %v", frame.Path, err)
				return
			}
		}
	}()

	// Tunnel server -> Destination
	go func() {
		defer wg.Done()
		// Data will be sent via writeWebSocketData
		<-a.conn.Closed()
		tunnel.mu.Lock()
		if !tunnel.closed {
			tunnel.conn.Close()
			tunnel.closed = true
		}
		tunnel.mu.Unlock()
	}()

	wg.Wait()
	return nil
}

func (a *App) writeWebSocketData(frame protocol.Frame) error {
	value, ok := wsTunnels.Load(frame.ID)
	if !ok {
		return nil
	}
	tunnel := value.(*wsTunnel)
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	if tunnel.closed {
		return nil
	}
	_, err := tunnel.conn.Write(frame.Chunk)
	return err
}

func (a *App) closeWebSocketTunnel(frame protocol.Frame, err error) error {
	value, ok := wsTunnels.LoadAndDelete(frame.ID)
	if !ok {
		return nil
	}
	tunnel := value.(*wsTunnel)
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	if !tunnel.closed {
		tunnel.closed = true
		tunnel.conn.Close()
	}
	return nil
}

func (a *App) sendWebSocketError(requestID string, err error) {
	_ = a.conn.Send(protocol.Frame{
		Type:      protocol.FrameTypeWebSocketError,
		ID:        requestID,
		Error:     err.Error(),
		Timestamp: time.Now().UTC(),
	})
}

func formatBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	} else if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	} else {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
}

func (a *App) closePendingRequests(err error) {
	a.requests.Range(func(_, value any) bool {
		request := value.(*inboundRequest)
		request.Cancel()
		_ = request.Body.CloseWithError(err)
		return true
	})

	// Close all active WebSocket tunnels
	wsTunnels.Range(func(_, value any) bool {
		tunnel := value.(*wsTunnel)
		tunnel.mu.Lock()
		if !tunnel.closed {
			tunnel.closed = true
			tunnel.conn.Close()
		}
		tunnel.mu.Unlock()
		return true
	})
}


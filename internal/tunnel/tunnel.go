package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// StreamState tracks the state of an HTTP/2 stream
type StreamState int

const (
	StreamStateIdle StreamState = iota
	StreamStateOpen
	StreamStateClosed
)

// RequestHeader is the minimal request info sent over the tunnel
type RequestHeader struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
}

// ResponseHeader is the minimal response info sent back
type ResponseHeader struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
}

// Session represents a tunnel session between client and server
type Session struct {
	mu       sync.Mutex
	framer   *http2.Framer
	hpackEnc *hpack.Encoder
	hpackDec *hpack.Decoder
	conn     net.Conn

	streamID  uint32
	closed    chan struct{}
	closeOnce sync.Once

	// For server side: pending streams waiting for response
	pending   map[uint32]chan *Response
	pendingMu sync.Mutex

	// Header buffer for HPACK
	headerBuf bytes.Buffer
}

// Response holds the full response from a proxied request
type Response struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// NewServerSession creates a server-side session (accepts client connection)
func NewServerSession(conn net.Conn) (*Session, error) {
	s := &Session{
		conn:    conn,
		framer:  http2.NewFramer(conn, conn),
		closed:  make(chan struct{}),
		pending: make(map[uint32]chan *Response),
	}

	s.hpackEnc = hpack.NewEncoder(&s.headerBuf)
	s.hpackDec = hpack.NewDecoder(4096, func(hf hpack.HeaderField) {})

	// Read client preface (SETTINGS)
	frame, err := s.framer.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading client preface: %w", err)
	}
	if _, ok := frame.(*http2.SettingsFrame); !ok {
		return nil, fmt.Errorf("expected SETTINGS frame, got %T", frame)
	}

	// Send server settings
	if err := s.framer.WriteSettings(); err != nil {
		return nil, fmt.Errorf("writing server settings: %w", err)
	}

	// Send initial settings ACK
	s.framer.WriteSettingsAck()

	// Read settings ACK from client
	for {
		frame, err := s.framer.ReadFrame()
		if err != nil {
			return nil, fmt.Errorf("reading settings ack: %w", err)
		}
		if _, ok := frame.(*http2.SettingsFrame); ok {
			// Another settings frame (with ACK)
			continue
		}
		if _, ok := frame.(*http2.GoAwayFrame); ok {
			return nil, fmt.Errorf("client sent GOAWAY")
		}
		// First non-settings frame should be headers (register)
		if hf, ok := frame.(*http2.HeadersFrame); ok {
			return s.handleRegisterFrame(hf)
		}
	}
}

// NewClientSession creates a client-side session (initiates connection)
func NewClientSession(ctx context.Context, conn net.Conn) (*Session, error) {
	s := &Session{
		conn:    conn,
		framer:  http2.NewFramer(conn, conn),
		closed:  make(chan struct{}),
		pending: make(map[uint32]chan *Response),
	}

	s.hpackEnc = hpack.NewEncoder(&s.headerBuf)
	s.hpackDec = hpack.NewDecoder(4096, func(hf hpack.HeaderField) {})

	// Send client preface
	if err := s.framer.WriteSettings(); err != nil {
		return nil, fmt.Errorf("writing settings: %w", err)
	}

	// Read server settings
	frame, err := s.framer.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading server settings: %w", err)
	}
	if _, ok := frame.(*http2.SettingsFrame); !ok {
		return nil, fmt.Errorf("expected SETTINGS, got %T", frame)
	}

	// Send settings ACK
	s.framer.WriteSettingsAck()

	// Register this client with the server
	if err := s.register(ctx); err != nil {
		return nil, fmt.Errorf("registering client: %w", err)
	}

	// Start reader goroutine
	go s.readLoop()

	return s, nil
}

func (s *Session) register(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	streamID := s.nextStreamID()
	s.headerBuf.Reset()

	// Register headers
	s.hpackEnc.WriteField(hpack.HeaderField{Name: ":method", Value: "REGISTER"})
	s.hpackEnc.WriteField(hpack.HeaderField{Name: ":path", Value: "/tunnel"})
	s.hpackEnc.WriteField(hpack.HeaderField{Name: "content-type", Value: "application/json"})

	return s.framer.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      streamID,
		EndHeaders:    true,
		EndStream:     true,
		BlockFragment: s.headerBuf.Bytes(),
	})
}

func (s *Session) handleRegisterFrame(hf *http2.HeadersFrame) (*Session, error) {
	headers := s.decodeHeaders(hf)
	if headers[":method"] == "REGISTER" {
		log.Printf("Client registered via tunnel")
		// Start reader goroutine for this session
		go s.readLoop()
		return s, nil
	}
	return nil, fmt.Errorf("unexpected first frame: %s %s", headers[":method"], headers[":path"])
}

// SendRequest sends an HTTP request through the tunnel and waits for response
func (s *Session) SendRequest(ctx context.Context, reqHeader RequestHeader, body io.Reader) (*Response, error) {
	s.mu.Lock()
	streamID := s.nextStreamID()
	s.headerBuf.Reset()

	// Encode request headers
	s.hpackEnc.WriteField(hpack.HeaderField{Name: ":method", Value: reqHeader.Method})
	s.hpackEnc.WriteField(hpack.HeaderField{Name: ":path", Value: reqHeader.Path})
	s.hpackEnc.WriteField(hpack.HeaderField{Name: "content-type", Value: "application/octet-stream"})
	s.hpackEnc.WriteField(hpack.HeaderField{Name: "x-tunnel-request", Value: "1"})

	// Write optional custom headers
	for k, vals := range reqHeader.Headers {
		for _, v := range vals {
			s.hpackEnc.WriteField(hpack.HeaderField{Name: strings.ToLower(k), Value: v})
		}
	}

	// Create pending channel
	respCh := make(chan *Response, 1)
	s.pendingMu.Lock()
	s.pending[streamID] = respCh
	s.pendingMu.Unlock()
	s.mu.Unlock()

	// Write HEADERS frame
	if err := s.framer.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      streamID,
		EndHeaders:    true,
		EndStream:     body == nil,
		BlockFragment: s.headerBuf.Bytes(),
	}); err != nil {
		return nil, fmt.Errorf("writing headers: %w", err)
	}

	// Write body if present
	if body != nil {
		buf := make([]byte, 32*1024)
		for {
			n, err := body.Read(buf)
			if n > 0 {
				if err := s.framer.WriteData(streamID, err == io.EOF, buf[:n]); err != nil {
					return nil, fmt.Errorf("writing body data: %w", err)
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("reading body: %w", err)
			}
		}
	}

	// Wait for response
	select {
	case resp := <-respCh:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.closed:
		return nil, fmt.Errorf("session closed")
	}
}

// AcceptRequest waits for the next incoming request from the server
func (s *Session) AcceptRequest(ctx context.Context) (uint32, *RequestHeader, io.Reader, error) {
	// This is handled by readLoop and dispatched via callback
	// For simplicity, we use a channel-based approach
	select {
	case <-ctx.Done():
		return 0, nil, nil, ctx.Err()
	case <-s.closed:
		return 0, nil, nil, fmt.Errorf("session closed")
	}
}

// SendResponse sends a response back through the tunnel
func (s *Session) SendResponse(streamID uint32, respHeader ResponseHeader, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.headerBuf.Reset()

	// Encode response headers
	s.hpackEnc.WriteField(hpack.HeaderField{Name: ":status", Value: fmt.Sprintf("%d", respHeader.Status)})
	for k, vals := range respHeader.Headers {
		for _, v := range vals {
			s.hpackEnc.WriteField(hpack.HeaderField{Name: strings.ToLower(k), Value: v})
		}
	}

	endStream := len(body) == 0

	// Write HEADERS
	if err := s.framer.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      streamID,
		EndHeaders:    true,
		EndStream:     endStream,
		BlockFragment: s.headerBuf.Bytes(),
	}); err != nil {
		return fmt.Errorf("writing response headers: %w", err)
	}

	// Write body
	if len(body) > 0 {
		if err := s.framer.WriteData(streamID, true, body); err != nil {
			return fmt.Errorf("writing response body: %w", err)
		}
	}

	return nil
}

// readLoop reads frames from the connection
func (s *Session) readLoop() {
	defer s.Close()

	for {
		frame, err := s.framer.ReadFrame()
		if err != nil {
			if !isClosedError(err) {
				log.Printf("read frame error: %v", err)
			}
			return
		}

		switch f := frame.(type) {
		case *http2.HeadersFrame:
			s.handleHeadersFrame(f)
		case *http2.DataFrame:
			s.handleDataFrame(f)
		case *http2.RSTStreamFrame:
			s.handleRSTStream(f)
		case *http2.GoAwayFrame:
			log.Printf("received GOAWAY: %v", f)
			return
		case *http2.PingFrame:
			s.handlePing(f)
		case *http2.SettingsFrame:
			// Ignore, already handled
		case *http2.WindowUpdateFrame:
			// Ignore, we don't do flow control
		default:
			log.Printf("unhandled frame type: %T", f)
		}
	}
}

func (s *Session) handleHeadersFrame(hf *http2.HeadersFrame) {
	headers := s.decodeHeaders(hf)

	// Check if this is a response (has :status) or request (has :method)
	if status, ok := headers[":status"]; ok {
		// This is a response
		s.handleResponse(hf.StreamID, status, headers, hf.StreamEnded())
	} else if method, ok := headers[":method"]; ok {
		// This is a request — notify via callback
		s.handleRequest(hf.StreamID, method, headers, hf.StreamEnded())
	}
}

func (s *Session) handleResponse(streamID uint32, status string, headers map[string]string, endStream bool) {
	resp := &Response{
		Status:  parseInt(status, 200),
		Headers: make(map[string][]string),
	}
	for k, v := range headers {
		if k != ":status" {
			resp.Headers[k] = append(resp.Headers[k], v)
		}
	}

	if endStream {
		s.deliverResponse(streamID, resp)
	}
}

func (s *Session) handleRequest(streamID uint32, method string, headers map[string]string, endStream bool) {
	req := &RequestHeader{
		Method:  method,
		Path:    headers[":path"],
		Headers: make(map[string][]string),
	}
	for k, v := range headers {
		if k != ":method" && k != ":path" && !strings.HasPrefix(k, ":") {
			req.Headers[k] = append(req.Headers[k], v)
		}
	}

	// TODO: dispatch to request handler
	log.Printf("Received request: %s %s (stream %d)", method, req.Path, streamID)
}

func (s *Session) handleDataFrame(df *http2.DataFrame) {
	// TODO: accumulate body data for pending requests/responses
	if df.StreamEnded() {
		// End of stream
	}
}

func (s *Session) handleRSTStream(rst *http2.RSTStreamFrame) {
	s.pendingMu.Lock()
	if ch, ok := s.pending[rst.StreamID]; ok {
		close(ch)
		delete(s.pending, rst.StreamID)
	}
	s.pendingMu.Unlock()
}

func (s *Session) handlePing(ping *http2.PingFrame) {
	if !ping.IsAck() {
		s.framer.WritePing(true, ping.Data)
	}
}

func (s *Session) deliverResponse(streamID uint32, resp *Response) {
	s.pendingMu.Lock()
	if ch, ok := s.pending[streamID]; ok {
		select {
		case ch <- resp:
		default:
		}
		delete(s.pending, streamID)
	}
	s.pendingMu.Unlock()
}

func (s *Session) decodeHeaders(hf *http2.HeadersFrame) map[string]string {
	headers := make(map[string]string)
	s.headerBuf.Reset()

	// Read header block fragment
	hf.HeaderBlockFragment()
	// We need to feed the fragment to the decoder
	// For now, use a simple approach
	_ = hf

	return headers
}

func (s *Session) nextStreamID() uint32 {
	return atomic.AddUint32(&s.streamID, 2)
}

// Close closes the session
func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closed)
		// Close pending channels
		s.pendingMu.Lock()
		for id, ch := range s.pending {
			close(ch)
			delete(s.pending, id)
		}
		s.pendingMu.Unlock()
		err = s.conn.Close()
	})
	return err
}

// Closed returns a channel that's closed when the session is closed
func (s *Session) Closed() <-chan struct{} {
	return s.closed
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "use of closed network connection") ||
		strings.Contains(err.Error(), "connection reset") ||
		strings.Contains(err.Error(), "broken pipe") ||
		strings.Contains(err.Error(), "EOF")
}

func parseInt(s string, defaultVal int) int {
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return defaultVal
	}
	return v
}

// HTTPHandler creates an http.Handler that tunnels requests through this session
func (s *Session) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Read body
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}

		// Send through tunnel
		resp, err := s.SendRequest(ctx, RequestHeader{
			Method:  r.Method,
			Path:    r.URL.RequestURI(),
			Headers: r.Header,
		}, bytes.NewReader(body))
		if err != nil {
			http.Error(w, fmt.Sprintf("tunnel error: %v", err), http.StatusBadGateway)
			return
		}

		// Write response
		for k, vals := range resp.Headers {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.Status)
		w.Write(resp.Body)
	})
}

// ServeTunnel starts the tunnel server, accepting client connections
func ServeTunnel(ctx context.Context, listener net.Listener, handler func(*Session)) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if isClosedError(err) {
				return nil
			}
			log.Printf("accept error: %v", err)
			continue
		}

		go func() {
			session, err := NewServerSession(conn)
			if err != nil {
				log.Printf("creating server session: %v", err)
				conn.Close()
				return
			}
			handler(session)
		}()
	}
}

// DialTunnel connects to a tunnel server and returns a session
func DialTunnel(ctx context.Context, addr string, tlsCfg *tls.Config) (*Session, error) {
	dialer := tls.Dialer{Config: tlsCfg}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dialing tunnel server: %w", err)
	}

	return NewClientSession(ctx, conn)
}

// Ensure http package is used
var _ = bufio.NewReader
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

// IncomingRequest represents a request received from the tunnel
type IncomingRequest struct {
	StreamID uint32
	Header   RequestHeader
	Body     []byte
	RespCh   chan *Response
}

// Response holds the full response from a proxied request
type Response struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// Session represents a tunnel session between client and server
type Session struct {
	mu       sync.Mutex
	framer   *http2.Framer
	hpackEnc *hpack.Encoder
	hpackDec *hpack.Decoder
	conn     net.Conn

	serverStreamID uint32 // even numbers for server-initiated streams
	clientStreamID uint32 // odd numbers for client-initiated streams (set by server)
	closed         chan struct{}
	closeOnce      sync.Once

	// Incoming responses (server side: received from client via handleResponse)
	incomingResponses   map[uint32]chan *Response
	incomingResponsesMu sync.Mutex

	// Pending requests waiting for response (server side: created by SendRequest)
	pendingRequests   map[uint32]chan *Response
	pendingRequestsMu sync.Mutex

	// Incoming requests (client side: received from server)
	incoming   chan *IncomingRequest
	incomingMu sync.Mutex

	// Header buffer for HPACK
	headerBuf bytes.Buffer

	// Partial data accumulation
	streamData   map[uint32][]byte
	streamDataMu sync.Mutex
	streamEnded  map[uint32]bool
	streamEndMu  sync.Mutex
	// Stored response headers for pending streams
	streamRespStatus  map[uint32]int
	streamRespHeaders map[uint32]map[string][]string
	streamRespMu      sync.Mutex

	// Request handler (set by client side)
	requestHandler func(*IncomingRequest)

	// Temporary storage for HPACK decoding
	decodeHeadersResult map[string]string
}

// NewServerSession creates a server-side session (accepts client connection)
func NewServerSession(conn net.Conn) (*Session, error) {
	s := &Session{
		conn:        conn,
		framer:      http2.NewFramer(conn, conn),
		closed:      make(chan struct{}),
		incomingResponses: make(map[uint32]chan *Response),
		pendingRequests:   make(map[uint32]chan *Response),
		streamData:  make(map[uint32][]byte),
		streamEnded: make(map[uint32]bool),
		streamRespStatus:  make(map[uint32]int),
		streamRespHeaders: make(map[uint32]map[string][]string),
	}

	s.hpackEnc = hpack.NewEncoder(&s.headerBuf)
	s.hpackDec = hpack.NewDecoder(4096, s.onHeaderField)

	// Step 1: Read client preface (SETTINGS)
	frame, err := s.framer.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading client preface: %w", err)
	}
	if _, ok := frame.(*http2.SettingsFrame); !ok {
		return nil, fmt.Errorf("expected SETTINGS frame, got %T", frame)
	}

	// Step 2: Send server SETTINGS (no ACK, just our settings)
	if err := s.framer.WriteSettings(); err != nil {
		return nil, fmt.Errorf("writing server settings: %w", err)
	}

	// Step 3: Read client's SETTINGS ACK
	ackFrame, err := s.framer.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading settings ack: %w", err)
	}
	if sf, ok := ackFrame.(*http2.SettingsFrame); !ok || !sf.IsAck() {
		return nil, fmt.Errorf("expected SETTINGS ACK, got %T", ackFrame)
	}

	// Step 4: Read client's REGISTER headers
	regFrame, err := s.framer.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading register frame: %w", err)
	}
	if hf, ok := regFrame.(*http2.HeadersFrame); ok {
		return s.handleRegisterFrame(hf)
	}
	return nil, fmt.Errorf("expected HEADERS frame for registration, got %T", regFrame)
}

// NewClientSession creates a client-side session (initiates connection)
func NewClientSession(ctx context.Context, conn net.Conn) (*Session, error) {
	s := &Session{
		conn:       conn,
		framer:     http2.NewFramer(conn, conn),
		closed:     make(chan struct{}),
		incomingResponses: make(map[uint32]chan *Response),
		pendingRequests:   make(map[uint32]chan *Response),
		incoming:   make(chan *IncomingRequest, 64),
		streamData:  make(map[uint32][]byte),
		streamEnded: make(map[uint32]bool),
		streamRespStatus:  make(map[uint32]int),
		streamRespHeaders: make(map[uint32]map[string][]string),
	}

	s.hpackEnc = hpack.NewEncoder(&s.headerBuf)
	s.hpackDec = hpack.NewDecoder(4096, s.onHeaderField)

	// Send HTTP/2 client preface (PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n)
	preface := "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
	if _, err := conn.Write([]byte(preface)); err != nil {
		return nil, fmt.Errorf("writing preface: %w", err)
	}

	// Send client SETTINGS
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
	if err := s.framer.WriteSettingsAck(); err != nil {
		return nil, fmt.Errorf("writing settings ack: %w", err)
	}

	// Register this client
	if err := s.register(ctx); err != nil {
		return nil, fmt.Errorf("registering: %w", err)
	}

	// Start reader
	go s.readLoop()

	return s, nil
}

func (s *Session) register(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Client-initiated stream must be odd
	streamID := s.nextClientStreamID()
	s.headerBuf.Reset()
	s.hpackEnc = hpack.NewEncoder(&s.headerBuf)

	s.hpackEnc.WriteField(hpack.HeaderField{Name: ":method", Value: "REGISTER"})
	s.hpackEnc.WriteField(hpack.HeaderField{Name: ":path", Value: "/tunnel"})

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
		log.Printf("Client registered via HTTP/2 tunnel")
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
	s.hpackEnc = hpack.NewEncoder(&s.headerBuf)

	// Encode request headers via HPACK
	s.hpackEnc.WriteField(hpack.HeaderField{Name: ":method", Value: reqHeader.Method})
	s.hpackEnc.WriteField(hpack.HeaderField{Name: ":path", Value: reqHeader.Path})

	// Write custom headers
	for k, vals := range reqHeader.Headers {
		for _, v := range vals {
			s.hpackEnc.WriteField(hpack.HeaderField{Name: strings.ToLower(k), Value: v})
		}
	}

	// Create pending channel
	respCh := make(chan *Response, 1)
	s.pendingRequestsMu.Lock()
	s.pendingRequests[streamID] = respCh
	s.pendingRequestsMu.Unlock()
	s.mu.Unlock()

	// Determine if there's a body
	hasBody := body != nil

	// Write HEADERS frame
	if err := s.framer.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      streamID,
		EndHeaders:    true,
		EndStream:     !hasBody,
		BlockFragment: s.headerBuf.Bytes(),
	}); err != nil {
		s.cleanupPending(streamID)
		return nil, fmt.Errorf("writing headers: %w", err)
	}

	// Write body if present
	if hasBody {
		buf := make([]byte, 32*1024)
		for {
			n, err := body.Read(buf)
			if n > 0 {
				endStream := err == io.EOF
				if writeErr := s.framer.WriteData(streamID, endStream, buf[:n]); writeErr != nil {
					s.cleanupPending(streamID)
					return nil, fmt.Errorf("writing body data: %w", writeErr)
				}
				if endStream {
					break
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				s.cleanupPending(streamID)
				return nil, fmt.Errorf("reading body: %w", err)
			}
		}
	}

	// Wait for response
	select {
	case resp := <-respCh:
		return resp, nil
	case <-ctx.Done():
		s.cleanupPending(streamID)
		return nil, ctx.Err()
	case <-s.closed:
		return nil, fmt.Errorf("session closed")
	}
}

// SetRequestHandler sets the handler for incoming requests (client side)
func (s *Session) SetRequestHandler(handler func(*IncomingRequest)) {
	s.requestHandler = handler
}

// IncomingRequests returns the channel of incoming requests (client side)
func (s *Session) IncomingRequests() <-chan *IncomingRequest {
	return s.incoming
}

// SendResponse sends a response back through the tunnel
func (s *Session) SendResponse(streamID uint32, respHeader ResponseHeader, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.headerBuf.Reset()
	s.hpackEnc = hpack.NewEncoder(&s.headerBuf)

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
			return
		case *http2.PingFrame:
			s.handlePing(f)
		case *http2.SettingsFrame:
		case *http2.WindowUpdateFrame:
		default:
		}
	}
}

// HPACK header field callback
func (s *Session) onHeaderField(f hpack.HeaderField) {
	if s.decodeHeadersResult != nil {
		s.decodeHeadersResult[f.Name] = f.Value
	}
}

func (s *Session) handleHeadersFrame(hf *http2.HeadersFrame) {
	headers := s.decodeHeaders(hf)

	// Check if this is a response (has :status)
	if status, ok := headers[":status"]; ok {
		s.handleResponse(hf.StreamID, status, headers, hf.StreamEnded())
		return
	}

	// Otherwise it's a request (has :method)
	if method, ok := headers[":method"]; ok {
		s.handleRequest(hf.StreamID, method, headers, hf.StreamEnded())
		return
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
	} else {
		// Store response metadata for when data frames arrive
		s.streamRespMu.Lock()
		s.streamRespStatus[streamID] = resp.Status
		s.streamRespHeaders[streamID] = resp.Headers
		s.streamRespMu.Unlock()

		// Create pending channel
		s.incomingResponsesMu.Lock()
		ch := make(chan *Response, 1)
		s.incomingResponses[streamID] = ch
		s.incomingResponsesMu.Unlock()

		// Prepare data accumulation
		s.streamDataMu.Lock()
		s.streamData[streamID] = []byte{}
		s.streamDataMu.Unlock()
		s.streamEndMu.Lock()
		s.streamEnded[streamID] = false
		s.streamEndMu.Unlock()
	}
}

func (s *Session) handleRequest(streamID uint32, method string, headers map[string]string, endStream bool) {
	req := &RequestHeader{
		Method:  method,
		Path:    headers[":path"],
		Headers: make(map[string][]string),
	}
	for k, v := range headers {
		if !strings.HasPrefix(k, ":") {
			req.Headers[k] = append(req.Headers[k], v)
		}
	}

	incoming := &IncomingRequest{
		StreamID: streamID,
		Header:   *req,
		Body:     nil,
		RespCh:   make(chan *Response, 1),
	}

	if endStream {
		// No body, dispatch immediately
		s.dispatchIncoming(incoming)
	} else {
		// Wait for data frames
		s.streamDataMu.Lock()
		s.streamData[streamID] = []byte{}
		s.streamDataMu.Unlock()
		s.streamEndMu.Lock()
		s.streamEnded[streamID] = false
		s.streamEndMu.Unlock()

		// Store for data accumulation
		s.incomingResponsesMu.Lock()
		s.incomingResponses[streamID] = incoming.RespCh
		s.incomingResponsesMu.Unlock()

		// Store incoming request reference
		_ = incoming
	}
}

func (s *Session) handleDataFrame(df *http2.DataFrame) {
	streamID := df.StreamID

	// Accumulate data
	s.streamDataMu.Lock()
	s.streamData[streamID] = append(s.streamData[streamID], df.Data()...)
	s.streamDataMu.Unlock()

	if df.StreamEnded() {
		s.streamEndMu.Lock()
		s.streamEnded[streamID] = true
		s.streamEndMu.Unlock()

		// Check if this is a pending response or incoming request
		s.incomingResponsesMu.Lock()
		ch, isPending := s.incomingResponses[streamID]
		delete(s.incomingResponses, streamID)
		s.incomingResponsesMu.Unlock()

		if isPending && ch != nil {
			// This is a response body — get stored headers
			s.streamRespMu.Lock()
			status := s.streamRespStatus[streamID]
			headers := s.streamRespHeaders[streamID]
			delete(s.streamRespStatus, streamID)
			delete(s.streamRespHeaders, streamID)
			s.streamRespMu.Unlock()

			s.streamDataMu.Lock()
			data := s.streamData[streamID]
			delete(s.streamData, streamID)
			s.streamDataMu.Unlock()

			if headers == nil {
				headers = make(map[string][]string)
			}

			resp := &Response{
				Status:  status,
				Headers: headers,
				Body:    data,
			}

			// Deliver to pendingRequests (waited by SendRequest)
			s.pendingRequestsMu.Lock()
			if reqCh, ok := s.pendingRequests[streamID]; ok {
				select {
				case reqCh <- resp:
				default:
				}
				delete(s.pendingRequests, streamID)
			}
			s.pendingRequestsMu.Unlock()
		} else {
			// This is a request body
			s.streamDataMu.Lock()
			data := s.streamData[streamID]
			delete(s.streamData, streamID)
			s.streamDataMu.Unlock()

			// Build incoming request
			req := &IncomingRequest{
				StreamID: streamID,
				Header:   RequestHeader{},
				Body:     data,
				RespCh:   make(chan *Response, 1),
			}

			s.dispatchIncoming(req)
		}
	}
}

func (s *Session) handleRSTStream(rst *http2.RSTStreamFrame) {
	s.cleanupPending(rst.StreamID)
	s.streamDataMu.Lock()
	delete(s.streamData, rst.StreamID)
	s.streamDataMu.Unlock()
}

func (s *Session) handlePing(ping *http2.PingFrame) {
	if !ping.IsAck() {
		_ = s.framer.WritePing(true, ping.Data)
	}
}

func (s *Session) dispatchIncoming(req *IncomingRequest) {
	if s.requestHandler != nil {
		s.requestHandler(req)
	} else {
		// Fallback: send to channel
		select {
		case s.incoming <- req:
		default:
		}
	}
}

func (s *Session) deliverResponse(streamID uint32, resp *Response) {
	s.pendingRequestsMu.Lock()
	if ch, ok := s.pendingRequests[streamID]; ok {
		select {
		case ch <- resp:
		default:
		}
		delete(s.pendingRequests, streamID)
	}
	s.pendingRequestsMu.Unlock()
}

func (s *Session) cleanupPending(streamID uint32) {
	s.pendingRequestsMu.Lock()
	if ch, ok := s.pendingRequests[streamID]; ok {
		close(ch)
		delete(s.pendingRequests, streamID)
	}
	s.pendingRequestsMu.Unlock()
	
	s.incomingResponsesMu.Lock()
	if ch, ok := s.incomingResponses[streamID]; ok {
		close(ch)
		delete(s.incomingResponses, streamID)
	}
	s.incomingResponsesMu.Unlock()
}

func (s *Session) decodeHeaders(hf *http2.HeadersFrame) map[string]string {
	headers := make(map[string]string)
	s.decodeHeadersResult = headers
	defer func() { s.decodeHeadersResult = nil }()

	fragment := hf.HeaderBlockFragment()
	if _, err := s.hpackDec.Write(fragment); err != nil {
		log.Printf("HPACK decode error: %v", err)
		return headers
	}

	return headers
}

// nextStreamID returns the next server-initiated stream ID (even numbers)
func (s *Session) nextStreamID() uint32 {
	return atomic.AddUint32(&s.serverStreamID, 2)
}

// nextClientStreamID returns the next client-initiated stream ID (odd numbers)
func (s *Session) nextClientStreamID() uint32 {
	return atomic.AddUint32(&s.clientStreamID, 2) + 1
}

// Close closes the session
func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closed)
		// Close pending channels
		s.pendingRequestsMu.Lock()
		for id, ch := range s.pendingRequests {
			close(ch)
			delete(s.pendingRequests, id)
		}
		s.pendingRequestsMu.Unlock()
		s.incomingResponsesMu.Lock()
		for id, ch := range s.incomingResponses {
			close(ch)
			delete(s.incomingResponses, id)
		}
		s.incomingResponsesMu.Unlock()
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
		var bodyReader io.Reader
		if r.Body != nil && r.ContentLength != 0 {
			bodyReader = r.Body
		}

		// Send through tunnel
		resp, err := s.SendRequest(ctx, RequestHeader{
			Method:  r.Method,
			Path:    r.URL.RequestURI(),
			Headers: r.Header,
		}, bodyReader)
		if err != nil {
			log.Printf("tunnel error for %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, fmt.Sprintf("tunnel error: %v", err), http.StatusBadGateway)
			return
		}

		// Write response headers
		for k, vals := range resp.Headers {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.Status)
		if len(resp.Body) > 0 {
			w.Write(resp.Body)
		}
	})
}

// ServeTunnel starts the tunnel server, accepting client connections
func ServeTunnel(ctx context.Context, listener net.Listener, sessionHandler func(*Session)) error {
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
			sessionHandler(session)
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

// Ensure bufio is used (for potential future use)
var _ = bufio.NewReaderSize
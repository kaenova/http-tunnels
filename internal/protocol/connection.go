package protocol

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	TransportWebSocket = "websocket"
	TransportHTTP2     = "http2"
	maxFrameSize       = 64 * 1024 * 1024
)

var ErrConnectionClosed = &connectionError{"protocol connection closed"}

type connectionError struct {
	msg string
}

func (e *connectionError) Error() string { return e.msg }

type frameTransport interface {
	Send(frame *Frame) error
	ReadFrame() (*Frame, error)
	Close() error
	WritePing() error
	Name() string
}

// Connection wraps a frame transport with a shared lifecycle.
type Connection struct {
	transport frameTransport
	closed    chan struct{}
	closeOnce sync.Once
}

// StreamConnectionOptions configures a streamed frame connection.
type StreamConnectionOptions struct {
	Reader io.Reader
	Writer io.Writer
	Name   string
	Close  func() error
	Flush  func() error
}

// NewConnection creates a new protocol connection over a WebSocket.
func NewConnection(conn *websocket.Conn) *Connection {
	return newProtocolConnection(newWebSocketTransport(conn))
}

// NewStreamConnection creates a new protocol connection over a length-prefixed byte stream.
func NewStreamConnection(options StreamConnectionOptions) *Connection {
	name := options.Name
	if name == "" {
		name = "stream"
	}
	return newProtocolConnection(&streamTransport{
		reader:  bufio.NewReader(options.Reader),
		writer:  options.Writer,
		closeFn: options.Close,
		flushFn: options.Flush,
		name:    name,
	})
}

func newProtocolConnection(transport frameTransport) *Connection {
	return &Connection{
		transport: transport,
		closed:    make(chan struct{}),
	}
}

// Send writes a protobuf frame over the underlying transport.
func (c *Connection) Send(frame *Frame) error {
	select {
	case <-c.closed:
		return ErrConnectionClosed
	default:
	}
	return c.transport.Send(frame)
}

// ReadFrame reads a protobuf frame from the underlying transport.
func (c *Connection) ReadFrame() (*Frame, error) {
	return c.transport.ReadFrame()
}

// Close closes the underlying transport.
func (c *Connection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		err = c.transport.Close()
	})
	return err
}

// Closed returns a channel that's closed when the connection is closed.
func (c *Connection) Closed() <-chan struct{} {
	return c.closed
}

// WritePing sends a ping message or logical ping frame.
func (c *Connection) WritePing() error {
	select {
	case <-c.closed:
		return ErrConnectionClosed
	default:
	}
	return c.transport.WritePing()
}

// TransportName returns the connection transport label used for logging and analytics.
func (c *Connection) TransportName() string {
	if c == nil || c.transport == nil {
		return ""
	}
	return c.transport.Name()
}

func WriteDelimitedFrame(w io.Writer, frame *Frame) error {
	if w == nil {
		return io.ErrClosedPipe
	}
	data, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	if len(data) > maxFrameSize {
		return fmt.Errorf("protocol frame too large: %d bytes", len(data))
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(data)))
	if _, err := w.Write(prefix[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func ReadDelimitedFrame(r io.Reader) (*Frame, error) {
	if r == nil {
		return nil, io.ErrClosedPipe
	}
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(prefix[:])
	if size == 0 {
		return nil, fmt.Errorf("protocol frame length cannot be zero")
	}
	if size > maxFrameSize {
		return nil, fmt.Errorf("protocol frame too large: %d bytes", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	var frame Frame
	if err := proto.Unmarshal(payload, &frame); err != nil {
		return nil, err
	}
	return &frame, nil
}

type webSocketTransport struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func newWebSocketTransport(conn *websocket.Conn) *webSocketTransport {
	transport := &webSocketTransport{conn: conn}
	_ = conn.SetReadDeadline(time.Now().Add(DefaultPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(DefaultPongWait))
	})
	return transport
}

func (t *webSocketTransport) Send(frame *Frame) error {
	data, err := proto.Marshal(frame)
	if err != nil {
		return err
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if err := t.conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	return t.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (t *webSocketTransport) ReadFrame() (*Frame, error) {
	_, data, err := t.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	_ = t.conn.SetReadDeadline(time.Now().Add(DefaultPongWait))
	var frame Frame
	if err := proto.Unmarshal(data, &frame); err != nil {
		return nil, err
	}
	return &frame, nil
}

func (t *webSocketTransport) Close() error {
	return t.conn.Close()
}

func (t *webSocketTransport) WritePing() error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
}

func (t *webSocketTransport) Name() string {
	return TransportWebSocket
}

type streamTransport struct {
	reader  *bufio.Reader
	writer  io.Writer
	closeFn func() error
	flushFn func() error
	name    string
	writeMu sync.Mutex
}

func (t *streamTransport) Send(frame *Frame) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if err := WriteDelimitedFrame(t.writer, frame); err != nil {
		return err
	}
	if t.flushFn != nil {
		return t.flushFn()
	}
	return nil
}

func (t *streamTransport) ReadFrame() (*Frame, error) {
	return ReadDelimitedFrame(t.reader)
}

func (t *streamTransport) Close() error {
	if t.closeFn != nil {
		return t.closeFn()
	}
	return nil
}

func (t *streamTransport) WritePing() error {
	return t.Send(&Frame{Type: FrameType_PING})
}

func (t *streamTransport) Name() string {
	return t.name
}

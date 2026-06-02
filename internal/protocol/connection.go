package protocol

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

var ErrConnectionClosed = &connectionError{"protocol connection closed"}

type connectionError struct {
	msg string
}

func (e *connectionError) Error() string { return e.msg }

// Connection wraps a WebSocket connection with binary frame read/write
type Connection struct {
	conn      *websocket.Conn
	closed    chan struct{}
	closeOnce sync.Once
	writeMu   sync.Mutex // protects writes
}

// NewConnection creates a new protocol connection over a WebSocket
func NewConnection(conn *websocket.Conn) *Connection {
	c := &Connection{
		conn:   conn,
		closed: make(chan struct{}),
	}

	// Setup ping/pong handlers
	_ = conn.SetReadDeadline(time.Now().Add(DefaultPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(DefaultPongWait))
	})

	return c
}

// Send writes a protobuf frame as a binary WebSocket message
func (c *Connection) Send(frame *Frame) error {
	select {
	case <-c.closed:
		return ErrConnectionClosed
	default:
	}

	data, err := proto.Marshal(frame)
	if err != nil {
		return err
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

// ReadFrame reads a protobuf frame from the WebSocket
func (c *Connection) ReadFrame() (*Frame, error) {
	_, data, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(DefaultPongWait))

	var frame Frame
	if err := proto.Unmarshal(data, &frame); err != nil {
		return nil, err
	}
	return &frame, nil
}

// Close closes the WebSocket connection
func (c *Connection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		err = c.conn.Close()
	})
	return err
}

// Closed returns a channel that's closed when the connection is closed
func (c *Connection) Closed() <-chan struct{} {
	return c.closed
}

// WritePing sends a ping message
func (c *Connection) WritePing() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
}

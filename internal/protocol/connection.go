package protocol

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var ErrConnectionClosed = errors.New("protocol connection closed")

const (
	controlBufSize = 256
	dataBufSize    = 4096
)

type Connection struct {
	conn      *websocket.Conn
	control   chan Frame
	data      chan Frame
	closed    chan struct{}
	closeOnce sync.Once
}

func NewConnection(conn *websocket.Conn) *Connection {
	c := &Connection{
		conn:    conn,
		control: make(chan Frame, controlBufSize),
		data:    make(chan Frame, dataBufSize),
		closed:  make(chan struct{}),
	}

	_ = conn.SetReadDeadline(time.Now().Add(DefaultPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(DefaultPongWait))
	})

	go c.writeLoop()

	return c
}

func (c *Connection) Send(frame Frame) error {
	select {
	case <-c.closed:
		return ErrConnectionClosed
	default:
	}

	switch frame.Type {
	// Data frames go to data channel (best-effort, larger buffer)
	case FrameTypeResponseBody, FrameTypeRequestBody, FrameTypeWebSocketData:
		select {
		case c.data <- frame:
			return nil
		default:
			// Data channel full — drop frame (non-blocking)
			return nil
		}
	// Control frames go to control channel (prioritized)
	default:
		select {
		case c.control <- frame:
			return nil
		case <-c.closed:
			return ErrConnectionClosed
		}
	}
}

func (c *Connection) ReadLoop(handler func(Frame) error) error {
	defer c.Close()

	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			return err
		}
		if messageType != websocket.TextMessage {
			continue
		}

		var frame Frame
		if err := json.Unmarshal(payload, &frame); err != nil {
			continue
		}

		if err := handler(frame); err != nil {
			return err
		}
	}
}

func (c *Connection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		err = c.conn.Close()
	})
	return err
}

func (c *Connection) Closed() <-chan struct{} {
	return c.closed
}

func (c *Connection) writeLoop() {
	ticker := time.NewTicker(DefaultPingPeriod)
	defer ticker.Stop()

	for {
		select {
		case frame := <-c.control:
			if err := c.writeJSON(frame); err != nil {
				_ = c.Close()
				return
			}
			// After a control frame, drain available data frames
			// (non-blocking, up to 4 per cycle)
			c.drainData(4)
		case frame := <-c.data:
			if err := c.writeJSON(frame); err != nil {
				_ = c.Close()
				return
			}
		case <-ticker.C:
			if err := c.writeControl(websocket.PingMessage, nil); err != nil {
				_ = c.Close()
				return
			}
		case <-c.closed:
			return
		}
	}
}

func (c *Connection) drainData(max int) {
	for i := 0; i < max; i++ {
		select {
		case frame := <-c.data:
			if err := c.writeJSON(frame); err != nil {
				_ = c.Close()
				return
			}
		default:
			return
		}
	}
}

func (c *Connection) writeJSON(frame Frame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}

	if err := c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, payload)
}

func (c *Connection) writeControl(messageType int, payload []byte) error {
	return c.conn.WriteControl(messageType, payload, time.Now().Add(10*time.Second))
}
package protocol

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var ErrConnectionClosed = errors.New("protocol connection closed")

type Connection struct {
	conn      *websocket.Conn
	control   chan Frame
	closed    chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	streams   map[string]chan Frame
}

func NewConnection(conn *websocket.Conn) *Connection {
	c := &Connection{
		conn:    conn,
		control: make(chan Frame, 256),
		closed:  make(chan struct{}),
		streams: make(map[string]chan Frame),
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

	// Data frames go to per-stream buffer, control frames go to control channel
	switch frame.Type {
	case FrameTypeResponseBody, FrameTypeRequestBody, FrameTypeWebSocketData:
		return c.sendToStream(frame)
	default:
		return c.sendControl(frame)
	}
}

func (c *Connection) sendControl(frame Frame) error {
	select {
	case c.control <- frame:
		return nil
	case <-c.closed:
		return ErrConnectionClosed
	}
}

func (c *Connection) sendToStream(frame Frame) error {
	c.mu.Lock()
	buf, ok := c.streams[frame.ID]
	if !ok {
		buf = make(chan Frame, 64)
		c.streams[frame.ID] = buf
	}
	c.mu.Unlock()

	select {
	case buf <- frame:
		return nil
	default:
		// Buffer full — non-blocking, try to send anyway
		select {
		case buf <- frame:
			return nil
		case <-c.closed:
			return ErrConnectionClosed
		}
	}
}

func (c *Connection) RemoveStream(streamID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if buf, ok := c.streams[streamID]; ok {
		close(buf)
		delete(c.streams, streamID)
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

	// Collect stream IDs for round-robin polling
	streamIDs := make([]string, 0, 64)

	for {
		select {
		case frame := <-c.control:
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

		// Non-blocking: drain available data frames from streams
		// between control frames
		c.mu.Lock()
		streamIDs = streamIDs[:0]
		for id := range c.streams {
			streamIDs = append(streamIDs, id)
		}
		c.mu.Unlock()

		for _, id := range streamIDs {
			c.mu.Lock()
			buf, ok := c.streams[id]
			c.mu.Unlock()
			if !ok {
				continue
			}

			// Drain up to 4 frames from this stream
			for i := 0; i < 4; i++ {
				select {
				case frame, ok := <-buf:
					if !ok {
						// Stream removed
						goto nextStream
					}
					if err := c.writeJSON(frame); err != nil {
						_ = c.Close()
						return
					}
				default:
					goto nextStream
				}
			}
		nextStream:
		}
	}
}

func (c *Connection) writeJSON(frame Frame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}

	if err := c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, payload)
}

func (c *Connection) writeControl(messageType int, payload []byte) error {
	return c.conn.WriteControl(messageType, payload, time.Now().Add(10*time.Second))
}
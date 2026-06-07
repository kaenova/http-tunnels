package server

import (
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/kaenova/http-tunnels/internal/protocol"
)

// WSBridge bridges a user WebSocket connection and a tunnel session.
type WSBridge struct {
	RequestID string
	Session   *TunnelSession
	UserConn  *websocket.Conn
	done      chan struct{}
	closeOnce sync.Once
}

// NewWSBridge creates a new WebSocket bridge.
func NewWSBridge(requestID string, session *TunnelSession, userConn *websocket.Conn) *WSBridge {
	return &WSBridge{
		RequestID: requestID,
		Session:   session,
		UserConn:  userConn,
		done:      make(chan struct{}),
	}
}

// Done returns a channel that closes when the bridge is closed.
func (b *WSBridge) Done() <-chan struct{} {
	return b.done
}

// Close closes the bridge and the user connection.
func (b *WSBridge) Close() {
	b.closeOnce.Do(func() {
		close(b.done)
		_ = b.UserConn.Close()
	})
}

// WSBridgeStore holds active WebSocket bridges keyed by request ID.
type WSBridgeStore struct {
	mu      sync.RWMutex
	bridges map[string]*WSBridge
}

// NewWSBridgeStore creates a new bridge store.
func NewWSBridgeStore() *WSBridgeStore {
	return &WSBridgeStore{
		bridges: make(map[string]*WSBridge),
	}
}

// Set registers a bridge.
func (s *WSBridgeStore) Set(requestID string, bridge *WSBridge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bridges[requestID] = bridge
}

// Get retrieves a bridge by request ID.
func (s *WSBridgeStore) Get(requestID string) (*WSBridge, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.bridges[requestID]
	return b, ok
}

// Delete removes a bridge and closes it.
func (s *WSBridgeStore) Delete(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.bridges[requestID]; ok {
		b.Close()
		delete(s.bridges, requestID)
	}
}

// forwardUserToTunnel reads from the user WebSocket and sends frames to the tunnel.
func (b *WSBridge) forwardUserToTunnel() {
	defer b.Close()
	for {
		msgType, data, err := b.UserConn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				log.Printf("user ws unexpected close: request_id=%s err=%v", b.RequestID, err)
			}
			// Notify tunnel that user closed
			_ = b.Session.Enqueue(&protocol.Frame{
				Type:        protocol.FrameType_WEBSOCKET_CLOSE,
				RequestId:   b.RequestID,
				WsCloseCode: int32(websocket.CloseNormalClosure),
			})
			return
		}

		var ft protocol.FrameType
		var closeCode int32
		var closeText string
		sendChunk := data

		switch msgType {
		case websocket.TextMessage:
			ft = protocol.FrameType_WEBSOCKET_TEXT
		case websocket.BinaryMessage:
			ft = protocol.FrameType_WEBSOCKET_BINARY
		case websocket.CloseMessage:
			ft = protocol.FrameType_WEBSOCKET_CLOSE
			if len(data) >= 2 {
				closeCode = int32(data[0])<<8 | int32(data[1])
				if len(data) > 2 {
					closeText = string(data[2:])
				}
			} else {
				closeCode = int32(websocket.CloseNoStatusReceived)
			}
		case websocket.PingMessage:
			ft = protocol.FrameType_WEBSOCKET_PING
		case websocket.PongMessage:
			ft = protocol.FrameType_WEBSOCKET_PONG
		default:
			continue
		}

		frame := &protocol.Frame{
			Type:      ft,
			RequestId: b.RequestID,
			Chunk:     sendChunk,
		}
		if ft == protocol.FrameType_WEBSOCKET_CLOSE {
			frame.WsCloseCode = closeCode
			frame.WsCloseText = closeText
		}

		if err := b.Session.Enqueue(frame); err != nil {
			log.Printf("ws bridge enqueue failed: request_id=%s err=%v", b.RequestID, err)
			return
		}

		if ft == protocol.FrameType_WEBSOCKET_CLOSE {
			return
		}
	}
}

// ForwardTunnelToUser sends a tunnel frame to the user WebSocket.
func (b *WSBridge) ForwardTunnelToUser(frame *protocol.Frame) {
	if b == nil || b.UserConn == nil {
		return
	}

	var msgType int
	var data []byte

	switch frame.GetType() {
	case protocol.FrameType_WEBSOCKET_TEXT:
		msgType = websocket.TextMessage
		data = frame.GetChunk()
	case protocol.FrameType_WEBSOCKET_BINARY:
		msgType = websocket.BinaryMessage
		data = frame.GetChunk()
	case protocol.FrameType_WEBSOCKET_CLOSE:
		msgType = websocket.CloseMessage
		code := int(frame.GetWsCloseCode())
		if code == 0 {
			code = websocket.CloseNormalClosure
		}
		text := frame.GetWsCloseText()
		if text == "" {
			text = frame.GetError()
		}
		data = websocket.FormatCloseMessage(code, text)
		_ = b.UserConn.WriteMessage(msgType, data)
		b.Close()
		return
	case protocol.FrameType_WEBSOCKET_PING:
		msgType = websocket.PingMessage
		data = frame.GetChunk()
	case protocol.FrameType_WEBSOCKET_PONG:
		msgType = websocket.PongMessage
		data = frame.GetChunk()
	default:
		return
	}

	if err := b.UserConn.WriteMessage(msgType, data); err != nil {
		log.Printf("ws bridge write to user failed: request_id=%s err=%v", b.RequestID, err)
		b.Close()
	}
}

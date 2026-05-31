package protocol

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

var testUpgrader = websocket.Upgrader{
	EnableCompression: true,
	CheckOrigin:       func(r *http.Request) bool { return true },
}

func TestConnectionSendReceive(t *testing.T) {
	// Start test WS server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		c := NewConnection(conn)

		// Read frame from client
		frame, err := c.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}

		// Send response
		resp := &Frame{
			Type:      FrameType_RESPONSE_START,
			RequestId: frame.GetRequestId(),
			Status:    200,
		}
		if err := c.Send(resp); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	// Connect client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dialer := websocket.Dialer{EnableCompression: true}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	c := NewConnection(conn)

	// Send request
	req := &Frame{
		Type:      FrameType_REQUEST,
		RequestId: "test_001",
		Method:    "GET",
		Path:      "/api/test",
	}
	if err := c.Send(req); err != nil {
		t.Fatal(err)
	}

	// Read response
	resp, err := c.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetType() != FrameType_RESPONSE_START {
		t.Errorf("expected RESPONSE_START, got %v", resp.GetType())
	}
	if resp.GetRequestId() != "test_001" {
		t.Errorf("request_id mismatch: got %q", resp.GetRequestId())
	}
	if resp.GetStatus() != 200 {
		t.Errorf("status mismatch: got %d", resp.GetStatus())
	}
}

func TestConnectionBinaryFrame(t *testing.T) {
	// Verify frames are sent as BinaryMessage, not TextMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		msgType, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if msgType != websocket.BinaryMessage {
			t.Errorf("expected BinaryMessage (%d), got %d", websocket.BinaryMessage, msgType)
		}

		var frame Frame
		if err := proto.Unmarshal(data, &frame); err != nil {
			t.Fatal(err)
		}

		// Echo back
		if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dialer := websocket.Dialer{EnableCompression: true}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	c := NewConnection(conn)

	frame := &Frame{
		Type:      FrameType_BODY,
		RequestId: "bin_001",
		Chunk:     []byte{0x00, 0x01, 0x02, 0xFF},
	}
	if err := c.Send(frame); err != nil {
		t.Fatal(err)
	}

	resp, err := c.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetChunk()) != 4 {
		t.Errorf("chunk length: got %d, want 4", len(resp.GetChunk()))
	}
}

func TestConnectionClose(t *testing.T) {
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := testUpgrader.Upgrade(w, r, nil)
		c := NewConnection(conn)
		// Read one frame, then close
		c.ReadFrame()
		c.Close()
		close(serverDone)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dialer := websocket.Dialer{EnableCompression: true}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := NewConnection(conn)

	// Send a frame so server reads and then closes
	c.Send(&Frame{Type: FrameType_PING})

	// Wait for server to close
	<-serverDone

	// Close our side too
	c.Close()

	// Send after close should fail
	err = c.Send(&Frame{Type: FrameType_PING})
	if err == nil {
		t.Error("Send after close should fail")
	}
}

func TestConnectionCompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		// Read and echo
		_, data, _ := conn.ReadMessage()
		conn.WriteMessage(websocket.BinaryMessage, data)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dialer := websocket.Dialer{EnableCompression: true}
	conn, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Check that compression was negotiated
	ext := resp.Header.Get("Sec-WebSocket-Extensions")
	if !strings.Contains(ext, "permessage-deflate") {
		t.Errorf("compression not negotiated: got %q", ext)
	}
}
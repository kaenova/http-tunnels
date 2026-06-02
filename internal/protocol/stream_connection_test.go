package protocol

import (
	"net"
	"testing"
)

func TestStreamConnectionSendReceive(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	client := NewStreamConnection(StreamConnectionOptions{
		Reader: left,
		Writer: left,
		Name:   TransportHTTP2,
		Close:  left.Close,
	})
	server := NewStreamConnection(StreamConnectionOptions{
		Reader: right,
		Writer: right,
		Name:   TransportHTTP2,
		Close:  right.Close,
	})
	defer client.Close()
	defer server.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Send(&Frame{
			Type:      FrameType_REQUEST_START,
			RequestId: "req-h2-1",
			Method:    "GET",
			Path:      "/hello",
		})
	}()

	frame, err := server.ReadFrame()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("send frame: %v", err)
	}
	if frame.GetType() != FrameType_REQUEST_START {
		t.Fatalf("expected REQUEST_START, got %v", frame.GetType())
	}
	if frame.GetRequestId() != "req-h2-1" {
		t.Fatalf("expected request id req-h2-1, got %s", frame.GetRequestId())
	}
	if frame.GetPath() != "/hello" {
		t.Fatalf("expected path /hello, got %s", frame.GetPath())
	}
}

package protocol

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestFrameMarshalUnmarshal_AllTypes(t *testing.T) {
	tests := []struct {
		name  string
		frame *Frame
	}{
		{
			name: "REGISTER",
			frame: &Frame{
				Type:      FrameType_REGISTER,
				Domain:    "test.example.com",
				DomainKey: "abc123key",
			},
		},
		{
			name: "REGISTERED",
			frame: &Frame{
				Type:          FrameType_REGISTERED,
				TunnelId:      "tun_abc123",
				Domain:        "test.example.com",
				ServerMessage: "Welcome!",
				Config: &TunnelConfig{
					MaxConcurrent:    500,
					RequestTimeoutMs: 10000,
					BackendTimeoutMs: 30000,
					Reconnect: &ReconnectConfig{
						Enabled:        true,
						InitialDelayMs: 1000,
						MaxDelayMs:     60000,
						Multiplier:     2.0,
						MaxRetries:     0,
						Jitter:         true,
					},
				},
			},
		},
		{
			name: "REQUEST_START with headers",
			frame: &Frame{
				Type:          FrameType_REQUEST_START,
				RequestId:     "req_001",
				Method:        "POST",
				Path:          "/api/users",
				ContentLength: 128,
				Headers: map[string]*StringList{
					"content-type": {Values: []string{"application/json"}},
					"accept":       {Values: []string{"application/json", "text/plain"}},
				},
			},
		},
		{
			name: "REQUEST_BODY chunk",
			frame: &Frame{
				Type:      FrameType_REQUEST_BODY,
				RequestId: "req_001",
				Chunk:     []byte("hello world"),
			},
		},
		{
			name: "REQUEST_END",
			frame: &Frame{
				Type:      FrameType_REQUEST_END,
				RequestId: "req_001",
			},
		},
		{
			name: "REQUEST_CANCEL",
			frame: &Frame{
				Type:      FrameType_REQUEST_CANCEL,
				RequestId: "req_001",
				Error:     "client disconnected",
			},
		},
		{
			name:  "PING",
			frame: &Frame{Type: FrameType_PING},
		},
		{
			name:  "PONG",
			frame: &Frame{Type: FrameType_PONG},
		},
		{
			name: "RESPONSE_START",
			frame: &Frame{
				Type:      FrameType_RESPONSE_START,
				RequestId: "req_001",
				Status:    200,
				ResponseHeaders: map[string]*StringList{
					"content-type": {Values: []string{"application/json"}},
				},
			},
		},
		{
			name: "RESPONSE_BODY",
			frame: &Frame{
				Type:      FrameType_RESPONSE_BODY,
				RequestId: "req_001",
				Chunk:     []byte(`{"status":"ok"}`),
			},
		},
		{
			name: "RESPONSE_END",
			frame: &Frame{
				Type:      FrameType_RESPONSE_END,
				RequestId: "req_001",
			},
		},
		{
			name: "RESPONSE_ERROR",
			frame: &Frame{
				Type:      FrameType_RESPONSE_ERROR,
				RequestId: "req_001",
				Status:    504,
				Error:     "backend timeout",
			},
		},
		{
			name: "binary chunk with null bytes",
			frame: &Frame{
				Type:      FrameType_RESPONSE_BODY,
				RequestId: "req_bin",
				Chunk:     []byte{0x00, 0x01, 0x02, 0x00, 0xFF, 0x00},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := proto.Marshal(tt.frame)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}

			var decoded Frame
			if err := proto.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			if decoded.GetType() != tt.frame.GetType() {
				t.Errorf("type mismatch: got %v, want %v", decoded.GetType(), tt.frame.GetType())
			}
			if decoded.GetRequestId() != tt.frame.GetRequestId() {
				t.Errorf("request_id mismatch: got %q, want %q", decoded.GetRequestId(), tt.frame.GetRequestId())
			}
			if len(decoded.GetChunk()) != len(tt.frame.GetChunk()) {
				t.Errorf("chunk length mismatch: got %d, want %d", len(decoded.GetChunk()), len(tt.frame.GetChunk()))
			}
			for i := range decoded.GetChunk() {
				if decoded.GetChunk()[i] != tt.frame.GetChunk()[i] {
					t.Errorf("chunk byte mismatch at index %d: got %02x, want %02x", i, decoded.GetChunk()[i], tt.frame.GetChunk()[i])
				}
			}
			if len(decoded.GetHeaders()) != len(tt.frame.GetHeaders()) {
				t.Errorf("headers count mismatch: got %d, want %d", len(decoded.GetHeaders()), len(tt.frame.GetHeaders()))
			}
			if tt.frame.GetConfig() != nil {
				if decoded.GetConfig() == nil {
					t.Error("config is nil after round-trip")
				} else if decoded.GetConfig().GetMaxConcurrent() != tt.frame.GetConfig().GetMaxConcurrent() {
					t.Errorf("config max_concurrent mismatch")
				}
			}
		})
	}
}

func TestFrameTypeEnumValues(t *testing.T) {
	if FrameType_REGISTER != 1 {
		t.Errorf("REGISTER enum value changed: got %d, want 1", FrameType_REGISTER)
	}
	if FrameType_REGISTERED != 2 {
		t.Errorf("REGISTERED enum value changed: got %d, want 2", FrameType_REGISTERED)
	}
	if FrameType_REQUEST_START != 3 {
		t.Errorf("REQUEST_START enum value changed: got %d, want 3", FrameType_REQUEST_START)
	}
	if FrameType_REQUEST_BODY != 10 {
		t.Errorf("REQUEST_BODY enum value changed: got %d, want 10", FrameType_REQUEST_BODY)
	}
	if FrameType_RESPONSE_START != 12 {
		t.Errorf("RESPONSE_START enum value changed: got %d, want 12", FrameType_RESPONSE_START)
	}
}

func TestFrameEmptyChunk(t *testing.T) {
	frame := &Frame{
		Type:      FrameType_RESPONSE_BODY,
		RequestId: "req_001",
		Chunk:     []byte{},
	}

	data, err := proto.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal empty chunk: %v", err)
	}

	var decoded Frame
	if err := proto.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal empty chunk: %v", err)
	}

	if len(decoded.GetChunk()) != 0 {
		t.Errorf("empty chunk became %d bytes", len(decoded.GetChunk()))
	}
}

func TestFrameLargeChunk(t *testing.T) {
	largeChunk := make([]byte, 1024*1024)
	for i := range largeChunk {
		largeChunk[i] = byte(i % 256)
	}

	frame := &Frame{
		Type:      FrameType_REQUEST_BODY,
		RequestId: "req_large",
		Chunk:     largeChunk,
	}

	data, err := proto.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal large chunk: %v", err)
	}

	var decoded Frame
	if err := proto.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal large chunk: %v", err)
	}

	if len(decoded.GetChunk()) != len(largeChunk) {
		t.Errorf("large chunk length mismatch: got %d, want %d", len(decoded.GetChunk()), len(largeChunk))
	}
	if decoded.GetChunk()[0] != 0 {
		t.Errorf("first byte mismatch: got %d, want 0", decoded.GetChunk()[0])
	}
	if decoded.GetChunk()[len(largeChunk)-1] != byte((len(largeChunk)-1)%256) {
		t.Errorf("last byte mismatch")
	}
}

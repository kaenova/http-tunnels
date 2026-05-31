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
			name: "REQUEST with headers",
			frame: &Frame{
				Type:          FrameType_REQUEST,
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
			name: "REQUEST_ACK",
			frame: &Frame{
				Type:      FrameType_REQUEST_ACK,
				RequestId: "req_001",
			},
		},
		{
			name: "REQUEST_ERROR",
			frame: &Frame{
				Type:      FrameType_REQUEST_ERROR,
				RequestId: "req_001",
				Status:    502,
				Error:     "backend connection refused",
			},
		},
		{
			name: "PING",
			frame: &Frame{
				Type: FrameType_PING,
			},
		},
		{
			name: "PONG",
			frame: &Frame{
				Type: FrameType_PONG,
			},
		},
		{
			name: "BODY chunk",
			frame: &Frame{
				Type:      FrameType_BODY,
				RequestId: "req_001",
				Chunk:     []byte("hello world"),
			},
		},
		{
			name: "BODY_END",
			frame: &Frame{
				Type:      FrameType_BODY_END,
				RequestId: "req_001",
			},
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
			name: "WS_DATA",
			frame: &Frame{
				Type:      FrameType_WS_DATA,
				RequestId: "req_ws_001",
				Chunk:     []byte{0x01, 0x02, 0x03, 0xFF},
			},
		},
		{
			name: "WS_CLOSE",
			frame: &Frame{
				Type:      FrameType_WS_CLOSE,
				RequestId: "req_ws_001",
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

			// Verify type
			if decoded.GetType() != tt.frame.GetType() {
				t.Errorf("type mismatch: got %v, want %v", decoded.GetType(), tt.frame.GetType())
			}

			// Verify request_id
			if decoded.GetRequestId() != tt.frame.GetRequestId() {
				t.Errorf("request_id mismatch: got %q, want %q", decoded.GetRequestId(), tt.frame.GetRequestId())
			}

			// Verify chunk bytes exactly
			if len(decoded.GetChunk()) != len(tt.frame.GetChunk()) {
				t.Errorf("chunk length mismatch: got %d, want %d", len(decoded.GetChunk()), len(tt.frame.GetChunk()))
			}
			for i := range decoded.GetChunk() {
				if decoded.GetChunk()[i] != tt.frame.GetChunk()[i] {
					t.Errorf("chunk byte mismatch at index %d: got %02x, want %02x", i, decoded.GetChunk()[i], tt.frame.GetChunk()[i])
				}
			}

			// Verify headers round-trip
			if len(decoded.GetHeaders()) != len(tt.frame.GetHeaders()) {
				t.Errorf("headers count mismatch: got %d, want %d", len(decoded.GetHeaders()), len(tt.frame.GetHeaders()))
			}
			for k, v := range tt.frame.GetHeaders() {
				decV, ok := decoded.GetHeaders()[k]
				if !ok {
					t.Errorf("missing header key: %q", k)
					continue
				}
				if len(decV.GetValues()) != len(v.GetValues()) {
					t.Errorf("header %q values count mismatch: got %d, want %d", k, len(decV.GetValues()), len(v.GetValues()))
				}
			}

			// Verify config round-trip
			if tt.frame.GetConfig() != nil {
				if decoded.GetConfig() == nil {
					t.Error("config is nil after round-trip")
				} else {
					if decoded.GetConfig().GetMaxConcurrent() != tt.frame.GetConfig().GetMaxConcurrent() {
						t.Errorf("config max_concurrent mismatch")
					}
					if decoded.GetConfig().GetReconnect() != nil && tt.frame.GetConfig().GetReconnect() != nil {
						if decoded.GetConfig().GetReconnect().GetEnabled() != tt.frame.GetConfig().GetReconnect().GetEnabled() {
							t.Errorf("reconnect enabled mismatch")
						}
					}
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
	if FrameType_REQUEST != 3 {
		t.Errorf("REQUEST enum value changed: got %d, want 3", FrameType_REQUEST)
	}
	if FrameType_BODY != 10 {
		t.Errorf("BODY enum value changed: got %d, want 10", FrameType_BODY)
	}
	if FrameType_RESPONSE_START != 12 {
		t.Errorf("RESPONSE_START enum value changed: got %d, want 12", FrameType_RESPONSE_START)
	}
	if FrameType_WS_DATA != 16 {
		t.Errorf("WS_DATA enum value changed: got %d, want 16", FrameType_WS_DATA)
	}
	if FrameType_WS_CLOSE != 17 {
		t.Errorf("WS_CLOSE enum value changed: got %d, want 17", FrameType_WS_CLOSE)
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
	largeChunk := make([]byte, 1024*1024) // 1MB
	for i := range largeChunk {
		largeChunk[i] = byte(i % 256)
	}

	frame := &Frame{
		Type:      FrameType_BODY,
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
package protocol

import "time"

type FrameType string

const (
	FrameTypeRequestStart  FrameType = "request_start"
	FrameTypeRequestBody   FrameType = "request_body"
	FrameTypeRequestEnd    FrameType = "request_end"
	FrameTypeRequestCancel FrameType = "request_cancel"

	FrameTypeResponseStart FrameType = "response_start"
	FrameTypeResponseBody  FrameType = "response_body"
	FrameTypeResponseEnd   FrameType = "response_end"
	FrameTypeResponseError FrameType = "response_error"
)

const (
	DefaultChunkSize  = 32 * 1024
	DefaultPingPeriod = 20 * time.Second
	DefaultPongWait   = 60 * time.Second
)

type Frame struct {
	Type      FrameType            `json:"type"`
	ID        string               `json:"id,omitempty"`
	Method    string               `json:"method,omitempty"`
	Path      string               `json:"path,omitempty"`
	Status    int                  `json:"status,omitempty"`
	Headers   map[string][]string  `json:"headers,omitempty"`
	Chunk     []byte               `json:"chunk,omitempty"`
	Error     string               `json:"error,omitempty"`
	Timestamp time.Time            `json:"timestamp,omitempty"`
}

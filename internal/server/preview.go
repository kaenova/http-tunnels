package server

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/kaenova/http-tunnels/internal/protocol"
)

const previewLimit = 8 * 1024

type BodyCapture struct {
	contentType string
	totalBytes  int64
	buffer      bytes.Buffer
	canPreview  bool
}

func NewBodyCapture(contentType string) *BodyCapture {
	return &BodyCapture{
		contentType: contentType,
		canPreview:  protocol.IsTextContentType(contentType),
	}
}

func (c *BodyCapture) Observe(chunk []byte) {
	c.totalBytes += int64(len(chunk))
	if !c.canPreview || len(chunk) == 0 {
		return
	}
	remaining := previewLimit - c.buffer.Len()
	if remaining <= 0 {
		return
	}
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
	}
	_, _ = c.buffer.Write(chunk)
}

func (c *BodyCapture) Preview() string {
	if !c.canPreview {
		return ""
	}
	return c.buffer.String()
}

func contentTypeFromHeaders(headers map[string][]string) string {
	if headers == nil {
		return ""
	}
	return http.Header(headers).Get("Content-Type")
}

func shouldFlushStreamingResponse(contentType string) bool {
	return strings.EqualFold(strings.TrimSpace(contentType), "text/event-stream")
}

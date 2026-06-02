package protocol

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

const (
	H2MessageRequestStart  byte = 1
	H2MessageRequestBody   byte = 2
	H2MessageRequestEnd    byte = 3
	H2MessageRequestCancel byte = 4

	H2MessageResponseStart byte = 11
	H2MessageResponseBody  byte = 12
	H2MessageResponseEnd   byte = 13
	H2MessageResponseError byte = 14

	maxH2MessageSize = 64 * 1024 * 1024
)

type H2RequestStart struct {
	Method        string              `json:"method"`
	Path          string              `json:"path"`
	Headers       map[string][]string `json:"headers,omitempty"`
	ContentLength int64               `json:"contentLength,omitempty"`
}

type H2ResponseStart struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
}

type H2ResponseError struct {
	Status int    `json:"status"`
	Error  string `json:"error"`
}

type H2TunnelStreamOptions struct {
	Reader io.Reader
	Writer io.Writer
	Flush  func() error
	Close  func() error
}

type H2TunnelStream struct {
	reader    *bufio.Reader
	writer    io.Writer
	flushFn   func() error
	closeFn   func() error
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func NewH2TunnelStream(options H2TunnelStreamOptions) *H2TunnelStream {
	return &H2TunnelStream{
		reader:  bufio.NewReader(options.Reader),
		writer:  options.Writer,
		flushFn: options.Flush,
		closeFn: options.Close,
	}
}

func (s *H2TunnelStream) ReadMessage() (byte, []byte, error) {
	if s == nil || s.reader == nil {
		return 0, nil, io.ErrClosedPipe
	}
	var header [5]byte
	if _, err := io.ReadFull(s.reader, header[:]); err != nil {
		return 0, nil, err
	}
	messageType := header[0]
	size := binary.BigEndian.Uint32(header[1:])
	if size > maxH2MessageSize {
		return 0, nil, fmt.Errorf("h2 tunnel message too large: %d", size)
	}
	payload := make([]byte, int(size))
	if size > 0 {
		if _, err := io.ReadFull(s.reader, payload); err != nil {
			return 0, nil, err
		}
	}
	return messageType, payload, nil
}

func (s *H2TunnelStream) WriteMessage(messageType byte, payload []byte) error {
	if s == nil || s.writer == nil {
		return io.ErrClosedPipe
	}
	if len(payload) > maxH2MessageSize {
		return fmt.Errorf("h2 tunnel message too large: %d", len(payload))
	}
	var header [5]byte
	header[0] = messageType
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writer.Write(header[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := s.writer.Write(payload); err != nil {
			return err
		}
	}
	if s.flushFn != nil {
		return s.flushFn()
	}
	return nil
}

func (s *H2TunnelStream) ReadRequestStart() (H2RequestStart, error) {
	messageType, payload, err := s.ReadMessage()
	if err != nil {
		return H2RequestStart{}, err
	}
	if messageType != H2MessageRequestStart {
		return H2RequestStart{}, fmt.Errorf("unexpected h2 tunnel message: %s", h2MessageName(messageType))
	}
	var start H2RequestStart
	if err := json.Unmarshal(payload, &start); err != nil {
		return H2RequestStart{}, err
	}
	return start, nil
}

func (s *H2TunnelStream) WriteRequestStart(start H2RequestStart) error {
	payload, err := json.Marshal(start)
	if err != nil {
		return err
	}
	return s.WriteMessage(H2MessageRequestStart, payload)
}

func (s *H2TunnelStream) WriteRequestBody(chunk []byte) error {
	return s.WriteMessage(H2MessageRequestBody, chunk)
}

func (s *H2TunnelStream) WriteRequestEnd() error {
	return s.WriteMessage(H2MessageRequestEnd, nil)
}

func (s *H2TunnelStream) WriteRequestCancel(message string) error {
	return s.WriteMessage(H2MessageRequestCancel, []byte(message))
}

func (s *H2TunnelStream) WriteResponseStart(start H2ResponseStart) error {
	payload, err := json.Marshal(start)
	if err != nil {
		return err
	}
	return s.WriteMessage(H2MessageResponseStart, payload)
}

func (s *H2TunnelStream) WriteResponseBody(chunk []byte) error {
	return s.WriteMessage(H2MessageResponseBody, chunk)
}

func (s *H2TunnelStream) WriteResponseEnd() error {
	return s.WriteMessage(H2MessageResponseEnd, nil)
}

func (s *H2TunnelStream) WriteResponseError(status int, message string) error {
	payload, err := json.Marshal(H2ResponseError{Status: status, Error: message})
	if err != nil {
		return err
	}
	return s.WriteMessage(H2MessageResponseError, payload)
}

func (s *H2TunnelStream) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.closeFn != nil {
			err = s.closeFn()
		}
	})
	return err
}

func h2MessageName(messageType byte) string {
	switch messageType {
	case H2MessageRequestStart:
		return "request_start"
	case H2MessageRequestBody:
		return "request_body"
	case H2MessageRequestEnd:
		return "request_end"
	case H2MessageRequestCancel:
		return "request_cancel"
	case H2MessageResponseStart:
		return "response_start"
	case H2MessageResponseBody:
		return "response_body"
	case H2MessageResponseEnd:
		return "response_end"
	case H2MessageResponseError:
		return "response_error"
	default:
		return fmt.Sprintf("unknown(%d)", messageType)
	}
}

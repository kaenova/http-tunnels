package server

import (
	"fmt"
	"sync"

	"github.com/kaenova/http-tunnels/internal/protocol"
)

type TunnelSession struct {
	TunnelID string
	Domain   string
	Conn     *protocol.Connection
	pending  sync.Map
}

func NewTunnelSession(record TunnelRecord, conn *protocol.Connection) *TunnelSession {
	return &TunnelSession{
		TunnelID: record.ID,
		Domain:   record.Domain,
		Conn:     conn,
	}
}

func (s *TunnelSession) Send(frame protocol.Frame) error {
	return s.Conn.Send(frame)
}

func (s *TunnelSession) RegisterPending(requestID string, stream *responseStream) {
	s.pending.Store(requestID, stream)
}

func (s *TunnelSession) RemovePending(requestID string) {
	s.pending.Delete(requestID)
}

func (s *TunnelSession) HandleFrame(frame protocol.Frame) error {
	value, ok := s.pending.Load(frame.ID)
	if !ok {
		return nil
	}
	stream := value.(*responseStream)

	switch frame.Type {
	case protocol.FrameTypeResponseStart:
		stream.start(frame)
	case protocol.FrameTypeResponseBody:
		stream.write(frame.Chunk)
	case protocol.FrameTypeResponseEnd:
		stream.finish()
		s.pending.Delete(frame.ID)
	case protocol.FrameTypeResponseError:
		stream.fail(fmt.Errorf("%s", frame.Error))
		s.pending.Delete(frame.ID)
	}
	return nil
}

func (s *TunnelSession) FailAll(err error) {
	s.pending.Range(func(key, value any) bool {
		stream := value.(*responseStream)
		stream.fail(err)
		s.pending.Delete(key)
		return true
	})
}

type responseStream struct {
	startOnce sync.Once
	closeOnce sync.Once
	startCh   chan protocol.Frame
	bodyCh    chan []byte
	errCh     chan error
	doneCh    chan struct{}
}

func newResponseStream() *responseStream {
	return &responseStream{
		startCh: make(chan protocol.Frame, 1),
		bodyCh:  make(chan []byte, 16),
		errCh:   make(chan error, 1),
		doneCh:  make(chan struct{}),
	}
}

func (s *responseStream) start(frame protocol.Frame) {
	s.startOnce.Do(func() {
		s.startCh <- frame
	})
}

func (s *responseStream) write(chunk []byte) {
	select {
	case <-s.doneCh:
		return
	default:
	}
	copied := make([]byte, len(chunk))
	copy(copied, chunk)
	select {
	case s.bodyCh <- copied:
	case <-s.doneCh:
	}
}

func (s *responseStream) finish() {
	s.closeOnce.Do(func() {
		close(s.bodyCh)
		close(s.doneCh)
	})
}

func (s *responseStream) fail(err error) {
	select {
	case s.errCh <- err:
	default:
	}
	s.finish()
}

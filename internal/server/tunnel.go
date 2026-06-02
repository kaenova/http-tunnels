package server

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kaenova/http-tunnels/internal/protocol"
)

const defaultHTTP2WorkerQueueSize = 16

// TunnelSessionStore manages active tunnel sessions.
type TunnelSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*TunnelSession // domain -> session
}

func NewTunnelSessionStore() *TunnelSessionStore {
	return &TunnelSessionStore{sessions: make(map[string]*TunnelSession)}
}

func (s *TunnelSessionStore) Get(domain string) (*TunnelSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[domain]
	return sess, ok
}

func (s *TunnelSessionStore) Set(domain string, session *TunnelSession) *TunnelSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.sessions[domain]
	s.sessions[domain] = session
	return prev
}

func (s *TunnelSessionStore) GetOrCreateHTTP2(domain string, create func() *TunnelSession) (*TunnelSession, bool, *TunnelSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.sessions[domain]; ok {
		if existing != nil && existing.Transport == protocol.TransportHTTP2 {
			return existing, false, nil
		}
		session := create()
		s.sessions[domain] = session
		return session, true, existing
	}
	session := create()
	s.sessions[domain] = session
	return session, true, nil
}

func (s *TunnelSessionStore) Delete(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, domain)
}

func (s *TunnelSessionStore) GetAll() map[string]*TunnelSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*TunnelSession, len(s.sessions))
	for k, v := range s.sessions {
		result[k] = v
	}
	return result
}

func (s *TunnelSessionStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

func (s *TunnelSessionStore) ActiveRequestCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, session := range s.sessions {
		if session == nil {
			continue
		}
		total += int(atomic.LoadInt32(&session.activeCount))
	}
	return total
}

type HTTP2WorkerStream struct {
	Stream    *protocol.H2TunnelStream
	done      chan struct{}
	closeOnce sync.Once
}

func NewHTTP2WorkerStream(stream *protocol.H2TunnelStream) *HTTP2WorkerStream {
	return &HTTP2WorkerStream{Stream: stream, done: make(chan struct{})}
}

func (s *HTTP2WorkerStream) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		if s.Stream != nil {
			err = s.Stream.Close()
		}
	})
	return err
}

func (s *HTTP2WorkerStream) Done() <-chan struct{} { return s.done }

// TunnelSession represents an active tunnel connection/session.
type TunnelSession struct {
	TunnelID      string
	Domain        string
	Transport     string
	Conn          *protocol.Connection // websocket only
	MaxConcurrent int
	activeCount   int32

	outbound  *protocol.FrameScheduler
	done      chan struct{}
	closeOnce sync.Once

	activityMu   sync.Mutex
	lastActivity time.Time
	awaitingPong bool
	pingSentAt   time.Time

	h2Mu        sync.Mutex
	h2Workers   map[*HTTP2WorkerStream]struct{}
	h2Available chan *HTTP2WorkerStream
}

func NewTunnelSession(tunnelID, domain, transport string, conn *protocol.Connection, maxConcurrent int) *TunnelSession {
	if transport == "" && conn != nil {
		transport = conn.TransportName()
	}
	session := &TunnelSession{
		TunnelID:      tunnelID,
		Domain:        domain,
		Transport:     transport,
		Conn:          conn,
		MaxConcurrent: maxConcurrent,
		done:          make(chan struct{}),
		lastActivity:  time.Now(),
	}
	if conn != nil {
		session.outbound = protocol.NewFrameScheduler(protocol.DefaultPerRequestFrameQueue)
	}
	if transport == protocol.TransportHTTP2 {
		session.h2Workers = make(map[*HTTP2WorkerStream]struct{})
		session.h2Available = make(chan *HTTP2WorkerStream, http2WorkerQueueSize(maxConcurrent))
	}
	return session
}

func http2WorkerQueueSize(maxConcurrent int) int {
	if maxConcurrent > 0 {
		if maxConcurrent < defaultHTTP2WorkerQueueSize {
			return maxConcurrent
		}
		if maxConcurrent > 64 {
			return 64
		}
		return maxConcurrent
	}
	return defaultHTTP2WorkerQueueSize
}

func (s *TunnelSession) Start() {
	if s.Conn == nil || s.outbound == nil {
		return
	}
	go s.writeLoop()
	go s.heartbeatLoop()
}

func (s *TunnelSession) Enqueue(frame *protocol.Frame) error {
	if frame == nil {
		return nil
	}
	if s.outbound == nil {
		return protocol.ErrConnectionClosed
	}
	if frame.GetRequestId() == "" {
		return s.outbound.EnqueueControl(frame)
	}
	return s.outbound.Enqueue(frame)
}

func (s *TunnelSession) SupportsHTTP2Workers() bool {
	return s != nil && s.Transport == protocol.TransportHTTP2 && s.h2Available != nil
}

func (s *TunnelSession) RegisterHTTP2Worker(worker *HTTP2WorkerStream) error {
	if !s.SupportsHTTP2Workers() || worker == nil {
		return protocol.ErrConnectionClosed
	}
	select {
	case <-s.done:
		return protocol.ErrConnectionClosed
	default:
	}
	s.h2Mu.Lock()
	s.h2Workers[worker] = struct{}{}
	s.h2Mu.Unlock()
	select {
	case <-s.done:
		s.UnregisterHTTP2Worker(worker)
		return protocol.ErrConnectionClosed
	case s.h2Available <- worker:
		return nil
	}
}

func (s *TunnelSession) AcquireHTTP2Worker() (*HTTP2WorkerStream, error) {
	if !s.SupportsHTTP2Workers() {
		return nil, protocol.ErrConnectionClosed
	}
	for {
		select {
		case <-s.done:
			return nil, protocol.ErrConnectionClosed
		case worker := <-s.h2Available:
			if worker == nil {
				continue
			}
			select {
			case <-worker.Done():
				s.UnregisterHTTP2Worker(worker)
				continue
			default:
				return worker, nil
			}
		}
	}
}

func (s *TunnelSession) UnregisterHTTP2Worker(worker *HTTP2WorkerStream) int {
	if worker == nil {
		return s.HTTP2WorkerCount()
	}
	s.h2Mu.Lock()
	delete(s.h2Workers, worker)
	remaining := len(s.h2Workers)
	s.h2Mu.Unlock()
	return remaining
}

func (s *TunnelSession) HTTP2WorkerCount() int {
	s.h2Mu.Lock()
	defer s.h2Mu.Unlock()
	return len(s.h2Workers)
}

func (s *TunnelSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		if s.outbound != nil {
			s.outbound.Close()
		}
		if s.Conn != nil {
			err = s.Conn.Close()
		}
		s.h2Mu.Lock()
		workers := make([]*HTTP2WorkerStream, 0, len(s.h2Workers))
		for worker := range s.h2Workers {
			workers = append(workers, worker)
		}
		s.h2Mu.Unlock()
		for _, worker := range workers {
			_ = worker.Close()
		}
	})
	return err
}

func (s *TunnelSession) Done() <-chan struct{} { return s.done }

func (s *TunnelSession) CanAcceptRequest() bool {
	if s.MaxConcurrent <= 0 {
		return true
	}
	return atomic.LoadInt32(&s.activeCount) < int32(s.MaxConcurrent)
}

func (s *TunnelSession) IncrementActive() { atomic.AddInt32(&s.activeCount, 1) }
func (s *TunnelSession) DecrementActive() { atomic.AddInt32(&s.activeCount, -1) }

func (s *TunnelSession) MarkActivity() {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	s.lastActivity = time.Now()
}

func (s *TunnelSession) AckPong() {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	s.awaitingPong = false
}

func (s *TunnelSession) shouldPing() bool {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	return !s.awaitingPong && time.Since(s.lastActivity) >= protocol.DefaultPingPeriod
}

func (s *TunnelSession) notePingSent() {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	s.awaitingPong = true
	s.pingSentAt = time.Now()
}

func (s *TunnelSession) shouldCloseForMissedPong() bool {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	return s.awaitingPong && time.Since(s.pingSentAt) > protocol.DefaultPongWait
}

func (s *TunnelSession) writeLoop() {
	for {
		frame, err := s.outbound.Next()
		if err != nil {
			return
		}
		if err := s.Conn.Send(frame); err != nil {
			log.Printf("Tunnel %s write failed: domain=%s tunnel_id=%s err=%v", s.Transport, s.Domain, s.TunnelID, err)
			s.Close()
			return
		}
		s.MarkActivity()
	}
}

func (s *TunnelSession) heartbeatLoop() {
	ticker := time.NewTicker(protocol.DefaultPingPeriod / 2)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if s.shouldCloseForMissedPong() {
				log.Printf("Tunnel %s heartbeat timed out: domain=%s tunnel_id=%s", s.Transport, s.Domain, s.TunnelID)
				s.Close()
				return
			}
			if s.shouldPing() {
				s.notePingSent()
				if err := s.outbound.EnqueueControl(&protocol.Frame{Type: protocol.FrameType_PING}); err != nil {
					s.Close()
					return
				}
			}
		}
	}
}

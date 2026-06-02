package server

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kaenova/http-tunnels/internal/protocol"
)

// TunnelSessionStore manages active tunnel sessions (main WS connections)
type TunnelSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*TunnelSession // domain -> session
}

// NewTunnelSessionStore creates a new session store
func NewTunnelSessionStore() *TunnelSessionStore {
	return &TunnelSessionStore{
		sessions: make(map[string]*TunnelSession),
	}
}

// Get retrieves a session by domain
func (s *TunnelSessionStore) Get(domain string) (*TunnelSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[domain]
	return sess, ok
}

// Set stores a session, returning the previous one if any
func (s *TunnelSessionStore) Set(domain string, session *TunnelSession) *TunnelSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.sessions[domain]
	s.sessions[domain] = session
	return prev
}

// Delete removes a session
func (s *TunnelSessionStore) Delete(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, domain)
}

// GetAll returns all sessions (for iteration)
func (s *TunnelSessionStore) GetAll() map[string]*TunnelSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*TunnelSession, len(s.sessions))
	for k, v := range s.sessions {
		result[k] = v
	}
	return result
}

// Count returns the number of active sessions
func (s *TunnelSessionStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// TunnelSession represents an active tunnel connection
type TunnelSession struct {
	TunnelID      string
	Domain        string
	Conn          *protocol.Connection
	MaxConcurrent int
	activeCount   int32 // atomic counter for active requests

	outbound  *protocol.FrameScheduler
	done      chan struct{}
	closeOnce sync.Once

	activityMu   sync.Mutex
	lastActivity time.Time
	awaitingPong bool
	pingSentAt   time.Time
}

func NewTunnelSession(tunnelID, domain string, conn *protocol.Connection, maxConcurrent int) *TunnelSession {
	return &TunnelSession{
		TunnelID:      tunnelID,
		Domain:        domain,
		Conn:          conn,
		MaxConcurrent: maxConcurrent,
		outbound:      protocol.NewFrameScheduler(protocol.DefaultPerRequestFrameQueue),
		done:          make(chan struct{}),
		lastActivity:  time.Now(),
	}
}

func (s *TunnelSession) Start() {
	go s.writeLoop()
	go s.heartbeatLoop()
}

func (s *TunnelSession) Enqueue(frame *protocol.Frame) error {
	if frame == nil {
		return nil
	}
	if frame.GetRequestId() == "" {
		return s.outbound.EnqueueControl(frame)
	}
	return s.outbound.Enqueue(frame)
}

func (s *TunnelSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		s.outbound.Close()
		err = s.Conn.Close()
	})
	return err
}

func (s *TunnelSession) Done() <-chan struct{} {
	return s.done
}

// CanAcceptRequest checks if the tunnel can accept another request
func (s *TunnelSession) CanAcceptRequest() bool {
	if s.MaxConcurrent <= 0 {
		return true // unlimited
	}
	return atomic.LoadInt32(&s.activeCount) < int32(s.MaxConcurrent)
}

// IncrementActive increments the active request count
func (s *TunnelSession) IncrementActive() {
	atomic.AddInt32(&s.activeCount, 1)
}

// DecrementActive decrements the active request count
func (s *TunnelSession) DecrementActive() {
	atomic.AddInt32(&s.activeCount, -1)
}

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
			log.Printf("Tunnel websocket write failed: domain=%s tunnel_id=%s err=%v", s.Domain, s.TunnelID, err)
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
				log.Printf("Tunnel websocket heartbeat timed out: domain=%s tunnel_id=%s", s.Domain, s.TunnelID)
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

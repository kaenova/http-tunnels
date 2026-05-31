package server

import (
	"sync"
	"sync/atomic"

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
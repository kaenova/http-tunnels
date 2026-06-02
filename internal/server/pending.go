package server

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrRequestTimeout is returned when a pending request times out
var ErrRequestTimeout = errors.New("request timeout waiting for client")

// PendingRequest represents a request waiting for client response frames.
type PendingRequest struct {
	ID         string
	TunnelID   string
	Method     string
	Path       string
	Headers    map[string][]string
	ContentLen int64
	ResponseCh chan *PendingResponse
	ErrorCh    chan error
	bodyCh     chan []byte
	CreatedAt  time.Time
	LogEntry   *RequestResponseLog // for request/response logging

	responseStarted atomic.Bool
	closeBodyOnce   sync.Once
}

func (r *PendingRequest) MarkResponseStarted() {
	if r != nil {
		r.responseStarted.Store(true)
	}
}

func (r *PendingRequest) ResponseStarted() bool {
	return r != nil && r.responseStarted.Load()
}

func (r *PendingRequest) CloseBody() {
	if r == nil {
		return
	}
	r.closeBodyOnce.Do(func() {
		if r.bodyCh != nil {
			close(r.bodyCh)
		}
	})
}

func (r *PendingRequest) Fail(err error) {
	if r == nil || err == nil {
		return
	}
	select {
	case r.ErrorCh <- err:
	default:
	}
	r.CloseBody()
}

// PendingResponse is the response received from the client via the main WS.
type PendingResponse struct {
	Status  int
	Headers map[string][]string
	Body    <-chan []byte
	Error   error
}

// PendingStore manages pending requests with timeout cleanup.
type PendingStore struct {
	mu       sync.RWMutex
	requests map[string]*PendingRequest
	timeout  time.Duration
	stopCh   chan struct{}
}

// NewPendingStore creates a new pending request store with cleanup goroutine
func NewPendingStore(timeout time.Duration) *PendingStore {
	ps := &PendingStore{
		requests: make(map[string]*PendingRequest),
		timeout:  timeout,
		stopCh:   make(chan struct{}),
	}
	go ps.cleanupLoop()
	return ps
}

// Add inserts a pending request
func (ps *PendingStore) Add(req *PendingRequest) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	req.CreatedAt = time.Now()
	ps.requests[req.ID] = req
}

// Get retrieves a pending request by ID
func (ps *PendingStore) Get(id string) (*PendingRequest, bool) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	req, ok := ps.requests[id]
	return req, ok
}

// Remove deletes a pending request by ID
func (ps *PendingStore) Remove(id string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	delete(ps.requests, id)
}

// CleanupByTunnel removes all pending requests for a given tunnel
func (ps *PendingStore) CleanupByTunnel(tunnelID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for id, req := range ps.requests {
		if req.TunnelID == tunnelID {
			delete(ps.requests, id)
		}
	}
}

// FailByTunnel fails and removes all pending requests for a given tunnel.
func (ps *PendingStore) FailByTunnel(tunnelID string, err error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for id, req := range ps.requests {
		if req.TunnelID != tunnelID {
			continue
		}
		delete(ps.requests, id)
		req.Fail(err)
	}
}

// Count returns the total number of pending requests
func (ps *PendingStore) Count() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.requests)
}

// CountByTunnel returns the number of pending requests for a given tunnel
func (ps *PendingStore) CountByTunnel(tunnelID string) int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	count := 0
	for _, req := range ps.requests {
		if req.TunnelID == tunnelID {
			count++
		}
	}
	return count
}

// Stop terminates the cleanup goroutine
func (ps *PendingStore) Stop() {
	close(ps.stopCh)
}

func (ps *PendingStore) cleanupLoop() {
	ticker := time.NewTicker(ps.timeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ps.stopCh:
			return
		case <-ticker.C:
			ps.cleanupExpired()
		}
	}
}

func (ps *PendingStore) cleanupExpired() {
	now := time.Now()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for id, req := range ps.requests {
		if req.ResponseStarted() {
			continue
		}
		if now.Sub(req.CreatedAt) > ps.timeout {
			req.Fail(ErrRequestTimeout)
			delete(ps.requests, id)
		}
	}
}

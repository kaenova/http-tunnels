package server

import (
	"errors"
	"io"
	"sync"
	"time"
)

// ErrRequestTimeout is returned when a pending request times out
var ErrRequestTimeout = errors.New("request timeout waiting for client")

// PendingRequest represents a request waiting for client to open a dedicated WS
type PendingRequest struct {
	ID          string
	TunnelID    string
	Method      string
	Path        string
	Headers     map[string][]string
	ContentLen  int64
	BodyReader  *io.PipeReader
	ResponseCh  chan *PendingResponse
	ErrorCh     chan error
	bodyCh      chan []byte
	CreatedAt   time.Time
	LogEntry    *RequestResponseLog // for request/response logging
}

// PendingResponse is the response received from the client via dedicated WS
type PendingResponse struct {
	Status  int
	Headers map[string][]string
	Body    <-chan []byte
	Error   error
}

// PendingStore manages pending requests with timeout cleanup
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
		if now.Sub(req.CreatedAt) > ps.timeout {
			if req.ErrorCh != nil {
				select {
				case req.ErrorCh <- ErrRequestTimeout:
				default:
				}
			}
			delete(ps.requests, id)
		}
	}
}
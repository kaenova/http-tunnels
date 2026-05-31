package server

import (
	"sync"
	"testing"
	"time"
)

func TestPendingStoreAddGet(t *testing.T) {
	ps := NewPendingStore(5 * time.Second)

	req := &PendingRequest{
		ID:       "req_001",
		TunnelID: "tun_001",
		Method:   "GET",
		Path:     "/api/test",
	}
	ps.Add(req)

	got, ok := ps.Get("req_001")
	if !ok {
		t.Fatal("pending request not found")
	}
	if got.ID != "req_001" {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, "req_001")
	}
	if got.TunnelID != "tun_001" {
		t.Errorf("TunnelID mismatch")
	}
}

func TestPendingStoreRemove(t *testing.T) {
	ps := NewPendingStore(5 * time.Second)

	req := &PendingRequest{ID: "req_001"}
	ps.Add(req)
	ps.Remove("req_001")

	_, ok := ps.Get("req_001")
	if ok {
		t.Error("request should be removed")
	}
}

func TestPendingStoreTimeout(t *testing.T) {
	ps := NewPendingStore(50 * time.Millisecond)

	req := &PendingRequest{
		ID:        "req_001",
		CreatedAt: time.Now(),
	}
	ps.Add(req)

	// Should exist immediately
	_, ok := ps.Get("req_001")
	if !ok {
		t.Fatal("request should exist before timeout")
	}

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	_, ok = ps.Get("req_001")
	if ok {
		t.Error("request should be cleaned up after timeout")
	}
}

func TestPendingStoreCleanupByTunnel(t *testing.T) {
	ps := NewPendingStore(5 * time.Second)

	ps.Add(&PendingRequest{ID: "req_a", TunnelID: "tun_1"})
	ps.Add(&PendingRequest{ID: "req_b", TunnelID: "tun_1"})
	ps.Add(&PendingRequest{ID: "req_c", TunnelID: "tun_2"})

	ps.CleanupByTunnel("tun_1")

	_, ok := ps.Get("req_a")
	if ok {
		t.Error("req_a should be cleaned up")
	}
	_, ok = ps.Get("req_b")
	if ok {
		t.Error("req_b should be cleaned up")
	}
	_, ok = ps.Get("req_c")
	if !ok {
		t.Error("req_c (different tunnel) should still exist")
	}
}

func TestPendingStoreCount(t *testing.T) {
	ps := NewPendingStore(5 * time.Second)

	if ps.Count() != 0 {
		t.Errorf("initial count: got %d, want 0", ps.Count())
	}

	ps.Add(&PendingRequest{ID: "req_1"})
	ps.Add(&PendingRequest{ID: "req_2"})
	ps.Add(&PendingRequest{ID: "req_3"})

	if ps.Count() != 3 {
		t.Errorf("count: got %d, want 3", ps.Count())
	}

	ps.Remove("req_1")
	if ps.Count() != 2 {
		t.Errorf("count after remove: got %d, want 2", ps.Count())
	}
}

func TestPendingStoreCountByTunnel(t *testing.T) {
	ps := NewPendingStore(5 * time.Second)

	ps.Add(&PendingRequest{ID: "req_a", TunnelID: "tun_1"})
	ps.Add(&PendingRequest{ID: "req_b", TunnelID: "tun_1"})
	ps.Add(&PendingRequest{ID: "req_c", TunnelID: "tun_2"})

	if ps.CountByTunnel("tun_1") != 2 {
		t.Errorf("tun_1 count: got %d, want 2", ps.CountByTunnel("tun_1"))
	}
	if ps.CountByTunnel("tun_2") != 1 {
		t.Errorf("tun_2 count: got %d, want 1", ps.CountByTunnel("tun_2"))
	}
	if ps.CountByTunnel("tun_3") != 0 {
		t.Errorf("tun_3 count: got %d, want 0", ps.CountByTunnel("tun_3"))
	}
}

func TestPendingStoreConcurrent(t *testing.T) {
	ps := NewPendingStore(5 * time.Second)
	var wg sync.WaitGroup

	// Concurrent adds
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ps.Add(&PendingRequest{ID: string(rune('A' + id%26)) + string(rune('0' + id/26))})
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ps.Get("A0")
			ps.Count()
		}()
	}

	wg.Wait()
}
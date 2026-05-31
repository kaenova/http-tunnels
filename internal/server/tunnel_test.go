package server

import (
	"testing"
)

func TestTunnelSessionStoreSetGetDelete(t *testing.T) {
	store := NewTunnelSessionStore()

	sess := &TunnelSession{
		TunnelID: "tun_001",
		Domain:   "test.localhost",
	}

	// Set
	prev := store.Set("test.localhost", sess)
	if prev != nil {
		t.Error("first Set should return nil")
	}

	// Get
	got, ok := store.Get("test.localhost")
	if !ok {
		t.Fatal("session not found")
	}
	if got.TunnelID != "tun_001" {
		t.Errorf("TunnelID mismatch: got %q", got.TunnelID)
	}

	// Replace
	newSess := &TunnelSession{TunnelID: "tun_002", Domain: "test.localhost"}
	prev = store.Set("test.localhost", newSess)
	if prev == nil || prev.TunnelID != "tun_001" {
		t.Error("Set should return previous session")
	}

	// Delete
	store.Delete("test.localhost")
	_, ok = store.Get("test.localhost")
	if ok {
		t.Error("session should be deleted")
	}
}

func TestTunnelSessionStoreCount(t *testing.T) {
	store := NewTunnelSessionStore()

	if store.Count() != 0 {
		t.Errorf("initial count: got %d, want 0", store.Count())
	}

	store.Set("a.localhost", &TunnelSession{TunnelID: "a"})
	store.Set("b.localhost", &TunnelSession{TunnelID: "b"})

	if store.Count() != 2 {
		t.Errorf("count: got %d, want 2", store.Count())
	}
}

func TestTunnelSessionStoreGetAll(t *testing.T) {
	store := NewTunnelSessionStore()

	store.Set("a.localhost", &TunnelSession{TunnelID: "a"})
	store.Set("b.localhost", &TunnelSession{TunnelID: "b"})

	all := store.GetAll()
	if len(all) != 2 {
		t.Errorf("GetAll count: got %d, want 2", len(all))
	}
}
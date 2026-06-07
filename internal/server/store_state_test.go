package server

import (
	"context"
	"testing"
	"time"
)

func TestListActiveTunnelsExcludesPending(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	record, err := store.CreateTunnel(ctx, "demo", "demo.localhost", "hash-1", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	if record.State != "pending" {
		t.Fatalf("expected pending state, got %s", record.State)
	}

	response, err := store.ListActiveTunnels(ctx, 1, 10)
	if err != nil {
		t.Fatalf("list active tunnels: %v", err)
	}
	if len(response.Items) != 0 {
		t.Fatalf("expected no active tunnels, got %d", len(response.Items))
	}
}

func TestReconcileActiveTunnelStatesMarksMissingSessionsDisconnected(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	record, err := store.CreateTunnel(ctx, "demo", "demo.localhost", "hash-1", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	if err := store.MarkTunnelActive(ctx, record.ID, "127.0.0.1", "test", "test-version"); err != nil {
		t.Fatalf("mark active: %v", err)
	}

	if err := store.ReconcileActiveTunnelStates(ctx, nil); err != nil {
		t.Fatalf("reconcile active tunnels: %v", err)
	}

	updated, err := store.GetTunnelByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("get tunnel: %v", err)
	}
	if updated.State != "disconnected" {
		t.Fatalf("expected disconnected state, got %s", updated.State)
	}
}

func TestExpirePendingTunnelsMarksOldPendingDisconnected(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	record, err := store.CreateTunnel(ctx, "demo", "demo.localhost", "hash-1", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	if err := store.ExpirePendingTunnels(ctx, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("expire pending tunnels: %v", err)
	}

	updated, err := store.GetTunnelByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("get tunnel: %v", err)
	}
	if updated.State != "disconnected" {
		t.Fatalf("expected disconnected state, got %s", updated.State)
	}
}

package server

import (
	"context"
	"testing"
)

func TestDashboardSummaryCountsActiveTransportBreakdown(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	h2Record, err := store.CreateTunnel(ctx, "alpha", "alpha.localhost", "hash-1", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("create h2 tunnel: %v", err)
	}
	wsRecord, err := store.CreateTunnel(ctx, "beta", "beta.localhost", "hash-2", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("create websocket tunnel: %v", err)
	}

	if err := store.MarkTunnelActive(ctx, h2Record.ID, "http2", "127.0.0.1", "agent-h2"); err != nil {
		t.Fatalf("mark h2 active: %v", err)
	}
	if err := store.MarkTunnelActive(ctx, wsRecord.ID, "websocket", "127.0.0.1", "agent-ws"); err != nil {
		t.Fatalf("mark websocket active: %v", err)
	}

	summary, err := store.dashboardSummary(ctx)
	if err != nil {
		t.Fatalf("dashboard summary: %v", err)
	}
	if summary.ActiveTunnels != 2 {
		t.Fatalf("expected 2 active tunnels, got %d", summary.ActiveTunnels)
	}
	if summary.ActiveHTTP2Tunnels != 1 {
		t.Fatalf("expected 1 active HTTP/2 tunnel, got %d", summary.ActiveHTTP2Tunnels)
	}
	if summary.ActiveWebSocketTunnels != 1 {
		t.Fatalf("expected 1 active websocket tunnel, got %d", summary.ActiveWebSocketTunnels)
	}

	h2Stored, err := store.GetTunnelByID(ctx, h2Record.ID)
	if err != nil {
		t.Fatalf("get h2 tunnel: %v", err)
	}
	if h2Stored.Transport != "http2" {
		t.Fatalf("expected stored h2 transport, got %q", h2Stored.Transport)
	}

	wsStored, err := store.GetTunnelByID(ctx, wsRecord.ID)
	if err != nil {
		t.Fatalf("get websocket tunnel: %v", err)
	}
	if wsStored.Transport != "websocket" {
		t.Fatalf("expected stored websocket transport, got %q", wsStored.Transport)
	}
}

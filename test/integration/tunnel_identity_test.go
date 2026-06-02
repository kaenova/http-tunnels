package integration

import "testing"

func TestStableTunnelIDByFullDomain(t *testing.T) {
	h := NewHarness(t)

	first := h.CreateTunnel(t, "myapp")
	second := h.CreateTunnel(t, "myapp")

	if first.Domain != second.Domain {
		t.Fatalf("expected same domain, got %q and %q", first.Domain, second.Domain)
	}
	if first.ID == "" || second.ID == "" {
		t.Fatalf("expected tunnel ids to be present")
	}
	if first.ID != second.ID {
		t.Fatalf("expected stable tunnel id for same full domain, got %q and %q", first.ID, second.ID)
	}
}

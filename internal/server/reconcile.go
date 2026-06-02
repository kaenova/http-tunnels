package server

import (
	"context"
	"log"
	"time"
)

const (
	tunnelStateReconcileInterval = 30 * time.Second
	stalePendingTunnelAge        = 2 * time.Minute
)

func (a *App) reconcileTunnelStateLoop() {
	if a == nil || a.store == nil {
		return
	}
	a.reconcileTunnelState()

	ticker := time.NewTicker(tunnelStateReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.reconcileCtx.Done():
			return
		case <-ticker.C:
			a.reconcileTunnelState()
		}
	}
}

func (a *App) reconcileTunnelState() {
	if a == nil || a.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	activeTunnelIDs := make([]string, 0)
	for _, session := range a.sessions.GetAll() {
		if session == nil || session.TunnelID == "" {
			continue
		}
		activeTunnelIDs = append(activeTunnelIDs, session.TunnelID)
	}

	if err := a.store.ReconcileActiveTunnelStates(ctx, activeTunnelIDs); err != nil {
		log.Printf("reconcile active tunnel states failed: %v", err)
	}
	if err := a.store.ExpirePendingTunnels(ctx, time.Now().UTC().Add(-stalePendingTunnelAge)); err != nil {
		log.Printf("expire pending tunnels failed: %v", err)
	}
}

package server

import (
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"time"
)

// App is the main tunnel server application
type App struct {
	config   Config
	store    *Store
	pending  *PendingStore
	assets   fs.FS
	sessions *TunnelSessionStore
	server   *http.Server
}

// NewApp creates a new tunnel server application
func NewApp(config Config, assets fs.FS) (*App, error) {
	store, err := OpenStore(config.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}

	app := &App{
		config:   config,
		store:    store,
		pending:  NewPendingStore(time.Duration(config.DefaultRequestTimeout) * time.Millisecond),
		assets:   assets,
		sessions: NewTunnelSessionStore(),
	}

	return app, nil
}

// Serve starts the HTTP server on the given listener
func (a *App) Serve(listener net.Listener) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/ping", a.handlePing)
	mux.HandleFunc("/new_tunnel", a.handleNewTunnel)
	mux.HandleFunc("/tunnel", a.handleTunnelWS)
	mux.HandleFunc("/tunnel-response", a.handleTunnelResponseWS)
	mux.HandleFunc("/admin/", a.handleAdmin)
	mux.HandleFunc("/api/admin/", a.handleAdminAPI)
	// Catch-all for tunnel HTTP proxy (must be last)
	mux.HandleFunc("/", a.handleTunnelHTTP)

	a.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}

	log.Printf("Tunnel server listening on %s", listener.Addr().String())
	return a.server.Serve(listener)
}

// Shutdown gracefully shuts down the server
func (a *App) Shutdown() error {
	a.pending.Stop()
	if a.server != nil {
		return a.server.Close()
	}
	return nil
}

// Close closes the server and store
func (a *App) Close() error {
	a.pending.Stop()
	if a.store != nil {
		a.store.Close()
	}
	return nil
}

// Run starts the server on the configured address
func (a *App) Run() error {
	listener, err := net.Listen("tcp", a.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return a.Serve(listener)
}
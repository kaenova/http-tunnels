package server

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store handles SQLite operations
type Store struct {
	db *sql.DB
}

// OpenStore opens the SQLite database
func OpenStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}

	// Configure
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA foreign_keys=ON")
	db.Exec("PRAGMA busy_timeout=5000")

	// Migrate
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the database
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS tunnels (
			id TEXT PRIMARY KEY,
			domain TEXT NOT NULL,
			requested_subdomain TEXT,
			domain_key_hash TEXT NOT NULL,
			state TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			connected_at TIMESTAMP,
			disconnected_at TIMESTAMP,
			last_activity_at TIMESTAMP,
			total_request_bytes INTEGER NOT NULL DEFAULT 0,
			total_response_bytes INTEGER NOT NULL DEFAULT 0,
			request_count INTEGER NOT NULL DEFAULT 0,
			remote_addr TEXT,
			user_agent TEXT,
			deleted_at TIMESTAMP
		)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migration: %w", err)
		}
	}
	return nil
}

// CreateTunnel creates a new tunnel record
func (s *Store) CreateTunnel(ctx context.Context, requestedSubdomain, domain, domainKeyHash, remoteAddr, userAgent string) (TunnelRecord, error) {
	now := time.Now().UTC()
	id := "tun_" + randomID(12)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tunnels (id, domain, requested_subdomain, domain_key_hash, state, created_at, remote_addr, user_agent)
		VALUES (?, ?, ?, ?, 'pending', ?, ?, ?)
	`, id, domain, requestedSubdomain, domainKeyHash, now, remoteAddr, userAgent)
	if err != nil {
		return TunnelRecord{}, err
	}

	return TunnelRecord{
		ID:        id,
		Domain:    domain,
		CreatedAt: now,
	}, nil
}

// FindTunnelForConnection looks up a tunnel by domain and key hash
func (s *Store) FindTunnelForConnection(ctx context.Context, domain, domainKeyHash string) (TunnelRecord, error) {
	var rec TunnelRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT id, domain FROM tunnels
		WHERE domain = ? AND domain_key_hash = ? AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, domain, domainKeyHash).Scan(&rec.ID, &rec.Domain)
	return rec, err
}

// MarkTunnelActive marks a tunnel as active
func (s *Store) MarkTunnelActive(ctx context.Context, tunnelID, remoteAddr, userAgent string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE tunnels SET state='active', connected_at=?, last_activity_at=?
		WHERE id=?
	`, now, now, tunnelID)
	return err
}

// MarkTunnelDisconnected marks a tunnel as disconnected
func (s *Store) MarkTunnelDisconnected(ctx context.Context, tunnelID string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE tunnels SET state='disconnected', disconnected_at=?
		WHERE id=?
	`, now, tunnelID)
	return err
}

// TunnelRecord represents a tunnel database record
type TunnelRecord struct {
	ID        string
	Domain    string
	CreatedAt time.Time
}

func randomID(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(1) // ensure uniqueness
	}
	return string(b)
}
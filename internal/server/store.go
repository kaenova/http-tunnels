package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/kaenova/http-tunnels/internal/protocol"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB

	// Async log queue
	logQueue  chan RequestResponseLog
	logWg     sync.WaitGroup
	logCtx    context.Context
	logCancel context.CancelFunc
}

func OpenStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database failed: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	store := &Store{db: db}
	if err := store.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	// Start async log worker
	store.logCtx, store.logCancel = context.WithCancel(context.Background())
	store.logQueue = make(chan RequestResponseLog, 2048)
	store.logWg.Add(1)
	go store.logWorker()

	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	// Stop the log worker
	if s.logCancel != nil {
		s.logCancel()
	}
	s.logWg.Wait()
	close(s.logQueue)
	return s.db.Close()
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database table is locked")
}

func (s *Store) execBusyRetry(fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(25*(attempt+1)) * time.Millisecond)
		}
		err := fn()
		if !isSQLiteBusy(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

func (s *Store) execContextBusyRetry(ctx context.Context, query string, args ...any) error {
	return s.execBusyRetry(func() error {
		_, err := s.db.ExecContext(ctx, query, args...)
		return err
	})
}

func (s *Store) execResultContextBusyRetry(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var result sql.Result
	err := s.execBusyRetry(func() error {
		var err error
		result, err = s.db.ExecContext(ctx, query, args...)
		return err
	})
	return result, err
}

func (s *Store) beginTxBusyRetry(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	var tx *sql.Tx
	err := s.execBusyRetry(func() error {
		var err error
		tx, err = s.db.BeginTx(ctx, opts)
		return err
	})
	return tx, err
}

func (s *Store) logWorker() {
	defer s.logWg.Done()
	for {
		select {
		case <-s.logCtx.Done():
			// Drain remaining logs before exit
			for {
				select {
				case logEntry := <-s.logQueue:
					s.insertRequestLogSync(logEntry)
				default:
					return
				}
			}
		case logEntry := <-s.logQueue:
			s.insertRequestLogSync(logEntry)
		}
	}
}

func (s *Store) insertRequestLogSync(logEntry RequestResponseLog) {
	requestHeadersJSON, err := marshalHeaders(logEntry.RequestHeaders)
	if err != nil {
		log.Printf("marshal request headers: %v", err)
		return
	}
	responseHeadersJSON, err := marshalHeaders(logEntry.ResponseHeaders)
	if err != nil {
		log.Printf("marshal response headers: %v", err)
		return
	}

	tx, err := s.beginTxBusyRetry(context.Background(), nil)
	if err != nil {
		log.Printf("request log begin tx: %v", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`
		INSERT INTO request_response_logs (
			id, tunnel_id, domain, method, path, request_headers_json, response_headers_json, request_preview, response_preview,
			request_content_type, response_content_type, request_bytes, response_bytes, status_code, started_at, completed_at,
			duration_ms, error_message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, logEntry.ID, logEntry.TunnelID, logEntry.Domain, logEntry.Method, logEntry.Path,
		nullableString(requestHeadersJSON), nullableString(responseHeadersJSON),
		nullableString(logEntry.RequestPreview), nullableString(logEntry.ResponsePreview),
		nullableString(logEntry.RequestContentType), nullableString(logEntry.ResponseContentType),
		logEntry.RequestBytes, logEntry.ResponseBytes, logEntry.StatusCode,
		logEntry.StartedAt, nullableTimeValue(logEntry.CompletedAt),
		logEntry.DurationMs, nullableString(logEntry.ErrorMessage))
	if err != nil {
		log.Printf("inserting request log: %v", err)
		return
	}

	completedAt := time.Now().UTC()
	_, err = tx.Exec(`
		UPDATE tunnels
		SET request_count = request_count + 1,
		    total_request_bytes = total_request_bytes + ?,
		    total_response_bytes = total_response_bytes + ?,
		    last_activity_at = ?
		WHERE id = ?
	`, logEntry.RequestBytes, logEntry.ResponseBytes, completedAt, logEntry.TunnelID)
	if err != nil {
		log.Printf("updating tunnel counters: %v", err)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("request log commit: %v", err)
	}
}

func (s *Store) configure() error {
	pragmas := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA busy_timeout = 5000;`,
		`PRAGMA synchronous = NORMAL;`,
	}
	for _, statement := range pragmas {
		if err := s.execBusyRetry(func() error {
			_, err := s.db.Exec(statement)
			return err
		}); err != nil {
			return fmt.Errorf("configuring sqlite failed: %w", err)
		}
	}
	return nil
}

func (s *Store) migrate() error {
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
		);`,
		`CREATE INDEX IF NOT EXISTS idx_tunnels_domain_state ON tunnels(domain, state, deleted_at, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_tunnels_state_created_at ON tunnels(state, deleted_at, created_at DESC);`,
		`CREATE TABLE IF NOT EXISTS request_response_logs (
			id TEXT PRIMARY KEY,
			tunnel_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			request_headers_json TEXT,
			response_headers_json TEXT,
			request_preview TEXT,
			response_preview TEXT,
			request_content_type TEXT,
			response_content_type TEXT,
			request_bytes INTEGER NOT NULL DEFAULT 0,
			response_bytes INTEGER NOT NULL DEFAULT 0,
			status_code INTEGER NOT NULL DEFAULT 0,
			started_at TIMESTAMP NOT NULL,
			completed_at TIMESTAMP,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			error_message TEXT,
			FOREIGN KEY(tunnel_id) REFERENCES tunnels(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_tunnel_started_at ON request_response_logs(tunnel_id, started_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_domain_started_at ON request_response_logs(domain, started_at DESC);`,
		`CREATE TABLE IF NOT EXISTS tunnel_creation_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tunnel_id TEXT,
			domain TEXT,
			requested_subdomain TEXT,
			remote_addr TEXT,
			user_agent TEXT,
			success INTEGER NOT NULL DEFAULT 1,
			error_message TEXT,
			created_at TIMESTAMP NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_tunnel_creation_logs_tunnel_created_at ON tunnel_creation_logs(tunnel_id, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_tunnel_creation_logs_created_at ON tunnel_creation_logs(created_at DESC);`,
	}

	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("running migration failed: %w", err)
		}
	}
	return nil
}

func (s *Store) CreateTunnel(ctx context.Context, requestedSubdomain, domain, domainKeyHash, remoteAddr, userAgent string) (TunnelRecord, error) {
	now := time.Now().UTC()
	record := TunnelRecord{
		ID:                 generateTunnelID(),
		Domain:             domain,
		RequestedSubdomain: requestedSubdomain,
		State:              "pending",
		CreatedAt:          now,
		RemoteAddr:         remoteAddr,
		UserAgent:          userAgent,
	}

	err := s.execContextBusyRetry(ctx, `
		INSERT INTO tunnels (
			id, domain, requested_subdomain, domain_key_hash, state, created_at, remote_addr, user_agent
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.Domain, nullableString(record.RequestedSubdomain), domainKeyHash, record.State, record.CreatedAt, nullableString(record.RemoteAddr), nullableString(record.UserAgent))
	if err != nil {
		return TunnelRecord{}, fmt.Errorf("creating tunnel failed: %w", err)
	}

	return record, nil
}

func (s *Store) DomainExists(ctx context.Context, domain string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM tunnels
		WHERE domain = ? AND deleted_at IS NULL AND state IN ('pending', 'active')
	`, domain).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking domain existence failed: %w", err)
	}
	return count > 0, nil
}

func (s *Store) LogTunnelCreation(ctx context.Context, tunnelID, domain, requestedSubdomain, remoteAddr, userAgent string, success bool, errorMessage string) error {
	err := s.execContextBusyRetry(ctx, `
		INSERT INTO tunnel_creation_logs (
			tunnel_id, domain, requested_subdomain, remote_addr, user_agent, success, error_message, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableString(tunnelID), nullableString(domain), nullableString(requestedSubdomain), nullableString(remoteAddr), nullableString(userAgent), boolToInt(success), nullableString(errorMessage), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("logging tunnel creation failed: %w", err)
	}
	return nil
}

func (s *Store) FindTunnelForConnection(ctx context.Context, domain, domainKeyHash string) (TunnelRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, domain, requested_subdomain, state, created_at, connected_at, disconnected_at, last_activity_at,
		       total_request_bytes, total_response_bytes, request_count, remote_addr, user_agent, deleted_at
		FROM tunnels
		WHERE domain = ? AND domain_key_hash = ? AND deleted_at IS NULL AND state != 'deleted'
		ORDER BY created_at DESC
		LIMIT 1
	`, domain, domainKeyHash)
	return scanTunnelRecord(row)
}

func (s *Store) MarkTunnelActive(ctx context.Context, tunnelID, remoteAddr, userAgent string) error {
	now := time.Now().UTC()
	err := s.execContextBusyRetry(ctx, `
		UPDATE tunnels
		SET state = 'active', connected_at = COALESCE(connected_at, ?), disconnected_at = NULL, last_activity_at = ?, remote_addr = ?, user_agent = ?
		WHERE id = ?
	`, now, now, nullableString(remoteAddr), nullableString(userAgent), tunnelID)
	if err != nil {
		return fmt.Errorf("marking tunnel active failed: %w", err)
	}
	return nil
}

func (s *Store) MarkTunnelDisconnected(ctx context.Context, tunnelID string) error {
	now := time.Now().UTC()
	err := s.execContextBusyRetry(ctx, `
		UPDATE tunnels
		SET state = CASE WHEN deleted_at IS NULL THEN 'disconnected' ELSE 'deleted' END,
		    disconnected_at = ?,
		    last_activity_at = COALESCE(last_activity_at, ?)
		WHERE id = ?
	`, now, now, tunnelID)
	if err != nil {
		return fmt.Errorf("marking tunnel disconnected failed: %w", err)
	}
	return nil
}

func (s *Store) DeleteTunnel(ctx context.Context, tunnelID string) error {
	now := time.Now().UTC()
	result, err := s.execResultContextBusyRetry(ctx, `
		UPDATE tunnels
		SET state = 'deleted', deleted_at = ?, disconnected_at = COALESCE(disconnected_at, ?)
		WHERE id = ?
	`, now, now, tunnelID)
	if err != nil {
		return fmt.Errorf("deleting tunnel failed: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("deleting tunnel failed: %w", err)
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetTunnelByID(ctx context.Context, tunnelID string) (TunnelRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, domain, requested_subdomain, state, created_at, connected_at, disconnected_at, last_activity_at,
		       total_request_bytes, total_response_bytes, request_count, remote_addr, user_agent, deleted_at
		FROM tunnels
		WHERE id = ? AND deleted_at IS NULL
	`, tunnelID)
	return scanTunnelRecord(row)
}

func (s *Store) RecordRequestLog(ctx context.Context, logEntry RequestResponseLog) error {
	select {
	case s.logQueue <- logEntry:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Queue full — log to stderr and drop
		log.Printf("request log queue full, dropping log for %s %s", logEntry.Method, logEntry.Path)
		return nil
	}
}

func (s *Store) ListActiveTunnels(ctx context.Context, page, pageSize int) (TunnelListResponse, error) {
	page, pageSize = normalizePagination(page, pageSize)
	var totalItems int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM tunnels
		WHERE deleted_at IS NULL AND state IN ('pending', 'active')
	`).Scan(&totalItems); err != nil {
		return TunnelListResponse{}, fmt.Errorf("counting tunnels failed: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, domain, requested_subdomain, state, created_at, connected_at, disconnected_at, last_activity_at,
		       total_request_bytes, total_response_bytes, request_count, remote_addr, user_agent, deleted_at
		FROM tunnels
		WHERE deleted_at IS NULL AND state IN ('pending', 'active')
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, pageSize, (page-1)*pageSize)
	if err != nil {
		return TunnelListResponse{}, fmt.Errorf("listing tunnels failed: %w", err)
	}
	defer rows.Close()

	items, err := collectTunnelRecords(rows)
	if err != nil {
		return TunnelListResponse{}, err
	}

	return TunnelListResponse{
		Items:      items,
		Page:       page,
		PageSize:   pageSize,
		TotalItems: totalItems,
		TotalPages: totalPages(totalItems, pageSize),
	}, nil
}

func (s *Store) ListRequestLogs(ctx context.Context, tunnelID string, page, pageSize int) (RequestLogListResponse, error) {
	page, pageSize = normalizePagination(page, pageSize)
	var totalItems int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM request_response_logs
		WHERE tunnel_id = ?
	`, tunnelID).Scan(&totalItems); err != nil {
		return RequestLogListResponse{}, fmt.Errorf("counting request logs failed: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tunnel_id, domain, method, path, request_headers_json, response_headers_json, request_preview,
		       response_preview, request_content_type, response_content_type, request_bytes, response_bytes, status_code,
		       started_at, completed_at, duration_ms, error_message
		FROM request_response_logs
		WHERE tunnel_id = ?
		ORDER BY started_at DESC
		LIMIT ? OFFSET ?
	`, tunnelID, pageSize, (page-1)*pageSize)
	if err != nil {
		return RequestLogListResponse{}, fmt.Errorf("listing request logs failed: %w", err)
	}
	defer rows.Close()

	items, err := collectRequestLogs(rows)
	if err != nil {
		return RequestLogListResponse{}, err
	}

	return RequestLogListResponse{
		Items:      items,
		Page:       page,
		PageSize:   pageSize,
		TotalItems: totalItems,
		TotalPages: totalPages(totalItems, pageSize),
	}, nil
}

func (s *Store) ListAllRequestLogs(ctx context.Context, filters RequestLogFilters, page, pageSize int) (RequestLogListResponse, error) {
	page, pageSize = normalizePagination(page, pageSize)
	whereClause, args := buildRequestLogFilterClause(filters)

	countQuery := `
		SELECT COUNT(1)
		FROM request_response_logs
	` + whereClause

	var totalItems int64
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&totalItems); err != nil {
		return RequestLogListResponse{}, fmt.Errorf("counting request activity logs failed: %w", err)
	}

	dataQuery := `
		SELECT id, tunnel_id, domain, method, path, request_headers_json, response_headers_json, request_preview,
		       response_preview, request_content_type, response_content_type, request_bytes, response_bytes, status_code,
		       started_at, completed_at, duration_ms, error_message
		FROM request_response_logs
	` + whereClause + `
		ORDER BY started_at DESC
		LIMIT ? OFFSET ?
	`

	rows, err := s.db.QueryContext(ctx, dataQuery, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return RequestLogListResponse{}, fmt.Errorf("listing request activity logs failed: %w", err)
	}
	defer rows.Close()

	items, err := collectRequestLogs(rows)
	if err != nil {
		return RequestLogListResponse{}, err
	}

	return RequestLogListResponse{
		Items:      items,
		Page:       page,
		PageSize:   pageSize,
		TotalItems: totalItems,
		TotalPages: totalPages(totalItems, pageSize),
	}, nil
}

func (s *Store) GetRequestLogByID(ctx context.Context, requestLogID string) (RequestResponseLog, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, tunnel_id, domain, method, path, request_headers_json, response_headers_json, request_preview,
		       response_preview, request_content_type, response_content_type, request_bytes, response_bytes, status_code,
		       started_at, completed_at, duration_ms, error_message
		FROM request_response_logs
		WHERE id = ?
	`, requestLogID)
	return scanRequestLog(row)
}

func (s *Store) GetDashboard(ctx context.Context) (DashboardResponse, error) {
	summary, err := s.dashboardSummary(ctx)
	if err != nil {
		return DashboardResponse{}, err
	}
	activeTunnels, err := s.listLatestActiveTunnels(ctx, 5)
	if err != nil {
		return DashboardResponse{}, err
	}
	recentRequests, err := s.listRecentRequests(ctx, 12)
	if err != nil {
		return DashboardResponse{}, err
	}
	recentCreates, err := s.listRecentTunnelCreations(ctx, 8, "")
	if err != nil {
		return DashboardResponse{}, err
	}

	return DashboardResponse{
		Summary:             summary,
		ActiveTunnels:       activeTunnels,
		RecentRequests:      recentRequests,
		RecentTunnelCreates: recentCreates,
	}, nil
}

func (s *Store) GetTunnelDetail(ctx context.Context, tunnelID string, page, pageSize int) (TunnelDetailResponse, error) {
	tunnel, err := s.GetTunnelByID(ctx, tunnelID)
	if err != nil {
		return TunnelDetailResponse{}, err
	}
	statusChart, err := s.statusChart(ctx, tunnelID, 7)
	if err != nil {
		return TunnelDetailResponse{}, err
	}
	trafficChart, err := s.trafficChart(ctx, tunnelID, 7)
	if err != nil {
		return TunnelDetailResponse{}, err
	}
	logs, err := s.ListRequestLogs(ctx, tunnelID, page, pageSize)
	if err != nil {
		return TunnelDetailResponse{}, err
	}
	creationHistory, err := s.listRecentTunnelCreations(ctx, 10, tunnelID)
	if err != nil {
		return TunnelDetailResponse{}, err
	}

	return TunnelDetailResponse{
		Tunnel:          tunnel,
		StatusChart:     statusChart,
		TrafficChart:    trafficChart,
		Logs:            logs,
		CreationHistory: creationHistory,
	}, nil
}

func (s *Store) dashboardSummary(ctx context.Context) (DashboardSummary, error) {
	var summary DashboardSummary
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN state = 'active' AND deleted_at IS NULL THEN 1 ELSE 0 END), 0) AS active_tunnels,
			COALESCE(SUM(CASE WHEN state IN ('pending', 'active') AND deleted_at IS NULL THEN 1 ELSE 0 END), 0) AS registered_tunnels,
			COALESCE((SELECT COUNT(1) FROM request_response_logs), 0) AS total_requests,
			COALESCE((SELECT SUM(request_bytes + response_bytes) FROM request_response_logs), 0) AS transferred_bytes
		FROM tunnels
	`).Scan(&summary.ActiveTunnels, &summary.RegisteredTunnels, &summary.TotalRequests, &summary.DataTransferredBytes)
	if err != nil {
		return DashboardSummary{}, fmt.Errorf("loading dashboard summary failed: %w", err)
	}
	return summary, nil
}

func (s *Store) listLatestActiveTunnels(ctx context.Context, limit int) ([]TunnelRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, domain, requested_subdomain, state, created_at, connected_at, disconnected_at, last_activity_at,
		       total_request_bytes, total_response_bytes, request_count, remote_addr, user_agent, deleted_at
		FROM tunnels
		WHERE deleted_at IS NULL AND state IN ('pending', 'active')
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing latest active tunnels failed: %w", err)
	}
	defer rows.Close()
	return collectTunnelRecords(rows)
}

func (s *Store) listRecentRequests(ctx context.Context, limit int) ([]RequestResponseLog, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tunnel_id, domain, method, path, request_headers_json, response_headers_json, request_preview,
		       response_preview, request_content_type, response_content_type, request_bytes, response_bytes, status_code,
		       started_at, completed_at, duration_ms, error_message
		FROM request_response_logs
		ORDER BY started_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing recent requests failed: %w", err)
	}
	defer rows.Close()
	return collectRequestLogs(rows)
}

func (s *Store) listRecentTunnelCreations(ctx context.Context, limit int, tunnelID string) ([]TunnelCreationLog, error) {
	query := `
		SELECT id, tunnel_id, domain, requested_subdomain, remote_addr, user_agent, success, error_message, created_at
		FROM tunnel_creation_logs
	`
	args := []any{}
	if strings.TrimSpace(tunnelID) != "" {
		query += ` WHERE tunnel_id = ?`
		args = append(args, tunnelID)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing tunnel creation logs failed: %w", err)
	}
	defer rows.Close()

	items := make([]TunnelCreationLog, 0, limit)
	for rows.Next() {
		var item TunnelCreationLog
		var tunnelIDValue, domain, requestedSubdomain, remoteAddr, userAgent, errorMessage sql.NullString
		var success int
		if err := rows.Scan(&item.ID, &tunnelIDValue, &domain, &requestedSubdomain, &remoteAddr, &userAgent, &success, &errorMessage, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning tunnel creation log failed: %w", err)
		}
		item.TunnelID = tunnelIDValue.String
		item.Domain = domain.String
		item.RequestedSubdomain = requestedSubdomain.String
		item.RemoteAddr = remoteAddr.String
		item.UserAgent = userAgent.String
		item.ErrorMessage = errorMessage.String
		item.Success = success == 1
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tunnel creation logs failed: %w", err)
	}
	return items, nil
}

func (s *Store) statusChart(ctx context.Context, tunnelID string, days int) ([]StatusChartPoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT substr(CAST(started_at AS TEXT), 1, 10) AS bucket,
		       SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) AS two_xx,
		       SUM(CASE WHEN status_code BETWEEN 300 AND 399 THEN 1 ELSE 0 END) AS three_xx,
		       SUM(CASE WHEN status_code BETWEEN 400 AND 499 THEN 1 ELSE 0 END) AS four_xx,
		       SUM(CASE WHEN status_code BETWEEN 500 AND 599 THEN 1 ELSE 0 END) AS five_xx
		FROM request_response_logs
		WHERE tunnel_id = ? AND started_at >= ?
		GROUP BY bucket
		ORDER BY bucket ASC
	`, tunnelID, time.Now().UTC().AddDate(0, 0, -(days-1)))
	if err != nil {
		return nil, fmt.Errorf("loading status chart failed: %w", err)
	}
	defer rows.Close()

	points := make([]StatusChartPoint, 0, days)
	for rows.Next() {
		var point StatusChartPoint
		var bucket sql.NullString
		if err := rows.Scan(&bucket, &point.TwoXX, &point.ThreeXX, &point.FourXX, &point.FiveXX); err != nil {
			return nil, fmt.Errorf("scanning status chart failed: %w", err)
		}
		if !bucket.Valid || strings.TrimSpace(bucket.String) == "" {
			continue
		}
		point.Bucket = bucket.String
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating status chart failed: %w", err)
	}
	return ensureStatusChartBuckets(points, days), nil
}

func (s *Store) trafficChart(ctx context.Context, tunnelID string, days int) ([]TrafficChartPoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT substr(CAST(started_at AS TEXT), 1, 10) AS bucket,
		       COALESCE(SUM(request_bytes), 0) AS inbound_bytes,
		       COALESCE(SUM(response_bytes), 0) AS outbound_bytes
		FROM request_response_logs
		WHERE tunnel_id = ? AND started_at >= ?
		GROUP BY bucket
		ORDER BY bucket ASC
	`, tunnelID, time.Now().UTC().AddDate(0, 0, -(days-1)))
	if err != nil {
		return nil, fmt.Errorf("loading traffic chart failed: %w", err)
	}
	defer rows.Close()

	points := make([]TrafficChartPoint, 0, days)
	for rows.Next() {
		var point TrafficChartPoint
		var bucket sql.NullString
		if err := rows.Scan(&bucket, &point.InboundBytes, &point.OutboundBytes); err != nil {
			return nil, fmt.Errorf("scanning traffic chart failed: %w", err)
		}
		if !bucket.Valid || strings.TrimSpace(bucket.String) == "" {
			continue
		}
		point.Bucket = bucket.String
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating traffic chart failed: %w", err)
	}
	return ensureTrafficChartBuckets(points, days), nil
}

func buildRequestLogFilterClause(filters RequestLogFilters) (string, []any) {
	conditions := make([]string, 0, 4)
	args := make([]any, 0, 6)

	if search := strings.TrimSpace(filters.Search); search != "" {
		like := "%" + search + "%"
		conditions = append(conditions, `(id LIKE ? OR domain LIKE ? OR path LIKE ?)`)
		args = append(args, like, like, like)
	}

	if subdomain := strings.ToLower(strings.TrimSpace(filters.Subdomain)); subdomain != "" {
		conditions = append(conditions, `domain LIKE ?`)
		args = append(args, subdomain+".%")
	}

	if method := strings.ToUpper(strings.TrimSpace(filters.Method)); method != "" {
		conditions = append(conditions, `method = ?`)
		args = append(args, method)
	}

	switch strings.ToUpper(strings.TrimSpace(filters.StatusClass)) {
	case "2XX":
		conditions = append(conditions, `status_code BETWEEN 200 AND 299`)
	case "3XX":
		conditions = append(conditions, `status_code BETWEEN 300 AND 399`)
	case "4XX":
		conditions = append(conditions, `status_code BETWEEN 400 AND 499`)
	case "5XX":
		conditions = append(conditions, `status_code BETWEEN 500 AND 599`)
	}

	if len(conditions) == 0 {
		return "", args
	}

	return " WHERE " + strings.Join(conditions, " AND "), args
}

func scanTunnelRecord(scanner interface{ Scan(dest ...any) error }) (TunnelRecord, error) {
	var record TunnelRecord
	var connectedAt, disconnectedAt, lastActivityAt, deletedAt sql.NullTime
	var requestedSubdomain, remoteAddr, userAgent sql.NullString
	if err := scanner.Scan(
		&record.ID,
		&record.Domain,
		&requestedSubdomain,
		&record.State,
		&record.CreatedAt,
		&connectedAt,
		&disconnectedAt,
		&lastActivityAt,
		&record.TotalRequestBytes,
		&record.TotalResponseBytes,
		&record.RequestCount,
		&remoteAddr,
		&userAgent,
		&deletedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TunnelRecord{}, sql.ErrNoRows
		}
		return TunnelRecord{}, fmt.Errorf("scanning tunnel failed: %w", err)
	}
	if requestedSubdomain.Valid {
		record.RequestedSubdomain = requestedSubdomain.String
	}
	if connectedAt.Valid {
		record.ConnectedAt = &connectedAt.Time
	}
	if disconnectedAt.Valid {
		record.DisconnectedAt = &disconnectedAt.Time
	}
	if lastActivityAt.Valid {
		record.LastActivityAt = &lastActivityAt.Time
	}
	if remoteAddr.Valid {
		record.RemoteAddr = remoteAddr.String
	}
	if userAgent.Valid {
		record.UserAgent = userAgent.String
	}
	if deletedAt.Valid {
		record.DeletedAt = &deletedAt.Time
	}
	return record, nil
}

func collectTunnelRecords(rows *sql.Rows) ([]TunnelRecord, error) {
	items := make([]TunnelRecord, 0)
	for rows.Next() {
		record, err := scanTunnelRecord(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tunnels failed: %w", err)
	}
	return items, nil
}

func collectRequestLogs(rows *sql.Rows) ([]RequestResponseLog, error) {
	items := make([]RequestResponseLog, 0)
	for rows.Next() {
		item, err := scanRequestLog(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating request logs failed: %w", err)
	}
	return items, nil
}

func scanRequestLog(scanner interface{ Scan(dest ...any) error }) (RequestResponseLog, error) {
	var item RequestResponseLog
	var requestHeadersJSON, responseHeadersJSON sql.NullString
	var requestPreview, responsePreview, requestContentType, responseContentType, errorMessage sql.NullString
	var completedAt sql.NullTime
	if err := scanner.Scan(
		&item.ID,
		&item.TunnelID,
		&item.Domain,
		&item.Method,
		&item.Path,
		&requestHeadersJSON,
		&responseHeadersJSON,
		&requestPreview,
		&responsePreview,
		&requestContentType,
		&responseContentType,
		&item.RequestBytes,
		&item.ResponseBytes,
		&item.StatusCode,
		&item.StartedAt,
		&completedAt,
		&item.DurationMs,
		&errorMessage,
	); err != nil {
		return RequestResponseLog{}, fmt.Errorf("scanning request log failed: %w", err)
	}
	if requestHeadersJSON.Valid {
		item.RequestHeaders = unmarshalHeaders(requestHeadersJSON.String)
	}
	if responseHeadersJSON.Valid {
		item.ResponseHeaders = unmarshalHeaders(responseHeadersJSON.String)
	}
	if requestPreview.Valid {
		item.RequestPreview = requestPreview.String
	}
	if responsePreview.Valid {
		item.ResponsePreview = responsePreview.String
	}
	if requestContentType.Valid {
		item.RequestContentType = requestContentType.String
	}
	if responseContentType.Valid {
		item.ResponseContentType = responseContentType.String
	}
	if completedAt.Valid {
		item.CompletedAt = &completedAt.Time
	}
	if errorMessage.Valid {
		item.ErrorMessage = errorMessage.String
	}
	return item, nil
}

func marshalHeaders(headers map[string][]string) (string, error) {
	if len(headers) == 0 {
		return "", nil
	}
	payload, err := json.Marshal(headers)
	if err != nil {
		return "", fmt.Errorf("marshalling headers failed: %w", err)
	}
	return string(payload), nil
}

func unmarshalHeaders(payload string) map[string][]string {
	if strings.TrimSpace(payload) == "" {
		return nil
	}
	var headers map[string][]string
	if err := json.Unmarshal([]byte(payload), &headers); err != nil {
		return nil
	}
	return headers
}

func generateTunnelID() string {
	return protocol.GenerateID(16)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableTimeValue(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func ensureStatusChartBuckets(points []StatusChartPoint, days int) []StatusChartPoint {
	lookup := make(map[string]StatusChartPoint, len(points))
	for _, point := range points {
		lookup[point.Bucket] = point
	}
	filled := make([]StatusChartPoint, 0, days)
	for i := days - 1; i >= 0; i-- {
		bucket := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		point, ok := lookup[bucket]
		if !ok {
			point = StatusChartPoint{Bucket: bucket}
		}
		filled = append(filled, point)
	}
	return filled
}

func ensureTrafficChartBuckets(points []TrafficChartPoint, days int) []TrafficChartPoint {
	lookup := make(map[string]TrafficChartPoint, len(points))
	for _, point := range points {
		lookup[point.Bucket] = point
	}
	filled := make([]TrafficChartPoint, 0, days)
	for i := days - 1; i >= 0; i-- {
		bucket := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		point, ok := lookup[bucket]
		if !ok {
			point = TrafficChartPoint{Bucket: bucket}
		}
		filled = append(filled, point)
	}
	return filled
}

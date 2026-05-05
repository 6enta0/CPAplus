package usage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

const createTableSQL = `
CREATE TABLE IF NOT EXISTS usage_records (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	api_key          TEXT    NOT NULL DEFAULT '',
	model            TEXT    NOT NULL DEFAULT '',
	source           TEXT    NOT NULL DEFAULT '',
	auth_index       TEXT    NOT NULL DEFAULT '',
	timestamp        TEXT    NOT NULL,
	latency_ms       INTEGER NOT NULL DEFAULT 0,
	failed           INTEGER NOT NULL DEFAULT 0,
	input_tokens     INTEGER NOT NULL DEFAULT 0,
	output_tokens    INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	cached_tokens    INTEGER NOT NULL DEFAULT 0,
	total_tokens     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_usage_records_timestamp ON usage_records(timestamp);
CREATE INDEX IF NOT EXISTS idx_usage_records_api_key  ON usage_records(api_key);
CREATE INDEX IF NOT EXISTS idx_usage_records_model     ON usage_records(model);
`

type SQLiteStore struct {
	mu sync.Mutex
	db *sql.DB
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dbPath == "" {
		return nil, nil
	}

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create usage db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open usage db: %w", err)
	}

	db.SetMaxOpenConns(1)

	if _, err = db.Exec(createTableSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to init usage db schema: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
	}
	for _, p := range pragmas {
		if _, err = db.Exec(p); err != nil {
			log.WithError(err).Warnf("usage db: %s failed", p)
		}
	}

	log.Infof("usage statistics database opened: %s", dbPath)
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) InsertRecord(record coreusage.Record) {
	if s == nil || s.db == nil {
		return
	}

	timestamp := record.RequestedAt
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	apiKey := record.APIKey
	if apiKey == "" {
		apiKey = record.Provider
	}
	if apiKey == "" {
		apiKey = "unknown"
	}
	model := record.Model
	if model == "" {
		model = "unknown"
	}

	failed := 0
	if record.Failed {
		failed = 1
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`INSERT INTO usage_records (api_key, model, source, auth_index, timestamp, latency_ms, failed, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		apiKey,
		model,
		record.Source,
		record.AuthIndex,
		timestamp.UTC().Format(time.RFC3339Nano),
		normaliseLatency(record.Latency),
		failed,
		record.Detail.InputTokens,
		record.Detail.OutputTokens,
		record.Detail.ReasoningTokens,
		record.Detail.CachedTokens,
		record.Detail.TotalTokens,
	)
	if err != nil {
		log.WithError(err).Warn("usage db: failed to insert record")
	}
}

func (s *SQLiteStore) LoadAll() ([]loadedRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}

	rows, err := s.db.Query(
		`SELECT api_key, model, source, auth_index, timestamp, latency_ms, failed, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens FROM usage_records ORDER BY id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("usage db: query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []loadedRecord
	for rows.Next() {
		var r loadedRecord
		var timestampStr string
		var failed int
		if err := rows.Scan(
			&r.APIKey, &r.Model, &r.Source, &r.AuthIndex,
			&timestampStr, &r.LatencyMs, &failed,
			&r.InputTokens, &r.OutputTokens, &r.ReasoningTokens, &r.CachedTokens, &r.TotalTokens,
		); err != nil {
			return nil, fmt.Errorf("usage db: scan failed: %w", err)
		}
		r.Failed = failed != 0
		r.Timestamp, _ = time.Parse(time.RFC3339Nano, timestampStr)
		if r.Timestamp.IsZero() {
			r.Timestamp, _ = time.Parse(time.RFC3339, timestampStr)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

type loadedRecord struct {
	APIKey          string
	Model           string
	Source          string
	AuthIndex       string
	Timestamp       time.Time
	LatencyMs       int64
	Failed          bool
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	CachedTokens    int64
	TotalTokens     int64
}

func (r loadedRecord) ToRequestDetail() RequestDetail {
	tokens := TokenStats{
		InputTokens:     r.InputTokens,
		OutputTokens:    r.OutputTokens,
		ReasoningTokens: r.ReasoningTokens,
		CachedTokens:    r.CachedTokens,
		TotalTokens:     r.TotalTokens,
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = r.InputTokens + r.OutputTokens + r.ReasoningTokens
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = r.InputTokens + r.OutputTokens + r.ReasoningTokens + r.CachedTokens
	}
	return RequestDetail{
		Timestamp: r.Timestamp,
		LatencyMs: r.LatencyMs,
		Source:    r.Source,
		AuthIndex: r.AuthIndex,
		Tokens:    tokens,
		Failed:    r.Failed,
	}
}

func LoadFromSQLite(stats *RequestStatistics, store *SQLiteStore) error {
	if stats == nil || store == nil {
		return nil
	}
	records, err := store.LoadAll()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}

	log.Infof("usage db: loading %d historical records into memory", len(records))

	stats.mu.Lock()
	defer stats.mu.Unlock()

	for _, r := range records {
		detail := r.ToRequestDetail()
		totalTokens := detail.Tokens.TotalTokens
		if totalTokens < 0 {
			totalTokens = 0
		}

		stats.totalRequests++
		if detail.Failed {
			stats.failureCount++
		} else {
			stats.successCount++
		}
		stats.totalTokens += totalTokens

		apiKey := r.APIKey
		if apiKey == "" {
			apiKey = "unknown"
		}
		st, ok := stats.apis[apiKey]
		if !ok {
			st = &apiStats{Models: make(map[string]*modelStats)}
			stats.apis[apiKey] = st
		}
		modelName := r.Model
		if modelName == "" {
			modelName = "unknown"
		}
		stats.updateAPIStats(st, modelName, detail)

		dayKey := detail.Timestamp.Format("2006-01-02")
		hourKey := detail.Timestamp.Hour()
		stats.requestsByDay[dayKey]++
		stats.requestsByHour[hourKey]++
		stats.tokensByDay[dayKey] += totalTokens
		stats.tokensByHour[hourKey] += totalTokens
	}

	log.Infof("usage db: historical records loaded (%d total requests, %d total tokens)", stats.totalRequests, stats.totalTokens)
	return nil
}

func DefaultDBPath(wd string) string {
	return filepath.Join(wd, "usage.db")
}

func ResolveDBPath(cfgPath string) string {
	if cfgPath == "" {
		return ""
	}
	dir := filepath.Dir(cfgPath)
	return filepath.Join(dir, "usage.db")
}

func (s *SQLiteStore) DeleteOlderThan(before time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := before.UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`DELETE FROM usage_records WHERE timestamp < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("usage db: delete failed: %w", err)
	}
	affected, _ := res.RowsAffected()
	return affected, nil
}

func (s *SQLiteStore) ExportSnapshot() (StatisticsSnapshot, error) {
	if s == nil || s.db == nil {
		return StatisticsSnapshot{}, nil
	}

	records, err := s.LoadAll()
	if err != nil {
		return StatisticsSnapshot{}, err
	}

	stats := NewRequestStatistics()
	for _, r := range records {
		detail := r.ToRequestDetail()
		totalTokens := detail.Tokens.TotalTokens
		if totalTokens < 0 {
			totalTokens = 0
		}

		stats.totalRequests++
		if detail.Failed {
			stats.failureCount++
		} else {
			stats.successCount++
		}
		stats.totalTokens += totalTokens

		apiKey := strings.TrimSpace(r.APIKey)
		if apiKey == "" {
			apiKey = "unknown"
		}
		st, ok := stats.apis[apiKey]
		if !ok {
			st = &apiStats{Models: make(map[string]*modelStats)}
			stats.apis[apiKey] = st
		}
		modelName := strings.TrimSpace(r.Model)
		if modelName == "" {
			modelName = "unknown"
		}
		stats.updateAPIStats(st, modelName, detail)

		dayKey := detail.Timestamp.Format("2006-01-02")
		hourKey := detail.Timestamp.Hour()
		stats.requestsByDay[dayKey]++
		stats.requestsByHour[hourKey]++
		stats.tokensByDay[dayKey] += totalTokens
		stats.tokensByHour[hourKey] += totalTokens
	}

	return stats.Snapshot(), nil
}

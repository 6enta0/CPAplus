package usage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/pricing"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

const usageSchemaVersion = 5

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
	status_code      INTEGER NOT NULL DEFAULT 0,
	error_message    TEXT    NOT NULL DEFAULT '',
	input_tokens     INTEGER NOT NULL DEFAULT 0,
	output_tokens    INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	cached_tokens    INTEGER NOT NULL DEFAULT 0,
	total_tokens     INTEGER NOT NULL DEFAULT 0,
	cost_usd         REAL    NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_usage_records_timestamp ON usage_records(timestamp);
CREATE INDEX IF NOT EXISTS idx_usage_records_api_key  ON usage_records(api_key);
CREATE INDEX IF NOT EXISTS idx_usage_records_model     ON usage_records(model);
CREATE TABLE IF NOT EXISTS auth_last_used (
	auth_index    TEXT PRIMARY KEY,
	last_called_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS custom_model_prices (
	model            TEXT PRIMARY KEY,
	prompt_price     REAL NOT NULL DEFAULT 0,
	completion_price REAL NOT NULL DEFAULT 0,
	cache_price      REAL NOT NULL DEFAULT 0,
	cache_write_price REAL NOT NULL DEFAULT 0
);
`

type SQLiteStore struct {
	mu           sync.Mutex
	db           *sql.DB
	pricingStore *pricing.Store
}

func (s *SQLiteStore) SetPricingStore(ps *pricing.Store) {
	s.pricingStore = ps
}

func stableUsageHash(seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:8])
}

func stableUsageToken(kind string, parts ...string) string {
	hasher := sha256.New()
	hasher.Write([]byte(kind))
	for _, part := range parts {
		hasher.Write([]byte{0})
		hasher.Write([]byte(strings.TrimSpace(part)))
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if len(digest) < 12 {
		return fmt.Sprintf("%012s", digest)
	}
	return digest[:12]
}

type stableCounterTokenGenerator struct {
	counters map[string]int
}

func (g *stableCounterTokenGenerator) next(kind string, parts ...string) string {
	if g == nil {
		return stableUsageToken(kind, parts...)
	}
	if g.counters == nil {
		g.counters = make(map[string]int)
	}
	short := stableUsageToken(kind, parts...)
	key := kind + ":" + short
	index := g.counters[key]
	g.counters[key] = index + 1
	if index > 0 {
		return fmt.Sprintf("%s-%d", short, index)
	}
	return short
}

func openAICompatIdentityProviderKey(name, baseURL string) string {
	providerName := strings.ToLower(strings.TrimSpace(name))
	if providerName == "" {
		providerName = "openai-compatibility"
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return providerName
	}
	return providerName + "|base=" + baseURL
}

func openAICompatProviderKey(name, prefix, baseURL string) string {
	providerName := strings.ToLower(strings.TrimSpace(name))
	if providerName == "" {
		providerName = "openai-compatibility"
	}
	prefix = strings.TrimSpace(prefix)
	baseURL = strings.TrimSpace(baseURL)
	parts := []string{providerName}
	if prefix != "" {
		parts = append(parts, "prefix="+prefix)
	}
	if baseURL != "" {
		parts = append(parts, "base="+baseURL)
	}
	return strings.Join(parts, "|")
}

func buildOpenAICompatAuthIndex(providerKey, compatName, baseURL, apiKey, proxyURL, source, prefix string, includePrefix bool) string {
	providerKey = strings.ToLower(strings.TrimSpace(providerKey))
	compatName = strings.ToLower(strings.TrimSpace(compatName))
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	proxyURL = strings.TrimSpace(proxyURL)
	source = strings.TrimSpace(source)
	prefix = strings.TrimSpace(prefix)
	if providerKey == "" || baseURL == "" || apiKey == "" {
		return ""
	}
	parts := []string{"provider=" + providerKey}
	if compatName != "" {
		parts = append(parts, "compat="+compatName)
	}
	parts = append(parts, "base="+baseURL)
	if proxyURL != "" {
		parts = append(parts, "proxy="+proxyURL)
	}
	parts = append(parts, "api_key="+apiKey)
	if source != "" {
		parts = append(parts, "source="+source)
	}
	if includePrefix && prefix != "" {
		parts = append(parts, "prefix="+prefix)
	}
	return stableUsageHash("config:" + strings.Join(parts, "\x00"))
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
	if err = migrateUsageRecordOutcomeColumns(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to migrate usage db schema: %w", err)
	}
	if err = setSchemaVersion(db, usageSchemaVersion); err != nil {
		log.WithError(err).Warn("usage db: failed to persist schema version")
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

	var costUSD float64
	if s.pricingStore != nil && model != "unknown" && !record.Failed {
		if prices, ok := s.pricingStore.Lookup(model); ok {
			costUSD = pricing.CalcCost(record.Detail.InputTokens, record.Detail.OutputTokens, record.Detail.CachedTokens, prices)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	statusCode := record.StatusCode
	if statusCode < 0 {
		statusCode = 0
	}
	errorMessage := strings.TrimSpace(record.ErrorMessage)
	if !record.Failed || (statusCode > 0 && statusCode < 400) {
		errorMessage = ""
	}

	_, err := s.db.Exec(
		`INSERT INTO usage_records (api_key, model, source, auth_index, timestamp, latency_ms, failed, status_code, error_message, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, cost_usd)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		apiKey,
		model,
		record.Source,
		record.AuthIndex,
		timestamp.UTC().Format(time.RFC3339Nano),
		normaliseLatency(record.Latency),
		failed,
		statusCode,
		errorMessage,
		record.Detail.InputTokens,
		record.Detail.OutputTokens,
		record.Detail.ReasoningTokens,
		record.Detail.CachedTokens,
		record.Detail.TotalTokens,
		costUSD,
	)
	if err != nil {
		log.WithError(err).Warn("usage db: failed to insert record")
	}

	if authIdx := record.AuthIndex; authIdx != "" {
		_, _ = s.db.Exec(
			`INSERT INTO auth_last_used (auth_index, last_called_at) VALUES (?, ?)
			 ON CONFLICT(auth_index) DO UPDATE SET last_called_at = excluded.last_called_at`,
			authIdx, timestamp.UTC().Format(time.RFC3339),
		)
	}
}

func (s *SQLiteStore) GetLastCalledAt() (map[string]string, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT auth_index, last_called_at FROM auth_last_used`)
	if err != nil {
		return nil, fmt.Errorf("usage db: query auth_last_used failed: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]string)
	for rows.Next() {
		var authIdx, lastCalled string
		if err := rows.Scan(&authIdx, &lastCalled); err != nil {
			return nil, fmt.Errorf("usage db: scan auth_last_used failed: %w", err)
		}
		result[authIdx] = lastCalled
	}
	return result, rows.Err()
}

func (s *SQLiteStore) GetCostByAuthIndex() (map[string]float64, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT auth_index, SUM(cost_usd) FROM usage_records WHERE failed = 0 GROUP BY auth_index`)
	if err != nil {
		return nil, fmt.Errorf("usage db: query cost by auth_index failed: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]float64)
	for rows.Next() {
		var authIdx string
		var cost float64
		if err := rows.Scan(&authIdx, &cost); err != nil {
			return nil, fmt.Errorf("usage db: scan cost by auth_index failed: %w", err)
		}
		if cost > 0 {
			result[authIdx] = cost
		}
	}
	return result, rows.Err()
}

func (s *SQLiteStore) BackfillCosts() {
	if s == nil || s.db == nil || s.pricingStore == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(
		`SELECT id, model, input_tokens, output_tokens, cached_tokens FROM usage_records WHERE cost_usd = 0 AND failed = 0`,
	)
	if err != nil {
		log.WithError(err).Warn("usage db: backfill cost query failed")
		return
	}
	defer func() { _ = rows.Close() }()

	type costUpdate struct {
		id      int64
		costUSD float64
	}
	var updates []costUpdate
	for rows.Next() {
		var id int64
		var model string
		var inputTokens, outputTokens, cachedTokens int64
		if err := rows.Scan(&id, &model, &inputTokens, &outputTokens, &cachedTokens); err != nil {
			continue
		}
		if model == "" || model == "unknown" {
			continue
		}
		prices, ok := s.pricingStore.Lookup(model)
		if !ok {
			continue
		}
		cost := pricing.CalcCost(inputTokens, outputTokens, cachedTokens, prices)
		if cost > 0 {
			updates = append(updates, costUpdate{id: id, costUSD: cost})
		}
	}
	if err := rows.Err(); err != nil {
		log.WithError(err).Warn("usage db: backfill cost rows iteration failed")
		return
	}
	if len(updates) == 0 {
		return
	}

	stmt, err := s.db.Prepare(`UPDATE usage_records SET cost_usd = ? WHERE id = ?`)
	if err != nil {
		log.WithError(err).Warn("usage db: backfill cost prepare failed")
		return
	}
	defer func() { _ = stmt.Close() }()

	for _, u := range updates {
		if _, err := stmt.Exec(u.costUSD, u.id); err != nil {
			log.WithError(err).WithField("id", u.id).Warn("usage db: backfill cost update failed")
		}
	}
	log.Infof("usage db: backfilled cost for %d records", len(updates))
}

func (s *SQLiteStore) SaveCustomPrices(prices map[string][4]float64) error {
	if s == nil || s.db == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("usage db: begin save custom prices failed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM custom_model_prices`); err != nil {
		return fmt.Errorf("usage db: clear custom prices failed: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO custom_model_prices (model, prompt_price, completion_price, cache_price, cache_write_price) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("usage db: prepare custom prices insert failed: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for model, price := range prices {
		if _, err := stmt.Exec(model, price[0], price[1], price[2], price[3]); err != nil {
			return fmt.Errorf("usage db: insert custom price for %s failed: %w", model, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("usage db: commit custom prices failed: %w", err)
	}
	return nil
}

func (s *SQLiteStore) LoadCustomPrices() (map[string][4]float64, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT model, prompt_price, completion_price, cache_price, cache_write_price FROM custom_model_prices`)
	if err != nil {
		return nil, fmt.Errorf("usage db: query custom prices failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	prices := make(map[string][4]float64)
	for rows.Next() {
		var model string
		var prompt, completion, cache, cacheWrite float64
		if err := rows.Scan(&model, &prompt, &completion, &cache, &cacheWrite); err != nil {
			return nil, fmt.Errorf("usage db: scan custom prices failed: %w", err)
		}
		prices[model] = [4]float64{prompt, completion, cache, cacheWrite}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usage db: iterate custom prices failed: %w", err)
	}
	return prices, nil
}

func (s *SQLiteStore) MigrateLegacyOpenAICompatAuthIndexes(cfg *config.Config) {
	if s == nil || s.db == nil || cfg == nil {
		return
	}

	type remap struct {
		from string
		to   string
	}
	var remaps []remap
	oldTokenGen := &stableCounterTokenGenerator{}
	for i := range cfg.OpenAICompatibility {
		compat := cfg.OpenAICompatibility[i]
		if strings.TrimSpace(compat.Prefix) == "" {
			continue
		}
		oldProviderKey := openAICompatProviderKey(compat.Name, compat.Prefix, compat.BaseURL)
		newProviderKey := openAICompatIdentityProviderKey(compat.Name, compat.BaseURL)
		for j := range compat.APIKeyEntries {
			entry := compat.APIKeyEntries[j]
			oldKind := fmt.Sprintf("openai-compatibility:%s", oldProviderKey)
			oldToken := oldTokenGen.next(oldKind, entry.APIKey, entry.ProxyURL)
			oldSource := fmt.Sprintf("config:%s[%s]", oldProviderKey, oldToken)
			newKind := fmt.Sprintf("openai-compatibility:%s", newProviderKey)
			newToken := stableUsageToken(newKind, entry.APIKey, entry.ProxyURL)
			newSource := fmt.Sprintf("config:%s[%s]", newProviderKey, newToken)

			oldIndex := buildOpenAICompatAuthIndex(oldProviderKey, compat.Name, compat.BaseURL, entry.APIKey, entry.ProxyURL, oldSource, compat.Prefix, true)
			newIndex := buildOpenAICompatAuthIndex(newProviderKey, compat.Name, compat.BaseURL, entry.APIKey, entry.ProxyURL, newSource, "", false)
			if oldIndex != "" && newIndex != "" && oldIndex != newIndex {
				remaps = append(remaps, remap{from: oldIndex, to: newIndex})
			}
		}
	}
	if len(remaps) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	migrated := 0
	for _, remap := range remaps {
		res, err := s.db.Exec(`UPDATE usage_records SET auth_index = ? WHERE auth_index = ?`, remap.to, remap.from)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{"from": remap.from, "to": remap.to}).Warn("usage db: auth_index migration failed")
			continue
		}
		affected, err := res.RowsAffected()
		if err == nil && affected > 0 {
			migrated += int(affected)
		}
		if _, errLastUsed := s.db.Exec(`UPDATE OR IGNORE auth_last_used SET auth_index = ? WHERE auth_index = ?`, remap.to, remap.from); errLastUsed != nil {
			log.WithError(errLastUsed).WithFields(log.Fields{"from": remap.from, "to": remap.to}).Warn("usage db: auth_last_used migration failed")
		}
	}
	if migrated > 0 {
		log.Infof("usage db: migrated %d usage records to prefix-free openai-compatible auth indexes", migrated)
	}
}

func (s *SQLiteStore) LoadAll() ([]loadedRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}

	rows, err := s.db.Query(
		`SELECT api_key, model, source, auth_index, timestamp, latency_ms, failed, status_code, error_message, input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens, cost_usd FROM usage_records ORDER BY id ASC`,
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
			&timestampStr, &r.LatencyMs, &failed, &r.StatusCode, &r.ErrorMessage,
			&r.InputTokens, &r.OutputTokens, &r.ReasoningTokens, &r.CachedTokens, &r.TotalTokens,
			&r.CostUSD,
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
	StatusCode      int
	ErrorMessage    string
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	CachedTokens    int64
	TotalTokens     int64
	CostUSD         float64
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
		Timestamp:    r.Timestamp,
		LatencyMs:    r.LatencyMs,
		Source:       r.Source,
		AuthIndex:    r.AuthIndex,
		Tokens:       tokens,
		Failed:       r.Failed,
		StatusCode:   r.StatusCode,
		ErrorMessage: r.ErrorMessage,
		CostUSD:      r.CostUSD,
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

func migrateUsageRecordOutcomeColumns(db *sql.DB) error {
	if db == nil {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin usage outcome migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`PRAGMA table_info(usage_records)`)
	if err != nil {
		return fmt.Errorf("inspect usage_records columns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			columnID   int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err = rows.Scan(&columnID, &name, &columnType, &notNull, &defaultVal, &primaryKey); err != nil {
			return fmt.Errorf("scan usage_records column: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate usage_records columns: %w", err)
	}
	if err = rows.Close(); err != nil {
		return fmt.Errorf("close usage_records columns: %w", err)
	}

	if _, ok := columns["status_code"]; !ok {
		if _, err = tx.Exec(`ALTER TABLE usage_records ADD COLUMN status_code INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add status_code column: %w", err)
		}
	}
	if _, ok := columns["error_message"]; !ok {
		if _, err = tx.Exec(`ALTER TABLE usage_records ADD COLUMN error_message TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add error_message column: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit usage outcome migration: %w", err)
	}
	return nil
}

func setSchemaVersion(db *sql.DB, version int) error {
	_, err := db.Exec("CREATE TABLE IF NOT EXISTS usage_schema_meta (version INTEGER NOT NULL)")
	if err != nil {
		return err
	}
	_, err = db.Exec("DELETE FROM usage_schema_meta")
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO usage_schema_meta (version) VALUES (?)", version)
	return err
}

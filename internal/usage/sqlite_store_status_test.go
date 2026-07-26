package usage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"

	_ "modernc.org/sqlite"
)

func TestNewSQLiteStoreMigratesOutcomeColumnsWithoutLosingRecords(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "usage.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE usage_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			api_key TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			auth_index TEXT NOT NULL DEFAULT '',
			timestamp TEXT NOT NULL,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			failed INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			reasoning_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			cost_usd REAL NOT NULL DEFAULT 0
		);
		CREATE TABLE usage_schema_meta (version INTEGER NOT NULL);
		INSERT INTO usage_schema_meta (version) VALUES (4);
		INSERT INTO usage_records (api_key, model, timestamp, failed, total_tokens)
		VALUES ('legacy-key', 'legacy-model', '2026-07-24T00:00:00Z', 1, 42);`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create legacy usage db: %v", err)
	}
	if err = db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("migrate usage db: %v", err)
	}
	defer func() {
		if errClose := store.Close(); errClose != nil {
			t.Errorf("close store: %v", errClose)
		}
	}()

	records, err := store.LoadAll()
	if err != nil {
		t.Fatalf("load migrated records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].APIKey != "legacy-key" || records[0].TotalTokens != 42 {
		t.Fatalf("legacy record = %+v, want preserved values", records[0])
	}
	if records[0].StatusCode != 0 || records[0].ErrorMessage != "" {
		t.Fatalf("legacy outcome = (%d, %q), want unknown/empty", records[0].StatusCode, records[0].ErrorMessage)
	}

	store.InsertRecord(coreusage.Record{
		APIKey:       "new-key",
		Model:        "new-model",
		RequestedAt:  time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC),
		Failed:       true,
		StatusCode:   429,
		ErrorMessage: "rate limited",
		Detail:       coreusage.Detail{TotalTokens: 7},
	})
	records, err = store.LoadAll()
	if err != nil {
		t.Fatalf("load inserted record: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records after insert = %d, want 2", len(records))
	}
	if records[1].StatusCode != 429 || records[1].ErrorMessage != "rate limited" {
		t.Fatalf("new outcome = (%d, %q), want (429, rate limited)", records[1].StatusCode, records[1].ErrorMessage)
	}
}

// TestDeleteOlderThanUsesNanoPrecisionBoundary ensures retention cutoff strings
// match InsertRecord's fixed-width layout. Second-only RFC3339 cutoffs made a
// same-second fractional record after the wall-clock cutoff compare as smaller
// ('.' < 'Z') and get incorrectly deleted.
func TestDeleteOlderThanUsesNanoPrecisionBoundary(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatalf("open usage db: %v", err)
	}
	defer func() {
		if errClose := store.Close(); errClose != nil {
			t.Errorf("close store: %v", errClose)
		}
	}()

	// Exact-second cutoff is the sharp edge: trimmed RFC3339/RFC3339Nano both
	// emit "...:00Z", which sorts after any "...:00.<frac>Z".
	cutoff := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	keepAt := cutoff.Add(500 * time.Millisecond)
	dropAt := cutoff.Add(-500 * time.Millisecond)

	store.InsertRecord(coreusage.Record{
		APIKey:      "api-a",
		Model:       "keep-model",
		RequestedAt: keepAt,
		Detail:      coreusage.Detail{TotalTokens: 11},
	})
	store.InsertRecord(coreusage.Record{
		APIKey:      "api-a",
		Model:       "drop-model",
		RequestedAt: dropAt,
		Detail:      coreusage.Detail{TotalTokens: 22},
	})

	deleted, err := store.DeleteOlderThan(cutoff)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	records, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("remaining = %d, want 1 (%+v)", len(records), records)
	}
	if records[0].Model != "keep-model" || records[0].TotalTokens != 11 {
		t.Fatalf("remaining record = %+v, want keep-model/11", records[0])
	}
	if !records[0].Timestamp.Equal(keepAt) {
		t.Fatalf("timestamp = %v, want %v", records[0].Timestamp, keepAt)
	}
}

func TestSQLitePluginResolvesUnknownStatusFromRequestContext(t *testing.T) {
	prevEnabled := StatisticsEnabled()
	SetStatisticsEnabled(true)
	defer SetStatisticsEnabled(prevEnabled)

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatalf("open usage db: %v", err)
	}
	defer func() {
		if errClose := store.Close(); errClose != nil {
			t.Errorf("close store: %v", errClose)
		}
	}()

	ctx := internallogging.WithResponseStatusHolder(context.Background())
	internallogging.SetResponseStatus(ctx, 500)
	NewSQLitePlugin(store).HandleUsage(ctx, coreusage.Record{
		APIKey:       "api-key",
		Model:        "model",
		Failed:       true,
		ErrorMessage: "upstream failed",
	})

	records, err := store.LoadAll()
	if err != nil {
		t.Fatalf("load records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].StatusCode != 500 || records[0].ErrorMessage != "upstream failed" {
		t.Fatalf("outcome = (%d, %q), want (500, upstream failed)", records[0].StatusCode, records[0].ErrorMessage)
	}
}

// TestCommitRetentionPruneSavesBaselineAndDeletesRows locks baseline upsert +
// old-row delete into one transaction (P0 atomicity).
func TestCommitRetentionPruneSavesBaselineAndDeletesRows(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatalf("open usage db: %v", err)
	}
	defer func() {
		if errClose := store.Close(); errClose != nil {
			t.Errorf("close store: %v", errClose)
		}
	}()

	cutoff := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	store.InsertRecord(coreusage.Record{
		APIKey:      "api-a",
		Model:       "keep-model",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		RequestedAt: cutoff.Add(time.Hour),
		Detail:      coreusage.Detail{TotalTokens: 3},
	})
	store.InsertRecord(coreusage.Record{
		APIKey:      "api-a",
		Model:       "drop-model",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		RequestedAt: cutoff.Add(-time.Hour),
		Detail:      coreusage.Detail{TotalTokens: 10},
	})

	baseline := UsageBaseline{
		TotalRequests: 1,
		SuccessCount:  1,
		FailureCount:  0,
		TotalTokens:   10,
		ByAuthIndex: map[string]KeyStatBucket{
			"auth-1": {Success: 1, Tokens: 10},
		},
		BySource: map[string]KeyStatBucket{
			"source-a": {Success: 1, Tokens: 10},
		},
		ModelSummary: map[string]SummaryModelStat{
			"drop-model": {Model: "drop-model", TotalRequests: 1, SuccessCount: 1, TotalTokens: 10},
		},
		UpdatedAt: cutoff,
	}

	deleted, err := store.CommitRetentionPrune(baseline, cutoff)
	if err != nil {
		t.Fatalf("CommitRetentionPrune: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	loaded, err := store.LoadUsageBaseline()
	if err != nil {
		t.Fatalf("LoadUsageBaseline: %v", err)
	}
	if loaded.TotalRequests != 1 || loaded.TotalTokens != 10 || loaded.SuccessCount != 1 {
		t.Fatalf("loaded baseline = %+v, want pruned-only 1/1/0/10", loaded)
	}
	if loaded.ByAuthIndex["auth-1"].Success != 1 || loaded.ByAuthIndex["auth-1"].Tokens != 10 {
		t.Fatalf("loaded auth baseline = %+v", loaded.ByAuthIndex["auth-1"])
	}
	if m := loaded.ModelSummary["drop-model"]; m.TotalRequests != 1 || m.TotalTokens != 10 {
		t.Fatalf("loaded model baseline = %+v", m)
	}

	records, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("remaining records = %d, want 1", len(records))
	}
	if records[0].Model != "keep-model" || records[0].TotalTokens != 3 {
		t.Fatalf("remaining = %+v, want keep-model/3", records[0])
	}
}

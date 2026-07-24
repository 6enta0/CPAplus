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

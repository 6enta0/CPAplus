package usage

import (
	"context"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

type SQLitePlugin struct {
	store *SQLiteStore
}

func NewSQLitePlugin(store *SQLiteStore) *SQLitePlugin {
	if store == nil {
		return nil
	}
	return &SQLitePlugin{store: store}
}

func (p *SQLitePlugin) HandleUsage(ctx context.Context, record coreusage.Record) {
	if p == nil || p.store == nil {
		return
	}
	if !statisticsEnabled.Load() {
		return
	}

	failed := record.Failed
	if !failed {
		failed = !resolveSuccess(ctx)
	}
	record.Failed = failed
	record.StatusCode, record.ErrorMessage = resolveClientExitFields(ctx, record, failed)
	p.store.InsertRecord(record)
}

var globalSQLiteStore *SQLiteStore

func InitSQLitePersistence(dbPath string) (*SQLiteStore, error) {
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		log.WithError(err).Warn("usage: sqlite store init failed, usage will not be persisted")
		return nil, err
	}
	if store == nil {
		return nil, nil
	}

	globalSQLiteStore = store

	// Restore all-time baseline first, then remaining detail rows.
	if baseline, errBaseline := store.LoadUsageBaseline(); errBaseline != nil {
		log.WithError(errBaseline).Warn("usage: failed to load usage baseline")
	} else if baseline.TotalRequests > 0 || baseline.TotalTokens > 0 ||
		len(baseline.ByAuthIndex) > 0 || len(baseline.BySource) > 0 {
		defaultRequestStatistics.ApplyUsageBaseline(baseline)
		log.Infof("usage: applied baseline totals (requests=%d tokens=%d)", baseline.TotalRequests, baseline.TotalTokens)
	}

	if errLoad := LoadFromSQLite(defaultRequestStatistics, store); errLoad != nil {
		log.WithError(errLoad).Warn("usage: failed to load historical records from sqlite")
	}

	plugin := NewSQLitePlugin(store)
	if plugin != nil {
		coreusage.RegisterPlugin(plugin)
		log.Info("usage: sqlite persistence plugin registered")
	}

	return store, nil
}

func GetSQLiteStore() *SQLiteStore { return globalSQLiteStore }

// StartRetentionLoop periodically prunes usage details older than retentionDays.
// retentionDays <= 0 disables the loop. Safe to call once at process start.
func StartRetentionLoop(stats *RequestStatistics, retentionDays int) {
	if stats == nil || retentionDays <= 0 {
		return
	}
	go func() {
		// Initial delay so startup load/pricing finish first.
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		<-timer.C
		runRetentionPass(stats, retentionDays)

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			runRetentionPass(stats, retentionDays)
		}
	}()
	log.Infof("usage: retention loop started (days=%d)", retentionDays)
}

func runRetentionPass(stats *RequestStatistics, retentionDays int) {
	if stats == nil || retentionDays <= 0 {
		return
	}
	deletedDB, prunedMem, err := stats.PruneOlderThan(retentionDays)
	if err != nil {
		log.WithError(err).Warn("usage: retention prune failed")
		return
	}
	if deletedDB > 0 || prunedMem > 0 {
		log.Infof("usage: retention pruned db_rows=%d memory_details=%d (days=%d)", deletedDB, prunedMem, retentionDays)
	}
}

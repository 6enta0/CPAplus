package usage

import (
	"context"

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

func (p *SQLitePlugin) HandleUsage(_ context.Context, record coreusage.Record) {
	if p == nil || p.store == nil {
		return
	}
	if !statisticsEnabled.Load() {
		return
	}
	p.store.InsertRecord(record)
}

func InitSQLitePersistence(dbPath string) (*SQLiteStore, error) {
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		log.WithError(err).Warn("usage: sqlite store init failed, usage will not be persisted")
		return nil, err
	}
	if store == nil {
		return nil, nil
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

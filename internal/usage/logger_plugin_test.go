package usage

import (
	"context"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestSnapshotRangeFiltersDetailsAndRebuildsTotals(t *testing.T) {
	prevEnabled := StatisticsEnabled()
	SetStatisticsEnabled(true)
	defer SetStatisticsEnabled(prevEnabled)

	stats := NewRequestStatistics()
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		RequestedAt: base.Add(-3 * time.Hour),
		Detail:      coreusage.Detail{TotalTokens: 99},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		RequestedAt: base.Add(-30 * time.Minute),
		Detail:      coreusage.Detail{TotalTokens: 10},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		RequestedAt: base.Add(-15 * time.Minute),
		Failed:      true,
		Detail:      coreusage.Detail{TotalTokens: 5},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-b",
		Model:       "model-b",
		RequestedAt: base.Add(-10 * time.Minute),
		Detail:      coreusage.Detail{TotalTokens: 7},
	})

	snapshot := stats.SnapshotRange(base.Add(-time.Hour), base)

	if snapshot.TotalRequests != 3 {
		t.Fatalf("total requests = %d, want 3", snapshot.TotalRequests)
	}
	if snapshot.SuccessCount != 2 {
		t.Fatalf("success count = %d, want 2", snapshot.SuccessCount)
	}
	if snapshot.FailureCount != 1 {
		t.Fatalf("failure count = %d, want 1", snapshot.FailureCount)
	}
	if snapshot.TotalTokens != 22 {
		t.Fatalf("total tokens = %d, want 22", snapshot.TotalTokens)
	}

	apiA, ok := snapshot.APIs["api-a"]
	if !ok {
		t.Fatal("api-a missing from ranged snapshot")
	}
	if apiA.TotalRequests != 2 || apiA.TotalTokens != 15 {
		t.Fatalf("api-a totals = (%d, %d), want (2, 15)", apiA.TotalRequests, apiA.TotalTokens)
	}
	modelA := apiA.Models["model-a"]
	if len(modelA.Details) != 2 {
		t.Fatalf("model-a details = %d, want 2", len(modelA.Details))
	}

	if snapshot.RequestsByDay["2026-07-04"] != 3 {
		t.Fatalf("requests by day = %d, want 3", snapshot.RequestsByDay["2026-07-04"])
	}
	if snapshot.RequestsByHour["11"] != 3 {
		t.Fatalf("requests by hour = %d, want 3", snapshot.RequestsByHour["11"])
	}
	if snapshot.TokensByHour["11"] != 22 {
		t.Fatalf("tokens by hour = %d, want 22", snapshot.TokensByHour["11"])
	}
}

func TestAllTimeKeyStatsAndSummaryUseWriteTimeCounters(t *testing.T) {
	prevEnabled := StatisticsEnabled()
	SetStatisticsEnabled(true)
	defer SetStatisticsEnabled(prevEnabled)

	stats := NewRequestStatistics()
	now := time.Now()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		RequestedAt: now,
		Detail:      coreusage.Detail{TotalTokens: 10},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-b",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		Failed:      true,
		RequestedAt: now,
		Detail:      coreusage.Detail{TotalTokens: 4},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-b",
		Model:       "model-a",
		Source:      "source-b",
		AuthIndex:   "auth-2",
		RequestedAt: now,
		Detail:      coreusage.Detail{TotalTokens: 6},
	})

	keyStats := stats.KeyStats()
	if keyStats.TotalRequests != 3 || keyStats.SuccessCount != 2 || keyStats.FailureCount != 1 {
		t.Fatalf("key-stats totals = %+v", keyStats)
	}
	if keyStats.TotalTokens != 20 {
		t.Fatalf("key-stats tokens = %d, want 20", keyStats.TotalTokens)
	}
	auth1 := keyStats.ByAuthIndex["auth-1"]
	if auth1.Success != 1 || auth1.Failure != 1 || auth1.Tokens != 14 {
		t.Fatalf("auth-1 = %+v", auth1)
	}
	sourceB := keyStats.BySource["source-b"]
	if sourceB.Success != 1 || sourceB.Failure != 0 || sourceB.Tokens != 6 {
		t.Fatalf("source-b = %+v", sourceB)
	}

	// Mutate counters after the fact; all-time key-stats must follow counters,
	// proving it is not rescanning details.
	stats.mu.Lock()
	bucket := stats.keyStatsByAuthIndex["auth-1"]
	bucket.Success += 5
	stats.keyStatsByAuthIndex["auth-1"] = bucket
	stats.mu.Unlock()

	keyStats = stats.KeyStats()
	if keyStats.ByAuthIndex["auth-1"].Success != 6 {
		t.Fatalf("counter path not used: auth-1 success = %d", keyStats.ByAuthIndex["auth-1"].Success)
	}

	summary := stats.Summary()
	if summary.TotalRequests != 3 || summary.SuccessCount != 2 || summary.FailureCount != 1 {
		t.Fatalf("summary totals = %+v", summary)
	}
	if len(summary.Models) != 2 {
		t.Fatalf("summary models = %d, want 2", len(summary.Models))
	}
}

func TestPruneDetailsOlderThanKeepsAllTimeCounters(t *testing.T) {
	prevEnabled := StatisticsEnabled()
	SetStatisticsEnabled(true)
	defer SetStatisticsEnabled(prevEnabled)

	stats := NewRequestStatistics()
	now := time.Now()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		RequestedAt: now.Add(-48 * time.Hour),
		Detail:      coreusage.Detail{TotalTokens: 10},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		RequestedAt: now.Add(-1 * time.Hour),
		Detail:      coreusage.Detail{TotalTokens: 3},
	})

	pruned := stats.PruneDetailsOlderThan(now.Add(-24 * time.Hour))
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}

	// All-time counters must still include the pruned request.
	keyStats := stats.KeyStats()
	if keyStats.TotalRequests != 2 || keyStats.TotalTokens != 13 {
		t.Fatalf("all-time after prune = req=%d tokens=%d", keyStats.TotalRequests, keyStats.TotalTokens)
	}
	if keyStats.ByAuthIndex["auth-1"].Success != 2 {
		t.Fatalf("auth-1 success after prune = %d", keyStats.ByAuthIndex["auth-1"].Success)
	}

	// Ranged key-stats only sees remaining details.
	ranged := stats.KeyStatsWithOptions(SnapshotOptions{Since: now.Add(-2 * time.Hour), Until: now})
	if ranged.TotalRequests != 1 || ranged.TotalTokens != 3 {
		t.Fatalf("ranged after prune = req=%d tokens=%d", ranged.TotalRequests, ranged.TotalTokens)
	}

	// Remaining model details should be 1.
	snapshot := stats.Snapshot()
	details := snapshot.APIs["api-a"].Models["model-a"].Details
	if len(details) != 1 {
		t.Fatalf("remaining details = %d, want 1", len(details))
	}
	if stats.baselineTotalRequests != 1 || stats.baselineTotalTokens != 10 {
		t.Fatalf("baseline = req=%d tokens=%d", stats.baselineTotalRequests, stats.baselineTotalTokens)
	}
}

func TestUsageOutcomeDetailsSurviveSnapshotAndImport(t *testing.T) {
	prevEnabled := StatisticsEnabled()
	SetStatisticsEnabled(true)
	defer SetStatisticsEnabled(prevEnabled)

	stats := NewRequestStatistics()
	timestamp := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	stats.Record(context.Background(), coreusage.Record{
		APIKey:       "api-a",
		Model:        "model-a",
		RequestedAt:  timestamp,
		Failed:       true,
		StatusCode:   429,
		ErrorMessage: "rate limited",
		Detail:       coreusage.Detail{TotalTokens: 1},
	})

	snapshot := stats.Snapshot()
	detail := snapshot.APIs["api-a"].Models["model-a"].Details[0]
	if detail.StatusCode != 429 || detail.ErrorMessage != "rate limited" {
		t.Fatalf("snapshot outcome = (%d, %q), want (429, rate limited)", detail.StatusCode, detail.ErrorMessage)
	}

	result := stats.MergeSnapshot(StatisticsSnapshot{APIs: map[string]APISnapshot{
		"api-a": {Models: map[string]ModelSnapshot{
			"model-a": {Details: []RequestDetail{{
				Timestamp:    timestamp,
				Failed:       true,
				StatusCode:   503,
				ErrorMessage: "upstream unavailable",
				Tokens:       TokenStats{TotalTokens: 1},
			}}},
		}},
	}})
	if result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("merge result = %+v, want one distinct outcome added", result)
	}
}

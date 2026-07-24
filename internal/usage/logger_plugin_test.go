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

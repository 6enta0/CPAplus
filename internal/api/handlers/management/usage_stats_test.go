package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestGetUsageStatisticsAppliesSinceUntilRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevEnabled := internalusage.StatisticsEnabled()
	internalusage.SetStatisticsEnabled(true)
	defer internalusage.SetStatisticsEnabled(prevEnabled)

	stats := internalusage.NewRequestStatistics()
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		RequestedAt: base.Add(-2 * time.Hour),
		Detail:      coreusage.Detail{TotalTokens: 20},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		RequestedAt: base.Add(-30 * time.Minute),
		Detail:      coreusage.Detail{TotalTokens: 8},
	})

	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(
		http.MethodGet,
		"/v0/management/usage-statistics?since="+base.Add(-time.Hour).Format(time.RFC3339Nano)+"&until="+base.Format(time.RFC3339Nano),
		nil,
	)

	h := &Handler{usageStats: stats}
	h.GetUsageStatistics(ginCtx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload struct {
		Usage          internalusage.StatisticsSnapshot `json:"usage"`
		FailedRequests int64                            `json:"failed_requests"`
	}
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("unmarshal response: %v", errUnmarshal)
	}
	if payload.Usage.TotalRequests != 1 {
		t.Fatalf("total requests = %d, want 1", payload.Usage.TotalRequests)
	}
	if payload.Usage.TotalTokens != 8 {
		t.Fatalf("total tokens = %d, want 8", payload.Usage.TotalTokens)
	}
	model := payload.Usage.APIs["api-a"].Models["model-a"]
	if len(model.Details) != 1 {
		t.Fatalf("details = %d, want 1", len(model.Details))
	}
}

func TestGetUsageStatisticsRejectsUnsupportedRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage-statistics?range=30d", nil)

	h := &Handler{usageStats: internalusage.NewRequestStatistics()}
	h.GetUsageStatistics(ginCtx)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestGetUsageKeyStatsAggregatesWithoutDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevEnabled := internalusage.StatisticsEnabled()
	internalusage.SetStatisticsEnabled(true)
	defer internalusage.SetStatisticsEnabled(prevEnabled)

	stats := internalusage.NewRequestStatistics()
	now := time.Now()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		RequestedAt: now.Add(-30 * time.Minute),
		Detail:      coreusage.Detail{TotalTokens: 10},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		Failed:      true,
		RequestedAt: now.Add(-10 * time.Minute),
		Detail:      coreusage.Detail{TotalTokens: 4},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-b",
		Model:       "model-b",
		Source:      "source-b",
		AuthIndex:   "auth-2",
		RequestedAt: now.Add(-48 * time.Hour),
		Detail:      coreusage.Detail{TotalTokens: 99},
	})

	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage-statistics/key-stats?range=24h", nil)

	h := &Handler{usageStats: stats}
	h.GetUsageKeyStats(ginCtx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Range         string                                 `json:"range"`
		TotalRequests int64                                  `json:"total_requests"`
		SuccessCount  int64                                  `json:"success_count"`
		FailureCount  int64                                  `json:"failure_count"`
		TotalTokens   int64                                  `json:"total_tokens"`
		ByAuthIndex   map[string]internalusage.KeyStatBucket `json:"by_auth_index"`
		BySource      map[string]internalusage.KeyStatBucket `json:"by_source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Range != "24h" {
		t.Fatalf("range = %q, want 24h", payload.Range)
	}
	if payload.TotalRequests != 2 || payload.SuccessCount != 1 || payload.FailureCount != 1 {
		t.Fatalf("totals = req=%d success=%d failure=%d, want 2/1/1", payload.TotalRequests, payload.SuccessCount, payload.FailureCount)
	}
	if payload.TotalTokens != 14 {
		t.Fatalf("tokens = %d, want 14", payload.TotalTokens)
	}
	auth1 := payload.ByAuthIndex["auth-1"]
	if auth1.Success != 1 || auth1.Failure != 1 || auth1.Tokens != 14 {
		t.Fatalf("auth-1 bucket = %+v", auth1)
	}
	if _, ok := payload.ByAuthIndex["auth-2"]; ok {
		t.Fatalf("auth-2 should be excluded by 24h range")
	}
	sourceA := payload.BySource["source-a"]
	if sourceA.Success != 1 || sourceA.Failure != 1 {
		t.Fatalf("source-a bucket = %+v", sourceA)
	}
	if strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("key-stats response unexpectedly includes details")
	}
}

func TestGetUsageSummaryReturnsModelRollups(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevEnabled := internalusage.StatisticsEnabled()
	internalusage.SetStatisticsEnabled(true)
	defer internalusage.SetStatisticsEnabled(prevEnabled)

	stats := internalusage.NewRequestStatistics()
	now := time.Now()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		RequestedAt: now,
		Detail:      coreusage.Detail{TotalTokens: 5},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-b",
		Failed:      true,
		RequestedAt: now,
		Detail:      coreusage.Detail{TotalTokens: 7},
	})

	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage-statistics/summary?range=all", nil)

	h := &Handler{usageStats: stats}
	h.GetUsageSummary(ginCtx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		TotalRequests int64                            `json:"total_requests"`
		SuccessCount  int64                            `json:"success_count"`
		FailureCount  int64                            `json:"failure_count"`
		Models        []internalusage.SummaryModelStat `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.TotalRequests != 2 || payload.SuccessCount != 1 || payload.FailureCount != 1 {
		t.Fatalf("totals unexpected: %+v", payload)
	}
	if len(payload.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(payload.Models))
	}
	if strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("summary response unexpectedly includes details")
	}
}

func TestGetUsageRecentSamplesBucketsStatusBarWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevEnabled := internalusage.StatisticsEnabled()
	internalusage.SetStatisticsEnabled(true)
	defer internalusage.SetStatisticsEnabled(prevEnabled)

	stats := internalusage.NewRequestStatistics()
	now := time.Now()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		RequestedAt: now.Add(-5 * time.Minute),
		Detail:      coreusage.Detail{TotalTokens: 3},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		Failed:      true,
		RequestedAt: now.Add(-15 * time.Minute),
		Detail:      coreusage.Detail{TotalTokens: 1},
	})
	// Outside the 200-minute status-bar window.
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		Source:      "source-a",
		AuthIndex:   "auth-1",
		RequestedAt: now.Add(-6 * time.Hour),
		Detail:      coreusage.Detail{TotalTokens: 9},
	})

	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage-statistics/recent-samples?range=24h", nil)

	h := &Handler{usageStats: stats}
	h.GetUsageRecentSamples(ginCtx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Range         string                                  `json:"range"`
		BucketMinutes int                                     `json:"bucket_minutes"`
		BlockCount    int                                     `json:"block_count"`
		ByAuthIndex   map[string][]internalusage.SampleBucket `json:"by_auth_index"`
		BySource      map[string][]internalusage.SampleBucket `json:"by_source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Range != "24h" {
		t.Fatalf("range = %q, want 24h", payload.Range)
	}
	if payload.BucketMinutes != internalusage.StatusBarBucketMinutes || payload.BlockCount != internalusage.StatusBarBlockCount {
		t.Fatalf("bucket shape = %d/%d", payload.BucketMinutes, payload.BlockCount)
	}
	series := payload.ByAuthIndex["auth-1"]
	if len(series) != internalusage.StatusBarBlockCount {
		t.Fatalf("auth-1 buckets = %d, want %d", len(series), internalusage.StatusBarBlockCount)
	}
	var success, failure int64
	for _, bucket := range series {
		success += bucket.Success
		failure += bucket.Failure
	}
	if success != 1 || failure != 1 {
		t.Fatalf("auth-1 totals success=%d failure=%d, want 1/1", success, failure)
	}
	sourceSeries := payload.BySource["source-a"]
	if len(sourceSeries) != internalusage.StatusBarBlockCount {
		t.Fatalf("source-a buckets = %d", len(sourceSeries))
	}
	if strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("recent-samples response unexpectedly includes details")
	}
}

func TestGetUsageChartDataBucketsRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevEnabled := internalusage.StatisticsEnabled()
	internalusage.SetStatisticsEnabled(true)
	defer internalusage.SetStatisticsEnabled(prevEnabled)

	stats := internalusage.NewRequestStatistics()
	now := time.Now()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		RequestedAt: now.Add(-30 * time.Minute),
		Detail:      coreusage.Detail{TotalTokens: 5},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		Failed:      true,
		RequestedAt: now.Add(-10 * time.Minute),
		Detail:      coreusage.Detail{TotalTokens: 2},
	})

	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage-statistics/chart-data?range=24h&bucket_minutes=60", nil)
	h := &Handler{usageStats: stats}
	h.GetUsageChartData(ginCtx)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		BucketMinutes int                        `json:"bucket_minutes"`
		Points        []internalusage.ChartPoint `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.BucketMinutes != 60 {
		t.Fatalf("bucket_minutes = %d", payload.BucketMinutes)
	}
	if len(payload.Points) == 0 {
		t.Fatal("expected chart points")
	}
	var req, fail, tokens int64
	for _, p := range payload.Points {
		req += p.Requests
		fail += p.Failure
		tokens += p.Tokens
	}
	if req != 2 || fail != 1 || tokens != 7 {
		t.Fatalf("totals req=%d fail=%d tokens=%d", req, fail, tokens)
	}
	if strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatal("chart-data unexpectedly includes details")
	}
}

// TestGetUsageChartDataForceModelsIncludesNonTop ensures models= still returns a
// series for a low-traffic model outside the default top-10 ranking.
func TestGetUsageChartDataForceModelsIncludesNonTop(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevEnabled := internalusage.StatisticsEnabled()
	internalusage.SetStatisticsEnabled(true)
	defer internalusage.SetStatisticsEnabled(prevEnabled)

	stats := internalusage.NewRequestStatistics()
	now := time.Now()
	// 10 popular models with higher request counts.
	for i := 0; i < 10; i++ {
		model := fmt.Sprintf("top-%02d", i)
		for j := 0; j < 3; j++ {
			stats.Record(context.Background(), coreusage.Record{
				APIKey:      "api-a",
				Model:       model,
				RequestedAt: now.Add(-time.Duration(j+1) * time.Minute),
				Detail:      coreusage.Detail{TotalTokens: 1},
			})
		}
	}
	// Long-tail model with only one request — outside top-10 by volume.
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "tail-model",
		RequestedAt: now.Add(-15 * time.Minute),
		Detail:      coreusage.Detail{TotalTokens: 42},
	})

	// Without models=, tail-model must be absent from by_model.
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage-statistics/chart-data?range=24h&bucket_minutes=60", nil)
	h := &Handler{usageStats: stats}
	h.GetUsageChartData(ginCtx)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var defaultPayload struct {
		ByModel map[string][]internalusage.ChartPoint `json:"by_model"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &defaultPayload); err != nil {
		t.Fatalf("unmarshal default: %v", err)
	}
	if _, ok := defaultPayload.ByModel["tail-model"]; ok {
		t.Fatal("tail-model unexpectedly present in default top-10 by_model")
	}
	if len(defaultPayload.ByModel) != 10 {
		t.Fatalf("default by_model size = %d, want 10", len(defaultPayload.ByModel))
	}

	// With models=tail-model, series must be present with the one request.
	rec = httptest.NewRecorder()
	ginCtx, _ = gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(
		http.MethodGet,
		"/v0/management/usage-statistics/chart-data?range=24h&bucket_minutes=60&models=tail-model,all,tail-model",
		nil,
	)
	h.GetUsageChartData(ginCtx)
	if rec.Code != http.StatusOK {
		t.Fatalf("forced status = %d body=%s", rec.Code, rec.Body.String())
	}
	var forcedPayload struct {
		ByModel map[string][]internalusage.ChartPoint `json:"by_model"`
		Points  []internalusage.ChartPoint            `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &forcedPayload); err != nil {
		t.Fatalf("unmarshal forced: %v", err)
	}
	series, ok := forcedPayload.ByModel["tail-model"]
	if !ok {
		t.Fatal("tail-model missing from forced by_model")
	}
	var req, tokens int64
	for _, p := range series {
		req += p.Requests
		tokens += p.Tokens
	}
	if req != 1 || tokens != 42 {
		t.Fatalf("tail-model totals req=%d tokens=%d, want 1/42", req, tokens)
	}
	// Top-10 still present; force-include is additive.
	if len(forcedPayload.ByModel) < 11 {
		t.Fatalf("forced by_model size = %d, want >= 11", len(forcedPayload.ByModel))
	}
	// Aggregate points still include everything (top + tail).
	var allReq int64
	for _, p := range forcedPayload.Points {
		allReq += p.Requests
	}
	if allReq != 31 { // 10*3 + 1
		t.Fatalf("aggregate requests = %d, want 31", allReq)
	}
}

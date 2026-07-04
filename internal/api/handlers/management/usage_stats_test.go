package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

package management

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

type usageExportPayload struct {
	Version    int                      `json:"version"`
	ExportedAt time.Time                `json:"exported_at"`
	Usage      usage.StatisticsSnapshot `json:"usage"`
}

type usageImportPayload struct {
	Version int                      `json:"version"`
	Usage   usage.StatisticsSnapshot `json:"usage"`
}

func (h *Handler) GetUsageStatistics(c *gin.Context) {
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusOK, gin.H{
			"usage":           usage.StatisticsSnapshot{},
			"failed_requests": int64(0),
		})
		return
	}
	options, errOptions := parseUsageSnapshotOptions(c)
	if errOptions != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errOptions.Error()})
		return
	}
	snapshot := h.usageStats.SnapshotWithOptions(options)
	c.JSON(http.StatusOK, gin.H{
		"usage":           snapshot,
		"failed_requests": snapshot.FailureCount,
	})
}

func (h *Handler) GetUsageKeyStats(c *gin.Context) {
	rangeValue := usageRangeLabel(c)
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusOK, gin.H{
			"range":           rangeValue,
			"total_requests":  int64(0),
			"success_count":   int64(0),
			"failure_count":   int64(0),
			"total_tokens":    int64(0),
			"by_auth_index":   map[string]usage.KeyStatBucket{},
			"by_source":       map[string]usage.KeyStatBucket{},
			"failed_requests": int64(0),
		})
		return
	}
	options, errOptions := parseUsageSnapshotOptions(c)
	if errOptions != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errOptions.Error()})
		return
	}
	snapshot := h.usageStats.KeyStatsWithOptions(options)
	c.JSON(http.StatusOK, gin.H{
		"range":           rangeValue,
		"total_requests":  snapshot.TotalRequests,
		"success_count":   snapshot.SuccessCount,
		"failure_count":   snapshot.FailureCount,
		"total_tokens":    snapshot.TotalTokens,
		"by_auth_index":   snapshot.ByAuthIndex,
		"by_source":       snapshot.BySource,
		"failed_requests": snapshot.FailureCount,
	})
}

func (h *Handler) GetUsageSummary(c *gin.Context) {
	rangeValue := usageRangeLabel(c)
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusOK, gin.H{
			"range":           rangeValue,
			"total_requests":  int64(0),
			"success_count":   int64(0),
			"failure_count":   int64(0),
			"total_tokens":    int64(0),
			"models":          []usage.SummaryModelStat{},
			"failed_requests": int64(0),
		})
		return
	}
	options, errOptions := parseUsageSnapshotOptions(c)
	if errOptions != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errOptions.Error()})
		return
	}
	snapshot := h.usageStats.SummaryWithOptions(options, 20)
	c.JSON(http.StatusOK, gin.H{
		"range":           rangeValue,
		"total_requests":  snapshot.TotalRequests,
		"success_count":   snapshot.SuccessCount,
		"failure_count":   snapshot.FailureCount,
		"total_tokens":    snapshot.TotalTokens,
		"models":          snapshot.Models,
		"failed_requests": snapshot.FailureCount,
	})
}

func (h *Handler) GetUsageRecentSamples(c *gin.Context) {
	rangeValue := usageRangeLabel(c)
	empty := gin.H{
		"range":          rangeValue,
		"bucket_minutes": usage.StatusBarBucketMinutes,
		"block_count":    usage.StatusBarBlockCount,
		"window_start":   time.Time{},
		"window_end":     time.Time{},
		"by_auth_index":  map[string][]usage.SampleBucket{},
		"by_source":      map[string][]usage.SampleBucket{},
	}
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusOK, empty)
		return
	}
	options, errOptions := parseUsageSnapshotOptions(c)
	if errOptions != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errOptions.Error()})
		return
	}
	snapshot := h.usageStats.RecentSamplesWithOptions(options)
	c.JSON(http.StatusOK, gin.H{
		"range":          rangeValue,
		"bucket_minutes": snapshot.BucketMinutes,
		"block_count":    snapshot.BlockCount,
		"window_start":   snapshot.WindowStart,
		"window_end":     snapshot.WindowEnd,
		"by_auth_index":  snapshot.ByAuthIndex,
		"by_source":      snapshot.BySource,
	})
}

func (h *Handler) GetUsageChartData(c *gin.Context) {
	rangeValue := usageRangeLabel(c)
	bucketMinutes := 60
	if raw := strings.TrimSpace(c.Query("bucket_minutes")); raw != "" {
		if parsed, err := time.ParseDuration(raw + "m"); err == nil && parsed > 0 {
			bucketMinutes = int(parsed / time.Minute)
		} else if n, errAtoi := atoiSafe(raw); errAtoi == nil && n > 0 {
			bucketMinutes = n
		}
	}
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusOK, gin.H{
			"range":          rangeValue,
			"bucket_minutes": bucketMinutes,
			"window_start":   time.Time{},
			"window_end":     time.Time{},
			"points":         []usage.ChartPoint{},
			"by_model":       map[string][]usage.ChartPoint{},
		})
		return
	}
	options, errOptions := parseUsageSnapshotOptions(c)
	if errOptions != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errOptions.Error()})
		return
	}
	snapshot := h.usageStats.ChartDataWithOptions(options, bucketMinutes, 10)
	c.JSON(http.StatusOK, gin.H{
		"range":          rangeValue,
		"bucket_minutes": snapshot.BucketMinutes,
		"window_start":   snapshot.WindowStart,
		"window_end":     snapshot.WindowEnd,
		"points":         snapshot.Points,
		"by_model":       snapshot.ByModel,
	})
}

func atoiSafe(raw string) (int, error) {
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid int")
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}

func usageRangeLabel(c *gin.Context) string {
	if c == nil {
		return "all"
	}
	rangeValue := strings.TrimSpace(c.Query("range"))
	if rangeValue == "" {
		if strings.TrimSpace(c.Query("since")) != "" || strings.TrimSpace(c.Query("until")) != "" {
			return "custom"
		}
		return "all"
	}
	return rangeValue
}

func parseUsageSnapshotOptions(c *gin.Context) (usage.SnapshotOptions, error) {
	var options usage.SnapshotOptions
	if c == nil {
		return options, nil
	}

	now := time.Now()
	rangeValue := strings.TrimSpace(c.Query("range"))
	switch rangeValue {
	case "", "all":
	case "7h":
		options.Since = now.Add(-7 * time.Hour)
		options.Until = now
	case "24h":
		options.Since = now.Add(-24 * time.Hour)
		options.Until = now
	case "7d":
		options.Since = now.Add(-7 * 24 * time.Hour)
		options.Until = now
	default:
		return options, fmt.Errorf("unsupported range %q", rangeValue)
	}

	if sinceValue := strings.TrimSpace(c.Query("since")); sinceValue != "" {
		since, errParse := time.Parse(time.RFC3339Nano, sinceValue)
		if errParse != nil {
			return options, fmt.Errorf("invalid since timestamp")
		}
		options.Since = since
		if options.Until.IsZero() {
			options.Until = now
		}
	}
	if untilValue := strings.TrimSpace(c.Query("until")); untilValue != "" {
		until, errParse := time.Parse(time.RFC3339Nano, untilValue)
		if errParse != nil {
			return options, fmt.Errorf("invalid until timestamp")
		}
		options.Until = until
	}
	if !options.Since.IsZero() && !options.Until.IsZero() && options.Until.Before(options.Since) {
		return options, fmt.Errorf("until must be greater than or equal to since")
	}

	return options, nil
}

func (h *Handler) ExportUsageStatistics(c *gin.Context) {
	var snapshot usage.StatisticsSnapshot
	if h != nil && h.usageStats != nil {
		snapshot = h.usageStats.Snapshot()
	}
	c.JSON(http.StatusOK, usageExportPayload{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Usage:      snapshot,
	})
}

func (h *Handler) ImportUsageStatistics(c *gin.Context) {
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usage statistics unavailable"})
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var payload usageImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if payload.Version != 0 && payload.Version != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported version"})
		return
	}

	result := h.usageStats.MergeSnapshot(payload.Usage)
	snapshot := h.usageStats.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"added":           result.Added,
		"skipped":         result.Skipped,
		"total_requests":  snapshot.TotalRequests,
		"failed_requests": snapshot.FailureCount,
	})
}

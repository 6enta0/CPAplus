package usage

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/pricing"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

var statisticsEnabled atomic.Bool

func init() {
	statisticsEnabled.Store(true)
	coreusage.RegisterPlugin(NewLoggerPlugin(defaultRequestStatistics))
}

func SetStatisticsEnabled(enabled bool) { statisticsEnabled.Store(enabled) }
func StatisticsEnabled() bool           { return statisticsEnabled.Load() }

type LoggerPlugin struct {
	stats *RequestStatistics
}

func NewLoggerPlugin(stats *RequestStatistics) *LoggerPlugin {
	return &LoggerPlugin{stats: stats}
}

func (p *LoggerPlugin) HandleUsage(ctx context.Context, record coreusage.Record) {
	if !statisticsEnabled.Load() {
		return
	}
	if p == nil || p.stats == nil {
		return
	}
	p.stats.Record(ctx, record)
}

type RequestStatistics struct {
	mu sync.RWMutex

	totalRequests int64
	successCount  int64
	failureCount  int64
	totalTokens   int64

	apis map[string]*apiStats

	requestsByDay  map[string]int64
	requestsByHour map[int]int64
	tokensByDay    map[string]int64
	tokensByHour   map[int]int64

	pricingStore *pricing.Store
	sqliteStore  *SQLiteStore
}

func (s *RequestStatistics) SetPricingStore(ps *pricing.Store) {
	s.pricingStore = ps
}

func (s *RequestStatistics) SetSQLiteStore(ss *SQLiteStore) {
	s.sqliteStore = ss
}

func (s *RequestStatistics) GetPricingStore() *pricing.Store {
	return s.pricingStore
}

type apiStats struct {
	TotalRequests int64
	TotalTokens   int64
	Models        map[string]*modelStats
}

type modelStats struct {
	TotalRequests int64
	TotalTokens   int64
	Details       []RequestDetail
}

type RequestDetail struct {
	Timestamp time.Time  `json:"timestamp"`
	LatencyMs int64      `json:"latency_ms"`
	Source    string     `json:"source"`
	AuthIndex string     `json:"auth_index"`
	Tokens    TokenStats `json:"tokens"`
	Failed    bool       `json:"failed"`
	// StatusCode is the HTTP status returned to the client. 0 means unknown/legacy.
	StatusCode int `json:"status_code,omitempty"`
	// ErrorMessage is a short failure summary for non-2xx outcomes.
	ErrorMessage string  `json:"error_message,omitempty"`
	CostUSD      float64 `json:"cost_usd"`
}

type TokenStats struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
}

type StatisticsSnapshot struct {
	TotalRequests int64 `json:"total_requests"`
	SuccessCount  int64 `json:"success_count"`
	FailureCount  int64 `json:"failure_count"`
	TotalTokens   int64 `json:"total_tokens"`

	APIs map[string]APISnapshot `json:"apis"`

	RequestsByDay  map[string]int64 `json:"requests_by_day"`
	RequestsByHour map[string]int64 `json:"requests_by_hour"`
	TokensByDay    map[string]int64 `json:"tokens_by_day"`
	TokensByHour   map[string]int64 `json:"tokens_by_hour"`
}

type SnapshotOptions struct {
	Since time.Time
	Until time.Time
}

func (o SnapshotOptions) HasRange() bool {
	return !o.Since.IsZero() || !o.Until.IsZero()
}

func (o SnapshotOptions) includes(timestamp time.Time) bool {
	if !o.Since.IsZero() && timestamp.Before(o.Since) {
		return false
	}
	if !o.Until.IsZero() && timestamp.After(o.Until) {
		return false
	}
	return true
}

type APISnapshot struct {
	TotalRequests int64                    `json:"total_requests"`
	TotalTokens   int64                    `json:"total_tokens"`
	Models        map[string]ModelSnapshot `json:"models"`
}

type ModelSnapshot struct {
	TotalRequests int64           `json:"total_requests"`
	TotalTokens   int64           `json:"total_tokens"`
	Details       []RequestDetail `json:"details"`
}

var defaultRequestStatistics = NewRequestStatistics()

func GetRequestStatistics() *RequestStatistics { return defaultRequestStatistics }

func NewRequestStatistics() *RequestStatistics {
	return &RequestStatistics{
		apis:           make(map[string]*apiStats),
		requestsByDay:  make(map[string]int64),
		requestsByHour: make(map[int]int64),
		tokensByDay:    make(map[string]int64),
		tokensByHour:   make(map[int]int64),
	}
}

func (s *RequestStatistics) Record(ctx context.Context, record coreusage.Record) {
	if s == nil {
		return
	}
	if !statisticsEnabled.Load() {
		return
	}
	timestamp := record.RequestedAt
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	detail := normaliseDetail(record.Detail)
	totalTokens := detail.TotalTokens
	statsKey := record.APIKey
	if statsKey == "" {
		statsKey = resolveAPIIdentifier(ctx, record)
	}
	failed := record.Failed
	if !failed {
		failed = !resolveSuccess(ctx)
	}
	statusCode, errorMessage := resolveClientExitFields(ctx, record, failed)
	success := !failed
	modelName := record.Model
	if modelName == "" {
		modelName = "unknown"
	}
	dayKey := timestamp.Format("2006-01-02")
	hourKey := timestamp.Hour()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalRequests++
	if success {
		s.successCount++
	} else {
		s.failureCount++
	}
	s.totalTokens += totalTokens

	stats, ok := s.apis[statsKey]
	if !ok {
		stats = &apiStats{Models: make(map[string]*modelStats)}
		s.apis[statsKey] = stats
	}
	s.updateAPIStats(stats, modelName, RequestDetail{
		Timestamp:    timestamp,
		LatencyMs:    normaliseLatency(record.Latency),
		Source:       record.Source,
		AuthIndex:    record.AuthIndex,
		Tokens:       detail,
		Failed:       failed,
		StatusCode:   statusCode,
		ErrorMessage: errorMessage,
	})

	s.requestsByDay[dayKey]++
	s.requestsByHour[hourKey]++
	s.tokensByDay[dayKey] += totalTokens
	s.tokensByHour[hourKey] += totalTokens
}

func (s *RequestStatistics) updateAPIStats(stats *apiStats, model string, detail RequestDetail) {
	stats.TotalRequests++
	stats.TotalTokens += detail.Tokens.TotalTokens
	modelStatsValue, ok := stats.Models[model]
	if !ok {
		modelStatsValue = &modelStats{}
		stats.Models[model] = modelStatsValue
	}
	modelStatsValue.TotalRequests++
	modelStatsValue.TotalTokens += detail.Tokens.TotalTokens
	modelStatsValue.Details = append(modelStatsValue.Details, detail)
}

func (s *RequestStatistics) Snapshot() StatisticsSnapshot {
	return s.SnapshotWithOptions(SnapshotOptions{})
}

func (s *RequestStatistics) SnapshotRange(since, until time.Time) StatisticsSnapshot {
	return s.SnapshotWithOptions(SnapshotOptions{Since: since, Until: until})
}

func (s *RequestStatistics) SnapshotWithOptions(options SnapshotOptions) StatisticsSnapshot {
	result := StatisticsSnapshot{}
	if s == nil {
		return result
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if !options.HasRange() {
		result.TotalRequests = s.totalRequests
		result.SuccessCount = s.successCount
		result.FailureCount = s.failureCount
		result.TotalTokens = s.totalTokens
	}

	result.APIs = make(map[string]APISnapshot, len(s.apis))
	for apiName, stats := range s.apis {
		apiSnapshot := APISnapshot{
			Models: make(map[string]ModelSnapshot, len(stats.Models)),
		}
		if !options.HasRange() {
			apiSnapshot.TotalRequests = stats.TotalRequests
			apiSnapshot.TotalTokens = stats.TotalTokens
		}
		for modelName, modelStatsValue := range stats.Models {
			requestDetails := make([]RequestDetail, 0, len(modelStatsValue.Details))
			var modelRequests int64
			var modelTokens int64
			for _, detail := range modelStatsValue.Details {
				if !options.includes(detail.Timestamp) {
					continue
				}
				requestDetails = append(requestDetails, detail)
				if options.HasRange() {
					tokens := detail.Tokens.TotalTokens
					if tokens < 0 {
						tokens = 0
					}
					modelRequests++
					modelTokens += tokens
					result.TotalRequests++
					if detail.Failed {
						result.FailureCount++
					} else {
						result.SuccessCount++
					}
					result.TotalTokens += tokens
					addSnapshotTimeBucket(&result, detail.Timestamp, tokens)
				}
			}
			if options.HasRange() && len(requestDetails) == 0 {
				continue
			}
			if !options.HasRange() {
				modelRequests = modelStatsValue.TotalRequests
				modelTokens = modelStatsValue.TotalTokens
			}
			apiSnapshot.Models[modelName] = ModelSnapshot{
				TotalRequests: modelRequests,
				TotalTokens:   modelTokens,
				Details:       requestDetails,
			}
			if options.HasRange() {
				apiSnapshot.TotalRequests += modelRequests
				apiSnapshot.TotalTokens += modelTokens
			}
		}
		if options.HasRange() && len(apiSnapshot.Models) == 0 {
			continue
		}
		result.APIs[apiName] = apiSnapshot
	}

	if options.HasRange() {
		return result
	}

	copySnapshotTimeBuckets(&result, s)

	return result
}

func addSnapshotTimeBucket(snapshot *StatisticsSnapshot, timestamp time.Time, totalTokens int64) {
	if snapshot == nil {
		return
	}
	if snapshot.RequestsByDay == nil {
		snapshot.RequestsByDay = make(map[string]int64)
	}
	if snapshot.RequestsByHour == nil {
		snapshot.RequestsByHour = make(map[string]int64)
	}
	if snapshot.TokensByDay == nil {
		snapshot.TokensByDay = make(map[string]int64)
	}
	if snapshot.TokensByHour == nil {
		snapshot.TokensByHour = make(map[string]int64)
	}
	dayKey := timestamp.Format("2006-01-02")
	hourKey := formatHour(timestamp.Hour())
	snapshot.RequestsByDay[dayKey]++
	snapshot.RequestsByHour[hourKey]++
	snapshot.TokensByDay[dayKey] += totalTokens
	snapshot.TokensByHour[hourKey] += totalTokens
}

func copySnapshotTimeBuckets(result *StatisticsSnapshot, s *RequestStatistics) {
	result.RequestsByDay = make(map[string]int64, len(s.requestsByDay))
	for k, v := range s.requestsByDay {
		result.RequestsByDay[k] = v
	}

	result.RequestsByHour = make(map[string]int64, len(s.requestsByHour))
	for hour, v := range s.requestsByHour {
		key := formatHour(hour)
		result.RequestsByHour[key] = v
	}

	result.TokensByDay = make(map[string]int64, len(s.tokensByDay))
	for k, v := range s.tokensByDay {
		result.TokensByDay[k] = v
	}

	result.TokensByHour = make(map[string]int64, len(s.tokensByHour))
	for hour, v := range s.tokensByHour {
		key := formatHour(hour)
		result.TokensByHour[key] = v
	}
}

type MergeResult struct {
	Added   int64 `json:"added"`
	Skipped int64 `json:"skipped"`
}

func (s *RequestStatistics) MergeSnapshot(snapshot StatisticsSnapshot) MergeResult {
	result := MergeResult{}
	if s == nil {
		return result
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]struct{})
	for apiName, stats := range s.apis {
		if stats == nil {
			continue
		}
		for modelName, modelStatsValue := range stats.Models {
			if modelStatsValue == nil {
				continue
			}
			for _, detail := range modelStatsValue.Details {
				seen[dedupKey(apiName, modelName, detail)] = struct{}{}
			}
		}
	}

	for apiName, apiSnapshot := range snapshot.APIs {
		apiName = strings.TrimSpace(apiName)
		if apiName == "" {
			continue
		}
		stats, ok := s.apis[apiName]
		if !ok || stats == nil {
			stats = &apiStats{Models: make(map[string]*modelStats)}
			s.apis[apiName] = stats
		} else if stats.Models == nil {
			stats.Models = make(map[string]*modelStats)
		}
		for modelName, modelSnapshot := range apiSnapshot.Models {
			modelName = strings.TrimSpace(modelName)
			if modelName == "" {
				modelName = "unknown"
			}
			for _, detail := range modelSnapshot.Details {
				detail.Tokens = normaliseTokenStats(detail.Tokens)
				if detail.LatencyMs < 0 {
					detail.LatencyMs = 0
				}
				if detail.Timestamp.IsZero() {
					detail.Timestamp = time.Now()
				}
				key := dedupKey(apiName, modelName, detail)
				if _, exists := seen[key]; exists {
					result.Skipped++
					continue
				}
				seen[key] = struct{}{}
				s.recordImported(apiName, modelName, stats, detail)
				result.Added++
			}
		}
	}

	return result
}

func (s *RequestStatistics) recordImported(apiName, modelName string, stats *apiStats, detail RequestDetail) {
	totalTokens := detail.Tokens.TotalTokens
	if totalTokens < 0 {
		totalTokens = 0
	}

	s.totalRequests++
	if detail.Failed {
		s.failureCount++
	} else {
		s.successCount++
	}
	s.totalTokens += totalTokens

	s.updateAPIStats(stats, modelName, detail)

	dayKey := detail.Timestamp.Format("2006-01-02")
	hourKey := detail.Timestamp.Hour()

	s.requestsByDay[dayKey]++
	s.requestsByHour[hourKey]++
	s.tokensByDay[dayKey] += totalTokens
	s.tokensByHour[hourKey] += totalTokens

	if s.sqliteStore != nil {
		s.sqliteStore.InsertRecord(coreusage.Record{
			RequestedAt: detail.Timestamp,
			APIKey:      apiName,
			Source:      detail.Source,
			AuthIndex:   detail.AuthIndex,
			Model:       modelName,
			Detail: coreusage.Detail{
				InputTokens:     detail.Tokens.InputTokens,
				OutputTokens:    detail.Tokens.OutputTokens,
				CachedTokens:    detail.Tokens.CachedTokens,
				ReasoningTokens: detail.Tokens.ReasoningTokens,
				TotalTokens:     detail.Tokens.TotalTokens,
			},
			Latency:      time.Duration(detail.LatencyMs) * time.Millisecond,
			Failed:       detail.Failed,
			StatusCode:   detail.StatusCode,
			ErrorMessage: detail.ErrorMessage,
		})
	}
}

func dedupKey(apiName, modelName string, detail RequestDetail) string {
	timestamp := detail.Timestamp.UTC().Format(time.RFC3339Nano)
	tokens := normaliseTokenStats(detail.Tokens)
	return fmt.Sprintf(
		"%s|%s|%s|%s|%s|%t|%d|%s|%d|%d|%d|%d|%d",
		apiName,
		modelName,
		timestamp,
		detail.Source,
		detail.AuthIndex,
		detail.Failed,
		detail.StatusCode,
		detail.ErrorMessage,
		tokens.InputTokens,
		tokens.OutputTokens,
		tokens.ReasoningTokens,
		tokens.CachedTokens,
		tokens.TotalTokens,
	)
}

func resolveAPIIdentifier(ctx context.Context, record coreusage.Record) string {
	if ctx != nil {
		if endpoint := strings.TrimSpace(internallogging.GetEndpoint(ctx)); endpoint != "" {
			return endpoint
		}
	}
	if record.Provider != "" {
		return record.Provider
	}
	return "unknown"
}

func resolveSuccess(ctx context.Context) bool {
	status := internallogging.GetResponseStatus(ctx)
	if status == 0 {
		return true
	}
	return status < httpStatusBadRequest
}

func resolveClientExitFields(ctx context.Context, record coreusage.Record, failed bool) (statusCode int, errorMessage string) {
	statusCode = record.StatusCode
	if statusCode <= 0 && ctx != nil {
		statusCode = internallogging.GetResponseStatus(ctx)
	}
	if statusCode <= 0 && !failed {
		statusCode = 200
	}
	errorMessage = strings.TrimSpace(record.ErrorMessage)
	if !failed || (statusCode > 0 && statusCode < httpStatusBadRequest) {
		errorMessage = ""
	}
	return statusCode, errorMessage
}

const httpStatusBadRequest = 400

func normaliseDetail(detail coreusage.Detail) TokenStats {
	tokens := TokenStats{
		InputTokens:     detail.InputTokens,
		OutputTokens:    detail.OutputTokens,
		ReasoningTokens: detail.ReasoningTokens,
		CachedTokens:    detail.CachedTokens,
		TotalTokens:     detail.TotalTokens,
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = detail.InputTokens + detail.OutputTokens + detail.ReasoningTokens
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = detail.InputTokens + detail.OutputTokens + detail.ReasoningTokens + detail.CachedTokens
	}
	return tokens
}

func normaliseTokenStats(tokens TokenStats) TokenStats {
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = tokens.InputTokens + tokens.OutputTokens + tokens.ReasoningTokens
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = tokens.InputTokens + tokens.OutputTokens + tokens.ReasoningTokens + tokens.CachedTokens
	}
	return tokens
}

func normaliseLatency(latency time.Duration) int64 {
	if latency <= 0 {
		return 0
	}
	return latency.Milliseconds()
}

func formatHour(hour int) string {
	if hour < 0 {
		hour = 0
	}
	hour = hour % 24
	return fmt.Sprintf("%02d", hour)
}

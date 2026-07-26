package usage

import (
	"context"
	"fmt"
	"sort"
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

	// Baseline cumulative totals from pruned history (persisted separately).
	// Semantics: pruned-only history. Boot reconstructs:
	//   all-time = baseline(pruned) + remaining(details still loaded).
	baselineTotalRequests int64
	baselineSuccessCount  int64
	baselineFailureCount  int64
	baselineTotalTokens   int64
	baselineByAuthIndex   map[string]KeyStatBucket
	baselineBySource      map[string]KeyStatBucket
	baselineModelSummary  map[string]SummaryModelStat

	apis map[string]*apiStats

	// All-time identity counters maintained on write for O(keys) key-stats reads.
	// Includes baseline + remaining details.
	keyStatsByAuthIndex map[string]KeyStatBucket
	keyStatsBySource    map[string]KeyStatBucket
	// All-time per-model rollups for O(models) summary reads.
	modelSummary map[string]*SummaryModelStat

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

// KeyStatBucket is a compact success/failure counter for one identity key.
type KeyStatBucket struct {
	Success int64 `json:"success"`
	Failure int64 `json:"failure"`
	Tokens  int64 `json:"tokens,omitempty"`
}

// KeyStatsSnapshot is a list-page friendly aggregation without request details.
type KeyStatsSnapshot struct {
	TotalRequests int64                    `json:"total_requests"`
	SuccessCount  int64                    `json:"success_count"`
	FailureCount  int64                    `json:"failure_count"`
	TotalTokens   int64                    `json:"total_tokens"`
	ByAuthIndex   map[string]KeyStatBucket `json:"by_auth_index"`
	BySource      map[string]KeyStatBucket `json:"by_source"`
}

// SummaryModelStat is a compact per-model rollup for summary cards.
type SummaryModelStat struct {
	Model         string `json:"model"`
	TotalRequests int64  `json:"total_requests"`
	SuccessCount  int64  `json:"success_count"`
	FailureCount  int64  `json:"failure_count"`
	TotalTokens   int64  `json:"total_tokens"`
}

// SummarySnapshot is a compact dashboard/summary payload without request details.
type SummarySnapshot struct {
	TotalRequests int64              `json:"total_requests"`
	SuccessCount  int64              `json:"success_count"`
	FailureCount  int64              `json:"failure_count"`
	TotalTokens   int64              `json:"total_tokens"`
	Models        []SummaryModelStat `json:"models,omitempty"`
}

// Status-bar sample window matches the management UI strip:
// 20 blocks × 10 minutes = 200 minutes.
const (
	StatusBarBlockCount    = 20
	StatusBarBucketMinutes = 10
)

// SampleBucket is one fixed-width success/failure cell for status bars.
type SampleBucket struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Success int64     `json:"success"`
	Failure int64     `json:"failure"`
}

// RecentSamplesSnapshot is a compact per-identity bucket series without details.
type RecentSamplesSnapshot struct {
	BucketMinutes int                       `json:"bucket_minutes"`
	BlockCount    int                       `json:"block_count"`
	WindowStart   time.Time                 `json:"window_start"`
	WindowEnd     time.Time                 `json:"window_end"`
	ByAuthIndex   map[string][]SampleBucket `json:"by_auth_index"`
	BySource      map[string][]SampleBucket `json:"by_source"`
}

// ChartPoint is one time-bucket for Usage charts.
type ChartPoint struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Label    string    `json:"label"`
	Requests int64     `json:"requests"`
	Success  int64     `json:"success"`
	Failure  int64     `json:"failure"`
	Tokens   int64     `json:"tokens"`
}

// ChartDataSnapshot is a compact time-series payload without request details.
type ChartDataSnapshot struct {
	BucketMinutes int                     `json:"bucket_minutes"`
	WindowStart   time.Time               `json:"window_start"`
	WindowEnd     time.Time               `json:"window_end"`
	Points        []ChartPoint            `json:"points"`
	ByModel       map[string][]ChartPoint `json:"by_model,omitempty"`
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
		apis:                 make(map[string]*apiStats),
		baselineByAuthIndex:  make(map[string]KeyStatBucket),
		baselineBySource:     make(map[string]KeyStatBucket),
		baselineModelSummary: make(map[string]SummaryModelStat),
		keyStatsByAuthIndex:  make(map[string]KeyStatBucket),
		keyStatsBySource:     make(map[string]KeyStatBucket),
		modelSummary:         make(map[string]*SummaryModelStat),
		requestsByDay:        make(map[string]int64),
		requestsByHour:       make(map[int]int64),
		tokensByDay:          make(map[string]int64),
		tokensByHour:         make(map[int]int64),
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
	s.bumpAllTimeCounters(model, detail)
}

// bumpAllTimeCounters updates write-time identity/model aggregates.
// Caller must hold s.mu for write.
func (s *RequestStatistics) bumpAllTimeCounters(model string, detail RequestDetail) {
	if s == nil {
		return
	}
	tokens := detail.Tokens.TotalTokens
	if tokens < 0 {
		tokens = 0
	}
	if s.keyStatsByAuthIndex == nil {
		s.keyStatsByAuthIndex = make(map[string]KeyStatBucket)
	}
	if s.keyStatsBySource == nil {
		s.keyStatsBySource = make(map[string]KeyStatBucket)
	}
	if s.modelSummary == nil {
		s.modelSummary = make(map[string]*SummaryModelStat)
	}

	if authIndex := strings.TrimSpace(detail.AuthIndex); authIndex != "" {
		bucket := s.keyStatsByAuthIndex[authIndex]
		if detail.Failed {
			bucket.Failure++
		} else {
			bucket.Success++
		}
		bucket.Tokens += tokens
		s.keyStatsByAuthIndex[authIndex] = bucket
	}
	if source := strings.TrimSpace(detail.Source); source != "" {
		bucket := s.keyStatsBySource[source]
		if detail.Failed {
			bucket.Failure++
		} else {
			bucket.Success++
		}
		bucket.Tokens += tokens
		s.keyStatsBySource[source] = bucket
	}

	modelName := strings.TrimSpace(model)
	if modelName == "" {
		modelName = "unknown"
	}
	entry := s.modelSummary[modelName]
	if entry == nil {
		entry = &SummaryModelStat{Model: modelName}
		s.modelSummary[modelName] = entry
	}
	entry.TotalRequests++
	entry.TotalTokens += tokens
	if detail.Failed {
		entry.FailureCount++
	} else {
		entry.SuccessCount++
	}
}

func cloneKeyStatBuckets(src map[string]KeyStatBucket) map[string]KeyStatBucket {
	if len(src) == 0 {
		return make(map[string]KeyStatBucket)
	}
	out := make(map[string]KeyStatBucket, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// ApplyUsageBaseline seeds pruned-only baseline state and all-time counters.
// Call before loading remaining detail rows from SQLite. Caller should not hold s.mu.
// Remaining rows must then be loaded so all-time becomes baseline + remaining.
func (s *RequestStatistics) ApplyUsageBaseline(baseline UsageBaseline) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.baselineTotalRequests = baseline.TotalRequests
	s.baselineSuccessCount = baseline.SuccessCount
	s.baselineFailureCount = baseline.FailureCount
	s.baselineTotalTokens = baseline.TotalTokens
	s.baselineByAuthIndex = cloneKeyStatBuckets(baseline.ByAuthIndex)
	s.baselineBySource = cloneKeyStatBuckets(baseline.BySource)
	s.baselineModelSummary = cloneSummaryModelStats(baseline.ModelSummary)

	s.totalRequests = baseline.TotalRequests
	s.successCount = baseline.SuccessCount
	s.failureCount = baseline.FailureCount
	s.totalTokens = baseline.TotalTokens

	if s.keyStatsByAuthIndex == nil {
		s.keyStatsByAuthIndex = make(map[string]KeyStatBucket)
	}
	if s.keyStatsBySource == nil {
		s.keyStatsBySource = make(map[string]KeyStatBucket)
	}
	if s.modelSummary == nil {
		s.modelSummary = make(map[string]*SummaryModelStat)
	}
	for k, v := range baseline.ByAuthIndex {
		s.keyStatsByAuthIndex[k] = v
	}
	for k, v := range baseline.BySource {
		s.keyStatsBySource[k] = v
	}
	for k, v := range baseline.ModelSummary {
		copyEntry := v
		if strings.TrimSpace(copyEntry.Model) == "" {
			copyEntry.Model = k
		}
		s.modelSummary[k] = &copyEntry
	}
}

// CaptureUsageBaseline returns the pruned-only baseline snapshot for persistence.
// It must NOT include remaining in-memory details; boot reloads those separately.
func (s *RequestStatistics) CaptureUsageBaseline() UsageBaseline {
	if s == nil {
		return UsageBaseline{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	return UsageBaseline{
		TotalRequests: s.baselineTotalRequests,
		SuccessCount:  s.baselineSuccessCount,
		FailureCount:  s.baselineFailureCount,
		TotalTokens:   s.baselineTotalTokens,
		ByAuthIndex:   cloneKeyStatBuckets(s.baselineByAuthIndex),
		BySource:      cloneKeyStatBuckets(s.baselineBySource),
		ModelSummary:  cloneSummaryModelStats(s.baselineModelSummary),
		UpdatedAt:     time.Now().UTC(),
	}
}

func cloneSummaryModelStats(src map[string]SummaryModelStat) map[string]SummaryModelStat {
	if len(src) == 0 {
		return make(map[string]SummaryModelStat)
	}
	out := make(map[string]SummaryModelStat, len(src))
	for k, v := range src {
		if strings.TrimSpace(v.Model) == "" {
			v.Model = k
		}
		out[k] = v
	}
	return out
}

func mergeKeyStatBuckets(dst map[string]KeyStatBucket, src map[string]KeyStatBucket) {
	if dst == nil || len(src) == 0 {
		return
	}
	for k, v := range src {
		b := dst[k]
		b.Success += v.Success
		b.Failure += v.Failure
		b.Tokens += v.Tokens
		dst[k] = b
	}
}

func mergeBaselineModelSummary(dst map[string]SummaryModelStat, src map[string]*SummaryModelStat) {
	if dst == nil || len(src) == 0 {
		return
	}
	for k, v := range src {
		if v == nil {
			continue
		}
		entry := dst[k]
		if strings.TrimSpace(entry.Model) == "" {
			entry.Model = v.Model
		}
		if strings.TrimSpace(entry.Model) == "" {
			entry.Model = k
		}
		entry.TotalRequests += v.TotalRequests
		entry.SuccessCount += v.SuccessCount
		entry.FailureCount += v.FailureCount
		entry.TotalTokens += v.TotalTokens
		dst[k] = entry
	}
}

// PruneDetailsOlderThan removes in-memory request details older than cutoff.
// All-time counters are preserved (history remains in baseline + counters).
// Returns the number of pruned detail rows.
func (s *RequestStatistics) PruneDetailsOlderThan(cutoff time.Time) int {
	if s == nil || cutoff.IsZero() {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	pruned := 0
	var dropRequests, dropSuccess, dropFailure, dropTokens int64
	dropByAuth := make(map[string]KeyStatBucket)
	dropBySource := make(map[string]KeyStatBucket)
	dropModels := make(map[string]*SummaryModelStat)

	for _, stats := range s.apis {
		if stats == nil {
			continue
		}
		for modelName, modelStatsValue := range stats.Models {
			if modelStatsValue == nil || len(modelStatsValue.Details) == 0 {
				continue
			}
			kept := modelStatsValue.Details[:0]
			var keptRequests, keptTokens int64
			for _, detail := range modelStatsValue.Details {
				if !detail.Timestamp.IsZero() && detail.Timestamp.Before(cutoff) {
					pruned++
					tokens := detail.Tokens.TotalTokens
					if tokens < 0 {
						tokens = 0
					}
					dropRequests++
					dropTokens += tokens
					if detail.Failed {
						dropFailure++
					} else {
						dropSuccess++
					}
					if authIndex := strings.TrimSpace(detail.AuthIndex); authIndex != "" {
						b := dropByAuth[authIndex]
						if detail.Failed {
							b.Failure++
						} else {
							b.Success++
						}
						b.Tokens += tokens
						dropByAuth[authIndex] = b
					}
					if source := strings.TrimSpace(detail.Source); source != "" {
						b := dropBySource[source]
						if detail.Failed {
							b.Failure++
						} else {
							b.Success++
						}
						b.Tokens += tokens
						dropBySource[source] = b
					}
					name := strings.TrimSpace(modelName)
					if name == "" {
						name = "unknown"
					}
					entry := dropModels[name]
					if entry == nil {
						entry = &SummaryModelStat{Model: name}
						dropModels[name] = entry
					}
					entry.TotalRequests++
					entry.TotalTokens += tokens
					if detail.Failed {
						entry.FailureCount++
					} else {
						entry.SuccessCount++
					}
					continue
				}
				kept = append(kept, detail)
				keptRequests++
				tokens := detail.Tokens.TotalTokens
				if tokens < 0 {
					tokens = 0
				}
				keptTokens += tokens
			}
			modelStatsValue.Details = kept
			modelStatsValue.TotalRequests = keptRequests
			modelStatsValue.TotalTokens = keptTokens
		}
	}

	// Move dropped history into pruned-only baseline. In-process all-time counters
	// stay unchanged so range=all does not dip during runtime.
	if s.baselineByAuthIndex == nil {
		s.baselineByAuthIndex = make(map[string]KeyStatBucket)
	}
	if s.baselineBySource == nil {
		s.baselineBySource = make(map[string]KeyStatBucket)
	}
	if s.baselineModelSummary == nil {
		s.baselineModelSummary = make(map[string]SummaryModelStat)
	}
	s.baselineTotalRequests += dropRequests
	s.baselineSuccessCount += dropSuccess
	s.baselineFailureCount += dropFailure
	s.baselineTotalTokens += dropTokens
	mergeKeyStatBuckets(s.baselineByAuthIndex, dropByAuth)
	mergeKeyStatBuckets(s.baselineBySource, dropBySource)
	mergeBaselineModelSummary(s.baselineModelSummary, dropModels)
	return pruned
}

// PruneOlderThan prunes SQLite + in-memory details older than retentionDays.
// retentionDays <= 0 is a no-op. All-time counters remain available via baseline.
func (s *RequestStatistics) PruneOlderThan(retentionDays int) (deletedDB int64, prunedMemory int, err error) {
	if s == nil || retentionDays <= 0 {
		return 0, 0, nil
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	prunedMemory = s.PruneDetailsOlderThan(cutoff)

	// Persist pruned-only baseline, then delete old SQLite detail rows.
	// Boot: ApplyUsageBaseline(pruned-only) + Load remaining details.
	if s.sqliteStore != nil {
		baseline := s.CaptureUsageBaseline()
		if errSave := s.sqliteStore.SaveUsageBaseline(baseline); errSave != nil {
			return 0, prunedMemory, errSave
		}
		deletedDB, err = s.sqliteStore.DeleteOlderThan(cutoff)
		if err != nil {
			return deletedDB, prunedMemory, err
		}
	}
	return deletedDB, prunedMemory, nil
}

// Snapshot returns the unscoped statistics view used by management export.
// Top-level totals are process all-time (baseline + remaining). Details under
// APIs are only the remaining in-memory window after retention prune. Import
// (MergeSnapshot) restores details only — see management-usage-stats-contract §7.1.
func (s *RequestStatistics) Snapshot() StatisticsSnapshot {
	return s.SnapshotWithOptions(SnapshotOptions{})
}

func (s *RequestStatistics) SnapshotRange(since, until time.Time) StatisticsSnapshot {
	return s.SnapshotWithOptions(SnapshotOptions{Since: since, Until: until})
}

func (s *RequestStatistics) KeyStats() KeyStatsSnapshot {
	return s.KeyStatsWithOptions(SnapshotOptions{})
}

func (s *RequestStatistics) KeyStatsWithOptions(options SnapshotOptions) KeyStatsSnapshot {
	result := KeyStatsSnapshot{
		ByAuthIndex: make(map[string]KeyStatBucket),
		BySource:    make(map[string]KeyStatBucket),
	}
	if s == nil {
		return result
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// All-time path uses write-time counters (O(keys)), not a full details scan.
	if !options.HasRange() {
		result.TotalRequests = s.totalRequests
		result.SuccessCount = s.successCount
		result.FailureCount = s.failureCount
		result.TotalTokens = s.totalTokens
		result.ByAuthIndex = cloneKeyStatBuckets(s.keyStatsByAuthIndex)
		result.BySource = cloneKeyStatBuckets(s.keyStatsBySource)
		return result
	}

	for _, stats := range s.apis {
		if stats == nil {
			continue
		}
		for _, modelStatsValue := range stats.Models {
			if modelStatsValue == nil {
				continue
			}
			for _, detail := range modelStatsValue.Details {
				if !options.includes(detail.Timestamp) {
					continue
				}
				tokens := detail.Tokens.TotalTokens
				if tokens < 0 {
					tokens = 0
				}
				result.TotalRequests++
				result.TotalTokens += tokens
				if detail.Failed {
					result.FailureCount++
				} else {
					result.SuccessCount++
				}

				if authIndex := strings.TrimSpace(detail.AuthIndex); authIndex != "" {
					bucket := result.ByAuthIndex[authIndex]
					if detail.Failed {
						bucket.Failure++
					} else {
						bucket.Success++
					}
					bucket.Tokens += tokens
					result.ByAuthIndex[authIndex] = bucket
				}
				if source := strings.TrimSpace(detail.Source); source != "" {
					bucket := result.BySource[source]
					if detail.Failed {
						bucket.Failure++
					} else {
						bucket.Success++
					}
					bucket.Tokens += tokens
					result.BySource[source] = bucket
				}
			}
		}
	}
	return result
}

func (s *RequestStatistics) RecentSamples() RecentSamplesSnapshot {
	return s.RecentSamplesWithOptions(SnapshotOptions{})
}

// RecentSamplesWithOptions builds fixed status-bar buckets.
// The sample window is always the trailing status-bar window (200 minutes)
// ending at options.Until (or now). options.Since only further clips the window.
func (s *RequestStatistics) RecentSamplesWithOptions(options SnapshotOptions) RecentSamplesSnapshot {
	now := time.Now()
	windowEnd := now
	if !options.Until.IsZero() {
		windowEnd = options.Until
	}
	bucketDuration := time.Duration(StatusBarBucketMinutes) * time.Minute
	windowStart := windowEnd.Add(-time.Duration(StatusBarBlockCount) * bucketDuration)
	if !options.Since.IsZero() && options.Since.After(windowStart) {
		windowStart = options.Since
	}

	result := RecentSamplesSnapshot{
		BucketMinutes: StatusBarBucketMinutes,
		BlockCount:    StatusBarBlockCount,
		WindowStart:   windowStart,
		WindowEnd:     windowEnd,
		ByAuthIndex:   make(map[string][]SampleBucket),
		BySource:      make(map[string][]SampleBucket),
	}
	if s == nil {
		return result
	}

	type acc struct {
		success []int64
		failure []int64
	}
	byAuth := make(map[string]*acc)
	bySource := make(map[string]*acc)
	ensure := func(m map[string]*acc, key string) *acc {
		entry := m[key]
		if entry == nil {
			entry = &acc{
				success: make([]int64, StatusBarBlockCount),
				failure: make([]int64, StatusBarBlockCount),
			}
			m[key] = entry
		}
		return entry
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	windowMs := windowEnd.Sub(windowStart)
	if windowMs <= 0 {
		return result
	}

	for _, stats := range s.apis {
		if stats == nil {
			continue
		}
		for _, modelStatsValue := range stats.Models {
			if modelStatsValue == nil {
				continue
			}
			for _, detail := range modelStatsValue.Details {
				ts := detail.Timestamp
				if ts.IsZero() || ts.Before(windowStart) || ts.After(windowEnd) {
					continue
				}
				age := windowEnd.Sub(ts)
				blockIndex := StatusBarBlockCount - 1 - int(age/bucketDuration)
				if blockIndex < 0 || blockIndex >= StatusBarBlockCount {
					continue
				}
				if authIndex := strings.TrimSpace(detail.AuthIndex); authIndex != "" {
					entry := ensure(byAuth, authIndex)
					if detail.Failed {
						entry.failure[blockIndex]++
					} else {
						entry.success[blockIndex]++
					}
				}
				if source := strings.TrimSpace(detail.Source); source != "" {
					entry := ensure(bySource, source)
					if detail.Failed {
						entry.failure[blockIndex]++
					} else {
						entry.success[blockIndex]++
					}
				}
			}
		}
	}

	buildSeries := func(m map[string]*acc) map[string][]SampleBucket {
		out := make(map[string][]SampleBucket, len(m))
		for key, entry := range m {
			series := make([]SampleBucket, StatusBarBlockCount)
			for i := 0; i < StatusBarBlockCount; i++ {
				start := windowStart.Add(time.Duration(i) * bucketDuration)
				series[i] = SampleBucket{
					Start:   start,
					End:     start.Add(bucketDuration),
					Success: entry.success[i],
					Failure: entry.failure[i],
				}
			}
			out[key] = series
		}
		return out
	}
	result.ByAuthIndex = buildSeries(byAuth)
	result.BySource = buildSeries(bySource)
	return result
}

// ChartDataWithOptions builds fixed-width chart buckets over options range
// (or last 24h when no range is provided). bucketMinutes defaults to 60.
// modelLimit caps per-model series (0 disables by_model). forceModels are
// always included in by_model (beyond top-N) so selected chart lines never
// silently disappear.
func (s *RequestStatistics) ChartDataWithOptions(options SnapshotOptions, bucketMinutes, modelLimit int, forceModels []string) ChartDataSnapshot {
	if bucketMinutes <= 0 {
		bucketMinutes = 60
	}
	if modelLimit < 0 {
		modelLimit = 0
	}
	forceSet := make(map[string]struct{})
	for _, name := range forceModels {
		name = strings.TrimSpace(name)
		if name == "" || name == "all" {
			continue
		}
		forceSet[name] = struct{}{}
	}
	now := time.Now()
	windowEnd := now
	if !options.Until.IsZero() {
		windowEnd = options.Until
	}
	windowStart := windowEnd.Add(-24 * time.Hour)
	if !options.Since.IsZero() {
		windowStart = options.Since
	}
	if !windowEnd.After(windowStart) {
		windowEnd = windowStart.Add(time.Duration(bucketMinutes) * time.Minute)
	}

	bucketDuration := time.Duration(bucketMinutes) * time.Minute
	// Align start down to bucket boundary for stable labels.
	windowStart = windowStart.UTC().Truncate(bucketDuration)
	windowEnd = windowEnd.UTC()
	if !windowEnd.After(windowStart) {
		windowEnd = windowStart.Add(bucketDuration)
	}
	blockCount := int(windowEnd.Sub(windowStart) / bucketDuration)
	if windowEnd.After(windowStart.Add(time.Duration(blockCount) * bucketDuration)) {
		blockCount++
	}
	if blockCount < 1 {
		blockCount = 1
	}
	// Hard cap to keep payloads small (e.g. 7d @ 5m = 2016; allow up to ~3000).
	const maxBlocks = 3000
	if blockCount > maxBlocks {
		blockCount = maxBlocks
		windowStart = windowEnd.Add(-time.Duration(blockCount) * bucketDuration)
	}

	result := ChartDataSnapshot{
		BucketMinutes: bucketMinutes,
		WindowStart:   windowStart,
		WindowEnd:     windowEnd,
		Points:        make([]ChartPoint, blockCount),
		ByModel:       make(map[string][]ChartPoint),
	}
	for i := 0; i < blockCount; i++ {
		start := windowStart.Add(time.Duration(i) * bucketDuration)
		end := start.Add(bucketDuration)
		label := start.Format("01-02 15:04")
		if bucketMinutes >= 24*60 {
			label = start.Format("2006-01-02")
		}
		result.Points[i] = ChartPoint{Start: start, End: end, Label: label}
	}
	if s == nil {
		return result
	}

	type acc struct {
		requests, success, failure, tokens []int64
	}
	total := &acc{
		requests: make([]int64, blockCount),
		success:  make([]int64, blockCount),
		failure:  make([]int64, blockCount),
		tokens:   make([]int64, blockCount),
	}
	byModel := make(map[string]*acc)
	ensureModel := func(name string) *acc {
		entry := byModel[name]
		if entry == nil {
			entry = &acc{
				requests: make([]int64, blockCount),
				success:  make([]int64, blockCount),
				failure:  make([]int64, blockCount),
				tokens:   make([]int64, blockCount),
			}
			byModel[name] = entry
		}
		return entry
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, stats := range s.apis {
		if stats == nil {
			continue
		}
		for modelName, modelStatsValue := range stats.Models {
			if modelStatsValue == nil {
				continue
			}
			name := strings.TrimSpace(modelName)
			if name == "" {
				name = "unknown"
			}
			for _, detail := range modelStatsValue.Details {
				ts := detail.Timestamp
				if ts.IsZero() || ts.Before(windowStart) || !ts.Before(windowEnd) {
					continue
				}
				idx := int(ts.Sub(windowStart) / bucketDuration)
				if idx < 0 || idx >= blockCount {
					continue
				}
				tokens := detail.Tokens.TotalTokens
				if tokens < 0 {
					tokens = 0
				}
				total.requests[idx]++
				total.tokens[idx] += tokens
				if detail.Failed {
					total.failure[idx]++
				} else {
					total.success[idx]++
				}
				trackModel := modelLimit > 0
				if !trackModel {
					_, trackModel = forceSet[name]
				}
				if trackModel {
					m := ensureModel(name)
					m.requests[idx]++
					m.tokens[idx] += tokens
					if detail.Failed {
						m.failure[idx]++
					} else {
						m.success[idx]++
					}
				}
			}
		}
	}

	for i := 0; i < blockCount; i++ {
		result.Points[i].Requests = total.requests[i]
		result.Points[i].Success = total.success[i]
		result.Points[i].Failure = total.failure[i]
		result.Points[i].Tokens = total.tokens[i]
	}

	emitModelSeries := func(name string, entry *acc) {
		if entry == nil {
			entry = &acc{
				requests: make([]int64, blockCount),
				success:  make([]int64, blockCount),
				failure:  make([]int64, blockCount),
				tokens:   make([]int64, blockCount),
			}
		}
		series := make([]ChartPoint, blockCount)
		for i := 0; i < blockCount; i++ {
			series[i] = ChartPoint{
				Start:    result.Points[i].Start,
				End:      result.Points[i].End,
				Label:    result.Points[i].Label,
				Requests: entry.requests[i],
				Success:  entry.success[i],
				Failure:  entry.failure[i],
				Tokens:   entry.tokens[i],
			}
		}
		result.ByModel[name] = series
	}

	if modelLimit > 0 && len(byModel) > 0 {
		// Keep top models by total requests, then force-include selected models.
		type rank struct {
			name string
			req  int64
		}
		ranks := make([]rank, 0, len(byModel))
		for name, entry := range byModel {
			var sum int64
			for _, v := range entry.requests {
				sum += v
			}
			ranks = append(ranks, rank{name: name, req: sum})
		}
		sort.Slice(ranks, func(i, j int) bool {
			if ranks[i].req == ranks[j].req {
				return ranks[i].name < ranks[j].name
			}
			return ranks[i].req > ranks[j].req
		})
		if len(ranks) > modelLimit {
			ranks = ranks[:modelLimit]
		}
		for _, r := range ranks {
			emitModelSeries(r.name, byModel[r.name])
		}
		for name := range forceSet {
			if _, ok := result.ByModel[name]; ok {
				continue
			}
			emitModelSeries(name, byModel[name])
		}
	} else if len(forceSet) > 0 {
		// modelLimit disabled, but caller still asked for explicit models.
		for name := range forceSet {
			emitModelSeries(name, byModel[name])
		}
	} else {
		result.ByModel = nil
	}
	return result
}

func (s *RequestStatistics) Summary() SummarySnapshot {
	return s.SummaryWithOptions(SnapshotOptions{}, 20)
}

func (s *RequestStatistics) SummaryWithOptions(options SnapshotOptions, modelLimit int) SummarySnapshot {
	if modelLimit <= 0 {
		modelLimit = 20
	}
	result := SummarySnapshot{}
	if s == nil {
		return result
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	modelTotals := make(map[string]*SummaryModelStat)

	if !options.HasRange() {
		// All-time path uses write-time model counters.
		result.TotalRequests = s.totalRequests
		result.SuccessCount = s.successCount
		result.FailureCount = s.failureCount
		result.TotalTokens = s.totalTokens
		for name, entry := range s.modelSummary {
			if entry == nil {
				continue
			}
			copyEntry := *entry
			if strings.TrimSpace(copyEntry.Model) == "" {
				copyEntry.Model = name
			}
			modelTotals[copyEntry.Model] = &copyEntry
		}
	} else {
		for _, stats := range s.apis {
			if stats == nil {
				continue
			}
			for modelName, modelStatsValue := range stats.Models {
				if modelStatsValue == nil {
					continue
				}
				name := strings.TrimSpace(modelName)
				if name == "" {
					name = "unknown"
				}
				for _, detail := range modelStatsValue.Details {
					if !options.includes(detail.Timestamp) {
						continue
					}
					tokens := detail.Tokens.TotalTokens
					if tokens < 0 {
						tokens = 0
					}
					result.TotalRequests++
					result.TotalTokens += tokens
					entry := modelTotals[name]
					if entry == nil {
						entry = &SummaryModelStat{Model: name}
						modelTotals[name] = entry
					}
					entry.TotalRequests++
					entry.TotalTokens += tokens
					if detail.Failed {
						result.FailureCount++
						entry.FailureCount++
					} else {
						result.SuccessCount++
						entry.SuccessCount++
					}
				}
			}
		}
	}

	models := make([]SummaryModelStat, 0, len(modelTotals))
	for _, entry := range modelTotals {
		models = append(models, *entry)
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].TotalRequests == models[j].TotalRequests {
			if models[i].TotalTokens == models[j].TotalTokens {
				return models[i].Model < models[j].Model
			}
			return models[i].TotalTokens > models[j].TotalTokens
		}
		return models[i].TotalRequests > models[j].TotalRequests
	})
	if len(models) > modelLimit {
		models = models[:modelLimit]
	}
	result.Models = models
	return result
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

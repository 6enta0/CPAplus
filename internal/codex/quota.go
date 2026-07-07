package codex

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	codexauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	log "github.com/sirupsen/logrus"
)

const usageURL = "https://chatgpt.com/backend-api/wham/usage"

const resetCreditURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"

const httpTimeout = 60 * time.Second

type QuotaWindow struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	LabelKey    string   `json:"labelKey"`
	LabelParams any      `json:"labelParams,omitempty"`
	UsedPercent *float64 `json:"usedPercent"`
	ResetLabel  string   `json:"resetLabel"`
	ResetAtISO  string   `json:"resetAtIso"`
}

type QuotaCheckResult struct {
	Name               string        `json:"name"`
	Status             string        `json:"status"`
	PlanType           string        `json:"planType,omitempty"`
	Windows            []QuotaWindow `json:"windows,omitempty"`
	QuotaCheckedAt     string        `json:"quotaCheckedAt,omitempty"`
	Error              string        `json:"error,omitempty"`
	AutoDisableApplied bool          `json:"autoDisableApplied,omitempty"`
	AutoEnableApplied  bool          `json:"autoEnableApplied,omitempty"`
	TokenRefreshed     bool          `json:"tokenRefreshed,omitempty"`

	ResetCreditsAvailable *int `json:"resetCreditsAvailable,omitempty"`
}

func fieldStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func loadAuthFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return result, nil
}

func writeAuthFile(path string, account map[string]any) error {
	data, err := json.Marshal(account)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func isTokenExpired(account map[string]any) bool {
	expiredStr := firstNonEmpty(fieldStr(account, "expired"), fieldStr(account, "expire"))
	if expiredStr == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, expiredStr)
	if err != nil {
		return true
	}
	return time.Now().Add(60 * time.Second).After(t)
}

func resolveAccountID(account map[string]any) string {
	if id := firstNonEmpty(fieldStr(account, "account_id"), fieldStr(account, "accountId")); id != "" {
		return id
	}
	idToken := firstNonEmpty(fieldStr(account, "id_token"))
	if idToken != "" {
		if claims, err := codexauth.ParseJWTToken(idToken); err == nil {
			return claims.GetAccountID()
		}
	}
	accessToken := firstNonEmpty(fieldStr(account, "access_token"))
	if accessToken != "" {
		if claims, err := codexauth.ParseJWTToken(accessToken); err == nil {
			return claims.GetAccountID()
		}
	}
	return ""
}

func refreshTokens(account map[string]any, cfg *config.Config, proxyURL string) (*codexauth.CodexTokenData, error) {
	refreshToken := firstNonEmpty(fieldStr(account, "refresh_token"))
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is required")
	}
	svc := codexauth.NewCodexAuthWithProxyURL(cfg, proxyURL)
	return svc.RefreshTokensWithRetry(context.Background(), refreshToken, 2)
}

func FetchQuotaUsage(accessToken, accountID, proxyURL string, cfg *config.Config) (map[string]any, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("missing access_token")
	}
	if accountID == "" {
		return nil, fmt.Errorf("missing account_id")
	}

	req, err := http.NewRequest("GET", usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal")
	req.Header.Set("Chatgpt-Account-Id", accountID)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Connection", "close")

	client := &http.Client{Timeout: httpTimeout}
	if proxyURL != "" || cfg != nil {
		sdkCfg := config.SDKConfig{ProxyURL: proxyURL}
		if cfg != nil && proxyURL == "" {
			sdkCfg = cfg.SDKConfig
		}
		client = util.SetProxy(&sdkCfg, client)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usage request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("unauthorized: access token may be expired")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("usage api returned status %d: %s", resp.StatusCode, string(body))
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse usage response: %w", err)
	}
	return payload, nil
}

func resolvePlanType(account map[string]any, usagePayload map[string]any) string {
	if usagePayload != nil {
		if pt := firstNonEmpty(fieldStr(usagePayload, "plan_type"), fieldStr(usagePayload, "planType")); pt != "" {
			return normalizePlanType(pt)
		}
	}
	idToken := firstNonEmpty(fieldStr(account, "id_token"))
	if idToken != "" {
		if claims, err := codexauth.ParseJWTToken(idToken); err == nil && claims.CodexAuthInfo.ChatgptPlanType != "" {
			return normalizePlanType(claims.CodexAuthInfo.ChatgptPlanType)
		}
	}
	return ""
}

func normalizePlanType(pt string) string {
	switch strings.ToLower(strings.TrimSpace(pt)) {
	case "free":
		return "free"
	case "plus":
		return "plus"
	case "pro":
		return "pro"
	case "prolite", "pro_lite":
		return "prolite"
	case "team":
		return "team"
	default:
		return strings.ToLower(strings.TrimSpace(pt))
	}
}

func findWindowPercent(windows []QuotaWindow, windowID string) *float64 {
	for _, w := range windows {
		if w.ID == windowID {
			return w.UsedPercent
		}
	}
	return nil
}

func DetermineAutoDisable(planType string, windows []QuotaWindow) *bool {
	pt := strings.ToLower(strings.TrimSpace(planType))
	if pt == "" {
		return nil
	}

	weeklyPct := findWindowPercent(windows, "weekly")
	if pt == "free" {
		if weeklyPct == nil {
			return nil
		}
		val := *weeklyPct >= 100
		return &val
	}

	fiveHourPct := findWindowPercent(windows, "five-hour")
	if fiveHourPct == nil || weeklyPct == nil {
		return nil
	}
	if *fiveHourPct < 100 && *weeklyPct < 100 {
		val := false
		return &val
	}
	val := *fiveHourPct >= 100 || *weeklyPct >= 100
	return &val
}

func CheckQuotaForFile(authDir, name string, refreshNow bool, cfg *config.Config, proxyURL string) QuotaCheckResult {
	return checkQuotaForFile(authDir, name, refreshNow, cfg, proxyURL, false)
}

// CheckQuotaForFileWithStatusManagement is reserved for runtime usage-limit
// recovery flows that intentionally toggle the auth disabled state.
func CheckQuotaForFileWithStatusManagement(authDir, name string, refreshNow bool, cfg *config.Config, proxyURL string) QuotaCheckResult {
	return checkQuotaForFile(authDir, name, refreshNow, cfg, proxyURL, true)
}

func checkQuotaForFile(authDir, name string, refreshNow bool, cfg *config.Config, proxyURL string, manageStatus bool) QuotaCheckResult {
	result := QuotaCheckResult{Name: name}

	filePath := filepath.Join(authDir, filepath.Base(name))
	account, err := loadAuthFile(filePath)
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		return result
	}

	originalAccount := make(map[string]any, len(account))
	for k, v := range account {
		originalAccount[k] = v
	}

	tokenRefreshed := false
	shouldRefresh := refreshNow || isTokenExpired(account)

	if shouldRefresh {
		rt := firstNonEmpty(fieldStr(account, "refresh_token"))
		if rt == "" && refreshNow {
			result.Status = "error"
			result.Error = "refresh token is required for refresh-now"
			return result
		}
		if rt != "" {
			td, refreshErr := refreshTokens(account, cfg, proxyURL)
			if refreshErr != nil {
				result.Status = "error"
				result.Error = fmt.Sprintf("token refresh failed: %v", refreshErr)
				return result
			}
			account["access_token"] = td.AccessToken
			account["refresh_token"] = td.RefreshToken
			account["id_token"] = td.IDToken
			account["account_id"] = td.AccountID
			account["email"] = td.Email
			account["expired"] = td.Expire
			account["last_refresh"] = time.Now().Format(time.RFC3339)
			if account["type"] == nil || account["type"] == "" {
				account["type"] = "codex"
			}
			tokenRefreshed = true
			persistRefreshedAccount(filePath, originalAccount, account)
		}
	}

	accountID := resolveAccountID(account)
	accessToken := firstNonEmpty(fieldStr(account, "access_token"))
	if accountID == "" {
		result.Status = "error"
		result.Error = "missing account_id"
		return result
	}
	if accessToken == "" {
		result.Status = "error"
		result.Error = "missing access_token"
		return result
	}

	usagePayload, usageErr := FetchQuotaUsage(accessToken, accountID, proxyURL, cfg)
	if usageErr != nil && tokenRefreshed {
		if retry, retryErr := FetchQuotaUsage(accessToken, accountID, proxyURL, cfg); retryErr == nil {
			usagePayload = retry
			usageErr = nil
		}
	}
	if usageErr != nil {
		result.Status = "error"
		result.Error = usageErr.Error()
		result.TokenRefreshed = tokenRefreshed
		return result
	}

	planType := resolvePlanType(account, usagePayload)
	windows := buildQuotaWindows(usagePayload)
	rl := firstDict(usagePayload, "rate_limit", "rateLimit")
	rlJSON := "nil"
	if rl != nil {
		b, _ := json.Marshal(rl)
		rlJSON = string(b)
	}
	log.Infof("quota check for %s: planType=%s windows=%d rate_limit=%s", name, planType, len(windows), rlJSON)

	result.Status = "success"
	result.PlanType = planType
	result.Windows = windows
	result.QuotaCheckedAt = time.Now().Format(time.RFC3339)
	result.TokenRefreshed = tokenRefreshed
	result.ResetCreditsAvailable = parseResetCreditsAvailable(usagePayload)

	if autoDisable := DetermineAutoDisable(planType, windows); manageStatus && autoDisable != nil {
		previousDisabled := false
		if d, ok := account["disabled"].(bool); ok {
			previousDisabled = d
		}
		if *autoDisable && !previousDisabled {
			account["disabled"] = true
			result.AutoDisableApplied = true
		} else if !*autoDisable && previousDisabled {
			account["disabled"] = false
			result.AutoEnableApplied = true
		}
	}

	persistQuotaFields(filePath, account, planType, windows, result.ResetCreditsAvailable, result.Error)

	return result
}

func persistRefreshedAccount(path string, original, refreshed map[string]any) {
	merged := make(map[string]any, len(original))
	for k, v := range original {
		if !strings.HasPrefix(k, "__") {
			merged[k] = v
		}
	}
	persistFields := []string{
		"access_token", "refresh_token", "id_token",
		"account_id", "email", "expired", "last_refresh", "type",
	}
	for _, k := range persistFields {
		if v := fieldStr(refreshed, k); v != "" {
			merged[k] = v
		}
	}
	if err := writeAuthFile(path, merged); err != nil {
		log.WithError(err).Warnf("failed to persist refreshed account to %s", path)
	}
}

func persistQuotaFields(path string, account map[string]any, planType string, windows []QuotaWindow, resetCredits *int, quotaError string) {
	account["quota_plan_type"] = planType
	account["quota_windows"] = windows
	account["quota_checked_at"] = time.Now().Format(time.RFC3339)
	account["quota_error"] = quotaError
	account["quota_reset_credits"] = resetCredits
	windowsJSON, _ := json.Marshal(windows)
	log.Infof("persistQuotaFields for %s: windows_json=%s", filepath.Base(path), string(windowsJSON))
	if err := writeAuthFile(path, account); err != nil {
		log.WithError(err).Warnf("failed to persist quota fields to %s", path)
	}
}

func fieldFloat64(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

func buildQuotaWindows(payload map[string]any) []QuotaWindow {
	if payload == nil {
		return nil
	}

	rateLimit := firstDict(payload, "rate_limit", "rateLimit")

	var windows []QuotaWindow

	if rateLimit != nil {
		fiveHour, weekly := pickClassifiedWindows(rateLimit)
		if fiveHour != nil {
			windows = append(windows, buildWindow(fiveHour, "five-hour", "Primary window", "codex_quota.primary_window"))
		}
		if weekly != nil {
			windows = append(windows, buildWindow(weekly, "weekly", "Secondary window", "codex_quota.secondary_window"))
		}
	}

	additionalLimits, _ := payload["additional_rate_limits"].([]any)
	for i, item := range additionalLimits {
		limitItem, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rateInfo := firstDict(limitItem, "rate_limit", "rateLimit")
		if rateInfo == nil {
			continue
		}
		limitName := firstNonEmpty(
			fieldStr(limitItem, "limit_name"),
			fieldStr(limitItem, "limitName"),
			fieldStr(limitItem, "metered_feature"),
			fieldStr(limitItem, "meteredFeature"),
			fmt.Sprintf("additional-%d", i+1),
		)
		idPrefix := quotaNormalizeWindowID(limitName)
		if idPrefix == "" {
			idPrefix = fmt.Sprintf("additional-%d", i+1)
		}
		fiveHour, weekly := pickClassifiedWindows(rateInfo)
		if fiveHour != nil {
			windows = append(windows, buildWindow(fiveHour,
				fmt.Sprintf("%s-five-hour-%d", idPrefix, i),
				fmt.Sprintf("%s primary window", limitName),
				"codex_quota.additional_primary_window"))
		}
		if weekly != nil {
			windows = append(windows, buildWindow(weekly,
				fmt.Sprintf("%s-weekly-%d", idPrefix, i),
				fmt.Sprintf("%s secondary window", limitName),
				"codex_quota.additional_secondary_window"))
		}
	}

	legacyWindows := buildLegacyQuotaWindows(payload)
	if len(windows) == 0 && len(legacyWindows) > 0 {
		windows = legacyWindows
	}

	return windows
}

func firstDict(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		if v, ok := m[k].(map[string]any); ok {
			return v
		}
	}
	return nil
}

const (
	fiveHourSeconds = 18000
	weekSeconds     = 604800
)

func pickClassifiedWindows(limitInfo map[string]any) (fiveHour, weekly map[string]any) {
	primary := firstDict(limitInfo, "primary_window", "primaryWindow")
	secondary := firstDict(limitInfo, "secondary_window", "secondaryWindow")
	for _, w := range []map[string]any{primary, secondary} {
		if w == nil {
			continue
		}
		seconds := fieldFloat64(w, "limit_window_seconds")
		if seconds == 0 {
			seconds = fieldFloat64(w, "limitWindowSeconds")
		}
		if seconds == fiveHourSeconds && fiveHour == nil {
			fiveHour = w
		} else if seconds == weekSeconds && weekly == nil {
			weekly = w
		}
	}
	return
}

func buildWindow(w map[string]any, id, label, labelKey string) QuotaWindow {
	usedPercent := computeUsedPercent(w)
	var resetLabel string
	var resetAtISO string
	if s, ok := w["reset_at"].(string); ok && s != "" {
		resetAtISO = s
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			resetLabel = t.Format("01/02 15:04")
		}
	} else if unixSec := fieldFloat64(w, "reset_at"); unixSec > 0 {
		t := time.Unix(int64(unixSec), 0)
		resetAtISO = t.Format(time.RFC3339)
		resetLabel = t.Format("01/02 15:04")
	}
	if resetLabel == "" {
		if rl, ok := w["reset_label"].(string); ok {
			resetLabel = rl
		}
	}
	return QuotaWindow{
		ID:          id,
		Label:       label,
		LabelKey:    labelKey,
		UsedPercent: usedPercent,
		ResetLabel:  resetLabel,
		ResetAtISO:  resetAtISO,
	}
}

func quotaNormalizeWindowID(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

func buildLegacyQuotaWindows(payload map[string]any) []QuotaWindow {
	limitInfo := firstDict(payload, "limit_info", "limitInfo")
	if limitInfo == nil {
		return nil
	}
	var windows []QuotaWindow
	if primary, ok := limitInfo["primary_window"].(map[string]any); ok {
		windows = append(windows, buildWindow(primary, "five-hour", "Primary window", "codex_quota.primary_window"))
	}
	if secondary, ok := limitInfo["secondary_window"].(map[string]any); ok {
		windows = append(windows, buildWindow(secondary, "weekly", "Secondary window", "codex_quota.secondary_window"))
	}
	return windows
}

func computeUsedPercent(w map[string]any) *float64 {
	if v, ok := w["used_percent"]; ok {
		switch n := v.(type) {
		case float64:
			pct := n
			return &pct
		case json.Number:
			if f, err := n.Float64(); err == nil {
				pct := f
				return &pct
			}
		}
	}
	if v, ok := w["usedPercent"]; ok {
		switch n := v.(type) {
		case float64:
			pct := n
			return &pct
		case json.Number:
			if f, err := n.Float64(); err == nil {
				pct := f
				return &pct
			}
		}
	}
	used := fieldFloat64(w, "used")
	limit := fieldFloat64(w, "limit")
	if limit <= 0 {
		return nil
	}
	pct := (used / limit) * 100
	return &pct
}

// parseResetCreditsAvailable extracts rate_limit_reset_credits.available_count
// from a /wham/usage payload. Returns nil when the field is absent.
func parseResetCreditsAvailable(payload map[string]any) *int {
	if payload == nil {
		return nil
	}
	credits := firstDict(payload, "rate_limit_reset_credits", "rateLimitResetCredits")
	if credits == nil {
		return nil
	}
	if _, ok := credits["available_count"]; ok {
		v := int(fieldFloat64(credits, "available_count"))
		return &v
	}
	if _, ok := credits["availableCount"]; ok {
		v := int(fieldFloat64(credits, "availableCount"))
		return &v
	}
	return nil
}

// ResetCreditResult is the outcome of consuming one rate-limit reset credit.
// On success it also carries the refreshed quota snapshot (re-fetched from
// /wham/usage after the reset) so the UI can update the related columns.
type ResetCreditResult struct {
	Name                  string        `json:"name"`
	Status                string        `json:"status"`
	Error                 string        `json:"error,omitempty"`
	Code                  string        `json:"code,omitempty"`
	WindowsReset          int           `json:"windowsReset"`
	PlanType              string        `json:"planType,omitempty"`
	Windows               []QuotaWindow `json:"windows,omitempty"`
	QuotaCheckedAt        string        `json:"quotaCheckedAt,omitempty"`
	ResetCreditsAvailable *int          `json:"resetCreditsAvailable,omitempty"`
	TokenRefreshed        bool          `json:"tokenRefreshed,omitempty"`
}

// generateRedeemRequestID produces a UUID-v4-shaped idempotency key for the
// consume call, without pulling in a new dependency.
func generateRedeemRequestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexStr := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:]), nil
}

// ConsumeRateLimitReset consumes one rate_limit_reset_credit via the ChatGPT
// backend. Mirrors FetchQuotaUsage's request shape (headers + proxy handling).
func ConsumeRateLimitReset(accessToken, accountID, proxyURL string, cfg *config.Config) (map[string]any, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("missing access_token")
	}
	if accountID == "" {
		return nil, fmt.Errorf("missing account_id")
	}

	redeemID, err := generateRedeemRequestID()
	if err != nil {
		return nil, fmt.Errorf("generate redeem id: %w", err)
	}
	reqBody, err := json.Marshal(map[string]string{"redeem_request_id": redeemID})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", resetCreditURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal")
	req.Header.Set("Chatgpt-Account-Id", accountID)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Connection", "close")

	client := &http.Client{Timeout: httpTimeout}
	if proxyURL != "" || cfg != nil {
		sdkCfg := config.SDKConfig{ProxyURL: proxyURL}
		if cfg != nil && proxyURL == "" {
			sdkCfg = cfg.SDKConfig
		}
		client = util.SetProxy(&sdkCfg, client)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reset request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("unauthorized: access token may be expired")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("reset api returned status %d: %s", resp.StatusCode, string(body))
	}

	var payload map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("parse reset response: %w", err)
		}
	}
	return payload, nil
}

// ResetRateLimitCreditForFile consumes one rate-limit reset credit for the
// given auth file, refreshing an expired token first, then re-fetches usage so
// the returned result reflects the post-reset windows and remaining credits.
func ResetRateLimitCreditForFile(authDir, name string, cfg *config.Config, proxyURL string) ResetCreditResult {
	result := ResetCreditResult{Name: name}

	filePath := filepath.Join(authDir, filepath.Base(name))
	account, err := loadAuthFile(filePath)
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		return result
	}

	if isTokenExpired(account) {
		rt := firstNonEmpty(fieldStr(account, "refresh_token"))
		if rt == "" {
			result.Status = "error"
			result.Error = "missing refresh_token; cannot refresh expired token"
			return result
		}
		originalAccount := make(map[string]any, len(account))
		for k, v := range account {
			originalAccount[k] = v
		}
		td, refreshErr := refreshTokens(account, cfg, proxyURL)
		if refreshErr != nil {
			result.Status = "error"
			result.Error = fmt.Sprintf("token refresh failed: %v", refreshErr)
			return result
		}
		account["access_token"] = td.AccessToken
		account["refresh_token"] = td.RefreshToken
		account["id_token"] = td.IDToken
		account["account_id"] = td.AccountID
		account["email"] = td.Email
		account["expired"] = td.Expire
		account["last_refresh"] = time.Now().Format(time.RFC3339)
		if account["type"] == nil || account["type"] == "" {
			account["type"] = "codex"
		}
		result.TokenRefreshed = true
		persistRefreshedAccount(filePath, originalAccount, account)
	}

	accountID := resolveAccountID(account)
	accessToken := firstNonEmpty(fieldStr(account, "access_token"))
	if accountID == "" {
		result.Status = "error"
		result.Error = "missing account_id"
		return result
	}
	if accessToken == "" {
		result.Status = "error"
		result.Error = "missing access_token"
		return result
	}

	resetPayload, resetErr := ConsumeRateLimitReset(accessToken, accountID, proxyURL, cfg)
	if resetErr != nil {
		result.Status = "error"
		result.Error = resetErr.Error()
		return result
	}

	result.Status = "success"
	result.Code = fieldStr(resetPayload, "code")
	result.WindowsReset = int(fieldFloat64(resetPayload, "windows_reset"))

	// Re-fetch usage so the UI reflects post-reset windows and remaining credits.
	refreshed := CheckQuotaForFile(authDir, name, false, cfg, proxyURL)
	if refreshed.Status == "success" {
		result.PlanType = refreshed.PlanType
		result.Windows = refreshed.Windows
		result.QuotaCheckedAt = refreshed.QuotaCheckedAt
		result.ResetCreditsAvailable = refreshed.ResetCreditsAvailable
		if refreshed.TokenRefreshed {
			result.TokenRefreshed = true
		}
	}

	return result
}

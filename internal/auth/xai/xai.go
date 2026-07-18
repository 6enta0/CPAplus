package xai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
)

// XAIAuth performs xAI OAuth discovery, device-code login, and refresh.
type XAIAuth struct {
	httpClient *http.Client
}

var xaiRefreshGroup singleflight.Group

// NewXAIAuth creates an xAI OAuth helper using config proxy settings.
func NewXAIAuth(cfg *config.Config) *XAIAuth {
	return NewXAIAuthWithProxyURL(cfg, "")
}

// NewXAIAuthWithProxyURL creates an xAI OAuth helper with an explicit proxy URL.
func NewXAIAuthWithProxyURL(cfg *config.Config, proxyURL string) *XAIAuth {
	effectiveProxyURL := strings.TrimSpace(proxyURL)
	var sdkCfg config.SDKConfig
	if cfg != nil {
		sdkCfg = cfg.SDKConfig
		if effectiveProxyURL == "" {
			effectiveProxyURL = strings.TrimSpace(cfg.ProxyURL)
		}
	}
	sdkCfg.ProxyURL = effectiveProxyURL
	client := &http.Client{Timeout: httpClientTimeout}
	return &XAIAuth{httpClient: util.SetProxy(&sdkCfg, client)}
}

// ValidateOAuthEndpoint validates an endpoint returned by xAI discovery.
func ValidateOAuthEndpoint(rawURL, field string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("xai discovery %s is empty", field)
	}
	parsed, errParse := url.Parse(rawURL)
	if errParse != nil {
		return "", fmt.Errorf("xai discovery %s is invalid: %w", field, errParse)
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("xai discovery %s must use https: %q", field, rawURL)
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host != "x.ai" && !strings.HasSuffix(host, ".x.ai") {
		return "", fmt.Errorf("xai discovery %s host %q is not on x.ai", field, host)
	}
	return rawURL, nil
}

// Discover resolves xAI OAuth endpoints through OIDC discovery.
func (a *XAIAuth) Discover(ctx context.Context) (*Discovery, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, DiscoveryURL, nil)
	if errRequest != nil {
		return nil, fmt.Errorf("xai discovery: create request: %w", errRequest)
	}
	req.Header.Set("Accept", "application/json")
	resp, errDo := a.httpClient.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("xai discovery: request failed: %w", errDo)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("xai discovery: close response body error: %v", errClose)
		}
	}()
	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return nil, fmt.Errorf("xai discovery: read response: %w", errRead)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai discovery failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload Discovery
	if errUnmarshal := json.Unmarshal(body, &payload); errUnmarshal != nil {
		return nil, fmt.Errorf("xai discovery: parse response: %w", errUnmarshal)
	}
	deviceEndpoint, errValidateDevice := ValidateOAuthEndpoint(payload.DeviceAuthorizationEndpoint, "device_authorization_endpoint")
	if errValidateDevice != nil {
		return nil, errValidateDevice
	}
	tokenEndpoint, errValidateToken := ValidateOAuthEndpoint(payload.TokenEndpoint, "token_endpoint")
	if errValidateToken != nil {
		return nil, errValidateToken
	}
	return &Discovery{DeviceAuthorizationEndpoint: deviceEndpoint, TokenEndpoint: tokenEndpoint}, nil
}

// StartDeviceFlow requests a device code from xAI.
func (a *XAIAuth) StartDeviceFlow(ctx context.Context) (*DeviceCodeResponse, error) {
	discovery, errDiscover := a.Discover(ctx)
	if errDiscover != nil {
		return nil, errDiscover
	}
	return a.RequestDeviceCode(ctx, discovery.DeviceAuthorizationEndpoint, discovery.TokenEndpoint)
}

// RequestDeviceCode requests a device authorization code from the given endpoint.
func (a *XAIAuth) RequestDeviceCode(ctx context.Context, deviceEndpoint, tokenEndpoint string) (*DeviceCodeResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	deviceEndpoint = strings.TrimSpace(deviceEndpoint)
	if deviceEndpoint == "" {
		return nil, fmt.Errorf("xai device code: device authorization endpoint is required")
	}
	form := url.Values{"client_id": {ClientID}, "scope": {Scope}}
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, deviceEndpoint, strings.NewReader(form.Encode()))
	if errRequest != nil {
		return nil, fmt.Errorf("xai device code: create request: %w", errRequest)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, errDo := a.httpClient.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("xai device code request failed: %w", errDo)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("xai device code: close response body error: %v", errClose)
		}
	}()
	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return nil, fmt.Errorf("xai device code: read response: %w", errRead)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai device code request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var deviceCode DeviceCodeResponse
	if errUnmarshal := json.Unmarshal(body, &deviceCode); errUnmarshal != nil {
		return nil, fmt.Errorf("xai device code: parse response: %w", errUnmarshal)
	}
	if strings.TrimSpace(deviceCode.DeviceCode) == "" {
		return nil, fmt.Errorf("xai device code: response missing device_code")
	}
	if strings.TrimSpace(deviceCode.UserCode) == "" {
		return nil, fmt.Errorf("xai device code: response missing user_code")
	}
	if strings.TrimSpace(deviceCode.VerificationURI) == "" && strings.TrimSpace(deviceCode.VerificationURIComplete) == "" {
		return nil, fmt.Errorf("xai device code: response missing verification URI")
	}
	deviceCode.TokenEndpoint = strings.TrimSpace(tokenEndpoint)
	return &deviceCode, nil
}

// WaitForAuthorization polls until the user authorizes the device code.
func (a *XAIAuth) WaitForAuthorization(ctx context.Context, deviceCode *DeviceCodeResponse) (*AuthBundle, error) {
	tokenData, errPoll := a.PollForToken(ctx, deviceCode)
	if errPoll != nil {
		return nil, errPoll
	}
	tokenEndpoint := ""
	if deviceCode != nil {
		tokenEndpoint = strings.TrimSpace(deviceCode.TokenEndpoint)
	}
	return &AuthBundle{
		TokenData:     *tokenData,
		LastRefresh:   time.Now().UTC().Format(time.RFC3339),
		BaseURL:       DefaultAPIBaseURL,
		TokenEndpoint: tokenEndpoint,
	}, nil
}

// PollForToken polls the token endpoint until the device code is authorized.
func (a *XAIAuth) PollForToken(ctx context.Context, deviceCode *DeviceCodeResponse) (*TokenData, error) {
	if deviceCode == nil {
		return nil, fmt.Errorf("xai device code: response is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tokenEndpoint := strings.TrimSpace(deviceCode.TokenEndpoint)
	if tokenEndpoint == "" {
		discovery, errDiscover := a.Discover(ctx)
		if errDiscover != nil {
			return nil, errDiscover
		}
		tokenEndpoint = discovery.TokenEndpoint
	}
	interval := time.Duration(deviceCode.Interval) * time.Second
	if interval < defaultPollInterval {
		interval = defaultPollInterval
	}
	deadline := time.Now().Add(MaxPollDuration)
	if deviceCode.ExpiresIn > 0 {
		codeDeadline := time.Now().Add(time.Duration(deviceCode.ExpiresIn) * time.Second)
		if codeDeadline.Before(deadline) {
			deadline = codeDeadline
		}
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	firstAttempt := true
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("xai device code: context cancelled: %w", ctx.Err())
		case <-timer.C:
			if !firstAttempt && time.Now().After(deadline) {
				return nil, fmt.Errorf("xai device code expired")
			}
			firstAttempt = false
			tokenData, pollErr, nextInterval, shouldContinue := a.exchangeDeviceCode(ctx, tokenEndpoint, deviceCode.DeviceCode, interval)
			if tokenData != nil {
				return tokenData, nil
			}
			if !shouldContinue {
				return nil, pollErr
			}
			interval = nextInterval
			timer.Reset(interval)
		}
	}
}

func (a *XAIAuth) exchangeDeviceCode(ctx context.Context, tokenEndpoint, deviceCode string, interval time.Duration) (*TokenData, error, time.Duration, bool) {
	form := url.Values{
		"grant_type":  {DeviceCodeGrantType},
		"device_code": {strings.TrimSpace(deviceCode)},
		"client_id":   {ClientID},
	}
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(tokenEndpoint), strings.NewReader(form.Encode()))
	if errRequest != nil {
		return nil, fmt.Errorf("xai device token: create request: %w", errRequest), interval, false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, errDo := a.httpClient.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("xai device token request failed: %w", errDo), interval, false
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("xai device token: close response body error: %v", errClose)
		}
	}()
	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return nil, fmt.Errorf("xai device token: read response: %w", errRead), interval, false
	}
	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		IDToken          string `json:"id_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        int    `json:"expires_in"`
	}
	if errUnmarshal := json.Unmarshal(body, &payload); errUnmarshal != nil {
		return nil, fmt.Errorf("xai device token: parse response: %w", errUnmarshal), interval, false
	}
	if payload.Error != "" {
		switch payload.Error {
		case "authorization_pending":
			return nil, nil, interval, true
		case "slow_down":
			return nil, nil, interval + defaultPollInterval, true
		case "expired_token":
			return nil, fmt.Errorf("xai device code expired"), interval, false
		case "access_denied":
			return nil, fmt.Errorf("xai device authorization denied"), interval, false
		default:
			description := strings.TrimSpace(payload.ErrorDescription)
			if description != "" {
				return nil, fmt.Errorf("xai device token error: %s: %s", payload.Error, description), interval, false
			}
			return nil, fmt.Errorf("xai device token error: %s", payload.Error), interval, false
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai device token request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))), interval, false
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, fmt.Errorf("xai device token response missing access_token"), interval, false
	}
	email, subject := parseJWTIdentity(payload.IDToken)
	return buildTokenData(payload.AccessToken, payload.RefreshToken, payload.IDToken, payload.TokenType, payload.ExpiresIn, email, subject), nil, interval, false
}

// RefreshTokens refreshes an xAI access token.
func (a *XAIAuth) RefreshTokens(ctx context.Context, refreshToken, tokenEndpoint string) (*TokenData, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("xai token refresh: refresh token is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(tokenEndpoint) == "" {
		discovery, errDiscover := a.Discover(ctx)
		if errDiscover != nil {
			return nil, errDiscover
		}
		tokenEndpoint = discovery.TokenEndpoint
	}
	tokenEndpoint = strings.TrimSpace(tokenEndpoint)
	result, errRefresh, _ := xaiRefreshGroup.Do(refreshToken, func() (any, error) {
		return a.refreshTokensSingleFlight(context.WithoutCancel(ctx), refreshToken, tokenEndpoint)
	})
	if errRefresh != nil {
		return nil, errRefresh
	}
	tokenData, ok := result.(*TokenData)
	if !ok || tokenData == nil {
		return nil, fmt.Errorf("xai token refresh failed: invalid single-flight result")
	}
	return tokenData, nil
}

func (a *XAIAuth) refreshTokensSingleFlight(ctx context.Context, refreshToken, tokenEndpoint string) (*TokenData, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {ClientID},
		"refresh_token": {refreshToken},
	}
	return a.postTokenForm(ctx, tokenEndpoint, form)
}

func (a *XAIAuth) postTokenForm(ctx context.Context, tokenEndpoint string, form url.Values) (*TokenData, error) {
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(tokenEndpoint), strings.NewReader(form.Encode()))
	if errRequest != nil {
		return nil, fmt.Errorf("xai token request: create request: %w", errRequest)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, errDo := a.httpClient.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("xai token request failed: %w", errDo)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("xai token request: close response body error: %v", errClose)
		}
	}()
	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return nil, fmt.Errorf("xai token response: read body: %w", errRead)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai token request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if errUnmarshal := json.Unmarshal(body, &payload); errUnmarshal != nil {
		return nil, fmt.Errorf("xai token response: parse body: %w", errUnmarshal)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, fmt.Errorf("xai token response missing access_token")
	}
	email, subject := parseJWTIdentity(payload.IDToken)
	return buildTokenData(payload.AccessToken, payload.RefreshToken, payload.IDToken, payload.TokenType, payload.ExpiresIn, email, subject), nil
}

// CreateTokenStorage converts an auth bundle into persistable storage.
func (a *XAIAuth) CreateTokenStorage(bundle *AuthBundle) *TokenStorage {
	if bundle == nil {
		return nil
	}
	return &TokenStorage{
		Type:          "xai",
		AccessToken:   bundle.TokenData.AccessToken,
		RefreshToken:  bundle.TokenData.RefreshToken,
		IDToken:       bundle.TokenData.IDToken,
		TokenType:     bundle.TokenData.TokenType,
		ExpiresIn:     bundle.TokenData.ExpiresIn,
		Expire:        bundle.TokenData.Expire,
		LastRefresh:   bundle.LastRefresh,
		Email:         strings.TrimSpace(bundle.TokenData.Email),
		Subject:       bundle.TokenData.Subject,
		BaseURL:       firstNonEmpty(bundle.BaseURL, DefaultAPIBaseURL),
		TokenEndpoint: bundle.TokenEndpoint,
		AuthKind:      "oauth",
	}
}

func buildTokenData(accessToken, refreshToken, idToken, tokenType string, expiresIn int, email, subject string) *TokenData {
	tokenData := &TokenData{
		AccessToken: strings.TrimSpace(accessToken), RefreshToken: strings.TrimSpace(refreshToken),
		IDToken: strings.TrimSpace(idToken), TokenType: strings.TrimSpace(tokenType), ExpiresIn: expiresIn,
		Email: strings.TrimSpace(email), Subject: strings.TrimSpace(subject),
	}
	if expiresIn > 0 {
		tokenData.Expire = time.Now().Add(time.Duration(expiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	return tokenData
}

func parseJWTIdentity(token string) (email, subject string) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", ""
	}
	payload := parts[1] + strings.Repeat("=", (4-len(parts[1])%4)%4)
	raw, errDecode := base64.URLEncoding.DecodeString(payload)
	if errDecode != nil {
		return "", ""
	}
	var claims map[string]any
	if errUnmarshal := json.Unmarshal(raw, &claims); errUnmarshal != nil {
		return "", ""
	}
	if value, ok := claims["email"].(string); ok {
		email = strings.TrimSpace(value)
	}
	if value, ok := claims["sub"].(string); ok {
		subject = strings.TrimSpace(value)
	}
	return email, subject
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

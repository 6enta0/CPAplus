package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	xaiauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/xai"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// XAIAuthenticator implements the xAI Grok OAuth device-code flow.
type XAIAuthenticator struct{}

// NewXAIAuthenticator constructs a new xAI authenticator.
func NewXAIAuthenticator() Authenticator {
	return &XAIAuthenticator{}
}

// Provider returns the provider key for xAI.
func (XAIAuthenticator) Provider() string {
	return "xai"
}

// RefreshLead instructs the manager to refresh before token expiry.
func (XAIAuthenticator) RefreshLead() *time.Duration {
	lead := xaiauth.RefreshLead()
	return &lead
}

// Login launches the OAuth device-code flow to obtain xAI tokens.
func (authenticator XAIAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	authService := xaiauth.NewXAIAuth(cfg)
	fmt.Println("Starting xAI authentication...")
	deviceCode, errDevice := authService.StartDeviceFlow(ctx)
	if errDevice != nil {
		return nil, fmt.Errorf("xai: failed to start device flow: %w", errDevice)
	}
	verificationURL := strings.TrimSpace(deviceCode.VerificationURIComplete)
	if verificationURL == "" {
		verificationURL = strings.TrimSpace(deviceCode.VerificationURI)
	}
	fmt.Printf("\nTo authenticate, please visit:\n%s\n\n", verificationURL)
	if deviceCode.UserCode != "" {
		fmt.Printf("Then enter this code: %s\n\n", deviceCode.UserCode)
	}
	if !opts.NoBrowser {
		if browser.IsAvailable() {
			if errOpen := browser.OpenURL(verificationURL); errOpen != nil {
				log.Warnf("Failed to open browser automatically: %v", errOpen)
			} else {
				fmt.Println("Browser opened automatically.")
			}
		} else {
			log.Warn("No browser available; please open the URL manually")
		}
	}
	fmt.Println("Waiting for authorization...")
	if deviceCode.ExpiresIn > 0 {
		fmt.Printf("(This will timeout in %d seconds if not authorized)\n", deviceCode.ExpiresIn)
	}
	bundle, errWait := authService.WaitForAuthorization(ctx, deviceCode)
	if errWait != nil {
		return nil, fmt.Errorf("xai: %w", errWait)
	}
	tokenStorage := authService.CreateTokenStorage(bundle)
	if tokenStorage == nil || strings.TrimSpace(tokenStorage.AccessToken) == "" {
		return nil, fmt.Errorf("xai token storage missing access token")
	}
	fileName := xaiauth.CredentialFileName(tokenStorage.Email, tokenStorage.Subject)
	label := strings.TrimSpace(tokenStorage.Email)
	if label == "" {
		label = "xAI"
	}
	metadata := xaiTokenMetadata(tokenStorage)
	fmt.Println("xAI authentication successful")
	return &coreauth.Auth{
		ID:       fileName,
		Provider: authenticator.Provider(),
		FileName: fileName,
		Label:    label,
		Storage:  tokenStorage,
		Metadata: metadata,
		Attributes: map[string]string{
			"auth_kind": "oauth",
			"base_url":  tokenStorage.BaseURL,
		},
	}, nil
}

// RefreshXAIAuth refreshes xAI OAuth metadata without binding request execution.
func RefreshXAIAuth(ctx context.Context, cfg *config.Config, auth *coreauth.Auth) (*coreauth.Auth, error) {
	if auth == nil {
		return nil, fmt.Errorf("xai auth refresh: auth is nil")
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "xai") {
		return nil, fmt.Errorf("xai auth refresh: unexpected provider %q", auth.Provider)
	}
	refreshToken := metadataString(auth.Metadata, "refresh_token")
	if refreshToken == "" {
		return auth.Clone(), nil
	}
	tokenEndpoint := metadataString(auth.Metadata, "token_endpoint")
	authService := xaiauth.NewXAIAuthWithProxyURL(cfg, auth.ProxyURL)
	tokenData, errRefresh := authService.RefreshTokens(ctx, refreshToken, tokenEndpoint)
	if errRefresh != nil {
		return nil, errRefresh
	}
	updated := auth.Clone()
	if updated.Metadata == nil {
		updated.Metadata = make(map[string]any)
	}
	updated.Metadata["type"] = "xai"
	updated.Metadata["auth_kind"] = "oauth"
	updated.Metadata["access_token"] = tokenData.AccessToken
	if tokenData.RefreshToken != "" {
		updated.Metadata["refresh_token"] = tokenData.RefreshToken
	}
	if tokenData.IDToken != "" {
		updated.Metadata["id_token"] = tokenData.IDToken
	}
	if tokenData.TokenType != "" {
		updated.Metadata["token_type"] = tokenData.TokenType
	}
	if tokenData.ExpiresIn > 0 {
		updated.Metadata["expires_in"] = tokenData.ExpiresIn
	}
	if tokenData.Expire != "" {
		updated.Metadata["expired"] = tokenData.Expire
	}
	if tokenData.Email != "" {
		updated.Metadata["email"] = tokenData.Email
	}
	if tokenData.Subject != "" {
		updated.Metadata["sub"] = tokenData.Subject
	}
	if tokenEndpoint != "" {
		updated.Metadata["token_endpoint"] = tokenEndpoint
	}
	if metadataString(updated.Metadata, "base_url") == "" {
		updated.Metadata["base_url"] = xaiauth.DefaultAPIBaseURL
	}
	updated.Metadata["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	if updated.Attributes == nil {
		updated.Attributes = make(map[string]string)
	}
	updated.Attributes["auth_kind"] = "oauth"
	if strings.TrimSpace(updated.Attributes["base_url"]) == "" {
		updated.Attributes["base_url"] = xaiauth.DefaultAPIBaseURL
	}
	return updated, nil
}

func xaiTokenMetadata(storage *xaiauth.TokenStorage) map[string]any {
	metadata := map[string]any{
		"type":           "xai",
		"access_token":   storage.AccessToken,
		"refresh_token":  storage.RefreshToken,
		"id_token":       storage.IDToken,
		"token_type":     storage.TokenType,
		"expires_in":     storage.ExpiresIn,
		"expired":        storage.Expire,
		"last_refresh":   storage.LastRefresh,
		"base_url":       storage.BaseURL,
		"token_endpoint": storage.TokenEndpoint,
		"auth_kind":      "oauth",
	}
	if storage.Email != "" {
		metadata["email"] = storage.Email
	}
	if storage.Subject != "" {
		metadata["sub"] = storage.Subject
	}
	return metadata
}

func metadataString(metadata map[string]any, key string) string {
	if value, ok := metadata[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

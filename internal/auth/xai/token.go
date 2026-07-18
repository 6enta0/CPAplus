package xai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
	log "github.com/sirupsen/logrus"
)

// TokenStorage stores xAI OAuth credentials on disk.
type TokenStorage struct {
	Type          string `json:"type"`
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	IDToken       string `json:"id_token,omitempty"`
	TokenType     string `json:"token_type,omitempty"`
	ExpiresIn     int    `json:"expires_in,omitempty"`
	Expire        string `json:"expired,omitempty"`
	LastRefresh   string `json:"last_refresh,omitempty"`
	Email         string `json:"email,omitempty"`
	Subject       string `json:"sub,omitempty"`
	BaseURL       string `json:"base_url,omitempty"`
	TokenEndpoint string `json:"token_endpoint,omitempty"`
	AuthKind      string `json:"auth_kind,omitempty"`

	Metadata map[string]any `json:"-"`
}

// SetMetadata allows the token store to merge status fields before saving.
func (ts *TokenStorage) SetMetadata(meta map[string]any) {
	ts.Metadata = meta
}

// SaveTokenToFile writes xAI credentials to a JSON auth file.
func (ts *TokenStorage) SaveTokenToFile(authFilePath string) error {
	misc.LogSavingCredentials(authFilePath)
	ts.Type = "xai"
	ts.AuthKind = "oauth"
	if errMkdir := os.MkdirAll(filepath.Dir(authFilePath), 0o700); errMkdir != nil {
		return fmt.Errorf("xai token storage: create directory: %w", errMkdir)
	}
	file, errCreate := os.Create(authFilePath)
	if errCreate != nil {
		return fmt.Errorf("xai token storage: create token file: %w", errCreate)
	}
	defer func() {
		if errClose := file.Close(); errClose != nil {
			log.Errorf("xai token storage: close token file error: %v", errClose)
		}
	}()

	data, errMerge := misc.MergeMetadata(ts, ts.Metadata)
	if errMerge != nil {
		return fmt.Errorf("xai token storage: merge metadata: %w", errMerge)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if errEncode := encoder.Encode(data); errEncode != nil {
		return fmt.Errorf("xai token storage: write token file: %w", errEncode)
	}
	return nil
}

// CredentialFileName returns the filename used for xAI credentials.
func CredentialFileName(email, subject string) string {
	if email = sanitizeFileSegment(email); email != "" {
		return fmt.Sprintf("xai-%s.json", email)
	}
	if subject = sanitizeFileSegment(subject); subject != "" {
		return fmt.Sprintf("xai-%s.json", subject)
	}
	return fmt.Sprintf("xai-%d.json", time.Now().UnixMilli())
}

func sanitizeFileSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '@' || char == '.' || char == '_' || char == '-':
			builder.WriteRune(char)
		default:
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

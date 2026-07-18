package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestXAIAuthenticatorProviderAndRefreshLead(t *testing.T) {
	authenticator := NewXAIAuthenticator()
	if authenticator.Provider() != "xai" {
		t.Fatalf("Provider() = %q, want xai", authenticator.Provider())
	}
	if lead := authenticator.RefreshLead(); lead == nil || *lead <= 0 {
		t.Fatalf("RefreshLead() = %v, want positive duration", lead)
	}
}

func TestRefreshXAIAuthPreservesRefreshTokenWhenNotRotated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-access", "token_type": "Bearer", "expires_in": 3600,
		})
	}))
	defer server.Close()
	auth := &coreauth.Auth{
		ID: "xai-user.json", Provider: "xai", FileName: "xai-user.json",
		Metadata: map[string]any{
			"refresh_token": "old-refresh", "token_endpoint": server.URL,
		},
	}
	updated, errRefresh := RefreshXAIAuth(context.Background(), &config.Config{}, auth)
	if errRefresh != nil {
		t.Fatalf("RefreshXAIAuth() error = %v", errRefresh)
	}
	if metadataString(updated.Metadata, "access_token") != "new-access" || metadataString(updated.Metadata, "refresh_token") != "old-refresh" {
		t.Fatalf("updated metadata = %#v", updated.Metadata)
	}
	if metadataString(updated.Metadata, "auth_kind") != "oauth" || metadataString(updated.Metadata, "last_refresh") == "" {
		t.Fatalf("auth_kind/last_refresh missing: %#v", updated.Metadata)
	}
	if updated.Attributes["base_url"] != "https://api.x.ai/v1" {
		t.Fatalf("base_url = %q", updated.Attributes["base_url"])
	}
	if auth.Metadata["access_token"] != nil {
		t.Fatal("RefreshXAIAuth mutated the input auth")
	}
}

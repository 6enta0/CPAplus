package management

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/watcher/synthesizer"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestOpenAICompatibilityWithAuthIndexIncludesIDsAndDisabledKeyIndex(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		OpenAICompatibility: []config.OpenAICompatibility{{
			ID:       "op-stable",
			Name:     "Shared",
			Prefix:   "deepseek",
			BaseURL:  "https://one.example/v1",
			Disabled: false,
			APIKeyEntries: []config.OpenAICompatibilityAPIKey{{
				ID:       "key-stable",
				APIKey:   "key-a",
				Disabled: true,
			}},
		}},
	}
	h := NewHandlerWithoutConfigFilePath(cfg, nil)

	got := h.openAICompatibilityWithAuthIndex()
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "op-stable" {
		t.Fatalf("provider id = %q, want op-stable", got[0].ID)
	}
	if len(got[0].APIKeyEntries) != 1 {
		t.Fatalf("api-key entries len = %d, want 1", len(got[0].APIKeyEntries))
	}
	key := got[0].APIKeyEntries[0]
	if key.ID != "key-stable" {
		t.Fatalf("key id = %q, want key-stable", key.ID)
	}
	if !key.Disabled {
		t.Fatal("key disabled flag should be returned")
	}
	if key.AuthIndex == "" {
		t.Fatal("disabled key should still return an auth_index for historical stats")
	}
}

func TestInteractionsKeysWithAuthIndexUsesSynthesizedAuthIdentity(t *testing.T) {
	entry := config.GeminiKey{APIKey: "interactions-key", BaseURL: "https://interactions.example.com"}
	id, _ := synthesizer.NewStableIDGenerator().Next("gemini-interactions:apikey", entry.APIKey, entry.BaseURL)
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       id,
		Provider: "gemini-interactions",
		Status:   coreauth.StatusActive,
	}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	h := &Handler{cfg: &config.Config{InteractionsKey: []config.GeminiKey{entry}}, authManager: manager}

	got := h.interactionsKeysWithAuthIndex()
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	listed := manager.List()
	if len(listed) != 1 {
		t.Fatalf("manager auth count = %d, want 1", len(listed))
	}
	wantIndex := listed[0].EnsureIndex()
	if got[0].AuthIndex == "" || got[0].AuthIndex != wantIndex {
		t.Fatalf("auth-index = %q, want %q", got[0].AuthIndex, wantIndex)
	}
}

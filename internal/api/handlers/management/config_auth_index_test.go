package management

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
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

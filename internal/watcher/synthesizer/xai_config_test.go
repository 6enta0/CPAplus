package synthesizer

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestConfigSynthesizerXAIKeys(t *testing.T) {
	ctx := &SynthesisContext{
		Config: &config.Config{XAIKey: []config.XAIKey{{
			APIKey:         "xai-key-123",
			Priority:       4,
			Prefix:         "grok",
			BaseURL:        "https://api.x.ai/v1",
			ProxyURL:       "http://proxy.local",
			Websockets:     true,
			Headers:        map[string]string{"X-Custom": "value"},
			Models:         []config.XAIModel{{Name: "grok-4.5", Alias: "grok-latest"}},
			ExcludedModels: []string{"grok-3-*"},
		}}},
		Now:         time.Now(),
		IDGenerator: NewStableIDGenerator(),
	}

	auths, errSynthesize := NewConfigSynthesizer().Synthesize(ctx)
	if errSynthesize != nil {
		t.Fatalf("Synthesize() error = %v", errSynthesize)
	}
	if len(auths) != 1 {
		t.Fatalf("auth count = %d, want 1", len(auths))
	}
	auth := auths[0]
	if auth.Provider != "xai" || auth.Label != "xai-apikey" {
		t.Fatalf("provider/label = %q/%q", auth.Provider, auth.Label)
	}
	if auth.Attributes["source"] == "" || auth.Attributes["auth_kind"] != "apikey" {
		t.Fatalf("source/auth_kind = %q/%q", auth.Attributes["source"], auth.Attributes["auth_kind"])
	}
	if auth.Attributes["priority"] != "4" || auth.Attributes["websockets"] != "true" {
		t.Fatalf("priority/websockets = %q/%q", auth.Attributes["priority"], auth.Attributes["websockets"])
	}
	if auth.Attributes["base_url"] != "https://api.x.ai/v1" || auth.ProxyURL != "http://proxy.local" {
		t.Fatalf("base_url/proxy = %q/%q", auth.Attributes["base_url"], auth.ProxyURL)
	}
	if auth.Attributes["header:X-Custom"] != "value" || auth.Attributes["models_hash"] == "" {
		t.Fatalf("header/models_hash = %q/%q", auth.Attributes["header:X-Custom"], auth.Attributes["models_hash"])
	}
	if auth.Attributes["excluded_models"] != "grok-3-*" {
		t.Fatalf("excluded_models = %q", auth.Attributes["excluded_models"])
	}
}

func TestConfigSynthesizerXAIIdentitySurvivesMutableUpdates(t *testing.T) {
	synthesize := func(entry config.XAIKey) (string, string) {
		auths, errSynthesize := NewConfigSynthesizer().Synthesize(&SynthesisContext{
			Config:      &config.Config{XAIKey: []config.XAIKey{entry}},
			Now:         time.Now(),
			IDGenerator: NewStableIDGenerator(),
		})
		if errSynthesize != nil || len(auths) != 1 {
			t.Fatalf("Synthesize() error/auths = %v/%d", errSynthesize, len(auths))
		}
		return auths[0].ID, auths[0].EnsureIndex()
	}

	entry := config.XAIKey{APIKey: "same-key", BaseURL: "https://api.x.ai/v1"}
	baseID, baseIndex := synthesize(entry)
	entry.Priority = 9
	entry.Prefix = "team"
	entry.ProxyURL = "http://proxy.local"
	entry.Websockets = true
	entry.Headers = map[string]string{"X-Test": "value"}
	entry.Models = []config.XAIModel{{Name: "grok-4.5", Alias: "grok-latest"}}
	entry.ExcludedModels = []string{"grok-3-*"}
	updatedID, updatedIndex := synthesize(entry)
	if updatedID != baseID || updatedIndex != baseIndex {
		t.Fatalf("mutable update changed identity: id %q -> %q, index %q -> %q", baseID, updatedID, baseIndex, updatedIndex)
	}
}

func TestFileSynthesizerLoadsDisabledXAIOAuth(t *testing.T) {
	authDir := t.TempDir()
	path := filepath.Join(authDir, "xai-user.json")
	data, errMarshal := json.Marshal(map[string]any{
		"type": "xai", "access_token": "secret", "disabled": true, "base_url": "https://api.x.ai/v1",
	})
	if errMarshal != nil {
		t.Fatalf("json.Marshal() error = %v", errMarshal)
	}
	auths := SynthesizeAuthFile(&SynthesisContext{
		Config: &config.Config{}, AuthDir: authDir, Now: time.Now(), IDGenerator: NewStableIDGenerator(),
	}, path, data)
	if len(auths) != 1 {
		t.Fatalf("auth count = %d, want 1", len(auths))
	}
	auth := auths[0]
	if auth.Provider != "xai" || !auth.Disabled || auth.Attributes["auth_kind"] != "oauth" {
		t.Fatalf("xAI OAuth auth = %#v", auth)
	}
	if auth.Attributes["base_url"] != "https://api.x.ai/v1" {
		t.Fatalf("base_url = %q", auth.Attributes["base_url"])
	}
}

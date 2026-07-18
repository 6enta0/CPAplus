package auth

import (
	"context"
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestXAIAPIKeyModelAliasResolvesUpstreamName(t *testing.T) {
	cfg := &internalconfig.Config{XAIKey: []internalconfig.XAIKey{{
		APIKey:  "xai-key",
		BaseURL: "https://api.x.ai/v1",
		Models:  []internalconfig.XAIModel{{Name: "grok-4.5", Alias: "grok-latest"}},
	}}}
	mgr := NewManager(nil, nil, nil)
	mgr.SetConfig(cfg)
	auth := &Auth{
		ID:       "xai-auth",
		Provider: "xai",
		Attributes: map[string]string{
			"api_key":  "xai-key",
			"base_url": "https://api.x.ai/v1",
		},
	}
	if _, errRegister := mgr.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	if got := mgr.lookupAPIKeyUpstreamModel(auth.ID, "grok-latest(high)"); got != "grok-4.5(high)" {
		t.Fatalf("lookupAPIKeyUpstreamModel() = %q, want grok-4.5(high)", got)
	}
	if got := mgr.applyAPIKeyModelAlias(auth, "grok-latest"); got != "grok-4.5" {
		t.Fatalf("applyAPIKeyModelAlias() = %q, want grok-4.5", got)
	}
}

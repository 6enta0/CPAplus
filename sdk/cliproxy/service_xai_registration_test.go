package cliproxy

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestEnsureExecutorsForXAIAuthBindsNativeExecutor(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{cfg: &sdkconfig.Config{}, coreManager: manager}
	service.ensureExecutorsForAuth(&coreauth.Auth{Provider: "xai"})
	if registered, ok := manager.Executor("xai"); !ok {
		t.Fatal("xAI executor was not registered")
	} else if _, ok := registered.(*executor.XAIExecutor); !ok {
		t.Fatalf("xAI executor = %T, want *executor.XAIExecutor", registered)
	}
}

func TestRegisterModelsForXAIAuthUsesConfiguredAliasesAndExclusions(t *testing.T) {
	auth := &coreauth.Auth{
		ID: "xai-config-models", Provider: "xai", Status: coreauth.StatusActive,
		Attributes: map[string]string{"api_key": "xai-key", "base_url": "https://api.x.ai/v1", "auth_kind": "apikey"},
	}
	service := &Service{cfg: &sdkconfig.Config{XAIKey: []config.XAIKey{{
		APIKey: "xai-key", BaseURL: "https://api.x.ai/v1",
		Models:         []config.XAIModel{{Name: "grok-4.5", Alias: "grok-latest"}, {Name: "grok-3-mini", Alias: "grok-mini"}},
		ExcludedModels: []string{"grok-mini"},
	}}}}
	registry := GlobalModelRegistry()
	registry.UnregisterClient(auth.ID)
	t.Cleanup(func() { registry.UnregisterClient(auth.ID) })
	service.registerModelsForAuth(auth)
	if !registry.ClientSupportsModel(auth.ID, "grok-latest") {
		t.Fatal("grok-latest was not registered")
	}
	if registry.ClientSupportsModel(auth.ID, "grok-mini") {
		t.Fatal("grok-mini should be excluded")
	}
}

func TestRegisterModelsForXAIOAuthUsesNativeProviderCatalog(t *testing.T) {
	auth := &coreauth.Auth{ID: "xai-oauth-models", Provider: "xai", Status: coreauth.StatusActive, Attributes: map[string]string{"auth_kind": "oauth"}}
	service := &Service{cfg: &sdkconfig.Config{}}
	registry := GlobalModelRegistry()
	registry.UnregisterClient(auth.ID)
	t.Cleanup(func() { registry.UnregisterClient(auth.ID) })
	service.registerModelsForAuth(auth)
	if !registry.ClientSupportsModel(auth.ID, "grok-4.5") {
		t.Fatal("embedded xAI catalog was not registered")
	}
}

package cliproxy

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestEnsureExecutorsForInteractionsAuth(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{cfg: &sdkconfig.Config{}, coreManager: manager}
	service.ensureExecutorsForAuth(&coreauth.Auth{Provider: "gemini-interactions"})

	registered, ok := manager.Executor("gemini-interactions")
	if !ok {
		t.Fatal("gemini-interactions executor was not registered")
	}
	if _, ok = registered.(*executor.GeminiExecutor); !ok {
		t.Fatalf("executor type = %T, want *executor.GeminiExecutor", registered)
	}
	if registered.Identifier() != "gemini-interactions" {
		t.Fatalf("identifier = %q, want gemini-interactions", registered.Identifier())
	}
}

func TestRegisterModelsForInteractionsAuthUsesConfiguredModelsAndExclusions(t *testing.T) {
	auth := &coreauth.Auth{
		ID:       "interactions-model-registration-auth",
		Provider: "gemini-interactions",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"api_key":   "interactions-key",
			"auth_kind": "apikey",
		},
	}
	service := &Service{cfg: &sdkconfig.Config{InteractionsKey: []config.GeminiKey{{
		APIKey: "interactions-key",
		Models: []config.GeminiModel{
			{Name: "gemini-3-pro-preview", Alias: "native-pro"},
			{Name: "gemini-3-flash-preview", Alias: "native-flash"},
		},
		ExcludedModels: []string{"native-flash"},
	}}}}
	registry := GlobalModelRegistry()
	registry.UnregisterClient(auth.ID)
	t.Cleanup(func() { registry.UnregisterClient(auth.ID) })

	service.registerModelsForAuth(auth)

	if !registry.ClientSupportsModel(auth.ID, "native-pro") {
		t.Fatal("native-pro was not registered")
	}
	if registry.ClientSupportsModel(auth.ID, "native-flash") {
		t.Fatal("native-flash should be excluded")
	}
}

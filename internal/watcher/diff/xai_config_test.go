package diff

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestBuildConfigChangeDetailsXAIKeysRedactsSecrets(t *testing.T) {
	oldCfg := &config.Config{XAIKey: []config.XAIKey{{
		APIKey: "old-secret", Priority: 1, Prefix: "old", BaseURL: "https://old.example/v1",
	}}}
	newCfg := &config.Config{XAIKey: []config.XAIKey{{
		APIKey: "new-secret", Priority: 2, Prefix: "new", BaseURL: "https://new.example/v1", Websockets: true,
	}}}

	details := strings.Join(BuildConfigChangeDetails(oldCfg, newCfg), "\n")
	for _, expected := range []string{
		"xai[0].base-url: https://old.example/v1 -> https://new.example/v1",
		"xai[0].prefix: old -> new",
		"xai[0].priority: 1 -> 2",
		"xai[0].websockets: false -> true",
		"xai[0].api-key: updated",
	} {
		if !strings.Contains(details, expected) {
			t.Fatalf("details missing %q: %s", expected, details)
		}
	}
	if strings.Contains(details, "old-secret") || strings.Contains(details, "new-secret") {
		t.Fatalf("details leaked key material: %s", details)
	}
}

func TestBuildConfigChangeDetailsXAIKeyCount(t *testing.T) {
	details := strings.Join(BuildConfigChangeDetails(&config.Config{}, &config.Config{
		XAIKey: []config.XAIKey{{APIKey: "secret", BaseURL: "https://api.x.ai/v1"}},
	}), "\n")
	if !strings.Contains(details, "xai-api-key count: 0 -> 1") {
		t.Fatalf("details = %s", details)
	}
}

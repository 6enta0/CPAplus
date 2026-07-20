package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseConfigBytesXAIAPIKeyMatchesV6CodexShape(t *testing.T) {
	var cfg Config
	errParse := yaml.Unmarshal([]byte(`xai-api-key:
  - name: " Team xAI "
    api-key: " xai-key "
    priority: 3
    prefix: " team-xai "
    base-url: " https://api.x.ai/v1 "
    websockets: true
    proxy-url: " http://proxy.local "
    headers:
      X-Custom: value
    models:
      - name: grok-4.5
        alias: grok-latest
    excluded-models:
      - " grok-3-* "
  - api-key: dropped
    base-url: " "
`), &cfg)
	if errParse != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", errParse)
	}
	cfg.SanitizeXAIKeys()
	if len(cfg.XAIKey) != 1 {
		t.Fatalf("xai-api-key count = %d, want 1", len(cfg.XAIKey))
	}
	entry := cfg.XAIKey[0]
	if entry.Name != "Team xAI" {
		t.Fatalf("name = %q, want Team xAI", entry.Name)
	}
	if entry.APIKey != " xai-key " {
		t.Fatalf("api-key = %q, want original v6 Codex-compatible value", entry.APIKey)
	}
	if entry.Priority != 3 || entry.Prefix != "team-xai" {
		t.Fatalf("priority/prefix = %d/%q, want 3/team-xai", entry.Priority, entry.Prefix)
	}
	if entry.BaseURL != "https://api.x.ai/v1" || !entry.Websockets {
		t.Fatalf("base-url/websockets = %q/%t", entry.BaseURL, entry.Websockets)
	}
	if entry.Headers["X-Custom"] != "value" {
		t.Fatalf("X-Custom header = %q, want value", entry.Headers["X-Custom"])
	}
	if len(entry.Models) != 1 || entry.Models[0].Name != "grok-4.5" || entry.Models[0].Alias != "grok-latest" {
		t.Fatalf("models = %#v", entry.Models)
	}
	if len(entry.ExcludedModels) != 1 || entry.ExcludedModels[0] != "grok-3-*" {
		t.Fatalf("excluded-models = %#v, want [grok-3-*]", entry.ExcludedModels)
	}
}

func TestParseConfigBytesWithoutXAIFieldsRemainsValid(t *testing.T) {
	var cfg Config
	errParse := yaml.Unmarshal([]byte("port: 8317\n"), &cfg)
	if errParse != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", errParse)
	}
	if len(cfg.XAIKey) != 0 {
		t.Fatalf("xai-api-key count = %d, want 0", len(cfg.XAIKey))
	}
}

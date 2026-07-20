package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestProviderModelYAMLOmitsEmptyAlias(t *testing.T) {
	tests := []struct {
		name  string
		model any
	}{
		{name: "claude", model: ClaudeModel{Name: "claude-sonnet"}},
		{name: "codex", model: CodexModel{Name: "gpt-5"}},
		{name: "gemini", model: GeminiModel{Name: "gemini-pro"}},
		{name: "openai compatibility", model: OpenAICompatibilityModel{Name: "gpt-4o"}},
		{name: "vertex compatibility", model: VertexCompatModel{Name: "gemini-pro"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, errMarshal := yaml.Marshal(tt.model)
			if errMarshal != nil {
				t.Fatalf("yaml.Marshal() error = %v", errMarshal)
			}
			if strings.Contains(string(raw), "alias:") {
				t.Fatalf("empty alias was serialized:\n%s", raw)
			}
		})
	}
}

func TestProviderModelYAMLPreservesNonEmptyAlias(t *testing.T) {
	raw, errMarshal := yaml.Marshal(OpenAICompatibilityModel{Name: "GPT-5", Alias: "gpt-5"})
	if errMarshal != nil {
		t.Fatalf("yaml.Marshal() error = %v", errMarshal)
	}
	if !strings.Contains(string(raw), "alias: gpt-5") {
		t.Fatalf("non-empty alias was not serialized:\n%s", raw)
	}
}

func TestSaveConfigPreserveCommentsRemovesEmptyModelAlias(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	rawConfig := []byte(`codex-api-key:
  - api-key: test-key
    base-url: https://example.com/v1
    models:
      - name: gpt-5
        alias: ""
`)
	if errWrite := os.WriteFile(configPath, rawConfig, 0o600); errWrite != nil {
		t.Fatalf("os.WriteFile() error = %v", errWrite)
	}

	var cfg Config
	if errUnmarshal := yaml.Unmarshal(rawConfig, &cfg); errUnmarshal != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", errUnmarshal)
	}
	if errSave := SaveConfigPreserveComments(configPath, &cfg); errSave != nil {
		t.Fatalf("SaveConfigPreserveComments() error = %v", errSave)
	}

	saved, errRead := os.ReadFile(configPath)
	if errRead != nil {
		t.Fatalf("os.ReadFile() error = %v", errRead)
	}
	if strings.Contains(string(saved), "alias:") {
		t.Fatalf("empty alias remained after save:\n%s", saved)
	}
}

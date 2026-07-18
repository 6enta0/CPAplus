package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSanitizeInteractionsKeysNormalizesAndDeduplicates(t *testing.T) {
	cfg := &Config{InteractionsKey: []GeminiKey{
		{APIKey: " key ", BaseURL: " https://example.com ", Prefix: "/team/", Headers: map[string]string{" X-Test ": " value "}},
		{APIKey: "key", BaseURL: "https://example.com"},
		{APIKey: "other"},
	}}
	cfg.SanitizeInteractionsKeys()
	if len(cfg.InteractionsKey) != 2 {
		t.Fatalf("key count = %d, want 2", len(cfg.InteractionsKey))
	}
	if cfg.InteractionsKey[0].APIKey != "key" || cfg.InteractionsKey[0].BaseURL != "https://example.com" || cfg.InteractionsKey[0].Prefix != "team" {
		t.Fatalf("first entry not normalized: %+v", cfg.InteractionsKey[0])
	}
}

func TestInteractionsKeysYAMLRoundTripAndLegacyAbsence(t *testing.T) {
	var legacy Config
	if err := yaml.Unmarshal([]byte("gemini-api-key:\n  - api-key: legacy\n"), &legacy); err != nil {
		t.Fatal(err)
	}
	if len(legacy.InteractionsKey) != 0 {
		t.Fatalf("legacy config unexpectedly populated interactions keys: %+v", legacy.InteractionsKey)
	}

	original := Config{InteractionsKey: []GeminiKey{{APIKey: "native-key", Prefix: "native", BaseURL: "https://example.com"}}}
	raw, err := yaml.Marshal(&original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Config
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.InteractionsKey) != 1 || decoded.InteractionsKey[0].APIKey != "native-key" {
		t.Fatalf("interactions keys did not round-trip: %+v", decoded.InteractionsKey)
	}
}

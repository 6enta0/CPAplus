package interactions

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	"github.com/tidwall/gjson"
)

func TestApplierWritesNativeInteractionsThinkingFields(t *testing.T) {
	out, errApply := NewApplier().Apply([]byte(`{"generation_config":{"thinkingConfig":{"thinkingBudget":1024,"includeThoughts":true}}}`), thinking.ThinkingConfig{Mode: thinking.ModeLevel, Level: thinking.LevelHigh}, nil)
	if errApply != nil {
		t.Fatalf("Apply() error = %v", errApply)
	}
	if got := gjson.GetBytes(out, "generation_config.thinking_level").String(); got != "high" {
		t.Fatalf("thinking_level = %q, want high. Body: %s", got, out)
	}
	if got := gjson.GetBytes(out, "generation_config.thinking_summaries").String(); got != "auto" {
		t.Fatalf("thinking_summaries = %q, want auto. Body: %s", got, out)
	}
	if gjson.GetBytes(out, "generation_config.thinkingConfig").Exists() {
		t.Fatalf("legacy thinkingConfig remained: %s", out)
	}
}

func TestApplierNoneDisablesSummaries(t *testing.T) {
	out, errApply := NewApplier().Apply([]byte(`{"generation_config":{"thinking_level":"high","thinking_summaries":"detailed"}}`), thinking.ThinkingConfig{Mode: thinking.ModeNone}, nil)
	if errApply != nil {
		t.Fatalf("Apply() error = %v", errApply)
	}
	if got := gjson.GetBytes(out, "generation_config.thinking_summaries").String(); got != "none" {
		t.Fatalf("thinking_summaries = %q, want none. Body: %s", got, out)
	}
}

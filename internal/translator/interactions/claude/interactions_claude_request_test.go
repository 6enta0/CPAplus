package claude

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertClaudeRequestToInteractionsPreservesSystemAndMessage(t *testing.T) {
	raw := []byte(`{"model":"claude-test","system":"Be concise","messages":[{"role":"user","content":"hello"}]}`)
	out := ConvertClaudeRequestToInteractions("claude-test", raw, false)
	if got := gjson.GetBytes(out, "system_instruction").String(); got != "Be concise" {
		t.Fatalf("system = %q", got)
	}
	if got := gjson.GetBytes(out, "input.0.content.0.text").String(); got != "hello" {
		t.Fatalf("input text = %q", got)
	}
}

package interactions

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertInteractionsRequestToClaudePreservesSystemToolAndInput(t *testing.T) {
	raw := []byte(`{"model":"claude-test","system_instruction":"Be concise","input":[{"type":"user_input","content":[{"type":"text","text":"hello"}]}],"tools":[{"type":"function","name":"lookup","description":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}]}`)
	out := ConvertInteractionsRequestToClaude("claude-test", raw, false)
	if got := gjson.GetBytes(out, "system").String(); got != "Be concise" {
		t.Fatalf("system = %q", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.text").String(); got != "hello" {
		t.Fatalf("input text = %q", got)
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "lookup" {
		t.Fatalf("tool name = %q", got)
	}
}

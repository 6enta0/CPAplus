package interactions

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertInteractionsRequestToCodexPreservesInstructionsAndToolChoice(t *testing.T) {
	raw := []byte(`{"model":"gpt-test","system_instruction":"Follow policy","input":[{"type":"user_input","content":"hello"}],"generation_config":{"tool_choice":"auto"}}`)
	out := ConvertInteractionsRequestToCodex("gpt-test", raw, true)
	if got := gjson.GetBytes(out, "instructions").String(); got != "Follow policy" {
		t.Fatalf("instructions = %q", got)
	}
	if got := gjson.GetBytes(out, "input.0.content.0.text").String(); got != "hello" {
		t.Fatalf("input text = %q", got)
	}
	if !gjson.GetBytes(out, "stream").Bool() {
		t.Fatal("stream was not preserved")
	}
}

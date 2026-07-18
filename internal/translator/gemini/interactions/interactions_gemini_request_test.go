package interactions

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertInteractionsRequestToGeminiAndBackPreservesText(t *testing.T) {
	raw := []byte(`{"model":"gemini-test","input":[{"type":"user_input","content":[{"type":"text","text":"hello"}]}],"generation_config":{"temperature":0.2}}`)
	gemini := ConvertInteractionsRequestToGemini("gemini-test", raw, false)
	if got := gjson.GetBytes(gemini, "contents.0.parts.0.text").String(); got != "hello" {
		t.Fatalf("Gemini text = %q", got)
	}
	back := ConvertGeminiRequestToInteractions("gemini-test", []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`), false)
	if got := gjson.GetBytes(back, "input.0.content.0.text").String(); got != "hello" {
		t.Fatalf("Interactions text = %q", got)
	}
}

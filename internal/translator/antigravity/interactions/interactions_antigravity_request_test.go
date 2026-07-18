package interactions

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertInteractionsRequestToAntigravityMapsGenerationAndTools(t *testing.T) {
	raw := []byte(`{"model":"gemini-test","input":[{"type":"user_input","content":[{"type":"text","text":"hello"}]}],"generation_config":{"temperature":0.2}}`)
	out := ConvertInteractionsRequestToAntigravity("gemini-test", raw, false)
	if got := gjson.GetBytes(out, "request.contents.0.parts.0.text").String(); got != "hello" {
		t.Fatalf("input text = %q", got)
	}
	if got := gjson.GetBytes(out, "request.generationConfig.temperature").Float(); got != 0.2 {
		t.Fatalf("temperature = %v", got)
	}
}

package chat_completions

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertOpenAIRequestToGemini_MapsMaxTokens(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "max_tokens", raw: `{"max_tokens":123}`, want: 123},
		{name: "max_completion_tokens", raw: `{"max_completion_tokens":456}`, want: 456},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := ConvertOpenAIRequestToGemini("gemini-test", []byte(tt.raw), false)
			if got := gjson.GetBytes(out, "generationConfig.maxOutputTokens").Int(); got != tt.want {
				t.Fatalf("maxOutputTokens = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestConvertOpenAIRequestToGemini_StripsTrailingAssistantPrefill(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"prefill"}]}`)
	out := ConvertOpenAIRequestToGemini("gemini-test", raw, false)
	contents := gjson.GetBytes(out, "contents").Array()
	if len(contents) != 1 || contents[0].Get("role").String() != "user" {
		t.Fatalf("unexpected contents after prefill removal: %s", out)
	}
}

func TestConvertOpenAIRequestToGemini_KeepsTrailingUserTurn(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"assistant","content":"history"},{"role":"user","content":"question"}]}`)
	out := ConvertOpenAIRequestToGemini("gemini-test", raw, false)
	contents := gjson.GetBytes(out, "contents").Array()
	if len(contents) != 2 || contents[1].Get("role").String() != "user" {
		t.Fatalf("trailing user turn was removed: %s", out)
	}
}

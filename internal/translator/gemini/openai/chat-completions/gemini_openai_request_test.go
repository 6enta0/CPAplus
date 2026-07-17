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

func TestConvertOpenAIRequestToGeminiPreservesReasoningVisibleTextAndToolCall(t *testing.T) {
	input := []byte(`{"messages":[{"role":"assistant","content":"visible","reasoning_content":"thinking","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"ok"}]}`)
	out := ConvertOpenAIRequestToGemini("gemini-test", input, false)
	parts := gjson.GetBytes(out, "contents.0.parts").Array()
	if len(parts) != 3 || parts[0].Get("text").String() != "thinking" || !parts[0].Get("thought").Bool() {
		t.Fatalf("reasoning part missing or out of order: %s", out)
	}
	if parts[1].Get("text").String() != "visible" || parts[2].Get("functionCall.name").String() != "lookup" {
		t.Fatalf("visible text or tool call missing: %s", out)
	}
}

func TestConvertOpenAIRequestToGeminiCleansToolSchema(t *testing.T) {
	input := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"search","parameters":{"type":"object","title":"Search","properties":{"query":{"type":"string"}},"required":["query","stale"]}}}]}`)
	out := ConvertOpenAIRequestToGemini("gemini-test", input, false)
	schema := gjson.GetBytes(out, "tools.0.functionDeclarations.0.parametersJsonSchema")
	if schema.Get("title").Exists() {
		t.Fatalf("schema title was not removed: %s", schema.Raw)
	}
	if required := schema.Get("required").Array(); len(required) != 1 || required[0].String() != "query" {
		t.Fatalf("required fields were not cleaned: %s", schema.Raw)
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

package chat_completions

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertOpenAIRequestToAntigravityDisambiguatesAndDeduplicatesTools(t *testing.T) {
	first := "mcp__plugin_cloudflare_cloudflare-builds__workers_builds_get_build"
	second := "mcp__plugin_cloudflare_cloudflare-builds__workers_builds_get_build_logs"
	input := []byte(`{
		"messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"` + second + `","arguments":"{}"}}]}],
		"tools":[
			{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}},
			{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}},
			{"type":"function","function":{"name":"` + first + `","parameters":{"type":"object"}}},
			{"type":"function","function":{"name":"` + second + `","parameters":{"type":"object"}}}
		],
		"tool_choice":{"type":"function","function":{"name":"` + second + `"}}
	}`)

	out := ConvertOpenAIRequestToAntigravity("gemini-3-flash", input, false)
	declarations := gjson.GetBytes(out, "request.tools.0.functionDeclarations").Array()
	if len(declarations) != 3 {
		t.Fatalf("declaration count = %d, want 3: %s", len(declarations), out)
	}
	if got := gjson.GetBytes(out, "request.contents.0.parts.0.functionCall.name").String(); got != declarations[2].Get("name").String() {
		t.Fatalf("function call name does not match declaration: %s", out)
	}
	if got := gjson.GetBytes(out, "request.toolConfig.functionCallingConfig.allowedFunctionNames.0").String(); got != declarations[2].Get("name").String() {
		t.Fatalf("tool choice name does not match declaration: %s", out)
	}
}

func TestConvertOpenAIRequestToAntigravityPreservesReasoningVisibleTextAndToolCall(t *testing.T) {
	input := []byte(`{"messages":[{"role":"assistant","content":"visible","reasoning_content":"thinking","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"ok"}]}`)
	out := ConvertOpenAIRequestToAntigravity("gemini-test", input, false)
	parts := gjson.GetBytes(out, "request.contents.0.parts").Array()
	if len(parts) != 3 || parts[0].Get("text").String() != "thinking" || !parts[0].Get("thought").Bool() {
		t.Fatalf("reasoning part missing or out of order: %s", out)
	}
	if parts[1].Get("text").String() != "visible" || parts[2].Get("functionCall.name").String() != "lookup" {
		t.Fatalf("visible text or tool call missing: %s", out)
	}
}

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

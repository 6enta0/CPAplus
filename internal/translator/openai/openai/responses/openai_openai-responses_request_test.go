package responses

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletionsFlattensNamespaceTools(t *testing.T) {
	raw := []byte(`{
		"tools":[{
			"type":"namespace",
			"name":"mcp__test_mcp__",
			"tools":[{
				"type":"function",
				"name":"add_numbers",
				"description":"Add two numbers",
				"parameters":{"type":"object","properties":{"a":{"type":"number"}},"required":["a"]}
			}]
		}]
	}`)

	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("model", raw, false)
	if got := gjson.GetBytes(out, "tools.#").Int(); got != 1 {
		t.Fatalf("tools count = %d, want 1; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.name").String(); got != "mcp__test_mcp__add_numbers" {
		t.Fatalf("tool name = %q, want qualified namespace name; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.parameters.required.0").String(); got != "a" {
		t.Fatalf("required parameter = %q, want a; output=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletionsMergesAdditionalCustomTools(t *testing.T) {
	raw := []byte(`{
		"tools":[{"type":"function","name":"read","parameters":{"type":"object"}}],
		"input":[{
			"type":"additional_tools",
			"tools":[{
				"type":"namespace",
				"name":"terminal",
				"tools":[{"type":"custom","name":"exec","description":"Run a command"}]
			}]
		}]
	}`)

	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("model", raw, false)
	if got := gjson.GetBytes(out, "tools.#").Int(); got != 2 {
		t.Fatalf("tools count = %d, want 2; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.1.function.name").String(); got != "terminal__exec" {
		t.Fatalf("custom tool name = %q, want terminal__exec; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.1.function.parameters.properties.input.type").String(); got != "string" {
		t.Fatalf("custom input type = %q, want string; output=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletionsReplaysCustomToolHistory(t *testing.T) {
	raw := []byte(`{
		"input":[
			{"type":"custom_tool_call","call_id":"call_exec","name":"terminal__exec","input":"pwd"},
			{"type":"custom_tool_call_output","call_id":"call_exec","output":["/repo",{"type":"input_text","text":"\n"}]}
		]
	}`)

	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("model", raw, false)
	if got := gjson.GetBytes(out, "messages.0.tool_calls.0.function.name").String(); got != "terminal__exec" {
		t.Fatalf("tool call name = %q, want terminal__exec; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.0.function.arguments").String(); got != `{"input":"pwd"}` {
		t.Fatalf("tool arguments = %q, want wrapped custom input; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.content").String(); got != "/repo\n" {
		t.Fatalf("tool output = %q, want flattened content; output=%s", got, out)
	}
}

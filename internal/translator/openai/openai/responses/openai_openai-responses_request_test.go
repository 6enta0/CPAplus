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

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletionsReplaysReasoningWithVisibleText(t *testing.T) {
	raw := []byte(`{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"think"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"visible"}]}]}`)
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", raw, false)
	message := gjson.GetBytes(out, "messages.0")
	if got := message.Get("reasoning_content").String(); got != "think" {
		t.Fatalf("reasoning_content = %q, want think; output=%s", got, out)
	}
	if got := message.Get("content.0.text").String(); got != "visible" {
		t.Fatalf("visible content = %q, want visible; output=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletionsAttachesReasoningToToolCall(t *testing.T) {
	raw := []byte(`{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"use tool"}]},{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", raw, false)
	if got := gjson.GetBytes(out, "messages.0.reasoning_content").String(); got != "use tool" {
		t.Fatalf("tool reasoning_content = %q, want use tool; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.0.function.name").String(); got != "lookup" {
		t.Fatalf("tool name = %q, want lookup; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.tool_call_id").String(); got != "call_1" {
		t.Fatalf("tool output id = %q, want call_1; output=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletionsPreservesStructuredToolChoice(t *testing.T) {
	raw := []byte(`{"input":[{"role":"user","content":"Run command."}],"tool_choice":{"type":"function","function":{"name":"run_command"}}}`)
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", raw, false)
	if got := gjson.GetBytes(out, "tool_choice.type").String(); got != "function" {
		t.Fatalf("tool_choice.type = %q, want function; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tool_choice.function.name").String(); got != "run_command" {
		t.Fatalf("tool_choice.function.name = %q, want run_command; output=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletionsPreservesInputImageMetadata(t *testing.T) {
	raw := []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/image.png","detail":"high"},{"type":"input_image","file_id":"file-image-123","detail":"low"}]}]}`)
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-test", raw, false)
	if got := gjson.GetBytes(out, "messages.0.content.0.image_url.url").String(); got != "https://example.com/image.png" {
		t.Fatalf("image URL = %q", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.image_url.detail").String(); got != "high" {
		t.Fatalf("URL image detail = %q", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.image_url.file_id").String(); got != "file-image-123" {
		t.Fatalf("image file ID = %q", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.image_url.detail").String(); got != "low" {
		t.Fatalf("file image detail = %q", got)
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

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletionsBatchesMixedToolCalls(t *testing.T) {
	raw := []byte(`{
		"input":[
			{"type":"function_call","call_id":"call_read","name":"read","arguments":"{}"},
			{"type":"custom_tool_call","call_id":"call_exec","name":"exec","input":"pwd"},
			{"type":"custom_tool_call_output","call_id":"call_exec","output":"done"},
			{"type":"function_call_output","call_id":"call_read","output":"read result"}
		]
	}`)

	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("model", raw, false)
	if got := gjson.GetBytes(out, "messages.#").Int(); got != 3 {
		t.Fatalf("messages count = %d, want 3; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.#").Int(); got != 2 {
		t.Fatalf("tool call count = %d, want 2; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.0.id").String(); got != "call_read" {
		t.Fatalf("first tool call id = %q, want call_read; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.1.id").String(); got != "call_exec" {
		t.Fatalf("second tool call id = %q, want call_exec; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.tool_call_id").String(); got != "call_exec" {
		t.Fatalf("first output id = %q, want call_exec; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.2.tool_call_id").String(); got != "call_read" {
		t.Fatalf("second output id = %q, want call_read; output=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletionsDefersMessagesUntilAllToolOutputs(t *testing.T) {
	raw := []byte(`{
		"input":[
			{"type":"function_call","call_id":"call_a","name":"a","arguments":"{}"},
			{"type":"function_call","call_id":"call_b","name":"b","arguments":"{}"},
			{"type":"message","role":"user","content":"do not insert here"},
			{"type":"function_call_output","call_id":"call_b","output":"b"},
			{"type":"function_call_output","call_id":"call_a","output":"a"},
			{"type":"message","role":"user","content":"continue"}
		]
	}`)

	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("model", raw, false)
	if got := gjson.GetBytes(out, "messages.#").Int(); got != 5 {
		t.Fatalf("messages count = %d, want 5; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.#").Int(); got != 2 {
		t.Fatalf("tool call count = %d, want 2; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.tool_call_id").String(); got != "call_b" {
		t.Fatalf("first output id = %q, want call_b; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.2.tool_call_id").String(); got != "call_a" {
		t.Fatalf("second output id = %q, want call_a; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.3.content").String(); got != "do not insert here" {
		t.Fatalf("deferred message = %q, want do not insert here; output=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletionsUsesStructuredFunctionOutputText(t *testing.T) {
	raw := []byte(`{
		"input":[
			{"type":"function_call","call_id":"call_array","name":"array_output","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_array","output":["/repo",{"type":"input_text","text":"\n"}]},
			{"type":"function_call","call_id":"call_object","name":"object_output","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_object","output":{"value":42}}
		]
	}`)

	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("model", raw, false)
	if got := gjson.GetBytes(out, "messages.1.content").String(); got != "/repo\n" {
		t.Fatalf("array output = %q, want /repo\\n; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.3.tool_call_id").String(); got != "call_object" {
		t.Fatalf("object output id = %q, want call_object; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.3.content").String(); got != `{"value":42}` {
		t.Fatalf("object output = %q, want raw JSON object; output=%s", got, out)
	}
}

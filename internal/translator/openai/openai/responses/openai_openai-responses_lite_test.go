package responses

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
)

func collectResponsesLiteEvents(t *testing.T, request []byte, chunks ...string) map[string][]gjson.Result {
	t.Helper()
	var param any
	events := make(map[string][]gjson.Result)
	for _, line := range chunks {
		for _, chunk := range ConvertOpenAIChatCompletionsResponseToOpenAIResponses(context.Background(), "model", request, nil, []byte(line), &param) {
			event, data := parseOpenAIResponsesSSEEvent(t, chunk)
			events[event] = append(events[event], data)
		}
	}
	return events
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesRestoresNamespaceFunctionCall(t *testing.T) {
	request := []byte(`{"tools":[{"type":"namespace","name":"mcp__test_mcp__","tools":[{"type":"function","name":"add_numbers"}]}]}`)
	events := collectResponsesLiteEvents(t, request,
		`data: {"id":"chatcmpl_ns","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_ns","function":{"name":"mcp__test_mcp__add_numbers","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)

	added := events["response.output_item.added"][0]
	if got := added.Get("item.name").String(); got != "add_numbers" {
		t.Fatalf("added name = %q, want add_numbers", got)
	}
	if got := added.Get("item.namespace").String(); got != "mcp__test_mcp__" {
		t.Fatalf("added namespace = %q, want mcp__test_mcp__", got)
	}
	completed := events["response.completed"][0]
	if got := completed.Get("response.output.0.namespace").String(); got != "mcp__test_mcp__" {
		t.Fatalf("completed namespace = %q, want mcp__test_mcp__", got)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesCustomToolNameArrivesLate(t *testing.T) {
	request := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	events := collectResponsesLiteEvents(t, request,
		`data: {"id":"chatcmpl_custom_late","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_exec","function":{"arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_custom_late","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"exec","arguments":"{\"input\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)

	added := events["response.output_item.added"][0]
	if got := added.Get("item.type").String(); got != "custom_tool_call" {
		t.Fatalf("added type = %q, want custom_tool_call", got)
	}
	inputDone := events["response.custom_tool_call_input.done"][0]
	if got := inputDone.Get("input").String(); got != "pwd" {
		t.Fatalf("custom input = %q, want pwd", got)
	}
	completed := events["response.completed"][0]
	if got := completed.Get("response.output.0.type").String(); got != "custom_tool_call" {
		t.Fatalf("completed type = %q, want custom_tool_call", got)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesSynthesizesMissingToolCallID(t *testing.T) {
	events := collectResponsesLiteEvents(t, nil,
		`data: {"id":"chatcmpl_missing_id","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)

	wantCallID := "call_chatcmpl_missing_id_0_0"
	added := events["response.output_item.added"][0]
	if got := added.Get("item.call_id").String(); got != wantCallID {
		t.Fatalf("added call_id = %q, want %q", got, wantCallID)
	}
	done := events["response.output_item.done"][0]
	if got := done.Get("item.call_id").String(); got != wantCallID {
		t.Fatalf("done call_id = %q, want %q", got, wantCallID)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesWaitsForLateToolCallID(t *testing.T) {
	events := collectResponsesLiteEvents(t, nil,
		`data: {"id":"chatcmpl_late_id","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"read","arguments":"{\"path\":"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_late_id","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_late","function":{"arguments":"\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)

	added := events["response.output_item.added"][0]
	if got := added.Get("item.call_id").String(); got != "call_late" {
		t.Fatalf("added call_id = %q, want call_late", got)
	}
	deltas := events["response.function_call_arguments.delta"]
	if len(deltas) != 1 {
		t.Fatalf("argument delta count = %d, want 1", len(deltas))
	}
	if got := deltas[0].Get("delta").String(); got != `{"path":"README.md"}` {
		t.Fatalf("arguments delta = %q, want full buffered arguments", got)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesRestoresAdditionalNamespaceCustomTool(t *testing.T) {
	request := []byte(`{"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"terminal","tools":[{"type":"custom","name":"exec"}]}]}]}`)
	events := collectResponsesLiteEvents(t, request,
		`data: {"id":"chatcmpl_ns_custom","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_exec","function":{"name":"terminal__exec","arguments":"{\"input\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)

	added := events["response.output_item.added"][0]
	if got := added.Get("item.type").String(); got != "custom_tool_call" {
		t.Fatalf("added type = %q, want custom_tool_call", got)
	}
	if got := added.Get("item.name").String(); got != "terminal__exec" {
		t.Fatalf("added name = %q, want terminal__exec", got)
	}
	if got := events["response.custom_tool_call_input.done"][0].Get("input").String(); got != "pwd" {
		t.Fatalf("custom input = %q, want pwd", got)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStreamRestoresNamespaceCustomTool(t *testing.T) {
	request := []byte(`{"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"terminal","tools":[{"type":"custom","name":"exec"}]}]}]}`)
	raw := []byte(`{"id":"chatcmpl_custom","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"tool_calls":[{"id":"call_exec","function":{"name":"terminal__exec","arguments":"{\"input\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}]}`)

	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(context.Background(), "model", request, nil, raw, nil)
	if got := gjson.GetBytes(resp, "output.0.type").String(); got != "custom_tool_call" {
		t.Fatalf("output type = %q, want custom_tool_call; response=%s", got, resp)
	}
	if got := gjson.GetBytes(resp, "output.0.input").String(); got != "pwd" {
		t.Fatalf("output input = %q, want pwd; response=%s", got, resp)
	}
}

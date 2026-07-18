package responses

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertOpenAIResponsesRequestToClaude_SanitizesToolCallIDs(t *testing.T) {
	raw := []byte(`{
		"input":[
			{"type":"function_call","call_id":"call.with space:1","name":"Read","arguments":"{}"},
			{"type":"function_call_output","call_id":"call.with space:1","output":"ok"}
		]
	}`)

	out := ConvertOpenAIResponsesRequestToClaude("claude-test", raw, false)
	root := gjson.ParseBytes(out)
	toolUseID := root.Get("messages.0.content.0.id").String()
	toolResultID := root.Get("messages.1.content.0.tool_use_id").String()

	if toolUseID != "call_with_space_1" {
		t.Fatalf("tool_use id = %q, want call_with_space_1", toolUseID)
	}
	if toolResultID != toolUseID {
		t.Fatalf("tool_result tool_use_id = %q, want %q", toolResultID, toolUseID)
	}
}

func TestConvertOpenAIResponsesRequestToClaudeDropsApplyPatchCustomTool(t *testing.T) {
	raw := []byte(`{"input":"hi","tools":[{"type":"custom","name":"apply_patch"},{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}]}`)
	out := ConvertOpenAIResponsesRequestToClaude("claude-test", raw, false)
	if got := gjson.GetBytes(out, "tools.#").Int(); got != 1 {
		t.Fatalf("tool count = %d, want 1; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "exec_command" {
		t.Fatalf("tool name = %q, want exec_command; output=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToClaude_FunctionCallOutputPreservesInputImage(t *testing.T) {
	const imageB64 = "iVBORw0KGgo="
	dataURL := "data:image/png;base64," + imageB64
	raw := []byte(`{
		"input":[
			{"type":"function_call","call_id":"call_view_image_1","name":"view_image","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_view_image_1","output":[{"type":"input_image","image_url":"` + dataURL + `"}]}
		]
	}`)

	out := ConvertOpenAIResponsesRequestToClaude("claude-test", raw, false)
	toolResult := gjson.GetBytes(out, "messages.1.content.0")

	if got := toolResult.Get("content.0.type").String(); got != "image" {
		t.Fatalf("content block type = %q, want image. Output: %s", got, out)
	}
	if got := toolResult.Get("content.0.source.media_type").String(); got != "image/png" {
		t.Fatalf("media type = %q, want image/png", got)
	}
	if got := toolResult.Get("content.0.source.data").String(); got != imageB64 {
		t.Fatalf("image data = %q, want %q", got, imageB64)
	}
	if strings.Contains(toolResult.Get("content").Raw, "data:image") {
		t.Fatalf("tool result retained data URL text: %s", out)
	}
}

func TestConvertOpenAIResponsesRequestToClaudeSkipsInvalidToolResultFileDataURL(t *testing.T) {
	raw := []byte(`{"input":[{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"fallback"},{"type":"input_file","file_data":"data:application/pdf,not-base64"}]}]}`)
	out := ConvertOpenAIResponsesRequestToClaude("claude-test", raw, false)
	content := gjson.GetBytes(out, "messages.1.content.0.content")
	if content.IsArray() {
		parts := content.Array()
		if len(parts) != 1 || parts[0].Get("type").String() != "text" || parts[0].Get("text").String() != "fallback" {
			t.Fatalf("unexpected tool result content: %s", content.Raw)
		}
		return
	}
	if content.String() != "fallback" {
		t.Fatalf("tool result content = %s", content.Raw)
	}
}

func TestConvertOpenAIResponsesRequestToClaude_PreservesCacheControl(t *testing.T) {
	raw := []byte(`{
		"input":[{
			"type":"message",
			"role":"user",
			"content":[
				{"type":"input_text","text":"cached","cache_control":{"type":"ephemeral"}},
				{"type":"input_text","text":"fresh"}
			]
		}],
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}},"cache_control":{"type":"ephemeral"}}]
	}`)

	out := ConvertOpenAIResponsesRequestToClaude("claude-test", raw, false)
	content := gjson.GetBytes(out, "messages.0.content")
	if !content.IsArray() {
		t.Fatalf("content with cache marker was collapsed to string: %s", out)
	}
	if got := content.Get("0.cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("content cache_control.type = %q, want ephemeral. Output: %s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("tool cache_control.type = %q, want ephemeral. Output: %s", got, out)
	}
}

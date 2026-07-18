package chat_completions

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertGeminiResponseToOpenAIEmitsFinishReasonOnlyOnTerminalChunk(t *testing.T) {
	var param any
	intermediate := []byte(`{"candidates":[{"index":0,"finishReason":"STOP","content":{"parts":[{"functionCall":{"name":"lookup","args":{}}}]}}]}`)
	first := ConvertGeminiResponseToOpenAI(context.Background(), "", nil, nil, intermediate, &param)
	if got := gjson.GetBytes(first[0], "choices.0.finish_reason"); got.Type != gjson.Null {
		t.Fatalf("intermediate finish_reason = %s, want null; output=%s", got.Raw, first[0])
	}
	terminal := []byte(`{"candidates":[{"index":0,"finishReason":"STOP","content":{"parts":[]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	last := ConvertGeminiResponseToOpenAI(context.Background(), "", nil, nil, terminal, &param)
	if got := gjson.GetBytes(last[0], "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("terminal finish_reason = %q, want tool_calls; output=%s", got, last[0])
	}
	if got := gjson.GetBytes(last[0], "choices.0.native_finish_reason").String(); got != "stop" {
		t.Fatalf("native_finish_reason = %q, want stop; output=%s", got, last[0])
	}
}

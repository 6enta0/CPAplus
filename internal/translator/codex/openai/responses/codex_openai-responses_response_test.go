package responses

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertCodexResponseToOpenAIResponsesNonStreamPreservesIncomplete(t *testing.T) {
	raw := []byte(`{"type":"response.incomplete","response":{"id":"resp_1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}`)
	out := ConvertCodexResponseToOpenAIResponsesNonStream(context.Background(), "", nil, nil, raw, nil)
	if got := gjson.GetBytes(out, "status").String(); got != "incomplete" {
		t.Fatalf("status = %q, want incomplete; output=%s", got, out)
	}
	if got := gjson.GetBytes(out, "incomplete_details.reason").String(); got != "max_output_tokens" {
		t.Fatalf("reason = %q, want max_output_tokens; output=%s", got, out)
	}
}

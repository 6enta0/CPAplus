package responses

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertOpenAIResponsesRequestToGemini_StripsTrailingAssistantPrefill(t *testing.T) {
	raw := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"prefill"}]}
		]
	}`)
	out := ConvertOpenAIResponsesRequestToGemini("gemini-test", raw, false)
	contents := gjson.GetBytes(out, "contents").Array()
	if len(contents) != 1 || contents[0].Get("role").String() != "user" {
		t.Fatalf("unexpected contents after prefill removal: %s", out)
	}
}

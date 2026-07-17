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

func TestConvertOpenAIResponsesRequestToGeminiPreservesReasoningAndVisibleText(t *testing.T) {
	input := []byte(`{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}],"encrypted_content":"signature"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"visible"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`)
	out := ConvertOpenAIResponsesRequestToGemini("gemini-test", input, false)
	parts := gjson.GetBytes(out, "contents.0.parts").Array()
	if len(parts) != 2 || parts[0].Get("text").String() != "thinking" || !parts[0].Get("thought").Bool() {
		t.Fatalf("reasoning part missing: %s", out)
	}
	if parts[0].Get("thoughtSignature").String() != "signature" || parts[1].Get("text").String() != "visible" {
		t.Fatalf("signature or visible text missing: %s", out)
	}
}

func TestConvertOpenAIResponsesRequestToGeminiKeepsTrailingReasoningHistory(t *testing.T) {
	input := []byte(`{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]}]}`)
	out := ConvertOpenAIResponsesRequestToGemini("gemini-test", input, false)
	if got := gjson.GetBytes(out, "contents.0.parts.0.text").String(); got != "thinking" {
		t.Fatalf("trailing reasoning was stripped: %s", out)
	}
	if got := gjson.GetBytes(out, "contents.0.parts.0.thoughtSignature").String(); got != geminiResponsesThoughtSignature {
		t.Fatalf("missing signature fallback: %s", out)
	}
}

func TestConvertOpenAIResponsesRequestToGeminiStructuredOutputAndToolSchema(t *testing.T) {
	input := []byte(`{
		"input":"hi",
		"text":{"format":{"type":"json_schema","schema":{"type":"object","title":"Result","properties":{"answer":{"type":"string"}},"required":["answer","stale"]}}},
		"tools":[{"type":"function","name":"search","parameters":{"type":"object","title":"Search","properties":{"query":{"type":"string"}},"required":["query","stale"]}}]
	}`)
	out := ConvertOpenAIResponsesRequestToGemini("gemini-test", input, false)
	if got := gjson.GetBytes(out, "generationConfig.responseMimeType").String(); got != "application/json" {
		t.Fatalf("responseMimeType = %q, want application/json; output=%s", got, out)
	}
	responseSchema := gjson.GetBytes(out, "generationConfig.responseJsonSchema")
	if responseSchema.Get("title").Exists() || len(responseSchema.Get("required").Array()) != 1 {
		t.Fatalf("response schema was not cleaned: %s", responseSchema.Raw)
	}
	toolSchema := gjson.GetBytes(out, "tools.0.functionDeclarations.0.parametersJsonSchema")
	if toolSchema.Get("title").Exists() || len(toolSchema.Get("required").Array()) != 1 {
		t.Fatalf("tool schema was not cleaned: %s", toolSchema.Raw)
	}
}

func TestConvertOpenAIResponsesRequestToGeminiJSONOutput(t *testing.T) {
	input := []byte(`{"input":"hi","text":{"format":{"type":"json_object"}}}`)
	out := ConvertOpenAIResponsesRequestToGemini("gemini-test", input, false)
	if got := gjson.GetBytes(out, "generationConfig.responseMimeType").String(); got != "application/json" {
		t.Fatalf("responseMimeType = %q, want application/json; output=%s", got, out)
	}
}

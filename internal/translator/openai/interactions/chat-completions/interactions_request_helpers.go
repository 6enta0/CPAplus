package chat_completions

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func interactionsRequestFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func interactionsRequestTextStep(stepType, text string) []byte {
	step := []byte(`{"type":"","content":[{"type":"text","text":""}]}`)
	step, _ = sjson.SetBytes(step, "type", stepType)
	step, _ = sjson.SetBytes(step, "content.0.text", text)
	return step
}

func interactionsRequestReasoningTexts(reasoning gjson.Result) []string {
	if reasoning.Type == gjson.String {
		return []string{reasoning.String()}
	}
	if reasoning.IsArray() {
		out := make([]string, 0, len(reasoning.Array()))
		for _, item := range reasoning.Array() {
			if text := strings.TrimSpace(item.Get("text").String()); text != "" {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func interactionsRequestToolCallStep(toolCall gjson.Result) ([]byte, bool) {
	name := interactionsRequestFirstNonEmpty(toolCall.Get("function.name").String(), toolCall.Get("name").String())
	if name == "" {
		return nil, false
	}
	step := []byte(`{"type":"function_call","call_id":"","name":"","arguments":""}`)
	step, _ = sjson.SetBytes(step, "call_id", toolCall.Get("id").String())
	step, _ = sjson.SetBytes(step, "name", name)
	args := interactionsRequestFirstNonEmpty(toolCall.Get("function.arguments").String(), toolCall.Get("arguments").String())
	if gjson.Valid(args) {
		step, _ = sjson.SetRawBytes(step, "arguments", []byte(args))
	} else {
		step, _ = sjson.SetBytes(step, "arguments", args)
	}
	return step, true
}

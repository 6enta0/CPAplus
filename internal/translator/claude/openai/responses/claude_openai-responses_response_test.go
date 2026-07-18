package responses

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func parseResponsesEvent(t *testing.T, chunk []byte) (string, gjson.Result) {
	t.Helper()
	var event, data string
	for _, line := range strings.Split(string(chunk), "\n") {
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	if data == "" {
		t.Fatalf("SSE chunk has no data: %s", chunk)
	}
	return event, gjson.Parse(data)
}

func TestConvertClaudeResponseToOpenAIResponsesUsageAndAnnotations(t *testing.T) {
	chunks := [][]byte{
		[]byte(`data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":4,"cache_read_input_tokens":3}}}`),
		[]byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		[]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"first "}}`),
		[]byte(`data: {"type":"content_block_stop","index":0}`),
		[]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"url_citation","url":"https://example.com"}}}`),
		[]byte(`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`),
		[]byte(`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"second"}}`),
		[]byte(`data: {"type":"content_block_stop","index":1}`),
		[]byte(`data: {"type":"message_delta","usage":{"output_tokens":2}}`),
		[]byte(`data: {"type":"message_stop"}`),
	}

	var param any
	counts := map[string]int{}
	var itemDone, completed gjson.Result
	for _, chunk := range chunks {
		for _, output := range ConvertClaudeResponseToOpenAIResponses(context.Background(), "claude-test", nil, nil, chunk, &param) {
			event, data := parseResponsesEvent(t, output)
			counts[event]++
			if event == "response.output_item.done" {
				itemDone = data
			}
			if event == "response.completed" {
				completed = data
			}
		}
	}

	if counts["response.output_item.added"] != 1 || counts["response.output_item.done"] != 1 {
		t.Fatalf("message event counts = added %d, done %d", counts["response.output_item.added"], counts["response.output_item.done"])
	}
	if got := itemDone.Get("item.content.0.text").String(); got != "first second" {
		t.Fatalf("stream text = %q", got)
	}
	if got := itemDone.Get("item.content.0.annotations.0.url").String(); got != "https://example.com" {
		t.Fatalf("stream annotation URL = %q", got)
	}
	assertUsage := func(prefix string, root gjson.Result) {
		t.Helper()
		if got := root.Get(prefix + "input_tokens").Int(); got != 17 {
			t.Fatalf("%sinput_tokens = %d", prefix, got)
		}
		if got := root.Get(prefix + "input_tokens_details.cached_tokens").Int(); got != 3 {
			t.Fatalf("%scached_tokens = %d", prefix, got)
		}
		if got := root.Get(prefix + "output_tokens").Int(); got != 2 {
			t.Fatalf("%soutput_tokens = %d", prefix, got)
		}
		if got := root.Get(prefix + "total_tokens").Int(); got != 19 {
			t.Fatalf("%stotal_tokens = %d", prefix, got)
		}
	}
	assertUsage("response.usage.", completed)

	nonStream := ConvertClaudeResponseToOpenAIResponsesNonStream(context.Background(), "claude-test", nil, nil, []byte(strings.Join([]string{
		string(chunks[0]), string(chunks[1]), string(chunks[2]), string(chunks[4]), string(chunks[5]), string(chunks[6]), string(chunks[8]), string(chunks[9]),
	}, "\n")), nil)
	nonStreamRoot := gjson.ParseBytes(nonStream)
	assertUsage("usage.", nonStreamRoot)
	if got := nonStreamRoot.Get("output.0.content.0.annotations.0.url").String(); got != "https://example.com" {
		t.Fatalf("non-stream annotation URL = %q", got)
	}
}

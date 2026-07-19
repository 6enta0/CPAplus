package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestXAIExecutorExecuteShapesResponsesRequest(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotGrokConvID string
	var gotOriginator string
	var gotAccountID string
	var gotBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotGrokConvID = r.Header.Get("x-grok-conv-id")
		gotOriginator = r.Header.Get("Originator")
		gotAccountID = r.Header.Get("Chatgpt-Account-Id")
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"model\":\"grok-4.3\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "xai-auth",
		Provider: "xai",
		Attributes: map[string]string{
			"base_url":  server.URL,
			"auth_kind": "oauth",
		},
		Metadata: map[string]any{
			"access_token": "xai-token",
			"email":        "user@example.com",
		},
	}

	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"test"}],"content":null,"encrypted_content":null},{"type":"reasoning","summary":[{"type":"summary_text","text":"second"}]},{"role":"user","content":"hello"}],"include":["reasoning.encrypted_content"],"reasoning":{"effort":"high"},"tools":[{"type":"tool_search"},{"type":"image_generation"},{"type":"custom","name":"apply_patch"},{"type":"custom","name":"custom_lookup"},{"type":"function","name":"lookup"},{"type":"web_search","external_web_access":true,"search_content_types":["text","image"]},{"type":"namespace","name":"codex_app","description":"Tools in the codex_app namespace.","tools":[{"type":"function","name":"automation_update"},{"type":"custom","name":"namespace_custom"},{"type":"tool_search"}]}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Stream:       false,
		Metadata: map[string]any{
			cliproxyexecutor.ExecutionSessionMetadataKey: "conv-xai-1",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if gotPath != "/responses" {
		t.Fatalf("path = %q, want /responses", gotPath)
	}
	if gotAuth != "Bearer xai-token" {
		t.Fatalf("Authorization = %q, want Bearer xai-token", gotAuth)
	}
	if gotGrokConvID != "conv-xai-1" {
		t.Fatalf("x-grok-conv-id = %q, want conv-xai-1", gotGrokConvID)
	}
	if gotOriginator != "" {
		t.Fatalf("Originator = %q, want empty", gotOriginator)
	}
	if gotAccountID != "" {
		t.Fatalf("Chatgpt-Account-Id = %q, want empty", gotAccountID)
	}
	if gjson.GetBytes(gotBody, "prompt_cache_key").String() != "conv-xai-1" {
		t.Fatalf("prompt_cache_key missing from body: %s", string(gotBody))
	}
	if !gjson.GetBytes(gotBody, "stream").Bool() {
		t.Fatalf("stream = false, want true; body=%s", string(gotBody))
	}
	if gjson.GetBytes(gotBody, "reasoning.effort").String() != "high" {
		t.Fatalf("reasoning.effort = %q, want high; body=%s", gjson.GetBytes(gotBody, "reasoning.effort").String(), string(gotBody))
	}
	if gjson.GetBytes(gotBody, "input.0.content").Exists() {
		t.Fatalf("input.0.content exists, want removed; body=%s", string(gotBody))
	}
	if gjson.GetBytes(gotBody, "input.0.encrypted_content").Exists() {
		t.Fatalf("input.0.encrypted_content exists, want removed; body=%s", string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.0.summary.0.text").String(); got != "test" {
		t.Fatalf("input.0.summary.0.text = %q, want test; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.0.summary.1.text").String(); got != "second" {
		t.Fatalf("input.0.summary.1.text = %q, want second; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.1.role").String(); got != "user" {
		t.Fatalf("input.1.role = %q, want user; body=%s", got, string(gotBody))
	}
	if gjson.GetBytes(gotBody, "input.2").Exists() {
		t.Fatalf("input.2 exists, want consecutive reasoning item merged; body=%s", string(gotBody))
	}
	tools := gjson.GetBytes(gotBody, "tools").Array()
	if len(tools) != 6 {
		t.Fatalf("tools length = %d, want 6; body=%s", len(tools), string(gotBody))
	}
	foundAutomationUpdate := false
	foundNamespaceCustom := false
	foundXSearch := false
	for i, tool := range tools {
		toolType := tool.Get("type").String()
		if toolType == "image_generation" {
			t.Fatalf("tools.%d.type = image_generation, want removed; body=%s", i, string(gotBody))
		}
		if toolType != "function" && toolType != "web_search" && toolType != "x_search" {
			t.Fatalf("tools.%d.type = %q, want function, web_search, or x_search; body=%s", i, toolType, string(gotBody))
		}
		if toolType == "x_search" {
			foundXSearch = true
		}
		if toolType == "function" && !tool.Get("parameters").Exists() {
			t.Fatalf("tools.%d.parameters missing for xAI function tool; body=%s", i, string(gotBody))
		}
		if got := tool.Get("name").String(); got == "apply_patch" {
			t.Fatalf("tools.%d.name = apply_patch, want removed; body=%s", i, string(gotBody))
		}
		switch tool.Get("name").String() {
		case "codex_app__automation_update":
			foundAutomationUpdate = true
		case "codex_app__namespace_custom":
			foundNamespaceCustom = true
		}
		if toolType == "web_search" {
			if tool.Get("external_web_access").Exists() {
				t.Fatalf("tools.%d.external_web_access exists, want removed; body=%s", i, string(gotBody))
			}
			if got := tool.Get("search_content_types.1").String(); got != "image" {
				t.Fatalf("tools.%d.search_content_types missing image entry; body=%s", i, string(gotBody))
			}
		}
	}
	if !foundAutomationUpdate {
		t.Fatalf("namespace function tool was not moved to top-level tools; body=%s", string(gotBody))
	}
	if !foundNamespaceCustom {
		t.Fatalf("namespace custom tool was not moved to top-level tools; body=%s", string(gotBody))
	}
	if !foundXSearch {
		t.Fatalf("native x_search tool was not injected; body=%s", string(gotBody))
	}
	foundEncryptedReasoningInclude := false
	for _, include := range gjson.GetBytes(gotBody, "include").Array() {
		if include.String() == "reasoning.encrypted_content" {
			foundEncryptedReasoningInclude = true
		}
	}
	if !foundEncryptedReasoningInclude {
		t.Fatalf("xai request must retain encrypted reasoning content for replay: %s", string(gotBody))
	}
}

func TestXAIExecutorExecuteStreamNormalizesReasoningTextEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_item.added\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.added\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"status\":\"in_progress\",\"summary\":[]}}\n\n"))
		_, _ = w.Write([]byte("event: response.content_part.added\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.content_part.added\",\"sequence_number\":2,\"item_id\":\"rs_1\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"reasoning_text\",\"text\":\"\"}}\n\n"))
		_, _ = w.Write([]byte("event: response.reasoning_text.delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.reasoning_text.delta\",\"sequence_number\":3,\"item_id\":\"rs_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"thinking\"}\n\n"))
		_, _ = w.Write([]byte("event: response.reasoning_text.done\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.reasoning_text.done\",\"sequence_number\":4,\"item_id\":\"rs_1\",\"output_index\":0,\"content_index\":0,\"text\":\"thinking\"}\n\n"))
		_, _ = w.Write([]byte("event: response.output_item.done\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"sequence_number\":5,\"output_index\":0,\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"status\":\"completed\",\"summary\":[],\"content\":[{\"type\":\"reasoning_text\",\"text\":\"thinking\"}]}}\n\n"))
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"sequence_number\":6,\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"model\":\"grok-4.3\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "xai", Attributes: map[string]string{"base_url": server.URL}, Metadata: map[string]any{"access_token": "xai-token"}}
	result, err := exec.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","input":"hello"}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Stream: true})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}

	var streamed bytes.Buffer
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
		streamed.Write(chunk.Payload)
	}
	output := streamed.String()
	if strings.Contains(output, "reasoning_text") {
		t.Fatalf("stream contains xAI reasoning_text shape: %s", output)
	}
	for _, want := range []string{
		"event: response.reasoning_summary_part.added",
		"event: response.reasoning_summary_text.delta",
		"event: response.reasoning_summary_text.done",
		"event: response.reasoning_summary_part.done",
		`"type":"response.reasoning_summary_part.added"`,
		`"type":"response.reasoning_summary_text.delta"`,
		`"type":"response.reasoning_summary_text.done"`,
		`"type":"response.reasoning_summary_part.done"`,
		`"part":{"type":"summary_text","text":"thinking"}`,
		`"summary_index":0`,
		`"summary":[{"type":"summary_text","text":"thinking"}]`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stream missing %q: %s", want, output)
		}
	}
}

func TestXAIExecutorExecuteNormalizesReasoningOutputForNonStreamTranslation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"status\":\"completed\",\"summary\":[],\"content\":[{\"type\":\"reasoning_text\",\"text\":\"thinking\"}]}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"model\":\"grok-4.3\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "xai", Attributes: map[string]string{"base_url": server.URL}, Metadata: map[string]any{"access_token": "xai-token"}}
	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{Model: "grok-4.3", Payload: []byte(`{"model":"grok-4.3","input":"hello"}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(string(resp.Payload), "reasoning_text") {
		t.Fatalf("payload contains xAI reasoning_text shape: %s", resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "output.0.summary.0.type").String(); got != "summary_text" {
		t.Fatalf("output.0.summary.0.type = %q, want summary_text; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "output.0.summary.0.text").String(); got != "thinking" {
		t.Fatalf("output.0.summary.0.text = %q, want thinking; payload=%s", got, resp.Payload)
	}
	if gjson.GetBytes(resp.Payload, "output.0.content").Exists() {
		t.Fatalf("reasoning output content exists, want summary only: %s", resp.Payload)
	}
}

func TestXAIExecutorExecuteIncompleteResponseIsSuccessful(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_incomplete\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "xai", Attributes: map[string]string{"base_url": server.URL}, Metadata: map[string]any{"access_token": "xai-token"}}
	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{Model: "grok-4.3", Payload: []byte(`{"model":"grok-4.3","input":"hello"}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "status").String(); got != "incomplete" {
		t.Fatalf("status = %q, want incomplete; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "incomplete_details.reason").String(); got != "max_output_tokens" {
		t.Fatalf("incomplete reason = %q, want max_output_tokens; payload=%s", got, resp.Payload)
	}
}

func TestXAIExecutorExecuteStreamMissingTerminalIsRequestScoped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_open\"}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "xai", Attributes: map[string]string{"base_url": server.URL}, Metadata: map[string]any{"access_token": "xai-token"}}
	result, err := exec.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{Model: "grok-4.3", Payload: []byte(`{"model":"grok-4.3","input":"hello"}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Stream: true})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	var streamErr error
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			streamErr = chunk.Err
		}
	}
	if streamErr == nil {
		t.Fatal("stream completed without a terminal error")
	}
	if status, ok := streamErr.(interface{ StatusCode() int }); !ok || status.StatusCode() != http.StatusRequestTimeout {
		t.Fatalf("stream error status = %v, want %d", streamErr, http.StatusRequestTimeout)
	}
	if scoped, ok := streamErr.(interface{ IsRequestScoped() bool }); !ok || !scoped.IsRequestScoped() {
		t.Fatalf("stream error = %T, want request-scoped error", streamErr)
	}
}

func TestXAIExecutorPreparesSupportedEntryProtocols(t *testing.T) {
	tests := []struct {
		name    string
		format  sdktranslator.Format
		payload string
	}{
		{name: "openai chat", format: sdktranslator.FormatOpenAI, payload: `{"model":"grok-4.3","messages":[{"role":"user","content":"hello"}]}`},
		{name: "openai responses", format: sdktranslator.FormatOpenAIResponse, payload: `{"model":"grok-4.3","input":"hello"}`},
		{name: "claude", format: sdktranslator.FormatClaude, payload: `{"model":"grok-4.3","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`},
		{name: "codex", format: sdktranslator.FormatCodex, payload: `{"model":"grok-4.3","instructions":"","input":[{"role":"user","content":"hello"}]}`},
	}
	exec := NewXAIExecutor(&config.Config{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prepared, err := exec.prepareResponsesRequest(context.Background(), cliproxyexecutor.Request{
				Model:   "grok-4.3",
				Payload: []byte(tt.payload),
			}, cliproxyexecutor.Options{SourceFormat: tt.format}, true)
			if err != nil {
				t.Fatalf("prepareResponsesRequest() error = %v", err)
			}
			if !gjson.ValidBytes(prepared.body) {
				t.Fatalf("prepared body is invalid JSON: %s", prepared.body)
			}
			if got := gjson.GetBytes(prepared.body, "model").String(); got != "grok-4.3" {
				t.Fatalf("model = %q, want grok-4.3; body=%s", got, prepared.body)
			}
			if !gjson.GetBytes(prepared.body, `tools.#(type=="x_search")`).Exists() {
				t.Fatalf("native x_search missing; body=%s", prepared.body)
			}
		})
	}
}

func TestXAIExecutorCompactUsesCompactEndpoint(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotAccept string
	var gotBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response.compaction","output":[{"type":"compaction","encrypted_content":"opaque-out"}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "xai",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "xai-token",
		},
	}

	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","stream":true,"input":[{"type":"compaction","encrypted_content":"opaque-in"},{"role":"user","content":"hello"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Alt:          "responses/compact",
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute compact error: %v", err)
	}
	if gotPath != "/responses/compact" {
		t.Fatalf("path = %q, want /responses/compact", gotPath)
	}
	if gotAuth != "Bearer xai-token" {
		t.Fatalf("Authorization = %q, want Bearer xai-token", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q, want application/json", gotAccept)
	}
	if gjson.GetBytes(gotBody, "stream").Exists() {
		t.Fatalf("stream exists in compact body: %s", string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.0.encrypted_content").String(); got != "opaque-in" {
		t.Fatalf("input.0.encrypted_content = %q, want opaque-in; body=%s", got, string(gotBody))
	}
	if string(resp.Payload) != `{"id":"resp_1","object":"response.compaction","output":[{"type":"compaction","encrypted_content":"opaque-out"}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}` {
		t.Fatalf("payload = %s", string(resp.Payload))
	}
}

func TestXAIExecutorExecuteStreamCompactionTriggerUsesCompactEndpoint(t *testing.T) {
	var gotPath string
	var gotAccept string
	var gotBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAccept = r.Header.Get("Accept")
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_xai_1","model":"grok-4.3","output":[{"type":"compaction","encrypted_content":"opaque"}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "xai",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "xai-token",
		},
	}

	result, err := exec.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","stream":true,"input":[{"role":"user","content":"hello"},{"type":"compaction_trigger"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Stream:       true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream compaction trigger error: %v", err)
	}
	if gotPath != "/responses/compact" {
		t.Fatalf("path = %q, want /responses/compact", gotPath)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q, want application/json", gotAccept)
	}
	if xaiInputHasItemType(gotBody, "compaction_trigger") {
		t.Fatalf("compaction_trigger reached xai compact body: %s", string(gotBody))
	}
	if gjson.GetBytes(gotBody, "stream").Exists() {
		t.Fatalf("stream exists in compact body: %s", string(gotBody))
	}

	var streamed bytes.Buffer
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
		streamed.Write(chunk.Payload)
	}
	output := streamed.String()
	for _, eventName := range []string{"response.created", "response.in_progress", "response.output_item.added", "response.output_item.done", "response.completed"} {
		if !strings.Contains(output, "event: "+eventName+"\n") {
			t.Fatalf("missing %s event in stream: %s", eventName, output)
		}
	}
	if !strings.Contains(output, `"type":"compaction"`) || !strings.Contains(output, `"encrypted_content":"opaque"`) {
		t.Fatalf("compaction output missing from stream: %s", output)
	}
	if !strings.Contains(output, `"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}`) {
		t.Fatalf("usage missing from completed stream: %s", output)
	}
}

func TestXAIExecutorOmitsUnsupportedReasoningEffort(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"model\":\"grok-4\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "xai",
		Attributes: map[string]string{
			"base_url":  server.URL,
			"auth_kind": "oauth",
		},
		Metadata: map[string]any{"access_token": "xai-token"},
	}

	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4",
		Payload: []byte(`{"model":"grok-4","input":"hello","reasoning":{"effort":"high"}}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if gjson.GetBytes(gotBody, "reasoning").Exists() {
		t.Fatalf("unsupported xAI model must omit reasoning key: %s", string(gotBody))
	}
}

func TestXAIExecutorAppliesThinkingSuffix(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"model\":\"grok-4.3\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "xai",
		Attributes: map[string]string{
			"base_url":  server.URL,
			"auth_kind": "oauth",
		},
		Metadata: map[string]any{"access_token": "xai-token"},
	}

	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4.3(low)",
		Payload: []byte(`{"model":"grok-4.3","input":"hello"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := gjson.GetBytes(gotBody, "model").String(); got != "grok-4.3" {
		t.Fatalf("model = %q, want grok-4.3; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "reasoning.effort").String(); got != "low" {
		t.Fatalf("reasoning.effort = %q, want low; body=%s", got, string(gotBody))
	}
}

func TestXAISupportsReasoningEffortUsesModelRegistry(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "grok-4.5", want: true},
		{model: "grok-4.3(high)", want: true},
		{model: "xai/grok-3-mini", want: true},
		{model: "grok-4", want: false},
		{model: "grok-composer-2.5-fast", want: false},
		{model: "unknown-xai-model", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := xaiSupportsReasoningEffort(tt.model); got != tt.want {
				t.Fatalf("xaiSupportsReasoningEffort(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestXAIExecutorExecuteStreamFiltersToolSearchTool(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"model\":\"grok-4.3\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata:   map[string]any{"access_token": "xai-token"},
	}

	result, err := exec.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"test"}],"content":null,"encrypted_content":null},{"type":"reasoning","summary":[{"type":"summary_text","text":"second"}]},{"role":"user","content":"hello"},{"type":"reasoning","summary":[{"type":"summary_text","text":"separate"}]}],"tools":[{"type":"tool_search"},{"type":"image_generation"},{"type":"custom","name":"apply_patch"},{"type":"custom","name":"custom_lookup"},{"type":"function","name":"lookup"},{"type":"web_search","external_web_access":true,"search_content_types":["text","image"]},{"type":"namespace","name":"codex_app","description":"Tools in the codex_app namespace.","tools":[{"type":"function","name":"automation_update"},{"type":"custom","name":"namespace_custom"},{"type":"tool_search"}]}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Stream:       true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
	}

	tools := gjson.GetBytes(gotBody, "tools").Array()
	if len(tools) != 6 {
		t.Fatalf("tools length = %d, want 6; body=%s", len(tools), string(gotBody))
	}
	if gjson.GetBytes(gotBody, "input.0.content").Exists() {
		t.Fatalf("input.0.content exists, want removed; body=%s", string(gotBody))
	}
	if gjson.GetBytes(gotBody, "input.0.encrypted_content").Exists() {
		t.Fatalf("input.0.encrypted_content exists, want removed; body=%s", string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.0.summary.0.text").String(); got != "test" {
		t.Fatalf("input.0.summary.0.text = %q, want test; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.0.summary.1.text").String(); got != "second" {
		t.Fatalf("input.0.summary.1.text = %q, want second; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.1.role").String(); got != "user" {
		t.Fatalf("input.1.role = %q, want user; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.2.summary.0.text").String(); got != "separate" {
		t.Fatalf("input.2.summary.0.text = %q, want separate; body=%s", got, string(gotBody))
	}
	foundAutomationUpdate := false
	foundNamespaceCustom := false
	foundXSearch := false
	for i, tool := range tools {
		toolType := tool.Get("type").String()
		if toolType == "image_generation" {
			t.Fatalf("tools.%d.type = image_generation, want removed; body=%s", i, string(gotBody))
		}
		if toolType != "function" && toolType != "web_search" && toolType != "x_search" {
			t.Fatalf("tools.%d.type = %q, want function, web_search, or x_search; body=%s", i, toolType, string(gotBody))
		}
		if toolType == "x_search" {
			foundXSearch = true
		}
		if toolType == "function" && !tool.Get("parameters").Exists() {
			t.Fatalf("tools.%d.parameters missing for xAI function tool; body=%s", i, string(gotBody))
		}
		if got := tool.Get("name").String(); got == "apply_patch" {
			t.Fatalf("tools.%d.name = apply_patch, want removed; body=%s", i, string(gotBody))
		}
		switch tool.Get("name").String() {
		case "codex_app__automation_update":
			foundAutomationUpdate = true
		case "codex_app__namespace_custom":
			foundNamespaceCustom = true
		}
		if toolType == "web_search" {
			if tool.Get("external_web_access").Exists() {
				t.Fatalf("tools.%d.external_web_access exists, want removed; body=%s", i, string(gotBody))
			}
			if got := tool.Get("search_content_types.1").String(); got != "image" {
				t.Fatalf("tools.%d.search_content_types missing image entry; body=%s", i, string(gotBody))
			}
		}
	}
	if !foundAutomationUpdate {
		t.Fatalf("namespace function tool was not moved to top-level tools; body=%s", string(gotBody))
	}
	if !foundNamespaceCustom {
		t.Fatalf("namespace custom tool was not moved to top-level tools; body=%s", string(gotBody))
	}
	if !foundXSearch {
		t.Fatalf("native x_search tool was not injected; body=%s", string(gotBody))
	}
}

func TestXAIExecutorExecuteRestoresAdditionalToolsNamespaceCalls(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"name\":\"mcp__exa__web_search_exa\",\"call_id\":\"call_1\",\"arguments\":\"{}\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"grok-4.3\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata:   map[string]any{"access_token": "xai-token"},
	}
	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model: "grok-4.3",
		Payload: []byte(`{
			"model":"grok-4.3",
			"input":[
				{"type":"additional_tools","role":"developer","tools":[{"type":"namespace","name":"mcp__exa","tools":[{"type":"function","name":"web_search_exa","parameters":{"type":"object"}}]}]},
				{"role":"user","content":"use Exa"}
			]
		}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	tool := gjson.GetBytes(gotBody, "input.0.tools.0")
	if got := tool.Get("name").String(); got != "mcp__exa__web_search_exa" {
		t.Fatalf("upstream additional tool name = %q, want qualified name; body=%s", got, gotBody)
	}
	if got := tool.Get("type").String(); got != "function" {
		t.Fatalf("upstream additional tool type = %q, want function; body=%s", got, gotBody)
	}
	if tool.Get("tools").Exists() {
		t.Fatalf("upstream additional tool should not contain namespace children: %s", gotBody)
	}
	output := gjson.GetBytes(resp.Payload, "output.0")
	if got := output.Get("name").String(); got != "web_search_exa" {
		t.Fatalf("response output name = %q, want child name; payload=%s", got, resp.Payload)
	}
	if got := output.Get("namespace").String(); got != "mcp__exa" {
		t.Fatalf("response output namespace = %q, want mcp__exa; payload=%s", got, resp.Payload)
	}
}

func TestXAIExecutorExecuteNormalizesCustomToolCallHistory(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		for _, item := range gjson.GetBytes(gotBody, "input").Array() {
			if strings.HasPrefix(item.Get("type").String(), "custom_tool_call") {
				http.Error(w, `{"error":"data did not match any variant of untagged enum ModelInput"}`, http.StatusUnprocessableEntity)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"model\":\"grok-4.5\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "xai",
		Attributes: map[string]string{
			"base_url":  server.URL,
			"auth_kind": "oauth",
		},
		Metadata: map[string]any{"access_token": "xai-token"},
	}
	payload := []byte(`{
		"model":"grok-4.5",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"search"}]},
			{"type":"custom_tool_call","name":"missing_call_id","input":"invalid"},
			{"type":"custom_tool_call_output","output":"missing call id"},
			{"type":"custom_tool_call","status":"completed","call_id":"xs_call-1","name":"x_semantic_search","input":"{\"query\":\"US stocks\",\"limit\":\"10\"}","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}},
			{"type":"custom_tool_call_output","call_id":"xs_call-1","output":"unsupported custom tool call: x_semantic_search","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}},
			{"type":"custom_tool_call","call_id":"call-2","name":"apply_patch","input":"*** Begin Patch"},
			{"type":"custom_tool_call_output","call_id":"call-2","output":[{"type":"input_text","text":"done"}]}
		],
		"tools":[{"type":"x_search"}],
		"tool_choice":"auto"
	}`)

	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4.5",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	input := gjson.GetBytes(gotBody, "input").Array()
	if len(input) != 5 {
		t.Fatalf("input length = %d, want 5; body=%s", len(input), gotBody)
	}
	if got := input[1].Get("type").String(); got != "function_call" {
		t.Fatalf("input.1.type = %q, want function_call; body=%s", got, gotBody)
	}
	if got := gjson.Get(input[1].Get("arguments").String(), "query").String(); got != "US stocks" {
		t.Fatalf("input.1 arguments query = %q, want US stocks; body=%s", got, gotBody)
	}
	if input[1].Get("input").Exists() || input[1].Get("internal_chat_message_metadata_passthrough").Exists() {
		t.Fatalf("input.1 contains unsupported custom fields: %s", input[1].Raw)
	}
	if got := input[2].Get("type").String(); got != "function_call_output" {
		t.Fatalf("input.2.type = %q, want function_call_output; body=%s", got, gotBody)
	}
	if got := input[2].Get("output").String(); got != "unsupported custom tool call: x_semantic_search" {
		t.Fatalf("input.2.output = %q; body=%s", got, gotBody)
	}
	if got := gjson.Get(input[3].Get("arguments").String(), "input").String(); got != "*** Begin Patch" {
		t.Fatalf("input.3 freeform arguments = %q, want patch input; body=%s", got, gotBody)
	}
	if got := input[4].Get("output").String(); got != `[{"type":"input_text","text":"done"}]` {
		t.Fatalf("input.4 output = %q, want flattened JSON string; body=%s", got, gotBody)
	}
	if got := gjson.GetBytes(gotBody, "tools.0.type").String(); got != "x_search" {
		t.Fatalf("tools.0.type = %q, want x_search; body=%s", got, gotBody)
	}
}

func TestXAIExecutorStreamRestoresNamespaceToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"name\":\"mcp__exa__web_search_exa\",\"call_id\":\"call_1\",\"arguments\":\"{}\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata:   map[string]any{"access_token": "xai-token"},
	}
	result, err := exec.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"mcp__exa","tools":[{"type":"function","name":"web_search_exa","parameters":{"type":"object"}}]}]},{"role":"user","content":"search"}]}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Stream: true})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	var stream bytes.Buffer
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
		stream.Write(chunk.Payload)
	}
	if !strings.Contains(stream.String(), `"name":"web_search_exa"`) || !strings.Contains(stream.String(), `"namespace":"mcp__exa"`) {
		t.Fatalf("stream did not restore namespace tool identity: %s", stream.String())
	}
}

func TestXAIExecutorExecuteFiltersInternalXSearchCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"ctc_1\",\"type\":\"custom_tool_call\",\"call_id\":\"xs_call-1\",\"name\":\"x_user_search\",\"input\":\"{}\",\"status\":\"completed\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}],\"status\":\"completed\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"output\":[{\"id\":\"ctc_1\",\"type\":\"custom_tool_call\",\"call_id\":\"xs_call-1\",\"name\":\"x_user_search\",\"input\":\"{}\"},{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata:   map[string]any{"access_token": "xai-token"},
	}
	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4.5",
		Payload: []byte(`{"model":"grok-4.5","input":"search X","tools":[{"type":"x_search"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(string(resp.Payload), "x_user_search") || strings.Contains(string(resp.Payload), "custom_tool_call") {
		t.Fatalf("internal X search call leaked into response: %s", resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "output.#").Int(); got != 1 {
		t.Fatalf("response output length = %d, want 1; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "output.0.content.0.text").String(); got != "answer" {
		t.Fatalf("response text = %q, want answer; payload=%s", got, resp.Payload)
	}
}

func TestXAIExecutorAcceptsIncompleteTerminalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"partial\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n"))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata:   map[string]any{"access_token": "xai-token"},
	}
	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-4.5",
		Payload: []byte(`{"model":"grok-4.5","input":"hello"}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "status").String(); got != "incomplete" {
		t.Fatalf("status = %q, want incomplete; payload=%s", got, resp.Payload)
	}
}

func TestEnsureXAINativeXSearchTool(t *testing.T) {
	t.Parallel()

	// Missing tools array: inject a top-level x_search tool.
	out := ensureXAINativeXSearchTool([]byte(`{"model":"grok-4.5","input":"hi"}`))
	tools := gjson.GetBytes(out, "tools").Array()
	if len(tools) != 1 {
		t.Fatalf("tools length = %d, want 1; body=%s", len(tools), out)
	}
	if got := tools[0].Get("type").String(); got != "x_search" {
		t.Fatalf("tools.0.type = %q, want x_search; body=%s", got, out)
	}

	// Existing tools without x_search: append once.
	out = ensureXAINativeXSearchTool([]byte(`{"tools":[{"type":"web_search"},{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`))
	tools = gjson.GetBytes(out, "tools").Array()
	if len(tools) != 3 {
		t.Fatalf("tools length = %d, want 3; body=%s", len(tools), out)
	}
	if got := tools[2].Get("type").String(); got != "x_search" {
		t.Fatalf("tools.2.type = %q, want x_search; body=%s", got, out)
	}

	// Already present: leave body unchanged (no duplicate).
	in := []byte(`{"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}},{"type":"x_search"}]}`)
	out = ensureXAINativeXSearchTool(in)
	tools = gjson.GetBytes(out, "tools").Array()
	if len(tools) != 2 {
		t.Fatalf("tools length = %d, want 2; body=%s", len(tools), out)
	}
	xSearchCount := 0
	for _, tool := range tools {
		if tool.Get("type").String() == "x_search" {
			xSearchCount++
		}
	}
	if xSearchCount != 1 {
		t.Fatalf("x_search count = %d, want 1; body=%s", xSearchCount, out)
	}

	// allowed_tools without x_search: append once so Grok may select it.
	out = ensureXAINativeXSearchTool([]byte(`{
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
		"tool_choice":{"type":"allowed_tools","tools":[{"type":"function","name":"lookup"}]}
	}`))
	if got := gjson.GetBytes(out, "tools.1.type").String(); got != "x_search" {
		t.Fatalf("tools.1.type = %q, want x_search; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tool_choice.tools.1.type").String(); got != "x_search" {
		t.Fatalf("tool_choice.tools.1.type = %q, want x_search; body=%s", got, out)
	}

	// allowed_tools already lists x_search: do not duplicate.
	out = ensureXAINativeXSearchTool([]byte(`{
		"tools":[{"type":"web_search"},{"type":"x_search"}],
		"tool_choice":{"type":"allowed_tools","tools":[{"type":"web_search"},{"type":"x_search"}]}
	}`))
	tools = gjson.GetBytes(out, "tools").Array()
	if len(tools) != 2 {
		t.Fatalf("tools length = %d, want 2; body=%s", len(tools), out)
	}
	allowed := gjson.GetBytes(out, "tool_choice.tools").Array()
	if len(allowed) != 2 {
		t.Fatalf("tool_choice.tools length = %d, want 2; body=%s", len(allowed), out)
	}
	xSearchAllowed := 0
	for _, tool := range allowed {
		if tool.Get("type").String() == "x_search" {
			xSearchAllowed++
		}
	}
	if xSearchAllowed != 1 {
		t.Fatalf("allowed_tools x_search count = %d, want 1; body=%s", xSearchAllowed, out)
	}
}

func TestPruneXAIOrphanedToolChoice(t *testing.T) {
	t.Parallel()

	// Forced choice for a removed tool is dropped.
	out := pruneXAIOrphanedToolChoice([]byte(`{
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
		"tool_choice":{"type":"image_generation"}
	}`))
	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("orphaned forced tool_choice should be removed: %s", out)
	}

	// allowed_tools keeps only still-available entries.
	out = pruneXAIOrphanedToolChoice([]byte(`{
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}},{"type":"web_search"}],
		"tool_choice":{"type":"allowed_tools","tools":[
			{"type":"function","name":"lookup"},
			{"type":"image_generation"},
			{"type":"web_search"}
		]}
	}`))
	allowed := gjson.GetBytes(out, "tool_choice.tools").Array()
	if len(allowed) != 2 {
		t.Fatalf("allowed_tools length = %d, want 2; body=%s", len(allowed), out)
	}
	if got := allowed[0].Get("name").String(); got != "lookup" {
		t.Fatalf("allowed_tools.0.name = %q, want lookup; body=%s", got, out)
	}
	if got := allowed[1].Get("type").String(); got != "web_search" {
		t.Fatalf("allowed_tools.1.type = %q, want web_search; body=%s", got, out)
	}

	// When every allowed entry is orphaned, drop tool_choice entirely.
	out = pruneXAIOrphanedToolChoice([]byte(`{
		"tools":[],
		"tool_choice":{"type":"allowed_tools","tools":[{"type":"image_generation"}]}
	}`))
	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("fully orphaned allowed_tools should be removed: %s", out)
	}

	// String choices are not tool references.
	in := []byte(`{"tools":[{"type":"web_search"}],"tool_choice":"auto"}`)
	if got := pruneXAIOrphanedToolChoice(in); !bytes.Equal(got, in) {
		t.Fatalf("string tool_choice changed: got=%s want=%s", got, in)
	}
}

func TestNormalizeXAIToolChoiceForTools_DropsWhenToolsEmpty(t *testing.T) {
	body := []byte(`{"model":"grok-4","tools":[],"tool_choice":"auto","parallel_tool_calls":true,"input":"hi"}`)
	out := normalizeXAIToolChoiceForTools(body)

	if gjson.GetBytes(out, "tools").Exists() {
		t.Fatalf("empty tools should be removed: %s", string(out))
	}
	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("tool_choice should be removed when tools empty: %s", string(out))
	}
	if gjson.GetBytes(out, "parallel_tool_calls").Exists() {
		t.Fatalf("parallel_tool_calls should be removed when tools empty: %s", string(out))
	}
}

func TestNormalizeXAIToolChoiceForTools_DropsWhenToolsMissing(t *testing.T) {
	body := []byte(`{"model":"grok-4","tool_choice":"auto","input":"hi"}`)
	out := normalizeXAIToolChoiceForTools(body)

	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("tool_choice should be removed when tools missing: %s", string(out))
	}
}

func TestNormalizeXAIToolChoiceForTools_DropsOrphanedParallelToolCalls(t *testing.T) {
	body := []byte(`{"model":"grok-4","parallel_tool_calls":true,"input":"hi"}`)
	out := normalizeXAIToolChoiceForTools(body)

	if gjson.GetBytes(out, "parallel_tool_calls").Exists() {
		t.Fatalf("parallel_tool_calls should be removed when tools missing even without tool_choice: %s", string(out))
	}
}

func TestNormalizeXAIToolChoiceForTools_KeepsWhenToolsPresent(t *testing.T) {
	body := []byte(`{"model":"grok-4","tools":[{"type":"function","name":"Bash"}],"tool_choice":"auto","input":"hi"}`)
	out := normalizeXAIToolChoiceForTools(body)

	if !gjson.GetBytes(out, "tools").Exists() {
		t.Fatalf("tools should be kept: %s", string(out))
	}
	if got := gjson.GetBytes(out, "tool_choice").String(); got != "auto" {
		t.Fatalf("tool_choice = %q, want auto: %s", got, string(out))
	}
}

func TestNormalizeXAIToolChoiceForTools_NoOpWhenBothAbsent(t *testing.T) {
	body := []byte(`{"model":"grok-4","input":"hi"}`)
	out := normalizeXAIToolChoiceForTools(body)

	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("tool_choice should not appear: %s", string(out))
	}
}
func TestXAIExecutorExecuteImagesUsesImagesEndpoint(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotAccept string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":123,"data":[{"b64_json":"AA=="}]}`))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "xai",
		Attributes: map[string]string{
			"base_url":  server.URL,
			"auth_kind": "oauth",
		},
		Metadata: map[string]any{"access_token": "xai-token"},
	}

	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-imagine-image",
		Payload: []byte(`{"model":"grok-imagine-image","prompt":"draw"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-image"),
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/images/generations",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if gotPath != "/images/generations" {
		t.Fatalf("path = %q, want /images/generations", gotPath)
	}
	if gotAuth != "Bearer xai-token" {
		t.Fatalf("Authorization = %q, want Bearer xai-token", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q, want application/json", gotAccept)
	}
	if string(gotBody) != `{"model":"grok-imagine-image","prompt":"draw"}` {
		t.Fatalf("body = %s", string(gotBody))
	}
	if gjson.GetBytes(resp.Payload, "data.0.b64_json").String() != "AA==" {
		t.Fatalf("payload = %s", string(resp.Payload))
	}
}

func TestXAIExecutorExecuteImagesUsesEditsEndpoint(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":123,"data":[{"url":"https://x.ai/image.png"}]}`))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata:   map[string]any{"access_token": "xai-token"},
	}

	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-imagine-image",
		Payload: []byte(`{"model":"grok-imagine-image","prompt":"edit","image":{"type":"image_url","url":"https://example.com/a.png"}}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-image"),
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/images/edits",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if gotPath != "/images/edits" {
		t.Fatalf("path = %q, want /images/edits", gotPath)
	}
}

func TestXAIExecutorExecuteVideosCreate(t *testing.T) {
	var gotPath string
	var gotMethod string
	var gotAuth string
	var gotIdempotencyKey string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotIdempotencyKey = r.Header.Get("x-idempotency-key")
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"vid_123"}`))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata:   map[string]any{"access_token": "xai-token"},
	}

	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-imagine-video",
		Payload: []byte(`{"model":"grok-imagine-video","prompt":"animate","duration":4}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-video"),
		Metadata: map[string]any{
			"idempotency_key": "idem-123",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/videos/generations" {
		t.Fatalf("path = %q, want /videos/generations", gotPath)
	}
	if gotAuth != "Bearer xai-token" {
		t.Fatalf("Authorization = %q, want Bearer xai-token", gotAuth)
	}
	if gotIdempotencyKey != "idem-123" {
		t.Fatalf("x-idempotency-key = %q, want idem-123", gotIdempotencyKey)
	}
	if string(gotBody) != `{"model":"grok-imagine-video","prompt":"animate","duration":4}` {
		t.Fatalf("body = %s", string(gotBody))
	}
	if gjson.GetBytes(resp.Payload, "request_id").String() != "vid_123" {
		t.Fatalf("payload = %s", string(resp.Payload))
	}
}

func TestXAIExecutorExecuteVideosRetrieve(t *testing.T) {
	var gotPath string
	var gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"done","video":{"url":"https://vidgen.x.ai/video.mp4","duration":6},"model":"grok-imagine-video","progress":100}`))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata:   map[string]any{"access_token": "xai-token"},
	}

	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-imagine-video",
		Payload: []byte(`{"request_id":"vid_123"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-video"),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Fatalf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/videos/vid_123" {
		t.Fatalf("path = %q, want /videos/vid_123", gotPath)
	}
	if gjson.GetBytes(resp.Payload, "video.url").String() != "https://vidgen.x.ai/video.mp4" {
		t.Fatalf("payload = %s", string(resp.Payload))
	}
}

func TestXAIExecutorExecuteVideosUsesNativeEndpointFromRequestPath(t *testing.T) {
	tests := []struct {
		name        string
		requestPath string
		wantPath    string
	}{
		{
			name:        "generations",
			requestPath: "/v1/videos/generations",
			wantPath:    "/videos/generations",
		},
		{
			name:        "edits",
			requestPath: "/v1/videos/edits",
			wantPath:    "/videos/edits",
		},
		{
			name:        "extensions",
			requestPath: "/v1/videos/extensions",
			wantPath:    "/videos/extensions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotMethod string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotMethod = r.Method
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"request_id":"vid_123"}`))
			}))
			defer server.Close()

			exec := NewXAIExecutor(&config.Config{})
			auth := &cliproxyauth.Auth{
				Provider:   "xai",
				Attributes: map[string]string{"base_url": server.URL},
				Metadata:   map[string]any{"access_token": "xai-token"},
			}

			_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
				Model:   "grok-imagine-video",
				Payload: []byte(`{"model":"grok-imagine-video","prompt":"animate"}`),
			}, cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FromString("openai-video"),
				Metadata: map[string]any{
					cliproxyexecutor.RequestPathMetadataKey: tt.requestPath,
				},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if gotMethod != http.MethodPost {
				t.Fatalf("method = %q, want POST", gotMethod)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("path = %q, want %s", gotPath, tt.wantPath)
			}
		})
	}
}

func TestNormalizeXAIImageRefsRewritesImageURLField(t *testing.T) {
	in := []byte(`{
		"image":{"type":"image_url","image_url":"https://example.com/a.png"},
		"images":[{"image_url":{"url":"https://example.com/b.png"}},{"url":"https://example.com/c.png","image_url":"https://example.com/ignored.png"}],
		"reference_images":[{"image_url":"https://example.com/d.png"}],
		"nested":{"image":{"image_url":"https://example.com/e.png"}},
		"content":[{"type":"image_url","image_url":{"url":"https://example.com/keep.png"}}]
	}`)

	out := normalizeXAIImageRefs(in)

	for path, want := range map[string]string{
		"image.url":               "https://example.com/a.png",
		"images.0.url":            "https://example.com/b.png",
		"images.1.url":            "https://example.com/c.png",
		"reference_images.0.url":  "https://example.com/d.png",
		"nested.image.url":        "https://example.com/e.png",
		"content.0.image_url.url": "https://example.com/keep.png",
	} {
		if got := gjson.GetBytes(out, path).String(); got != want {
			t.Fatalf("%s = %q, want %q; body=%s", path, got, want, out)
		}
	}
	for _, path := range []string{"image.image_url", "images.0.image_url", "images.1.image_url", "reference_images.0.image_url", "nested.image.image_url"} {
		if gjson.GetBytes(out, path).Exists() {
			t.Fatalf("%s should be removed; body=%s", path, out)
		}
	}
	if gjson.GetBytes(out, "content.0.url").Exists() {
		t.Fatalf("chat content image part must remain unchanged; body=%s", out)
	}
}

func TestXAIExecutorExecuteImagesRewritesImageURLToURL(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var errRead error
		gotBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":123,"data":[{"url":"https://x.ai/image.png"}]}`))
	}))
	defer server.Close()

	exec := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata:   map[string]any{"access_token": "xai-token"},
	}

	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "grok-imagine-image",
		Payload: []byte(`{"model":"grok-imagine-image","prompt":"edit","image":{"image_url":"https://example.com/a.png"}}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-image"),
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/images/edits",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := gjson.GetBytes(gotBody, "image.url").String(); got != "https://example.com/a.png" {
		t.Fatalf("upstream image.url = %q; body=%s", got, gotBody)
	}
	if gjson.GetBytes(gotBody, "image.image_url").Exists() {
		t.Fatalf("upstream body still has image.image_url: %s", gotBody)
	}
}

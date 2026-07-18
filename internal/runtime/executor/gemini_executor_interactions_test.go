package executor

import (
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

func TestGeminiInteractionsExecutorUsesNativeEndpoint(t *testing.T) {
	var gotPath, gotRevision string
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRevision = r.Header.Get("Api-Revision")
		var errRead error
		upstreamBody, errRead = io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read request body: %v", errRead)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"interaction_1","status":"completed","steps":[{"type":"model_output","content":[{"text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	exec := NewGeminiInteractionsExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "gemini-interactions", Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
	resp, errExecute := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "agents/test-agent",
		Payload: []byte(`{"agent":"agents/test-agent","input":"hi"}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatInteractions})
	if errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}
	if gotPath != "/v1beta/interactions" || gotRevision != geminiInteractionsAPIRevision {
		t.Fatalf("upstream target = %q revision %q", gotPath, gotRevision)
	}
	if got := gjson.GetBytes(upstreamBody, "agent").String(); got != "agents/test-agent" {
		t.Fatalf("upstream agent = %q, want agents/test-agent", got)
	}
	if gjson.GetBytes(upstreamBody, "model").Exists() {
		t.Fatalf("agent request unexpectedly gained model: %s", upstreamBody)
	}
	if got := gjson.GetBytes(resp.Payload, "id").String(); got != "interaction_1" {
		t.Fatalf("response id = %q, want interaction_1", got)
	}
}

func TestGeminiExecutorInteractionsSourceKeepsGeminiEndpointForGeminiAuth(t *testing.T) {
	var gotPath string
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`))
	}))
	defer server.Close()

	exec := NewGeminiExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "gemini", Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
	_, errExecute := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gemini-test",
		Payload: []byte(`{"model":"gemini-test","input":"hi"}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatInteractions})
	if errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}
	if gotPath != "/v1beta/models/gemini-test:generateContent" {
		t.Fatalf("path = %q, want Gemini generateContent endpoint", gotPath)
	}
	if got := gjson.GetBytes(upstreamBody, "contents.0.parts.0.text").String(); got != "hi" {
		t.Fatalf("translated text = %q, want hi. Body: %s", got, upstreamBody)
	}
}

func TestGeminiInteractionsExecutorStreamsNativeSSEFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: interaction.completed\ndata: {\"event_type\":\"interaction.completed\",\"interaction\":{\"id\":\"i1\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
		_, _ = io.WriteString(w, "event: done\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	exec := NewGeminiInteractionsExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "gemini-interactions", Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
	result, errExecute := exec.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gemini-test",
		Payload: []byte(`{"model":"gemini-test","input":"hi","stream":true}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatInteractions, Stream: true})
	if errExecute != nil {
		t.Fatalf("ExecuteStream() error = %v", errExecute)
	}
	var output strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		output.Write(chunk.Payload)
	}
	if !strings.Contains(output.String(), "event: interaction.completed") || !strings.Contains(output.String(), "event: done") {
		t.Fatalf("native SSE frames not preserved: %q", output.String())
	}
}

func TestGeminiInteractionsExecutorPrefersAuthRevisionHeader(t *testing.T) {
	var gotRevision string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRevision = r.Header.Get("Api-Revision")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"interaction_1","status":"completed","steps":[]}`))
	}))
	defer server.Close()

	exec := NewGeminiInteractionsExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "gemini-interactions", Attributes: map[string]string{
		"api_key":             "test-key",
		"base_url":            server.URL,
		"header:Api-Revision": "2026-06-01",
	}}
	_, errExecute := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "agents/test-agent",
		Payload: []byte(`{"agent":"agents/test-agent","input":"hi"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatInteractions,
		Headers:      http.Header{"Api-Revision": []string{"2026-07-01"}},
	})
	if errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}
	if gotRevision != "2026-06-01" {
		t.Fatalf("Api-Revision = %q, want auth header", gotRevision)
	}
}

func TestGeminiInteractionsExecutorTranslatesOpenAIResponses(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"interaction_1","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	exec := NewGeminiInteractionsExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "gemini-interactions", Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
	resp, errExecute := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gemini-test",
		Payload: []byte(`{"model":"gemini-test","instructions":"be brief","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"reasoning":{"effort":"high","summary":"auto"}}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
	if errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}
	if got := gjson.GetBytes(upstreamBody, "input.0.type").String(); got != "user_input" {
		t.Fatalf("translated input type = %q, want user_input. Body: %s", got, upstreamBody)
	}
	if got := gjson.GetBytes(upstreamBody, "generation_config.thinking_level").String(); got != "high" {
		t.Fatalf("thinking level = %q, want high. Body: %s", got, upstreamBody)
	}
	if got := gjson.GetBytes(resp.Payload, "output.0.content.0.text").String(); got != "ok" {
		t.Fatalf("response text = %q, want ok. Payload: %s", got, resp.Payload)
	}
}

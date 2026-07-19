package executor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/gin-gonic/gin"
	internalcache "github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestXAIReasoningReplayPrepareInjectsExecutionScopedReasoning(t *testing.T) {
	internalcache.ClearXAIReasoningReplayCache()
	t.Cleanup(internalcache.ClearXAIReasoningReplayCache)
	encrypted := testExecutorGrokEncryptedContent(3)
	if !internalcache.CacheXAIReasoningReplayItem("grok-4.3", "execution:session-1", []byte(`{"type":"reasoning","encrypted_content":"`+encrypted+`"}`)) {
		t.Fatal("failed to seed replay cache")
	}
	exec := NewXAIExecutor(&config.Config{})
	prepared, err := exec.prepareResponsesRequest(context.Background(), cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","input":[{"type":"message","role":"user","content":"next"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Metadata:     map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: "session-1"},
	}, false)
	if err != nil {
		t.Fatalf("prepareResponsesRequest() error = %v", err)
	}
	if got := gjson.GetBytes(prepared.body, "input.0.type").String(); got != "reasoning" {
		t.Fatalf("input.0.type = %q, want reasoning; body=%s", got, prepared.body)
	}
	if got := gjson.GetBytes(prepared.body, "input.1.role").String(); got != "user" {
		t.Fatalf("input.1.role = %q, want user; body=%s", got, prepared.body)
	}
}

func TestXAIReasoningReplayInsertsToolCallBeforeMatchingOutput(t *testing.T) {
	items := [][]byte{[]byte(`{"type":"function_call","call_id":"call-1","name":"lookup","arguments":"{}"}`)}
	body := []byte(`{"input":[{"type":"function_call_output","call_id":"call-1","output":"ok"},{"type":"message","role":"user","content":"next"}]}`)
	filtered := filterXAIReasoningReplayItemsForInput(body, items)
	if len(filtered) != 1 {
		t.Fatalf("filtered replay count = %d, want 1", len(filtered))
	}
	updated, ok := insertXAIReasoningReplayItems(body, filtered)
	if !ok {
		t.Fatal("insertXAIReasoningReplayItems() failed")
	}
	if got := gjson.GetBytes(updated, "input.0.type").String(); got != "function_call" {
		t.Fatalf("input.0.type = %q, want function_call; body=%s", got, updated)
	}
	if got := gjson.GetBytes(updated, "input.1.type").String(); got != "function_call_output" {
		t.Fatalf("input.1.type = %q, want function_call_output; body=%s", got, updated)
	}
}

func TestXAIReasoningReplaySkipsAmbiguousAssistantHistory(t *testing.T) {
	items := [][]byte{
		[]byte(`{"type":"reasoning","encrypted_content":"` + testExecutorGrokEncryptedContent(4) + `"}`),
		[]byte(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"old answer"}]}`),
	}
	body := []byte(`{"input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"different answer"}]},{"type":"message","role":"user","content":"next"}]}`)
	if got := filterXAIReasoningReplayItemsForInput(body, items); len(got) != 0 {
		t.Fatalf("ambiguous replay items = %d, want 0", len(got))
	}
}

func TestClearXAIReasoningReplayOnInvalidSignature(t *testing.T) {
	internalcache.ClearXAIReasoningReplayCache()
	t.Cleanup(internalcache.ClearXAIReasoningReplayCache)
	if !internalcache.CacheXAIReasoningReplayItem("grok-4.3", "execution:session-2", []byte(`{"type":"function_call","call_id":"call-1","name":"lookup","arguments":"{}"}`)) {
		t.Fatal("failed to seed replay cache")
	}
	scope := xaiReasoningReplayScope{modelName: "grok-4.3", sessionKey: "execution:session-2"}
	if err := clearXAIReasoningReplayOnInvalidSignature(context.Background(), scope, 400, []byte(`{"error":{"code":"invalid_encrypted_content"}}`)); err != nil {
		t.Fatalf("clearXAIReasoningReplayOnInvalidSignature() error = %v", err)
	}
	if _, ok := internalcache.GetXAIReasoningReplayItem("grok-4.3", "execution:session-2"); ok {
		t.Fatal("invalid signature should clear replay cache")
	}
}

func TestXAIReasoningReplayScopeIsolatesCallerControlledSession(t *testing.T) {
	request := cliproxyexecutor.Request{Model: "grok-4.3", Payload: []byte(`{"prompt_cache_key":"shared"}`)}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse}
	scopeA := xaiReasoningReplayScopeFromRequest(testXAIContextWithAPIKey("key-a"), sdktranslator.FormatOpenAIResponse, request, opts, request.Payload)
	scopeB := xaiReasoningReplayScopeFromRequest(testXAIContextWithAPIKey("key-b"), sdktranslator.FormatOpenAIResponse, request, opts, request.Payload)
	if !scopeA.valid() || !scopeB.valid() || scopeA.sessionKey == scopeB.sessionKey {
		t.Fatalf("caller scopes were not isolated: %#v %#v", scopeA, scopeB)
	}
	if scope := xaiReasoningReplayScopeFromRequest(context.Background(), sdktranslator.FormatOpenAIResponse, request, opts, request.Payload); scope.valid() {
		t.Fatalf("unisolated caller scope should be disabled: %#v", scope)
	}
}

func testXAIContextWithAPIKey(apiKey string) context.Context {
	gin.SetMode(gin.TestMode)
	ginCtx := &gin.Context{}
	ginCtx.Set("apiKey", apiKey)
	return context.WithValue(context.Background(), "gin", ginCtx)
}

func testExecutorGrokEncryptedContent(seed byte) string {
	buf := make([]byte, 0, 128)
	for i := 0; len(buf) < 128; i++ {
		sum := sha256.Sum256([]byte{seed, byte(i), byte(i >> 8)})
		buf = append(buf, sum[:]...)
	}
	return base64.RawStdEncoding.EncodeToString(buf[:128])
}

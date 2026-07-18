package gemini

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

func TestParseInteractionsRequestTargetRequiresExactlyOneTarget(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "missing", raw: `{}`, want: "request requires exactly one of model or agent"},
		{name: "both", raw: `{"model":"gemini-test","agent":"agent-test"}`, want: "request requires exactly one of model or agent"},
		{name: "bad stream", raw: `{"model":"gemini-test","stream":"true"}`, want: "stream must be a boolean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseInteractionsRequestTarget([]byte(tt.raw)); err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPrepareInteractionsExecutionNormalizesModelResource(t *testing.T) {
	target, err := ParseInteractionsRequestTarget([]byte(`{"model":"models/gemini-test","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	model, body, metadata := prepareInteractionsExecution([]byte(`{"model":"models/gemini-test","stream":true}`), target)
	if model != "gemini-test" || string(body) != `{"model":"gemini-test","stream":true}` {
		t.Fatalf("normalized target = %q, body = %s", model, body)
	}
	if metadata.EntryProtocol != "interactions" || metadata.ExitProtocol != "interactions" || !metadata.Stream {
		t.Fatalf("unexpected execution metadata: %+v", metadata)
	}
}

func TestPrepareInteractionsExecutionUsesAgentAuthSelectionModel(t *testing.T) {
	target, err := ParseInteractionsRequestTarget([]byte(`{"agent":"agents/researcher"}`))
	if err != nil {
		t.Fatal(err)
	}
	model, body, metadata := prepareInteractionsExecution([]byte(`{"agent":"agents/researcher"}`), target)
	if model != "agents/researcher" || string(body) != `{"agent":"agents/researcher"}` {
		t.Fatalf("agent target = %q, body = %s", model, body)
	}
	if metadata.Agent != "agents/researcher" || metadata.AuthSelectionModel != interactionsAgentAuthSelectionModel {
		t.Fatalf("unexpected agent metadata: %+v", metadata)
	}
}

func TestInteractionsAgentExecutesThroughNativeProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotPath string
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"interaction_1","status":"completed","steps":[{"type":"model_output","content":[{"text":"ok"}]}]}`))
	}))
	defer server.Close()

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor.NewGeminiInteractionsExecutor(&config.Config{}))
	auth := &coreauth.Auth{ID: "interactions-handler-native", Provider: "gemini-interactions", Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: interactionsAgentAuthSelectionModel}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	manager.RefreshSchedulerEntry(auth.ID)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1beta/interactions", strings.NewReader(`{"agent":"agents/test-agent","input":"hi"}`))
	h := NewGeminiAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager))
	h.Interactions(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if gotPath != "/v1beta/interactions" {
		t.Fatalf("path = %q, want /v1beta/interactions", gotPath)
	}
	if got := gjson.GetBytes(upstreamBody, "agent").String(); got != "agents/test-agent" {
		t.Fatalf("upstream agent = %q, want agents/test-agent", got)
	}
}

func TestInteractionsAgentDoesNotFallbackToGemini(t *testing.T) {
	gin.SetMode(gin.TestMode)
	geminiCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geminiCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"unexpected"}]}}]}`))
	}))
	defer server.Close()

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor.NewGeminiExecutor(&config.Config{}))
	auth := &coreauth.Auth{ID: "interactions-agent-gemini-only", Provider: "gemini", Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: interactionsAgentAuthSelectionModel}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	manager.RefreshSchedulerEntry(auth.ID)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1beta/interactions", strings.NewReader(`{"agent":"agents/test-agent","input":"hi"}`))
	h := NewGeminiAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager))
	h.Interactions(ctx)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	if geminiCalls != 0 {
		t.Fatalf("Gemini fallback calls = %d, want 0", geminiCalls)
	}
}

func TestInteractionsModelExecutesThroughGeminiTranslation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const model = "interactions-runtime-model"
	var gotPath string
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"translated-ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`))
	}))
	defer server.Close()

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor.NewGeminiExecutor(&config.Config{}))
	auth := &coreauth.Auth{ID: "interactions-handler-gemini", Provider: "gemini", Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	manager.RefreshSchedulerEntry(auth.ID)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1beta/interactions", strings.NewReader(`{"model":"`+model+`","input":"hi"}`))
	h := NewGeminiAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager))
	h.Interactions(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if gotPath != "/v1beta/models/"+model+":generateContent" {
		t.Fatalf("path = %q, want Gemini generateContent", gotPath)
	}
	if got := gjson.GetBytes(upstreamBody, "contents.0.parts.0.text").String(); got != "hi" {
		t.Fatalf("translated request text = %q, want hi. Body: %s", got, upstreamBody)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "steps.0.content.0.text").String(); got != "translated-ok" {
		t.Fatalf("translated response text = %q, want translated-ok. Body: %s", got, recorder.Body.String())
	}
}

func TestInteractionsModelFallsBackAfterNativeProviderFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const model = "interactions-failover-model"
	nativeCalls := 0
	nativeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeCalls++
		http.Error(w, `{"error":"native unavailable"}`, http.StatusTooManyRequests)
	}))
	defer nativeServer.Close()
	geminiCalls := 0
	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geminiCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"fallback-ok"}]},"finishReason":"STOP"}]}`))
	}))
	defer geminiServer.Close()

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor.NewGeminiInteractionsExecutor(&config.Config{}))
	manager.RegisterExecutor(executor.NewGeminiExecutor(&config.Config{}))
	auths := []*coreauth.Auth{
		{ID: "interactions-failover-native", Provider: "gemini-interactions", Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "native-key", "base_url": nativeServer.URL}},
		{ID: "interactions-failover-gemini", Provider: "gemini", Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "gemini-key", "base_url": geminiServer.URL}},
	}
	for _, auth := range auths {
		registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
		if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
			t.Fatalf("Register(%s) error = %v", auth.Provider, errRegister)
		}
		manager.RefreshSchedulerEntry(auth.ID)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1beta/interactions", strings.NewReader(`{"model":"`+model+`","input":"hi"}`))
	h := NewGeminiAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager))
	h.Interactions(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if nativeCalls != 1 || geminiCalls != 1 {
		t.Fatalf("provider calls = native:%d gemini:%d, want 1 each", nativeCalls, geminiCalls)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "steps.0.content.0.text").String(); got != "fallback-ok" {
		t.Fatalf("fallback response text = %q, want fallback-ok. Body: %s", got, recorder.Body.String())
	}
}

func TestInteractionsModelExecutesThroughAntigravityTranslation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const model = "interactions-antigravity-bridge-model"
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:generateContent" {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"responseId":"resp_1","candidates":[{"content":{"role":"model","parts":[{"text":"translated-ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}}`))
	}))
	defer server.Close()

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor.NewAntigravityExecutor(&config.Config{RequestRetry: 1}))
	auth := &coreauth.Auth{
		ID:       "interactions-handler-antigravity",
		Provider: "antigravity",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"base_url": server.URL,
		},
		Metadata: map[string]any{
			"access_token": "token",
			"project_id":   "project-1",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	manager.RefreshSchedulerEntry(auth.ID)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1beta/interactions", strings.NewReader(`{"model":"`+model+`","input":"hi","generation_config":{"top_p":0.8}}`))
	h := NewGeminiAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager))
	h.Interactions(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if gjson.GetBytes(upstreamBody, "input").Exists() {
		t.Fatalf("upstream body still contains raw Interactions input: %s", upstreamBody)
	}
	if got := gjson.GetBytes(upstreamBody, "request.contents.0.parts.0.text").String(); got != "hi" {
		t.Fatalf("upstream request text = %q, want hi. Body: %s", got, upstreamBody)
	}
	if got := gjson.GetBytes(upstreamBody, "request.generationConfig.topP").Float(); got != 0.8 {
		t.Fatalf("upstream topP = %v, want 0.8. Body: %s", got, upstreamBody)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "steps.0.content.0.text").String(); got != "translated-ok" {
		t.Fatalf("response text = %q, want translated-ok. Body: %s", got, recorder.Body.String())
	}
}

func TestForwardInteractionsStreamWrapsBareJSONAsSSEData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1beta/interactions", strings.NewReader(`{}`))
	data := make(chan []byte, 1)
	errs := make(chan *interfaces.ErrorMessage)
	data <- []byte(`{"event_type":"interaction.completed"}`)
	close(data)
	close(errs)
	h := NewGeminiAPIHandler(&handlers.BaseAPIHandler{})

	h.forwardInteractionsStream(ctx, recorder, func(error) {}, data, errs)

	if got := recorder.Body.String(); got != "data: {\"event_type\":\"interaction.completed\"}\n\n" {
		t.Fatalf("body = %q, want SSE data frame", got)
	}
}

func TestForwardInteractionsStreamPreservesNativeSSEFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1beta/interactions", strings.NewReader(`{}`))
	data := make(chan []byte, 1)
	errs := make(chan *interfaces.ErrorMessage)
	frame := []byte("event: interaction.completed\ndata: {\"event_type\":\"interaction.completed\"}\n\n")
	data <- frame
	close(data)
	close(errs)
	h := NewGeminiAPIHandler(&handlers.BaseAPIHandler{})

	h.forwardInteractionsStream(ctx, recorder, func(error) {}, data, errs)

	if got := recorder.Body.String(); got != string(frame) {
		t.Fatalf("body = %q, want native SSE frame", got)
	}
}

package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	xaiauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/xai"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestXAIChatRoutingAndHeaders(t *testing.T) {
	tests := []struct {
		name       string
		auth       *cliproxyauth.Auth
		wantBase   string
		wantProxy  bool
		wantUserUA bool
	}{
		{
			name:       "oauth defaults to cli proxy",
			auth:       &cliproxyauth.Auth{Attributes: map[string]string{"auth_kind": "oauth", "base_url": xaiauth.DefaultAPIBaseURL}},
			wantBase:   xaiauth.CLIChatProxyBaseURL,
			wantProxy:  true,
			wantUserUA: true,
		},
		{
			name:     "api key defaults to official api",
			auth:     &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "key", "base_url": xaiauth.DefaultAPIBaseURL}},
			wantBase: xaiauth.DefaultAPIBaseURL,
		},
		{
			name:     "oauth using api stays official",
			auth:     &cliproxyauth.Auth{Attributes: map[string]string{"auth_kind": "oauth", "using_api": "true", "base_url": xaiauth.DefaultAPIBaseURL}},
			wantBase: xaiauth.DefaultAPIBaseURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := xaiChatBaseURL(tt.auth); got != tt.wantBase {
				t.Fatalf("xaiChatBaseURL() = %q, want %q", got, tt.wantBase)
			}
			req := httptest.NewRequest(http.MethodPost, "https://example.test/v1/responses", nil)
			applyXAIChatHeaders(req, tt.auth, "token", true, "session")
			if got := req.Header.Get(xaiTokenAuthHeader); (got == xaiTokenAuthValue) != tt.wantProxy {
				t.Fatalf("%s = %q, proxy=%v", xaiTokenAuthHeader, got, tt.wantProxy)
			}
			if got := req.Header.Get(xaiClientVersionHeader); (got == xaiClientVersionValue) != tt.wantProxy {
				t.Fatalf("%s = %q, proxy=%v", xaiClientVersionHeader, got, tt.wantProxy)
			}
			if got := req.Header.Get("User-Agent"); (got == "xai-grok-workspace/"+xaiClientVersionValue) != tt.wantUserUA {
				t.Fatalf("User-Agent = %q, wantProxyUA=%v", got, tt.wantUserUA)
			}
		})
	}
}

func TestXAIComposerSessionIsolationPreservesExplicitKeys(t *testing.T) {
	exec := NewXAIExecutor(&config.Config{})
	base := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Stream: true}

	generated, err := exec.prepareResponsesRequest(context.Background(), cliproxyexecutor.Request{
		Model:   "grok-composer-2.5-fast",
		Payload: []byte(`{"model":"grok-composer-2.5-fast","input":"hello"}`),
	}, base, true)
	if err != nil {
		t.Fatalf("prepare generated request: %v", err)
	}
	if _, errParse := uuid.Parse(generated.sessionID); errParse != nil {
		t.Fatalf("generated sessionID = %q, want UUID", generated.sessionID)
	}
	if got := gjson.GetBytes(generated.body, "prompt_cache_key").String(); got != generated.sessionID {
		t.Fatalf("prompt_cache_key = %q, want %q", got, generated.sessionID)
	}

	for _, tt := range []struct {
		name    string
		request cliproxyexecutor.Request
		opts    cliproxyexecutor.Options
		want    string
	}{
		{
			name:    "payload prompt cache key",
			request: cliproxyexecutor.Request{Model: "grok-composer-2.5-fast", Payload: []byte(`{"prompt_cache_key":"payload-key","input":"hello"}`)},
			want:    "payload-key",
		},
		{
			name:    "request execution session wins",
			request: cliproxyexecutor.Request{Model: "grok-composer-2.5-fast", Payload: []byte(`{"prompt_cache_key":"payload-key","input":"hello"}`), Metadata: map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: "request-key"}},
			want:    "request-key",
		},
		{
			name:    "option execution session wins",
			request: cliproxyexecutor.Request{Model: "grok-composer-2.5-fast", Payload: []byte(`{"prompt_cache_key":"payload-key","input":"hello"}`)},
			opts:    cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Metadata: map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: "option-key"}},
			want:    "option-key",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.opts
			if opts.SourceFormat.String() == "" {
				opts.SourceFormat = sdktranslator.FormatOpenAIResponse
			}
			prepared, errPrepare := exec.prepareResponsesRequest(context.Background(), tt.request, opts, true)
			if errPrepare != nil {
				t.Fatalf("prepare request: %v", errPrepare)
			}
			if prepared.sessionID != tt.want {
				t.Fatalf("sessionID = %q, want %q", prepared.sessionID, tt.want)
			}
			if got := gjson.GetBytes(prepared.body, "prompt_cache_key").String(); got != tt.want {
				t.Fatalf("prompt_cache_key = %q, want %q", got, tt.want)
			}
		})
	}

	stateless, err := exec.prepareResponsesRequest(context.Background(), cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","input":"hello"}`),
	}, base, true)
	if err != nil {
		t.Fatalf("prepare stateless request: %v", err)
	}
	if stateless.sessionID != "" || gjson.GetBytes(stateless.body, "prompt_cache_key").Exists() {
		t.Fatalf("stateless model unexpectedly received session: %#v body=%s", stateless.sessionID, stateless.body)
	}
}

func TestXAIStatusErrFreeUsageShapes(t *testing.T) {
	for _, body := range []string{
		`{"code":"subscription:free-usage-exhausted","error":"included free usage exhausted"}`,
		`{"error":{"code":"subscription:free-usage-exhausted","message":"usage exhausted"}}`,
		`{"message":"You've used all the included free usage for now"}`,
	} {
		err := xaiStatusErr(http.StatusTooManyRequests, []byte(body))
		if err.RetryAfter() == nil || *err.RetryAfter() != 24*time.Hour {
			t.Fatalf("body %s: RetryAfter = %v, want 24h", body, err.RetryAfter())
		}
	}

	for _, tt := range []struct {
		code int
		body string
	}{
		{code: http.StatusTooManyRequests, body: `{"code":"rate_limit","error":"too many requests"}`},
		{code: http.StatusBadRequest, body: `{"code":"subscription:free-usage-exhausted"}`},
	} {
		err := xaiStatusErr(tt.code, []byte(tt.body))
		if err.RetryAfter() != nil {
			t.Fatalf("code=%d body=%s: unexpected RetryAfter %v", tt.code, tt.body, *err.RetryAfter())
		}
	}
}

func TestXAIChatRouteUsesCapturedCustomGatewayWithoutProxyIdentity(t *testing.T) {
	var gotTokenAuth, gotClientVersion, gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTokenAuth = r.Header.Get(xaiTokenAuthHeader)
		gotClientVersion = r.Header.Get(xaiClientVersionHeader)
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}

`))
	}))
	defer server.Close()

	_, err := NewXAIExecutor(&config.Config{}).Execute(context.Background(), &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"auth_kind": "oauth", "base_url": server.URL},
		Metadata:   map[string]any{"access_token": "token"},
	}, cliproxyexecutor.Request{Model: "grok-4.3", Payload: []byte(`{"model":"grok-4.3","input":"hi"}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotTokenAuth != "" || gotClientVersion != "" || gotUserAgent == "xai-grok-workspace/"+xaiClientVersionValue {
		t.Fatalf("custom gateway received proxy identity: token=%q version=%q ua=%q", gotTokenAuth, gotClientVersion, gotUserAgent)
	}

}

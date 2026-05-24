package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	internalcodex "github.com/router-for-me/CLIProxyAPI/v6/internal/codex"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

const requestScopedNotFoundMessage = "Item with id 'rs_0b5f3eb6f51f175c0169ca74e4a85881998539920821603a74' not found. Items are not persisted when `store` is set to false. Try again with `store` set to true, or remove this item from your input."

func TestManager_ShouldRetryAfterError_RespectsAuthRequestRetryOverride(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(3, 30*time.Second, 0)

	model := "test-model"
	next := time.Now().Add(5 * time.Second)

	auth := &Auth{
		ID:       "auth-1",
		Provider: "claude",
		Metadata: map[string]any{
			"request_retry": float64(0),
		},
		ModelStates: map[string]*ModelState{
			model: {
				Unavailable:    true,
				Status:         StatusError,
				NextRetryAfter: next,
			},
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	_, _, maxWait := m.retrySettings()
	wait, shouldRetry := m.shouldRetryAfterError(&Error{HTTPStatus: 500, Message: "boom"}, 0, []string{"claude"}, model, maxWait)
	if shouldRetry {
		t.Fatalf("expected shouldRetry=false for request_retry=0, got true (wait=%v)", wait)
	}

	auth.Metadata["request_retry"] = float64(1)
	if _, errUpdate := m.Update(context.Background(), auth); errUpdate != nil {
		t.Fatalf("update auth: %v", errUpdate)
	}

	wait, shouldRetry = m.shouldRetryAfterError(&Error{HTTPStatus: 500, Message: "boom"}, 0, []string{"claude"}, model, maxWait)
	if !shouldRetry {
		t.Fatalf("expected shouldRetry=true for request_retry=1, got false")
	}
	if wait <= 0 {
		t.Fatalf("expected wait > 0, got %v", wait)
	}

	_, shouldRetry = m.shouldRetryAfterError(&Error{HTTPStatus: 500, Message: "boom"}, 1, []string{"claude"}, model, maxWait)
	if shouldRetry {
		t.Fatalf("expected shouldRetry=false on attempt=1 for request_retry=1, got true")
	}
}

func TestManager_ShouldRetryAfterError_UsesOAuthModelAliasForCooldown(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(3, 30*time.Second, 0)
	m.SetOAuthModelAlias(map[string][]internalconfig.OAuthModelAlias{
		"kimi": {
			{Name: "deepseek-v3.1", Alias: "pool-model"},
		},
	})

	routeModel := "pool-model"
	upstreamModel := "deepseek-v3.1"
	next := time.Now().Add(5 * time.Second)

	auth := &Auth{
		ID:       "auth-1",
		Provider: "kimi",
		ModelStates: map[string]*ModelState{
			upstreamModel: {
				Unavailable:    true,
				Status:         StatusError,
				NextRetryAfter: next,
				Quota: QuotaState{
					Exceeded:      true,
					Reason:        "quota",
					NextRecoverAt: next,
				},
			},
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	_, _, maxWait := m.retrySettings()
	wait, shouldRetry := m.shouldRetryAfterError(&Error{HTTPStatus: 429, Message: "quota"}, 0, []string{"kimi"}, routeModel, maxWait)
	if !shouldRetry {
		t.Fatalf("expected shouldRetry=true, got false (wait=%v)", wait)
	}
	if wait <= 0 {
		t.Fatalf("expected wait > 0, got %v", wait)
	}
}

func TestManager_ShouldRetryAfterError_CapsCooldownWaitToMaxRetryInterval(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(3, 30*time.Second, 0)

	model := "test-model"
	next := time.Now().Add(1 * time.Minute)

	auth := &Auth{
		ID:       "auth-1",
		Provider: "claude",
		ModelStates: map[string]*ModelState{
			model: {
				Unavailable:    true,
				Status:         StatusError,
				NextRetryAfter: next,
			},
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	_, _, maxWait := m.retrySettings()
	wait, shouldRetry := m.shouldRetryAfterError(&Error{Message: "dial tcp: connection refused"}, 0, []string{"claude"}, model, maxWait)
	if !shouldRetry {
		t.Fatalf("expected shouldRetry=true for capped cooldown wait")
	}
	if wait != maxWait {
		t.Fatalf("wait = %v, want capped maxWait %v", wait, maxWait)
	}
}

func TestManager_ShouldRetryAfterError_OpenAICompatCooldownAboveMaxWaitReturnsImmediately(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(3, 30*time.Second, 0)

	model := "test-model"
	next := time.Now().Add(1 * time.Minute)

	auth := &Auth{
		ID:       "compat-auth",
		Provider: "compat-a",
		Attributes: map[string]string{
			"api_key":      "sk-a",
			"compat_name":  "compat-a",
			"provider_key": "compat-a",
		},
		ModelStates: map[string]*ModelState{
			model: {
				Unavailable:    true,
				Status:         StatusError,
				NextRetryAfter: next,
			},
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	_, _, maxWait := m.retrySettings()
	wait, shouldRetry := m.shouldRetryAfterError(&Error{Message: "dial tcp: connection refused"}, 0, []string{"compat-a"}, model, maxWait)
	if shouldRetry {
		t.Fatalf("expected shouldRetry=false for compat cooldown above maxWait, got true (wait=%v)", wait)
	}
	if wait != 0 {
		t.Fatalf("wait = %v, want 0", wait)
	}
}

func TestResolveOpenAICompatConfig_PrefersCompositeProviderKey(t *testing.T) {
	cfg := &internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{
			{
				Name:    "shared",
				Prefix:  "deepseek",
				BaseURL: "https://deepseek.example/v1",
				Models: []internalconfig.OpenAICompatibilityModel{
					{Name: "deepseek-v4-flash"},
				},
			},
			{
				Name:    "shared",
				Prefix:  "glm",
				BaseURL: "https://glm.example/v1",
				Models: []internalconfig.OpenAICompatibilityModel{
					{Name: "glm-5.1"},
				},
			},
		},
	}

	providerKey := internalconfig.OpenAICompatibilityProviderKey("shared", "glm", "https://glm.example/v1")
	entry := resolveOpenAICompatConfig(cfg, providerKey, "shared", providerKey)
	if entry == nil {
		t.Fatal("resolveOpenAICompatConfig() returned nil")
	}
	if entry.Prefix != "glm" {
		t.Fatalf("resolveOpenAICompatConfig() prefix = %q, want %q", entry.Prefix, "glm")
	}
	if entry.BaseURL != "https://glm.example/v1" {
		t.Fatalf("resolveOpenAICompatConfig() baseURL = %q, want %q", entry.BaseURL, "https://glm.example/v1")
	}
}

func TestManager_Execute_OpenAICompatBootstrapTimeoutFallsBackToNextProvider(t *testing.T) {
	badExecutor := &compatBootstrapTimeoutExecutor{id: "compat-a", blockExecute: true}
	goodExecutor := &compatBootstrapTimeoutExecutor{id: "compat-b"}
	m := newCompatBootstrapTimeoutTestManager(t, "20ms", badExecutor, goodExecutor, nil, nil)

	resp, errExecute := m.Execute(context.Background(), []string{"compat-a", "compat-b"}, cliproxyexecutor.Request{Model: "deepseek/deepseek-v4-flash"}, cliproxyexecutor.Options{})
	if errExecute != nil {
		t.Fatalf("Execute() error = %v, want success", errExecute)
	}
	if got := string(resp.Payload); got != "bb-good-auth" {
		t.Fatalf("Execute() payload = %q, want %q", got, "bb-good-auth")
	}

	if got := badExecutor.ExecuteCalls(); len(got) != 1 || got[0] != "aa-bad-auth" {
		t.Fatalf("bad executor calls = %v, want [aa-bad-auth]", got)
	}
	if got := goodExecutor.ExecuteCalls(); len(got) != 1 || got[0] != "bb-good-auth" {
		t.Fatalf("good executor calls = %v, want [bb-good-auth]", got)
	}
}

func TestManager_ExecuteStream_OpenAICompatBootstrapTimeoutFallsBackToNextProvider(t *testing.T) {
	badExecutor := &compatBootstrapTimeoutExecutor{id: "compat-a", silentBootstrap: true}
	goodExecutor := &compatBootstrapTimeoutExecutor{id: "compat-b"}
	m := newCompatBootstrapTimeoutTestManager(t, "20ms", badExecutor, goodExecutor, nil, nil)

	streamResult, errExecute := m.ExecuteStream(context.Background(), []string{"compat-a", "compat-b"}, cliproxyexecutor.Request{Model: "deepseek/deepseek-v4-flash"}, cliproxyexecutor.Options{})
	if errExecute != nil {
		t.Fatalf("ExecuteStream() error = %v, want success", errExecute)
	}
	var payload []byte
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream chunk error: %v", chunk.Err)
		}
		payload = append(payload, chunk.Payload...)
	}
	if got := string(payload); got != "bb-good-auth" {
		t.Fatalf("stream payload = %q, want %q", got, "bb-good-auth")
	}

	if got := badExecutor.StreamCalls(); len(got) != 1 || got[0] != "aa-bad-auth" {
		t.Fatalf("bad executor stream calls = %v, want [aa-bad-auth]", got)
	}
	if got := goodExecutor.StreamCalls(); len(got) != 1 || got[0] != "bb-good-auth" {
		t.Fatalf("good executor stream calls = %v, want [bb-good-auth]", got)
	}
}

type credentialRetryLimitExecutor struct {
	id string

	mu    sync.Mutex
	calls int
}

func (e *credentialRetryLimitExecutor) Identifier() string {
	return e.id
}

func (e *credentialRetryLimitExecutor) Execute(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.recordCall()
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: 500, Message: "boom"}
}

func (e *credentialRetryLimitExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	e.recordCall()
	return nil, &Error{HTTPStatus: 500, Message: "boom"}
}

func (e *credentialRetryLimitExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *credentialRetryLimitExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.recordCall()
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: 500, Message: "boom"}
}

func (e *credentialRetryLimitExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func (e *credentialRetryLimitExecutor) recordCall() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
}

func (e *credentialRetryLimitExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type authFallbackExecutor struct {
	id string

	mu                sync.Mutex
	executeCalls      []string
	streamCalls       []string
	executeErrors     map[string]error
	streamFirstErrors map[string]error
}

func (e *authFallbackExecutor) Identifier() string {
	return e.id
}

func (e *authFallbackExecutor) Execute(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.mu.Lock()
	e.executeCalls = append(e.executeCalls, auth.ID)
	err := e.executeErrors[auth.ID]
	e.mu.Unlock()
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	return cliproxyexecutor.Response{Payload: []byte(auth.ID)}, nil
}

func (e *authFallbackExecutor) ExecuteStream(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	e.mu.Lock()
	e.streamCalls = append(e.streamCalls, auth.ID)
	err := e.streamFirstErrors[auth.ID]
	e.mu.Unlock()

	ch := make(chan cliproxyexecutor.StreamChunk, 1)
	if err != nil {
		ch <- cliproxyexecutor.StreamChunk{Err: err}
		close(ch)
		return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Auth": {auth.ID}}, Chunks: ch}, nil
	}
	ch <- cliproxyexecutor.StreamChunk{Payload: []byte(auth.ID)}
	close(ch)
	return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Auth": {auth.ID}}, Chunks: ch}, nil
}

func (e *authFallbackExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *authFallbackExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: 500, Message: "not implemented"}
}

func (e *authFallbackExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func (e *authFallbackExecutor) ExecuteCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.executeCalls))
	copy(out, e.executeCalls)
	return out
}

func (e *authFallbackExecutor) StreamCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.streamCalls))
	copy(out, e.streamCalls)
	return out
}

type compatBootstrapTimeoutExecutor struct {
	id string

	mu              sync.Mutex
	executeCalls    []string
	streamCalls     []string
	blockExecute    bool
	silentBootstrap bool
}

func (e *compatBootstrapTimeoutExecutor) Identifier() string {
	return e.id
}

func (e *compatBootstrapTimeoutExecutor) Execute(ctx context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	authID := ""
	if auth != nil {
		authID = auth.ID
	}
	e.mu.Lock()
	e.executeCalls = append(e.executeCalls, authID)
	block := e.blockExecute
	e.mu.Unlock()
	if block {
		<-ctx.Done()
		return cliproxyexecutor.Response{}, ctx.Err()
	}
	return cliproxyexecutor.Response{Payload: []byte(authID)}, nil
}

func (e *compatBootstrapTimeoutExecutor) ExecuteStream(ctx context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	authID := ""
	if auth != nil {
		authID = auth.ID
	}
	e.mu.Lock()
	e.streamCalls = append(e.streamCalls, authID)
	silent := e.silentBootstrap
	e.mu.Unlock()

	ch := make(chan cliproxyexecutor.StreamChunk, 1)
	if silent {
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Auth": {authID}}, Chunks: ch}, nil
	}
	ch <- cliproxyexecutor.StreamChunk{Payload: []byte(authID)}
	close(ch)
	return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Auth": {authID}}, Chunks: ch}, nil
}

func (e *compatBootstrapTimeoutExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *compatBootstrapTimeoutExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: 500, Message: "not implemented"}
}

func (e *compatBootstrapTimeoutExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func (e *compatBootstrapTimeoutExecutor) ExecuteCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.executeCalls))
	copy(out, e.executeCalls)
	return out
}

func (e *compatBootstrapTimeoutExecutor) StreamCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.streamCalls))
	copy(out, e.streamCalls)
	return out
}

type retryAfterStatusError struct {
	status     int
	message    string
	retryAfter time.Duration
}

func (e *retryAfterStatusError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *retryAfterStatusError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.status
}

func (e *retryAfterStatusError) RetryAfter() *time.Duration {
	if e == nil {
		return nil
	}
	d := e.retryAfter
	return &d
}

type captureStore struct {
	mu   sync.Mutex
	last *Auth
}

func (s *captureStore) List(context.Context) ([]*Auth, error) { return nil, nil }

func (s *captureStore) Save(_ context.Context, auth *Auth) (string, error) {
	if auth == nil {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = auth.Clone()
	return auth.ID, nil
}

func (s *captureStore) Delete(context.Context, string) error { return nil }

func (s *captureStore) Last() *Auth {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return nil
	}
	return s.last.Clone()
}

func newCredentialRetryLimitTestManager(t *testing.T, maxRetryCredentials int) (*Manager, *credentialRetryLimitExecutor) {
	t.Helper()

	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(0, 0, maxRetryCredentials)

	executor := &credentialRetryLimitExecutor{id: "claude"}
	m.RegisterExecutor(executor)

	baseID := uuid.NewString()
	auth1 := &Auth{ID: baseID + "-auth-1", Provider: "claude"}
	auth2 := &Auth{ID: baseID + "-auth-2", Provider: "claude"}

	// Auth selection requires that the global model registry knows each credential supports the model.
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth1.ID, "claude", []*registry.ModelInfo{{ID: "test-model"}})
	reg.RegisterClient(auth2.ID, "claude", []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		reg.UnregisterClient(auth1.ID)
		reg.UnregisterClient(auth2.ID)
	})

	if _, errRegister := m.Register(context.Background(), auth1); errRegister != nil {
		t.Fatalf("register auth1: %v", errRegister)
	}
	if _, errRegister := m.Register(context.Background(), auth2); errRegister != nil {
		t.Fatalf("register auth2: %v", errRegister)
	}

	return m, executor
}

func newCompatBootstrapTimeoutTestManager(t *testing.T, timeout string, badExec, badStream *compatBootstrapTimeoutExecutor, goodExec, goodStream *compatBootstrapTimeoutExecutor) *Manager {
	t.Helper()

	m := NewManager(nil, &FillFirstSelector{}, nil)
	m.SetConfig(&internalconfig.Config{
		Routing: internalconfig.RoutingConfig{
			Strategy:                            "fill-first",
			OpenAICompatibilityStrategy:         "fill-first",
			OpenAICompatibilityBootstrapTimeout: timeout,
		},
	})

	if badExec != nil {
		m.RegisterExecutor(badExec)
	}
	if goodExec != nil && goodExec != badExec {
		m.RegisterExecutor(goodExec)
	}
	if badStream != nil && badStream != badExec && badStream != goodExec {
		m.RegisterExecutor(badStream)
	}
	if goodStream != nil && goodStream != badExec && goodStream != goodExec && goodStream != badStream {
		m.RegisterExecutor(goodStream)
	}

	model := "deepseek/deepseek-v4-flash"
	badAuth := &Auth{
		ID:       "aa-bad-auth",
		Provider: "compat-a",
		Attributes: map[string]string{
			"api_key":      "sk-a",
			"compat_name":  "compat-a",
			"provider_key": "compat-a",
		},
	}
	goodAuth := &Auth{
		ID:       "bb-good-auth",
		Provider: "compat-b",
		Attributes: map[string]string{
			"api_key":      "sk-b",
			"compat_name":  "compat-b",
			"provider_key": "compat-b",
		},
	}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(badAuth.ID, "compat-a", []*registry.ModelInfo{{ID: model}})
	reg.RegisterClient(goodAuth.ID, "compat-b", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(badAuth.ID)
		reg.UnregisterClient(goodAuth.ID)
	})

	if _, errRegister := m.Register(context.Background(), badAuth); errRegister != nil {
		t.Fatalf("register bad auth: %v", errRegister)
	}
	if _, errRegister := m.Register(context.Background(), goodAuth); errRegister != nil {
		t.Fatalf("register good auth: %v", errRegister)
	}

	return m
}

func TestManager_MaxRetryCredentials_LimitsCrossCredentialRetries(t *testing.T) {
	request := cliproxyexecutor.Request{Model: "test-model"}
	testCases := []struct {
		name   string
		invoke func(*Manager) error
	}{
		{
			name: "execute",
			invoke: func(m *Manager) error {
				_, errExecute := m.Execute(context.Background(), []string{"claude"}, request, cliproxyexecutor.Options{})
				return errExecute
			},
		},
		{
			name: "execute_count",
			invoke: func(m *Manager) error {
				_, errExecute := m.ExecuteCount(context.Background(), []string{"claude"}, request, cliproxyexecutor.Options{})
				return errExecute
			},
		},
		{
			name: "execute_stream",
			invoke: func(m *Manager) error {
				_, errExecute := m.ExecuteStream(context.Background(), []string{"claude"}, request, cliproxyexecutor.Options{})
				return errExecute
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			limitedManager, limitedExecutor := newCredentialRetryLimitTestManager(t, 1)
			if errInvoke := tc.invoke(limitedManager); errInvoke == nil {
				t.Fatalf("expected error for limited retry execution")
			}
			if calls := limitedExecutor.Calls(); calls != 1 {
				t.Fatalf("expected 1 call with max-retry-credentials=1, got %d", calls)
			}

			unlimitedManager, unlimitedExecutor := newCredentialRetryLimitTestManager(t, 0)
			if errInvoke := tc.invoke(unlimitedManager); errInvoke == nil {
				t.Fatalf("expected error for unlimited retry execution")
			}
			if calls := unlimitedExecutor.Calls(); calls != 2 {
				t.Fatalf("expected 2 calls with max-retry-credentials=0, got %d", calls)
			}
		})
	}
}

func TestManager_ModelSupportBadRequest_FallsBackAndSuspendsAuth(t *testing.T) {
	m := NewManager(nil, nil, nil)
	executor := &authFallbackExecutor{
		id: "claude",
		executeErrors: map[string]error{
			"aa-bad-auth": &Error{
				HTTPStatus: http.StatusBadRequest,
				Message:    "invalid_request_error: The requested model is not supported.",
			},
		},
	}
	m.RegisterExecutor(executor)

	model := "claude-opus-4-6"
	badAuth := &Auth{ID: "aa-bad-auth", Provider: "claude"}
	goodAuth := &Auth{ID: "bb-good-auth", Provider: "claude"}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(badAuth.ID, "claude", []*registry.ModelInfo{{ID: model}})
	reg.RegisterClient(goodAuth.ID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(badAuth.ID)
		reg.UnregisterClient(goodAuth.ID)
	})

	if _, errRegister := m.Register(context.Background(), badAuth); errRegister != nil {
		t.Fatalf("register bad auth: %v", errRegister)
	}
	if _, errRegister := m.Register(context.Background(), goodAuth); errRegister != nil {
		t.Fatalf("register good auth: %v", errRegister)
	}

	request := cliproxyexecutor.Request{Model: model}
	for i := 0; i < 2; i++ {
		resp, errExecute := m.Execute(context.Background(), []string{"claude"}, request, cliproxyexecutor.Options{})
		if errExecute != nil {
			t.Fatalf("execute %d error = %v, want success", i, errExecute)
		}
		if string(resp.Payload) != goodAuth.ID {
			t.Fatalf("execute %d payload = %q, want %q", i, string(resp.Payload), goodAuth.ID)
		}
	}

	got := executor.ExecuteCalls()
	want := []string{badAuth.ID, goodAuth.ID, goodAuth.ID}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d auth = %q, want %q", i, got[i], want[i])
		}
	}

	updatedBad, ok := m.GetByID(badAuth.ID)
	if !ok || updatedBad == nil {
		t.Fatalf("expected bad auth to remain registered")
	}
	state := updatedBad.ModelStates[model]
	if state == nil {
		t.Fatalf("expected model state for %q", model)
	}
	if !state.Unavailable {
		t.Fatalf("expected bad auth model state to be unavailable")
	}
	if state.NextRetryAfter.IsZero() {
		t.Fatalf("expected bad auth model state cooldown to be set")
	}
}

func TestManagerExecuteStream_ModelSupportBadRequestFallsBackAndSuspendsAuth(t *testing.T) {
	m := NewManager(nil, nil, nil)
	executor := &authFallbackExecutor{
		id: "claude",
		streamFirstErrors: map[string]error{
			"aa-bad-auth": &Error{
				HTTPStatus: http.StatusBadRequest,
				Message:    "invalid_request_error: The requested model is not supported.",
			},
		},
	}
	m.RegisterExecutor(executor)

	model := "claude-opus-4-6"
	badAuth := &Auth{ID: "aa-bad-auth", Provider: "claude"}
	goodAuth := &Auth{ID: "bb-good-auth", Provider: "claude"}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(badAuth.ID, "claude", []*registry.ModelInfo{{ID: model}})
	reg.RegisterClient(goodAuth.ID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(badAuth.ID)
		reg.UnregisterClient(goodAuth.ID)
	})

	if _, errRegister := m.Register(context.Background(), badAuth); errRegister != nil {
		t.Fatalf("register bad auth: %v", errRegister)
	}
	if _, errRegister := m.Register(context.Background(), goodAuth); errRegister != nil {
		t.Fatalf("register good auth: %v", errRegister)
	}

	request := cliproxyexecutor.Request{Model: model}
	for i := 0; i < 2; i++ {
		streamResult, errExecute := m.ExecuteStream(context.Background(), []string{"claude"}, request, cliproxyexecutor.Options{})
		if errExecute != nil {
			t.Fatalf("execute stream %d error = %v, want success", i, errExecute)
		}
		var payload []byte
		for chunk := range streamResult.Chunks {
			if chunk.Err != nil {
				t.Fatalf("execute stream %d chunk error = %v, want success", i, chunk.Err)
			}
			payload = append(payload, chunk.Payload...)
		}
		if string(payload) != goodAuth.ID {
			t.Fatalf("execute stream %d payload = %q, want %q", i, string(payload), goodAuth.ID)
		}
	}

	got := executor.StreamCalls()
	want := []string{badAuth.ID, goodAuth.ID, goodAuth.ID}
	if len(got) != len(want) {
		t.Fatalf("stream calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stream call %d auth = %q, want %q", i, got[i], want[i])
		}
	}

	updatedBad, ok := m.GetByID(badAuth.ID)
	if !ok || updatedBad == nil {
		t.Fatalf("expected bad auth to remain registered")
	}
	state := updatedBad.ModelStates[model]
	if state == nil {
		t.Fatalf("expected model state for %q", model)
	}
	if !state.Unavailable {
		t.Fatalf("expected bad auth model state to be unavailable")
	}
	if state.NextRetryAfter.IsZero() {
		t.Fatalf("expected bad auth model state cooldown to be set")
	}
}

func TestManager_MarkResult_RespectsAuthDisableCoolingOverride(t *testing.T) {
	prev := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(prev) })

	m := NewManager(nil, nil, nil)

	auth := &Auth{
		ID:       "auth-1",
		Provider: "claude",
		Metadata: map[string]any{
			"disable_cooling": true,
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	model := "test-model"
	m.MarkResult(context.Background(), Result{
		AuthID:   "auth-1",
		Provider: "claude",
		Model:    model,
		Success:  false,
		Error:    &Error{HTTPStatus: 500, Message: "boom"},
	})

	updated, ok := m.GetByID("auth-1")
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	state := updated.ModelStates[model]
	if state == nil {
		t.Fatalf("expected model state to be present")
	}
	if !state.NextRetryAfter.IsZero() {
		t.Fatalf("expected NextRetryAfter to be zero when disable_cooling=true, got %v", state.NextRetryAfter)
	}
}

func TestManager_MarkResult_RespectsAuthDisableCoolingOverride_On403(t *testing.T) {
	prev := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(prev) })

	m := NewManager(nil, nil, nil)

	auth := &Auth{
		ID:       "auth-403",
		Provider: "claude",
		Metadata: map[string]any{
			"disable_cooling": true,
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	model := "test-model-403"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })

	m.MarkResult(context.Background(), Result{
		AuthID:   auth.ID,
		Provider: "claude",
		Model:    model,
		Success:  false,
		Error:    &Error{HTTPStatus: http.StatusForbidden, Message: "forbidden"},
	})

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	state := updated.ModelStates[model]
	if state == nil {
		t.Fatalf("expected model state to be present")
	}
	if !state.NextRetryAfter.IsZero() {
		t.Fatalf("expected NextRetryAfter to be zero when disable_cooling=true, got %v", state.NextRetryAfter)
	}

	if count := reg.GetModelCount(model); count <= 0 {
		t.Fatalf("expected model count > 0 when disable_cooling=true, got %d", count)
	}
}

func TestManager_Execute_DisableCooling_DoesNotBlackoutAfter403(t *testing.T) {
	prev := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(prev) })

	m := NewManager(nil, nil, nil)
	executor := &authFallbackExecutor{
		id: "claude",
		executeErrors: map[string]error{
			"auth-403-exec": &Error{
				HTTPStatus: http.StatusForbidden,
				Message:    "forbidden",
			},
		},
	}
	m.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "auth-403-exec",
		Provider: "claude",
		Metadata: map[string]any{
			"disable_cooling": true,
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	model := "test-model-403-exec"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })

	req := cliproxyexecutor.Request{Model: model}
	_, errExecute1 := m.Execute(context.Background(), []string{"claude"}, req, cliproxyexecutor.Options{})
	if errExecute1 == nil {
		t.Fatal("expected first execute error")
	}
	if statusCodeFromError(errExecute1) != http.StatusForbidden {
		t.Fatalf("first execute status = %d, want %d", statusCodeFromError(errExecute1), http.StatusForbidden)
	}

	_, errExecute2 := m.Execute(context.Background(), []string{"claude"}, req, cliproxyexecutor.Options{})
	if errExecute2 == nil {
		t.Fatal("expected second execute error")
	}
	if statusCodeFromError(errExecute2) != http.StatusForbidden {
		t.Fatalf("second execute status = %d, want %d", statusCodeFromError(errExecute2), http.StatusForbidden)
	}
}

func TestManager_Execute_DisableCooling_DoesNotBlackoutAfter429RetryAfter(t *testing.T) {
	prev := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(prev) })

	m := NewManager(nil, nil, nil)
	executor := &authFallbackExecutor{
		id: "claude",
		executeErrors: map[string]error{
			"auth-429-exec": &retryAfterStatusError{
				status:     http.StatusTooManyRequests,
				message:    "quota exhausted",
				retryAfter: 2 * time.Minute,
			},
		},
	}
	m.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "auth-429-exec",
		Provider: "claude",
		Metadata: map[string]any{
			"disable_cooling": true,
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	model := "test-model-429-exec"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })

	req := cliproxyexecutor.Request{Model: model}
	_, errExecute1 := m.Execute(context.Background(), []string{"claude"}, req, cliproxyexecutor.Options{})
	if errExecute1 == nil {
		t.Fatal("expected first execute error")
	}
	if statusCodeFromError(errExecute1) != http.StatusTooManyRequests {
		t.Fatalf("first execute status = %d, want %d", statusCodeFromError(errExecute1), http.StatusTooManyRequests)
	}

	_, errExecute2 := m.Execute(context.Background(), []string{"claude"}, req, cliproxyexecutor.Options{})
	if errExecute2 == nil {
		t.Fatal("expected second execute error")
	}
	if statusCodeFromError(errExecute2) != http.StatusTooManyRequests {
		t.Fatalf("second execute status = %d, want %d", statusCodeFromError(errExecute2), http.StatusTooManyRequests)
	}

	calls := executor.ExecuteCalls()
	if len(calls) != 2 {
		t.Fatalf("execute calls = %d, want 2", len(calls))
	}

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	state := updated.ModelStates[model]
	if state == nil {
		t.Fatalf("expected model state to be present")
	}
	if !state.NextRetryAfter.IsZero() {
		t.Fatalf("expected NextRetryAfter to be zero when disable_cooling=true, got %v", state.NextRetryAfter)
	}
}

func TestManager_Execute_DisablesCodexAuthFileOnUsageLimitReached(t *testing.T) {
	prevCheckQuota := checkCodexQuotaForFile
	t.Cleanup(func() {
		checkCodexQuotaForFile = prevCheckQuota
	})

	store := &captureStore{}
	m := NewManager(store, nil, nil)
	executor := &authFallbackExecutor{
		id: "codex",
		executeErrors: map[string]error{
			"auth-codex-usage-limit": &retryAfterStatusError{
				status:     http.StatusTooManyRequests,
				message:    `{"error":{"type":"usage_limit_reached","message":"usage limit reached","resets_in_seconds":60}}`,
				retryAfter: 60 * time.Second,
			},
		},
	}
	m.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "auth-codex-usage-limit",
		Provider: "codex",
		FileName: "codex-auth.json",
		Metadata: map[string]any{
			"type": "codex",
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	model := "test-model-codex-usage-limit"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "codex", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })

	_, errExecute := m.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if errExecute == nil {
		t.Fatal("expected execute error")
	}
	if statusCodeFromError(errExecute) != http.StatusTooManyRequests {
		t.Fatalf("execute status = %d, want %d", statusCodeFromError(errExecute), http.StatusTooManyRequests)
	}

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if !updated.Disabled {
		t.Fatalf("expected auth to be disabled")
	}
	if updated.Status != StatusDisabled {
		t.Fatalf("status = %v, want %v", updated.Status, StatusDisabled)
	}
	if got := updated.StatusMessage; got != "usage limit reached" {
		t.Fatalf("status message = %q, want %q", got, "usage limit reached")
	}
	if got, _ := updated.Metadata["disabled"].(bool); !got {
		t.Fatalf("expected metadata disabled=true, got %#v", updated.Metadata["disabled"])
	}
	if got, _ := updated.Metadata["quota_error"].(string); got == "" {
		t.Fatalf("expected quota_error to be populated")
	} else {
		var payload struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(got), &payload); err != nil {
			t.Fatalf("quota_error is not valid json: %v", err)
		}
		if payload.Error.Type != "usage_limit_reached" {
			t.Fatalf("quota_error type = %q, want %q", payload.Error.Type, "usage_limit_reached")
		}
	}
	if !updated.NextRetryAfter.IsZero() {
		t.Fatalf("expected NextRetryAfter to be zero, got %v", updated.NextRetryAfter)
	}
	if !updated.Quota.NextRecoverAt.IsZero() {
		t.Fatalf("expected NextRecoverAt to be zero, got %v", updated.Quota.NextRecoverAt)
	}

	persisted := store.Last()
	if persisted == nil {
		t.Fatalf("expected persisted auth snapshot")
	}
	if !persisted.Disabled {
		t.Fatalf("expected persisted auth to be disabled")
	}
	if got, _ := persisted.Metadata["disabled"].(bool); !got {
		t.Fatalf("expected persisted metadata disabled=true, got %#v", persisted.Metadata["disabled"])
	}
	if got := fmt.Sprintf("%v", persisted.Metadata[quotaAutoReenableReasonKey]); got != quotaAutoReenableReason {
		t.Fatalf("quota auto re-enable reason = %q, want %q", got, quotaAutoReenableReason)
	}
}

func TestManager_Execute_DisablesCodexAuthFileOnUsageLimitReachedWrappedPayload(t *testing.T) {
	store := &captureStore{}
	m := NewManager(store, nil, nil)
	executor := &authFallbackExecutor{
		id: "codex",
		executeErrors: map[string]error{
			"auth-codex-usage-limit": &retryAfterStatusError{
				status:     http.StatusTooManyRequests,
				message:    `upstream request failed: {"status":429,"body":{"error":{"type":"usage_limit_reached","message":"usage limit reached","resets_in_seconds":60}},"error":{"type":"usage_limit_reached","message":"usage limit reached","resets_in_seconds":60}}`,
				retryAfter: 60 * time.Second,
			},
		},
	}
	m.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "auth-codex-usage-limit",
		Provider: "codex",
		FileName: "codex-auth.json",
		Metadata: map[string]any{
			"type": "codex",
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	model := "test-model-codex-usage-limit"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "codex", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })

	_, errExecute := m.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if errExecute == nil {
		t.Fatal("expected execute error")
	}

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if !updated.Disabled {
		t.Fatalf("expected auth to be disabled")
	}
	if got, _ := updated.Metadata["disabled"].(bool); !got {
		t.Fatalf("expected metadata disabled=true, got %#v", updated.Metadata["disabled"])
	}
	if got := fmt.Sprintf("%v", updated.Metadata[quotaAutoReenableReasonKey]); got != quotaAutoReenableReason {
		t.Fatalf("quota auto re-enable reason = %q, want %q", got, quotaAutoReenableReason)
	}
	if persisted := store.Last(); persisted == nil || !persisted.Disabled {
		t.Fatalf("expected persisted disabled auth, got %#v", persisted)
	}
}

func TestManager_Execute_DisablesCodexAuthFileOnUsageLimitReachedFromBodyErrorType(t *testing.T) {
	store := &captureStore{}
	m := NewManager(store, nil, nil)
	executor := &authFallbackExecutor{
		id: "codex",
		executeErrors: map[string]error{
			"auth-codex-usage-limit": &retryAfterStatusError{
				status:     http.StatusTooManyRequests,
				message:    `{"status":429,"body":{"error":{"type":"usage_limit_reached","message":"usage limit reached","resets_in_seconds":45}}}`,
				retryAfter: 45 * time.Second,
			},
		},
	}
	m.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "auth-codex-usage-limit",
		Provider: "codex",
		FileName: "codex-auth.json",
		Metadata: map[string]any{
			"type": "codex",
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	model := "test-model-codex-usage-limit"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "codex", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })

	_, errExecute := m.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if errExecute == nil {
		t.Fatal("expected execute error")
	}

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if !updated.Disabled {
		t.Fatalf("expected auth to be disabled")
	}
	if got := fmt.Sprintf("%v", updated.Metadata[quotaAutoReenableReasonKey]); got != quotaAutoReenableReason {
		t.Fatalf("quota auto re-enable reason = %q, want %q", got, quotaAutoReenableReason)
	}
	if persisted := store.Last(); persisted == nil || !persisted.Disabled {
		t.Fatalf("expected persisted disabled auth, got %#v", persisted)
	}
}

func TestManager_RefreshCodexUsageLimitedAuth_AutoEnablesWhenQuotaRecovers(t *testing.T) {
	prevCheckQuota := checkCodexQuotaForFile
	t.Cleanup(func() {
		checkCodexQuotaForFile = prevCheckQuota
	})

	checkCodexQuotaForFile = func(authDir, name string, refreshNow bool, cfg *internalconfig.Config, proxyURL string) internalcodex.QuotaCheckResult {
		if authDir != "/tmp/auths" {
			t.Fatalf("authDir = %q, want %q", authDir, "/tmp/auths")
		}
		if name != "codex-auth.json" {
			t.Fatalf("name = %q, want %q", name, "codex-auth.json")
		}
		if !refreshNow {
			t.Fatal("expected refreshNow=true")
		}
		return internalcodex.QuotaCheckResult{
			Name:              name,
			Status:            "success",
			PlanType:          "plus",
			QuotaCheckedAt:    time.Now().Format(time.RFC3339),
			AutoEnableApplied: true,
			Windows: []internalcodex.QuotaWindow{{
				ID:         "weekly",
				ResetAtISO: time.Now().Add(24 * time.Hour).Format(time.RFC3339),
			}},
		}
	}

	store := &captureStore{}
	m := NewManager(store, nil, nil)
	m.SetConfig(&internalconfig.Config{AuthDir: "/tmp/auths"})
	executor := &authFallbackExecutor{id: "codex"}
	m.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "auth-codex-auto-reenable",
		Provider: "codex",
		Disabled: true,
		Status:   StatusDisabled,
		FileName: "codex-auth.json",
		Metadata: map[string]any{
			"type":                     "codex",
			"disabled":                 true,
			quotaAutoReenableReasonKey: quotaAutoReenableReason,
			quotaAutoReenableAtKey:     time.Now().Add(-time.Minute).Format(time.RFC3339),
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	m.refreshCodexUsageLimitedAuth(context.Background(), auth.ID, auth.Clone(), &internalconfig.Config{AuthDir: "/tmp/auths"})

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if updated.Disabled {
		t.Fatalf("expected auth to be enabled")
	}
	if updated.Status != StatusActive {
		t.Fatalf("status = %v, want %v", updated.Status, StatusActive)
	}
	if got, _ := updated.Metadata["disabled"].(bool); got {
		t.Fatalf("expected metadata disabled=false, got %#v", updated.Metadata["disabled"])
	}
	if _, ok := updated.Metadata[quotaAutoReenableReasonKey]; ok {
		t.Fatalf("expected quota auto re-enable reason to be cleared")
	}
	if _, ok := updated.Metadata[quotaAutoReenableAtKey]; ok {
		t.Fatalf("expected quota auto re-enable at to be cleared")
	}

	persisted := store.Last()
	if persisted == nil {
		t.Fatalf("expected persisted auth snapshot")
	}
	if persisted.Disabled {
		t.Fatalf("expected persisted auth to be enabled")
	}
	if got, _ := persisted.Metadata["disabled"].(bool); got {
		t.Fatalf("expected persisted metadata disabled=false, got %#v", persisted.Metadata["disabled"])
	}
}

func TestManager_Execute_DisableCooling_RetriesAfter429RetryAfter(t *testing.T) {
	prev := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(prev) })

	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(3, 100*time.Millisecond, 0)

	executor := &authFallbackExecutor{
		id: "claude",
		executeErrors: map[string]error{
			"auth-429-retryafter-exec": &retryAfterStatusError{
				status:     http.StatusTooManyRequests,
				message:    "quota exhausted",
				retryAfter: 5 * time.Millisecond,
			},
		},
	}
	m.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "auth-429-retryafter-exec",
		Provider: "claude",
		Metadata: map[string]any{
			"disable_cooling": true,
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	model := "test-model-429-retryafter-exec"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })

	req := cliproxyexecutor.Request{Model: model}
	_, errExecute := m.Execute(context.Background(), []string{"claude"}, req, cliproxyexecutor.Options{})
	if errExecute == nil {
		t.Fatal("expected execute error")
	}
	if statusCodeFromError(errExecute) != http.StatusTooManyRequests {
		t.Fatalf("execute status = %d, want %d", statusCodeFromError(errExecute), http.StatusTooManyRequests)
	}

	calls := executor.ExecuteCalls()
	if len(calls) != 4 {
		t.Fatalf("execute calls = %d, want 4 (initial + 3 retries)", len(calls))
	}
}

func TestManager_MarkResult_RequestScopedNotFoundDoesNotCooldownAuth(t *testing.T) {
	m := NewManager(nil, nil, nil)

	auth := &Auth{
		ID:       "auth-1",
		Provider: "openai",
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	model := "gpt-4.1"
	m.MarkResult(context.Background(), Result{
		AuthID:   auth.ID,
		Provider: auth.Provider,
		Model:    model,
		Success:  false,
		Error: &Error{
			HTTPStatus: http.StatusNotFound,
			Message:    requestScopedNotFoundMessage,
		},
	})

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if updated.Unavailable {
		t.Fatalf("expected request-scoped 404 to keep auth available")
	}
	if !updated.NextRetryAfter.IsZero() {
		t.Fatalf("expected request-scoped 404 to keep auth cooldown unset, got %v", updated.NextRetryAfter)
	}
	if state := updated.ModelStates[model]; state != nil {
		t.Fatalf("expected request-scoped 404 to avoid model cooldown state, got %#v", state)
	}
}

func TestManager_RequestScopedNotFoundStopsRetryWithoutSuspendingAuth(t *testing.T) {
	m := NewManager(nil, nil, nil)
	executor := &authFallbackExecutor{
		id: "openai",
		executeErrors: map[string]error{
			"aa-bad-auth": &Error{
				HTTPStatus: http.StatusNotFound,
				Message:    requestScopedNotFoundMessage,
			},
		},
	}
	m.RegisterExecutor(executor)

	model := "gpt-4.1"
	badAuth := &Auth{ID: "aa-bad-auth", Provider: "openai"}
	goodAuth := &Auth{ID: "bb-good-auth", Provider: "openai"}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(badAuth.ID, "openai", []*registry.ModelInfo{{ID: model}})
	reg.RegisterClient(goodAuth.ID, "openai", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(badAuth.ID)
		reg.UnregisterClient(goodAuth.ID)
	})

	if _, errRegister := m.Register(context.Background(), badAuth); errRegister != nil {
		t.Fatalf("register bad auth: %v", errRegister)
	}
	if _, errRegister := m.Register(context.Background(), goodAuth); errRegister != nil {
		t.Fatalf("register good auth: %v", errRegister)
	}

	_, errExecute := m.Execute(context.Background(), []string{"openai"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if errExecute == nil {
		t.Fatal("expected request-scoped not-found error")
	}
	errResult, ok := errExecute.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", errExecute)
	}
	if errResult.HTTPStatus != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", errResult.HTTPStatus, http.StatusNotFound)
	}
	if errResult.Message != requestScopedNotFoundMessage {
		t.Fatalf("message = %q, want %q", errResult.Message, requestScopedNotFoundMessage)
	}

	got := executor.ExecuteCalls()
	want := []string{badAuth.ID}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d auth = %q, want %q", i, got[i], want[i])
		}
	}

	updatedBad, ok := m.GetByID(badAuth.ID)
	if !ok || updatedBad == nil {
		t.Fatalf("expected bad auth to remain registered")
	}
	if updatedBad.Unavailable {
		t.Fatalf("expected request-scoped 404 to keep bad auth available")
	}
	if !updatedBad.NextRetryAfter.IsZero() {
		t.Fatalf("expected request-scoped 404 to keep bad auth cooldown unset, got %v", updatedBad.NextRetryAfter)
	}
	if state := updatedBad.ModelStates[model]; state != nil {
		t.Fatalf("expected request-scoped 404 to avoid bad auth model cooldown state, got %#v", state)
	}
}

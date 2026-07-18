package handlers

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

type protocolCaptureExecutor struct {
	mu   sync.Mutex
	req  coreexecutor.Request
	opts coreexecutor.Options
}

func (e *protocolCaptureExecutor) Identifier() string { return "gemini-interactions" }

func (e *protocolCaptureExecutor) Execute(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.mu.Lock()
	e.req = req
	e.opts = opts
	e.mu.Unlock()
	return coreexecutor.Response{Payload: []byte(`{"id":"interaction_1"}`)}, nil
}

func (e *protocolCaptureExecutor) ExecuteStream(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.mu.Lock()
	e.req = req
	e.opts = opts
	e.mu.Unlock()
	chunks := make(chan coreexecutor.StreamChunk, 1)
	chunks <- coreexecutor.StreamChunk{Payload: []byte(`{"event_type":"interaction.completed"}`)}
	close(chunks)
	return &coreexecutor.StreamResult{Chunks: chunks}, nil
}

func (e *protocolCaptureExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, nil
}

func (e *protocolCaptureExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *protocolCaptureExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func (e *protocolCaptureExecutor) captured() (coreexecutor.Request, coreexecutor.Options) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.req, e.opts
}

func TestExecuteProtocolAgentUsesSelectionModelButPreservesExecutionTarget(t *testing.T) {
	const selectionModel = "gemini-2.5-flash"
	const agent = "agents/test-agent"
	exec := &protocolCaptureExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(exec)
	auth := &coreauth.Auth{ID: "interactions-agent-auth", Provider: exec.Identifier(), Status: coreauth.StatusActive}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: selectionModel}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	manager.RefreshSchedulerEntry(auth.ID)
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)

	resp, errMsg := handler.ExecuteProtocolWithAuthManager(context.Background(), ProtocolExecutionRequest{
		EntryProtocol:      "interactions",
		ExitProtocol:       "interactions",
		ForcedProvider:     exec.Identifier(),
		AuthSelectionModel: selectionModel,
		Model:              agent,
		Body:               []byte(`{"agent":"agents/test-agent","input":"hi"}`),
	})
	if errMsg != nil {
		t.Fatalf("ExecuteProtocolWithAuthManager() error = %+v", errMsg)
	}
	if string(resp.Body) != `{"id":"interaction_1"}` {
		t.Fatalf("response body = %s", resp.Body)
	}
	gotReq, gotOpts := exec.captured()
	if gotReq.Model != agent {
		t.Fatalf("executor model = %q, want %q", gotReq.Model, agent)
	}
	if gotOpts.SourceFormat != sdktranslator.FormatInteractions {
		t.Fatalf("source format = %q, want interactions", gotOpts.SourceFormat)
	}
	if gotOpts.Metadata[coreexecutor.AuthSelectionModelMetadataKey] != selectionModel {
		t.Fatalf("auth selection metadata = %#v", gotOpts.Metadata[coreexecutor.AuthSelectionModelMetadataKey])
	}
	if gotOpts.Metadata[coreexecutor.RequestedModelMetadataKey] != agent {
		t.Fatalf("requested model metadata = %#v, want %q", gotOpts.Metadata[coreexecutor.RequestedModelMetadataKey], agent)
	}
}

func TestExecuteProtocolStreamAgentUsesSelectionModelButPreservesExecutionTarget(t *testing.T) {
	const selectionModel = "gemini-2.5-flash"
	const agent = "agents/test-agent"
	exec := &protocolCaptureExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(exec)
	auth := &coreauth.Auth{ID: "interactions-agent-stream-auth", Provider: exec.Identifier(), Status: coreauth.StatusActive}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: selectionModel}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	manager.RefreshSchedulerEntry(auth.ID)
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)

	stream, errMsg := handler.ExecuteProtocolStreamWithAuthManager(context.Background(), ProtocolExecutionRequest{
		EntryProtocol:      "interactions",
		ExitProtocol:       "interactions",
		ForcedProvider:     exec.Identifier(),
		AuthSelectionModel: selectionModel,
		Model:              agent,
		Stream:             true,
		Body:               []byte(`{"agent":"agents/test-agent","input":"hi","stream":true}`),
	})
	if errMsg != nil {
		t.Fatalf("ExecuteProtocolStreamWithAuthManager() error = %+v", errMsg)
	}
	chunk, ok := <-stream.Chunks
	if !ok {
		t.Fatal("stream closed before payload")
	}
	if string(chunk) != `{"event_type":"interaction.completed"}` {
		t.Fatalf("stream payload = %s", chunk)
	}
	if errStream, okErr := <-stream.Errors; okErr && errStream != nil {
		t.Fatalf("stream error = %+v", errStream)
	}
	gotReq, gotOpts := exec.captured()
	if gotReq.Model != agent {
		t.Fatalf("executor model = %q, want %q", gotReq.Model, agent)
	}
	if !gotOpts.Stream || gotOpts.SourceFormat != sdktranslator.FormatInteractions {
		t.Fatalf("stream options = %+v", gotOpts)
	}
	if gotOpts.Metadata[coreexecutor.AuthSelectionModelMetadataKey] != selectionModel {
		t.Fatalf("auth selection metadata = %#v", gotOpts.Metadata[coreexecutor.AuthSelectionModelMetadataKey])
	}
	if gotOpts.Metadata[coreexecutor.RequestedModelMetadataKey] != agent {
		t.Fatalf("requested model metadata = %#v, want %q", gotOpts.Metadata[coreexecutor.RequestedModelMetadataKey], agent)
	}
}

func TestAdjustExecutionProvidersForInteractions(t *testing.T) {
	providers := adjustExecutionProvidersForEntryProtocol("interactions", []string{"gemini", "gemini-interactions", "claude"})
	if len(providers) != 3 || providers[0] != "gemini-interactions" {
		t.Fatalf("providers = %#v, want native Interactions first", providers)
	}
	providers = adjustExecutionProvidersForEntryProtocol("codex", []string{"gemini-interactions", "codex"})
	if len(providers) != 1 || providers[0] != "codex" {
		t.Fatalf("providers = %#v, want Interactions provider excluded", providers)
	}
}

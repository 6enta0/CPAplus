package management

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/watcher/synthesizer"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestPatchXAIKeyUpdatesV6ExecutionFields(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{XAIKey: []config.XAIKey{{
			APIKey: "xai-key", Priority: 1, BaseURL: "https://api.x.ai/v1", Websockets: true,
		}}},
		configFilePath: writeTestConfigFile(t),
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/xai-api-key", strings.NewReader(`{
		"index":0,"value":{"name":" Team xAI ","priority":7,"websockets":false,"prefix":"team"}
	}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.PatchXAIKey(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	entry := handler.cfg.XAIKey[0]
	if entry.Name != "Team xAI" || entry.Priority != 7 || entry.Websockets || entry.Prefix != "team" {
		t.Fatalf("updated entry = %#v", entry)
	}
}

func TestDeleteXAIKeyRequiresBaseURLForDuplicateKey(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{XAIKey: []config.XAIKey{
			{APIKey: "shared", BaseURL: "https://a.example/v1"},
			{APIKey: "shared", BaseURL: "https://b.example/v1"},
		}},
		configFilePath: writeTestConfigFile(t),
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/v0/management/xai-api-key?api-key=shared", nil)
	handler.DeleteXAIKey(ctx)
	if recorder.Code != http.StatusBadRequest || len(handler.cfg.XAIKey) != 2 {
		t.Fatalf("status/keys = %d/%d, body = %s", recorder.Code, len(handler.cfg.XAIKey), recorder.Body.String())
	}
}

func TestDeleteXAIKeyWithBaseURLRemovesOnlyMatchingEntry(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{XAIKey: []config.XAIKey{
			{APIKey: "shared", BaseURL: "https://a.example/v1"},
			{APIKey: "shared", BaseURL: "https://b.example/v1"},
		}},
		configFilePath: writeTestConfigFile(t),
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/v0/management/xai-api-key?api-key=shared&base-url=https%3A%2F%2Fa.example%2Fv1", nil)
	handler.DeleteXAIKey(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(handler.cfg.XAIKey) != 1 || handler.cfg.XAIKey[0].BaseURL != "https://b.example/v1" {
		t.Fatalf("remaining keys = %#v", handler.cfg.XAIKey)
	}
}

func TestXAIKeysWithAuthIndexUsesSynthesizedIdentity(t *testing.T) {
	cfg := &config.Config{XAIKey: []config.XAIKey{{APIKey: "xai-key", BaseURL: "https://api.x.ai/v1"}}}
	auths, errSynthesize := synthesizer.NewConfigSynthesizer().Synthesize(&synthesizer.SynthesisContext{
		Config: cfg, Now: time.Now(), IDGenerator: synthesizer.NewStableIDGenerator(),
	})
	if errSynthesize != nil || len(auths) != 1 {
		t.Fatalf("Synthesize() error/auths = %v/%d", errSynthesize, len(auths))
	}
	manager := coreauth.NewManager(nil, nil, nil)
	if _, errRegister := manager.Register(context.Background(), auths[0]); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	handler := &Handler{cfg: cfg, authManager: manager}
	items := handler.xaiKeysWithAuthIndex()
	if len(items) != 1 || items[0].AuthIndex == "" {
		t.Fatalf("xAI management items = %#v", items)
	}
}

func TestPatchAuthFileStatusPersistsXAIAPIKeyDisable(t *testing.T) {
	cfg := &config.Config{XAIKey: []config.XAIKey{{
		APIKey: "xai-key", BaseURL: "https://api.x.ai/v1", ExcludedModels: []string{"grok-old"},
	}}}
	auths, errSynthesize := synthesizer.NewConfigSynthesizer().Synthesize(&synthesizer.SynthesisContext{
		Config: cfg, Now: time.Now(), IDGenerator: synthesizer.NewStableIDGenerator(),
	})
	if errSynthesize != nil || len(auths) != 1 {
		t.Fatalf("Synthesize() error/auths = %v/%d", errSynthesize, len(auths))
	}
	manager := coreauth.NewManager(nil, nil, nil)
	if _, errRegister := manager.Register(context.Background(), auths[0]); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	handler := &Handler{cfg: cfg, authManager: manager, configFilePath: writeTestConfigFile(t)}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/status", strings.NewReader(`{
		"name":"`+auths[0].ID+`","disabled":true
	}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.PatchAuthFileStatus(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := strings.Join(cfg.XAIKey[0].ExcludedModels, ","); got != "grok-old,*" {
		t.Fatalf("excluded models = %q, want grok-old,*", got)
	}

	enableRecorder := httptest.NewRecorder()
	enableContext, _ := gin.CreateTestContext(enableRecorder)
	enableContext.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/status", strings.NewReader(`{
		"name":"`+auths[0].ID+`","disabled":false
	}`))
	enableContext.Request.Header.Set("Content-Type", "application/json")
	handler.PatchAuthFileStatus(enableContext)
	if enableRecorder.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body = %s", enableRecorder.Code, enableRecorder.Body.String())
	}
	if got := strings.Join(cfg.XAIKey[0].ExcludedModels, ","); got != "grok-old" {
		t.Fatalf("enabled excluded models = %q, want grok-old", got)
	}
}

func TestNormalizeOAuthProviderAcceptsXAIAliases(t *testing.T) {
	for _, input := range []string{"xai", "x-ai", "x.ai", "grok"} {
		provider, errNormalize := NormalizeOAuthProvider(input)
		if errNormalize != nil || provider != "xai" {
			t.Fatalf("NormalizeOAuthProvider(%q) = %q, %v", input, provider, errNormalize)
		}
	}
}

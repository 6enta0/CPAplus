package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestListAuthFiles_LoadsUsageMapsOncePerRequest(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	store, err := usage.NewSQLiteStore(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := coreauth.NewManager(nil, nil, nil)
	for _, id := range []string{"auth-a", "auth-b", "auth-c"} {
		record := &coreauth.Auth{
			ID:       id,
			Provider: "codex",
			Attributes: map[string]string{
				"runtime_only": "true",
			},
			Metadata: map[string]any{"type": "codex"},
		}
		registered, errRegister := manager.Register(context.Background(), record)
		if errRegister != nil {
			t.Fatalf("register %s: %v", id, errRegister)
		}
		authIndex := id
		if registered != nil {
			registered.EnsureIndex()
			if registered.Index != "" {
				authIndex = registered.Index
			}
		}
		store.InsertRecord(coreusage.Record{
			AuthIndex: authIndex,
			Model:     "gpt-test",
			Detail: coreusage.Detail{
				TotalTokens: 100,
			},
		})
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	h.tokenStore = &memoryAuthStore{}
	h.SetUsageStore(store)

	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)
	h.ListAuthFiles(ginCtx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(payload.Files))
	}
	for _, file := range payload.Files {
		if _, ok := file["last_used_at"]; !ok {
			t.Fatalf("expected last_used_at on file %#v", file)
		}
	}
}

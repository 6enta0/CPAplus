package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestListAuthFilesFromDisk_IncludesLastRefreshFromJSON(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-user.json"
	filePath := filepath.Join(authDir, fileName)
	content := `{"type":"codex","email":"user@example.com","last_refresh":"2026-05-24T08:15:30Z"}`
	if errWrite := os.WriteFile(filePath, []byte(content), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)

	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected list status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("failed to decode list payload: %v", errUnmarshal)
	}

	filesRaw, ok := payload["files"].([]any)
	if !ok || len(filesRaw) != 1 {
		t.Fatalf("expected one file entry, got %#v", payload["files"])
	}

	entry, ok := filesRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("expected file entry object, got %#v", filesRaw[0])
	}

	got, _ := entry["last_refresh"].(string)
	if got != "2026-05-24T08:15:30Z" {
		t.Fatalf("last_refresh = %q, want %q", got, "2026-05-24T08:15:30Z")
	}
}

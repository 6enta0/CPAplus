package management

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestPutAndPatchInteractionsKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}

	putRecorder := httptest.NewRecorder()
	putContext, _ := gin.CreateTestContext(putRecorder)
	putContext.Request = httptest.NewRequest(http.MethodPut, "/v0/management/interactions-api-key", strings.NewReader(`[{"api-key":" key ","prefix":" native ","base-url":"https://example.com/ ","headers":{" Api-Revision ":" 2026-06-01 "},"excluded-models":[" gemini-2.5-* "]}]`))
	putContext.Request.Header.Set("Content-Type", "application/json")

	h.PutInteractionsKeys(putContext)

	if putRecorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", putRecorder.Code, putRecorder.Body.String())
	}
	if len(h.cfg.InteractionsKey) != 1 {
		t.Fatalf("Interactions key count = %d, want 1", len(h.cfg.InteractionsKey))
	}
	entry := h.cfg.InteractionsKey[0]
	if entry.APIKey != "key" || entry.Prefix != "native" || entry.BaseURL != "https://example.com/" {
		t.Fatalf("sanitized entry = %+v", entry)
	}
	if entry.Headers["Api-Revision"] != "2026-06-01" {
		t.Fatalf("headers = %#v", entry.Headers)
	}

	patchRecorder := httptest.NewRecorder()
	patchContext, _ := gin.CreateTestContext(patchRecorder)
	patchContext.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/interactions-api-key", strings.NewReader(`{"index":0,"value":{"prefix":"team","proxy-url":"http://proxy.example:8080","excluded-models":["gemini-3-*"]}}`))
	patchContext.Request.Header.Set("Content-Type", "application/json")

	h.PatchInteractionsKey(patchContext)

	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", patchRecorder.Code, patchRecorder.Body.String())
	}
	entry = h.cfg.InteractionsKey[0]
	if entry.Prefix != "team" || entry.ProxyURL != "http://proxy.example:8080" {
		t.Fatalf("patched entry = %+v", entry)
	}
	if len(entry.ExcludedModels) != 1 || entry.ExcludedModels[0] != "gemini-3-*" {
		t.Fatalf("excluded models = %#v", entry.ExcludedModels)
	}
}

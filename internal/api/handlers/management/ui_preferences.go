// Package management — UI preferences endpoint.
//
// Stores opaque frontend display preferences (view modes, etc.) in a sibling
// JSON file next to the main config. Decoupled from config.yaml so writes do
// not trigger the config watcher / client reload path.
package management

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/gin-gonic/gin"
)

var uiPreferencesMu sync.Mutex

func (h *Handler) uiPreferencesPath() string {
	dir := filepath.Dir(h.configFilePath)
	return filepath.Join(dir, "ui-preferences.json")
}

// GetUIPreferences returns the stored UI preferences blob, or {} when absent.
func (h *Handler) GetUIPreferences(c *gin.Context) {
	uiPreferencesMu.Lock()
	defer uiPreferencesMu.Unlock()

	data, err := os.ReadFile(h.uiPreferencesPath())
	if err != nil {
		if os.IsNotExist(err) {
			c.Data(http.StatusOK, "application/json", []byte("{}"))
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read ui preferences"})
		return
	}
	if len(data) == 0 {
		c.Data(http.StatusOK, "application/json", []byte("{}"))
		return
	}
	c.Data(http.StatusOK, "application/json", data)
}

// PutUIPreferences overwrites the preferences blob with the request body.
// Body must be a JSON object; the backend treats contents as opaque.
func (h *Handler) PutUIPreferences(c *gin.Context) {
	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	uiPreferencesMu.Lock()
	defer uiPreferencesMu.Unlock()

	path := h.uiPreferencesPath()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ui-preferences-*.json")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create temp file"})
		return
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			if errRemove := os.Remove(tmpName); errRemove != nil && !os.IsNotExist(errRemove) {
				// best-effort cleanup; ignore
				_ = errRemove
			}
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		if errClose := tmp.Close(); errClose != nil {
			_ = errClose
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write temp file"})
		return
	}
	if err = tmp.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to close temp file"})
		return
	}
	if err = os.Rename(tmpName, path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist ui preferences"})
		return
	}
	cleanup = false
	c.Data(http.StatusOK, "application/json", data)
}

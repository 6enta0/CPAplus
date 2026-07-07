package management

import (
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	codexquota "github.com/router-for-me/CLIProxyAPI/v6/internal/codex"
)

type quotaCheckRequest struct {
	Names      []string `json:"names"`
	RefreshNow bool     `json:"refresh_now"`
}

type quotaResetRequest struct {
	Name string `json:"name"`
}

type refreshTokenRequest struct {
	Names []string `json:"names"`
}

func (h *Handler) PostQuotaCheck(c *gin.Context) {
	var req quotaCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid request body"})
		return
	}
	if len(req.Names) == 0 {
		c.JSON(400, gin.H{"error": "names is required"})
		return
	}

	authDir := h.cfg.AuthDir
	proxyURL := h.cfg.ProxyURL

	type indexedResult struct {
		Index  int                         `json:"-"`
		Result codexquota.QuotaCheckResult `json:"-"`
	}

	results := make([]indexedResult, len(req.Names))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, name := range req.Names {
		wg.Add(1)
		go func(idx int, fileName string) {
			defer wg.Done()
			r := codexquota.CheckQuotaForFile(authDir, fileName, req.RefreshNow, h.cfg, proxyURL)
			mu.Lock()
			results[idx] = indexedResult{Index: idx, Result: r}
			mu.Unlock()
		}(i, name)
	}
	wg.Wait()

	response := make([]codexquota.QuotaCheckResult, len(req.Names))
	for i, r := range results {
		response[i] = r.Result
	}

	c.JSON(200, gin.H{"results": response})
}

// PostQuotaReset consumes one rate-limit reset credit for a single auth file
// and returns the refreshed quota snapshot.
func (h *Handler) PostQuotaReset(c *gin.Context) {
	var req quotaResetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid request body"})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		c.JSON(400, gin.H{"error": "name is required"})
		return
	}

	result := codexquota.ResetRateLimitCreditForFile(h.cfg.AuthDir, req.Name, h.cfg, h.cfg.ProxyURL)

	c.JSON(200, gin.H{"result": result})
}

func (h *Handler) PostRefreshToken(c *gin.Context) {
	var req refreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid request body"})
		return
	}
	if len(req.Names) == 0 {
		c.JSON(400, gin.H{"error": "names is required"})
		return
	}

	type refreshResult struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		Error     string `json:"error,omitempty"`
		Refreshed bool   `json:"refreshed"`
	}

	authDir := h.cfg.AuthDir
	proxyURL := h.cfg.ProxyURL

	results := make([]refreshResult, len(req.Names))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, name := range req.Names {
		wg.Add(1)
		go func(idx int, fileName string) {
			defer wg.Done()
			r := codexquota.CheckQuotaForFile(authDir, fileName, true, h.cfg, proxyURL)
			mu.Lock()
			results[idx] = refreshResult{
				Name:      r.Name,
				Status:    r.Status,
				Error:     r.Error,
				Refreshed: r.TokenRefreshed,
			}
			mu.Unlock()
		}(i, name)
	}
	wg.Wait()

	c.JSON(200, gin.H{"results": results})
}

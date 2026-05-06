package management

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) GetPricing(c *gin.Context) {
	ps := h.usageStats.GetPricingStore()
	if ps == nil {
		c.JSON(http.StatusOK, gin.H{"prices": nil, "count": 0})
		return
	}
	prices := ps.FrontendPrices()
	custom := ps.CustomPricesFrontend()
	c.JSON(http.StatusOK, gin.H{"prices": prices, "custom_prices": custom, "count": len(prices)})
}

func (h *Handler) SyncPricing(c *gin.Context) {
	ps := h.usageStats.GetPricingStore()
	if ps == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "pricing store not initialized"})
		return
	}
	if err := ps.Sync(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	prices := ps.FrontendPrices()
	c.JSON(http.StatusOK, gin.H{"prices": prices, "count": len(prices)})
}

type customPriceEntry struct {
	Prompt     float64 `json:"prompt"`
	Completion float64 `json:"completion"`
	Cache      float64 `json:"cache"`
}

func (h *Handler) PutCustomPricing(c *gin.Context) {
	ps := h.usageStats.GetPricingStore()
	if ps == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "pricing store not initialized"})
		return
	}

	var body map[string]customPriceEntry
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	customPrices := make(map[string][4]float64, len(body))
	for model, p := range body {
		customPrices[model] = [4]float64{
			p.Prompt / 1e6,
			p.Completion / 1e6,
			p.Cache / 1e6,
			0,
		}
	}

	ps.SetCustomPrices(customPrices)
	custom := ps.CustomPricesFrontend()
	c.JSON(http.StatusOK, gin.H{"custom_prices": custom, "count": len(custom)})
}

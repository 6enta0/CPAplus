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
	c.JSON(http.StatusOK, gin.H{"prices": prices, "count": len(prices)})
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

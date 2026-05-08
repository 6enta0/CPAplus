package pricing

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const pricingURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

type ModelPricing struct {
	InputCostPerToken           *float64 `json:"input_cost_per_token"`
	OutputCostPerToken          *float64 `json:"output_cost_per_token"`
	CacheReadInputTokenCost     *float64 `json:"cache_read_input_token_cost"`
	CacheCreationInputTokenCost *float64 `json:"cache_creation_input_token_cost"`
}

type Store struct {
	mu           sync.RWMutex
	prices       map[string][4]float64
	customPrices map[string][4]float64
}

func NewStore() *Store {
	s := &Store{
		prices:       make(map[string][4]float64),
		customPrices: make(map[string][4]float64),
	}
	s.initCustomPrices()
	return s
}

func (s *Store) initCustomPrices() {
	s.customPrices = map[string][4]float64{
		"mimo-v2.5-pro": {1.0 / 1e6, 3.0 / 1e6, 0.2 / 1e6, 0},
		"mimo-v2-pro":   {1.0 / 1e6, 3.0 / 1e6, 0.2 / 1e6, 0},
		"mimo-v2.5":     {0.4 / 1e6, 2.0 / 1e6, 0.08 / 1e6, 0},
		"mimo-v2-omni":  {0.4 / 1e6, 2.0 / 1e6, 0.08 / 1e6, 0},
		"mimo-v2-flash": {0.1 / 1e6, 0.3 / 1e6, 0.01 / 1e6, 0},
		"glm-5.1":       {1.4 / 1e6, 4.4 / 1e6, 0.26 / 1e6, 0},
		"glm-5":         {1.0 / 1e6, 3.2 / 1e6, 0.2 / 1e6, 0},
		"glm-5-turbo":   {1.2 / 1e6, 4.0 / 1e6, 0.24 / 1e6, 0},
	}
}

func (s *Store) Sync() error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(pricingURL)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	var data map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}

	newPrices := make(map[string][4]float64, len(data))
	count := 0
	for model, raw := range data {
		var p ModelPricing
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		if p.InputCostPerToken == nil || p.OutputCostPerToken == nil {
			continue
		}
		var cacheRead, cacheCreate float64
		if p.CacheReadInputTokenCost != nil {
			cacheRead = *p.CacheReadInputTokenCost
		}
		if p.CacheCreationInputTokenCost != nil {
			cacheCreate = *p.CacheCreationInputTokenCost
		}
		newPrices[model] = [4]float64{*p.InputCostPerToken, *p.OutputCostPerToken, cacheRead, cacheCreate}
		count++
	}

	s.mu.Lock()
	s.prices = newPrices
	s.mu.Unlock()

	log.Infof("pricing: synced %d models from litellm", count)
	return nil
}

func (s *Store) Lookup(model string) ([4]float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.customPrices[model]; ok {
		return p, true
	}
	if p, ok := s.customPrices[strings.ToLower(model)]; ok {
		return p, true
	}
	return matchPricing(model, s.prices)
}

func (s *Store) All() map[string][4]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string][4]float64, len(s.prices))
	for k, v := range s.prices {
		cp[k] = v
	}
	return cp
}

func (s *Store) FrontendPrices() map[string]struct {
	Prompt     float64 `json:"prompt"`
	Completion float64 `json:"completion"`
	Cache      float64 `json:"cache"`
} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	merged := make(map[string][4]float64, len(s.customPrices)+len(s.prices))
	for k, v := range s.prices {
		merged[k] = v
	}
	for k, v := range s.customPrices {
		merged[k] = v
	}
	result := make(map[string]struct {
		Prompt     float64 `json:"prompt"`
		Completion float64 `json:"completion"`
		Cache      float64 `json:"cache"`
	}, len(merged))
	for model, p := range merged {
		result[model] = struct {
			Prompt     float64 `json:"prompt"`
			Completion float64 `json:"completion"`
			Cache      float64 `json:"cache"`
		}{
			Prompt:     p[0] * 1e6,
			Completion: p[1] * 1e6,
			Cache:      p[2] * 1e6,
		}
	}
	return result
}

func (s *Store) CustomPricesFrontend() map[string]struct {
	Prompt     float64 `json:"prompt"`
	Completion float64 `json:"completion"`
	Cache      float64 `json:"cache"`
} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]struct {
		Prompt     float64 `json:"prompt"`
		Completion float64 `json:"completion"`
		Cache      float64 `json:"cache"`
	}, len(s.customPrices))
	for model, p := range s.customPrices {
		result[model] = struct {
			Prompt     float64 `json:"prompt"`
			Completion float64 `json:"completion"`
			Cache      float64 `json:"cache"`
		}{
			Prompt:     p[0] * 1e6,
			Completion: p[1] * 1e6,
			Cache:      p[2] * 1e6,
		}
	}
	return result
}

func (s *Store) SetCustomPrices(prices map[string][4]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.customPrices = prices
}

func CalcCost(inputTokens, outputTokens, cachedTokens int64, prices [4]float64) float64 {
	nonCached := float64(inputTokens-cachedTokens) * prices[0]
	cacheCost := float64(cachedTokens) * prices[2]
	outputCost := float64(outputTokens) * prices[1]
	cost := nonCached + cacheCost + outputCost
	if cost < 0 {
		cost = 0
	}
	return cost
}

func StartSyncLoop(store *Store, interval time.Duration) chan struct{} {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if err := store.Sync(); err != nil {
					log.WithError(err).Warn("pricing: periodic sync failed")
				}
			}
		}
	}()
	return stop
}

func matchPricing(model string, allPrices map[string][4]float64) ([4]float64, bool) {
	if p, ok := allPrices[model]; ok {
		return p, true
	}
	lower := strings.ToLower(model)
	if p, ok := allPrices[lower]; ok {
		return p, true
	}
	for _, prefix := range []string{"anthropic/", "openai/", "deepseek/", "gemini/", "google/", "mistral/", "cohere/", "azure_ai/"} {
		if p, ok := allPrices[prefix+model]; ok {
			return p, true
		}
		if p, ok := allPrices[prefix+lower]; ok {
			return p, true
		}
	}
	norm := func(s string) string {
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, "/", ".")
		return s
	}
	modelNorm := norm(model)
	var bestKey string
	var bestScore int
	for k := range allPrices {
		kNorm := norm(k)
		if strings.Contains(kNorm, modelNorm) || strings.Contains(modelNorm, kNorm) {
			score := 10000 - len(k)
			if kNorm == modelNorm {
				score += 100000
			}
			if score > bestScore {
				bestKey = k
				bestScore = score
			}
		}
	}
	if bestKey != "" {
		return allPrices[bestKey], true
	}
	return [4]float64{}, false
}

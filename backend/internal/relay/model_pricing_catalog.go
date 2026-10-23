package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

const officialPricingCatalogURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// Catalog requests are public metadata reads: no relay credentials or model calls.
var officialPricingCatalog = struct {
	sync.Mutex
	expiresAt time.Time
	models    map[string]remoteDefaultModelPricing
}{}

type catalogModelPricing struct {
	Provider         string   `json:"litellm_provider"`
	Mode             string   `json:"mode"`
	InputPrice       *float64 `json:"input_cost_per_token"`
	OutputPrice      *float64 `json:"output_cost_per_token"`
	CacheWritePrice  *float64 `json:"cache_creation_input_token_cost"`
	CacheReadPrice   *float64 `json:"cache_read_input_token_cost"`
	ImageInputPrice  *float64 `json:"input_cost_per_image_token"`
	ImageOutputPrice *float64 `json:"output_cost_per_image_token"`
	PerRequestPrice  *float64 `json:"output_cost_per_image"`
}

func validCatalogPrice(value *float64) *float64 {
	if value == nil || *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	return value
}

func parseOfficialPricingCatalog(reader io.Reader) (map[string]remoteDefaultModelPricing, error) {
	var catalog map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(reader, 16<<20)).Decode(&catalog); err != nil {
		return nil, err
	}
	prices := make(map[string]remoteDefaultModelPricing, len(catalog))
	for model, raw := range catalog {
		if model == "sample_spec" {
			continue
		}
		var entry catalogModelPricing
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}
		price := remoteDefaultModelPricing{
			Provider: entry.Provider, LiteLLMProvider: entry.Provider, Mode: entry.Mode, BillingMode: "token", Source: "official_catalog",
			InputPrice: validCatalogPrice(entry.InputPrice), OutputPrice: validCatalogPrice(entry.OutputPrice),
			CacheWritePrice: validCatalogPrice(entry.CacheWritePrice), CacheReadPrice: validCatalogPrice(entry.CacheReadPrice),
			ImageInputPrice: validCatalogPrice(entry.ImageInputPrice), ImageOutputPrice: validCatalogPrice(entry.ImageOutputPrice),
			PerRequestPrice: validCatalogPrice(entry.PerRequestPrice),
		}
		if price.PerRequestPrice != nil && price.InputPrice == nil && price.OutputPrice == nil {
			price.BillingMode = "per_request"
		}
		price.Found = officialModelPricing(price) != nil
		if !price.Found && strings.TrimSpace(price.Mode) == "" {
			continue
		}
		prices[strings.ToLower(strings.TrimSpace(model))] = price
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("official pricing catalog is empty")
	}
	return prices, nil
}

func (s *Service) loadOfficialPricingCatalog(ctx context.Context) map[string]remoteDefaultModelPricing {
	officialPricingCatalog.Lock()
	defer officialPricingCatalog.Unlock()
	if time.Now().Before(officialPricingCatalog.expiresAt) {
		return officialPricingCatalog.models
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, officialPricingCatalogURL, nil)
	if err != nil {
		return officialPricingCatalog.models
	}
	response, err := s.client.Do(req)
	if err == nil {
		defer response.Body.Close()
		if response.StatusCode == http.StatusOK {
			if prices, parseErr := parseOfficialPricingCatalog(response.Body); parseErr == nil {
				officialPricingCatalog.models = prices
				officialPricingCatalog.expiresAt = time.Now().Add(6 * time.Hour)
				return prices
			}
		}
	}
	// Retain the last successful public catalog on a transient network failure.
	officialPricingCatalog.expiresAt = time.Now().Add(5 * time.Minute)
	return officialPricingCatalog.models
}

func (s *Service) fillOfficialPricingFallback(ctx context.Context, prices map[string]remoteDefaultModelPricing, models []string, includeModes ...bool) {
	missing := make([]string, 0)
	for _, model := range models {
		if officialModelPricing(prices[model]) == nil {
			missing = append(missing, model)
		}
	}
	if len(missing) == 0 && (len(includeModes) == 0 || !includeModes[0]) {
		return
	}
	catalog := s.loadOfficialPricingCatalog(ctx)
	for _, model := range models {
		// Exact model identity only: do not assign a guessed family price to aliases.
		if price, ok := catalog[strings.ToLower(strings.TrimSpace(model))]; ok {
			current := prices[model]
			if officialModelPricing(current) == nil {
				prices[model] = price
			} else if current.Mode == "" {
				// Capability metadata must not replace channel/system prices (including zero).
				current.Mode = price.Mode
				prices[model] = current
			}
		}
	}
}

func pricingReferenceSource(price remoteDefaultModelPricing) string {
	if officialModelPricing(price) == nil {
		return ""
	}
	if price.Source == "official_catalog" {
		return "official_catalog"
	}
	return "system_channel"
}

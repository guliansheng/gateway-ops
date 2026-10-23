package relay

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOfficialPricingCatalogParsesValidPricesAndPreservesZero(t *testing.T) {
	catalog := `{
		"sample_spec":{"input_cost_per_token":0},
		"test-model":{"litellm_provider":"openai","input_cost_per_token":0.000002,"output_cost_per_token":0.000006,"cache_read_input_token_cost":0,"cache_creation_input_token_cost":0.000003},
		"test-image":{"output_cost_per_image":0.04},
		"invalid":{"input_cost_per_token":-1},
		"metadata-only":{"max_tokens":1000},
		"invalid-schema":{"input_cost_per_token":"unknown"}
	}`
	prices, err := parseOfficialPricingCatalog(strings.NewReader(catalog))
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 2 {
		t.Fatalf("parsed models = %d, want two valid entries", len(prices))
	}
	price := prices["test-model"]
	if !price.Found || price.InputPrice == nil || *price.InputPrice != 0.000002 || price.CacheReadPrice == nil || *price.CacheReadPrice != 0 {
		t.Fatalf("token pricing = %#v", price)
	}
	if pricingReferenceSource(price) != "official_catalog" {
		t.Fatalf("source = %q", pricingReferenceSource(price))
	}
	if prices["test-image"].BillingMode != "per_request" {
		t.Fatal("image price must be billed per request")
	}
}

func TestOfficialPricingFallbackKeepsSystemPriceAndRequiresExactModel(t *testing.T) {
	officialPricingCatalog.Lock()
	previousModels, previousExpiry := officialPricingCatalog.models, officialPricingCatalog.expiresAt
	officialPricingCatalog.models = map[string]remoteDefaultModelPricing{
		"test-model":     {Found: true, InputPrice: floatPointer(9e-6), Source: "official_catalog"},
		"fallback-model": {Found: true, InputPrice: floatPointer(3e-6), Source: "official_catalog"},
	}
	officialPricingCatalog.expiresAt = time.Now().Add(time.Hour)
	officialPricingCatalog.Unlock()
	t.Cleanup(func() {
		officialPricingCatalog.Lock()
		officialPricingCatalog.models, officialPricingCatalog.expiresAt = previousModels, previousExpiry
		officialPricingCatalog.Unlock()
	})
	prices := map[string]remoteDefaultModelPricing{
		"test-model": {Found: true, InputPrice: floatPointer(0)},
	}
	service := &Service{} // Any unintended network lookup would fail.
	service.fillOfficialPricingFallback(context.Background(), prices, []string{"test-model", "fallback-model", "fallback-model-custom"})
	if prices["test-model"].InputPrice == nil || *prices["test-model"].InputPrice != 0 || pricingReferenceSource(prices["test-model"]) != "system_channel" {
		t.Fatal("explicit zero system price was overwritten")
	}
	if prices["fallback-model"].InputPrice == nil || *prices["fallback-model"].InputPrice != 3e-6 {
		t.Fatal("missing system pricing was not filled from the official catalog")
	}
	if _, exists := prices["fallback-model-custom"]; exists {
		t.Fatal("a guessed price was assigned to a custom alias")
	}
}

func TestOfficialPricingReferenceDoesNotTreatFoundFlagAsPrice(t *testing.T) {
	if officialModelPricing(remoteDefaultModelPricing{Found: true}) != nil {
		t.Fatal("an empty price object must remain unavailable")
	}
	if officialModelPricing(remoteDefaultModelPricing{InputPrice: floatPointer(0)}) == nil {
		t.Fatal("explicit zero is valid reference pricing")
	}
}

func TestOfficialPricingCatalogRetainsCapabilityMetadataWithoutInventingPrice(t *testing.T) {
	prices, err := parseOfficialPricingCatalog(strings.NewReader(`{
		"opaque-paint":{"mode":"image_generation","litellm_provider":"openai"},
		"image-analysis-chat":{"mode":"chat","litellm_provider":"openai"},
		"priced-image":{"mode":"image_generation","input_cost_per_token":0.000005}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 3 || prices["opaque-paint"].Mode != "image_generation" || prices["image-analysis-chat"].Mode != "chat" {
		t.Fatalf("capability metadata = %#v", prices)
	}
	if prices["opaque-paint"].Found || officialModelPricing(prices["opaque-paint"]) != nil {
		t.Fatal("capability-only image model acquired an invented price")
	}
	if !modelUsesGroupImagePricing("opaque-paint", "opaque-paint", prices, nil) || modelUsesGroupImagePricing("image-analysis-chat", "image-analysis-chat", prices, nil) {
		t.Fatal("catalog image-generation mode did not distinguish a chat model")
	}
}

func TestOfficialPricingModesDoNotOverwriteExistingZeroOrChannelReference(t *testing.T) {
	officialPricingCatalog.Lock()
	previousModels, previousExpiry := officialPricingCatalog.models, officialPricingCatalog.expiresAt
	officialPricingCatalog.models = map[string]remoteDefaultModelPricing{
		"opaque-paint": {Found: true, Mode: "image_generation", InputPrice: floatPointer(99e-6), Source: "official_catalog"},
	}
	officialPricingCatalog.expiresAt = time.Now().Add(time.Hour)
	officialPricingCatalog.Unlock()
	t.Cleanup(func() {
		officialPricingCatalog.Lock()
		officialPricingCatalog.models, officialPricingCatalog.expiresAt = previousModels, previousExpiry
		officialPricingCatalog.Unlock()
	})
	prices := map[string]remoteDefaultModelPricing{"opaque-paint": {Found: true, InputPrice: floatPointer(0)}}
	service := &Service{}
	service.fillOfficialPricingFallback(context.Background(), prices, []string{"opaque-paint"}, true)
	got := prices["opaque-paint"]
	if got.Mode != "image_generation" || got.InputPrice == nil || *got.InputPrice != 0 || pricingReferenceSource(got) != "system_channel" {
		t.Fatalf("metadata merge replaced system price: %#v", got)
	}
	reference, source := channelPricingReference(got, &remoteModelPricing{InputPrice: floatPointer(1.5e-6)})
	if reference == nil || *reference.InputPrice != 1.5e-6 || source != "channel_pricing" {
		t.Fatal("catalog metadata replaced the saved channel reference")
	}
}

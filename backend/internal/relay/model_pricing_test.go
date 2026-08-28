package relay

import (
	"testing"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func floatPointer(value float64) *float64 { return &value }

func TestMergeEffectivePricingFillsMissingCachePrices(t *testing.T) {
	defaults := remoteDefaultModelPricing{
		Found: true, InputPrice: floatPointer(1e-6), OutputPrice: floatPointer(5e-6),
		CacheWritePrice: floatPointer(1.25e-6), CacheReadPrice: floatPointer(1e-7),
	}
	manual := &remoteModelPricing{InputPrice: floatPointer(2e-6), CacheWritePrice: floatPointer(0)}
	got, source := mergeEffectivePricing(defaults, manual)
	if source != "channel_override_with_default" {
		t.Fatalf("source = %q", source)
	}
	if got.InputPrice == nil || *got.InputPrice != 2e-6 {
		t.Fatalf("input price = %v", got.InputPrice)
	}
	if got.CacheWritePrice == nil || *got.CacheWritePrice != 0 {
		t.Fatalf("explicit zero cache write lost: %v", got.CacheWritePrice)
	}
	if got.CacheReadPrice == nil || *got.CacheReadPrice != 1e-7 {
		t.Fatalf("cache read fallback = %v", got.CacheReadPrice)
	}
}

func TestPricingLookupModelUsesChannelMapping(t *testing.T) {
	mapping := map[string]map[string]string{"openai": {"public-model": "gpt-5.4"}}
	if got := pricingLookupModel("channel_mapped", "OpenAI", "public-model", mapping); got != "gpt-5.4" {
		t.Fatalf("mapped model = %q", got)
	}
	if got := pricingLookupModel("requested", "openai", "public-model", mapping); got != "public-model" {
		t.Fatalf("requested model = %q", got)
	}
}

func TestModelCompanyUsesModelIdentityNotProtocol(t *testing.T) {
	tests := map[string]string{
		"deepseek-v4-pro": "DeepSeek", "qwen3.8-max": "阿里云", "glm-5.3": "智谱 AI",
		"kimi-k2.7-code": "月之暗面", "gpt-5.6-sol": "OpenAI", "claude-opus-4-6": "Anthropic",
		"llama-4-maverick": "Meta", "mistral-large-latest": "Mistral AI", "command-r-plus": "Cohere",
		"amazon-nova-pro": "Amazon", "phi-4": "Microsoft", "nemotron-3-super": "NVIDIA",
		"ernie-4.5": "百度", "doubao-seed-1.8": "字节跳动", "hunyuan-t1": "腾讯",
		"pangu-pro": "华为", "baichuan4": "百川智能", "step-3.5-flash": "阶跃星辰",
		"yi-lightning": "零一万物", "sparkdesk-v4": "科大讯飞", "sensenova-v6": "商汤",
	}
	for model, want := range tests {
		if got := modelCompany(model, "openai"); got != want {
			t.Errorf("modelCompany(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestPricingModelsAndGroupsUseActualAvailability(t *testing.T) {
	groups := []pricingGroupAvailability{
		{group: storage.RelayGroup{Name: "GPT-PRO"}, models: map[string]struct{}{"gpt-5.4": {}, "gpt-5.6-sol": {}}},
		{group: storage.RelayGroup{Name: "GPT-特价"}, models: map[string]struct{}{"gpt-5.4": {}, "gpt-5.5": {}}},
	}
	models := pricingModelsForGroups(groups)
	if len(models) != 3 {
		t.Fatalf("model count = %d, want 3", len(models))
	}
	if _, ok := models["gpt-5.6-terra"]; ok {
		t.Fatal("model absent from current groups was included")
	}
	groupNames := pricingGroupNamesForModel(groups, "gpt-5.4")
	if len(groupNames) != 2 || groupNames[0] != "GPT-PRO" || groupNames[1] != "GPT-特价" {
		t.Fatalf("group names = %#v", groupNames)
	}
}

func TestGroupImagePricingUsesResolutionPricesPerRequest(t *testing.T) {
	price1K, price2K, price4K := 0.01, 0.02, 0.04
	group := storage.RelayGroup{
		Name: "Grok-Heavy", AllowImageGeneration: true, ImageRateIndependent: true,
		ImagePrice1K: &price1K, ImagePrice2K: &price2K, ImagePrice4K: &price4K,
	}
	availability := []pricingGroupAvailability{{
		group:  group,
		models: map[string]struct{}{"grok-4.6": {}, "grok-imagine-image-quality": {}},
	}}

	priced, regular := splitGroupImagePricing(availability, "grok-imagine-image-quality")
	if len(priced) != 1 || len(regular) != 0 {
		t.Fatalf("image split = priced %d regular %d", len(priced), len(regular))
	}
	priced, regular = splitGroupImagePricing(availability, "grok-4.6")
	if len(priced) != 0 || len(regular) != 1 {
		t.Fatalf("chat split = priced %d regular %d", len(priced), len(regular))
	}

	item := groupImagePricingItem(storage.RelayStation{ID: 1, Name: "奇点"}, "grok", "grok-imagine-image-quality", group, 0, group.Name)
	if item.BillingMode != "per_request" || item.PriceSource != "group_image_pricing" {
		t.Fatalf("billing = %q source = %q", item.BillingMode, item.PriceSource)
	}
	if item.ImagePrice1K == nil || *item.ImagePrice1K != price1K || item.ImagePrice2K == nil || *item.ImagePrice2K != price2K || item.ImagePrice4K == nil || *item.ImagePrice4K != price4K {
		t.Fatalf("resolution prices = %#v %#v %#v", item.ImagePrice1K, item.ImagePrice2K, item.ImagePrice4K)
	}
	if item.Company != "xAI" || len(item.Groups) != 1 || item.Groups[0] != group.Name {
		t.Fatalf("identity = company %q groups %#v", item.Company, item.Groups)
	}
}

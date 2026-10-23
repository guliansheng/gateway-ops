package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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

func TestPublicPricingGroupsOnlyIncludesActiveNonExclusiveWithPricingEnabled(t *testing.T) {
	groups := []storage.RelayGroup{
		{ID: 1, Name: "公开", Status: "active", Platform: "openai", ModelPricingEnabled: true},
		{ID: 2, Name: "默认状态", Status: "", Platform: "composite", ModelPricingEnabled: true},
		{ID: 3, Name: "专属字样但实际公开", Status: " ENABLED ", ModelPricingEnabled: true},
		{ID: 4, Name: "公开字样但实际专属", Status: "active", IsExclusive: true, ModelPricingEnabled: true},
		{ID: 5, Name: "专属默认状态", IsExclusive: true, ModelPricingEnabled: true},
		{ID: 6, Name: "专属已启用", Status: "enabled", IsExclusive: true, ModelPricingEnabled: true},
		{ID: 7, Name: "已停用", Status: "inactive", ModelPricingEnabled: true},
		{ID: 8, Name: "已禁用", Status: "disabled", ModelPricingEnabled: true},
		{ID: 9, Name: "价格已关闭但监控开启", Status: "active", MonitorEnabled: true},
	}
	want := append([]storage.RelayGroup(nil), groups[:3]...)
	if got := publicPricingGroups(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("public groups = %#v, want %#v", got, want)
	}
	if groups[3].ID != 4 || !groups[3].IsExclusive {
		t.Fatal("filter modified the original groups")
	}
}

func TestFetchAvailableGroupModelsSkipsExclusiveAndInactiveGroups(t *testing.T) {
	groups := []storage.RelayGroup{
		{ID: 1, Status: "active", IsExclusive: true, ModelPricingEnabled: true},
		{ID: 2, Status: "", IsExclusive: true, ModelPricingEnabled: true},
		{ID: 3, Status: "inactive", ModelPricingEnabled: true},
		{ID: 4, Status: "disabled", ModelPricingEnabled: true},
		{ID: 5, Status: "active", MonitorEnabled: true, ModelPricingEnabled: false},
	}
	// No repository or HTTP client: hidden groups must be excluded before any lookup.
	svc := &Service{}
	got, err := svc.fetchAvailableGroupModels(context.Background(), groups)
	if err != nil || len(got) != 0 {
		t.Fatalf("available models = %#v, err = %v; want empty models without lookups", got, err)
	}
}

func TestStationModelPricingUsesSavedChannelPricesPerToken(t *testing.T) {
	// Sub2API's admin form saves 1.5/4.5 USD/MTok as 0.0000015/0.0000045 USD/token.
	// Exercise the existing sync serializer and PricingJSON decoder, not a separate fixture schema.
	const model = "deepseek-v4.1-flash"
	const alias = "public-flash"
	first := remoteChannel{
		ID: 11, Name: "渠道甲", BillingModelSource: "channel_mapped",
		ModelMapping: map[string]map[string]string{"deepseek": {alias: model}},
		ModelPricing: []remoteModelPricing{{ID: 1, Platform: "deepseek", Models: []string{model}, BillingMode: "token", InputPrice: floatPointer(1.5e-6), OutputPrice: floatPointer(4.5e-6), CacheReadPrice: floatPointer(0)}},
		// Account-statistics prices are not public billing prices.
		AccountStatsPricingRules: []remotePricingRule{{Pricing: []remoteModelPricing{{Platform: "deepseek", Models: []string{model}, InputPrice: floatPointer(99)}}}},
	}
	second := remoteChannel{
		ID: 12, Name: "渠道乙", BillingModelSource: "requested",
		ModelPricing: []remoteModelPricing{{ID: 2, Platform: "deepseek", Models: []string{model}, BillingMode: "token", InputPrice: floatPointer(0), OutputPrice: floatPointer(8e-6)}},
	}
	builds := make([]pricingChannelBuild, 0, 2)
	for _, channel := range []remoteChannel{first, second} {
		body, _, _, _, _, err := channelPricing(channel)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot channelPricingSnapshot
		if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
			t.Fatal(err)
		}
		builds = append(builds, pricingChannelBuild{
			channel: storage.RelayChannel{ExternalID: channel.ID, Name: channel.Name}, snapshot: snapshot,
			groups: map[string][]pricingGroupAvailability{"composite": {{group: storage.RelayGroup{Name: "组合分组", RateMultiplier: 3}, models: map[string]struct{}{model: {}, alias: {}}}}},
		})
	}
	defaults := map[string]remoteDefaultModelPricing{
		model: {Found: true, InputPrice: floatPointer(9e-6), OutputPrice: floatPointer(27e-6), CacheReadPrice: floatPointer(7e-7), CacheWritePrice: floatPointer(2e-6), Source: "official_catalog"},
	}
	items := stationModelPricingItems(storage.RelayStation{ID: 1}, nil, builds, defaults)
	for _, channelID := range []int64{11, 12} {
		var got *PublicModelPricingItem
		for i := range items {
			if items[i].ChannelID == channelID && items[i].Model == model {
				got = &items[i]
			}
		}
		if got == nil || got.OfficialPricing == nil || got.OfficialPricingSource != "channel_pricing" || got.PriceSource != "channel_override_with_default" {
			t.Fatalf("channel %d pricing = %#v", channelID, got)
		}
		wantInput, wantOutput := 1.5e-6, 4.5e-6
		if channelID == 12 {
			wantInput, wantOutput = 0, 8e-6
		}
		if got.InputPrice == nil || *got.InputPrice != wantInput || got.OutputPrice == nil || *got.OutputPrice != wantOutput || *got.OfficialPricing.InputPrice != wantInput || *got.OfficialPricing.OutputPrice != wantOutput {
			t.Fatalf("channel %d did not override system/catalog prices: %#v", channelID, got)
		}
		if got.CacheWritePrice == nil || *got.CacheWritePrice != 2e-6 || got.OfficialPricing.CacheWritePrice == nil || *got.OfficialPricing.CacheWritePrice != 2e-6 {
			t.Fatal("missing channel field must fall back to the default price")
		}
		wantRead := 0.0
		if channelID == 12 {
			wantRead = 7e-7
		}
		if got.CacheReadPrice == nil || *got.CacheReadPrice != wantRead || *got.OfficialPricing.CacheReadPrice != wantRead {
			t.Fatal("zero and missing channel fields must remain distinct")
		}
	}
	for _, item := range items {
		if item.ChannelID == 11 && item.Model == alias {
			if item.BillingModel != model || item.OfficialPricing == nil || *item.OfficialPricing.InputPrice != 1.5e-6 {
				t.Fatalf("mapped alias = %#v", item)
			}
			return
		}
	}
	t.Fatal("actual public alias was omitted")
}

func TestChannelPricingReferenceDistinguishesMissingAndZero(t *testing.T) {
	tests := []struct {
		name       string
		defaults   remoteDefaultModelPricing
		manual     *remoteModelPricing
		wantSource string
		wantInput  *float64
	}{
		{"missing", remoteDefaultModelPricing{}, nil, "", nil},
		{"found-without-price", remoteDefaultModelPricing{Found: true}, &remoteModelPricing{}, "", nil},
		{"default-zero", remoteDefaultModelPricing{InputPrice: floatPointer(0)}, &remoteModelPricing{}, "system_channel", floatPointer(0)},
		{"channel-zero", remoteDefaultModelPricing{InputPrice: floatPointer(8e-6)}, &remoteModelPricing{InputPrice: floatPointer(0)}, "channel_pricing", floatPointer(0)},
		{"channel-only", remoteDefaultModelPricing{}, &remoteModelPricing{InputPrice: floatPointer(2e-6)}, "channel_pricing", floatPointer(2e-6)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, source := channelPricingReference(tt.defaults, tt.manual)
			if source != tt.wantSource {
				t.Fatalf("source = %q, want %q", source, tt.wantSource)
			}
			if tt.wantInput == nil {
				if got != nil {
					t.Fatalf("missing reference = %#v", got)
				}
			} else if got == nil || got.InputPrice == nil || *got.InputPrice != *tt.wantInput {
				t.Fatalf("reference = %#v, want input %v", got, *tt.wantInput)
			}
			_, effectiveSource := mergeEffectivePricing(tt.defaults, tt.manual)
			if tt.wantInput == nil && effectiveSource != "unavailable" {
				t.Fatalf("empty pricing source = %q", effectiveSource)
			}
		})
	}
}

func TestPricingPlatformsMappingsAndWildcardPrices(t *testing.T) {
	entries := []remoteModelPricing{
		{ID: 1, Platform: "anthropic", Models: []string{"shared-*"}, InputPrice: floatPointer(1e-6)},
		{ID: 2, Platform: "gemini", Models: []string{"shared-model"}, InputPrice: floatPointer(2e-6)},
		{ID: 3, Platform: "openai", Models: []string{"shared-model"}, InputPrice: floatPointer(3e-6)},
		{ID: 4, Platform: "deepseek", Models: []string{"shared-model"}, InputPrice: floatPointer(4e-6)},
		{ID: 5, Models: []string{"legacy"}, InputPrice: floatPointer(5e-6)},
		{ID: 6, Platform: "opencode_go", Models: []string{"addon"}, InputPrice: floatPointer(6e-6)},
		{ID: 7, Platform: "typesafe", Models: []string{"addon"}, InputPrice: floatPointer(7e-6)},
	}
	index := manualPricingIndex(entries)
	for _, tt := range []struct {
		platform, model string
		wantID          int64
	}{
		{"composite", "SHARED-MODEL", 2}, // All exact matches precede wildcard matches.
		{"openai", "shared-model", 3}, {"deepseek", "shared-model", 4},
		{"anthropic", "shared-model", 1}, {"grok", "shared-model", 0},
		{"anthropic", "legacy", 5}, {"composite", "addon", 6},
	} {
		got := manualPlatformPricingLookup(index, tt.platform, tt.model)
		if tt.wantID == 0 {
			if got != nil {
				t.Fatalf("cross-platform pricing leaked: %#v", got)
			}
		} else if got == nil || got.ID != tt.wantID {
			t.Fatalf("lookup %s/%s = %#v, want ID %d", tt.platform, tt.model, got, tt.wantID)
		}
	}
	mapping := map[string]map[string]string{
		" Anthropic ": {"public-*": "claude-model"},
		"Gemini":      {"PUBLIC-MODEL": "gemini-model"},
		"openai":      {"public-model": "gpt-model"},
	}
	if got := pricingLookupModel("channel_mapped", "composite", "public-model", mapping); got != "gemini-model" {
		t.Fatalf("composite exact mapping = %q", got)
	}
	if got := pricingMappedModel("anthropic", "public-other", mapping); got != "claude-model" {
		t.Fatalf("wildcard mapping = %q", got)
	}
	if got := pricingLookupModel("requested", "composite", "public-model", mapping); got != "public-model" {
		t.Fatalf("requested billing source changed = %q", got)
	}
}

func TestPublicPricingGroupOrderIsNotAlphabeticalOrRateSorted(t *testing.T) {
	// Already ordered as storage.ListGroups: sort_order ASC, external_id ASC.
	groups := []storage.RelayGroup{
		{ID: 1, ExternalID: 9, SortOrder: -1, Name: "Z-优先", RateMultiplier: 9, ModelPricingEnabled: true},
		{ID: 2, ExternalID: 1, SortOrder: 0, Name: "隐藏", IsExclusive: true, ModelPricingEnabled: true},
		{ID: 3, ExternalID: 2, SortOrder: 0, Name: "B-同序较小ID", RateMultiplier: 4, ModelPricingEnabled: true},
		{ID: 4, ExternalID: 7, SortOrder: 0, Name: "A-同序较大ID", RateMultiplier: 1, ModelPricingEnabled: true},
	}
	got := publicPricingGroups(groups)
	want := []storage.RelayGroup{groups[0], groups[2], groups[3]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("public group order = %#v, want %#v", got, want)
	}
	availability := make([]pricingGroupAvailability, 0, len(got))
	for _, group := range got {
		availability = append(availability, pricingGroupAvailability{group: group, models: map[string]struct{}{"model": {}}})
	}
	if names := pricingGroupNamesForModel(availability, "model"); !reflect.DeepEqual(names, []string{groups[0].Name, groups[2].Name, groups[3].Name}) {
		t.Fatalf("model group order = %#v", names)
	}
}

func TestStationModelPricingImageAliasesMixedGroupsAndNoReference(t *testing.T) {
	group := storage.RelayGroup{Name: "混合", AllowImageGeneration: true, ImageRateIndependent: true, RateMultiplier: 10, ImageRateMultiplier: 20, ImagePrice1K: floatPointer(0), ImagePrice2K: floatPointer(0.02), ImagePrice4K: floatPointer(0.04)}
	disabled := group
	disabled.Name, disabled.ImageRateIndependent = "未启用独立价", false
	models := map[string]struct{}{"paint-alias": {}, "mode-alias": {}, "grok-4-chat": {}, "chat-image-analysis": {}, "gpt-image-1": {}, "generic-request": {}}
	build := pricingChannelBuild{
		channel: storage.RelayChannel{ExternalID: 1, Name: "图文渠道"},
		snapshot: channelPricingSnapshot{
			BillingModelSource: "requested", ModelMapping: map[string]map[string]string{"openai": {"paint-alias": "gpt-image-1"}},
			ModelPricing: []remoteModelPricing{
				{Platform: "openai", Models: []string{"mode-alias"}, BillingMode: "image", PerRequestPrice: floatPointer(999)},
				{Platform: "openai", Models: []string{"generic-request"}, BillingMode: "per_request", PerRequestPrice: floatPointer(999)},
			},
		},
		groups: map[string][]pricingGroupAvailability{"composite": {{group: group, models: models}, {group: disabled, models: models}}},
	}
	defaults := map[string]remoteDefaultModelPricing{
		"gpt-image-1":         {Found: true, Mode: "image_generation", InputPrice: floatPointer(5e-6), PerRequestPrice: floatPointer(888)},
		"chat-image-analysis": {Found: true, Mode: "chat", InputPrice: floatPointer(1e-6)},
	}
	items := stationModelPricingItems(storage.RelayStation{ID: 1}, nil, []pricingChannelBuild{build}, defaults)
	images := map[string]bool{}
	for _, item := range items {
		if item.Model == "generic-request" {
			t.Fatal("generic channel per-request prices leaked into the public directory")
		}
		if item.PriceSource != "group_image_pricing" {
			if item.BillingMode != "token" {
				t.Fatalf("unexpected public billing mode %q", item.BillingMode)
			}
			continue
		}
		images[item.Model] = true
		if item.OfficialPricing != nil || item.OfficialPricingSource != "" || item.InputPrice != nil || item.PerRequestPrice != nil {
			t.Fatalf("image has non-group/reference pricing: %#v", item)
		}
		if !reflect.DeepEqual(item.Groups, []string{group.Name}) || *item.ImagePrice1K != 0 || *item.ImagePrice2K != 0.02 || *item.ImagePrice4K != 0.04 {
			t.Fatalf("group resolution price scaled or leaked into disabled group: %#v", item)
		}
		if item.Model == "paint-alias" && (item.Company != "OpenAI" || item.BillingModel != "gpt-image-1") {
			t.Fatalf("mapped image identity = %#v", item)
		}
	}
	if !reflect.DeepEqual(images, map[string]bool{"paint-alias": true, "mode-alias": true, "gpt-image-1": true}) {
		t.Fatalf("image identities = %#v", images)
	}
	for _, tt := range []struct {
		model, mapped string
		manual        *remoteModelPricing
		want          bool
	}{
		{"chat-image-analysis", "chat-image-analysis", &remoteModelPricing{Models: []string{"chat-image-analysis"}, BillingMode: "image"}, false},
		{"grok-4-chat", "grok-4-chat", nil, false},
		{"chat-image-alias", "grok-4-chat", nil, false},
		{"unknown", "unknown", &remoteModelPricing{Models: []string{"*"}, BillingMode: "image"}, false},
		{"imagen-4.0-generate", "imagen-4.0-generate", nil, true},
		{"gemini-3-pro-image-preview", "gemini-3-pro-image-preview", nil, true},
	} {
		if got := modelUsesGroupImagePricing(tt.model, tt.mapped, defaults, tt.manual); got != tt.want {
			t.Errorf("image identity %s/%s = %v, want %v", tt.model, tt.mapped, got, tt.want)
		}
	}
}

func TestIndependentGroupImagePricingDistinguishesDisabledMissingAndZero(t *testing.T) {
	base := storage.RelayGroup{AllowImageGeneration: true, ImageRateIndependent: true}
	if hasIndependentGroupImagePricing(base) {
		t.Fatal("missing prices must not create request pricing")
	}
	base.ImagePrice1K = floatPointer(0)
	if !hasIndependentGroupImagePricing(base) {
		t.Fatal("explicit free image price was lost")
	}
	base.ImageRateIndependent = false
	if hasIndependentGroupImagePricing(base) {
		t.Fatal("disabled independent pricing was included")
	}
	base.ImageRateIndependent, base.AllowImageGeneration = true, false
	if hasIndependentGroupImagePricing(base) {
		t.Fatal("image-disabled group was included")
	}
}

// These regressions use only an explicitly supplied disposable database and mocked metadata.
func pricingIntegrationService(t *testing.T, handler http.Handler) (*Service, *gorm.DB, storage.RelayStation) {
	t.Helper()
	dsn := os.Getenv("GATEWAYOPS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GATEWAYOPS_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&storage.RelayStation{}, &storage.RelayGroup{}, &storage.RelayChannel{}, &storage.RelayChannelGroup{}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cipher, err := crypto.NewCipher("pricing-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := cipher.Encrypt("metadata-test-key")
	if err != nil {
		t.Fatal(err)
	}
	station := storage.RelayStation{Name: fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()), BaseURL: server.URL, APIKeyCipher: key}
	if err := db.Create(&station).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewService(storage.NewRelayStations(db), cipher)
	svc.client = &http.Client{Transport: pricingMetadataTestTransport{base: server.Client().Transport}, Timeout: 5 * time.Second}
	return svc, db, station
}

type pricingMetadataTestTransport struct{ base http.RoundTripper }

func (transport pricingMetadataTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.String() == officialPricingCatalogURL {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"gpt-image-1":{"mode":"image_generation","litellm_provider":"openai"},"gpt-6-astra":{"mode":"chat","input_cost_per_token":0.0000015,"output_cost_per_token":0.0000045}}`)), Request: request}, nil
	}
	if request.URL.Hostname() != "127.0.0.1" || request.Method != http.MethodGet {
		return nil, fmt.Errorf("unmocked or non-read-only pricing request")
	}
	return transport.base.RoundTrip(request)
}

func TestPublicPricingImageGroupWithoutGatewayKey(t *testing.T) {
	svc, db, station := pricingIntegrationService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" && r.Header.Get("x-api-key") != "metadata-test-key" {
			t.Error("metadata request did not use the station admin credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/admin/groups/58/api-keys":
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[],"pages":1}}`)
		case "/api/v1/admin/groups/66/api-keys":
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"key":"gateway-test-key","status":"active","group_id":66,"user":{"role":"admin"}}],"pages":1}}`)
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-6-astra"}]}`)
		case "/api/v1/admin/groups/58":
			_, _ = io.WriteString(w, `{"code":0,"data":{"id":58,"platform":"openai","model_allowlist":{"enabled":true,"models":["gpt-image-*"]}}}`)
		case "/api/v1/admin/accounts":
			if r.URL.Query().Get("group") != "58" {
				t.Error("account lookup was not scoped to the image group")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"id":101,"platform":"openai","type":"apikey","status":"active","schedulable":true,"group_ids":[58]},{"id":102,"platform":"openai","type":"apikey","status":"inactive","schedulable":true,"group_ids":[58]},{"id":103,"platform":"openai","type":"apikey","status":"active","schedulable":true,"group_ids":[99]}],"pages":1}}`)
		case "/api/v1/admin/accounts/101/models":
			_, _ = io.WriteString(w, `{"code":0,"data":[{"id":"gpt-image-1"},{"id":"gpt-vision-chat"},{"id":"gpt-image-1"}]}`)
		case "/api/v1/admin/channels/model-pricing":
			_, _ = io.WriteString(w, `{"code":0,"data":{"found":true,"billing_mode":"token","input_price":0.0000015,"output_price":0.0000045}}`)
		default:
			t.Errorf("unexpected pricing request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	groups := []storage.RelayGroup{
		{RelayStationID: station.ID, ExternalID: 58, Name: "生图", Platform: "openai", Status: "active", ModelPricingEnabled: true, RateMultiplier: 10, AllowImageGeneration: true, ImageRateIndependent: true, ImageRateMultiplier: 20, ImagePrice1K: floatPointer(0.03), ImagePrice2K: floatPointer(0.04), ImagePrice4K: floatPointer(0.06)},
		{RelayStationID: station.ID, ExternalID: 66, Name: "已配置密钥", Platform: "openai", Status: "active", ModelPricingEnabled: true, RateMultiplier: 1},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	view, err := svc.PublicModelPricing(context.Background(), station.ID)
	if err != nil {
		t.Fatal(err)
	}
	var image *PublicModelPricingItem
	for index := range view.Items {
		item := &view.Items[index]
		if item.Model == "gpt-vision-chat" {
			t.Fatal("model excluded by the group allowlist was published")
		}
		if item.Model == "gpt-image-1" {
			if image != nil {
				t.Fatal("duplicate image price row")
			}
			image = item
		}
	}
	if image == nil {
		t.Fatalf("configured image group without gateway key was omitted: %#v", view.Items)
	}
	if image.PriceSource != "group_image_pricing" || image.BillingMode != "per_request" || image.OfficialPricing != nil || image.InputPrice != nil || image.PerRequestPrice != nil {
		t.Fatalf("image billing included token/reference prices: %#v", image)
	}
	if !reflect.DeepEqual(image.Groups, []string{"生图"}) || image.ImagePrice1K == nil || *image.ImagePrice1K != 0.03 || image.ImagePrice2K == nil || *image.ImagePrice2K != 0.04 || image.ImagePrice4K == nil || *image.ImagePrice4K != 0.06 {
		t.Fatalf("configured 1K/2K/4K prices were lost or multiplied: %#v", image)
	}
}

func TestPublicPricingPartialModelFailureIsVisible(t *testing.T) {
	svc, db, station := pricingIntegrationService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/admin/groups/66/api-keys":
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"key":"gateway-test-key","status":"active","group_id":66,"user":{"role":"admin"}}],"pages":1}}`)
		case "/api/v1/admin/groups/99/api-keys":
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[],"pages":1}}`)
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-6-astra"}]}`)
		case "/api/v1/admin/groups/99", "/api/v1/admin/accounts":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/api/v1/admin/channels/model-pricing":
			_, _ = io.WriteString(w, `{"code":0,"data":{"found":true,"billing_mode":"token","input_price":0.0000015}}`)
		default:
			t.Errorf("unexpected pricing request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	groups := []storage.RelayGroup{
		{RelayStationID: station.ID, ExternalID: 66, Name: "成功分组", Platform: "openai", Status: "active", ModelPricingEnabled: true, RateMultiplier: 1},
		{RelayStationID: station.ID, ExternalID: 99, Name: "失败分组", Platform: "openai", Status: "active", ModelPricingEnabled: true, RateMultiplier: 1},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	view, err := svc.PublicModelPricing(context.Background(), station.ID)
	if err != nil {
		t.Fatalf("successful groups must remain visible: %v", err)
	}
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Warnings) != 1 || !strings.Contains(response.Warnings[0], "失败分组") {
		t.Fatalf("partial collection failure was silently dropped: %s", body)
	}
	if strings.Contains(response.Warnings[0], "test-key") {
		t.Fatal("public collection warning leaked a credential")
	}
}

func TestPricingAdminFallbackEligibilityAndAllowlist(t *testing.T) {
	account := remoteAccount{ID: 1, Platform: "openai", Type: "apikey", Status: "active", Schedulable: true, GroupIDs: []int64{58}}
	if !pricingAccountEligible(account, 58, "openai", false) || pricingAccountEligible(account, 99, "openai", false) || pricingAccountEligible(account, 58, "gemini", false) || pricingAccountEligible(account, 58, "openai", true) {
		t.Fatal("account/group/platform/OAuth filtering changed")
	}
	account.Platform, account.Extra = "antigravity", map[string]any{"mixed_scheduling": true}
	if !pricingAccountEligible(account, 58, "gemini", false) || pricingAccountEligible(account, 58, "openai", false) || !pricingMixedModelEligible("gemini", "antigravity", "gemini-3-pro-image-preview") || pricingMixedModelEligible("gemini", "antigravity", "claude-sonnet-4") {
		t.Fatal("mixed-scheduling models crossed platform boundaries")
	}
	for _, test := range []struct {
		patterns []string
		model    string
		want     bool
	}{
		{[]string{"GPT-IMAGE-*"}, "gpt-image-2.5", true},
		{[]string{"gpt-*-image*"}, "gpt-4-image-preview", true},
		{[]string{"gemini-*"}, "models/gemini-3-pro-image-preview", true},
		{[]string{"claude-sonnet-4"}, "claude-sonnet-4-thinking", true},
		{[]string{"gpt-image-*"}, "gpt-vision-chat", false},
		{nil, "gpt-image-1", false},
	} {
		if got := pricingAllowlistAllows(test.patterns, test.model); got != test.want {
			t.Errorf("allowlist %v/%s = %v, want %v", test.patterns, test.model, got, test.want)
		}
	}
}

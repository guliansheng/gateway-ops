package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

const publicModelPricingTTL = 10 * time.Minute

type PublicModelPricing struct {
	StationID   uint                      `json:"station_id"`
	StationName string                    `json:"station_name"`
	UpdatedAt   time.Time                 `json:"updated_at"`
	Items       []PublicModelPricingItem  `json:"items"`
	Summary     PublicModelPricingSummary `json:"summary"`
}

type PublicModelPricingSummary struct {
	Models        int      `json:"models"`
	Companies     int      `json:"companies"`
	Channels      int      `json:"channels"`
	CompaniesList []string `json:"companies_list"`
	Platforms     []string `json:"platforms"`
	BillingModes  []string `json:"billing_modes"`
}

type PublicModelPricingItem struct {
	Model              string   `json:"model"`
	BillingModel       string   `json:"billing_model,omitempty"`
	Company            string   `json:"company"`
	Provider           string   `json:"provider,omitempty"`
	Platform           string   `json:"platform,omitempty"`
	BillingMode        string   `json:"billing_mode,omitempty"`
	BillingModelSource string   `json:"billing_model_source,omitempty"`
	PriceSource        string   `json:"price_source"`
	ChannelID          int64    `json:"channel_id"`
	ChannelName        string   `json:"channel_name"`
	StationID          uint     `json:"station_id"`
	StationName        string   `json:"station_name"`
	Groups             []string `json:"groups,omitempty"`
	InputPrice         *float64 `json:"input_price,omitempty"`
	OutputPrice        *float64 `json:"output_price,omitempty"`
	CacheWritePrice    *float64 `json:"cache_write_price,omitempty"`
	CacheReadPrice     *float64 `json:"cache_read_price,omitempty"`
	ImageInputPrice    *float64 `json:"image_input_price,omitempty"`
	ImageOutputPrice   *float64 `json:"image_output_price,omitempty"`
	PerRequestPrice    *float64 `json:"per_request_price,omitempty"`
	ImagePrice1K       *float64 `json:"image_price_1k,omitempty"`
	ImagePrice2K       *float64 `json:"image_price_2k,omitempty"`
	ImagePrice4K       *float64 `json:"image_price_4k,omitempty"`
	FastMultiplier     *float64 `json:"fast_multiplier,omitempty"`
	FlexMultiplier     *float64 `json:"flex_multiplier,omitempty"`
	Intervals          []any    `json:"intervals,omitempty"`
	TimePricing        any      `json:"time_pricing,omitempty"`
}

type publicModelPricingCacheEntry struct {
	ExpiresAt time.Time
	View      PublicModelPricing
}

type remoteDefaultModelPricing struct {
	Found            bool     `json:"found"`
	Provider         string   `json:"provider"`
	LiteLLMProvider  string   `json:"litellm_provider"`
	BillingMode      string   `json:"billing_mode"`
	InputPrice       *float64 `json:"input_price"`
	OutputPrice      *float64 `json:"output_price"`
	CacheWritePrice  *float64 `json:"cache_write_price"`
	CacheReadPrice   *float64 `json:"cache_read_price"`
	ImageInputPrice  *float64 `json:"image_input_price"`
	ImageOutputPrice *float64 `json:"image_output_price"`
	PerRequestPrice  *float64 `json:"per_request_price"`
}

type pricingChannelBuild struct {
	channel  storage.RelayChannel
	snapshot channelPricingSnapshot
	groups   map[string][]pricingGroupAvailability
}

type pricingGroupAvailability struct {
	group  storage.RelayGroup
	models map[string]struct{}
}

func (s *Service) PublicModelPricing(ctx context.Context, stationID uint) (PublicModelPricing, error) {
	s.publicPricingMu.Lock()
	cached, ok := s.publicPricingCache[stationID]
	if ok && !cached.ExpiresAt.IsZero() && time.Now().Before(cached.ExpiresAt) {
		s.publicPricingMu.Unlock()
		return cached.View, nil
	}
	s.publicPricingMu.Unlock()

	view, err := s.buildPublicModelPricing(ctx, stationID)
	if err != nil {
		if ok && !cached.View.UpdatedAt.IsZero() {
			return cached.View, nil
		}
		return PublicModelPricing{}, err
	}
	s.publicPricingMu.Lock()
	s.publicPricingCache[stationID] = publicModelPricingCacheEntry{ExpiresAt: time.Now().Add(publicModelPricingTTL), View: view}
	s.publicPricingMu.Unlock()
	return view, nil
}

func (s *Service) invalidatePublicModelPricing(stationID uint) {
	s.publicPricingMu.Lock()
	delete(s.publicPricingCache, stationID)
	s.publicPricingMu.Unlock()
}

func (s *Service) buildPublicModelPricing(ctx context.Context, stationID uint) (PublicModelPricing, error) {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return PublicModelPricing{}, err
	}
	items, err := s.buildStationModelPricing(ctx, *station)
	if err != nil {
		return PublicModelPricing{}, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Company != items[j].Company {
			return items[i].Company < items[j].Company
		}
		if items[i].Model != items[j].Model {
			return items[i].Model < items[j].Model
		}
		return items[i].ChannelName < items[j].ChannelName
	})
	models, companies, channels := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	platforms, billingModes := map[string]struct{}{}, map[string]struct{}{}
	for _, item := range items {
		models[item.Model] = struct{}{}
		companies[item.Company] = struct{}{}
		if item.ChannelID > 0 {
			channels[fmt.Sprintf("%d", item.ChannelID)] = struct{}{}
		}
		if item.Platform != "" {
			platforms[item.Platform] = struct{}{}
		}
		if item.BillingMode != "" {
			billingModes[item.BillingMode] = struct{}{}
		}
	}
	return PublicModelPricing{StationID: station.ID, StationName: station.Name, UpdatedAt: time.Now().UTC(), Items: items, Summary: PublicModelPricingSummary{
		Models: len(models), Companies: len(companies), Channels: len(channels),
		CompaniesList: sortedKeys(companies), Platforms: sortedKeys(platforms), BillingModes: sortedKeys(billingModes),
	}}, nil
}

func (s *Service) buildStationModelPricing(ctx context.Context, station storage.RelayStation) ([]PublicModelPricingItem, error) {
	channels, err := s.stations.ListChannels(station.ID)
	if err != nil {
		return nil, err
	}
	groups, err := s.stations.ListGroups(station.ID)
	if err != nil {
		return nil, err
	}
	links, err := s.stations.ListLinks(station.ID)
	if err != nil {
		return nil, err
	}
	availableGroupModels, err := s.fetchAvailableGroupModels(ctx, groups)
	if err != nil {
		return nil, err
	}
	groupByID := make(map[uint]storage.RelayGroup, len(groups))
	for _, group := range groups {
		groupByID[group.ID] = group
	}
	activeChannelIDs := make(map[uint]struct{}, len(channels))
	for _, channel := range channels {
		if activeStatus(channel.Status) {
			activeChannelIDs[channel.ID] = struct{}{}
		}
	}
	linkedGroupIDs := map[uint]struct{}{}
	for _, link := range links {
		if _, ok := activeChannelIDs[link.RelayChannelID]; ok {
			linkedGroupIDs[link.RelayGroupID] = struct{}{}
		}
	}
	defaultGroups := map[string][]pricingGroupAvailability{}
	for _, group := range groups {
		if !activeStatus(group.Status) {
			continue
		}
		if _, linked := linkedGroupIDs[group.ID]; linked {
			continue
		}
		platform := normalizePlatform(group.Platform)
		models := availableGroupModels[group.ID]
		if platform != "" && platform != "composite" && len(models) > 0 {
			defaultGroups[platform] = append(defaultGroups[platform], pricingGroupAvailability{group: group, models: models})
		}
	}
	channelGroups := make(map[uint]map[string][]pricingGroupAvailability)
	for _, link := range links {
		group, ok := groupByID[link.RelayGroupID]
		if !ok || !activeStatus(group.Status) {
			continue
		}
		platform := normalizePlatform(group.Platform)
		models := availableGroupModels[group.ID]
		if platform == "" || len(models) == 0 {
			continue
		}
		if channelGroups[link.RelayChannelID] == nil {
			channelGroups[link.RelayChannelID] = map[string][]pricingGroupAvailability{}
		}
		channelGroups[link.RelayChannelID][platform] = append(channelGroups[link.RelayChannelID][platform], pricingGroupAvailability{group: group, models: models})
	}
	builds := make([]pricingChannelBuild, 0, len(channels))
	for _, channel := range channels {
		if !activeStatus(channel.Status) {
			continue
		}
		var snapshot channelPricingSnapshot
		if strings.TrimSpace(channel.PricingJSON) != "" {
			_ = json.Unmarshal([]byte(channel.PricingJSON), &snapshot)
		}
		builds = append(builds, pricingChannelBuild{channel: channel, snapshot: snapshot, groups: channelGroups[channel.ID]})
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt admin API key: %w", err)
	}
	lookupModels := map[string]struct{}{}
	for _, groupAvailability := range defaultGroups {
		for model := range pricingModelsForGroups(groupAvailability) {
			lookupModels[model] = struct{}{}
		}
	}
	for _, build := range builds {
		for platform, groupAvailability := range build.groups {
			for model := range pricingModelsForGroups(groupAvailability) {
				billingModel := pricingLookupModel(build.snapshot.BillingModelSource, platform, model, build.snapshot.ModelMapping)
				if billingModel != "" {
					lookupModels[billingModel] = struct{}{}
				}
			}
		}
	}
	defaults := s.fetchDefaultModelPricing(ctx, station.BaseURL, apiKey, sortedKeys(lookupModels))
	result := make([]PublicModelPricingItem, 0)
	for platform, groupAvailability := range defaultGroups {
		for _, model := range sortedKeys(pricingModelsForGroups(groupAvailability)) {
			imageGroups, regularGroups := splitGroupImagePricing(groupAvailability, model)
			for _, availability := range imageGroups {
				result = append(result, groupImagePricingItem(station, platform, model, availability.group, 0, availability.group.Name))
			}
			if len(regularGroups) == 0 {
				continue
			}
			groups := pricingGroupNamesForModel(regularGroups, model)
			price := defaults[model]
			provider := strings.TrimSpace(price.Provider)
			if provider == "" {
				provider = strings.TrimSpace(price.LiteLLMProvider)
			}
			mode := strings.TrimSpace(price.BillingMode)
			if mode == "" {
				mode = "token"
			}
			source := "system_default"
			if !price.Found && price.InputPrice == nil && price.OutputPrice == nil && price.PerRequestPrice == nil {
				source = "unavailable"
			}
			result = append(result, PublicModelPricingItem{Model: model, BillingModel: model, Company: modelCompany(model, provider), Provider: provider, Platform: platform, BillingMode: mode, BillingModelSource: "requested", PriceSource: source, ChannelID: 0, ChannelName: "系统默认", StationID: station.ID, StationName: station.Name, Groups: groups, InputPrice: price.InputPrice, OutputPrice: price.OutputPrice, CacheWritePrice: price.CacheWritePrice, CacheReadPrice: price.CacheReadPrice, ImageInputPrice: price.ImageInputPrice, ImageOutputPrice: price.ImageOutputPrice, PerRequestPrice: price.PerRequestPrice})
		}
	}
	for _, build := range builds {
		manualByPlatform := manualPricingIndex(build.snapshot.ModelPricing)
		for platform, groupAvailability := range build.groups {
			for _, model := range sortedKeys(pricingModelsForGroups(groupAvailability)) {
				imageGroups, regularGroups := splitGroupImagePricing(groupAvailability, model)
				for _, availability := range imageGroups {
					name := build.channel.Name + " · " + availability.group.Name
					result = append(result, groupImagePricingItem(station, platform, model, availability.group, build.channel.ExternalID, name))
				}
				if len(regularGroups) == 0 {
					continue
				}
				groups := pricingGroupNamesForModel(regularGroups, model)
				billingModel := pricingLookupModel(build.snapshot.BillingModelSource, platform, model, build.snapshot.ModelMapping)
				defaultPrice := defaults[billingModel]
				manual := manualPricingLookup(manualByPlatform[platform], billingModel, model)
				price, source := mergeEffectivePricing(defaultPrice, manual)
				provider := strings.TrimSpace(defaultPrice.Provider)
				if provider == "" {
					provider = strings.TrimSpace(defaultPrice.LiteLLMProvider)
				}
				mode := price.BillingMode
				if mode == "" {
					mode = defaultPrice.BillingMode
				}
				if mode == "" {
					mode = "token"
				}
				result = append(result, PublicModelPricingItem{Model: model, BillingModel: billingModel, Company: modelCompany(model, provider), Provider: provider, Platform: platform, BillingMode: mode, BillingModelSource: build.snapshot.BillingModelSource, PriceSource: source, ChannelID: build.channel.ExternalID, ChannelName: build.channel.Name, StationID: station.ID, StationName: station.Name, Groups: groups, InputPrice: price.InputPrice, OutputPrice: price.OutputPrice, CacheWritePrice: price.CacheWritePrice, CacheReadPrice: price.CacheReadPrice, ImageInputPrice: price.ImageInputPrice, ImageOutputPrice: price.ImageOutputPrice, PerRequestPrice: price.PerRequestPrice, FastMultiplier: price.FastMultiplier, FlexMultiplier: price.FlexMultiplier, Intervals: price.Intervals, TimePricing: price.TimePricing})
			}
		}
	}
	return result, nil
}

func (s *Service) fetchAvailableGroupModels(ctx context.Context, groups []storage.RelayGroup) (map[uint]map[string]struct{}, error) {
	type result struct {
		groupID uint
		models  []string
		err     error
	}
	activeGroups := make([]storage.RelayGroup, 0, len(groups))
	for _, group := range groups {
		if activeStatus(group.Status) {
			activeGroups = append(activeGroups, group)
		}
	}
	if len(activeGroups) == 0 {
		return map[uint]map[string]struct{}{}, nil
	}

	jobs := make(chan storage.RelayGroup)
	results := make(chan result, len(activeGroups))
	workers := 6
	if len(activeGroups) < workers {
		workers = len(activeGroups)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for group := range jobs {
				models, modelErr := s.GroupModels(ctx, group.RelayStationID, group.ExternalID)
				results <- result{groupID: group.ID, models: models, err: modelErr}
			}
		}()
	}
	go func() {
		defer close(results)
		for _, group := range activeGroups {
			select {
			case jobs <- group:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return
			}
		}
		close(jobs)
		wg.Wait()
	}()

	available := make(map[uint]map[string]struct{}, len(activeGroups))
	var firstErr error
	succeeded := 0
	for item := range results {
		if item.err != nil {
			if firstErr == nil {
				firstErr = item.err
			}
			continue
		}
		succeeded++
		models := make(map[string]struct{}, len(item.models))
		for _, model := range item.models {
			if model = strings.TrimSpace(model); model != "" {
				models[model] = struct{}{}
			}
		}
		if len(models) > 0 {
			available[item.groupID] = models
		}
	}
	if succeeded == 0 {
		if firstErr != nil {
			return nil, fmt.Errorf("读取中转站当前可用模型失败: %w", firstErr)
		}
		return nil, fmt.Errorf("读取中转站当前可用模型失败")
	}
	return available, nil
}

func pricingModelsForGroups(groups []pricingGroupAvailability) map[string]struct{} {
	models := map[string]struct{}{}
	for _, group := range groups {
		for model := range group.models {
			models[model] = struct{}{}
		}
	}
	return models
}

func pricingGroupNamesForModel(groups []pricingGroupAvailability, model string) []string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		if _, ok := group.models[model]; ok {
			names = append(names, group.group.Name)
		}
	}
	names = uniqueStrings(names)
	sort.Strings(names)
	return names
}

func splitGroupImagePricing(groups []pricingGroupAvailability, model string) (priced, regular []pricingGroupAvailability) {
	for _, availability := range groups {
		if _, ok := availability.models[model]; !ok {
			continue
		}
		if imageGenerationModel(model) && hasIndependentGroupImagePricing(availability.group) {
			priced = append(priced, availability)
		} else {
			regular = append(regular, availability)
		}
	}
	return priced, regular
}

func hasIndependentGroupImagePricing(group storage.RelayGroup) bool {
	return group.AllowImageGeneration && group.ImageRateIndependent && (group.ImagePrice1K != nil || group.ImagePrice2K != nil || group.ImagePrice4K != nil)
}

func imageGenerationModel(model string) bool {
	value := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(value, "grok-imagine") || strings.Contains(value, "gpt-image") || strings.Contains(value, "dall-e") || strings.Contains(value, "imagen") || strings.Contains(value, "seedream") || strings.Contains(value, "image-generation") || strings.HasPrefix(value, "flux-")
}

func groupImagePricingItem(station storage.RelayStation, platform, model string, group storage.RelayGroup, channelID int64, channelName string) PublicModelPricingItem {
	return PublicModelPricingItem{
		Model: model, BillingModel: model, Company: modelCompany(model, ""), Platform: platform,
		BillingMode: "per_request", BillingModelSource: "group_image_pricing", PriceSource: "group_image_pricing",
		ChannelID: channelID, ChannelName: channelName, StationID: station.ID, StationName: station.Name,
		Groups: []string{group.Name}, ImagePrice1K: group.ImagePrice1K, ImagePrice2K: group.ImagePrice2K, ImagePrice4K: group.ImagePrice4K,
	}
}

func (s *Service) fetchDefaultModelPricing(ctx context.Context, baseURL, apiKey string, models []string) map[string]remoteDefaultModelPricing {
	type result struct {
		model   string
		pricing remoteDefaultModelPricing
	}
	jobs, results := make(chan string), make(chan result)
	workers := 12
	if len(models) < workers {
		workers = len(models)
	}
	if workers == 0 {
		return map[string]remoteDefaultModelPricing{}
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for model := range jobs {
				var pricing remoteDefaultModelPricing
				endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/admin/channels/model-pricing?model=" + url.QueryEscape(model)
				if s.get(ctx, endpoint, apiKey, &pricing) == nil {
					results <- result{model: model, pricing: pricing}
				}
			}
		}()
	}
	go func() {
		defer close(results)
		for _, model := range models {
			select {
			case jobs <- model:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return
			}
		}
		close(jobs)
		wg.Wait()
	}()
	out := make(map[string]remoteDefaultModelPricing, len(models))
	for item := range results {
		out[item.model] = item.pricing
	}
	return out
}

func manualPricingIndex(items []remoteModelPricing) map[string]map[string]*remoteModelPricing {
	out := map[string]map[string]*remoteModelPricing{}
	for i := range items {
		platform := normalizePlatform(items[i].Platform)
		if platform == "" {
			continue
		}
		if out[platform] == nil {
			out[platform] = map[string]*remoteModelPricing{}
		}
		for _, model := range items[i].Models {
			model = strings.TrimSpace(model)
			if model != "" {
				out[platform][model] = &items[i]
			}
		}
	}
	return out
}

func manualPricingLookup(index map[string]*remoteModelPricing, names ...string) *remoteModelPricing {
	for _, name := range names {
		if item := index[name]; item != nil {
			return item
		}
		for candidate, item := range index {
			if strings.EqualFold(candidate, name) {
				return item
			}
		}
	}
	return nil
}

func mergeEffectivePricing(defaultPrice remoteDefaultModelPricing, manual *remoteModelPricing) (remoteModelPricing, string) {
	price := remoteModelPricing{BillingMode: defaultPrice.BillingMode, InputPrice: defaultPrice.InputPrice, OutputPrice: defaultPrice.OutputPrice, CacheWritePrice: defaultPrice.CacheWritePrice, CacheReadPrice: defaultPrice.CacheReadPrice, ImageInputPrice: defaultPrice.ImageInputPrice, ImageOutputPrice: defaultPrice.ImageOutputPrice, PerRequestPrice: defaultPrice.PerRequestPrice}
	if manual == nil {
		if defaultPrice.Found || hasAnyPrice(price) {
			return price, "system_default"
		}
		return price, "unavailable"
	}
	fallback := false
	apply := func(target **float64, value *float64) {
		if value != nil {
			*target = value
		} else if *target != nil {
			fallback = true
		}
	}
	apply(&price.InputPrice, manual.InputPrice)
	apply(&price.OutputPrice, manual.OutputPrice)
	apply(&price.CacheWritePrice, manual.CacheWritePrice)
	apply(&price.CacheReadPrice, manual.CacheReadPrice)
	apply(&price.ImageInputPrice, manual.ImageInputPrice)
	apply(&price.ImageOutputPrice, manual.ImageOutputPrice)
	apply(&price.PerRequestPrice, manual.PerRequestPrice)
	if manual.BillingMode != "" {
		price.BillingMode = manual.BillingMode
	}
	price.FastMultiplier = manual.FastMultiplier
	price.FlexMultiplier = manual.FlexMultiplier
	price.Intervals = manual.Intervals
	price.TimePricing = manual.TimePricing
	if fallback {
		return price, "channel_override_with_default"
	}
	return price, "channel_override"
}

func hasAnyPrice(item remoteModelPricing) bool {
	return item.InputPrice != nil || item.OutputPrice != nil || item.CacheWritePrice != nil || item.CacheReadPrice != nil || item.ImageInputPrice != nil || item.ImageOutputPrice != nil || item.PerRequestPrice != nil
}

func pricingLookupModel(source, platform, requested string, mappings map[string]map[string]string) string {
	requested = strings.TrimSpace(requested)
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "channel_mapped" {
		if mapped := strings.TrimSpace(mappings[normalizePlatform(platform)][requested]); mapped != "" {
			return mapped
		}
	}
	return requested
}

func normalizePlatform(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func activeStatus(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" || value == "active" || value == "enabled"
}
func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
func sortedKeys[T any](values map[string]T) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func modelCompany(model, provider string) string {
	value := strings.ToLower(strings.TrimSpace(model))
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch {
	case strings.Contains(provider, "anthropic") || strings.Contains(value, "claude"):
		return "Anthropic"
	case strings.Contains(provider, "deepseek") || strings.Contains(value, "deepseek"):
		return "DeepSeek"
	case strings.Contains(provider, "gemini") || strings.Contains(provider, "vertex") || strings.Contains(value, "gemini") || strings.HasPrefix(value, "veo-"):
		return "Google"
	case strings.Contains(provider, "xai") || strings.Contains(provider, "grok") || strings.Contains(value, "grok"):
		return "xAI"
	case strings.Contains(value, "qwen") || strings.Contains(value, "qwq") || strings.Contains(value, "tongyi"):
		return "阿里云"
	case strings.Contains(value, "glm") || strings.Contains(value, "z-ai") || strings.Contains(provider, "zhipu"):
		return "智谱 AI"
	case strings.Contains(value, "kimi") || strings.Contains(value, "moonshot") || strings.Contains(provider, "kimi"):
		return "月之暗面"
	case strings.Contains(value, "minimax") || strings.Contains(value, "abab"):
		return "MiniMax"
	case strings.Contains(value, "mimo"):
		return "小米"
	case strings.Contains(value, "doubao") || strings.Contains(value, "seedream") || strings.Contains(value, "seedance"):
		return "字节跳动"
	case strings.Contains(provider, "bytedance") || strings.Contains(provider, "volcengine"):
		return "字节跳动"
	case strings.Contains(value, "hunyuan") || strings.Contains(provider, "tencent"):
		return "腾讯"
	case strings.Contains(value, "ernie") || strings.Contains(value, "wenxin") || strings.Contains(provider, "baidu"):
		return "百度"
	case strings.Contains(value, "pangu") || strings.Contains(value, "pan-gu") || strings.Contains(provider, "huawei"):
		return "华为"
	case strings.Contains(value, "baichuan") || strings.Contains(provider, "baichuan"):
		return "百川智能"
	case strings.Contains(value, "step-") || strings.Contains(provider, "stepfun"):
		return "阶跃星辰"
	case strings.HasPrefix(value, "yi-") || strings.Contains(value, "/yi-") || strings.Contains(provider, "zeroone"):
		return "零一万物"
	case strings.Contains(value, "sparkdesk") || strings.Contains(provider, "iflytek"):
		return "科大讯飞"
	case strings.Contains(value, "sensenova") || strings.Contains(provider, "sensetime"):
		return "商汤"
	case strings.Contains(value, "mistral") || strings.Contains(value, "codestral") || strings.Contains(value, "pixtral"):
		return "Mistral AI"
	case strings.Contains(value, "llama") || strings.Contains(provider, "meta"):
		return "Meta"
	case strings.Contains(value, "command-") || strings.Contains(provider, "cohere"):
		return "Cohere"
	case strings.Contains(value, "nova-") || strings.Contains(provider, "bedrock"):
		return "Amazon"
	case strings.Contains(value, "phi-") || strings.Contains(value, "wizardlm") || strings.Contains(provider, "microsoft"):
		return "Microsoft"
	case strings.Contains(value, "nemotron") || strings.Contains(provider, "nvidia"):
		return "NVIDIA"
	case strings.Contains(provider, "openai") || strings.HasPrefix(value, "gpt-") || strings.HasPrefix(value, "o1") || strings.HasPrefix(value, "o3") || strings.HasPrefix(value, "o4") || strings.Contains(value, "codex"):
		return "OpenAI"
	default:
		return "其他"
	}
}

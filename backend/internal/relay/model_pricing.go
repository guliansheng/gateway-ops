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

type PublicPricingGroup struct {
	Name           string  `json:"name"`
	RateMultiplier float64 `json:"rate_multiplier"`
}

type PublicModelPricing struct {
	StationID    uint                      `json:"station_id"`
	StationName  string                    `json:"station_name"`
	Currency     string                    `json:"currency"`
	UpdatedAt    time.Time                 `json:"updated_at"`
	Items        []PublicModelPricingItem  `json:"items"`
	PublicGroups []PublicPricingGroup      `json:"public_groups"`
	Warnings     []string                  `json:"warnings,omitempty"`
	Summary      PublicModelPricingSummary `json:"summary"`
}

type PublicModelPricingSummary struct {
	Models        int      `json:"models"`
	Companies     int      `json:"companies"`
	Channels      int      `json:"channels"`
	CompaniesList []string `json:"companies_list"`
	Platforms     []string `json:"platforms"`
	BillingModes  []string `json:"billing_modes"`
}

type PublicModelPricingValues struct {
	BillingMode      string   `json:"billing_mode,omitempty"`
	InputPrice       *float64 `json:"input_price,omitempty"`
	OutputPrice      *float64 `json:"output_price,omitempty"`
	CacheWritePrice  *float64 `json:"cache_write_price,omitempty"`
	CacheReadPrice   *float64 `json:"cache_read_price,omitempty"`
	ImageInputPrice  *float64 `json:"image_input_price,omitempty"`
	ImageOutputPrice *float64 `json:"image_output_price,omitempty"`
	PerRequestPrice  *float64 `json:"per_request_price,omitempty"`
	ImagePrice1K     *float64 `json:"image_price_1k,omitempty"`
	ImagePrice2K     *float64 `json:"image_price_2k,omitempty"`
	ImagePrice4K     *float64 `json:"image_price_4k,omitempty"`
}

type PublicModelPricingItem struct {
	Model                 string                    `json:"model"`
	BillingModel          string                    `json:"billing_model,omitempty"`
	Company               string                    `json:"company"`
	Provider              string                    `json:"provider,omitempty"`
	Platform              string                    `json:"platform,omitempty"`
	BillingMode           string                    `json:"billing_mode,omitempty"`
	BillingModelSource    string                    `json:"billing_model_source,omitempty"`
	PriceSource           string                    `json:"price_source"`
	OfficialPricing       *PublicModelPricingValues `json:"official_pricing,omitempty"`
	OfficialPricingSource string                    `json:"official_pricing_source,omitempty"`
	ChannelID             int64                     `json:"channel_id"`
	ChannelName           string                    `json:"channel_name"`
	StationID             uint                      `json:"station_id"`
	StationName           string                    `json:"station_name"`
	Groups                []string                  `json:"groups,omitempty"`
	InputPrice            *float64                  `json:"input_price,omitempty"`
	OutputPrice           *float64                  `json:"output_price,omitempty"`
	CacheWritePrice       *float64                  `json:"cache_write_price,omitempty"`
	CacheReadPrice        *float64                  `json:"cache_read_price,omitempty"`
	ImageInputPrice       *float64                  `json:"image_input_price,omitempty"`
	ImageOutputPrice      *float64                  `json:"image_output_price,omitempty"`
	PerRequestPrice       *float64                  `json:"per_request_price,omitempty"`
	ImagePrice1K          *float64                  `json:"image_price_1k,omitempty"`
	ImagePrice2K          *float64                  `json:"image_price_2k,omitempty"`
	ImagePrice4K          *float64                  `json:"image_price_4k,omitempty"`
	FastMultiplier        *float64                  `json:"fast_multiplier,omitempty"`
	FlexMultiplier        *float64                  `json:"flex_multiplier,omitempty"`
	Intervals             []any                     `json:"intervals,omitempty"`
	TimePricing           any                       `json:"time_pricing,omitempty"`
}

type publicModelPricingCacheEntry struct {
	ExpiresAt time.Time
	View      PublicModelPricing
}

type remoteDefaultModelPricing struct {
	Found            bool     `json:"found"`
	Source           string   `json:"-"`
	Provider         string   `json:"provider"`
	LiteLLMProvider  string   `json:"litellm_provider"`
	BillingMode      string   `json:"billing_mode"`
	Mode             string   `json:"mode"`
	InputPrice       *float64 `json:"input_price"`
	OutputPrice      *float64 `json:"output_price"`
	CacheWritePrice  *float64 `json:"cache_write_price"`
	CacheReadPrice   *float64 `json:"cache_read_price"`
	ImageInputPrice  *float64 `json:"image_input_price"`
	ImageOutputPrice *float64 `json:"image_output_price"`
	PerRequestPrice  *float64 `json:"per_request_price"`
}

func officialModelPricing(price remoteDefaultModelPricing) *PublicModelPricingValues {
	values := PublicModelPricingValues{
		BillingMode: price.BillingMode, InputPrice: price.InputPrice, OutputPrice: price.OutputPrice,
		CacheWritePrice: price.CacheWritePrice, CacheReadPrice: price.CacheReadPrice,
		ImageInputPrice: price.ImageInputPrice, ImageOutputPrice: price.ImageOutputPrice,
		PerRequestPrice: price.PerRequestPrice,
	}
	if !hasAnyPrice(remoteModelPricing{
		InputPrice: values.InputPrice, OutputPrice: values.OutputPrice,
		CacheWritePrice: values.CacheWritePrice, CacheReadPrice: values.CacheReadPrice,
		ImageInputPrice: values.ImageInputPrice, ImageOutputPrice: values.ImageOutputPrice,
		PerRequestPrice: values.PerRequestPrice,
	}) {
		return nil
	}
	return &values
}

// Sub2API stores channel model_pricing in USD/token, not the USD/MTok admin form unit.
// See upstream frontend/src/components/admin/channel/types.ts: mTokToPerToken.
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
	ttl := publicModelPricingTTL
	if len(view.Warnings) > 0 {
		ttl = 30 * time.Second
	}
	s.publicPricingMu.Lock()
	s.publicPricingCache[stationID] = publicModelPricingCacheEntry{ExpiresAt: time.Now().Add(ttl), View: view}
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
	var warnings []string
	items, err := s.buildStationModelPricing(ctx, *station, &warnings)
	if err != nil {
		return PublicModelPricing{}, err
	}
	groups, err := s.stations.ListGroups(station.ID)
	if err != nil {
		return PublicModelPricing{}, err
	}
	publicGroups := make([]PublicPricingGroup, 0, len(groups))
	for _, group := range publicPricingGroups(groups) {
		if name := strings.TrimSpace(group.Name); name != "" {
			publicGroups = append(publicGroups, PublicPricingGroup{Name: name, RateMultiplier: group.RateMultiplier})
		}
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
	return PublicModelPricing{StationID: station.ID, StationName: station.Name, Currency: "USD", UpdatedAt: time.Now().UTC(), Items: items, PublicGroups: publicGroups, Warnings: warnings, Summary: PublicModelPricingSummary{
		Models: len(models), Companies: len(companies), Channels: len(channels),
		CompaniesList: sortedKeys(companies), Platforms: sortedKeys(platforms), BillingModes: sortedKeys(billingModes),
	}}, nil
}

// Public pricing includes only active, non-exclusive groups with local pricing enabled.
func publicPricingGroups(groups []storage.RelayGroup) []storage.RelayGroup {
	result := make([]storage.RelayGroup, 0, len(groups))
	for _, group := range groups {
		if !group.IsExclusive && activeStatus(group.Status) && group.ModelPricingEnabled {
			result = append(result, group)
		}
	}
	return result
}

func (s *Service) buildStationModelPricing(ctx context.Context, station storage.RelayStation, warningOutputs ...*[]string) ([]PublicModelPricingItem, error) {
	channels, err := s.stations.ListChannels(station.ID)
	if err != nil {
		return nil, err
	}
	groups, err := s.stations.ListGroups(station.ID)
	if err != nil {
		return nil, err
	}
	groups = publicPricingGroups(groups)
	if len(groups) == 0 {
		return []PublicModelPricingItem{}, nil
	}
	links, err := s.stations.ListLinks(station.ID)
	if err != nil {
		return nil, err
	}
	availableGroupModels, err := s.fetchAvailableGroupModels(ctx, groups, warningOutputs...)
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
		if len(models) > 0 {
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
		if len(models) == 0 {
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
	includeImageModes := false
	for _, group := range groups {
		includeImageModes = includeImageModes || hasIndependentGroupImagePricing(group)
	}
	for _, groupAvailability := range defaultGroups {
		for model := range pricingModelsForGroups(groupAvailability) {
			lookupModels[model] = struct{}{}
		}
	}
	for _, build := range builds {
		for platform, groupAvailability := range build.groups {
			for model := range pricingModelsForGroups(groupAvailability) {
				lookupModels[model] = struct{}{}
				billingModel := pricingLookupModel(build.snapshot.BillingModelSource, platform, model, build.snapshot.ModelMapping)
				if billingModel != "" {
					lookupModels[billingModel] = struct{}{}
				}
				// Mapping is also capability evidence for public aliases, regardless of billing source.
				lookupModels[pricingMappedModel(platform, model, build.snapshot.ModelMapping)] = struct{}{}
			}
		}
	}
	defaults := s.fetchDefaultModelPricing(ctx, station.BaseURL, apiKey, sortedKeys(lookupModels), includeImageModes)
	return stationModelPricingItems(station, defaultGroups, builds, defaults), nil
}

func stationModelPricingItems(station storage.RelayStation, defaultGroups map[string][]pricingGroupAvailability, builds []pricingChannelBuild, defaults map[string]remoteDefaultModelPricing) []PublicModelPricingItem {
	result := make([]PublicModelPricingItem, 0)
	for platform, groupAvailability := range defaultGroups {
		for _, model := range sortedKeys(pricingModelsForGroups(groupAvailability)) {
			imageGroups, regularGroups := splitGroupImagePricing(groupAvailability, model, modelUsesGroupImagePricing(model, model, defaults, nil))
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
			if mode != "token" {
				continue // Public request prices are exclusively independent group image prices.
			}
			source := "system_default"
			if officialModelPricing(price) == nil {
				source = "unavailable"
			}
			result = append(result, PublicModelPricingItem{Model: model, BillingModel: model, Company: modelCompany(model, provider), Provider: provider, Platform: platform, BillingMode: mode, BillingModelSource: "requested", PriceSource: source, OfficialPricing: officialModelPricing(price), OfficialPricingSource: pricingReferenceSource(price), ChannelID: 0, ChannelName: "系统默认", StationID: station.ID, StationName: station.Name, Groups: groups, InputPrice: price.InputPrice, OutputPrice: price.OutputPrice, CacheWritePrice: price.CacheWritePrice, CacheReadPrice: price.CacheReadPrice, ImageInputPrice: price.ImageInputPrice, ImageOutputPrice: price.ImageOutputPrice, PerRequestPrice: price.PerRequestPrice})
		}
	}
	for _, build := range builds {
		manualByPlatform := manualPricingIndex(build.snapshot.ModelPricing)
		for platform, groupAvailability := range build.groups {
			for _, model := range sortedKeys(pricingModelsForGroups(groupAvailability)) {
				billingModel := pricingLookupModel(build.snapshot.BillingModelSource, platform, model, build.snapshot.ModelMapping)
				manual := manualPlatformPricingLookup(manualByPlatform, platform, billingModel, model)
				mappedModel := pricingMappedModel(platform, model, build.snapshot.ModelMapping)
				imageGroups, regularGroups := splitGroupImagePricing(groupAvailability, model, modelUsesGroupImagePricing(model, mappedModel, defaults, manual))
				for _, availability := range imageGroups {
					name := build.channel.Name + " · " + availability.group.Name
					item := groupImagePricingItem(station, platform, model, availability.group, build.channel.ExternalID, name)
					item.BillingModel = mappedModel
					item.Company = modelCompany(mappedModel, defaults[mappedModel].Provider)
					result = append(result, item)
				}
				if len(regularGroups) == 0 {
					continue
				}
				groups := pricingGroupNamesForModel(regularGroups, model)
				defaultPrice := defaults[billingModel]
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
				if mode != "token" {
					continue
				}
				reference, referenceSource := channelPricingReference(defaultPrice, manual)
				result = append(result, PublicModelPricingItem{Model: model, BillingModel: billingModel, Company: modelCompany(model, provider), Provider: provider, Platform: platform, BillingMode: mode, BillingModelSource: build.snapshot.BillingModelSource, PriceSource: source, OfficialPricing: reference, OfficialPricingSource: referenceSource, ChannelID: build.channel.ExternalID, ChannelName: build.channel.Name, StationID: station.ID, StationName: station.Name, Groups: groups, InputPrice: price.InputPrice, OutputPrice: price.OutputPrice, CacheWritePrice: price.CacheWritePrice, CacheReadPrice: price.CacheReadPrice, ImageInputPrice: price.ImageInputPrice, ImageOutputPrice: price.ImageOutputPrice, PerRequestPrice: price.PerRequestPrice, FastMultiplier: price.FastMultiplier, FlexMultiplier: price.FlexMultiplier, Intervals: price.Intervals, TimePricing: price.TimePricing})
			}
		}
	}
	for index := range result {
		if result[index].PriceSource == "group_image_pricing" || result[index].OfficialPricing != nil {
			continue
		}
		price := defaults[result[index].Model]
		result[index].OfficialPricing = officialModelPricing(price)
		result[index].OfficialPricingSource = pricingReferenceSource(price)
	}
	return result
}

func (s *Service) fetchAvailableGroupModels(ctx context.Context, groups []storage.RelayGroup, warningOutputs ...*[]string) (map[uint]map[string]struct{}, error) {
	type result struct {
		group  storage.RelayGroup
		models []string
		err    error
	}
	activeGroups := publicPricingGroups(groups)
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
				if modelErr == ErrGroupAdminAPIKeyMissing {
					station, err := s.stations.FindByID(group.RelayStationID)
					if err == nil {
						var key string
						key, err = s.cipher.Decrypt(station.APIKeyCipher)
						if err == nil {
							models, err = s.fetchPricingGroupModelsFromAdmin(ctx, station.BaseURL, key, group)
						}
					}
					modelErr = err
				}
				results <- result{group: group, models: models, err: modelErr}
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
	var warnings []string
	succeeded := 0
	for item := range results {
		if item.err != nil {
			if firstErr == nil {
				firstErr = item.err
			}
			// Public warnings identify the affected group, never upstream credentials/errors.
			warnings = append(warnings, fmt.Sprintf("分组「%s」模型读取失败，价格列表可能不完整。", item.group.Name))
			continue
		}
		succeeded++
		models := make(map[string]struct{}, len(item.models))
		for _, model := range item.models {
			if model = strings.TrimSpace(model); model != "" && !strings.Contains(model, "*") {
				models[model] = struct{}{}
			}
		}
		if len(models) > 0 {
			available[item.group.ID] = models
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if succeeded == 0 {
		return nil, fmt.Errorf("读取中转站当前可用模型失败: %w", firstErr)
	}
	sort.Strings(warnings)
	for _, output := range warningOutputs {
		if output != nil {
			*output = append(*output, warnings...)
		}
	}
	return available, nil
}

// Price discovery is read-only and does not require a gateway key for each group.
// Do not use model-allowlist-candidates: it includes platform defaults without account support.
func (s *Service) fetchPricingGroupModelsFromAdmin(ctx context.Context, baseURL, key string, group storage.RelayGroup) ([]string, error) {
	var metadata struct {
		Platform         string `json:"platform"`
		RequireOAuthOnly bool   `json:"require_oauth_only"`
		ModelAllowlist   struct {
			Enabled bool     `json:"enabled"`
			Models  []string `json:"models"`
		} `json:"model_allowlist"`
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if err := s.get(ctx, fmt.Sprintf("%s/api/v1/admin/groups/%d", baseURL, group.ExternalID), key, &metadata); err != nil {
		return nil, err
	}
	platform := normalizePlatform(metadata.Platform)
	if platform == "" {
		platform = normalizePlatform(group.Platform)
	}
	models := map[string]struct{}{}
	for page := 1; page <= 1000; page++ {
		var accounts remoteAccountPage
		endpoint := fmt.Sprintf("%s/api/v1/admin/accounts?group=%d&status=active&page=%d&page_size=100", baseURL, group.ExternalID, page)
		if err := s.get(ctx, endpoint, key, &accounts); err != nil {
			return nil, err
		}
		for _, account := range accounts.Items {
			if !pricingAccountEligible(account, group.ExternalID, platform, metadata.RequireOAuthOnly || group.RequireOAuthOnly) {
				continue
			}
			var raw json.RawMessage
			if err := s.get(ctx, fmt.Sprintf("%s/api/v1/admin/accounts/%d/models", baseURL, account.ID), key, &raw); err != nil {
				return nil, err
			}
			for _, model := range extractAccountModelIDs(raw) {
				if strings.Contains(model, "*") || !pricingMixedModelEligible(platform, normalizePlatform(account.Platform), model) {
					continue
				}
				if metadata.ModelAllowlist.Enabled && !pricingAllowlistAllows(metadata.ModelAllowlist.Models, model) {
					continue
				}
				models[model] = struct{}{}
			}
		}
		if accounts.Pages <= page || len(accounts.Items) == 0 {
			return sortedKeys(models), nil
		}
	}
	return nil, fmt.Errorf("中转站分组账号分页异常")
}

func pricingAccountEligible(account remoteAccount, groupID int64, platform string, oauthOnly bool) bool {
	if account.ID <= 0 || !activeStatus(account.Status) || !account.Schedulable {
		return false
	}
	bound := false
	for _, id := range account.GroupIDs {
		bound = bound || id == groupID
	}
	if !bound || (oauthOnly && account.Type != "oauth" && account.Type != "setup-token") {
		return false
	}
	accountPlatform := normalizePlatform(account.Platform)
	if platform == "composite" || accountPlatform == platform {
		return true
	}
	mixed, _ := account.Extra["mixed_scheduling"].(bool)
	return accountPlatform == "antigravity" && mixed && (platform == "anthropic" || platform == "gemini")
}

func pricingMixedModelEligible(platform, accountPlatform, model string) bool {
	if platform == "composite" || platform == accountPlatform {
		return true
	}
	model = strings.ToLower(strings.TrimPrefix(model, "models/"))
	return (platform == "anthropic" && strings.HasPrefix(model, "claude-")) || (platform == "gemini" && strings.HasPrefix(model, "gemini-"))
}

func pricingAllowlistAllows(patterns []string, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	candidates := uniqueStrings([]string{model, strings.TrimPrefix(model, "models/"), strings.TrimSuffix(model, "-thinking")})
	for _, pattern := range patterns {
		parts := strings.Split(strings.ToLower(strings.TrimSpace(pattern)), "*")
		for _, candidate := range candidates {
			if len(parts) == 1 {
				if parts[0] == candidate {
					return true
				}
				continue
			}
			if !strings.HasPrefix(candidate, parts[0]) {
				continue
			}
			rest := candidate[len(parts[0]):]
			matches := true
			for _, part := range parts[1 : len(parts)-1] {
				index := strings.Index(rest, part)
				if index < 0 {
					matches = false
					break
				}
				rest = rest[index+len(part):]
			}
			if matches && strings.HasSuffix(rest, parts[len(parts)-1]) {
				return true
			}
		}
	}
	return false
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
	return uniqueStrings(names)
}

func splitGroupImagePricing(groups []pricingGroupAvailability, model string, imageIdentity ...bool) (priced, regular []pricingGroupAvailability) {
	isImage := imageGenerationModel(model)
	if len(imageIdentity) > 0 {
		isImage = imageIdentity[0]
	}
	for _, availability := range groups {
		if _, ok := availability.models[model]; !ok {
			continue
		}
		if isImage && hasIndependentGroupImagePricing(availability.group) {
			priced = append(priced, availability)
		} else {
			regular = append(regular, availability)
		}
	}
	return priced, regular
}

func hasIndependentGroupImagePricing(group storage.RelayGroup) bool {
	return group.AllowImageGeneration && group.ImageRateIndependent && (validCatalogPrice(group.ImagePrice1K) != nil || validCatalogPrice(group.ImagePrice2K) != nil || validCatalogPrice(group.ImagePrice4K) != nil)
}

func imagePricingMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "image", "image_generation", "image_edit":
		return true
	}
	return false
}

func modelUsesGroupImagePricing(model, mapped string, defaults map[string]remoteDefaultModelPricing, manual *remoteModelPricing) bool {
	for _, name := range uniqueStrings([]string{mapped, model}) {
		if mode := strings.TrimSpace(defaults[name].Mode); mode != "" {
			return imagePricingMode(mode) // Catalog chat/vision metadata must not become image generation.
		}
	}
	if imageGenerationModel(mapped) || imageGenerationModel(model) {
		return true
	}
	if manual != nil && imagePricingMode(manual.BillingMode) {
		for _, pattern := range manual.Models {
			if pricingModelMatches(pattern, model, false) || pricingModelMatches(pattern, mapped, false) {
				return true // An explicit image entry can identify an alias; a group-wide wildcard cannot.
			}
		}
	}
	return false
}

func imageGenerationModel(model string) bool {
	value := strings.ToLower(strings.TrimSpace(model))
	if index := strings.LastIndex(value, "/"); index >= 0 {
		value = value[index+1:]
	}
	// Generation families only; generic "image" substrings also occur in chat/vision models.
	for _, prefix := range []string{"gpt-image-", "chatgpt-image-", "grok-imagine-image", "qwen-image", "dall-e-", "imagen-", "flux", "seedream", "stable-diffusion", "sdxl", "midjourney", "ideogram", "recraft", "cogview", "wanx", "wenxin-image"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return strings.HasPrefix(value, "gemini-") && (strings.HasSuffix(value, "-image") || strings.Contains(value, "-image-preview"))
}

func groupImagePricingItem(station storage.RelayStation, platform, model string, group storage.RelayGroup, channelID int64, channelName string) PublicModelPricingItem {
	return PublicModelPricingItem{
		Model: model, BillingModel: model, Company: modelCompany(model, ""), Platform: platform,
		BillingMode: "per_request", BillingModelSource: "group_image_pricing", PriceSource: "group_image_pricing",
		ChannelID: channelID, ChannelName: channelName, StationID: station.ID, StationName: station.Name,
		Groups: []string{group.Name}, ImagePrice1K: validCatalogPrice(group.ImagePrice1K), ImagePrice2K: validCatalogPrice(group.ImagePrice2K), ImagePrice4K: validCatalogPrice(group.ImagePrice4K),
	}
}

func (s *Service) fetchDefaultModelPricing(ctx context.Context, baseURL, apiKey string, models []string, includeImageModes ...bool) map[string]remoteDefaultModelPricing {
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
	s.fillOfficialPricingFallback(ctx, out, models, includeImageModes...)
	return out
}

func manualPricingIndex(items []remoteModelPricing) map[string][]*remoteModelPricing {
	out := map[string][]*remoteModelPricing{}
	for i := range items {
		platform := normalizePlatform(items[i].Platform)
		if platform == "" {
			platform = "anthropic" // Sub2API's legacy channel-pricing platform default.
		}
		out[platform] = append(out[platform], &items[i])
	}
	return out
}

// Composite lookup follows Sub2API's configuration-time platform precedence.
// Upstream backend/internal/domain/platforms.go; new platform keys remain supported.
func pricingPlatforms[T any](platform string, values map[string]T) []string {
	platform = normalizePlatform(platform)
	if platform != "composite" {
		return []string{platform}
	}
	order := []string{"anthropic", "gemini", "openai", "antigravity", "grok", "kimi", "zhipu", "deepseek", "minimax", "opencode_go", "typesafe", "command_code", "cline"}
	known := make(map[string]struct{}, len(order)+1)
	known["composite"] = struct{}{}
	for _, key := range order {
		known[key] = struct{}{}
	}
	for _, key := range sortedKeys(values) {
		if _, ok := known[key]; !ok {
			order = append(order, key)
		}
	}
	return order
}

func pricingModelMatches(pattern, name string, wildcard bool) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.HasSuffix(pattern, "*") {
		return wildcard && strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	return !wildcard && pattern == name
}

func manualPlatformPricingLookup(index map[string][]*remoteModelPricing, platform string, names ...string) *remoteModelPricing {
	for _, name := range names {
		for _, wildcard := range []bool{false, true} {
			for _, key := range pricingPlatforms(platform, index) {
				for _, item := range index[key] {
					for _, pattern := range item.Models {
						if pricingModelMatches(pattern, name, wildcard) {
							return item
						}
					}
				}
			}
		}
	}
	return nil
}

func mergeEffectivePricing(defaultPrice remoteDefaultModelPricing, manual *remoteModelPricing) (remoteModelPricing, string) {
	price := remoteModelPricing{BillingMode: defaultPrice.BillingMode, InputPrice: defaultPrice.InputPrice, OutputPrice: defaultPrice.OutputPrice, CacheWritePrice: defaultPrice.CacheWritePrice, CacheReadPrice: defaultPrice.CacheReadPrice, ImageInputPrice: defaultPrice.ImageInputPrice, ImageOutputPrice: defaultPrice.ImageOutputPrice, PerRequestPrice: defaultPrice.PerRequestPrice}
	if manual == nil {
		if hasAnyPrice(price) {
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
	if !hasAnyPrice(price) {
		return price, "unavailable"
	}
	if !hasAnyPrice(*manual) {
		return price, "system_default"
	}
	if fallback {
		return price, "channel_override_with_default"
	}
	return price, "channel_override"
}

func channelPricingReference(defaultPrice remoteDefaultModelPricing, manual *remoteModelPricing) (*PublicModelPricingValues, string) {
	values := officialModelPricing(defaultPrice)
	if values == nil {
		values = &PublicModelPricingValues{}
	}
	manualHasPrice := false
	if manual != nil {
		apply := func(target **float64, value *float64) {
			if value != nil {
				*target = value
				manualHasPrice = true
			}
		}
		apply(&values.InputPrice, manual.InputPrice)
		apply(&values.OutputPrice, manual.OutputPrice)
		apply(&values.CacheWritePrice, manual.CacheWritePrice)
		apply(&values.CacheReadPrice, manual.CacheReadPrice)
		apply(&values.ImageInputPrice, manual.ImageInputPrice)
		apply(&values.ImageOutputPrice, manual.ImageOutputPrice)
		apply(&values.PerRequestPrice, manual.PerRequestPrice)
		if manual.BillingMode != "" {
			values.BillingMode = manual.BillingMode
		}
	}
	if !hasAnyPrice(remoteModelPricing{InputPrice: values.InputPrice, OutputPrice: values.OutputPrice, CacheWritePrice: values.CacheWritePrice, CacheReadPrice: values.CacheReadPrice, ImageInputPrice: values.ImageInputPrice, ImageOutputPrice: values.ImageOutputPrice, PerRequestPrice: values.PerRequestPrice}) {
		return nil, ""
	}
	if manualHasPrice {
		return values, "channel_pricing"
	}
	return values, pricingReferenceSource(defaultPrice)
}

func hasAnyPrice(item remoteModelPricing) bool {
	return item.InputPrice != nil || item.OutputPrice != nil || item.CacheWritePrice != nil || item.CacheReadPrice != nil || item.ImageInputPrice != nil || item.ImageOutputPrice != nil || item.PerRequestPrice != nil
}

func pricingLookupModel(source, platform, requested string, mappings map[string]map[string]string) string {
	if strings.EqualFold(strings.TrimSpace(source), "channel_mapped") {
		return pricingMappedModel(platform, requested, mappings)
	}
	return strings.TrimSpace(requested)
}

func pricingMappedModel(platform, requested string, mappings map[string]map[string]string) string {
	requested = strings.TrimSpace(requested)
	normalized := make(map[string]map[string]string, len(mappings))
	for key, value := range mappings {
		normalized[normalizePlatform(key)] = value
	}
	for _, wildcard := range []bool{false, true} {
		for _, key := range pricingPlatforms(platform, normalized) {
			mapping := normalized[key]
			for _, pattern := range sortedKeys(mapping) {
				if mapped := strings.TrimSpace(mapping[pattern]); mapped != "" && pricingModelMatches(pattern, requested, wildcard) {
					return mapped
				}
			}
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

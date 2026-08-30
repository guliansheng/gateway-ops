package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func registerRelayStations(g *gin.RouterGroup, d *Deps) {
	gp := g.Group("/relay-stations")
	gp.GET("", func(c *gin.Context) { listRelayStations(c, d) })
	gp.POST("", func(c *gin.Context) { createRelayStation(c, d) })
	gp.POST("/sync-all", func(c *gin.Context) { syncAllRelayStations(c, d) })
	gp.GET("/:id", func(c *gin.Context) { relayStationOverview(c, d) })
	gp.GET("/:id/usage", func(c *gin.Context) { relayStationUsage(c, d) })
	gp.GET("/:id/usage/recent", func(c *gin.Context) { relayStationRecentUsage(c, d) })
	gp.GET("/:id/users", func(c *gin.Context) { relayStationUsers(c, d) })
	gp.PUT("/:id/users/:user_id/status", func(c *gin.Context) { updateRelayStationUserStatus(c, d) })
	gp.POST("/:id/users/batch-limits", func(c *gin.Context) { updateRelayStationUserLimits(c, d) })
	gp.POST("/:id/users/batch-delete", func(c *gin.Context) { deleteRelayStationUsers(c, d) })
	gp.GET("/:id/users/:user_id/balance-history", func(c *gin.Context) { relayStationUserBalanceHistory(c, d) })
	gp.GET("/:id/accounts/:external_id/models", func(c *gin.Context) { relayStationAccountModels(c, d) })
	gp.POST("/:id/accounts/:external_id/test", func(c *gin.Context) { relayStationTestAccount(c, d) })
	gp.DELETE("/:id/accounts/:external_id", func(c *gin.Context) { deleteRelayStationAccount(c, d) })
	gp.DELETE("/:id/accounts", func(c *gin.Context) { deleteAllRelayStationAccounts(c, d) })
	gp.GET("/:id/groups/:external_id/models", func(c *gin.Context) { relayStationGroupModels(c, d) })
	gp.POST("/:id/groups/:external_id/test", func(c *gin.Context) { relayStationTestGroup(c, d) })
	gp.DELETE("/:id/groups/:external_id", func(c *gin.Context) { deleteRelayStationGroup(c, d) })
	gp.DELETE("/:id/groups", func(c *gin.Context) { deleteAllRelayStationGroups(c, d) })
	gp.PUT("/:id", func(c *gin.Context) { updateRelayStation(c, d) })
	gp.DELETE("/:id", func(c *gin.Context) { deleteRelayStation(c, d) })
	gp.POST("/:id/sync", func(c *gin.Context) { syncRelayStation(c, d) })
	gp.PUT("/:id/groups/sort-order", func(c *gin.Context) { updateRelayGroupSortOrder(c, d) })
	gp.PUT("/:id/groups/:external_id", func(c *gin.Context) { updateRelayGroup(c, d) })
	gp.PUT("/:id/channels/:external_id/mapping", func(c *gin.Context) { updateRelayMapping(c, d) })
	gp.PUT("/:id/accounts/cost-overrides", func(c *gin.Context) { updateRelayCostOverrides(c, d) })
	gp.PUT("/:id/accounts/groups", func(c *gin.Context) { updateRelayAccountGroupsBatch(c, d) })
	gp.PUT("/:id/accounts/model-types", func(c *gin.Context) { updateRelayAccountModelTypes(c, d) })
	gp.PUT("/:id/accounts/runtime-settings", func(c *gin.Context) { updateRelayAccountRuntimeSettings(c, d) })
	gp.PUT("/:id/accounts/schedulable", func(c *gin.Context) { updateRelayAccountsSchedulable(c, d) })
	gp.POST("/:id/accounts/apply-suggestions", func(c *gin.Context) { applyRelayAccountSuggestions(c, d) })
	gp.POST("/:id/accounts/add-downgrades", func(c *gin.Context) { addRelayAccountDowngrades(c, d) })
	gp.PUT("/:id/accounts/:external_id/groups", func(c *gin.Context) { updateRelayAccountGroups(c, d) })
	gp.POST("/:id/accounts/:external_id/groups", func(c *gin.Context) { addRelayAccountGroup(c, d) })
	gp.PUT("/:id/accounts/:external_id/schedulable", func(c *gin.Context) { updateRelayAccountSchedulable(c, d) })
	gp.POST("/:id/accounts/:external_id/probe", func(c *gin.Context) { probeRelayAccount(c, d) })
	gp.POST("/:id/accounts/:external_id/apply-suggestion", func(c *gin.Context) { applyRelayAccountSuggestion(c, d) })
}

func syncAllRelayStations(c *gin.Context, d *Deps) {
	synced, failed, err := d.Relay.SyncAll(c.Request.Context())
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"synced": synced, "failed": failed}})
}

type relayStationInput struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

type relayStationUpdateInput struct {
	Name                      *string `json:"name"`
	BaseURL                   *string `json:"base_url"`
	APIKey                    *string `json:"api_key"`
	AutoAdjustEnabled         *bool   `json:"auto_adjust_enabled"`
	AutoAdjustNoProfitEnabled *bool   `json:"auto_adjust_no_profit_enabled"`
	AutoPriorityEnabled       *bool   `json:"auto_priority_enabled"`
	AutoPriorityRecallEnabled *bool   `json:"auto_priority_recall_enabled"`
	AutoPriorityRecallMinutes *int    `json:"auto_priority_recall_minutes"`
}

type relayMappingInput struct {
	MonitorChannelID *uint  `json:"monitor_channel_id"`
	UpstreamGroup    string `json:"upstream_group"`
}

type relayCostOverrideInput struct {
	AccountExternalIDs []int64  `json:"account_external_ids"`
	Mode               string   `json:"mode"`
	MonitorChannelID   *uint    `json:"monitor_channel_id"`
	UpstreamGroup      string   `json:"upstream_group"`
	ManualMultiplier   *float64 `json:"manual_multiplier"`
	Clear              bool     `json:"clear"`
}

type relayAccountGroupsInput struct {
	AccountExternalIDs []int64 `json:"account_external_ids"`
	GroupExternalIDs   []int64 `json:"group_external_ids"`
}

type relayAccountAddGroupInput struct {
	GroupExternalID int64 `json:"group_external_id"`
}

type relayAccountSchedulableInput struct {
	Schedulable *bool `json:"schedulable"`
}

type relayAccountSchedulableBatchInput struct {
	AccountExternalIDs []int64 `json:"account_external_ids"`
	Schedulable        *bool   `json:"schedulable"`
}

type relayAccountActionBatchInput struct {
	AccountExternalIDs []int64 `json:"account_external_ids"`
}

type relayAccountRuntimeSettingsInput struct {
	AccountExternalIDs []int64 `json:"account_external_ids"`
	Concurrency        *int    `json:"concurrency"`
	Priority           *int    `json:"priority"`
	PoolModeRetryCount *int    `json:"pool_mode_retry_count"`
}

type relayAccountModelTypesInput struct {
	AccountExternalIDs []int64 `json:"account_external_ids"`
	ModelType          string  `json:"model_type"`
}

type relayGroupUpdateInput struct {
	Name           *string   `json:"name"`
	Description    *string   `json:"description"`
	RateMultiplier *float64  `json:"rate_multiplier"`
	IsExclusive    *bool     `json:"is_exclusive"`
	Status         *string   `json:"status"`
	ModelTypes     *[]string `json:"model_types"`
	MonitorEnabled *bool     `json:"monitor_enabled"`
}

type relayGroupSortOrderInput struct {
	Updates []relay.GroupSortOrderUpdate `json:"updates"`
}

type relayUserStatusInput struct {
	Status string `json:"status"`
}

type relayUserLimitsInput struct {
	UserIDs     []int64 `json:"user_ids"`
	Concurrency *int    `json:"concurrency"`
	RPMLimit    *int    `json:"rpm_limit"`
}

type relayUserDeleteInput struct {
	UserIDs []int64 `json:"user_ids"`
}

type relayStationSummary struct {
	ID                        uint       `json:"id"`
	Name                      string     `json:"name"`
	BaseURL                   string     `json:"base_url"`
	APIKeyConfigured          bool       `json:"api_key_configured"`
	AutoAdjustEnabled         bool       `json:"auto_adjust_enabled"`
	AutoAdjustNoProfitEnabled bool       `json:"auto_adjust_no_profit_enabled"`
	AutoPriorityEnabled       bool       `json:"auto_priority_enabled"`
	AutoPriorityRecallEnabled bool       `json:"auto_priority_recall_enabled"`
	AutoPriorityRecallMinutes int        `json:"auto_priority_recall_minutes"`
	LastSyncedAt              *time.Time `json:"last_synced_at,omitempty"`
	LastError                 string     `json:"last_error,omitempty"`
}

type relayOverview struct {
	Station             relayStationSummary    `json:"station"`
	Groups              []relayGroupView       `json:"groups"`
	Accounts            []relayAccountView     `json:"accounts"`
	Summary             relayRiskSummary       `json:"summary"`
	Adjustments         []relayAdjustmentView  `json:"adjustments"`
	MonitorChannels     []monitorChannelOption `json:"monitor_channels"`
	SyncAvailable       bool                   `json:"sync_available"`
	Range               string                 `json:"range"`
	UsageComplete       bool                   `json:"usage_complete"`
	UsageFailedAccounts int                    `json:"usage_failed_accounts"`
}

type relayUsageAccountView struct {
	ExternalID       int64   `json:"external_id"`
	UsageTotalTokens int64   `json:"usage_total_tokens"`
	UserChargeAmount float64 `json:"user_charge_amount"`
	RequestCount     int64   `json:"request_count"`
}

type relayUsageView struct {
	Range          string                  `json:"range"`
	Accounts       []relayUsageAccountView `json:"accounts"`
	Complete       bool                    `json:"complete"`
	FailedAccounts int                     `json:"failed_accounts"`
}

type relayGroupOption struct {
	ExternalID       int64    `json:"external_id"`
	Name             string   `json:"name"`
	Platform         string   `json:"platform,omitempty"`
	Status           string   `json:"status,omitempty"`
	IsExclusive      bool     `json:"is_exclusive"`
	RequireOAuthOnly bool     `json:"require_oauth_only"`
	ModelTypes       []string `json:"model_types,omitempty"`
	RateMultiplier   float64  `json:"rate_multiplier"`
}

type relayAccountView struct {
	ExternalID            int64                        `json:"external_id"`
	Name                  string                       `json:"name"`
	BaseURL               string                       `json:"base_url,omitempty"`
	Platform              string                       `json:"platform,omitempty"`
	Type                  string                       `json:"type,omitempty"`
	Status                string                       `json:"status,omitempty"`
	Schedulable           bool                         `json:"schedulable"`
	Concurrency           int                          `json:"concurrency"`
	CurrentConcurrency    int                          `json:"current_concurrency"`
	Priority              int                          `json:"priority"`
	PoolMode              bool                         `json:"pool_mode"`
	PoolModeRetryCount    int                          `json:"pool_mode_retry_count"`
	CostMultiplier        *float64                     `json:"cost_multiplier,omitempty"`
	CostSource            string                       `json:"cost_source,omitempty"`
	CostObservedAt        *time.Time                   `json:"cost_observed_at,omitempty"`
	CostOverrideMode      string                       `json:"cost_override_mode,omitempty"`
	CostOverrideChannelID *uint                        `json:"cost_override_channel_id,omitempty"`
	CostOverrideGroup     string                       `json:"cost_override_group,omitempty"`
	CurrentGroups         []relayGroupOption           `json:"current_groups"`
	UnsafeGroups          []relayGroupOption           `json:"unsafe_groups"`
	NoProfitGroups        []relayGroupOption           `json:"no_profit_groups"`
	RecommendedGroup      *relayGroupOption            `json:"recommended_group,omitempty"`
	DowngradeGroup        *relayGroupOption            `json:"downgrade_group,omitempty"`
	DowngradeGroups       []relayGroupOption           `json:"downgrade_groups"`
	SuggestedGroupIDs     []int64                      `json:"suggested_group_ids"`
	CurrentMinMultiplier  *float64                     `json:"current_min_multiplier,omitempty"`
	Margin                *float64                     `json:"margin,omitempty"`
	RiskState             string                       `json:"risk_state"`
	CanApply              bool                         `json:"can_apply"`
	LatencySamples        []storage.RelayLatencySample `json:"latency_samples"`
	UsageTotalTokens      int64                        `json:"usage_total_tokens"`
	UserChargeAmount      float64                      `json:"user_charge_amount"`
	AccountPlan           string                       `json:"account_plan,omitempty"`
	ModelType             string                       `json:"model_type,omitempty"`
	LastUsedAt            *time.Time                   `json:"last_used_at,omitempty"`
}

type relayRiskSummary struct {
	AccountCount         int        `json:"account_count"`
	RiskAccountCount     int        `json:"risk_account_count"`
	NoProfitAccountCount int        `json:"no_profit_account_count"`
	NoSafeCandidateCount int        `json:"no_safe_candidate_count"`
	UnknownCostCount     int        `json:"unknown_cost_count"`
	AutoAdjustedCount    int        `json:"auto_adjusted_count"`
	LastAdjustmentAt     *time.Time `json:"last_adjustment_at,omitempty"`
}

type relayAdjustmentView struct {
	ID                     uint      `json:"id"`
	RelayStationID         uint      `json:"relay_station_id"`
	RelayStationName       string    `json:"relay_station_name,omitempty"`
	RelayAccountExternalID int64     `json:"relay_account_external_id"`
	AccountName            string    `json:"account_name"`
	AccountPlatform        string    `json:"account_platform,omitempty"`
	CostMultiplier         float64   `json:"cost_multiplier"`
	OldGroupIDs            []int64   `json:"old_group_ids"`
	NewGroupIDs            []int64   `json:"new_group_ids"`
	OldGroupNames          []string  `json:"old_group_names"`
	NewGroupNames          []string  `json:"new_group_names"`
	RecommendedGroupID     *int64    `json:"recommended_group_id,omitempty"`
	OldConcurrency         *int      `json:"old_concurrency,omitempty"`
	NewConcurrency         *int      `json:"new_concurrency,omitempty"`
	OldPriority            *int      `json:"old_priority,omitempty"`
	NewPriority            *int      `json:"new_priority,omitempty"`
	OldPoolModeRetryCount  *int      `json:"old_pool_mode_retry_count,omitempty"`
	NewPoolModeRetryCount  *int      `json:"new_pool_mode_retry_count,omitempty"`
	Source                 string    `json:"source"`
	Action                 string    `json:"action"`
	Success                bool      `json:"success"`
	ErrorMessage           string    `json:"error_message,omitempty"`
	AppliedAt              time.Time `json:"applied_at"`
}

type monitorChannelOption struct {
	ID     uint     `json:"id"`
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Groups []string `json:"groups"`
}

type relayGroupView struct {
	ID                 uint               `json:"id"`
	ExternalID         int64              `json:"external_id"`
	Name               string             `json:"name"`
	Description        string             `json:"description,omitempty"`
	Platform           string             `json:"platform,omitempty"`
	Status             string             `json:"status,omitempty"`
	IsExclusive        bool               `json:"is_exclusive"`
	RequireOAuthOnly   bool               `json:"require_oauth_only"`
	SortOrder          int                `json:"sort_order"`
	AccountTypes       []string           `json:"account_types"`
	ModelTypes         []string           `json:"model_types"`
	RateMultiplier     float64            `json:"rate_multiplier"`
	MonitorEnabled     bool               `json:"monitor_enabled"`
	AccountCount       int                `json:"account_count"`
	PricedAccountCount int                `json:"priced_account_count"`
	Channels           []relayChannelView `json:"channels"`
}

type relayChannelView struct {
	ExternalID                 int64      `json:"external_id"`
	Name                       string     `json:"name"`
	Description                string     `json:"description,omitempty"`
	Status                     string     `json:"status,omitempty"`
	MonitorChannelID           *uint      `json:"monitor_channel_id,omitempty"`
	UpstreamGroup              string     `json:"upstream_group,omitempty"`
	UpstreamRatio              *float64   `json:"upstream_ratio,omitempty"`
	UpstreamLastSeenAt         *time.Time `json:"upstream_last_seen_at,omitempty"`
	RatioDelta                 *float64   `json:"ratio_delta,omitempty"`
	ProfitRatio                *float64   `json:"profit_ratio,omitempty"`
	ProfitSource               string     `json:"profit_source,omitempty"`
	BillingModelSource         string     `json:"billing_model_source,omitempty"`
	ApplyPricingToAccountStats bool       `json:"apply_pricing_to_account_stats"`
	PricingModelCount          int        `json:"pricing_model_count"`
	PricingRuleCount           int        `json:"pricing_rule_count"`
	PricingModels              []string   `json:"pricing_models"`
	PricingChangedAt           *time.Time `json:"pricing_changed_at,omitempty"`
}

func listRelayStations(c *gin.Context, d *Deps) {
	list, err := d.RelayStations.List()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]relayStationSummary, 0, len(list))
	for _, station := range list {
		out = append(out, stationSummary(&station))
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

func createRelayStation(c *gin.Context, d *Deps) {
	var in relayStationInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	station, err := d.Relay.Create(relay.CreateInput{Name: in.Name, BaseURL: in.BaseURL, APIKey: in.APIKey})
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": stationSummary(station)})
}

func updateRelayStation(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in relayStationUpdateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	station, err := d.Relay.Update(id, relay.UpdateInput{
		Name: in.Name, BaseURL: in.BaseURL, APIKey: in.APIKey,
		AutoAdjustEnabled: in.AutoAdjustEnabled, AutoAdjustNoProfitEnabled: in.AutoAdjustNoProfitEnabled,
		AutoPriorityEnabled: in.AutoPriorityEnabled, AutoPriorityRecallEnabled: in.AutoPriorityRecallEnabled,
		AutoPriorityRecallMinutes: in.AutoPriorityRecallMinutes,
	})
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": stationSummary(station)})
}

func updateRelayGroup(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	externalID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || externalID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站分组 ID"))
		return
	}
	var in relayGroupUpdateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	group, err := d.Relay.UpdateGroup(c.Request.Context(), stationID, externalID, relay.GroupUpdateInput{
		Name: in.Name, Description: in.Description, RateMultiplier: in.RateMultiplier,
		IsExclusive: in.IsExclusive, Status: in.Status,
		ModelTypes: in.ModelTypes, MonitorEnabled: in.MonitorEnabled,
	})
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": groupOption(*group)})
}

func deleteRelayStationGroup(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	externalID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || externalID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站分组 ID"))
		return
	}
	if err := d.Relay.DeleteGroup(c.Request.Context(), stationID, externalID); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}

func deleteRelayStationAccount(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	externalID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || externalID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站账号 ID"))
		return
	}
	if err := d.Relay.DeleteAccount(c.Request.Context(), stationID, externalID); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}

func deleteAllRelayStationAccounts(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	deleted, err := d.Relay.DeleteAllAccounts(c.Request.Context(), stationID)
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": deleted}})
}

func deleteAllRelayStationGroups(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	deleted, err := d.Relay.DeleteAllGroups(c.Request.Context(), stationID)
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": deleted}})
}

func updateRelayGroupSortOrder(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in relayGroupSortOrderInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.Relay.UpdateGroupSortOrders(c.Request.Context(), stationID, in.Updates); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"updated": len(in.Updates)}})
}

func deleteRelayStation(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.Relay.Delete(id); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func syncRelayStation(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.Relay.Sync(c.Request.Context(), id); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	relayStationOverview(c, d)
}

func updateRelayMapping(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	externalID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || externalID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站渠道 ID"))
		return
	}
	var in relayMappingInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if _, err := d.RelayStations.FindByID(stationID); err != nil {
		fail(c, http.StatusNotFound, errors.New("中转站不存在"))
		return
	}
	if _, err := d.RelayStations.FindChannelByExternalID(stationID, externalID); err != nil {
		fail(c, http.StatusNotFound, errors.New("中转站渠道不存在或快照已过期"))
		return
	}
	in.UpstreamGroup = strings.TrimSpace(in.UpstreamGroup)
	if in.MonitorChannelID == nil {
		if err := d.RelayStations.DeleteMapping(stationID, externalID); err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		relayStationOverview(c, d)
		return
	}
	if in.MonitorChannelID != nil {
		if _, err := d.Channels.FindByID(*in.MonitorChannelID); err != nil {
			fail(c, http.StatusBadRequest, errors.New("监测渠道不存在"))
			return
		}
	}
	if in.UpstreamGroup == "" {
		fail(c, http.StatusBadRequest, errors.New("选择成本来源渠道后必须选择分组"))
		return
	}
	rates, err := d.Rates.ListByChannel(*in.MonitorChannelID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	groupExists := false
	for _, rate := range rates {
		if rate.ModelName == in.UpstreamGroup {
			if rate.Source == storage.RateSnapshotSourceRelayAccount {
				fail(c, http.StatusBadRequest, errors.New("自动关联账号分组不能作为手工成本来源"))
				return
			}
			groupExists = true
			break
		}
	}
	if !groupExists {
		fail(c, http.StatusBadRequest, errors.New("所选成本来源分组不存在，请先同步该监测渠道"))
		return
	}
	if err := d.RelayStations.UpsertMapping(&storage.RelayChannelMapping{
		RelayStationID: stationID, RelayChannelExternalID: externalID,
		MonitorChannelID: in.MonitorChannelID, UpstreamGroup: in.UpstreamGroup,
	}); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	relayStationOverview(c, d)
}

func updateRelayCostOverrides(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in relayCostOverrideInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(in.AccountExternalIDs) == 0 || len(in.AccountExternalIDs) > 100 {
		fail(c, http.StatusBadRequest, errors.New("account_external_ids 必须包含 1 到 100 个账号"))
		return
	}
	if _, err := d.RelayStations.FindByID(stationID); err != nil {
		fail(c, http.StatusNotFound, errors.New("中转站不存在"))
		return
	}
	if in.Clear {
		for _, accountID := range in.AccountExternalIDs {
			if err := d.RelayStations.DeleteCostOverride(stationID, accountID); err != nil {
				fail(c, http.StatusInternalServerError, err)
				return
			}
		}
		if err := d.Relay.ReconcileManualChannelLinks(); err != nil {
			fail(c, http.StatusInternalServerError, fmt.Errorf("成本绑定已清除，但自动关联同步失败: %w", err))
			return
		}
		relayStationOverview(c, d)
		return
	}
	in.Mode = strings.ToLower(strings.TrimSpace(in.Mode))
	in.UpstreamGroup = strings.TrimSpace(in.UpstreamGroup)
	switch in.Mode {
	case "channel_group":
		if in.MonitorChannelID == nil || in.UpstreamGroup == "" {
			fail(c, http.StatusBadRequest, errors.New("渠道分组绑定必须选择渠道和分组"))
			return
		}
		if _, err := d.Channels.FindByID(*in.MonitorChannelID); err != nil {
			fail(c, http.StatusBadRequest, errors.New("监测渠道不存在"))
			return
		}
		rates, err := d.Rates.ListByChannel(*in.MonitorChannelID)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		found := false
		for _, rate := range rates {
			if rate.ModelName == in.UpstreamGroup && rate.Ratio >= 0 {
				if rate.Source == storage.RateSnapshotSourceRelayAccount {
					fail(c, http.StatusBadRequest, errors.New("自动关联账号分组不能作为手工成本来源"))
					return
				}
				found = true
				break
			}
		}
		if !found {
			fail(c, http.StatusBadRequest, errors.New("所选渠道分组倍率不存在，请先同步渠道"))
			return
		}
	case "manual":
		if in.ManualMultiplier == nil || *in.ManualMultiplier < 0 {
			fail(c, http.StatusBadRequest, errors.New("手工倍率必须是非负数"))
			return
		}
	default:
		fail(c, http.StatusBadRequest, errors.New("成本绑定模式必须是 channel_group 或 manual"))
		return
	}
	for _, accountID := range in.AccountExternalIDs {
		if _, err := d.RelayStations.FindAccountByExternalID(stationID, accountID); err != nil {
			fail(c, http.StatusBadRequest, fmt.Errorf("账号 %d 不存在或快照已过期", accountID))
			return
		}
		if err := d.RelayStations.UpsertCostOverride(&storage.RelayAccountCostOverride{
			RelayStationID: stationID, RelayAccountExternalID: accountID, Mode: in.Mode,
			MonitorChannelID: in.MonitorChannelID, UpstreamGroup: in.UpstreamGroup,
			ManualMultiplier: in.ManualMultiplier,
		}); err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	if err := d.Relay.ReconcileManualChannelLinks(); err != nil {
		fail(c, http.StatusInternalServerError, fmt.Errorf("成本绑定已保存，但自动关联同步失败: %w", err))
		return
	}
	relayStationOverview(c, d)
}

func updateRelayAccountGroups(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	externalID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || externalID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站账号 ID"))
		return
	}
	var in relayAccountGroupsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.Relay.SetGroups(c.Request.Context(), stationID, externalID, in.GroupExternalIDs); err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	relayStationOverview(c, d)
}

func addRelayAccountGroup(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	externalID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || externalID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站账号 ID"))
		return
	}
	var in relayAccountAddGroupInput
	if err := c.ShouldBindJSON(&in); err != nil || in.GroupExternalID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("group_external_id 必须是有效的销售分组 ID"))
		return
	}
	if err := d.Relay.AddGroup(c.Request.Context(), stationID, externalID, in.GroupExternalID); err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	relayStationOverview(c, d)
}

func updateRelayAccountGroupsBatch(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in relayAccountGroupsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(in.AccountExternalIDs) == 0 || len(in.AccountExternalIDs) > 100 {
		fail(c, http.StatusBadRequest, errors.New("account_external_ids 必须包含 1 到 100 个账号"))
		return
	}
	if err := d.Relay.SetGroupsBatch(c.Request.Context(), stationID, in.AccountExternalIDs, in.GroupExternalIDs); err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	relayStationOverview(c, d)
}

func updateRelayAccountModelTypes(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in relayAccountModelTypesInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(in.AccountExternalIDs) == 0 || len(in.AccountExternalIDs) > 100 {
		fail(c, http.StatusBadRequest, errors.New("account_external_ids 必须包含 1 到 100 个账号"))
		return
	}
	modelType := strings.TrimSpace(in.ModelType)
	if len(modelType) > 64 {
		fail(c, http.StatusBadRequest, errors.New("模型类型过长"))
		return
	}
	if err := d.RelayStations.SetAccountModelTypes(stationID, in.AccountExternalIDs, modelType); err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	relayStationOverview(c, d)
}

func updateRelayAccountSchedulable(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	externalID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || externalID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站账号 ID"))
		return
	}
	var in relayAccountSchedulableInput
	if err := c.ShouldBindJSON(&in); err != nil || in.Schedulable == nil {
		fail(c, http.StatusBadRequest, errors.New("schedulable 必须是布尔值"))
		return
	}
	if err := d.Relay.SetSchedulable(c.Request.Context(), stationID, externalID, *in.Schedulable); err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	relayStationOverview(c, d)
}

func updateRelayAccountsSchedulable(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in relayAccountSchedulableBatchInput
	if err := c.ShouldBindJSON(&in); err != nil || in.Schedulable == nil {
		fail(c, http.StatusBadRequest, errors.New("schedulable 必须是布尔值"))
		return
	}
	if err := validateRelayAccountBatchIDs(in.AccountExternalIDs); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	result, err := d.Relay.SetSchedulableBatch(c.Request.Context(), stationID, in.AccountExternalIDs, *in.Schedulable)
	if err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func applyRelayAccountSuggestions(c *gin.Context, d *Deps) {
	stationID, ids, ok := relayAccountActionBatchParams(c)
	if !ok {
		return
	}
	result, err := d.Relay.ApplySuggestionsBatch(c.Request.Context(), stationID, ids)
	if err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func addRelayAccountDowngrades(c *gin.Context, d *Deps) {
	stationID, ids, ok := relayAccountActionBatchParams(c)
	if !ok {
		return
	}
	result, err := d.Relay.AddDowngradesBatch(c.Request.Context(), stationID, ids)
	if err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func relayAccountActionBatchParams(c *gin.Context) (uint, []int64, bool) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return 0, nil, false
	}
	var in relayAccountActionBatchInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return 0, nil, false
	}
	if err := validateRelayAccountBatchIDs(in.AccountExternalIDs); err != nil {
		fail(c, http.StatusBadRequest, err)
		return 0, nil, false
	}
	return stationID, in.AccountExternalIDs, true
}

func validateRelayAccountBatchIDs(ids []int64) error {
	if len(ids) == 0 || len(ids) > 1000 {
		return errors.New("account_external_ids 必须包含 1 到 1000 个账号")
	}
	return nil
}

func updateRelayAccountRuntimeSettings(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in relayAccountRuntimeSettingsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(in.AccountExternalIDs) == 0 || len(in.AccountExternalIDs) > 100 {
		fail(c, http.StatusBadRequest, errors.New("account_external_ids 必须包含 1 到 100 个账号"))
		return
	}
	if err := d.Relay.SetRuntimeSettingsBatch(c.Request.Context(), stationID, in.AccountExternalIDs, in.Concurrency, in.Priority, in.PoolModeRetryCount); err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	relayStationOverview(c, d)
}

func probeRelayAccount(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	externalID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || externalID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站账号 ID"))
		return
	}
	if err := d.Relay.ProbeAccount(c.Request.Context(), stationID, externalID); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	relayStationOverview(c, d)
}

func applyRelayAccountSuggestion(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	externalID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || externalID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站账号 ID"))
		return
	}
	if err := d.Relay.ApplySuggestion(c.Request.Context(), stationID, externalID); err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	relayStationOverview(c, d)
}

func relayStationOverview(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	station, err := d.RelayStations.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	groups, err := d.RelayStations.ListGroups(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	channels, err := d.RelayStations.ListChannels(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	links, err := d.RelayStations.ListLinks(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	mappings, err := d.RelayStations.ListMappings(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	monitorChannels, err := d.Channels.List()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	risks, err := d.Relay.Risks(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	_, rangeName := dashboardSince(c.DefaultQuery("range", "today"))
	adjustments, err := d.RelayStations.ListAdjustmentLogsByCategory(id, 50)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	channelByLocalID := make(map[uint]storage.RelayChannel, len(channels))
	for _, channel := range channels {
		channelByLocalID[channel.ID] = channel
	}
	mappingByExternalID := make(map[int64]storage.RelayChannelMapping, len(mappings))
	for _, mapping := range mappings {
		mappingByExternalID[mapping.RelayChannelExternalID] = mapping
	}
	rateCache := make(map[uint][]storage.RateSnapshot)
	monitorOptions := make([]monitorChannelOption, 0, len(monitorChannels))
	for _, channel := range monitorChannels {
		if _, ok := rateCache[channel.ID]; !ok {
			rateCache[channel.ID], _ = d.Rates.ListByChannel(channel.ID)
		}
		groupNames := make([]string, 0, len(rateCache[channel.ID]))
		for _, rate := range rateCache[channel.ID] {
			groupNames = append(groupNames, rate.ModelName)
		}
		monitorOptions = append(monitorOptions, monitorChannelOption{ID: channel.ID, Name: channel.Name, Type: string(channel.Type), Groups: groupNames})
	}

	groupViews := make([]relayGroupView, 0, len(groups))
	accountTypesByGroup := make(map[int64]map[string]struct{}, len(groups))
	for _, risk := range risks {
		accountType := strings.ToLower(strings.TrimSpace(risk.Account.Type))
		if accountType == "" {
			accountType = "unknown"
		}
		for _, group := range risk.CurrentGroups {
			if accountTypesByGroup[group.ExternalID] == nil {
				accountTypesByGroup[group.ExternalID] = make(map[string]struct{})
			}
			accountTypesByGroup[group.ExternalID][accountType] = struct{}{}
		}
	}
	for _, group := range groups {
		accountTypes := make([]string, 0, len(accountTypesByGroup[group.ExternalID]))
		for accountType := range accountTypesByGroup[group.ExternalID] {
			accountTypes = append(accountTypes, accountType)
		}
		sort.Strings(accountTypes)
		var modelTypes []string
		if strings.TrimSpace(group.ModelTypesJSON) != "" {
			_ = json.Unmarshal([]byte(group.ModelTypesJSON), &modelTypes)
		}
		view := relayGroupView{ID: group.ID, ExternalID: group.ExternalID, Name: group.Name, Description: group.Description, Platform: group.Platform, Status: group.Status, IsExclusive: group.IsExclusive, RequireOAuthOnly: group.RequireOAuthOnly, SortOrder: group.SortOrder, AccountTypes: accountTypes, ModelTypes: modelTypes, RateMultiplier: group.RateMultiplier, MonitorEnabled: group.MonitorEnabled, Channels: []relayChannelView{}}
		for _, link := range links {
			if link.RelayGroupID != group.ID {
				continue
			}
			channel, ok := channelByLocalID[link.RelayChannelID]
			if !ok {
				continue
			}
			mapping := mappingByExternalID[channel.ExternalID]
			pricingModels := make([]string, 0)
			_ = json.Unmarshal([]byte(channel.PricingModelsJSON), &pricingModels)
			item := relayChannelView{
				ExternalID: channel.ExternalID, Name: channel.Name, Description: channel.Description, Status: channel.Status,
				MonitorChannelID: mapping.MonitorChannelID, UpstreamGroup: mapping.UpstreamGroup,
				BillingModelSource: channel.BillingModelSource, ApplyPricingToAccountStats: channel.ApplyPricingToAccountStats,
				PricingModelCount: channel.PricingModelCount, PricingRuleCount: channel.PricingRuleCount,
				PricingModels: pricingModels, PricingChangedAt: channel.PricingChangedAt,
			}
			if channel.PricingModelCount > 0 {
				view.PricedAccountCount++
			}
			if mapping.MonitorChannelID != nil && mapping.UpstreamGroup != "" {
				if _, ok := rateCache[*mapping.MonitorChannelID]; !ok {
					rateCache[*mapping.MonitorChannelID], _ = d.Rates.ListByChannel(*mapping.MonitorChannelID)
				}
				for _, rate := range rateCache[*mapping.MonitorChannelID] {
					if rate.ModelName == mapping.UpstreamGroup {
						ratio, delta := rate.Ratio, group.RateMultiplier-rate.Ratio
						item.UpstreamRatio = &ratio
						item.RatioDelta = &delta
						last := rate.LastSeenAt
						item.UpstreamLastSeenAt = &last
						break
					}
				}
			}
			if item.UpstreamRatio != nil {
				delta := group.RateMultiplier - *item.UpstreamRatio
				item.RatioDelta = &delta
				item.ProfitRatio = &delta
				item.ProfitSource = "显式成本映射"
			}
			view.Channels = append(view.Channels, item)
		}
		view.AccountCount = len(view.Channels)
		groupViews = append(groupViews, view)
	}

	accountViews := make([]relayAccountView, 0, len(risks))
	summary := relayRiskSummary{AccountCount: len(risks)}
	for _, risk := range risks {
		view := relayAccountView{
			ExternalID: risk.Account.ExternalID, Name: risk.Account.Name,
			BaseURL:  risk.Account.BaseURL,
			Platform: risk.Account.Platform, Type: risk.Account.Type, Status: risk.Account.Status, Schedulable: risk.Account.Schedulable,
			Concurrency: risk.Account.Concurrency, CurrentConcurrency: risk.Account.CurrentConcurrency, Priority: risk.Account.Priority,
			PoolMode: risk.Account.PoolMode, PoolModeRetryCount: risk.Account.PoolModeRetryCount,
			AccountPlan: risk.Account.AccountPlan, LastUsedAt: risk.Account.LastUsedAt, ModelType: risk.Account.ModelType,
			CostMultiplier: risk.Account.RateMultiplier, CurrentGroups: groupOptions(risk.CurrentGroups),
			CostSource: risk.Account.RateSource, CostObservedAt: risk.Account.RateObservedAt,
			UnsafeGroups: groupOptions(risk.UnsafeGroups), NoProfitGroups: groupOptions(risk.NoProfitGroups), DowngradeGroups: groupOptions(risk.DowngradeGroups), SuggestedGroupIDs: risk.SuggestedGroupIDs,
			CurrentMinMultiplier: risk.CurrentMinMultiplier, Margin: risk.Margin,
			RiskState: risk.State, CanApply: risk.CanApply, LatencySamples: []storage.RelayLatencySample{},
		}
		if risk.Account.LatencySamplesJSON != "" {
			_ = json.Unmarshal([]byte(risk.Account.LatencySamplesJSON), &view.LatencySamples)
		}
		if risk.CostOverride != nil {
			view.CostOverrideMode = risk.CostOverride.Mode
			view.CostOverrideChannelID = risk.CostOverride.MonitorChannelID
			view.CostOverrideGroup = risk.CostOverride.UpstreamGroup
		}
		if risk.RecommendedGroup != nil {
			candidate := groupOption(*risk.RecommendedGroup)
			view.RecommendedGroup = &candidate
		}
		if risk.DowngradeGroup != nil {
			candidate := groupOption(*risk.DowngradeGroup)
			view.DowngradeGroup = &candidate
		}
		switch risk.State {
		case relay.RiskStateRisk:
			summary.RiskAccountCount++
		case relay.RiskStateNoProfit:
			summary.NoProfitAccountCount++
		case relay.RiskStateNoSafeCandidate:
			summary.NoSafeCandidateCount++
		case relay.RiskStateCostUnknown:
			summary.UnknownCostCount++
		}
		accountViews = append(accountViews, view)
	}
	adjustmentViews := make([]relayAdjustmentView, 0, len(adjustments))
	for _, adjustment := range adjustments {
		view := relayAdjustmentView{
			ID: adjustment.ID, RelayStationID: station.ID, RelayStationName: station.Name,
			RelayAccountExternalID: adjustment.RelayAccountExternalID,
			AccountName:            adjustment.AccountName, AccountPlatform: adjustment.AccountPlatform,
			CostMultiplier:     adjustment.CostMultiplier,
			RecommendedGroupID: adjustment.RecommendedGroupID, Source: adjustment.Source,
			OldConcurrency: adjustment.OldConcurrency, NewConcurrency: adjustment.NewConcurrency,
			OldPriority: adjustment.OldPriority, NewPriority: adjustment.NewPriority,
			OldPoolModeRetryCount: adjustment.OldPoolModeRetryCount, NewPoolModeRetryCount: adjustment.NewPoolModeRetryCount,
			Action:  adjustment.Action,
			Success: adjustment.Success, ErrorMessage: adjustment.ErrorMessage, AppliedAt: adjustment.AppliedAt,
		}
		_ = json.Unmarshal([]byte(adjustment.OldGroupIDsJSON), &view.OldGroupIDs)
		_ = json.Unmarshal([]byte(adjustment.NewGroupIDsJSON), &view.NewGroupIDs)
		view.OldGroupNames = relayAdjustmentGroupNames(view.OldGroupIDs, groups)
		view.NewGroupNames = relayAdjustmentGroupNames(view.NewGroupIDs, groups)
		if adjustment.Source == "auto" && adjustment.Success && adjustment.Action != "priority_update" && adjustment.Action != "concurrency_update" && adjustment.Action != "runtime_settings_update" {
			summary.AutoAdjustedCount++
		}
		if summary.LastAdjustmentAt == nil || adjustment.AppliedAt.After(*summary.LastAdjustmentAt) {
			at := adjustment.AppliedAt
			summary.LastAdjustmentAt = &at
		}
		adjustmentViews = append(adjustmentViews, view)
	}

	c.JSON(http.StatusOK, gin.H{"data": relayOverview{
		Station: stationSummary(station), Groups: groupViews, Accounts: accountViews, Summary: summary,
		Adjustments: adjustmentViews, MonitorChannels: monitorOptions, SyncAvailable: station.APIKeyCipher != "",
		Range: rangeName, UsageComplete: false, UsageFailedAccounts: 0,
	}})
}

func relayStationUsage(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	since, rangeName := operationSince(c.DefaultQuery("range", "today"))
	usage, usageErr := d.Relay.UsageStats(c.Request.Context(), id, rangeName, since)
	if usageErr != nil && d.Log != nil {
		d.Log.Warn("relay usage stats failed", "station_id", id, "range", rangeName, "err", usageErr)
	}
	accounts := make([]relayUsageAccountView, 0, len(usage.Accounts))
	for externalID, stats := range usage.Accounts {
		accounts = append(accounts, relayUsageAccountView{
			ExternalID:       externalID,
			UsageTotalTokens: stats.TotalTokens,
			UserChargeAmount: stats.UserCharge,
			RequestCount:     stats.Requests,
		})
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ExternalID < accounts[j].ExternalID })
	c.JSON(http.StatusOK, gin.H{"data": relayUsageView{
		Range:          rangeName,
		Accounts:       accounts,
		Complete:       usageErr == nil && usage.FailedAccounts == 0,
		FailedAccounts: usage.FailedAccounts,
	}})
}

func relayStationRecentUsage(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	items, err := d.Relay.RecentUsage(c.Request.Context(), id, limit)
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func relayStationUsers(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	since, rangeName := operationSince(c.DefaultQuery("range", "today"))
	result, err := d.Relay.Users(c.Request.Context(), id, relay.UserListQuery{
		Page: page, PageSize: pageSize, Search: c.Query("search"), RangeName: rangeName, Since: since,
		SortBy: c.DefaultQuery("sort_by", "balance"), SortOrder: c.DefaultQuery("sort_order", "desc"),
		RiskLevel:      c.DefaultQuery("risk_level", "all"),
		RegistrationIP: c.Query("registration_ip"),
	})
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func updateRelayStationUserStatus(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("用户 ID 无效"))
		return
	}
	var input relayUserStatusInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.Relay.UpdateUserStatus(c.Request.Context(), id, userID, input.Status); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": strings.ToLower(strings.TrimSpace(input.Status))}})
}

func updateRelayStationUserLimits(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var input relayUserLimitsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	affected, err := d.Relay.UpdateUserLimits(c.Request.Context(), id, relay.UserBatchLimitsInput{
		UserIDs: input.UserIDs, Concurrency: input.Concurrency, RPMLimit: input.RPMLimit,
	})
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"affected": affected}})
}

func deleteRelayStationUsers(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var input relayUserDeleteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(input.UserIDs) == 0 || len(input.UserIDs) > 300 {
		fail(c, http.StatusBadRequest, errors.New("user_ids 必须包含 1 到 300 个用户"))
		return
	}
	result, err := d.Relay.DeleteUsers(c.Request.Context(), id, input.UserIDs)
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func relayStationUserBalanceHistory(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("用户 ID 无效"))
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "15"))
	result, err := d.Relay.UserBalanceHistory(c.Request.Context(), id, userID, page, pageSize, c.Query("type"))
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

type relayAccountTestInput struct {
	ModelID string `json:"model_id"`
	Mode    string `json:"mode"`
}

type relayGroupTestInput struct {
	Model string `json:"model"`
	Count int    `json:"count"`
}

func relayStationAccountModels(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	accountID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || accountID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("账号 ID 无效"))
		return
	}
	models, err := d.Relay.AccountModels(c.Request.Context(), stationID, accountID)
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": models})
}

func relayStationTestAccount(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	accountID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || accountID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("账号 ID 无效"))
		return
	}
	var in relayAccountTestInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	in.ModelID = strings.TrimSpace(in.ModelID)
	in.Mode = strings.TrimSpace(in.Mode)
	if in.ModelID == "" {
		fail(c, http.StatusBadRequest, errors.New("请选择测试模型"))
		return
	}
	if in.Mode != "regular" && in.Mode != "stream" {
		fail(c, http.StatusBadRequest, errors.New("测试模式无效"))
		return
	}
	result, err := d.Relay.TestAccount(c.Request.Context(), stationID, accountID, in.ModelID, in.Mode)
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func relayStationGroupModels(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	groupID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || groupID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("分组 ID 无效"))
		return
	}
	models, err := d.Relay.GroupModels(c.Request.Context(), stationID, groupID)
	if err != nil {
		failRelayGroupDependency(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": models})
}

func relayStationTestGroup(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	groupID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || groupID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("分组 ID 无效"))
		return
	}
	var in relayGroupTestInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	in.Model = strings.TrimSpace(in.Model)
	if in.Model == "" {
		fail(c, http.StatusBadRequest, errors.New("请选择测试模型"))
		return
	}
	if in.Count < 1 || in.Count > 10 {
		fail(c, http.StatusBadRequest, errors.New("调用次数必须是 1 到 10 次"))
		return
	}
	result, err := d.Relay.TestGroup(c.Request.Context(), stationID, groupID, in.Model, in.Count)
	if err != nil {
		failRelayGroupDependency(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func failRelayGroupDependency(c *gin.Context, err error) {
	if errors.Is(err, relay.ErrGroupAdminAPIKeyMissing) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"code":  "group_admin_api_key_missing",
			"error": err.Error(),
		})
		return
	}
	fail(c, http.StatusBadGateway, err)
}

func relayAdjustmentGroupNames(ids []int64, groups []storage.RelayGroup) []string {
	nameByID := make(map[int64]string, len(groups))
	for _, group := range groups {
		nameByID[group.ExternalID] = group.Name
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if name := strings.TrimSpace(nameByID[id]); name != "" {
			names = append(names, name)
		} else {
			names = append(names, fmt.Sprintf("#%d", id))
		}
	}
	return names
}

func stationSummary(station *storage.RelayStation) relayStationSummary {
	return relayStationSummary{
		ID: station.ID, Name: station.Name, BaseURL: station.BaseURL, APIKeyConfigured: station.APIKeyCipher != "",
		AutoAdjustEnabled: station.AutoAdjustEnabled, AutoAdjustNoProfitEnabled: station.AutoAdjustNoProfitEnabled,
		AutoPriorityEnabled: station.AutoPriorityEnabled, AutoPriorityRecallEnabled: station.AutoPriorityRecallEnabled,
		AutoPriorityRecallMinutes: station.AutoPriorityRecallMinutes,
		LastSyncedAt:              station.LastSyncedAt, LastError: station.LastError,
	}
}

func groupOption(group storage.RelayGroup) relayGroupOption {
	var modelTypes []string
	if strings.TrimSpace(group.ModelTypesJSON) != "" {
		_ = json.Unmarshal([]byte(group.ModelTypesJSON), &modelTypes)
	}
	return relayGroupOption{
		ExternalID: group.ExternalID, Name: group.Name, Platform: group.Platform,
		Status: group.Status, IsExclusive: group.IsExclusive, RequireOAuthOnly: group.RequireOAuthOnly, ModelTypes: modelTypes, RateMultiplier: group.RateMultiplier,
	}
}

func groupOptions(groups []storage.RelayGroup) []relayGroupOption {
	options := make([]relayGroupOption, 0, len(groups))
	for _, group := range groups {
		options = append(options, groupOption(group))
	}
	return options
}

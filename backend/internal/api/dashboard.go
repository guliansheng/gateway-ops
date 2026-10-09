package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

// registerDashboard 提供首页所需聚合视图。
func registerDashboard(g *gin.RouterGroup, d *Deps) {
	g.GET("/dashboard/summary", func(c *gin.Context) { dashboardSummary(c, d) })
	g.GET("/dashboard/balance-trend", func(c *gin.Context) { dashboardBalanceTrend(c, d) })
}

type dashboardLowest struct {
	ChannelID uint     `json:"channel_id"`
	Name      string   `json:"name"`
	Balance   *float64 `json:"balance"`
}

type dashboardChannelStat struct {
	ID             uint     `json:"id"`
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	SiteURL        string   `json:"site_url"`
	MonitorEnabled bool     `json:"monitor_enabled"`
	LastBalance    *float64 `json:"last_balance,omitempty"`
	LastError      string   `json:"last_error,omitempty"`
}

type relayDashboardStat struct {
	StationID                 uint                     `json:"station_id"`
	StationName               string                   `json:"station_name"`
	GroupCount                int                      `json:"group_count"`
	AccountCount              int                      `json:"account_count"`
	AssignmentCount           int                      `json:"assignment_count"`
	MappedAccountCount        int                      `json:"mapped_account_count"`
	MatchedAssignmentCount    int                      `json:"matched_assignment_count"`
	PricingAccountCount       int                      `json:"pricing_account_count"`
	ProfitRatioTotal          float64                  `json:"profit_ratio_total"`
	ProfitRatioAverage        float64                  `json:"profit_ratio_average"`
	NegativeMarginCount       int                      `json:"negative_margin_count"`
	RiskAccountCount          int                      `json:"risk_account_count"`
	NoProfitAccountCount      int                      `json:"no_profit_account_count"`
	NoSafeCandidateCount      int                      `json:"no_safe_candidate_count"`
	UnknownCostCount          int                      `json:"unknown_cost_count"`
	ProtectedAccountCount     int                      `json:"protected_account_count"`
	AutoAdjustEnabled         bool                     `json:"auto_adjust_enabled"`
	AutoAdjustNoProfitEnabled bool                     `json:"auto_adjust_no_profit_enabled"`
	AutoPriorityEnabled       bool                     `json:"auto_priority_enabled"`
	RecentPricingChanges      []relayPricingChangeView `json:"recent_pricing_changes"`
}

type relayPricingChangeView struct {
	StationID         uint      `json:"station_id"`
	StationName       string    `json:"station_name"`
	ChannelExternalID int64     `json:"channel_external_id"`
	ChannelName       string    `json:"channel_name"`
	OldModelCount     int       `json:"old_model_count"`
	NewModelCount     int       `json:"new_model_count"`
	OldRuleCount      int       `json:"old_rule_count"`
	NewRuleCount      int       `json:"new_rule_count"`
	ChangedAt         time.Time `json:"changed_at"`
}

func dashboardSummary(c *gin.Context, d *Deps) {
	since, rangeName := dashboardSince(c.DefaultQuery("range", "today"))
	channels, err := d.Channels.List()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	stats := make([]dashboardChannelStat, 0, len(channels))
	var totalBalance float64
	var consumptionAmount float64
	var matchedAccountCount int
	var lowest *dashboardLowest
	var activeCount, failedCount int

	for _, ch := range channels {
		lastBalance := ch.CurrentBalanceAmount()
		stat := dashboardChannelStat{
			ID:             ch.ID,
			Name:           ch.Name,
			Type:           string(ch.Type),
			SiteURL:        ch.SiteURL,
			MonitorEnabled: ch.MonitorEnabled,
			LastBalance:    lastBalance,
			LastError:      ch.LastError,
		}
		stats = append(stats, stat)
		if ch.LastError != "" {
			failedCount++
		} else if ch.MonitorEnabled {
			activeCount++
		}
		if lastBalance != nil {
			totalBalance += *lastBalance
			if ch.MonitorEnabled && (lowest == nil || (lowest.Balance == nil) || (*lastBalance < *lowest.Balance)) {
				bal := *lastBalance
				lowest = &dashboardLowest{ChannelID: ch.ID, Name: ch.Name, Balance: &bal}
			}
		}
	}
	consumption, err := d.Rates.ChannelConsumptionTotals(since)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	recharges, err := d.Operations.ChannelRechargeTotals()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	cumulativeRechargeAmount := dashboardCumulativeRecharge(channels, recharges)
	usage, err := d.Relay.ChannelUsageTotals(c.Request.Context(), channels, rangeName, since)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	for _, ch := range channels {
		interval := usage[ch.ID]
		consumptionAmount += channelAmounts(ch, interval, consumption[ch.ID], recharges[ch.ID]).ConsumptionAmount
		matchedAccountCount += interval.MatchedAccountCount
	}

	recentChanges, err := d.Rates.ListChangesSince(since, 20)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	rateChangeCount, err := d.Rates.CountChangesSince(since)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	relayStats, err := relayDashboardStats(d, since)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	userChargeAmount, userChargeErr := dashboardUserChargeTotal(c.Request.Context(), d.Relay, relayStats, rangeName, since)
	if userChargeErr != nil && d.Log != nil {
		d.Log.Warn("dashboard relay user charge failed", "err", userChargeErr)
	}
	relayAdjustments, err := relayDashboardAdjustments(d)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	recentNotifs, err := d.Notifies.ListLogs(10)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"range":                      rangeName,
			"total_channels":             len(channels),
			"active_channels":            activeCount,
			"failed_channels":            failedCount,
			"total_balance":              totalBalance,
			"cumulative_recharge_amount": cumulativeRechargeAmount,
			"consumption_amount":         consumptionAmount,
			"user_charge_amount":         userChargeAmount,
			"matched_account_count":      matchedAccountCount,
			"user_charge_complete":       userChargeErr == nil,
			"user_charge_breakdown":      dashboardUserChargeBreakdown(userChargeAmount, userChargeErr == nil, usage),
			"lowest_balance":             lowest,
			"channels":                   stats,
			"rate_change_count":          rateChangeCount,
			"recent_rate_changes":        recentChanges,
			"recent_notification_logs":   recentNotifs,
			"relay":                      relayStats,
			"recent_relay_adjustments":   relayAdjustments,
		},
	})
}

type relayUsageTotalReader interface {
	UsageTotal(context.Context, uint, string, time.Time) (relay.UsageSummary, error)
}

type dashboardChargeBreakdown struct {
	ChannelAmount   float64  `json:"channel_amount"`
	ChannelComplete bool     `json:"channel_complete"`
	UnlinkedAmount  *float64 `json:"unlinked_amount"`
}

func dashboardUserChargeBreakdown(total float64, totalComplete bool, usage map[uint]relay.ChannelUsageTotal) dashboardChargeBreakdown {
	result := dashboardChargeBreakdown{ChannelComplete: true}
	for _, item := range usage {
		result.ChannelAmount += item.UserCharge
		result.ChannelComplete = result.ChannelComplete && item.Complete
	}
	if !totalComplete || !result.ChannelComplete {
		return result
	}
	unlinked := total - result.ChannelAmount
	// Totals and channel usage have separate caches. If the channel samples
	// exceed the station total, wait for consistent data instead of reporting
	// negative unlinked charges. Ignore only floating-point rounding noise.
	tolerance := math.Max(1, math.Abs(total)) * 1e-9
	if unlinked < -tolerance {
		return result
	}
	unlinked = math.Max(0, unlinked)
	result.UnlinkedAmount = &unlinked
	return result
}

// User charges cover every relay station, including temporary accounts without
// a channel binding. Channel consumption is calculated separately by channel.
func dashboardUserChargeTotal(ctx context.Context, reader relayUsageTotalReader, stations []relayDashboardStat, rangeName string, since time.Time) (float64, error) {
	var total float64
	var failures []error
	for _, station := range stations {
		usage, err := reader.UsageTotal(ctx, station.StationID, rangeName, since)
		if err != nil {
			failures = append(failures, fmt.Errorf("station %d: %w", station.StationID, err))
			continue
		}
		total += usage.UserCharge
	}
	return total, errors.Join(failures...)
}

func dashboardCumulativeRecharge(channels []storage.Channel, amounts map[uint]float64) float64 {
	var total float64
	for _, channel := range channels {
		total += amounts[channel.ID]
	}
	return total
}

// relayDashboardAdjustments uses the same per-category history window as the
// relay station page. Dashboard time ranges apply to statistics, not to the
// shared adjustment log, so both views report the same number of records.
func relayDashboardAdjustments(d *Deps) ([]relayAdjustmentView, error) {
	stations, err := d.RelayStations.List()
	if err != nil {
		return nil, err
	}
	items := make([]relayAdjustmentView, 0)
	for _, station := range stations {
		groups, err := d.RelayStations.ListGroups(station.ID)
		if err != nil {
			return nil, err
		}
		logs, err := d.RelayStations.ListAdjustmentLogsByCategory(station.ID, 50)
		if err != nil {
			return nil, err
		}
		for _, log := range logs {
			view := relayAdjustmentView{
				ID: log.ID, RelayStationID: station.ID, RelayStationName: station.Name,
				RelayAccountExternalID: log.RelayAccountExternalID,
				AccountName:            log.AccountName, AccountPlatform: log.AccountPlatform,
				CostMultiplier: log.CostMultiplier, RecommendedGroupID: log.RecommendedGroupID,
				OldConcurrency: log.OldConcurrency, NewConcurrency: log.NewConcurrency,
				OldPriority: log.OldPriority, NewPriority: log.NewPriority,
				Source: log.Source, Action: log.Action, Success: log.Success,
				ErrorMessage: log.ErrorMessage, AppliedAt: log.AppliedAt,
			}
			_ = json.Unmarshal([]byte(log.OldGroupIDsJSON), &view.OldGroupIDs)
			_ = json.Unmarshal([]byte(log.NewGroupIDsJSON), &view.NewGroupIDs)
			view.OldGroupNames = relayAdjustmentGroupNames(view.OldGroupIDs, groups)
			view.NewGroupNames = relayAdjustmentGroupNames(view.NewGroupIDs, groups)
			items = append(items, view)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].AppliedAt.After(items[j].AppliedAt) })
	return items, nil
}

func dashboardSince(raw string) (time.Time, string) {
	return dashboardSinceAt(time.Now(), raw)
}

func dashboardSinceAt(now time.Time, raw string) (time.Time, string) {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch raw {
	case "all":
		return time.Time{}, "all"
	case "24h":
		return now.Add(-24 * time.Hour), "24h"
	case "7d":
		return start.AddDate(0, 0, -6), "7d"
	case "30d":
		return start.AddDate(0, 0, -29), "30d"
	default:
		return start, "today"
	}
}

func relayDashboardStats(d *Deps, since time.Time) ([]relayDashboardStat, error) {
	stations, err := d.RelayStations.List()
	if err != nil {
		return nil, err
	}
	out := make([]relayDashboardStat, 0, len(stations))
	for _, station := range stations {
		groups, err := d.RelayStations.ListGroups(station.ID)
		if err != nil {
			return nil, err
		}
		channels, err := d.RelayStations.ListChannels(station.ID)
		if err != nil {
			return nil, err
		}
		risks, err := d.Relay.Risks(station.ID)
		if err != nil {
			return nil, err
		}
		stat := relayDashboardStat{
			StationID: station.ID, StationName: station.Name, GroupCount: len(groups), AccountCount: len(risks),
			AutoAdjustEnabled:         station.AutoAdjustEnabled,
			AutoAdjustNoProfitEnabled: station.AutoAdjustNoProfitEnabled,
			AutoPriorityEnabled:       station.AutoPriorityEnabled,
			RecentPricingChanges:      []relayPricingChangeView{},
		}
		marginCount := 0
		for _, risk := range risks {
			stat.AssignmentCount += len(risk.CurrentGroups)
			if risk.Account.RateMultiplier != nil {
				stat.MappedAccountCount++
				stat.PricingAccountCount++
				for _, group := range risk.CurrentGroups {
					if strings.EqualFold(strings.TrimSpace(group.Status), "active") && strings.EqualFold(strings.TrimSpace(group.Platform), strings.TrimSpace(risk.Account.Platform)) {
						stat.MatchedAssignmentCount++
					}
				}
			}
			if risk.Margin != nil {
				stat.ProfitRatioTotal += *risk.Margin
				marginCount++
			}
			switch risk.State {
			case "risk":
				stat.RiskAccountCount++
				stat.NegativeMarginCount++
			case "no_profit":
				stat.NoProfitAccountCount++
			case "no_safe_candidate":
				stat.NoSafeCandidateCount++
				stat.NegativeMarginCount++
			case "cost_unknown":
				stat.UnknownCostCount++
			case "protected":
				stat.ProtectedAccountCount++
			}
		}
		if marginCount > 0 {
			stat.ProfitRatioAverage = stat.ProfitRatioTotal / float64(marginCount)
		}
		changes, err := d.RelayStations.ListPricingChanges(station.ID, since, 20)
		if err != nil {
			return nil, err
		}
		channelNames := make(map[int64]string, len(channels))
		for _, channel := range channels {
			channelNames[channel.ExternalID] = channel.Name
		}
		for _, change := range changes {
			stat.RecentPricingChanges = append(stat.RecentPricingChanges, relayPricingChangeView{
				StationID: station.ID, StationName: station.Name, ChannelExternalID: change.RelayChannelExternalID,
				ChannelName:   channelNames[change.RelayChannelExternalID],
				OldModelCount: change.OldModelCount, NewModelCount: change.NewModelCount,
				OldRuleCount: change.OldRuleCount, NewRuleCount: change.NewRuleCount,
				ChangedAt: change.ChangedAt,
			})
		}
		out = append(out, stat)
	}
	return out, nil
}

func dashboardBalanceTrend(c *gin.Context, d *Deps) {
	if rawRange := c.Query("range"); rawRange != "" {
		since, rangeName := dashboardSince(rawRange)
		var (
			trend []storage.DailyAggregate
			err   error
		)
		if rangeName == "today" || rangeName == "24h" {
			trend, err = d.Rates.AggregateBalanceTrendHourlySince(since)
		} else {
			trend, err = d.Rates.AggregateBalanceTrendSince(since)
		}
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": trend})
		return
	}
	if c.DefaultQuery("bucket", "day") == "hour" {
		hours, _ := strconv.Atoi(c.DefaultQuery("hours", "24"))
		if hours <= 0 {
			hours = 24
		}
		trend, err := d.Rates.AggregateBalanceTrendHourly(hours)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": trend})
		return
	}

	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days <= 0 {
		days = 7
	}
	trend, err := d.Rates.AggregateBalanceTrend(days)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": trend})
}

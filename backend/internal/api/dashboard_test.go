package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

type relayUsageTotalFunc func(context.Context, uint, string, time.Time) (relay.UsageSummary, error)

func (f relayUsageTotalFunc) UsageTotal(ctx context.Context, stationID uint, rangeName string, since time.Time) (relay.UsageSummary, error) {
	return f(ctx, stationID, rangeName, since)
}

func TestDashboardUserChargeTotalIncludesAllStationsAndUnboundAccounts(t *testing.T) {
	now := time.Date(2026, time.October, 9, 15, 30, 0, 0, time.UTC)
	stations := []relayDashboardStat{
		{StationID: 1, AccountCount: 2, MappedAccountCount: 1},
		{StationID: 2, AccountCount: 1, MappedAccountCount: 0},
		{StationID: 3, AccountCount: 0, MappedAccountCount: 0},
	}
	// The station total also includes unbound and not-yet-synced accounts.
	// The account breakdown is deliberately incomplete and must not be summed.
	usage := map[uint]relay.UsageSummary{
		1: {UserCharge: 12.5, Accounts: map[int64]relay.AccountUsageStats{
			101: {UserCharge: 7.5, AccountCost: 2},
		}},
		2: {UserCharge: 3.25},
		3: {UserCharge: 4.25},
	}
	for _, rangeName := range []string{"today", "24h", "7d", "30d", "all"} {
		t.Run(rangeName, func(t *testing.T) {
			since, _ := dashboardSinceAt(now, rangeName)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var visited []uint
			reader := relayUsageTotalFunc(func(gotCtx context.Context, stationID uint, gotRange string, gotSince time.Time) (relay.UsageSummary, error) {
				if gotCtx != ctx || gotRange != rangeName || !gotSince.Equal(since) {
					t.Fatalf("usage request lost context or range: %q, %s", gotRange, gotSince)
				}
				visited = append(visited, stationID)
				return usage[stationID], nil
			})
			total, err := dashboardUserChargeTotal(ctx, reader, stations, rangeName, since)
			if err != nil || total != 20 {
				t.Fatalf("user charge = %v, err = %v; want 20 and no error", total, err)
			}
			if !reflect.DeepEqual(visited, []uint{1, 2, 3}) {
				t.Fatalf("queried stations = %v, want each station exactly once", visited)
			}
		})
	}
}

func TestDashboardUserChargeTotalHandlesUnavailableStations(t *testing.T) {
	upstreamErr := errors.New("usage unavailable")
	for _, test := range []struct {
		name     string
		stations []relayDashboardStat
		failed   map[uint]bool
		want     float64
		wantErr  bool
	}{
		{name: "no stations"},
		{name: "zero usage", stations: []relayDashboardStat{{StationID: 0}}},
		{name: "partial failure preserves other stations", stations: []relayDashboardStat{{StationID: 1}, {StationID: 2}, {StationID: 3}}, failed: map[uint]bool{2: true}, want: 4, wantErr: true},
		{name: "all stations fail", stations: []relayDashboardStat{{StationID: 1}, {StationID: 2}}, failed: map[uint]bool{1: true, 2: true}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			reader := relayUsageTotalFunc(func(_ context.Context, stationID uint, _ string, _ time.Time) (relay.UsageSummary, error) {
				calls++
				if test.failed[stationID] {
					return relay.UsageSummary{UserCharge: 100}, upstreamErr
				}
				return relay.UsageSummary{UserCharge: float64(stationID)}, nil
			})
			total, err := dashboardUserChargeTotal(context.Background(), reader, test.stations, "today", time.Now())
			if total != test.want || (err != nil) != test.wantErr {
				t.Fatalf("user charge = %v, err = %v; want %v, error %v", total, err, test.want, test.wantErr)
			}
			if test.wantErr && !errors.Is(err, upstreamErr) {
				t.Fatalf("error %v does not preserve upstream failure", err)
			}
			if calls != len(test.stations) {
				t.Fatalf("queried %d stations, want %d", calls, len(test.stations))
			}
		})
	}
}

func TestDashboardUserChargeBreakdown(t *testing.T) {
	for _, test := range []struct {
		name                string
		total               float64
		totalComplete       bool
		usage               map[uint]relay.ChannelUsageTotal
		wantChannel         float64
		wantChannelComplete bool
		wantUnlinked        float64
		wantUnavailable     bool
	}{
		{
			name: "splits channel and temporary account charges", total: 20, totalComplete: true,
			usage: map[uint]relay.ChannelUsageTotal{
				1: {UserCharge: 7.5, Cost: 100, Complete: true},
				2: {UserCharge: 2.5, Cost: 200, Complete: true},
			},
			wantChannel: 10, wantChannelComplete: true, wantUnlinked: 10,
		},
		{name: "only unlinked accounts", total: 20, totalComplete: true, wantChannelComplete: true, wantUnlinked: 20},
		{name: "no usage", totalComplete: true, wantChannelComplete: true},
		{
			name: "only channel accounts", total: 20, totalComplete: true,
			usage:       map[uint]relay.ChannelUsageTotal{1: {UserCharge: 20, Complete: true}},
			wantChannel: 20, wantChannelComplete: true,
		},
		{
			name: "failed station cannot be classified as unlinked", total: 20,
			usage:       map[uint]relay.ChannelUsageTotal{1: {UserCharge: 5, Complete: true}},
			wantChannel: 5, wantChannelComplete: true, wantUnavailable: true,
		},
		{
			name: "missing channel usage cannot be classified as unlinked", total: 20, totalComplete: true,
			usage: map[uint]relay.ChannelUsageTotal{
				1: {UserCharge: 5, Complete: true},
				2: {UserCharge: 3, Complete: false},
			},
			wantChannel: 8, wantUnavailable: true,
		},
		{
			name: "inconsistent samples cannot produce negative unlinked charges", total: 20, totalComplete: true,
			usage:       map[uint]relay.ChannelUsageTotal{1: {UserCharge: 25, Complete: true}},
			wantChannel: 25, wantChannelComplete: true, wantUnavailable: true,
		},
		{
			name: "floating point rounding is zero unlinked usage", total: 0.3, totalComplete: true,
			usage:       map[uint]relay.ChannelUsageTotal{1: {UserCharge: 0.1, Complete: true}, 2: {UserCharge: 0.2, Complete: true}},
			wantChannel: 0.3, wantChannelComplete: true,
		},
		{
			// 被排除的账号若已关联渠道：合计与渠道侧同时少掉它的扣费，未关联金额不变。
			name: "excluded bound charge keeps unlinked amount stable", total: 15, totalComplete: true,
			usage:       map[uint]relay.ChannelUsageTotal{1: {UserCharge: 5, Complete: true}},
			wantChannel: 5, wantChannelComplete: true, wantUnlinked: 10,
		},
		{
			// 被排除的账号若未关联渠道：只有合计少掉它的扣费，未关联金额同步减少。
			name: "excluded unbound charge reduces unlinked amount", total: 15, totalComplete: true,
			usage:       map[uint]relay.ChannelUsageTotal{1: {UserCharge: 10, Complete: true}},
			wantChannel: 10, wantChannelComplete: true, wantUnlinked: 5,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := dashboardUserChargeBreakdown(test.total, test.totalComplete, test.usage)
			if math.Abs(got.ChannelAmount-test.wantChannel) > 1e-9 || got.ChannelComplete != test.wantChannelComplete {
				t.Fatalf("channel breakdown = %#v, want amount %v and complete %v", got, test.wantChannel, test.wantChannelComplete)
			}
			if test.wantUnavailable {
				if got.UnlinkedAmount != nil {
					t.Fatalf("unlinked amount = %v, want unavailable", *got.UnlinkedAmount)
				}
			} else {
				if got.UnlinkedAmount == nil || math.Abs(*got.UnlinkedAmount-test.wantUnlinked) > 1e-9 {
					t.Fatalf("unlinked amount = %v, want %v", got.UnlinkedAmount, test.wantUnlinked)
				}
				if math.Abs(got.ChannelAmount+*got.UnlinkedAmount-test.total) > 1e-9 {
					t.Fatalf("breakdown does not add up to total %v: %#v", test.total, got)
				}
			}
		})
	}
}

func TestDashboardUnlinkedAccountsViewSumsOnlyCountedCharges(t *testing.T) {
	accounts := []relay.UnlinkedAccountCharge{
		{StationID: 1, AccountExternalID: 101, AccountName: "临时号", UserCharge: 8},
		{StationID: 1, AccountExternalID: 102, AccountName: "凡人", UserCharge: 5, UserChargeExcluded: true},
		{StationID: 2, AccountExternalID: 201, AccountName: "福利号", UserCharge: 2},
	}
	view := dashboardUnlinkedAccountsView("today", accounts, nil)
	if view.RawAmount != 15 || view.TotalAmount != 10 {
		t.Fatalf("amounts = raw %v, counted %v; want 15 and 10", view.RawAmount, view.TotalAmount)
	}
	if !view.Complete || view.Range != "today" || len(view.Accounts) != 3 {
		t.Fatalf("view = %#v, want complete today view with 3 accounts", view)
	}

	// 读取失败的账号仍展示，但不计入 KPI 合计，并把明细标记为不完整。
	accounts[2].Failed = true
	view = dashboardUnlinkedAccountsView("7d", accounts, errors.New("station 2: boom"))
	if view.Complete || view.TotalAmount != 8 || view.RawAmount != 15 {
		t.Fatalf("view = %#v, want incomplete view counting 8 of 15", view)
	}

	empty := dashboardUnlinkedAccountsView("today", nil, nil)
	if empty.Accounts == nil || len(empty.Accounts) != 0 || !empty.Complete {
		t.Fatalf("empty view = %#v, want a non-nil empty account list", empty)
	}
}

func TestDashboardSinceAt(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 9, 15, 30, 0, 0, location)

	tests := []struct {
		name      string
		raw       string
		wantSince time.Time
		wantRange string
	}{
		{name: "today", raw: "today", wantSince: time.Date(2026, time.August, 9, 0, 0, 0, 0, location), wantRange: "today"},
		{name: "rolling 24 hours", raw: "24h", wantSince: time.Date(2026, time.August, 8, 15, 30, 0, 0, location), wantRange: "24h"},
		{name: "seven calendar days", raw: "7d", wantSince: time.Date(2026, time.August, 3, 0, 0, 0, 0, location), wantRange: "7d"},
		{name: "thirty calendar days", raw: "30d", wantSince: time.Date(2026, time.July, 11, 0, 0, 0, 0, location), wantRange: "30d"},
		{name: "all time", raw: "all", wantSince: time.Time{}, wantRange: "all"},
		{name: "unknown falls back to today", raw: "unexpected", wantSince: time.Date(2026, time.August, 9, 0, 0, 0, 0, location), wantRange: "today"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotSince, gotRange := dashboardSinceAt(now, test.raw)
			if !gotSince.Equal(test.wantSince) {
				t.Fatalf("since = %s, want %s", gotSince, test.wantSince)
			}
			if gotRange != test.wantRange {
				t.Fatalf("range = %q, want %q", gotRange, test.wantRange)
			}
		})
	}
}

func TestRelayAdjustmentGroupNames(t *testing.T) {
	groups := []storage.RelayGroup{{ExternalID: 10, Name: "标准组"}, {ExternalID: 20, Name: "高倍率组"}}
	if got := fmt.Sprint(relayAdjustmentGroupNames([]int64{10, 99, 20}, groups)); got != "[标准组 #99 高倍率组]" {
		t.Fatalf("names = %s", got)
	}
}

func TestDashboardCumulativeRechargeUsesCurrentChannels(t *testing.T) {
	channels := []storage.Channel{{ID: 1}, {ID: 2}}
	amounts := map[uint]float64{1: 120.5, 2: 79.5, 99: 500}
	if got := dashboardCumulativeRecharge(channels, amounts); got != 200 {
		t.Fatalf("total = %v, want 200", got)
	}
}

func TestFailRelayGroupDependencyUsesActionableStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		err        error
		statusCode int
		wantCode   string
	}{
		{name: "missing group key", err: relay.ErrGroupAdminAPIKeyMissing, statusCode: http.StatusUnprocessableEntity, wantCode: "group_admin_api_key_missing"},
		{name: "other upstream error", err: errors.New("HTTP 502"), statusCode: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			failRelayGroupDependency(ctx, test.err)
			if recorder.Code != test.statusCode {
				t.Fatalf("status = %d, want %d", recorder.Code, test.statusCode)
			}
			if test.wantCode == "" {
				return
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != test.wantCode {
				t.Fatalf("code = %q, want %q", body.Code, test.wantCode)
			}
		})
	}
}

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

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

package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMonitorBucketsKeepAllRequestsAndFiveMinuteBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	first := int64(1000)
	usage := make([]monitorEvent, 0, 81)
	for index := 0; index < 80; index++ {
		usage = append(usage, monitorEvent{success: true, createdAt: now.Add(-time.Minute), requestID: fmt.Sprint(index), firstTokenMS: &first, cacheReadTokens: 50, inputTokens: 50})
	}
	usage = append(usage, monitorEvent{success: true, createdAt: time.Date(2026, 10, 21, 10, 35, 0, 0, time.UTC)})
	failures := []monitorEvent{
		{createdAt: now.Add(-time.Minute), requestID: "0"}, // recovered by a successful request
		{createdAt: now.Add(-time.Minute), requestID: "failed"},
		{createdAt: now.Add(-2 * time.Minute), requestID: "failed"}, // one final failure, not two retries
		{createdAt: now.Add(time.Minute), requestID: "future"},
	}
	buckets, cache, latency := buildMonitorBuckets(usage, failures, true, now)
	if len(buckets) != 18 {
		t.Fatalf("bucket count = %d", len(buckets))
	}
	if buckets[0].StartAt.Format("15:04") != "10:35" || buckets[0].SuccessCount != 1 {
		t.Fatalf("first bucket = %#v", buckets[0])
	}
	last := buckets[17]
	if last.Total != 81 || last.SuccessCount != 80 || last.FailureCount != 1 {
		t.Fatalf("last bucket = %#v", last)
	}
	if cache == nil || *cache != 0.5 || latency == nil || *latency != 1000 {
		t.Fatalf("cache=%v latency=%v", cache, latency)
	}
	if last.Availability == nil || *last.Availability != float64(80)/81 {
		t.Fatalf("availability = %v", last.Availability)
	}
	if !buckets[1].DataComplete || buckets[1].Availability != nil {
		t.Fatal("idle bucket must have no availability, not 100%")
	}
	partial, _, _ := buildMonitorBuckets(usage, nil, false, now)
	for _, bucket := range partial {
		if bucket.DataComplete {
			t.Fatal("partial collection reported complete")
		}
	}
}

func TestMonitorWindowRangesUseExpectedAlignedBuckets(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	tests := []struct {
		rangeKey string
		count int
		bucket time.Duration
	}{
		{rangeKey: "90m", count: 18, bucket: 5 * time.Minute},
		{rangeKey: "24h", count: 24, bucket: time.Hour},
		{rangeKey: "7d", count: 28, bucket: 6 * time.Hour},
		{rangeKey: "30d", count: 30, bucket: 24 * time.Hour},
	}
	for _, test := range tests {
		t.Run(test.rangeKey, func(t *testing.T) {
			config, ok := monitorWindowConfigFor(test.rangeKey)
			if !ok || config.bucketCount != test.count || config.bucketDuration != test.bucket {
				t.Fatalf("config = %#v, valid = %v", config, ok)
			}
			start := monitorWindowStart(now, config)
			wantEnd := now.Truncate(test.bucket).Add(test.bucket)
			if gotEnd := start.Add(time.Duration(test.count) * test.bucket); !gotEnd.Equal(wantEnd) {
				t.Fatalf("window end = %s, want %s", gotEnd, wantEnd)
			}
			if _, valid := NormalizePublicMonitorRange(test.rangeKey); !valid {
				t.Fatal("supported range was rejected")
			}
		})
	}
	if _, valid := NormalizePublicMonitorRange("12h"); valid {
		t.Fatal("unsupported range was accepted")
	}
}

func TestMonitorBucketIncludesExactCurrentTime(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	usage := []monitorEvent{{success: true, createdAt: now}}
	config, _ := monitorWindowConfigFor("90m")
	buckets, _, _ := buildMonitorBucketsForConfig(usage, nil, true, now, config)
	if got := buckets[len(buckets)-1].Total; got != 1 {
		t.Fatalf("last bucket total = %d, want current-time request included", got)
	}
}

func TestFetchMonitorWindowCollectsMultiplePagesAndIgnoresOtherGroups(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("x-api-key") == "" {
			t.Error("missing upstream admin auth")
		}
		if r.URL.Query().Get("group_id") != "28" {
			t.Error("missing group filter")
		}
		page := remoteUsagePage{Total: 3, Pages: 2}
		if r.URL.Query().Get("page") == "1" {
			page.Items = []remoteUsage{{RequestID: "a", GroupID: 28, CreatedAt: now.Format(time.RFC3339)}, {RequestID: "not-this-group", GroupID: 29, CreatedAt: now.Format(time.RFC3339)}}
		} else {
			page.Items = []remoteUsage{{RequestID: "b", GroupID: 28, CreatedAt: now.Add(-time.Minute).Format(time.RFC3339)}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": page})
	}))
	defer server.Close()
	service := &Service{client: server.Client()}
	events, err := service.fetchMonitorWindow(context.Background(), server.URL, "local-test-key", 28, monitorFetchUsage, now)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(events) != 2 {
		t.Fatalf("requests=%d events=%d", requests, len(events))
	}
}

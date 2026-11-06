package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMonitorBucketsKeepAllRequestsAndFiveMinuteBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	first := int64(1000)
	usage := make([]monitorEvent, 0, 82)
	for index := 0; index < 80; index++ {
		readTokens := int64(100_000)
		if index >= 40 {
			readTokens = 300_000
		}
		usage = append(usage, monitorEvent{success: true, createdAt: now.Add(-time.Duration(80-index) * time.Second), requestID: fmt.Sprint(index), firstTokenMS: &first, stream: true, cacheReadTokens: readTokens, inputTokens: 100_000})
	}
	usage = append(usage, monitorEvent{success: true, createdAt: now.Add(-time.Minute), requestID: "sync-with-large-cache", cacheReadTokens: 100_000, inputTokens: 100_000})
	usage = append(usage, monitorEvent{success: true, createdAt: time.Date(2026, 10, 21, 10, 35, 0, 0, time.UTC)})
	failures := []monitorEvent{
		{createdAt: now.Add(-time.Minute), requestID: "0"}, // recovered by a successful request
		{createdAt: now.Add(-time.Minute), requestID: "failed"},
		{createdAt: now.Add(-2 * time.Minute), requestID: "failed"}, // one final failure, not two retries
		{createdAt: now.Add(time.Minute), requestID: "future"},
	}
	buckets, cache, latency, cacheSummary := buildMonitorBuckets(usage, failures, true, now)
	if len(buckets) != 18 {
		t.Fatalf("bucket count = %d", len(buckets))
	}
	if buckets[0].StartAt.Format("15:04") != "10:35" || buckets[0].SuccessCount != 1 {
		t.Fatalf("first bucket = %#v", buckets[0])
	}
	last := buckets[17]
	if last.Total != 82 || last.SuccessCount != 81 || last.FailureCount != 1 {
		t.Fatalf("last bucket = %#v", last)
	}
	if cache == nil || *cache != 0.75 || latency == nil || *latency != 1000 {
		t.Fatalf("cache=%v latency=%v", cache, latency)
	}
	if cacheSummary.SampleCount != 80 || cacheSummary.PromptTokens != 24_000_000 || cacheSummary.ReadTokens != 16_000_000 {
		t.Fatalf("window cache samples = %d/%d/%d", cacheSummary.SampleCount, cacheSummary.PromptTokens, cacheSummary.ReadTokens)
	}
	if cacheSummary.RatePromptTokens != 8_000_000 || cacheSummary.RateReadTokens != 6_000_000 || cacheSummary.RecentSampleCount != recentCacheSampleRequests {
		t.Fatalf("selected recent cache summary = %#v", cacheSummary)
	}
	if last.Availability == nil || *last.Availability != float64(81)/82 {
		t.Fatalf("availability = %v", last.Availability)
	}
	if !buckets[1].DataComplete || buckets[1].Availability != nil {
		t.Fatal("idle bucket must have no availability, not 100%")
	}
	partial, _, _, _ := buildMonitorBuckets(usage, nil, false, now)
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
		count    int
		bucket   time.Duration
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

func TestCacheSampleStatusUsesSelectedWindowThresholds(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	makeUsage := func(count int, promptTokens int64) []monitorEvent {
		usage := make([]monitorEvent, count)
		for index := range usage {
			usage[index] = monitorEvent{success: true, stream: true, createdAt: now.Add(-time.Minute)}
		}
		for index := range usage {
			usage[index].inputTokens = promptTokens / int64(count)
		}
		usage[0].inputTokens += promptTokens % int64(count)
		return usage
	}
	assertStatus := func(name string, usage []monitorEvent, complete bool, want string) {
		t.Helper()
		_, cache, _, summary := buildMonitorBuckets(usage, nil, complete, now)
		got := cacheSampleStatus(summary.SampleCount, summary.PromptTokens, complete)
		if got != want {
			t.Fatalf("%s status = %q, want %q (rate=%v samples=%d tokens=%d)", name, got, want, cache, summary.SampleCount, summary.PromptTokens)
		}
		if want != cacheSampleStatusReady && cache != nil {
			t.Fatalf("%s unexpectedly returned cache rate %v", name, cache)
		}
		if want == cacheSampleStatusReady && cache == nil {
			t.Fatalf("%s did not return a cache rate", name)
		}
	}

	assertStatus("50 requests", makeUsage(50, 10_000_001), true, cacheSampleStatusInsufficient)
	assertStatus("50 requests plus more tokens", makeUsage(50, 11_000_000), true, cacheSampleStatusInsufficient)
	assertStatus("51 requests at token boundary", makeUsage(51, 10_000_000), true, cacheSampleStatusInsufficient)
	usage := makeUsage(51, 10_000_001)
	assertStatus("above both thresholds", usage, true, cacheSampleStatusReady)
	assertStatus("incomplete usage window", usage, false, cacheSampleStatusIncomplete)
}

func TestCacheThresholdAppliesAcrossSelectedWindowAndBucketsExposeNoCacheFields(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	config, _ := monitorWindowConfigFor("90m")
	usage := make([]monitorEvent, 0, minCacheSampleRequests+1)
	for index := 0; index < minCacheSampleRequests+1; index++ {
		usage = append(usage, monitorEvent{
			success: true, stream: true,
			createdAt:   now.Add(-time.Duration(index%4) * 5 * time.Minute),
			inputTokens: (minCachePromptTokens + 1) / int64(minCacheSampleRequests+1),
		})
	}
	usage[len(usage)-1].inputTokens += (minCachePromptTokens + 1) % int64(minCacheSampleRequests+1)
	buckets, cache, _, summary := buildMonitorBucketsForConfig(usage, nil, true, true, now, config)
	if summary.SampleCount != minCacheSampleRequests+1 || summary.PromptTokens != minCachePromptTokens+1 || cache == nil {
		t.Fatalf("selected-window cache summary = samples %d, tokens %d, rate %v", summary.SampleCount, summary.PromptTokens, cache)
	}
	for _, bucket := range buckets {
		if bucket.Total > 0 && bucket.Total >= minCacheSampleRequests {
			t.Fatalf("fixture unexpectedly placed enough requests in one bucket: %#v", bucket)
		}
	}
	data, err := json.Marshal(buckets)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"cache_rate"`, `"cache_sample_count"`, `"cache_prompt_tokens"`, `"cache_read_tokens"`, `"cache_sample_status"`} {
		if strings.Contains(string(data), field) {
			t.Fatalf("bucket JSON exposed cache field %s: %s", field, data)
		}
	}

}

func TestRecentCacheRateUsesNewestTwentyEligibleSamples(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	samples := make([]cacheRateSample, 0, 60)
	for index := 0; index < 60; index++ {
		readTokens := int64(50_000)
		createdAt := now.Add(-time.Duration(100-index) * time.Second)
		if index >= 40 {
			readTokens = 150_000
			createdAt = now.Add(-time.Duration(20-(index-40)) * time.Second)
		}
		samples = append(samples, cacheRateSample{createdAt: createdAt, promptTokens: 200_000, readTokens: readTokens})
	}

	summary := summarizeCacheRateSamples(samples, true)
	if summary.Rate == nil || *summary.Rate != 0.75 {
		t.Fatalf("selected cache rate = %v, want 75%%", summary.Rate)
	}
	if summary.SampleCount != 60 || summary.PromptTokens != 12_000_000 || summary.ReadTokens != 5_000_000 {
		t.Fatalf("window totals = %#v", summary)
	}
	if summary.RecentSampleCount != recentCacheSampleRequests || summary.RecentPromptTokens != 4_000_000 || summary.RecentReadTokens != 3_000_000 {
		t.Fatalf("recent batch = %#v", summary)
	}
	if summary.RatePromptTokens != summary.RecentPromptTokens || summary.RateReadTokens != summary.RecentReadTokens {
		t.Fatalf("display-rate weights do not match recent batch: %#v", summary)
	}
}

func TestRecentCacheRateFallsBackToOverallWhenNotHigher(t *testing.T) {
	for _, test := range []struct {
		name       string
		oldRead    int64
		recentRead int64
		wantRate   float64
	}{
		{name: "equal", oldRead: 100_000, recentRead: 100_000, wantRate: 0.5},
		{name: "lower", oldRead: 150_000, recentRead: 50_000, wantRate: float64(7) / 12},
	} {
		t.Run(test.name, func(t *testing.T) {
			samples := make([]cacheRateSample, 0, 60)
			for index := 0; index < 60; index++ {
				readTokens := test.oldRead
				if index >= 40 {
					readTokens = test.recentRead
				}
				samples = append(samples, cacheRateSample{createdAt: time.Unix(int64(index), 0), promptTokens: 200_000, readTokens: readTokens})
			}
			summary := summarizeCacheRateSamples(samples, true)
			if summary.Rate == nil || *summary.Rate != test.wantRate {
				t.Fatalf("cache rate = %v, want %v", summary.Rate, test.wantRate)
			}
			if summary.RatePromptTokens != summary.PromptTokens || summary.RateReadTokens != summary.ReadTokens {
				t.Fatalf("fallback should use overall token weights: %#v", summary)
			}
		})
	}
}

func TestRecentBatchCannotBypassOverallCacheThreshold(t *testing.T) {
	samples := make([]cacheRateSample, 0, minCacheSampleRequests+1)
	for index := 0; index < minCacheSampleRequests+1; index++ {
		promptTokens := int64(196_078)
		if index == 0 {
			promptTokens = 196_100
		}
		readTokens := int64(0)
		if index >= minCacheSampleRequests+1-recentCacheSampleRequests {
			readTokens = promptTokens
		}
		samples = append(samples, cacheRateSample{createdAt: time.Unix(int64(index), 0), promptTokens: promptTokens, readTokens: readTokens})
	}
	summary := summarizeCacheRateSamples(samples, true)
	if summary.PromptTokens != minCachePromptTokens {
		t.Fatalf("fixture prompt tokens = %d, want exact threshold %d", summary.PromptTokens, minCachePromptTokens)
	}
	if summary.Rate != nil || cacheSampleStatus(summary.SampleCount, summary.PromptTokens, true) != cacheSampleStatusInsufficient {
		t.Fatalf("recent high cache rate bypassed overall threshold: %#v", summary)
	}
}

func TestCacheSamplesRespectSelectedWindowAndEligibility(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	config, _ := monitorWindowConfigFor("90m")
	usage := make([]monitorEvent, 0, minCacheSampleRequests+6)
	for index := 0; index < minCacheSampleRequests+1; index++ {
		usage = append(usage, monitorEvent{success: true, stream: true, createdAt: now.Add(-30 * time.Minute), inputTokens: 200_000, cacheReadTokens: 100_000})
	}
	usage = append(usage,
		monitorEvent{success: true, stream: true, createdAt: monitorWindowStart(now, config).Add(-time.Second), inputTokens: 50_000_000, cacheReadTokens: 50_000_000},
		monitorEvent{success: true, createdAt: now.Add(-time.Minute), inputTokens: 50_000_000, cacheReadTokens: 50_000_000},
		monitorEvent{success: false, stream: true, createdAt: now.Add(-time.Minute), inputTokens: 50_000_000, cacheReadTokens: 50_000_000},
		monitorEvent{success: true, stream: true, model: "gpt-5.6-luna", createdAt: now.Add(-time.Minute), inputTokens: 50_000_000, cacheReadTokens: 50_000_000},
		monitorEvent{success: true, stream: true, createdAt: now.Add(-time.Minute)},
	)

	_, rate, _, summary := buildMonitorBucketsForConfig(usage, nil, true, true, now, config)
	if summary.SampleCount != minCacheSampleRequests+1 || summary.PromptTokens != 15_300_000 || summary.ReadTokens != 5_100_000 {
		t.Fatalf("selected-window eligible samples = %#v", summary)
	}
	if rate == nil || *rate != float64(5_100_000)/15_300_000 {
		t.Fatalf("cache rate = %v, want one third", rate)
	}
}

func TestCacheSamplesExcludeLunaModelsWithoutExcludingRequests(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	usage := make([]monitorEvent, 0, minCacheSampleRequests+3)
	for index := 0; index < minCacheSampleRequests+1; index++ {
		usage = append(usage, monitorEvent{success: true, createdAt: now.Add(-time.Minute), stream: true, inputTokens: 200_000, cacheReadTokens: 200_001})
	}
	usage = append(usage,
		monitorEvent{success: true, createdAt: now.Add(-time.Minute), model: "gpt-5.6-luna", stream: true, inputTokens: 1_000_000, cacheReadTokens: 1_000_000},
		monitorEvent{success: true, createdAt: now.Add(-time.Minute), model: " GPT-6-LUNA ", stream: true, inputTokens: 1_000_000, cacheReadTokens: 1_000_000},
	)

	buckets, cache, _, summary := buildMonitorBuckets(usage, nil, true, now)
	last := buckets[len(buckets)-1]
	if last.Total != minCacheSampleRequests+3 || last.SuccessCount != minCacheSampleRequests+3 {
		t.Fatalf("request counts include excluded cache models: total=%d success=%d", last.Total, last.SuccessCount)
	}
	if summary.SampleCount != minCacheSampleRequests+1 || summary.PromptTokens != 20_400_051 || summary.ReadTokens != 10_200_051 {
		t.Fatalf("cache samples include excluded models: samples=%d prompt=%d read=%d", summary.SampleCount, summary.PromptTokens, summary.ReadTokens)
	}
	wantRate := float64(summary.ReadTokens) / float64(summary.PromptTokens)
	if cache == nil || *cache != wantRate {
		t.Fatalf("cache rate = %v, want %v", cache, wantRate)
	}
}

func TestCacheSamplesRemainCompleteWhenErrorCollectionIsIncomplete(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	usage := make([]monitorEvent, 0, minCacheSampleRequests+1)
	for index := 0; index < minCacheSampleRequests+1; index++ {
		usage = append(usage, monitorEvent{success: true, stream: true, createdAt: now.Add(-time.Minute), inputTokens: 200_000})
	}
	config, _ := monitorWindowConfigFor("90m")
	buckets, cache, _, summary := buildMonitorBucketsForConfig(usage, nil, false, true, now, config)
	last := buckets[len(buckets)-1]
	cacheStatus := cacheSampleStatus(summary.SampleCount, summary.PromptTokens, true)
	if last.DataComplete || cacheStatus != cacheSampleStatusReady || cache == nil || *cache != 0 {
		t.Fatalf("monitor complete=%v cache status=%q rate=%v", last.DataComplete, cacheStatus, cache)
	}
}

func TestMonitorBucketIncludesExactCurrentTime(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 3, 0, 0, time.UTC)
	usage := []monitorEvent{{success: true, createdAt: now}}
	config, _ := monitorWindowConfigFor("90m")
	buckets, _, _, _ := buildMonitorBucketsForConfig(usage, nil, true, true, now, config)
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

package relay

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const monitorBucketDuration = 5 * time.Minute
const monitorBucketCount = 18
const monitorWindowPageSize = 200
const monitorWindowMaxPages = 50

const publicMonitorDefaultRange = "90m"

type monitorWindowConfig struct {
	key            string
	windowDuration time.Duration
	bucketDuration time.Duration
	bucketCount    int
}

func monitorWindowConfigFor(raw string) (monitorWindowConfig, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "90m":
		return monitorWindowConfig{key: "90m", windowDuration: 90 * time.Minute, bucketDuration: 5 * time.Minute, bucketCount: 18}, true
	case "24h":
		return monitorWindowConfig{key: "24h", windowDuration: 24 * time.Hour, bucketDuration: time.Hour, bucketCount: 24}, true
	case "7d":
		return monitorWindowConfig{key: "7d", windowDuration: 7 * 24 * time.Hour, bucketDuration: 6 * time.Hour, bucketCount: 28}, true
	case "30d":
		return monitorWindowConfig{key: "30d", windowDuration: 30 * 24 * time.Hour, bucketDuration: 24 * time.Hour, bucketCount: 30}, true
	default:
		return monitorWindowConfig{}, false
	}
}

// NormalizePublicMonitorRange validates and canonicalizes the public range query.
func NormalizePublicMonitorRange(raw string) (string, bool) {
	config, ok := monitorWindowConfigFor(raw)
	return config.key, ok
}

func monitorWindowStart(now time.Time, config monitorWindowConfig) time.Time {
	return now.Truncate(config.bucketDuration).Add(-time.Duration(config.bucketCount-1) * config.bucketDuration)
}

type PublicGroupMonitorBucket struct {
	StartAt      time.Time `json:"start_at"`
	EndAt        time.Time `json:"end_at"`
	Total        int       `json:"total"`
	SuccessCount int       `json:"success_count"`
	FailureCount int       `json:"failure_count"`
	Availability *float64  `json:"availability,omitempty"`
	CacheRate    *float64  `json:"cache_rate,omitempty"`
	FirstTokenMS *int64    `json:"first_token_ms,omitempty"`
	DurationMS   *int64    `json:"duration_ms,omitempty"`
	DataComplete bool      `json:"data_complete"`
}

// Read the requested window, rather than treating the most recent calls as a
// complete time series. A bounded fetch is reported as incomplete, never healthy.
func (s *Service) fetchMonitorWindow(ctx context.Context, baseURL, key string, groupID int64, kind monitorFetchKind, now time.Time) ([]monitorEvent, error) {
	config, _ := monitorWindowConfigFor(publicMonitorDefaultRange)
	return s.fetchMonitorWindowForConfig(ctx, baseURL, key, groupID, kind, now, config)
}

func (s *Service) fetchMonitorWindowForConfig(ctx context.Context, baseURL, key string, groupID int64, kind monitorFetchKind, now time.Time, config monitorWindowConfig) ([]monitorEvent, error) {
	since := monitorWindowStart(now, config)
	events := []monitorEvent{}
	path := "/api/v1/admin/usage"
	if kind == monitorFetchErrors {
		path = "/api/v1/admin/ops/request-errors"
	}
	seen := map[string]struct{}{}
	fetched := 0
	for pageNumber := 1; pageNumber <= monitorWindowMaxPages; pageNumber++ {
		query := url.Values{
			"group_id":   {strconv.FormatInt(groupID, 10)},
			"page":       {strconv.Itoa(pageNumber)},
			"page_size":  {strconv.Itoa(monitorWindowPageSize)},
			"start_date": {since.In(time.Local).Format("2006-01-02")},
			"end_date":   {now.In(time.Local).Format("2006-01-02")},
			"sort_by":    {"created_at"},
			"sort_order": {"desc"},
		}
		endpoint := strings.TrimRight(baseURL, "/") + path + "?" + query.Encode()
		var batch []monitorEvent
		var timestamps []string
		var count, total, pages int
		if kind == monitorFetchUsage {
			var page remoteUsagePage
			if err := s.get(ctx, endpoint, key, &page); err != nil {
				return events, err
			}
			count, total, pages = len(page.Items), page.Total, page.Pages
			for _, item := range page.Items {
				if item.GroupID == 0 || item.GroupID == groupID {
					batch = append(batch, usageMonitorEvents([]remoteUsage{item})...)
				}
				timestamps = append(timestamps, item.CreatedAt)
			}
		} else {
			var page remoteRequestErrorPage
			if err := s.get(ctx, endpoint, key, &page); err != nil {
				return events, err
			}
			count, total = len(page.Items), int(page.Total)
			batch = errorMonitorEvents(page.Items, groupID)
			for _, item := range page.Items {
				timestamps = append(timestamps, item.CreatedAt)
			}
		}
		fetched += count
		reachedStart := false
		for _, stamp := range timestamps {
			if at, ok := parseMonitorTime(stamp); ok && at.Before(since) {
				reachedStart = true
			}
		}
		for _, event := range batch {
			if event.createdAt.Before(since) || event.createdAt.After(now) {
				continue
			}
			id := monitorEventRequestGroupKey(monitorEventRequestKeys(event))
			if id != "" {
				if _, ok := seen[id]; ok {
					continue
				}
				seen[id] = struct{}{}
			}
			events = append(events, event)
		}
		if count == 0 || reachedStart || (pages > 0 && pageNumber >= pages) || (pages == 0 && total > 0 && fetched >= total) || (pages == 0 && total == 0 && count < monitorWindowPageSize) {
			return events, nil
		}
	}
	return events, fmt.Errorf("monitor window exceeds pagination limit")
}

func buildMonitorBuckets(usage, failures []monitorEvent, complete bool, now time.Time) ([]PublicGroupMonitorBucket, *float64, *int64) {
	config, _ := monitorWindowConfigFor(publicMonitorDefaultRange)
	return buildMonitorBucketsForConfig(usage, failures, complete, now, config)
}

func buildMonitorBucketsForConfig(usage, failures []monitorEvent, complete bool, now time.Time, config monitorWindowConfig) ([]PublicGroupMonitorBucket, *float64, *int64) {
	start := monitorWindowStart(now, config)
	buckets := make([]PublicGroupMonitorBucket, config.bucketCount)
	for index := range buckets {
		buckets[index] = PublicGroupMonitorBucket{
			StartAt:      start.Add(time.Duration(index) * config.bucketDuration),
			EndAt:        start.Add(time.Duration(index+1) * config.bucketDuration),
			DataComplete: complete,
		}
	}
	firstSums := make([]int64, config.bucketCount)
	durationSums := make([]int64, config.bucketCount)
	firstCounts := make([]int64, config.bucketCount)
	durationCounts := make([]int64, config.bucketCount)
	inputs := make([]int64, config.bucketCount)
	cacheReads := make([]int64, config.bucketCount)
	var allFirst, allFirstCount, allInputs, allCache int64
	events := append(append([]monitorEvent{}, usage...), filterRecoveredMonitorFailures(usage, failures)...)
	for _, event := range events {
		if event.createdAt.Before(start) || event.createdAt.After(now) {
			continue
		}
		index := int(event.createdAt.Sub(start) / config.bucketDuration)
		if event.createdAt.Equal(now) {
			index = len(buckets) - 1
		}
		if index < 0 || index >= len(buckets) {
			continue
		}
		bucket := &buckets[index]
		bucket.Total++
		if !event.success {
			bucket.FailureCount++
			continue
		}
		bucket.SuccessCount++
		inputs[index] += event.inputTokens + event.cacheReadTokens + event.cacheCreationTokens
		cacheReads[index] += event.cacheReadTokens
		if event.firstTokenMS != nil && *event.firstTokenMS >= 0 {
			firstSums[index] += *event.firstTokenMS
			firstCounts[index]++
		}
		if event.durationMS != nil && *event.durationMS >= 0 {
			durationSums[index] += *event.durationMS
			durationCounts[index]++
		}
	}
	for index := range buckets {
		bucket := &buckets[index]
		if bucket.Total > 0 {
			value := float64(bucket.SuccessCount) / float64(bucket.Total)
			bucket.Availability = &value
		}
		if inputs[index] > 0 {
			value := float64(cacheReads[index]) / float64(inputs[index])
			bucket.CacheRate = &value
		}
		if firstCounts[index] > 0 {
			value := firstSums[index] / firstCounts[index]
			bucket.FirstTokenMS = &value
		}
		if durationCounts[index] > 0 {
			value := durationSums[index] / durationCounts[index]
			bucket.DurationMS = &value
		}
		allFirst += firstSums[index]
		allFirstCount += firstCounts[index]
		allInputs += inputs[index]
		allCache += cacheReads[index]
	}
	var rate *float64
	var first *int64
	if allInputs > 0 {
		value := float64(allCache) / float64(allInputs)
		rate = &value
	}
	if allFirstCount > 0 {
		value := allFirst / allFirstCount
		first = &value
	}
	return buckets, rate, first
}

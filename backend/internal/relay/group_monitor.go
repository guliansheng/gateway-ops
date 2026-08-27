package relay

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

const (
	groupMonitorLimit    = 60
	groupMonitorCacheTTL = 15 * time.Second
	groupMonitorWorkers  = 6
)

// PublicGroupMonitor is deliberately a small, non-sensitive projection of the
// relay station. It contains no users, accounts, API keys, IPs, or raw errors.
type PublicGroupMonitor struct {
	StationID   uint                      `json:"station_id"`
	StationName string                    `json:"station_name"`
	UpdatedAt   time.Time                 `json:"updated_at"`
	Groups      []PublicGroupMonitorGroup `json:"groups"`
	Summary     PublicGroupMonitorSummary `json:"summary"`
}

type PublicGroupMonitorSummary struct {
	Total       int `json:"total"`
	Available   int `json:"available"`
	Degraded    int `json:"degraded"`
	Unavailable int `json:"unavailable"`
	Idle        int `json:"idle"`
	Disabled    int `json:"disabled"`
	Unknown     int `json:"unknown"`
}

type PublicGroupMonitorGroup struct {
	ExternalID          int64                    `json:"external_id"`
	Name                string                   `json:"name"`
	Description         string                   `json:"description,omitempty"`
	Platform            string                   `json:"platform,omitempty"`
	RateMultiplier      float64                  `json:"rate_multiplier"`
	Enabled             bool                     `json:"enabled"`
	GroupStatus         string                   `json:"group_status,omitempty"`
	Status              string                   `json:"status"`
	DataComplete        bool                     `json:"data_complete"`
	UpdatedAt           time.Time                `json:"updated_at"`
	LatestCallAt        *time.Time               `json:"latest_call_at,omitempty"`
	SuccessCount        int                      `json:"success_count"`
	FailureCount        int                      `json:"failure_count"`
	ConsecutiveFailures int                      `json:"consecutive_failures"`
	FailureSummary      string                   `json:"failure_summary,omitempty"`
	Calls               []PublicGroupMonitorCall `json:"calls"`
}

type PublicGroupMonitorCall struct {
	Success      bool      `json:"success"`
	CreatedAt    time.Time `json:"created_at"`
	Model        string    `json:"model,omitempty"`
	FirstTokenMS *int64    `json:"first_token_ms,omitempty"`
	DurationMS   *int64    `json:"duration_ms,omitempty"`
}

type publicMonitorCacheEntry struct {
	ExpiresAt time.Time
	View      PublicGroupMonitor
}

type monitorEvent struct {
	success         bool
	createdAt       time.Time
	model           string
	requestID       string
	clientRequestID string
	firstTokenMS    *int64
	durationMS      *int64
	failureReason   string
}

type monitorModelStats struct {
	failures  int
	successes int
}

const (
	monitorFailureModelUnavailable = "模型当前不可用"
	monitorFailureUpstream         = "上游响应异常"
)

type remoteRequestError struct {
	RequestID          string `json:"request_id"`
	ClientRequestID    string `json:"client_request_id"`
	CreatedAt          string `json:"created_at"`
	ErrorPhase         string `json:"error_phase"`
	Phase              string `json:"phase"`
	Type               string `json:"type"`
	Message            string `json:"message"`
	ErrorOwner         string `json:"error_owner"`
	StatusCode         int    `json:"status_code"`
	Platform           string `json:"platform"`
	Model              string `json:"model"`
	RequestedModel     string `json:"requested_model"`
	UpstreamModel      string `json:"upstream_model"`
	GroupID            int64  `json:"group_id"`
	DurationMS         *int64 `json:"duration_ms"`
	TimeToFirstTokenMS *int64 `json:"time_to_first_token_ms"`
}

type remoteRequestErrorPage struct {
	Items []remoteRequestError `json:"items"`
}

type monitorFetchKind uint8

const (
	monitorFetchUsage monitorFetchKind = iota
	monitorFetchErrors
)

type monitorFetchResult struct {
	index  int
	kind   monitorFetchKind
	usage  []monitorEvent
	errors []monitorEvent
	err    error
}

func (s *Service) invalidatePublicMonitor(stationID uint) {
	s.publicMonitorMu.Lock()
	delete(s.publicMonitorCache, stationID)
	s.publicMonitorMu.Unlock()
}

// PublicGroupMonitor aggregates the latest successful calls and provider-side
// failures for monitored groups. Upstream failures are retained as events so a
// group that stopped working is visible even when its last successful call is
// old. The short cache prevents a public page refresh from fanning out to the
// remote admin API on every request.
func (s *Service) PublicGroupMonitor(ctx context.Context, stationID uint) (PublicGroupMonitor, error) {
	now := time.Now().UTC()
	s.publicMonitorMu.Lock()
	if cached, ok := s.publicMonitorCache[stationID]; ok && now.Before(cached.ExpiresAt) {
		s.publicMonitorMu.Unlock()
		return cached.View, nil
	}
	s.publicMonitorMu.Unlock()

	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return PublicGroupMonitor{}, err
	}
	groups, err := s.stations.ListGroups(stationID)
	if err != nil {
		return PublicGroupMonitor{}, err
	}
	view := PublicGroupMonitor{StationID: stationID, StationName: station.Name, UpdatedAt: now, Groups: []PublicGroupMonitorGroup{}}
	monitored := make([]int, 0, len(groups))
	for index := range groups {
		if groups[index].MonitorEnabled {
			monitored = append(monitored, index)
		}
	}
	if len(monitored) == 0 {
		view.Summary.Total = 0
		s.storePublicMonitor(stationID, view, now)
		return view, nil
	}

	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return PublicGroupMonitor{}, fmt.Errorf("decrypt admin API key: %w", err)
	}

	results := make(chan monitorFetchResult, len(monitored)*2)
	jobs := make(chan struct {
		index int
		kind  monitorFetchKind
	}, len(monitored)*2)
	workerCount := groupMonitorWorkers
	if workerCount > len(monitored)*2 {
		workerCount = len(monitored) * 2
	}
	var workers sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				group := groups[monitored[job.index]]
				result := monitorFetchResult{index: job.index, kind: job.kind}
				if job.kind == monitorFetchUsage {
					var page remoteUsagePage
					endpoint := fmt.Sprintf("%s/api/v1/admin/usage?page=1&page_size=%d&group_id=%d", station.BaseURL, groupMonitorLimit, group.ExternalID)
					result.err = s.get(ctx, endpoint, apiKey, &page)
					if result.err == nil {
						result.usage = usageMonitorEvents(page.Items)
					}
				} else {
					var page remoteRequestErrorPage
					endpoint := fmt.Sprintf("%s/api/v1/admin/ops/request-errors?page=1&page_size=%d&group_id=%d", station.BaseURL, groupMonitorLimit, group.ExternalID)
					result.err = s.get(ctx, endpoint, apiKey, &page)
					if result.err == nil {
						result.errors = errorMonitorEvents(page.Items, group.ExternalID)
					}
				}
				results <- result
			}
		}()
	}
	for index := range monitored {
		jobs <- struct {
			index int
			kind  monitorFetchKind
		}{index: index, kind: monitorFetchUsage}
		jobs <- struct {
			index int
			kind  monitorFetchKind
		}{index: index, kind: monitorFetchErrors}
	}
	close(jobs)
	workers.Wait()
	close(results)

	perGroup := make([]struct {
		usage, errors []monitorEvent
		usageOK       bool
		errorsOK      bool
	}, len(monitored))
	for result := range results {
		if result.kind == monitorFetchUsage {
			perGroup[result.index].usageOK = result.err == nil
			perGroup[result.index].usage = result.usage
		} else {
			perGroup[result.index].errorsOK = result.err == nil
			perGroup[result.index].errors = result.errors
		}
	}
	for index, groupIndex := range monitored {
		group := groups[groupIndex]
		item := buildPublicGroupMonitorGroup(group, perGroup[index].usage, perGroup[index].errors, perGroup[index].usageOK && perGroup[index].errorsOK, now)
		view.Groups = append(view.Groups, item)
		switch item.Status {
		case "available":
			view.Summary.Available++
		case "degraded":
			view.Summary.Degraded++
		case "unavailable":
			view.Summary.Unavailable++
		case "idle":
			view.Summary.Idle++
		case "disabled":
			view.Summary.Disabled++
		case "unknown":
			view.Summary.Unknown++
		}
	}
	view.Summary.Total = len(view.Groups)
	s.storePublicMonitor(stationID, view, now)
	return view, nil
}

func (s *Service) storePublicMonitor(stationID uint, view PublicGroupMonitor, now time.Time) {
	s.publicMonitorMu.Lock()
	s.publicMonitorCache[stationID] = publicMonitorCacheEntry{ExpiresAt: now.Add(groupMonitorCacheTTL), View: view}
	s.publicMonitorMu.Unlock()
}

func usageMonitorEvents(items []remoteUsage) []monitorEvent {
	events := make([]monitorEvent, 0, len(items))
	for _, item := range items {
		created, ok := parseMonitorTime(item.CreatedAt)
		if !ok {
			continue
		}
		model := strings.TrimSpace(item.RequestedModel)
		if model == "" {
			model = strings.TrimSpace(item.Model)
		}
		first, duration := item.FirstTokenMS, item.DurationMS
		events = append(events, monitorEvent{success: true, createdAt: created, model: model, requestID: strings.TrimSpace(item.RequestID), clientRequestID: strings.TrimSpace(item.ClientRequestID), firstTokenMS: &first, durationMS: &duration})
	}
	return events
}

func errorMonitorEvents(items []remoteRequestError, groupID int64) []monitorEvent {
	events := make([]monitorEvent, 0, len(items))
	for _, item := range items {
		if item.GroupID != 0 && item.GroupID != groupID {
			continue
		}
		owner := strings.ToLower(strings.TrimSpace(item.ErrorOwner))
		phase := strings.ToLower(strings.TrimSpace(item.ErrorPhase))
		if phase == "" {
			phase = strings.ToLower(strings.TrimSpace(item.Phase))
		}
		if owner == "client" || (owner != "provider" && phase != "upstream") {
			continue
		}
		// The admin endpoint exposes the upstream status, not the final gateway
		// status. Its message still preserves the request outcome: failover rows
		// say "Recovered upstream error ...", while a client disconnect (499)
		// has an empty message. Neither is a user-visible group failure.
		message := strings.TrimSpace(item.Message)
		if message == "" || strings.HasPrefix(strings.ToLower(message), "recovered upstream error") {
			continue
		}
		if item.StatusCode < http.StatusBadRequest || item.StatusCode >= 600 || item.StatusCode == 499 {
			continue
		}
		created, ok := parseMonitorTime(item.CreatedAt)
		if !ok {
			continue
		}
		model := strings.TrimSpace(item.RequestedModel)
		if model == "" {
			model = strings.TrimSpace(item.Model)
		}
		if model == "" {
			model = strings.TrimSpace(item.UpstreamModel)
		}
		events = append(events, monitorEvent{createdAt: created, model: model, requestID: strings.TrimSpace(item.RequestID), clientRequestID: strings.TrimSpace(item.ClientRequestID), firstTokenMS: item.TimeToFirstTokenMS, durationMS: item.DurationMS, failureReason: monitorFailureReason(item)})
	}
	return events
}

func monitorFailureReason(item remoteRequestError) string {
	detail := strings.ToLower(strings.Join([]string{item.Type, item.Message}, " "))
	containsAny := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(detail, value) {
				return true
			}
		}
		return false
	}
	switch {
	case containsAny("model_not_found", "model_not_supported", "model not found", "unsupported model", "model is not supported", "model does not exist", "does not exist or you do not have access", "不支持该模型", "未配置模型", "no available account"):
		return monitorFailureModelUnavailable
	case containsAny("insufficient", "quota", "credit", "balance", "额度不足", "余额不足"):
		return "可用额度不足"
	case item.StatusCode == http.StatusTooManyRequests || containsAny("rate limit", "too many request", "concurrency", "并发", "频率限制"):
		return "请求频率或并发已达上限"
	case item.StatusCode == http.StatusUnauthorized || item.StatusCode == http.StatusForbidden || containsAny("unauthorized", "forbidden", "invalid api key", "authentication", "permission", "凭证", "权限"):
		return "凭证或权限异常"
	case item.StatusCode == http.StatusBadRequest || item.StatusCode == http.StatusUnprocessableEntity || containsAny("invalid parameter", "invalid request", "bad request", "参数错误"):
		return "请求参数不符合要求"
	case item.StatusCode >= http.StatusInternalServerError || containsAny("timeout", "timed out", "temporarily unavailable", "connection", "network", "服务不可用", "超时"):
		return monitorFailureUpstream
	default:
		return "请求未能完成"
	}
}

func parseMonitorTime(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}

func monitorEventRequestKeys(event monitorEvent) []string {
	keys := make([]string, 0, 2)
	add := func(raw, kind string) {
		value := strings.TrimSpace(raw)
		if value == "" {
			return
		}
		key := "request:" + value
		if kind == "client" || strings.HasPrefix(strings.ToLower(value), "client:") {
			if strings.HasPrefix(strings.ToLower(value), "client:") {
				value = strings.TrimSpace(value[len("client:"):])
			}
			key = "client:" + value
		}
		for _, existing := range keys {
			if existing == key {
				return
			}
		}
		keys = append(keys, key)
	}
	add(event.requestID, "request")
	add(event.clientRequestID, "client")
	return keys
}

func monitorEventRequestGroupKey(keys []string) string {
	for _, key := range keys {
		if strings.HasPrefix(key, "client:") {
			return key
		}
	}
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// filterRecoveredMonitorFailures removes failover attempts that belong to a
// request which eventually succeeded, and collapses multiple error rows for a
// request that did end in failure to its last recorded error.
func filterRecoveredMonitorFailures(usage, failures []monitorEvent) []monitorEvent {
	completed := make(map[string]struct{}, len(usage))
	for _, event := range usage {
		for _, key := range monitorEventRequestKeys(event) {
			completed[key] = struct{}{}
		}
	}
	latest := make(map[string]monitorEvent, len(failures))
	uncorrelated := make([]monitorEvent, 0, len(failures))
	for _, event := range failures {
		keys := monitorEventRequestKeys(event)
		if len(keys) == 0 {
			uncorrelated = append(uncorrelated, event)
			continue
		}
		recovered := false
		for _, key := range keys {
			if _, ok := completed[key]; ok {
				recovered = true
				break
			}
		}
		if recovered {
			continue
		}
		key := monitorEventRequestGroupKey(keys)
		if previous, ok := latest[key]; !ok || event.createdAt.After(previous.createdAt) {
			latest[key] = event
		}
	}
	filtered := make([]monitorEvent, 0, len(uncorrelated)+len(latest))
	filtered = append(filtered, uncorrelated...)
	for _, event := range latest {
		filtered = append(filtered, event)
	}
	return filtered
}

func buildPublicGroupMonitorGroup(group storage.RelayGroup, usage, failures []monitorEvent, complete bool, now time.Time) PublicGroupMonitorGroup {
	failures = filterRecoveredMonitorFailures(usage, failures)
	events := append(append([]monitorEvent{}, usage...), failures...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].createdAt.After(events[j].createdAt) })
	if len(events) > groupMonitorLimit {
		events = events[:groupMonitorLimit]
	}
	successCount := 0
	failureCount := 0
	failureReasons := make(map[string]int)
	failureReasonOrder := make([]string, 0)
	for _, event := range events {
		if event.success {
			successCount++
		} else {
			failureCount++
			reason := strings.TrimSpace(event.failureReason)
			if reason == "" {
				reason = "请求未能完成"
			}
			if failureReasons[reason] == 0 {
				failureReasonOrder = append(failureReasonOrder, reason)
			}
			failureReasons[reason]++
		}
	}
	failureSummary := buildFailureSummary(events, failureReasons, failureReasonOrder)
	status := classifyGroupMonitor(group.Status, complete, events)
	consecutiveFailures := 0
	for _, event := range events {
		if event.success {
			break
		}
		consecutiveFailures++
	}
	var latest *time.Time
	if len(events) > 0 {
		value := events[0].createdAt
		latest = &value
	}
	calls := make([]PublicGroupMonitorCall, 0, len(events))
	for _, event := range events {
		calls = append(calls, PublicGroupMonitorCall{Success: event.success, CreatedAt: event.createdAt, Model: event.model, FirstTokenMS: event.firstTokenMS, DurationMS: event.durationMS})
	}
	return PublicGroupMonitorGroup{ExternalID: group.ExternalID, Name: group.Name, Description: group.Description, Platform: group.Platform, RateMultiplier: group.RateMultiplier, Enabled: group.MonitorEnabled, GroupStatus: group.Status, Status: status, DataComplete: complete, UpdatedAt: now, LatestCallAt: latest, SuccessCount: successCount, FailureCount: failureCount, ConsecutiveFailures: consecutiveFailures, FailureSummary: failureSummary, Calls: calls}
}

// buildFailureSummary deliberately uses only the requested model and a small
// set of safe categories. Raw provider messages, account names, credentials,
// request IDs, and internal endpoints never leave the backend.
func buildFailureSummary(events []monitorEvent, failureReasons map[string]int, failureReasonOrder []string) string {
	mainReason, mainReasonCount := mostCommonFailureReason(failureReasons, failureReasonOrder)
	modelStatsByName := make(map[string]monitorModelStats)
	modelOrder := make([]string, 0)
	for _, event := range events {
		model := strings.TrimSpace(event.model)
		if model == "" {
			continue
		}
		if _, ok := modelStatsByName[model]; !ok {
			modelOrder = append(modelOrder, model)
		}
		stats := modelStatsByName[model]
		if event.success {
			stats.successes++
		} else {
			stats.failures++
		}
		modelStatsByName[model] = stats
	}

	// An explicit model-not-available response is the strongest signal. Only
	// surface its model when it is the leading failure category, so one
	// isolated model error does not hide a larger unrelated problem.
	explicitModel, explicitCount := mostCommonUnavailableModel(events, modelOrder)
	if explicitCount > 0 && explicitCount >= mainReasonCount {
		if explicitModel != "" {
			return fmt.Sprintf("报错主要原因：%s 模型当前不可用（%d 次）", explicitModel, explicitCount)
		}
		return fmt.Sprintf("报错主要原因：请求的模型当前不可用（%d 次）", explicitCount)
	}

	// Some gateways return a generic 5xx even when every attempt for one model
	// fails. If another model succeeds in the same 60-call window, that model
	// distribution is enough to explain the user-visible problem without
	// exposing the provider's raw error text.
	totalFailures := 0
	totalSuccesses := 0
	for _, stats := range modelStatsByName {
		totalFailures += stats.failures
		totalSuccesses += stats.successes
	}
	if totalFailures >= 2 && totalSuccesses > 0 {
		candidate, candidateCount := mostCommonFailedOnlyModel(modelStatsByName, modelOrder)
		if candidate != "" && candidateCount >= 2 && candidateCount*100 >= totalFailures*60 {
			otherModelSuccess := false
			for model, stats := range modelStatsByName {
				if model != candidate && stats.successes > 0 {
					otherModelSuccess = true
					break
				}
			}
			if otherModelSuccess {
				return fmt.Sprintf("报错主要原因：%s 模型当前不可用（%d 次）", candidate, candidateCount)
			}
		}
	}

	if mainReasonCount > 0 {
		return fmt.Sprintf("报错主要原因：%s（%d 次）", mainReason, mainReasonCount)
	}
	return ""
}

func mostCommonFailureReason(counts map[string]int, order []string) (string, int) {
	mainReason := ""
	mainCount := 0
	for _, reason := range order {
		if counts[reason] > mainCount {
			mainReason = reason
			mainCount = counts[reason]
		}
	}
	return mainReason, mainCount
}

func mostCommonUnavailableModel(events []monitorEvent, order []string) (string, int) {
	counts := make(map[string]int)
	for _, event := range events {
		if event.success || strings.TrimSpace(event.failureReason) != monitorFailureModelUnavailable {
			continue
		}
		model := strings.TrimSpace(event.model)
		counts[model]++
	}
	return mostCommonModel(counts, order)
}

func mostCommonFailedOnlyModel(stats map[string]monitorModelStats, order []string) (string, int) {
	counts := make(map[string]int)
	for model, item := range stats {
		if item.failures > 0 && item.successes == 0 {
			counts[model] = item.failures
		}
	}
	return mostCommonModel(counts, order)
}

func mostCommonModel(counts map[string]int, order []string) (string, int) {
	model := ""
	count := 0
	for _, candidate := range order {
		if counts[candidate] > count {
			model = candidate
			count = counts[candidate]
		}
	}
	return model, count
}

func classifyGroupMonitor(groupStatus string, complete bool, events []monitorEvent) string {
	if !strings.EqualFold(strings.TrimSpace(groupStatus), "active") {
		return "disabled"
	}
	if !complete {
		return "unknown"
	}
	if len(events) == 0 {
		return "idle"
	}
	consecutiveFailures := 0
	for _, event := range events {
		if event.success {
			break
		}
		consecutiveFailures++
	}
	if consecutiveFailures >= 3 {
		return "unavailable"
	}
	if consecutiveFailures > 0 {
		return "degraded"
	}
	return "available"
}

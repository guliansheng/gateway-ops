package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

const (
	userRiskMaxWeight              = 100
	userRiskMaxLookbackMinutes     = 60
	userRiskMaxConcurrencyWindow   = 300
	userRiskMaxThreshold           = 100
	userRiskMaxConcurrentMinimum   = 20
	userRiskDefaultLookbackMinutes = 15
	userRiskCacheTTL               = 30 * time.Second
)

type userRiskCacheEntry struct {
	ExpiresAt time.Time
	Signals   userRiskSignals
	Complete  bool
}

// UserRiskConfig is the explainable scoring policy for one relay station.
type UserRiskConfig struct {
	Enabled                  bool `json:"enabled"`
	LookbackMinutes          int  `json:"lookback_minutes"`
	ConcurrencyWindowSeconds int  `json:"concurrency_window_seconds"`

	RegistrationIPWeight            int `json:"registration_ip_weight"`
	RegistrationIPIncrementWeight   int `json:"registration_ip_increment_weight"`
	SameIPConcurrentWeight          int `json:"same_ip_concurrent_weight"`
	SameIPConcurrentIncrementWeight int `json:"same_ip_concurrent_increment_weight"`
	SameUserMultiIPWeight           int `json:"same_user_multi_ip_weight"`
	SameUserMultiIPIncrementWeight  int `json:"same_user_multi_ip_increment_weight"`
	SuspiciousEmailWeight           int `json:"suspicious_email_weight"`
	NewAccountNoUsageWeight         int `json:"new_account_no_usage_weight"`

	RegistrationIPMinAccounts int `json:"registration_ip_min_accounts"`
	SameIPConcurrentMinUsers  int `json:"same_ip_concurrent_min_users"`
	SameUserMultiIPMinIPs     int `json:"same_user_multi_ip_min_ips"`

	AutoDisableEnabled   bool `json:"auto_disable_enabled"`
	AutoDisableThreshold int  `json:"auto_disable_threshold"`
}

// Defaults keep automatic disabling off. Quantity-based signals gain points
// once the count exceeds their configured hit threshold.
func DefaultUserRiskConfig() UserRiskConfig {
	return UserRiskConfig{
		Enabled:                         true,
		LookbackMinutes:                 userRiskDefaultLookbackMinutes,
		ConcurrencyWindowSeconds:        60,
		RegistrationIPWeight:            25,
		RegistrationIPIncrementWeight:   2,
		SameIPConcurrentWeight:          35,
		SameIPConcurrentIncrementWeight: 5,
		SameUserMultiIPWeight:           30,
		SameUserMultiIPIncrementWeight:  10,
		SuspiciousEmailWeight:           10,
		NewAccountNoUsageWeight:         0,
		RegistrationIPMinAccounts:       2,
		SameIPConcurrentMinUsers:        2,
		SameUserMultiIPMinIPs:           2,
		AutoDisableEnabled:              false,
		AutoDisableThreshold:            80,
	}
}

// ParseUserRiskConfig starts with defaults so persisted configs from before the
// incremental-weight fields existed transparently gain those default steps.
func ParseUserRiskConfig(raw string) UserRiskConfig {
	config := DefaultUserRiskConfig()
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &config)
	}
	return config.Normalize()
}

func (c UserRiskConfig) Normalize() UserRiskConfig {
	defaults := DefaultUserRiskConfig()
	if c.LookbackMinutes <= 0 {
		c.LookbackMinutes = defaults.LookbackMinutes
	}
	if c.ConcurrencyWindowSeconds <= 0 {
		c.ConcurrencyWindowSeconds = defaults.ConcurrencyWindowSeconds
	}
	if c.RegistrationIPMinAccounts <= 0 {
		c.RegistrationIPMinAccounts = defaults.RegistrationIPMinAccounts
	}
	if c.SameIPConcurrentMinUsers <= 0 {
		c.SameIPConcurrentMinUsers = defaults.SameIPConcurrentMinUsers
	}
	if c.SameUserMultiIPMinIPs <= 0 {
		c.SameUserMultiIPMinIPs = defaults.SameUserMultiIPMinIPs
	}
	if c.AutoDisableThreshold <= 0 {
		c.AutoDisableThreshold = defaults.AutoDisableThreshold
	}
	return c
}

func (c UserRiskConfig) Validate() error {
	if c.LookbackMinutes < 1 || c.LookbackMinutes > userRiskMaxLookbackMinutes {
		return fmt.Errorf("风险观察窗口必须是 1 到 %d 分钟", userRiskMaxLookbackMinutes)
	}
	if c.ConcurrencyWindowSeconds < 5 || c.ConcurrencyWindowSeconds > userRiskMaxConcurrencyWindow {
		return fmt.Errorf("并发重叠窗口必须是 5 到 %d 秒", userRiskMaxConcurrencyWindow)
	}
	for name, value := range map[string]int{
		"同 IP 注册基础权重":  c.RegistrationIPWeight,
		"同 IP 注册递增步长":  c.RegistrationIPIncrementWeight,
		"同 IP 并发基础权重":  c.SameIPConcurrentWeight,
		"同 IP 并发递增步长":  c.SameIPConcurrentIncrementWeight,
		"同账号多 IP 基础权重": c.SameUserMultiIPWeight,
		"同账号多 IP 递增步长": c.SameUserMultiIPIncrementWeight,
		"机器邮箱权重":       c.SuspiciousEmailWeight,
		"新账号无请求权重":     c.NewAccountNoUsageWeight,
	} {
		if value < 0 || value > userRiskMaxWeight {
			return fmt.Errorf("%s必须是 0 到 %d", name, userRiskMaxWeight)
		}
	}
	if c.RegistrationIPMinAccounts < 2 || c.RegistrationIPMinAccounts > userRiskMaxConcurrentMinimum {
		return fmt.Errorf("同 IP 注册账号数阈值必须是 2 到 %d", userRiskMaxConcurrentMinimum)
	}
	if c.SameIPConcurrentMinUsers < 2 || c.SameIPConcurrentMinUsers > userRiskMaxConcurrentMinimum {
		return fmt.Errorf("同 IP 并发账号数阈值必须是 2 到 %d", userRiskMaxConcurrentMinimum)
	}
	if c.SameUserMultiIPMinIPs < 2 || c.SameUserMultiIPMinIPs > userRiskMaxConcurrentMinimum {
		return fmt.Errorf("同账号并发 IP 数阈值必须是 2 到 %d", userRiskMaxConcurrentMinimum)
	}
	if c.AutoDisableThreshold < 1 || c.AutoDisableThreshold > userRiskMaxThreshold {
		return fmt.Errorf("自动禁用风险阈值必须是 1 到 %d", userRiskMaxThreshold)
	}
	return nil
}

func (s *Service) SaveUserRiskConfig(stationID uint, config UserRiskConfig) (UserRiskConfig, error) {
	config = config.Normalize()
	if err := config.Validate(); err != nil {
		return UserRiskConfig{}, err
	}
	body, err := json.Marshal(config)
	if err != nil {
		return UserRiskConfig{}, fmt.Errorf("编码用户风险配置失败: %w", err)
	}
	if err := s.stations.SetUserRiskConfig(stationID, string(body)); err != nil {
		return UserRiskConfig{}, err
	}
	return config, nil
}

func (s *Service) UserRiskConfig(stationID uint) (UserRiskConfig, error) {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return UserRiskConfig{}, err
	}
	return ParseUserRiskConfig(station.UserRiskConfigJSON), nil
}

type UserRiskExample struct {
	Name           string `json:"name"`
	Scenario       string `json:"scenario"`
	Score          int    `json:"score"`
	Recommendation string `json:"recommendation"`
}

func UserRiskExamples(config UserRiskConfig) []UserRiskExample {
	config = config.Normalize()
	makeExample := func(name, scenario string, score int) UserRiskExample {
		if score > userRiskMaxWeight {
			score = userRiskMaxWeight
		}
		return UserRiskExample{Name: name, Scenario: scenario, Score: score, Recommendation: recommendationForScore(score, config.AutoDisableThreshold)}
	}
	thresholdExample := func(name, scenarioPrefix, scenarioSuffix string, baseWeight, incrementWeight, hitThreshold int) UserRiskExample {
		count, score, reached := minimumObservedForRiskThreshold(baseWeight, incrementWeight, hitThreshold, config.AutoDisableThreshold)
		if reached {
			return makeExample(name, fmt.Sprintf("%s %d %s（达到自动禁用风险阈值 %d 分）", scenarioPrefix, count, scenarioSuffix, config.AutoDisableThreshold), score)
		}
		maxScore := progressiveRiskScore(baseWeight, incrementWeight, hitThreshold, hitThreshold+userRiskMaxWeight)
		return UserRiskExample{
			Name:           name,
			Scenario:       fmt.Sprintf("%s无法单独达到自动禁用风险阈值 %d 分（单项最高 %d 分）", scenarioPrefix, config.AutoDisableThreshold, maxScore),
			Score:          maxScore,
			Recommendation: "需叠加其他风险信号或调整权重",
		}
	}
	registration := func(count int) int {
		return progressiveRiskScore(config.RegistrationIPWeight, config.RegistrationIPIncrementWeight, config.RegistrationIPMinAccounts, count)
	}
	concurrentUsers := func(count int) int {
		return progressiveRiskScore(config.SameIPConcurrentWeight, config.SameIPConcurrentIncrementWeight, config.SameIPConcurrentMinUsers, count)
	}
	concurrentIPs := func(count int) int {
		return progressiveRiskScore(config.SameUserMultiIPWeight, config.SameUserMultiIPIncrementWeight, config.SameUserMultiIPMinIPs, count)
	}
	combined := concurrentUsers(config.SameIPConcurrentMinUsers) + concurrentIPs(config.SameUserMultiIPMinIPs)
	return []UserRiskExample{
		makeExample("同 IP 注册", fmt.Sprintf("同一 IP 注册 %d 个账号（命中阈值）", config.RegistrationIPMinAccounts), registration(config.RegistrationIPMinAccounts)),
		makeExample("同 IP 注册", "同一 IP 注册 5 个账号", registration(5)),
		thresholdExample("同 IP 注册", "同一 IP 注册", "个账号", config.RegistrationIPWeight, config.RegistrationIPIncrementWeight, config.RegistrationIPMinAccounts),
		makeExample("同 IP 多账号并发", fmt.Sprintf("%d 个账号在 %d 秒窗口同时调用", config.SameIPConcurrentMinUsers, config.ConcurrencyWindowSeconds), concurrentUsers(config.SameIPConcurrentMinUsers)),
		thresholdExample("同 IP 多账号并发", "同 IP", "个账号同时调用", config.SameIPConcurrentWeight, config.SameIPConcurrentIncrementWeight, config.SameIPConcurrentMinUsers),
		makeExample("同账号多 IP 并发", fmt.Sprintf("同账号在 %d 个 IP 上同时调用", config.SameUserMultiIPMinIPs), concurrentIPs(config.SameUserMultiIPMinIPs)),
		makeExample("并发风险合计", "同 IP 多账号并发 + 同账号多 IP 并发（仅两个信号都命中）", combined),
	}
}

func minimumObservedForRiskThreshold(baseWeight, incrementWeight, hitThreshold, riskThreshold int) (int, int, bool) {
	if hitThreshold < 1 || riskThreshold < 1 || baseWeight <= 0 {
		return 0, 0, false
	}
	if baseWeight >= riskThreshold {
		score := progressiveRiskScore(baseWeight, incrementWeight, hitThreshold, hitThreshold)
		return hitThreshold, score, score >= riskThreshold
	}
	if incrementWeight <= 0 {
		return 0, progressiveRiskScore(baseWeight, incrementWeight, hitThreshold, hitThreshold), false
	}
	extra := (riskThreshold - baseWeight + incrementWeight - 1) / incrementWeight
	observed := hitThreshold + extra
	score := progressiveRiskScore(baseWeight, incrementWeight, hitThreshold, observed)
	return observed, score, score >= riskThreshold
}

func progressiveRiskScore(baseWeight, incrementWeight, threshold, observed int) int {
	if observed < threshold || baseWeight <= 0 {
		return 0
	}
	if incrementWeight < 0 {
		incrementWeight = 0
	}
	score := baseWeight + (observed-threshold)*incrementWeight
	if score > userRiskMaxWeight {
		return userRiskMaxWeight
	}
	return score
}

func recommendationForScore(score, threshold int) string {
	if threshold > 0 && score >= threshold {
		return "达到自动禁用阈值"
	}
	if score >= 80 {
		return "高风险，建议人工复核"
	}
	if score >= 50 {
		return "中风险，建议限速观察"
	}
	if score >= 25 {
		return "低风险，继续观察"
	}
	return "正常"
}

type userRiskSignals struct {
	SameIPConcurrentUsers map[int64]int
	SameUserConcurrentIPs map[int64]int
	Complete              bool
}

type riskUsageEvent struct {
	UserID    int64
	IPAddress string
	StartedAt time.Time
	EndedAt   time.Time
}

func (e riskUsageEvent) coincides(other riskUsageEvent, window time.Duration) bool {
	if e.StartedAt.Before(other.EndedAt) && other.StartedAt.Before(e.EndedAt) {
		return true
	}
	delta := e.StartedAt.Sub(other.StartedAt)
	if delta < 0 {
		delta = -delta
	}
	return delta <= window
}

type riskSweepPoint struct {
	at    time.Time
	start bool
	user  int64
	ip    string
}

func maxConcurrentCounts(events []riskUsageEvent, groupKey func(riskUsageEvent) string, entityKey func(riskUsageEvent) string) map[int64]int {
	pointsByGroup := make(map[string][]riskSweepPoint)
	for _, event := range events {
		if event.UserID <= 0 || event.IPAddress == "" {
			continue
		}
		group := groupKey(event)
		pointsByGroup[group] = append(pointsByGroup[group],
			riskSweepPoint{at: event.StartedAt, start: true, user: event.UserID, ip: entityKey(event)},
			riskSweepPoint{at: event.EndedAt, start: false, user: event.UserID, ip: entityKey(event)},
		)
	}
	maxima := make(map[int64]int)
	for _, points := range pointsByGroup {
		sort.Slice(points, func(i, j int) bool {
			if points[i].at.Equal(points[j].at) {
				return !points[i].start && points[j].start
			}
			return points[i].at.Before(points[j].at)
		})
		activeUsers := make(map[int64]int)
		activeEntities := make(map[string]int)
		for _, point := range points {
			if point.start {
				activeUsers[point.user]++
				activeEntities[point.ip]++
			} else {
				activeUsers[point.user]--
				activeEntities[point.ip]--
			}
			activeCount := 0
			for _, count := range activeEntities {
				if count > 0 {
					activeCount++
				}
			}
			for userID, count := range activeUsers {
				if count > 0 && activeCount > maxima[userID] {
					maxima[userID] = activeCount
				}
			}
		}
	}
	return maxima
}

// userRiskSignalsFromUsage counts peak simultaneous distinct users and IPs.
func userRiskSignalsFromUsage(events []riskUsageEvent, window time.Duration) userRiskSignals {
	usersByIP := maxConcurrentCounts(events,
		func(event riskUsageEvent) string { return event.IPAddress },
		func(event riskUsageEvent) string { return fmt.Sprintf("%d", event.UserID) },
	)
	ipsByUser := maxConcurrentCounts(events,
		func(event riskUsageEvent) string { return fmt.Sprintf("%d", event.UserID) },
		func(event riskUsageEvent) string { return event.IPAddress },
	)
	// Account for brief calls with no reliable duration using the configured start window.
	for i, left := range events {
		for _, right := range events[i+1:] {
			if !left.coincides(right, window) {
				continue
			}
			if left.IPAddress == right.IPAddress && left.UserID != right.UserID {
				if usersByIP[left.UserID] < 2 {
					usersByIP[left.UserID] = 2
				}
				if usersByIP[right.UserID] < 2 {
					usersByIP[right.UserID] = 2
				}
			}
			if left.UserID == right.UserID && left.IPAddress != right.IPAddress && ipsByUser[left.UserID] < 2 {
				ipsByUser[left.UserID] = 2
			}
		}
	}
	return userRiskSignals{SameIPConcurrentUsers: usersByIP, SameUserConcurrentIPs: ipsByUser, Complete: true}
}
func riskUsageEvents(raw []remoteUsage, now time.Time, lookback time.Duration) []riskUsageEvent {
	cutoff := now.Add(-lookback)
	events := make([]riskUsageEvent, 0, len(raw))
	for _, usage := range raw {
		created, err := time.Parse(time.RFC3339Nano, usage.CreatedAt)
		if err != nil || usage.UserID <= 0 {
			continue
		}
		ip, localLocation := normalizeUsageIPAddress(usage.IPAddress)
		if ip == "" || localLocation != "" {
			continue
		}
		duration := time.Duration(usage.DurationMS) * time.Millisecond
		if duration <= 0 {
			duration = time.Second
		}
		if duration > 10*time.Minute {
			duration = 10 * time.Minute
		}
		end := created.Add(duration)
		if end.Before(cutoff) || created.After(now.Add(time.Second)) {
			continue
		}
		events = append(events, riskUsageEvent{UserID: usage.UserID, IPAddress: ip, StartedAt: created, EndedAt: end})
	}
	return events
}

func (s *Service) fetchRiskUsageEvents(ctx context.Context, baseURL, apiKey string, config UserRiskConfig) ([]riskUsageEvent, bool) {
	now := time.Now()
	cutoff := now.Add(-time.Duration(config.LookbackMinutes) * time.Minute)
	raw := make([]remoteUsage, 0, 500)
	complete := false
	for pageNumber := 1; pageNumber <= 100; pageNumber++ {
		var page remoteUsagePage
		endpoint := fmt.Sprintf("%s/api/v1/admin/usage?page=%d&page_size=500", strings.TrimRight(baseURL, "/"), pageNumber)
		if err := s.get(ctx, endpoint, apiKey, &page); err != nil {
			return nil, false
		}
		raw = append(raw, page.Items...)
		allOld := len(page.Items) > 0
		for _, item := range page.Items {
			created, err := time.Parse(time.RFC3339Nano, item.CreatedAt)
			if err != nil || !created.Before(cutoff.Add(-10*time.Minute)) {
				allOld = false
				break
			}
		}
		if len(page.Items) == 0 || (page.Pages > 0 && pageNumber >= page.Pages) || allOld {
			complete = true
			break
		}
	}
	return riskUsageEvents(raw, now, time.Duration(config.LookbackMinutes)*time.Minute), complete
}

func (s *Service) userRiskSignals(ctx context.Context, stationID uint, baseURL, apiKey string, config UserRiskConfig) (userRiskSignals, bool) {
	if !config.Enabled {
		return userRiskSignals{SameIPConcurrentUsers: map[int64]int{}, SameUserConcurrentIPs: map[int64]int{}, Complete: true}, true
	}
	key := fmt.Sprintf("%d:%d:%d", stationID, config.LookbackMinutes, config.ConcurrencyWindowSeconds)
	now := time.Now()
	s.userRiskMu.Lock()
	if cached, ok := s.userRiskCache[key]; ok && cached.ExpiresAt.After(now) {
		s.userRiskMu.Unlock()
		return cached.Signals, cached.Complete
	}
	s.userRiskMu.Unlock()
	events, complete := s.fetchRiskUsageEvents(ctx, baseURL, apiKey, config)
	signals := userRiskSignalsFromUsage(events, time.Duration(config.ConcurrencyWindowSeconds)*time.Second)
	signals.Complete = complete
	s.userRiskMu.Lock()
	s.userRiskCache[key] = userRiskCacheEntry{ExpiresAt: now.Add(userRiskCacheTTL), Signals: signals, Complete: complete}
	s.userRiskMu.Unlock()
	return signals, complete
}

// UserRiskReason 是一次风险命中的可解释快照，记录触发条件、观察值和具体算式。
type UserRiskReason struct {
	Key            string `json:"key"`
	Label          string `json:"label"`
	Condition      string `json:"condition"`
	Observed       int    `json:"observed,omitempty"`
	ObservedValue  string `json:"observed_value,omitempty"`
	Threshold      int    `json:"threshold,omitempty"`
	BaseScore      int    `json:"base_score,omitempty"`
	IncrementScore int    `json:"increment_score,omitempty"`
	Score          int    `json:"score"`
	Calculation    string `json:"calculation"`
}

type userRiskEvaluation struct {
	Score   int
	Level   string
	Reasons []string
	Details []UserRiskReason
}

func evaluateUserRisk(user remoteUser, info registrationIPInfo, usage UserUsageStats, signals userRiskSignals, config UserRiskConfig) userRiskEvaluation {
	config = config.Normalize()
	if !config.Enabled {
		return userRiskEvaluation{Score: 0, Level: "normal"}
	}
	score := 0
	reasons := make([]string, 0, 5)
	details := make([]UserRiskReason, 0, 5)
	addQuantity := func(key, label string, observed, threshold, base, increment int, unit, condition string) {
		itemScore := progressiveRiskScore(base, increment, threshold, observed)
		if itemScore <= 0 {
			return
		}
		score += itemScore
		reasons = append(reasons, fmt.Sprintf("%s：%d %s，基础 %d 分，递增 %d 分/%s（+%d）", label, observed, unit, base, increment, unit, itemScore))
		details = append(details, UserRiskReason{
			Key: key, Label: label, Condition: condition, Observed: observed, Threshold: threshold,
			BaseScore: base, IncrementScore: increment, Score: itemScore,
			Calculation: fmt.Sprintf("min(100, %d + max(%d - %d, 0) × %d) = %d 分", base, observed, threshold, increment, itemScore),
		})
	}
	addBinary := func(key, label, condition, observedValue string, weight int) {
		if weight <= 0 {
			return
		}
		score += weight
		reasons = append(reasons, fmt.Sprintf("%s（+%d）", label, weight))
		details = append(details, UserRiskReason{Key: key, Label: label, Condition: condition, Observed: 1, ObservedValue: observedValue, Threshold: 1, BaseScore: weight, Score: weight, Calculation: fmt.Sprintf("命中 + %d 分", weight)})
	}
	if count := info.Count; count >= config.RegistrationIPMinAccounts {
		addQuantity("registration_ip", "同注册 IP", count, config.RegistrationIPMinAccounts, config.RegistrationIPWeight, config.RegistrationIPIncrementWeight, "个账号", fmt.Sprintf("同一注册 IP 的账号数 ≥ %d", config.RegistrationIPMinAccounts))
	}
	if users := signals.SameIPConcurrentUsers[user.ID]; users >= config.SameIPConcurrentMinUsers {
		addQuantity("same_ip_concurrent", "同 IP 同时调用", users, config.SameIPConcurrentMinUsers, config.SameIPConcurrentWeight, config.SameIPConcurrentIncrementWeight, "个账号", fmt.Sprintf("同一 IP 在 %d 秒并发窗口内的账号数 ≥ %d", config.ConcurrencyWindowSeconds, config.SameIPConcurrentMinUsers))
	}
	if ips := signals.SameUserConcurrentIPs[user.ID]; ips >= config.SameUserMultiIPMinIPs {
		addQuantity("same_user_multi_ip", "同账号同时使用", ips, config.SameUserMultiIPMinIPs, config.SameUserMultiIPWeight, config.SameUserMultiIPIncrementWeight, "个 IP", fmt.Sprintf("同一账号在 %d 秒并发窗口内使用的 IP 数 ≥ %d", config.ConcurrencyWindowSeconds, config.SameUserMultiIPMinIPs))
	}
	if suspiciousEmail(user.Email) {
		addBinary("suspicious_email", "疑似机器注册邮箱", "邮箱为 Gmail，且本地部分按 . 或 _ 分成两段、每段至少 2 个字符，第二段长度至少 5 且包含数字", "命中（1）", config.SuspiciousEmailWeight)
	}
	if time.Since(user.CreatedAt) <= 24*time.Hour && user.LastUsedAt == nil && usage.TotalTokens == 0 {
		observed := fmt.Sprintf("创建时间 %s；last_used_at 为空；观察窗口 Token = %d", user.CreatedAt.Format(time.RFC3339), usage.TotalTokens)
		addBinary("new_account_no_usage", "新账号暂无 API 请求", "账号创建不超过 24 小时、没有最后使用时间记录，且风险观察窗口内 Token 总数为 0", observed, config.NewAccountNoUsageWeight)
	}
	if score > userRiskMaxWeight {
		score = userRiskMaxWeight
	}
	level := "normal"
	switch {
	case score >= 80:
		level = "high"
	case score >= 50:
		level = "medium"
	case score >= 25:
		level = "low"
	}
	return userRiskEvaluation{Score: score, Level: level, Reasons: reasons, Details: details}
}

func scoreUserRisk(user remoteUser, info registrationIPInfo, usage UserUsageStats, signals userRiskSignals, config UserRiskConfig) (int, string, []string) {
	evaluation := evaluateUserRisk(user, info, usage, signals, config)
	return evaluation.Score, evaluation.Level, evaluation.Reasons
}

var errRiskDataIncomplete = errors.New("用户风险数据不完整，跳过自动禁用")

func (s *Service) autoDisableRiskUsers(ctx context.Context, stationID uint, station *storage.RelayStation, users []remoteUser, evaluations map[int64]userRiskEvaluation, config UserRiskConfig, complete bool) error {
	if !config.AutoDisableEnabled {
		return nil
	}
	if !complete {
		return errRiskDataIncomplete
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return err
	}
	for _, user := range users {
		if strings.EqualFold(strings.TrimSpace(user.Role), "admin") || !strings.EqualFold(strings.TrimSpace(user.Status), "active") {
			continue
		}
		evaluation := evaluations[user.ID]
		if evaluation.Score < config.AutoDisableThreshold {
			continue
		}
		endpoint := fmt.Sprintf("%s/api/v1/admin/users/%d", strings.TrimRight(station.BaseURL, "/"), user.ID)
		if err := s.put(ctx, endpoint, apiKey, map[string]any{"status": "disabled"}); err != nil {
			return fmt.Errorf("自动禁用用户 %d 失败: %w", user.ID, err)
		}
		reasonsJSON, err := json.Marshal(evaluation.Details)
		if err != nil {
			return fmt.Errorf("编码自动禁用用户 %d 的风险原因失败: %w", user.ID, err)
		}
		if err := s.stations.RecordUserAutoDisableLog(&storage.RelayUserAutoDisableLog{
			RelayStationID: stationID, UserID: user.ID, Email: user.Email, Username: user.Username,
			RiskScore: evaluation.Score, Threshold: config.AutoDisableThreshold, ReasonsJSON: string(reasonsJSON), TriggeredAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("用户 %d 已自动禁用，但写入自动禁用记录失败: %w", user.ID, err)
		}
	}
	return nil
}

func (s *Service) EvaluateUserRisks(ctx context.Context, stationID uint) error {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	config := ParseUserRiskConfig(station.UserRiskConfigJSON)
	if !config.Enabled || !config.AutoDisableEnabled {
		return nil
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return err
	}
	users, err := s.fetchUsers(ctx, station.BaseURL, apiKey, "")
	if err != nil {
		return err
	}
	registrationIPs, auditComplete := s.registrationIPs(ctx, station.BaseURL, apiKey, users)
	signals, usageComplete := s.userRiskSignals(ctx, stationID, station.BaseURL, apiKey, config)
	usageSince := time.Now().Add(-time.Duration(config.LookbackMinutes) * time.Minute)
	usageUntil := time.Now()
	usageIDs := make(map[int64]struct{})
	for _, user := range users {
		isAdmin := strings.EqualFold(strings.TrimSpace(user.Role), "admin")
		isActive := strings.EqualFold(strings.TrimSpace(user.Status), "active")
		isNew := time.Since(user.CreatedAt) <= 24*time.Hour
		if !isAdmin && isActive && isNew && user.LastUsedAt == nil {
			usageIDs[user.ID] = struct{}{}
		}
	}
	userUsage := make(map[int64]UserUsageStats, len(users))
	failedUsers := s.fetchUserUsageStats(ctx, station.BaseURL, apiKey, usageSince, usageUntil, usageIDs, userUsage)
	complete := auditComplete && usageComplete && failedUsers == 0
	evaluations := make(map[int64]userRiskEvaluation, len(users))
	for _, user := range users {
		info := registrationIPs[user.ID]
		evaluations[user.ID] = evaluateUserRisk(user, info, userUsage[user.ID], signals, config)
	}
	return s.autoDisableRiskUsers(ctx, stationID, station, users, evaluations, config, complete)
}

func (s *Service) EvaluateUserRisksAll(ctx context.Context) (int, error) {
	stations, err := s.stations.List()
	if err != nil {
		return 0, err
	}
	processed := 0
	var failures []string
	for _, station := range stations {
		config := ParseUserRiskConfig(station.UserRiskConfigJSON)
		if !config.Enabled || !config.AutoDisableEnabled {
			continue
		}
		if err := s.EvaluateUserRisks(ctx, station.ID); err != nil {
			failures = append(failures, fmt.Sprintf("站点 %d: %v", station.ID, err))
			continue
		}
		processed++
	}
	if len(failures) > 0 {
		return processed, errors.New(strings.Join(failures, "; "))
	}
	return processed, nil
}

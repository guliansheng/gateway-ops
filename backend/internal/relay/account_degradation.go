package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

func (s *Service) AccountDegradationHistory(ctx context.Context, stationID uint, accountID int64) ([]storage.AccountDegradationResult, error) {
	return s.stations.AccountDegradationHistory(ctx, stationID, accountID)
}
func (s *Service) LatestAccountDegradation(ctx context.Context, stationID uint) (map[string]storage.AccountDegradationResult, error) {
	return s.stations.LatestAccountDegradation(ctx, stationID)
}
func (s *Service) AccountDegradationConfig(ctx context.Context, stationID uint, accountID int64) (storage.AccountDegradationConfig, error) {
	return s.stations.AccountDegradationConfig(ctx, stationID, accountID)
}
func (s *Service) SaveAccountDegradationConfig(ctx context.Context, stationID uint, accountID int64, config storage.AccountDegradationConfig) (storage.AccountDegradationConfig, error) {
	return s.stations.SaveAccountDegradationConfig(ctx, stationID, accountID, config)
}

func (s *Service) RunAccountDegradation(ctx context.Context, stationID uint, accountID int64, config storage.GroupDegradationConfig, fallbackModels ...string) (storage.AccountDegradationResult, error) {
	config.Enabled = true
	config, err := storage.NormalizeGroupDegradationConfig(config)
	if err != nil {
		return storage.AccountDegradationResult{}, err
	}
	config.Protocol, err = normalizeAccountDegradationProtocol(config.Protocol)
	if err != nil {
		return storage.AccountDegradationResult{}, fmt.Errorf("%w: %s", storage.ErrInvalidGroupDegradationConfig, err)
	}
	if len(fallbackModels) > 1 {
		return storage.AccountDegradationResult{}, storage.ErrInvalidGroupDegradationConfig
	}
	fallback := ""
	if len(fallbackModels) == 1 {
		fallback = strings.TrimSpace(fallbackModels[0])
	}
	release, ok := s.acquireGroupDegradationSlot()
	if !ok {
		return storage.AccountDegradationResult{}, storage.ErrGroupDegradationBusy
	}
	defer release()
	claimed, err := s.stations.ClaimAccountDegradation(ctx, stationID, accountID, groupDegradationLease)
	if err != nil {
		return storage.AccountDegradationResult{}, err
	}
	return s.runClaimedAccountDegradation(ctx, stationID, accountID, claimed, config, fallback, storage.DefaultGroupDegradationHistoryLimit)
}

func (s *Service) runClaimedAccountDegradation(ctx context.Context, stationID uint, accountID int64, claimed storage.AccountDegradationRun, config storage.GroupDegradationConfig, fallback string, historyLimit int) (storage.AccountDegradationResult, error) {
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), groupDegradationRunTimeout)
	defer cancel()
	started := time.Now()
	method := config.RunMethod
	if method == "" {
		method = config.Method
	}
	result := storage.AccountDegradationResult{GroupDegradationResult: storage.GroupDegradationResult{Method: method, Model: config.Model, JudgeModel: config.JudgeModel, Effort: config.Effort, Status: "error"}}
	if result.JudgeModel == "" {
		result.JudgeModel = config.Model
	}
	prompts, err := s.stations.GetDegradationPromptSettings(runCtx, stationID)
	if err != nil {
		result.Reason = "读取中转站降智测试标准失败"
	} else if config, err = s.selectAccountDegradationModel(runCtx, stationID, accountID, config, fallback); err != nil {
		result.Reason = err.Error()
	} else {
		result.Model = config.Model
		if config.JudgeModel == "" {
			result.JudgeModel = config.Model
		}
		client, endpoint, key, protocol, mapping, secrets, credentialErr := s.accountDegradationCredential(runCtx, stationID, accountID, config.Protocol)
		if credentialErr != nil {
			result.Reason = credentialErr.Error()
		} else {
			if protocol != "account_test" {
				defer client.client.CloseIdleConnections()
			}
			chat := func(callCtx context.Context, model, effort, system, prompt string, timeout time.Duration, limit int) (string, error) {
				if protocol == "account_test" {
					return client.accountDegradationAdminChat(callCtx, endpoint, key, accountID, model, effort, system, prompt, timeout, limit)
				}
				model = mappedDegradationModel(model, mapping)
				if protocol == "anthropic" || protocol == "gemini" {
					return client.nativeAccountDegradationChat(callCtx, endpoint, key, protocol, model, effort, system, prompt, timeout, limit)
				}
				return client.degradationChat(callCtx, endpoint, key, model, effort, system, prompt, timeout, limit, protocol)
			}
			result.GroupDegradationResult = s.evaluateDegradation(runCtx, config, prompts, chat, secrets...)
		}
	}
	result.StartedAt = &started
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(runCtx), 10*time.Second)
	defer saveCancel()
	if err := s.stations.CompleteAccountDegradationWithLimit(saveCtx, claimed, &result, historyLimit); err != nil {
		return storage.AccountDegradationResult{}, errors.New("保存账号降智测试记录失败，请稍后刷新记录")
	}
	// Cost and the serving account are filled in afterwards; a closed dialog
	// never interrupts either the run or its accounting.
	s.startDegradationUsageSync(degradationUsageJob{
		Scope: storage.DegradationUsageScopeAccount, StationID: stationID, AccountID: accountID,
		ResultID: result.ID, Models: []string{result.Model, result.JudgeModel}, StartedAt: started, FinishedAt: time.Now(),
		FallbackAccountID: accountID,
	})
	return result, nil
}

func (s *Service) RunDueAccountDegradation(ctx context.Context) error {
	// Cron work shares the scheduled pool only, so a user triggered test never
	// has to queue behind it (and vice versa).
	release, ok := s.acquireScheduledDegradationSlot()
	if !ok {
		return nil
	}
	defer release()
	configs, err := s.stations.ClaimDueAccountDegradation(ctx, 4, groupDegradationLease)
	if err != nil {
		return err
	}
	var combined error
	for _, scheduled := range configs {
		method := scheduled.RunMethod
		config := storage.GroupDegradationConfig{Enabled: true, Method: scheduled.Method, RunMethod: method, Model: scheduled.Model, JudgeModel: scheduled.JudgeModel, Effort: scheduled.Effort, Protocol: scheduled.Protocol, HistoryLimit: scheduled.HistoryLimit}
		claimed, claimErr := s.stations.ClaimAccountDegradation(ctx, scheduled.StationID, scheduled.AccountExternalID, groupDegradationLease)
		if claimErr != nil {
			combined = errors.Join(combined, claimErr)
			completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			combined = errors.Join(combined, s.stations.CompleteDueAccountDegradation(completeCtx, scheduled, scheduled.HistoryLimit))
			cancel()
			continue
		}
		result, runErr := s.runClaimedAccountDegradation(ctx, scheduled.StationID, scheduled.AccountExternalID, claimed, config, "", scheduled.HistoryLimit)
		if runErr != nil {
			combined = errors.Join(combined, runErr)
		} else if scheduled.DisableOnDegraded && result.Status == "degraded" {
			if err := s.SetSchedulable(ctx, scheduled.StationID, scheduled.AccountExternalID, false); err != nil {
				combined = errors.Join(combined, err)
			}
		}
		completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := s.stations.CompleteDueAccountDegradation(completeCtx, scheduled, scheduled.HistoryLimit); err != nil {
			combined = errors.Join(combined, err)
		}
		cancel()
	}
	return combined
}

func (s *Service) selectAccountDegradationModel(ctx context.Context, stationID uint, accountID int64, config storage.GroupDegradationConfig, fallback string) (storage.GroupDegradationConfig, error) {
	if fallback == "" {
		return config, nil
	}
	models, err := s.AccountModels(ctx, stationID, accountID)
	if err != nil {
		return config, errors.New("读取账号模型列表失败，未发送测试请求")
	}
	for _, model := range models {
		if model == config.Model {
			return config, nil
		}
	}
	for _, model := range models {
		if model == fallback {
			config.Model = fallback
			return config, nil
		}
	}
	return config, errors.New("首选模型和备选模型均不在账号模型列表中，未发送测试请求")
}

type degradationAccountRemote struct {
	remoteAccount
	ProxyID *int64 `json:"proxy_id"`
	Proxy   *struct {
		Protocol string `json:"protocol"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"proxy"`
}
type degradationHeaderTransport struct {
	base    http.RoundTripper
	headers http.Header
}

func (t degradationHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header = req.Header.Clone()
	for name, values := range t.headers {
		copy.Header[name] = append([]string(nil), values...)
	}
	return t.base.RoundTrip(copy)
}
func (t degradationHeaderTransport) CloseIdleConnections() {
	if transport, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}

func (s *Service) accountDegradationCredential(ctx context.Context, stationID uint, accountID int64, protocols ...string) (*Service, string, string, string, map[string]any, []string, error) {
	fail := func(reason string) (*Service, string, string, string, map[string]any, []string, error) {
		return nil, "", "", "", nil, nil, errors.New(reason)
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return fail("读取中转站配置失败")
	}
	adminKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return fail("无法解密中转站管理员凭据")
	}
	var selected degradationAccountRemote
	if err := s.get(ctx, fmt.Sprintf("%s/api/v1/admin/accounts/%d", strings.TrimRight(station.BaseURL, "/"), accountID), adminKey, &selected); err != nil || selected.ID != accountID {
		return fail("读取指定账号配置失败，请检查中转站管理员凭据")
	}
	if len(protocols) > 0 && protocols[0] == "" && accountTestAcceptsPrompt(selected, accountDegradationProtocol(selected.Extra)) {
		return &Service{client: s.client}, station.BaseURL, adminKey, "account_test", nil, []string{adminKey}, nil
	}
	account := selected
	seen := map[int64]bool{accountID: true}
	for account.ParentAccountID != nil && *account.ParentAccountID > 0 {
		parentID := *account.ParentAccountID
		if seen[parentID] || len(seen) >= 8 {
			return fail("账号凭据继承链无效")
		}
		seen[parentID] = true
		var parent degradationAccountRemote
		if err := s.get(ctx, fmt.Sprintf("%s/api/v1/admin/accounts/%d", strings.TrimRight(station.BaseURL, "/"), parentID), adminKey, &parent); err != nil || parent.ID != parentID {
			return fail("无法读取账号继承的上游凭据")
		}
		account = parent
	}
	if !strings.EqualFold(account.Type, "apikey") {
		return fail("中转站当前账号测试协议不透传自定义提示词，无法定向测试；请使用支持自定义提示词的 API Key 账号")
	}
	key, _ := account.Credentials.Raw["api_key"].(string)
	key = strings.TrimSpace(key)
	if key == "" {
		var exported struct {
			Accounts []remoteAccount `json:"accounts"`
		}
		endpoint := fmt.Sprintf("%s/api/v1/admin/accounts/data?ids=%d&include_proxies=false", strings.TrimRight(station.BaseURL, "/"), account.ID)
		if err := s.get(ctx, endpoint, adminKey, &exported); err != nil {
			return fail("上游已隐藏账号密钥，读取指定账号凭据失败；请检查管理员导出权限及二次验证要求")
		}
		if len(exported.Accounts) != 1 || exported.Accounts[0].Name != account.Name || exported.Accounts[0].Platform != account.Platform || exported.Accounts[0].Type != account.Type {
			return fail("上游返回的账号凭据与指定账号不匹配，未发送测试请求")
		}
		key, _ = exported.Accounts[0].Credentials.Raw["api_key"].(string)
		key = strings.TrimSpace(key)
	}
	if key == "" {
		return fail("指定账号未返回有效上游 API Key")
	}
	endpoint := strings.TrimSpace(account.Credentials.BaseURL)
	protocol := "chat"
	switch strings.ToLower(account.Platform) {
	case "openai":
		protocol = accountDegradationProtocol(selected.Extra)
		if endpoint == "" {
			endpoint = "https://api.openai.com"
		}
	case "anthropic":
		protocol = "anthropic"
		if endpoint == "" {
			endpoint = "https://api.anthropic.com"
		}
	case "gemini":
		protocol = "gemini"
		if endpoint == "" {
			endpoint = "https://generativelanguage.googleapis.com"
		}
	default:
		if endpoint == "" {
			return fail("指定账号缺少上游地址，无法定向测试")
		}
	}
	if configured, _ := account.Credentials.Raw["api_protocol"].(string); configured != "" && configured != "adaptive" && !strings.EqualFold(account.Platform, "openai") {
		switch configured {
		case "responses":
			protocol = "responses"
		case "anthropic":
			protocol = "anthropic"
		case "chat_completions":
			protocol = "chat"
		default:
			return fail("指定账号的上游协议尚不支持降智测试")
		}
	}
	if len(protocols) > 0 && protocols[0] != "" {
		protocol = protocols[0]
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fail("指定账号的上游地址无效")
	}
	parsed.Path = strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), "/v1")
	endpoint = strings.TrimRight(parsed.String(), "/")
	mapping, _ := selected.Credentials.Raw["model_mapping"].(map[string]any)
	if len(mapping) == 0 {
		mapping, _ = account.Credentials.Raw["model_mapping"].(map[string]any)
	}
	client := http.Client{}
	if s.client != nil {
		client = *s.client
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	transport, canClone := base.(*http.Transport)
	if canClone {
		transport = transport.Clone()
		base = transport
	}
	if selected.ProxyID != nil && *selected.ProxyID > 0 {
		if selected.Proxy == nil || !canClone {
			return fail("账号代理配置不可用，未发送测试请求")
		}
		proxy := selected.Proxy
		if proxy.Host == "" || proxy.Port < 1 || proxy.Port > 65535 || (proxy.Protocol != "http" && proxy.Protocol != "https" && proxy.Protocol != "socks5" && proxy.Protocol != "socks5h") {
			return fail("账号代理配置无效")
		}
		proxyURL := &url.URL{Scheme: proxy.Protocol, Host: fmt.Sprintf("%s:%d", proxy.Host, proxy.Port)}
		if proxy.Username != "" {
			proxyURL.User = url.UserPassword(proxy.Username, proxy.Password)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	headers := http.Header{}
	secrets := []string{adminKey, key}
	for _, name := range []string{"api_key", "access_token", "refresh_token", "client_secret"} {
		if text, ok := account.Credentials.Raw[name].(string); ok && text != "" {
			secrets = append(secrets, text)
		}
	}
	if overrides, ok := account.Credentials.Raw["header_overrides"].(map[string]any); ok {
		for name, value := range overrides {
			if text, ok := value.(string); ok {
				headers.Set(name, text)
				secrets = append(secrets, text)
			}
		}
	}
	client.Transport = degradationHeaderTransport{base: base, headers: headers}
	return &Service{client: &client}, endpoint, key, protocol, mapping, secrets, nil
}

func accountDegradationProtocol(extra map[string]any) string {
	if mode, ok := extra["openai_responses_mode"].(string); ok {
		if mode == "force_chat_completions" {
			return "chat"
		}
		if mode == "force_responses" {
			return "responses"
		}
	}
	if supported, ok := extra["openai_responses_supported"].(bool); ok && !supported {
		return "chat"
	}
	return "responses"
}
func mappedDegradationModel(model string, mapping map[string]any) string {
	if mapped, ok := mapping[model].(string); ok && mapped != "" {
		return mapped
	}
	best, value := "", ""
	for pattern, target := range mapping {
		if !strings.Contains(pattern, "*") || len(pattern) <= len(best) {
			continue
		}
		matched, err := path.Match(pattern, model)
		if mapped, ok := target.(string); err == nil && matched && ok && mapped != "" {
			best, value = pattern, mapped
		}
	}
	if value != "" {
		return value
	}
	return model
}

func (s *Service) nativeAccountDegradationChat(ctx context.Context, baseURL, key, protocol, model, effort, system, prompt string, timeout time.Duration, limit int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body map[string]any
	endpoint := baseURL + "/v1/messages"
	if protocol == "anthropic" {
		body = map[string]any{"model": model, "max_tokens": 16384, "messages": []map[string]string{{"role": "user", "content": prompt}}}
		if system != "" {
			body["system"] = system
		}
	} else {
		baseURL = strings.TrimSuffix(strings.TrimSuffix(baseURL, "/v1beta"), "/v1")
		endpoint = baseURL + "/v1beta/models/" + url.PathEscape(model) + ":generateContent"
		body = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []map[string]string{{"text": prompt}}}}}
		if system != "" {
			body["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": system}}}
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", errors.New("构造账号测试请求失败")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("账号上游地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	if protocol == "anthropic" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("x-goog-api-key", key)
	}
	client := *s.client
	client.Timeout = 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("账号模型请求超时")
		}
		return "", errors.New("账号上游连接失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("账号上游返回 HTTP %d（原始错误详情已隐藏）", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit*6+65537)))
	if err != nil || len(data) > limit*6+65536 {
		return "", errors.New("读取账号响应失败或超过大小上限")
	}
	var response struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if json.Unmarshal(data, &response) != nil {
		return "", errors.New("账号模型响应格式无效")
	}
	var output strings.Builder
	if protocol == "anthropic" {
		if response.StopReason != "end_turn" && response.StopReason != "stop_sequence" {
			return "", errors.New("账号模型输出未完成，不判定降智")
		}
		for _, part := range response.Content {
			if part.Type == "text" {
				output.WriteString(part.Text)
			}
		}
	} else {
		if len(response.Candidates) == 0 || response.Candidates[0].FinishReason != "STOP" {
			return "", errors.New("账号模型输出未完成，不判定降智")
		}
		for _, part := range response.Candidates[0].Content.Parts {
			if !part.Thought {
				output.WriteString(part.Text)
			}
		}
	}
	if output.Len() > limit || strings.TrimSpace(output.String()) == "" {
		return "", errors.New("账号模型输出为空或超过大小上限")
	}
	return output.String(), nil
}

// ClearAccountDegradationHistory removes one account's manual test records.
// Account configuration, model mapping and scheduling stay untouched.
func (s *Service) ClearAccountDegradationHistory(ctx context.Context, stationID uint, accountID int64) error {
	return s.stations.ClearAccountDegradationHistory(ctx, stationID, accountID)
}

// DegradationUsage pages through group and account test runs with the charge
// Sub2API recorded for each of them. Admin-only.
func (s *Service) DegradationUsage(ctx context.Context, stationID uint, scope string, page, pageSize int) (storage.DegradationUsagePage, error) {
	return s.stations.DegradationUsage(ctx, stationID, scope, page, pageSize)
}

// Package relay imports the read-only relay-station view exposed by Sub2API.
package relay

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

const requestTimeout = 45 * time.Second

// Latency samples are optional telemetry. A short snapshot interval must not
// fan out one usage request per account on every tick and overload Sub2API.
const latencyCacheTTL = 5 * time.Minute
const latencySampleLimit = 60

const (
	ipLocationEndpoint = "http://ip-api.com/batch?fields=status,message,query,countryCode,regionName,city"
	ipLocationTTL      = 24 * time.Hour
	ipLocationErrorTTL = time.Hour
)

// ErrGroupAdminAPIKeyMissing indicates that the selected group has no usable
// administrator API key. Callers can map this configuration error to a
// client-facing status instead of presenting it as an upstream 502.
var ErrGroupAdminAPIKeyMissing = errors.New("管理员未在 API 密钥中创建并启用当前分组的管理员密钥，请创建并绑定该分组；也可使用未绑定分组的全局管理员密钥")

type Service struct {
	stations           *storage.RelayStations
	cipher             *crypto.Cipher
	channels           *storage.Channels
	rates              *storage.Rates
	client             *http.Client
	usageMu            sync.Mutex
	usageCache         map[string]usageCacheEntry
	userUsageCache     map[string]userUsageCacheEntry
	ipLocationMu       sync.Mutex
	ipLocationCache    map[string]ipLocationCacheEntry
	latencyMu          sync.Mutex
	latencyCache       map[string]latencyCacheEntry
	publicMonitorMu    sync.Mutex
	publicMonitorCache map[uint]publicMonitorCacheEntry
	publicPricingMu    sync.Mutex
	publicPricingCache map[uint]publicModelPricingCacheEntry
}

func NewService(stations *storage.RelayStations, cipher *crypto.Cipher, deps ...any) *Service {
	var channels *storage.Channels
	var rates *storage.Rates
	if len(deps) > 0 {
		channels, _ = deps[0].(*storage.Channels)
	}
	if len(deps) > 1 {
		rates, _ = deps[1].(*storage.Rates)
	}
	return &Service{
		stations:           stations,
		cipher:             cipher,
		channels:           channels,
		rates:              rates,
		client:             &http.Client{Timeout: requestTimeout},
		usageCache:         make(map[string]usageCacheEntry),
		userUsageCache:     make(map[string]userUsageCacheEntry),
		ipLocationCache:    make(map[string]ipLocationCacheEntry),
		latencyCache:       make(map[string]latencyCacheEntry),
		publicMonitorCache: make(map[uint]publicMonitorCacheEntry),
		publicPricingCache: make(map[uint]publicModelPricingCacheEntry),
	}
}

type CreateInput struct {
	Name    string
	BaseURL string
	APIKey  string
}

var ErrInvalidBatchCloneInput = errors.New("批量新增账号参数无效")

const (
	maxBatchCloneGroups   = 100
	maxBatchClonePerGroup = 100
	maxBatchCloneAccounts = 300
)

type BatchCloneInput struct {
	Groups []BatchCloneGroup `json:"groups"`
}

type BatchCloneGroup struct {
	SourceAccountExternalID int64               `json:"source_account_external_id"`
	Accounts                []BatchCloneAccount `json:"accounts"`
}

type BatchCloneAccount struct {
	Name    string `json:"name"`
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
}

type BatchCloneResult struct {
	Requested int                    `json:"requested"`
	Succeeded int                    `json:"succeeded"`
	Failed    int                    `json:"failed"`
	Results   []BatchCloneResultItem `json:"results"`
	SyncError string                 `json:"sync_error,omitempty"`
}

type BatchCloneResultItem struct {
	SourceAccountExternalID int64  `json:"source_account_external_id"`
	SourceAccountName       string `json:"source_account_name"`
	Name                    string `json:"name"`
	ExternalID              int64  `json:"external_id,omitempty"`
	Success                 bool   `json:"success"`
	Error                   string `json:"error,omitempty"`
}

type UpdateInput struct {
	Name                      *string
	BaseURL                   *string
	APIKey                    *string
	AutoAdjustEnabled         *bool
	AutoAdjustNoProfitEnabled *bool
	AutoPriorityEnabled       *bool
	AutoPriorityRecallEnabled *bool
	AutoPriorityRecallMinutes *int
}

type GroupUpdateInput struct {
	Name           *string
	Description    *string
	RateMultiplier *float64
	IsExclusive    *bool
	Status         *string
	ModelTypes     *[]string
	MonitorEnabled *bool
}

type GroupSortOrderUpdate struct {
	ID        int64 `json:"id"`
	SortOrder int   `json:"sort_order"`
}

type AccountUsageStats struct {
	TotalTokens int64   `json:"total_tokens"`
	UserCharge  float64 `json:"user_charge"`
	AccountCost float64 `json:"account_cost"`
	// BaseCost is Sub2API's raw model cost before the account multiplier. It is
	// used when an explicit channel-group binding supplies the missing account
	// multiplier and Sub2API has not materialized account_cost yet.
	BaseCost float64 `json:"-"`
	Requests int64   `json:"requests"`
}

type UsageSummary struct {
	Accounts       map[int64]AccountUsageStats
	TotalTokens    int64
	UserCharge     float64
	FailedAccounts int
}

type ChannelUserChargeTotal struct {
	UserCharge          float64
	MatchedAccountCount int
	Complete            bool
}

type ChannelCostTotal struct {
	Cost                float64
	MatchedAccountCount int
	Complete            bool
}

type usageCacheEntry struct {
	ExpiresAt time.Time
	Summary   UsageSummary
}

type userUsageCacheEntry struct {
	ExpiresAt   time.Time
	Usage       map[int64]UserUsageStats
	FailedUsers int
}

type ipLocationCacheEntry struct {
	ExpiresAt time.Time
	Location  string
}

type latencyCacheEntry struct {
	ExpiresAt time.Time
	Samples   map[int64]string
}

type UserUsageStats struct {
	TotalTokens int64
	UserCharge  float64
}

type UserListQuery struct {
	Page           int
	PageSize       int
	Search         string
	RangeName      string
	Since          time.Time
	SortBy         string
	SortOrder      string
	RiskLevel      string
	RegistrationIP string
}

type UserManagementItem struct {
	ID                     int64      `json:"id"`
	Email                  string     `json:"email"`
	Username               string     `json:"username"`
	Role                   string     `json:"role"`
	Balance                float64    `json:"balance"`
	Usage                  float64    `json:"usage"`
	UsageTotalTokens       int64      `json:"usage_total_tokens"`
	Concurrency            int        `json:"concurrency"`
	CurrentConcurrency     int        `json:"current_concurrency"`
	RPMLimit               int        `json:"rpm_limit"`
	Status                 string     `json:"status"`
	LastUsedAt             *time.Time `json:"last_used_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	RegistrationIP         string     `json:"registration_ip,omitempty"`
	RegistrationIPCount    int        `json:"registration_ip_count"`
	RegistrationBurstCount int        `json:"registration_burst_count"`
	RiskScore              int        `json:"risk_score"`
	RiskLevel              string     `json:"risk_level"`
	RiskReasons            []string   `json:"risk_reasons,omitempty"`
}

type UserManagementPage struct {
	Items            []UserManagementItem `json:"items"`
	Total            int                  `json:"total"`
	TotalBalance     float64              `json:"total_balance"`
	Page             int                  `json:"page"`
	PageSize         int                  `json:"page_size"`
	Pages            int                  `json:"pages"`
	Range            string               `json:"range"`
	Complete         bool                 `json:"complete"`
	FailedUsers      int                  `json:"failed_users"`
	RiskDataComplete bool                 `json:"risk_data_complete"`
}

type UserDeleteResult struct {
	Affected      int     `json:"affected"`
	SkippedAdmins int     `json:"skipped_admins"`
	Failed        []int64 `json:"failed"`
}

type UserBatchLimitsInput struct {
	UserIDs     []int64
	Concurrency *int
	RPMLimit    *int
}

func (s *Service) Create(in CreateInput) (*storage.RelayStation, error) {
	name := strings.TrimSpace(in.Name)
	baseURL, err := normalizeBaseURL(in.BaseURL)
	if err != nil {
		return nil, err
	}
	if name == "" || strings.TrimSpace(in.APIKey) == "" {
		return nil, errors.New("名称和管理员 API Key 不能为空")
	}
	key, err := s.cipher.Encrypt(strings.TrimSpace(in.APIKey))
	if err != nil {
		return nil, fmt.Errorf("encrypt admin API key: %w", err)
	}
	station := &storage.RelayStation{
		Name: name, BaseURL: baseURL, APIKeyCipher: key,
	}
	if err := s.stations.Create(station); err != nil {
		return nil, err
	}
	return station, nil
}

func (s *Service) Update(id uint, in UpdateInput) (*storage.RelayStation, error) {
	station, err := s.stations.FindByID(id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, errors.New("名称不能为空")
		}
		station.Name = name
	}
	if in.BaseURL != nil {
		baseURL, err := normalizeBaseURL(*in.BaseURL)
		if err != nil {
			return nil, err
		}
		station.BaseURL = baseURL
	}
	if in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "" {
		key, err := s.cipher.Encrypt(strings.TrimSpace(*in.APIKey))
		if err != nil {
			return nil, fmt.Errorf("encrypt admin API key: %w", err)
		}
		station.APIKeyCipher = key
	}
	if in.AutoAdjustEnabled != nil {
		station.AutoAdjustEnabled = *in.AutoAdjustEnabled
	}
	if in.AutoAdjustNoProfitEnabled != nil {
		station.AutoAdjustNoProfitEnabled = *in.AutoAdjustNoProfitEnabled
	}
	if in.AutoPriorityEnabled != nil {
		station.AutoPriorityEnabled = *in.AutoPriorityEnabled
	}
	if in.AutoPriorityRecallEnabled != nil {
		station.AutoPriorityRecallEnabled = *in.AutoPriorityRecallEnabled
	}
	if in.AutoPriorityRecallMinutes != nil {
		if *in.AutoPriorityRecallMinutes < 15 || *in.AutoPriorityRecallMinutes > 10080 {
			return nil, errors.New("优先级回调时间必须在 15 分钟到 7 天之间")
		}
		station.AutoPriorityRecallMinutes = *in.AutoPriorityRecallMinutes
	}
	if err := s.stations.Update(station); err != nil {
		return nil, err
	}
	s.invalidatePublicModelPricing(id)
	return station, nil
}

func (s *Service) Delete(id uint) error {
	if err := s.stations.Delete(id); err != nil {
		return err
	}
	s.invalidatePublicModelPricing(id)
	return nil
}

// DeleteAccount removes the real Sub2API account, clears Hub-only account
// overrides, and refreshes the remaining snapshot without running a billing
// probe. The local lookup protects against deleting an ID from a stale or
// different relay station.
func (s *Service) DeleteAccount(ctx context.Context, stationID uint, externalID int64) error {
	if externalID <= 0 {
		return errors.New("无效的账号 ID")
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	if _, err := s.stations.FindAccountByExternalID(stationID, externalID); err != nil {
		return errors.New("账号不存在或快照已过期，请先刷新后重试")
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return fmt.Errorf("decrypt admin API key: %w", err)
	}
	endpoint := fmt.Sprintf("%s/api/v1/admin/accounts/%d", station.BaseURL, externalID)
	if err := s.deleteRemote(ctx, endpoint, apiKey); err != nil {
		return fmt.Errorf("删除中转站账号失败: %w", err)
	}
	if err := s.stations.DeleteCostOverride(stationID, externalID); err != nil {
		return fmt.Errorf("账号已从中转站删除，但清理本地成本覆盖失败: %w", err)
	}
	if err := s.SyncSnapshot(ctx, stationID); err != nil {
		return fmt.Errorf("账号已从中转站删除，但刷新本地快照失败: %w", err)
	}
	return nil
}

// BatchCloneAccounts duplicates each requested source account independently.
// The remote duplicate endpoint creates the account first; the following PUT
// only changes the requested name, API key, Base URL, and disabled state.
func (s *Service) BatchCloneAccounts(ctx context.Context, stationID uint, in BatchCloneInput) (BatchCloneResult, error) {
	if err := validateBatchCloneInput(in); err != nil {
		return BatchCloneResult{}, err
	}

	result := BatchCloneResult{Results: make([]BatchCloneResultItem, 0, countBatchCloneAccounts(in))}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return BatchCloneResult{}, err
	}
	localAccounts, err := s.stations.ListAccounts(stationID)
	if err != nil {
		return BatchCloneResult{}, fmt.Errorf("读取中转站账号快照失败: %w", err)
	}
	localNames := make(map[int64]string, len(localAccounts))
	localIDs := make(map[int64]struct{}, len(localAccounts))
	for _, account := range localAccounts {
		localNames[account.ExternalID] = account.Name
		localIDs[account.ExternalID] = struct{}{}
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return BatchCloneResult{}, fmt.Errorf("decrypt admin API key: %w", err)
	}
	remoteAccounts, err := s.fetchAccounts(ctx, station.BaseURL, apiKey)
	if err != nil {
		return BatchCloneResult{}, err
	}
	remoteByID := make(map[int64]remoteAccount, len(remoteAccounts))
	for _, account := range remoteAccounts {
		remoteByID[account.ID] = account
	}

	for _, group := range in.Groups {
		source, sourceOK := remoteByID[group.SourceAccountExternalID]
		sourceName := strings.TrimSpace(localNames[group.SourceAccountExternalID])
		if sourceOK && strings.TrimSpace(source.Name) != "" {
			sourceName = strings.TrimSpace(source.Name)
		}
		if sourceName == "" {
			sourceName = fmt.Sprintf("账号 #%d", group.SourceAccountExternalID)
		}
		var sourceErr error
		if _, exists := localIDs[group.SourceAccountExternalID]; !exists {
			sourceErr = errors.New("源账号不存在或快照已过期，请先刷新后重试")
		} else if !sourceOK {
			sourceErr = errors.New("远端源账号不存在或已被删除，请先刷新后重试")
		} else if source.ParentAccountID != nil {
			sourceErr = errors.New("影子账号不支持克隆，请选择其母账号")
		} else if !batchCloneAccountTypeAllowed(source.Type) {
			sourceErr = errors.New("该账号类型不支持克隆，请选择 API Key、上游、Bedrock 或服务账号")
		}

		for _, requested := range group.Accounts {
			name := strings.TrimSpace(requested.Name)
			if name == "" {
				name = sourceName
			}
			item := BatchCloneResultItem{
				SourceAccountExternalID: group.SourceAccountExternalID,
				SourceAccountName:       sourceName,
				Name:                    name,
			}
			result.Requested++
			if sourceErr != nil {
				item.Error = sourceErr.Error()
				result.Failed++
				result.Results = append(result.Results, item)
				continue
			}

			idempotencyKey, keyErr := newBatchCloneIdempotencyKey()
			if keyErr != nil {
				item.Error = sanitizeBatchCloneError(keyErr, requested.APIKey, apiKey)
				result.Failed++
				result.Results = append(result.Results, item)
				continue
			}
			cloned, cloneErr := s.duplicateRemote(ctx, station.BaseURL, apiKey, source.ID, idempotencyKey)
			if cloneErr != nil {
				item.Error = sanitizeBatchCloneError(cloneErr, requested.APIKey, apiKey)
				result.Failed++
				result.Results = append(result.Results, item)
				continue
			}
			if cloned != nil {
				item.ExternalID = cloned.ID
			}
			if item.ExternalID <= 0 {
				item.Error = "远端克隆账号响应缺少有效账号 ID"
				result.Failed++
				result.Results = append(result.Results, item)
				continue
			}

			body := batchCloneUpdateBody(&source, cloned, name, strings.TrimSpace(requested.APIKey), strings.TrimSpace(requested.BaseURL))
			if err := s.put(ctx, fmt.Sprintf("%s/api/v1/admin/accounts/%d", station.BaseURL, item.ExternalID), apiKey, body); err != nil {
				item.Error = sanitizeBatchCloneError(err, requested.APIKey, apiKey)
				result.Failed++
				result.Results = append(result.Results, item)
				continue
			}
			item.Success = true
			result.Succeeded++
			result.Results = append(result.Results, item)
		}
	}

	if err := s.SyncSnapshot(ctx, stationID); err != nil {
		result.SyncError = sanitizeBatchCloneError(err, apiKey)
	}
	return result, nil
}

func validateBatchCloneInput(in BatchCloneInput) error {
	if len(in.Groups) == 0 || len(in.Groups) > maxBatchCloneGroups {
		return fmt.Errorf("%w：groups 必须包含 1 到 %d 个源账号分组", ErrInvalidBatchCloneInput, maxBatchCloneGroups)
	}
	seenSources := make(map[int64]struct{}, len(in.Groups))
	total := 0
	for _, group := range in.Groups {
		if group.SourceAccountExternalID <= 0 {
			return fmt.Errorf("%w：源账号 ID 无效", ErrInvalidBatchCloneInput)
		}
		if _, exists := seenSources[group.SourceAccountExternalID]; exists {
			return fmt.Errorf("%w：源账号不能重复选择", ErrInvalidBatchCloneInput)
		}
		seenSources[group.SourceAccountExternalID] = struct{}{}
		if len(group.Accounts) == 0 || len(group.Accounts) > maxBatchClonePerGroup {
			return fmt.Errorf("%w：每个源账号必须包含 1 到 %d 个新账号", ErrInvalidBatchCloneInput, maxBatchClonePerGroup)
		}
		total += len(group.Accounts)
		if total > maxBatchCloneAccounts {
			return fmt.Errorf("%w：一次最多新增 %d 个账号", ErrInvalidBatchCloneInput, maxBatchCloneAccounts)
		}
		for _, account := range group.Accounts {
			if strings.TrimSpace(account.APIKey) == "" {
				return fmt.Errorf("%w：API Key 不能为空", ErrInvalidBatchCloneInput)
			}
			if len([]rune(strings.TrimSpace(account.Name))) > 255 {
				return fmt.Errorf("%w：账号名称不能超过 255 个字符", ErrInvalidBatchCloneInput)
			}
			if len(account.APIKey) > 4096 {
				return fmt.Errorf("%w：API Key 过长", ErrInvalidBatchCloneInput)
			}
			if len([]rune(strings.TrimSpace(account.BaseURL))) > 1024 {
				return fmt.Errorf("%w：Base URL 不能超过 1024 个字符", ErrInvalidBatchCloneInput)
			}
		}
	}
	return nil
}

func countBatchCloneAccounts(in BatchCloneInput) int {
	total := 0
	for _, group := range in.Groups {
		total += len(group.Accounts)
	}
	return total
}

func newBatchCloneIdempotencyKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := crand.Read(raw); err != nil {
		return "", err
	}
	return "gatewayops-account-duplicate-" + hex.EncodeToString(raw), nil
}

func batchCloneAccountTypeAllowed(accountType string) bool {
	switch strings.ToLower(strings.TrimSpace(accountType)) {
	case "apikey", "upstream", "bedrock", "service_account":
		return true
	default:
		return false
	}
}

func batchCloneUpdateBody(source, cloned *remoteAccount, name, apiKey, baseURL string) map[string]any {
	raw := map[string]any{}
	if cloned != nil && len(cloned.Credentials.Raw) > 0 {
		raw = cloned.Credentials.Raw
	} else if source != nil {
		raw = source.Credentials.Raw
	}
	credentials := make(map[string]any, len(raw)+1)
	for key, value := range raw {
		credentials[key] = value
	}
	credentials["api_key"] = apiKey
	if strings.TrimSpace(baseURL) != "" {
		credentials["base_url"] = strings.TrimSpace(baseURL)
	}
	return map[string]any{
		"name":        name,
		"credentials": credentials,
		"status":      "inactive",
		"schedulable": false,
	}
}

func sanitizeBatchCloneError(err error, credentials ...string) string {
	message := strings.TrimSpace(err.Error())
	for _, credential := range credentials {
		if credential = strings.TrimSpace(credential); credential != "" {
			message = strings.ReplaceAll(message, credential, "[已隐藏]")
		}
	}
	if message == "" {
		return "批量新增账号失败"
	}
	return message
}

// DeleteAllAccounts removes every account in one remote workflow and refreshes
// the local snapshot once at the end.
func (s *Service) DeleteAllAccounts(ctx context.Context, stationID uint) (int, error) {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return 0, err
	}
	accounts, err := s.stations.ListAccounts(stationID)
	if err != nil {
		return 0, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return 0, fmt.Errorf("decrypt admin API key: %w", err)
	}
	deleted := 0
	for _, account := range accounts {
		if err := s.deleteRemote(ctx, fmt.Sprintf("%s/api/v1/admin/accounts/%d", station.BaseURL, account.ExternalID), apiKey); err != nil {
			return deleted, fmt.Errorf("删除账号“%s”失败: %w", account.Name, err)
		}
		if err := s.stations.DeleteCostOverride(stationID, account.ExternalID); err != nil {
			return deleted, fmt.Errorf("清理账号“%s”成本覆盖失败: %w", account.Name, err)
		}
		deleted++
	}
	if err := s.SyncSnapshot(ctx, stationID); err != nil {
		return deleted, fmt.Errorf("账号已删除，但刷新本地快照失败: %w", err)
	}
	return deleted, nil
}

// DeleteGroup removes the real Sub2API group and then refreshes account/group
// assignments so the Hub mirrors any cascading changes made by Sub2API.
func (s *Service) DeleteGroup(ctx context.Context, stationID uint, externalID int64) error {
	if externalID <= 0 {
		return errors.New("无效的分组 ID")
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	if _, err := s.stations.FindGroupByExternalID(stationID, externalID); err != nil {
		return errors.New("分组不存在或快照已过期，请先刷新后重试")
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return fmt.Errorf("decrypt admin API key: %w", err)
	}
	endpoint := fmt.Sprintf("%s/api/v1/admin/groups/%d", station.BaseURL, externalID)
	if err := s.deleteRemote(ctx, endpoint, apiKey); err != nil {
		return fmt.Errorf("删除中转站分组失败: %w", err)
	}
	if err := s.SyncSnapshot(ctx, stationID); err != nil {
		return fmt.Errorf("分组已从中转站删除，但刷新本地快照失败: %w", err)
	}
	s.invalidatePublicMonitor(stationID)
	return nil
}

// DeleteAllGroups removes every group in one remote workflow and refreshes the
// local snapshot once at the end.
func (s *Service) DeleteAllGroups(ctx context.Context, stationID uint) (int, error) {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return 0, err
	}
	groups, err := s.stations.ListGroups(stationID)
	if err != nil {
		return 0, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return 0, fmt.Errorf("decrypt admin API key: %w", err)
	}
	deleted := 0
	for _, group := range groups {
		if err := s.deleteRemote(ctx, fmt.Sprintf("%s/api/v1/admin/groups/%d", station.BaseURL, group.ExternalID), apiKey); err != nil {
			return deleted, fmt.Errorf("删除分组“%s”失败: %w", group.Name, err)
		}
		deleted++
	}
	if err := s.SyncSnapshot(ctx, stationID); err != nil {
		return deleted, fmt.Errorf("分组已删除，但刷新本地快照失败: %w", err)
	}
	s.invalidatePublicMonitor(stationID)
	return deleted, nil
}

// UpdateGroup applies the same partial-update contract used by Sub2API's group
// management page, then refreshes only the affected local group snapshot.
func (s *Service) UpdateGroup(ctx context.Context, stationID uint, externalID int64, in GroupUpdateInput) (*storage.RelayGroup, error) {
	if externalID <= 0 {
		return nil, errors.New("无效的分组 ID")
	}
	if in.Name == nil && in.Description == nil && in.RateMultiplier == nil && in.IsExclusive == nil && in.Status == nil && in.ModelTypes == nil && in.MonitorEnabled == nil {
		return nil, errors.New("至少需要修改一个分组字段")
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return nil, err
	}
	group, err := s.stations.FindGroupByExternalID(stationID, externalID)
	if err != nil {
		return nil, err
	}
	previousModelTypesJSON := group.ModelTypesJSON
	body, err := applyGroupUpdate(group, in)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypt admin API key: %w", err)
		}
		endpoint := fmt.Sprintf("%s/api/v1/admin/groups/%d", station.BaseURL, externalID)
		if err := s.put(ctx, endpoint, apiKey, body); err != nil {
			return nil, fmt.Errorf("更新中转站分组失败: %w", err)
		}
	}
	group.SyncedAt = time.Now().UTC()
	oldModelType, newModelType, syncAccountModelType := modelTypeSyncPair(previousModelTypesJSON, group.ModelTypesJSON)
	if syncAccountModelType {
		if err := s.stations.UpdateGroupAndSyncAccountModelType(group, oldModelType, newModelType); err != nil {
			return nil, err
		}
	} else if err := s.stations.UpdateGroup(group); err != nil {
		return nil, err
	}
	if in.MonitorEnabled != nil || in.IsExclusive != nil {
		s.invalidatePublicMonitor(stationID)
	}
	return group, nil
}

// UpdateGroupSortOrders forwards a complete ordering to Sub2API and mirrors it locally.
func (s *Service) UpdateGroupSortOrders(ctx context.Context, stationID uint, updates []GroupSortOrderUpdate) error {
	if len(updates) == 0 {
		return errors.New("排序列表不能为空")
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	groups, err := s.stations.ListGroups(stationID)
	if err != nil {
		return err
	}
	local, err := validateGroupSortOrderUpdates(groups, updates)
	if err != nil {
		return err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return fmt.Errorf("decrypt admin API key: %w", err)
	}
	if err := s.put(ctx, station.BaseURL+"/api/v1/admin/groups/sort-order", apiKey, map[string]any{"updates": updates}); err != nil {
		return fmt.Errorf("更新中转站分组排序失败: %w", err)
	}
	return s.stations.UpdateGroupSortOrders(stationID, local)
}

func validateGroupSortOrderUpdates(groups []storage.RelayGroup, updates []GroupSortOrderUpdate) (map[int64]int, error) {
	if len(groups) != len(updates) {
		return nil, errors.New("排序列表必须包含当前站点的全部分组")
	}
	known := make(map[int64]struct{}, len(groups))
	for _, group := range groups {
		known[group.ExternalID] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(updates))
	orders := make(map[int]int64, len(updates))
	local := make(map[int64]int, len(updates))
	for _, update := range updates {
		if update.ID <= 0 || update.SortOrder < 0 {
			return nil, errors.New("分组排序参数无效")
		}
		if _, ok := known[update.ID]; !ok {
			return nil, errors.New("排序列表包含未知分组")
		}
		if _, ok := seen[update.ID]; ok {
			return nil, errors.New("排序列表包含重复分组")
		}
		seen[update.ID] = struct{}{}
		if _, ok := orders[update.SortOrder]; ok {
			return nil, errors.New("分组排序值不能重复")
		}
		orders[update.SortOrder] = update.ID
		local[update.ID] = update.SortOrder
	}
	return local, nil
}

// modelTypeSyncPair returns a deterministic account binding transition. A
// group with multiple model types cannot be represented by RelayAccount's
// single model_type field, so those changes remain an explicit manual action.
func modelTypeSyncPair(previousJSON, nextJSON string) (oldType, newType string, ok bool) {
	parse := func(raw string) (string, bool) {
		var values []string
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &values); err != nil {
				return "", false
			}
		}
		seen := make(map[string]struct{}, len(values))
		unique := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			unique = append(unique, value)
		}
		if len(unique) > 1 {
			return "", false
		}
		if len(unique) == 0 {
			return "", true
		}
		return unique[0], true
	}
	oldType, oldOK := parse(previousJSON)
	newType, newOK := parse(nextJSON)
	return oldType, newType, oldOK && newOK && oldType != newType
}

func applyGroupUpdate(group *storage.RelayGroup, in GroupUpdateInput) (map[string]any, error) {
	body := make(map[string]any, 5)
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, errors.New("分组名称不能为空")
		}
		group.Name = name
		body["name"] = name
	}
	if in.Description != nil {
		description := strings.TrimSpace(*in.Description)
		group.Description = description
		body["description"] = description
	}
	if in.RateMultiplier != nil {
		if *in.RateMultiplier < 0 || math.IsNaN(*in.RateMultiplier) || math.IsInf(*in.RateMultiplier, 0) {
			return nil, errors.New("分组倍率必须是非负数")
		}
		group.RateMultiplier = *in.RateMultiplier
		body["rate_multiplier"] = *in.RateMultiplier
	}
	if in.IsExclusive != nil {
		group.IsExclusive = *in.IsExclusive
		// 公开分组参与公开监控，专属分组不进入公开监控。
		// 类型切换时由类型统一决定监控状态，避免两者出现矛盾。
		group.MonitorEnabled = !*in.IsExclusive
		body["is_exclusive"] = *in.IsExclusive
	}
	if in.Status != nil {
		status := strings.ToLower(strings.TrimSpace(*in.Status))
		if status != "active" && status != "inactive" {
			return nil, errors.New("分组状态只支持 active 或 inactive")
		}
		group.Status = status
		body["status"] = status
	}
	if in.ModelTypes != nil {
		seen := make(map[string]struct{}, len(*in.ModelTypes))
		values := make([]string, 0, len(*in.ModelTypes))
		for _, value := range *in.ModelTypes {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
		sort.Strings(values)
		encoded, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		group.ModelTypesJSON = string(encoded)
		// Model types are a Hub-side constraint; Sub2API has no corresponding field.
	}
	if in.MonitorEnabled != nil && in.IsExclusive == nil {
		group.MonitorEnabled = *in.MonitorEnabled
		// Monitoring is Hub-side state and must not be sent to Sub2API.
	}
	return body, nil
}

type remoteEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type remoteGroup struct {
	ID                   int64    `json:"id"`
	Name                 string   `json:"name"`
	Description          string   `json:"description"`
	Platform             string   `json:"platform"`
	Status               string   `json:"status"`
	IsExclusive          bool     `json:"is_exclusive"`
	RequireOAuthOnly     bool     `json:"require_oauth_only"`
	SortOrder            int      `json:"sort_order"`
	RateMultiplier       float64  `json:"rate_multiplier"`
	AllowImageGeneration bool     `json:"allow_image_generation"`
	ImageRateIndependent bool     `json:"image_rate_independent"`
	ImageRateMultiplier  float64  `json:"image_rate_multiplier"`
	ImagePrice1K         *float64 `json:"image_price_1k"`
	ImagePrice2K         *float64 `json:"image_price_2k"`
	ImagePrice4K         *float64 `json:"image_price_4k"`
}

// remoteAccount 是 Sub2API 管理端账户列表的最小稳定字段集。group_ids 由
// PUT /api/v1/admin/accounts/:id 使用，rate_multiplier 是账号接入上游的成本倍率。
type remoteAccount struct {
	ID                 int64                    `json:"id"`
	ParentAccountID    *int64                   `json:"parent_account_id"`
	Name               string                   `json:"name"`
	Credentials        remoteAccountCredentials `json:"credentials"`
	Platform           string                   `json:"platform"`
	Type               string                   `json:"type"`
	Status             string                   `json:"status"`
	Schedulable        bool                     `json:"schedulable"`
	Concurrency        int                      `json:"concurrency"`
	CurrentConcurrency int                      `json:"current_concurrency"`
	Priority           int                      `json:"priority"`
	LastUsedAt         *time.Time               `json:"last_used_at"`
	RateMultiplier     *float64                 `json:"rate_multiplier"`
	GroupIDs           []int64                  `json:"group_ids"`
	Extra              map[string]any           `json:"extra"`
}

type remoteAccountCredentials struct {
	BaseURL            string         `json:"base_url"`
	PoolMode           bool           `json:"pool_mode"`
	PoolModeRetryCount *int           `json:"pool_mode_retry_count"`
	Raw                map[string]any `json:"-"`
}

func (c *remoteAccountCredentials) UnmarshalJSON(data []byte) error {
	type alias remoteAccountCredentials
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	decoded.Raw = raw
	*c = remoteAccountCredentials(decoded)
	return nil
}

func remoteAccountPlan(account remoteAccount) string {
	keys := []string{"plan_type", "account_type", "subscription_type", "chatgpt_plan_type"}
	for _, key := range keys {
		if value := strings.TrimSpace(fmt.Sprint(account.Extra[key])); value != "" && value != "<nil>" {
			return strings.ToLower(value)
		}
	}
	return "unknown"
}

func accountPoolModeRetryCount(account remoteAccount) int {
	if account.Credentials.PoolModeRetryCount != nil && *account.Credentials.PoolModeRetryCount >= 0 {
		return *account.Credentials.PoolModeRetryCount
	}
	return 3
}

func upstreamProbeCost(extra map[string]any) (*float64, *time.Time) {
	probe, ok := extra["upstream_billing_probe"].(map[string]any)
	if !ok || strings.ToLower(strings.TrimSpace(fmt.Sprint(probe["status"]))) != "ok" {
		return nil, nil
	}
	data, ok := probe["data"].(map[string]any)
	if !ok {
		return nil, nil
	}
	raw, ok := data["resolved_rate_multiplier"]
	if !ok {
		return nil, nil
	}
	var cost float64
	switch value := raw.(type) {
	case float64:
		cost = value
	case float32:
		cost = float64(value)
	case int:
		cost = float64(value)
	default:
		return nil, nil
	}
	if cost < 0 || cost != cost {
		return nil, nil
	}
	var observed *time.Time
	for _, key := range []string{"observed_at", "received_at"} {
		if value, ok := data[key].(string); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				parsed = parsed.UTC()
				observed = &parsed
				break
			}
		}
	}
	if observed == nil {
		if value, ok := probe["received_at"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				parsed = parsed.UTC()
				observed = &parsed
			}
		}
	}
	return &cost, observed
}

// retainLastProbeCost keeps a transient upstream probe failure from turning a
// previously known cost into an unknown cost. The original observation time
// remains unchanged so callers can still see that the retained value is old.
func retainLastProbeCost(cost *float64, observed *time.Time, previous storage.RelayAccount) (*float64, *time.Time) {
	if cost != nil {
		return cost, observed
	}
	if previous.RateMultiplier == nil || previous.RateSource != "upstream_probe" || *previous.RateMultiplier < 0 {
		return nil, nil
	}
	value := *previous.RateMultiplier
	return &value, previous.RateObservedAt
}

type remoteChannel struct {
	ID                         int64                        `json:"id"`
	Name                       string                       `json:"name"`
	Description                string                       `json:"description"`
	Status                     string                       `json:"status"`
	GroupIDs                   []int64                      `json:"group_ids"`
	BillingModelSource         string                       `json:"billing_model_source"`
	RestrictModels             bool                         `json:"restrict_models"`
	ModelMapping               map[string]map[string]string `json:"model_mapping"`
	ModelPricing               []remoteModelPricing         `json:"model_pricing"`
	ApplyPricingToAccountStats bool                         `json:"apply_pricing_to_account_stats"`
	AccountStatsPricingRules   []remotePricingRule          `json:"account_stats_pricing_rules"`
}

type remoteModelPricing struct {
	ID               int64    `json:"id"`
	Platform         string   `json:"platform"`
	Models           []string `json:"models"`
	BillingMode      string   `json:"billing_mode"`
	InputPrice       *float64 `json:"input_price"`
	OutputPrice      *float64 `json:"output_price"`
	CacheWritePrice  *float64 `json:"cache_write_price"`
	CacheReadPrice   *float64 `json:"cache_read_price"`
	ImageInputPrice  *float64 `json:"image_input_price"`
	ImageOutputPrice *float64 `json:"image_output_price"`
	PerRequestPrice  *float64 `json:"per_request_price"`
	Intervals        []any    `json:"intervals"`
	FastMultiplier   *float64 `json:"fast_multiplier"`
	FlexMultiplier   *float64 `json:"flex_multiplier"`
	TimePricing      any      `json:"time_pricing"`
}

type remotePricingRule struct {
	ID         int64                `json:"id"`
	Name       string               `json:"name"`
	GroupIDs   []int64              `json:"group_ids"`
	AccountIDs []int64              `json:"account_ids"`
	Pricing    []remoteModelPricing `json:"pricing"`
}

type channelPricingSnapshot struct {
	BillingModelSource         string                       `json:"billing_model_source,omitempty"`
	RestrictModels             bool                         `json:"restrict_models"`
	ModelMapping               map[string]map[string]string `json:"model_mapping,omitempty"`
	ModelPricing               []remoteModelPricing         `json:"model_pricing,omitempty"`
	ApplyPricingToAccountStats bool                         `json:"apply_pricing_to_account_stats"`
	AccountStatsPricingRules   []remotePricingRule          `json:"account_stats_pricing_rules,omitempty"`
}

type remotePage struct {
	Items    []remoteChannel `json:"items"`
	Page     int             `json:"page"`
	Pages    int             `json:"pages"`
	PageSize int             `json:"page_size"`
}

type remoteAccountPage struct {
	Items    []remoteAccount `json:"items"`
	Page     int             `json:"page"`
	Pages    int             `json:"pages"`
	PageSize int             `json:"page_size"`
}

type remoteUser struct {
	ID                 int64      `json:"id"`
	Username           string     `json:"username"`
	Email              string     `json:"email"`
	Role               string     `json:"role"`
	Balance            float64    `json:"balance"`
	Concurrency        int        `json:"concurrency"`
	CurrentConcurrency int        `json:"current_concurrency"`
	RPMLimit           int        `json:"rpm_limit"`
	Status             string     `json:"status"`
	LastUsedAt         *time.Time `json:"last_used_at"`
	CreatedAt          time.Time  `json:"created_at"`
	Notes              string     `json:"notes"`
	DeletedAt          *time.Time `json:"deleted_at"`
}

type remoteUserPage struct {
	Items    []remoteUser `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	Pages    int          `json:"pages"`
	PageSize int          `json:"page_size"`
}

type remoteAuditLog struct {
	CreatedAt  time.Time `json:"created_at"`
	Action     string    `json:"action"`
	ClientIP   string    `json:"client_ip"`
	StatusCode int       `json:"status_code"`
}

type remoteAuditLogPage struct {
	Items    []remoteAuditLog `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	Pages    int              `json:"pages"`
	PageSize int              `json:"page_size"`
}

type remoteUserRankingItem struct {
	UserID     int64   `json:"user_id"`
	ActualCost float64 `json:"actual_cost"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
}

type remoteUserRanking struct {
	Ranking         []remoteUserRankingItem `json:"ranking"`
	TotalActualCost float64                 `json:"total_actual_cost"`
	TotalRequests   int64                   `json:"total_requests"`
	TotalTokens     int64                   `json:"total_tokens"`
}

type remoteBatchUpdateResult struct {
	Affected int `json:"affected"`
}

type remoteUsage struct {
	ID                    int64   `json:"id"`
	RequestID             string  `json:"request_id"`
	ClientRequestID       string  `json:"client_request_id"`
	AccountID             int64   `json:"account_id"`
	AccountName           string  `json:"account_name"`
	UserID                int64   `json:"user_id"`
	UserName              string  `json:"user_name"`
	UserEmail             string  `json:"user_email"`
	IPAddress             string  `json:"ip_address"`
	GroupID               int64   `json:"group_id"`
	GroupName             string  `json:"group_name"`
	FirstTokenMS          int64   `json:"first_token_ms"`
	DurationMS            int64   `json:"duration_ms"`
	CreatedAt             string  `json:"created_at"`
	Model                 string  `json:"model"`
	RequestedModel        string  `json:"requested_model"`
	RequestType           string  `json:"request_type"`
	InputTokens           int64   `json:"input_tokens"`
	OutputTokens          int64   `json:"output_tokens"`
	CacheReadTokens       int64   `json:"cache_read_tokens"`
	CacheCreationTokens   int64   `json:"cache_creation_tokens"`
	CacheCreation5mTokens int64   `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int64   `json:"cache_creation_1h_tokens"`
	CacheTTLOverridden    bool    `json:"cache_ttl_overridden"`
	ActualCost            float64 `json:"actual_cost"`
	TotalCost             float64 `json:"total_cost"`
}

type remoteUsagePage struct {
	Items []remoteUsage `json:"items"`
}

type remoteUsageStats struct {
	TotalTokens      int64   `json:"total_tokens"`
	TotalCost        float64 `json:"total_cost"`
	TotalActualCost  float64 `json:"total_actual_cost"`
	TotalAccountCost float64 `json:"total_account_cost"`
	TotalRequests    int64   `json:"total_requests"`
	RequestCount     int64   `json:"request_count"`
}

type RecentUsageItem struct {
	ID                    int64     `json:"id"`
	UserID                int64     `json:"user_id"`
	UserEmail             string    `json:"user_email"`
	UserName              string    `json:"user_name"`
	IPAddress             string    `json:"ip_address"`
	IPLocation            string    `json:"ip_location"`
	GroupID               int64     `json:"group_id"`
	GroupName             string    `json:"group_name"`
	AccountID             int64     `json:"account_id"`
	AccountName           string    `json:"account_name"`
	Model                 string    `json:"model"`
	RequestType           string    `json:"request_type"`
	InputTokens           int64     `json:"input_tokens"`
	OutputTokens          int64     `json:"output_tokens"`
	CacheReadTokens       int64     `json:"cache_read_tokens"`
	CacheCreationTokens   int64     `json:"cache_creation_tokens"`
	CacheCreation5mTokens int64     `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int64     `json:"cache_creation_1h_tokens"`
	CacheTTLOverridden    bool      `json:"cache_ttl_overridden"`
	UserCharge            float64   `json:"user_charge"`
	OriginalCost          float64   `json:"original_cost"`
	FirstTokenMS          int64     `json:"first_token_ms"`
	DurationMS            int64     `json:"duration_ms"`
	CreatedAt             time.Time `json:"created_at"`
}

type UserBalanceHistoryItem struct {
	ID           int64      `json:"id"`
	Type         string     `json:"type"`
	Value        float64    `json:"value"`
	Notes        string     `json:"notes"`
	Code         string     `json:"code"`
	UsedAt       *time.Time `json:"used_at"`
	CreatedAt    time.Time  `json:"created_at"`
	ValidityDays *int       `json:"validity_days,omitempty"`
	Group        *struct {
		Name string `json:"name"`
	} `json:"group,omitempty"`
}

type UserBalanceHistory struct {
	User           remoteUser               `json:"user"`
	Items          []UserBalanceHistoryItem `json:"items"`
	Page           int                      `json:"page"`
	Pages          int                      `json:"pages"`
	Total          int                      `json:"total"`
	TotalRecharged float64                  `json:"total_recharged"`
}

type AccountTestResult struct {
	StatusCode int    `json:"status_code"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output"`
}

type GroupTestCallResult struct {
	Index      int    `json:"index"`
	Success    bool   `json:"success"`
	StatusCode int    `json:"status_code"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output"`
}

type GroupTestResult struct {
	Model     string                `json:"model"`
	Requested int                   `json:"requested"`
	Succeeded int                   `json:"succeeded"`
	Failed    int                   `json:"failed"`
	Results   []GroupTestCallResult `json:"results"`
}

func (s *Service) AccountModels(ctx context.Context, stationID uint, accountID int64) ([]string, error) {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return nil, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt admin API key: %w", err)
	}
	var raw json.RawMessage
	endpoint := fmt.Sprintf("%s/api/v1/admin/accounts/%d/models", station.BaseURL, accountID)
	if err := s.get(ctx, endpoint, apiKey, &raw); err != nil {
		return nil, err
	}
	models := extractAccountModelIDs(raw)
	if len(models) == 0 {
		return nil, errors.New("该账号没有可用测试模型")
	}
	return models, nil
}

func extractAccountModelIDs(raw json.RawMessage) []string {
	seen := make(map[string]struct{})
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	var stringsList []string
	if json.Unmarshal(raw, &stringsList) == nil {
		for _, value := range stringsList {
			add(value)
		}
	}
	var objects []map[string]any
	if json.Unmarshal(raw, &objects) == nil {
		for _, item := range objects {
			for _, key := range []string{"id", "model_id", "name", "value"} {
				if value, ok := item[key].(string); ok {
					add(value)
					break
				}
			}
		}
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) == nil {
		for _, key := range []string{"models", "items", "data"} {
			if nested := envelope[key]; len(nested) > 0 {
				for _, value := range extractAccountModelIDs(nested) {
					add(value)
				}
			}
		}
	}
	models := make([]string, 0, len(seen))
	for value := range seen {
		models = append(models, value)
	}
	sort.Strings(models)
	return models
}

// UsageStats aggregates Sub2API's actual user charge and token count for every
// account. Results are cached briefly because the relay account view refreshes
// regularly, while the source aggregation is read-only but relatively expensive.
func (s *Service) UsageStats(ctx context.Context, stationID uint, rangeName string, since time.Time) (UsageSummary, error) {
	accounts, err := s.stations.ListAccounts(stationID)
	if err != nil {
		return UsageSummary{}, err
	}
	accountIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		accountIDs = append(accountIDs, account.ExternalID)
	}
	return s.usageStatsForAccountIDs(ctx, stationID, rangeName, since, accountIDs)
}

// usageStatsForAccountIDs reads interval usage only for the specified remote
// accounts. Channel metrics use this narrower path so a few explicit cost
// bindings never trigger a fan-out over every account in a relay station.
func (s *Service) usageStatsForAccountIDs(ctx context.Context, stationID uint, rangeName string, since time.Time, accountIDs []int64) (UsageSummary, error) {
	accountIDs = normalizedUsageAccountIDs(accountIDs)
	result := UsageSummary{Accounts: make(map[int64]AccountUsageStats, len(accountIDs))}
	if len(accountIDs) == 0 {
		return result, nil
	}
	cacheKey := accountUsageCacheKey(stationID, rangeName, since, accountIDs)
	s.usageMu.Lock()
	if cached, ok := s.usageCache[cacheKey]; ok && time.Now().Before(cached.ExpiresAt) {
		s.usageMu.Unlock()
		return cached.Summary, nil
	}
	s.usageMu.Unlock()

	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return UsageSummary{}, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return UsageSummary{}, fmt.Errorf("decrypt admin API key: %w", err)
	}

	type usageResult struct {
		id    int64
		stats AccountUsageStats
		err   error
	}
	jobs := make(chan int64)
	results := make(chan usageResult)
	workers := 8
	if len(accountIDs) < workers {
		workers = len(accountIDs)
	}
	until := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for accountID := range jobs {
				var remote remoteUsageStats
				endpoint := usageStatsEndpoint(station.BaseURL, accountID, since, until)
				err := s.get(ctx, endpoint, apiKey, &remote)
				requests := remote.TotalRequests
				if requests == 0 {
					requests = remote.RequestCount
				}
				results <- usageResult{id: accountID, stats: AccountUsageStats{TotalTokens: remote.TotalTokens, UserCharge: remote.TotalActualCost, AccountCost: remote.TotalAccountCost, BaseCost: remote.TotalCost, Requests: requests}, err: err}
			}
		}()
	}
	go func() {
		for _, accountID := range accountIDs {
			jobs <- accountID
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	for item := range results {
		if item.err != nil {
			result.FailedAccounts++
			continue
		}
		result.Accounts[item.id] = item.stats
		result.TotalTokens += item.stats.TotalTokens
		result.UserCharge += item.stats.UserCharge
	}
	if result.FailedAccounts == len(accountIDs) {
		return result, errors.New("读取中转站账号消费统计失败")
	}
	s.usageMu.Lock()
	s.usageCache[cacheKey] = usageCacheEntry{ExpiresAt: time.Now().Add(time.Minute), Summary: result}
	s.usageMu.Unlock()
	return result, nil
}

// ChannelUsageTotal contains both sides of a channel's interval usage. Both
// values are calculated from the same explicit account-to-channel cost
// bindings so a channel cannot show cost for one account set and revenue for
// another set.
type ChannelUsageTotal struct {
	Cost                float64
	CostBasis           string
	UserCharge          float64
	MatchedAccountCount int
	Complete            bool
}

// ChannelUsageTotals aggregates interval usage by channel. An explicit
// channel-group cost binding always wins; otherwise an account is attributed
// to the uniquely matching monitored channel based on its Base URL. Both cost
// and user charge use this exact same account-to-channel mapping.
func (s *Service) ChannelUsageTotals(ctx context.Context, channels []storage.Channel, rangeName string, since time.Time) (map[uint]ChannelUsageTotal, error) {
	stations, err := s.stations.List()
	if err != nil {
		return nil, err
	}
	rateByChannel := make(map[uint][]storage.RateSnapshot, len(channels))
	if s.rates != nil {
		for _, channel := range channels {
			rates, err := s.rates.ListByChannel(channel.ID)
			if err != nil {
				return nil, err
			}
			rateByChannel[channel.ID] = rates
		}
	}
	totals := make(map[uint]ChannelUsageTotal, len(channels))
	basisEntries := make(map[uint][]channelUsageCostBasisEntry, len(channels))
	for _, channel := range channels {
		totals[channel.ID] = ChannelUsageTotal{Complete: true}
	}
	for _, station := range stations {
		overrides, err := s.stations.ListCostOverrides(station.ID)
		if err != nil {
			return nil, err
		}
		accounts, err := s.stations.ListAccounts(station.ID)
		if err != nil {
			return nil, err
		}
		bindings := resolveChannelUsageCostBindings(channels, accounts, overrides, rateByChannel)
		if len(bindings) == 0 {
			continue
		}
		accountIDs := make([]int64, 0, len(bindings))
		for accountID := range bindings {
			accountIDs = append(accountIDs, accountID)
		}
		usage, usageErr := s.usageStatsForAccountIDs(ctx, station.ID, rangeName, since, accountIDs)
		for accountID, binding := range bindings {
			channelID := binding.ChannelID
			total, exists := totals[channelID]
			if !exists {
				continue
			}
			total.MatchedAccountCount++
			stats, ok := usage.Accounts[accountID]
			if usageErr != nil || !ok {
				total.Complete = false
			} else {
				cost, multiplierBits, usesAccountCost := channelBoundAccountCostCalculation(stats, binding)
				total.Cost += cost
				total.UserCharge += stats.UserCharge
				basisEntries[channelID] = append(basisEntries[channelID], channelUsageCostBasisEntry{
					RelayStationID: station.ID, RelayAccountExternalID: accountID,
					MultiplierBits: multiplierBits, UsesAccountCost: usesAccountCost,
				})
			}
			totals[channelID] = total
		}
	}
	for channelID, total := range totals {
		total.CostBasis = channelUsageCostBasis(basisEntries[channelID])
		totals[channelID] = total
	}
	return totals, nil
}

// ChannelLatencyTrends returns the latest latency samples for each monitored
// channel. Samples are read from the account snapshots populated by sync, then
// attributed with the same explicit-binding/automatic-URL rules as cost and
// user-charge metrics.
func (s *Service) ChannelLatencyTrends(channels []storage.Channel, limit int) (map[uint][]storage.RelayLatencySample, error) {
	if limit <= 0 || limit > 100 {
		limit = latencySampleLimit
	}
	result := make(map[uint][]storage.RelayLatencySample, len(channels))
	for _, channel := range channels {
		result[channel.ID] = []storage.RelayLatencySample{}
	}
	ratesByChannel := make(map[uint][]storage.RateSnapshot, len(channels))
	if s.rates != nil {
		for _, channel := range channels {
			rates, err := s.rates.ListByChannel(channel.ID)
			if err != nil {
				return nil, err
			}
			ratesByChannel[channel.ID] = rates
		}
	}
	stations, err := s.stations.List()
	if err != nil {
		return nil, err
	}
	for _, station := range stations {
		overrides, err := s.stations.ListCostOverrides(station.ID)
		if err != nil {
			return nil, err
		}
		accounts, err := s.stations.ListAccounts(station.ID)
		if err != nil {
			return nil, err
		}
		bindings := resolveChannelUsageCostBindings(channels, accounts, overrides, ratesByChannel)
		for _, account := range accounts {
			binding, ok := bindings[account.ExternalID]
			if !ok {
				continue
			}
			var samples []storage.RelayLatencySample
			if strings.TrimSpace(account.LatencySamplesJSON) == "" || json.Unmarshal([]byte(account.LatencySamplesJSON), &samples) != nil {
				continue
			}
			for i := range samples {
				if strings.TrimSpace(samples[i].Platform) == "" {
					samples[i].Platform = account.Platform
				}
				if binding.Multiplier != nil {
					value := *binding.Multiplier
					samples[i].ChannelGroupMultiplier = &value
				}
			}
			result[binding.ChannelID] = append(result[binding.ChannelID], samples...)
		}
	}
	for channelID, samples := range result {
		sort.SliceStable(samples, func(i, j int) bool {
			return samples[i].CreatedAt.After(samples[j].CreatedAt)
		})
		if len(samples) > limit {
			samples = samples[:limit]
		}
		result[channelID] = samples
	}
	return result, nil
}

type channelUsageCostBinding struct {
	ChannelID  uint
	Mode       string
	GroupName  string
	Multiplier *float64
}

// resolveChannelUsageCostBindings returns the channel and, when available, the
// multiplier that should be used to derive account cost. Explicit bindings are
// durable operator decisions; URL matching is retained only for automatic
// channels that have no explicit binding.
func resolveChannelUsageCostBindings(channels []storage.Channel, accounts []storage.RelayAccount, overrides []storage.RelayAccountCostOverride, ratesByChannel map[uint][]storage.RateSnapshot) map[int64]channelUsageCostBinding {
	channelByID := make(map[uint]struct{}, len(channels))
	for _, channel := range channels {
		channelByID[channel.ID] = struct{}{}
	}

	result := make(map[int64]channelUsageCostBinding)
	explicitAccounts := make(map[int64]struct{})
	for _, override := range overrides {
		if (override.Mode != "channel_group" && override.Mode != "auto_link") || override.MonitorChannelID == nil || strings.TrimSpace(override.UpstreamGroup) == "" {
			continue
		}
		explicitAccounts[override.RelayAccountExternalID] = struct{}{}
		if _, exists := channelByID[*override.MonitorChannelID]; !exists {
			continue
		}
		binding := channelUsageCostBinding{ChannelID: *override.MonitorChannelID, Mode: override.Mode, GroupName: strings.TrimSpace(override.UpstreamGroup)}
		for _, rate := range ratesByChannel[*override.MonitorChannelID] {
			if rate.ModelName == override.UpstreamGroup && rate.Ratio >= 0 {
				value := rate.Ratio
				binding.Multiplier = &value
				break
			}
		}
		result[override.RelayAccountExternalID] = binding
	}

	for _, account := range accounts {
		if _, explicit := explicitAccounts[account.ExternalID]; explicit {
			continue
		}
		channelID, ok := uniqueAutomaticChannelForAccountBaseURL(channels, account.BaseURL)
		if ok {
			binding := channelUsageCostBinding{ChannelID: channelID}
			if account.RateMultiplier != nil && *account.RateMultiplier >= 0 && strings.TrimSpace(account.RateSource) != "" {
				value := *account.RateMultiplier
				binding.Multiplier = &value
			}
			for _, rate := range ratesByChannel[channelID] {
				if rate.Source == storage.RateSnapshotSourceRelayAccount && rate.RelayAccountExternalID != nil && *rate.RelayAccountExternalID == account.ExternalID {
					binding.GroupName = strings.TrimSpace(rate.ModelName)
					if binding.Multiplier == nil && rate.Ratio >= 0 {
						value := rate.Ratio
						binding.Multiplier = &value
					}
					break
				}
			}
			if binding.GroupName == "" && binding.Multiplier != nil {
				for _, rate := range ratesByChannel[channelID] {
					if rate.Ratio >= 0 && math.Abs(rate.Ratio-*binding.Multiplier) < 1e-9 {
						binding.GroupName = strings.TrimSpace(rate.ModelName)
						break
					}
				}
			}
			result[account.ExternalID] = binding
		}
	}
	return result
}

func channelBoundAccountCost(stats AccountUsageStats, binding channelUsageCostBinding) float64 {
	cost, _, _ := channelBoundAccountCostCalculation(stats, binding)
	return cost
}

func channelBoundAccountCostCalculation(stats AccountUsageStats, binding channelUsageCostBinding) (cost float64, multiplierBits uint64, usesAccountCost bool) {
	if binding.Multiplier != nil && *binding.Multiplier >= 0 {
		// The selected channel group (including an auto-linked relay account
		// group) is the source of truth for account cost. Sub2API's aggregate
		// account_cost may still use its default multiplier of 1 when the relay
		// account declaration is unavailable there, so do not prefer it over the
		// explicit binding.
		if stats.BaseCost > 0 || stats.AccountCost == 0 {
			return stats.BaseCost * *binding.Multiplier, math.Float64bits(*binding.Multiplier), false
		}
	}
	return stats.AccountCost, 0, true
}

type channelUsageCostBasisEntry struct {
	RelayStationID         uint
	RelayAccountExternalID int64
	MultiplierBits         uint64
	UsesAccountCost        bool
}

func channelUsageCostBasis(entries []channelUsageCostBasisEntry) string {
	if len(entries) == 0 {
		return ""
	}
	ordered := append([]channelUsageCostBasisEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].RelayStationID != ordered[j].RelayStationID {
			return ordered[i].RelayStationID < ordered[j].RelayStationID
		}
		return ordered[i].RelayAccountExternalID < ordered[j].RelayAccountExternalID
	})
	hash := sha256.New()
	var encoded [25]byte
	for _, entry := range ordered {
		binary.BigEndian.PutUint64(encoded[0:8], uint64(entry.RelayStationID))
		binary.BigEndian.PutUint64(encoded[8:16], uint64(entry.RelayAccountExternalID))
		binary.BigEndian.PutUint64(encoded[16:24], entry.MultiplierBits)
		encoded[24] = 0
		if entry.UsesAccountCost {
			encoded[24] = 1
		}
		_, _ = hash.Write(encoded[:])
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

// resolveChannelUsageBindings returns the accounting channel for each relay
// account. An explicit channel_group binding takes priority over Base URL
// matching. Base URL matching is used only when exactly one channel matches,
// so an ambiguous shared endpoint cannot be counted twice.
func resolveChannelUsageBindings(channels []storage.Channel, accounts []storage.RelayAccount, overrides []storage.RelayAccountCostOverride) map[int64]uint {
	bindings := resolveChannelUsageCostBindings(channels, accounts, overrides, nil)
	result := make(map[int64]uint, len(bindings))
	for accountID, binding := range bindings {
		result[accountID] = binding.ChannelID
	}
	return result
}

// Manual channels are attributed only through a durable account binding. URL
// fallback remains available for automatically monitored channels, whose
// upstream account ownership has always been inferred from the endpoint.
func uniqueAutomaticChannelForAccountBaseURL(channels []storage.Channel, accountBaseURL string) (uint, bool) {
	automatic := make([]storage.Channel, 0, len(channels))
	for _, channel := range channels {
		if channel.BalanceMode != storage.BalanceModeManual {
			automatic = append(automatic, channel)
		}
	}
	return uniqueChannelForAccountBaseURL(automatic, accountBaseURL)
}

func uniqueChannelForAccountBaseURL(channels []storage.Channel, accountBaseURL string) (uint, bool) {
	var matchedID uint
	for _, channel := range channels {
		if !channelSiteMatchesAccountBaseURL(channel.SiteURL, accountBaseURL) {
			continue
		}
		if matchedID != 0 {
			return 0, false
		}
		matchedID = channel.ID
	}
	return matchedID, matchedID != 0
}

// ChannelUserChargeTotals returns interval user charges for the same account
// bindings used by ChannelCostTotals.
func (s *Service) ChannelUserChargeTotals(ctx context.Context, channels []storage.Channel, rangeName string, since time.Time) (map[uint]ChannelUserChargeTotal, error) {
	usage, err := s.ChannelUsageTotals(ctx, channels, rangeName, since)
	if err != nil {
		return nil, err
	}
	totals := make(map[uint]ChannelUserChargeTotal, len(usage))
	for channelID, item := range usage {
		totals[channelID] = ChannelUserChargeTotal{UserCharge: item.UserCharge, MatchedAccountCount: item.MatchedAccountCount, Complete: item.Complete}
	}
	return totals, nil
}

// ChannelCostTotals aggregates interval upstream account cost for account
// overrides bound to a monitored channel group.
func (s *Service) ChannelCostTotals(ctx context.Context, channels []storage.Channel, rangeName string, since time.Time) (map[uint]ChannelCostTotal, error) {
	usage, err := s.ChannelUsageTotals(ctx, channels, rangeName, since)
	if err != nil {
		return nil, err
	}
	totals := make(map[uint]ChannelCostTotal, len(usage))
	for channelID, item := range usage {
		totals[channelID] = ChannelCostTotal{Cost: item.Cost, MatchedAccountCount: item.MatchedAccountCount, Complete: item.Complete}
	}
	return totals, nil
}

func usageCacheKey(kind string, stationID uint, rangeName string, since time.Time) string {
	return fmt.Sprintf("%s:%d:%s:%s", kind, stationID, rangeName, usageSinceKey(since))
}

func accountUsageCacheKey(stationID uint, rangeName string, since time.Time, accountIDs []int64) string {
	parts := make([]string, 0, len(accountIDs))
	for _, accountID := range normalizedUsageAccountIDs(accountIDs) {
		parts = append(parts, strconv.FormatInt(accountID, 10))
	}
	return usageCacheKey("accounts:"+strings.Join(parts, ","), stationID, rangeName, since)
}

func normalizedUsageAccountIDs(accountIDs []int64) []int64 {
	unique := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID > 0 {
			unique[accountID] = struct{}{}
		}
	}
	result := make([]int64, 0, len(unique))
	for accountID := range unique {
		result = append(result, accountID)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func usageSinceKey(since time.Time) string {
	if since.IsZero() {
		return "all"
	}
	return since.UTC().Format(time.RFC3339Nano)
}

type normalizedServiceURL struct {
	host string
	port string
	path string
}

func normalizeServiceURL(raw string) (normalizedServiceURL, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return normalizedServiceURL{}, false
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return normalizedServiceURL{}, false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	port := parsed.Port()
	if (strings.EqualFold(parsed.Scheme, "https") && port == "443") || (strings.EqualFold(parsed.Scheme, "http") && port == "80") {
		port = ""
	}
	path := strings.Trim(strings.TrimSpace(parsed.EscapedPath()), "/")
	return normalizedServiceURL{host: host, port: port, path: path}, true
}

func channelSiteMatchesAccountBaseURL(siteURL, accountBaseURL string) bool {
	site, siteOK := normalizeServiceURL(siteURL)
	account, accountOK := normalizeServiceURL(accountBaseURL)
	if !siteOK || !accountOK || !serviceHostsMatch(site.host, account.host) || site.port != account.port {
		return false
	}
	if site.path == "" || account.path == "" || site.path == account.path {
		return true
	}
	return strings.HasPrefix(site.path, account.path+"/") || strings.HasPrefix(account.path, site.path+"/")
}

// serviceHostsMatch treats a provider root and a provider-owned subdomain as
// the same service while preserving a label boundary. This covers accounts
// such as st.walkcoding.top belonging to the Walk AI Coding channel at
// walkcoding.top without matching lookalike domains such as walkcoding.top.evil.
func serviceHostsMatch(left, right string) bool {
	left = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(left)), ".")
	right = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(right)), ".")
	if left == "" || right == "" {
		return false
	}
	if left == right || strings.HasSuffix(left, "."+right) || strings.HasSuffix(right, "."+left) {
		return true
	}
	// Some providers expose a public site and an API endpoint on unrelated
	// registrable domains (for example api.mhapi.cn and mhapi.net). As a
	// conservative fallback, accept one unique, non-generic host label shared
	// by both names; this keeps example/api/www labels from creating matches.
	generic := map[string]struct{}{"www": {}, "api": {}, "app": {}, "www2": {}, "example": {}, "localhost": {}, "test": {}, "dev": {}, "staging": {}, "prod": {}}
	leftLabels := strings.Split(left, ".")
	rightLabels := strings.Split(right, ".")
	// Reject lookalikes where one complete host is merely embedded inside a
	// longer host (for example walkcoding.top.evil.test). Such names are not a
	// provider's alternate endpoint, even if they share a distinctive label.
	if hostLabelsEmbedded(leftLabels, rightLabels) || hostLabelsEmbedded(rightLabels, leftLabels) {
		return false
	}
	rightSet := make(map[string]struct{}, len(rightLabels))
	for _, label := range rightLabels {
		rightSet[label] = struct{}{}
	}
	shared := 0
	for _, l := range leftLabels {
		if len(l) < 4 {
			continue
		}
		if _, skip := generic[l]; skip {
			continue
		}
		if _, ok := rightSet[l]; ok {
			shared++
		}
	}
	return shared == 1
}

func hostLabelsEmbedded(shorter, longer []string) bool {
	if len(shorter) < 2 || len(shorter) >= len(longer) {
		return false
	}
	for start := 0; start+len(shorter) <= len(longer); start++ {
		match := true
		for i := range shorter {
			if shorter[i] != longer[start+i] {
				match = false
				break
			}
		}
		if match && start+len(shorter) != len(longer) {
			return true
		}
	}
	return false
}

func (s *Service) RecentUsage(ctx context.Context, stationID uint, limit int) ([]RecentUsageItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return nil, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt admin API key: %w", err)
	}
	var page remoteUsagePage
	endpoint := fmt.Sprintf("%s/api/v1/admin/usage?page=1&page_size=%d", station.BaseURL, limit)
	if err := s.get(ctx, endpoint, apiKey, &page); err != nil {
		return nil, err
	}
	users := make(map[int64]remoteUser)
	for pageNumber := 1; pageNumber <= 100; pageNumber++ {
		var userPage remoteUserPage
		userEndpoint := fmt.Sprintf("%s/api/v1/admin/users?page=%d&page_size=100", station.BaseURL, pageNumber)
		if err := s.get(ctx, userEndpoint, apiKey, &userPage); err != nil {
			break
		}
		for _, user := range userPage.Items {
			users[user.ID] = user
		}
		if len(userPage.Items) == 0 || userPage.Pages <= pageNumber {
			break
		}
	}
	groups, _ := s.stations.ListGroups(stationID)
	groupNames := make(map[int64]string, len(groups))
	for _, group := range groups {
		groupNames[group.ExternalID] = group.Name
	}
	accounts, _ := s.stations.ListAccounts(stationID)
	accountNames := make(map[int64]string, len(accounts))
	for _, account := range accounts {
		accountNames[account.ExternalID] = account.Name
	}
	locations := s.resolveIPLocations(ctx, page.Items)
	items := make([]RecentUsageItem, 0, len(page.Items))
	for _, usage := range page.Items {
		created, err := time.Parse(time.RFC3339Nano, usage.CreatedAt)
		if err != nil {
			continue
		}
		model := strings.TrimSpace(usage.RequestedModel)
		if model == "" {
			model = usage.Model
		}
		user := users[usage.UserID]
		userName := strings.TrimSpace(usage.UserName)
		if userName == "" {
			userName = strings.TrimSpace(user.Username)
		}
		userEmail := strings.TrimSpace(user.Email)
		if userName == "" {
			userName = userEmail
		}
		groupName := strings.TrimSpace(usage.GroupName)
		if groupName == "" {
			groupName = groupNames[usage.GroupID]
		}
		accountName := strings.TrimSpace(usage.AccountName)
		if accountName == "" {
			accountName = accountNames[usage.AccountID]
		}
		ipAddress, localLocation := normalizeUsageIPAddress(usage.IPAddress)
		ipLocation := localLocation
		if ipLocation == "" {
			ipLocation = locations[ipAddress]
		}
		items = append(items, RecentUsageItem{ID: usage.ID, UserID: usage.UserID, UserEmail: userEmail, UserName: userName,
			IPAddress: ipAddress, IPLocation: ipLocation,
			GroupID: usage.GroupID, GroupName: groupName, AccountID: usage.AccountID, AccountName: accountName,
			Model: model, RequestType: usage.RequestType, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheReadTokens: usage.CacheReadTokens, CacheCreationTokens: usage.CacheCreationTokens,
			CacheCreation5mTokens: usage.CacheCreation5mTokens, CacheCreation1hTokens: usage.CacheCreation1hTokens,
			CacheTTLOverridden: usage.CacheTTLOverridden,
			UserCharge:         usage.ActualCost, OriginalCost: usage.TotalCost,
			FirstTokenMS: usage.FirstTokenMS, DurationMS: usage.DurationMS, CreatedAt: created.UTC()})
	}
	return items, nil
}

type ipLocationResponse struct {
	Status      string `json:"status"`
	Query       string `json:"query"`
	CountryCode string `json:"countryCode"`
	RegionName  string `json:"regionName"`
	City        string `json:"city"`
}

func (s *Service) resolveIPLocations(ctx context.Context, usages []remoteUsage) map[string]string {
	result := make(map[string]string)
	missing := make([]string, 0)
	seen := make(map[string]struct{})
	now := time.Now()

	s.ipLocationMu.Lock()
	for _, usage := range usages {
		ipAddress, localLocation := normalizeUsageIPAddress(usage.IPAddress)
		if ipAddress == "" || localLocation != "" {
			continue
		}
		if cached, ok := s.ipLocationCache[ipAddress]; ok && cached.ExpiresAt.After(now) {
			result[ipAddress] = cached.Location
			continue
		}
		if _, ok := seen[ipAddress]; !ok {
			seen[ipAddress] = struct{}{}
			missing = append(missing, ipAddress)
		}
	}
	s.ipLocationMu.Unlock()
	if len(missing) == 0 {
		return result
	}

	payload, err := json.Marshal(missing)
	if err != nil {
		return result
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(lookupCtx, http.MethodPost, ipLocationEndpoint, strings.NewReader(string(payload)))
	if err != nil {
		return result
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		s.cacheIPLocationFailures(missing, now)
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.cacheIPLocationFailures(missing, now)
		return result
	}
	var responses []ipLocationResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&responses); err != nil {
		s.cacheIPLocationFailures(missing, now)
		return result
	}
	resolved := make(map[string]string, len(responses))
	for _, item := range responses {
		ipAddress, localLocation := normalizeUsageIPAddress(item.Query)
		if ipAddress == "" || localLocation != "" || item.Status != "success" {
			continue
		}
		resolved[ipAddress] = formatIPLocation(item.CountryCode, item.RegionName, item.City)
	}

	s.ipLocationMu.Lock()
	for _, ipAddress := range missing {
		location := resolved[ipAddress]
		ttl := ipLocationTTL
		if location == "" {
			ttl = ipLocationErrorTTL
		}
		s.ipLocationCache[ipAddress] = ipLocationCacheEntry{ExpiresAt: now.Add(ttl), Location: location}
		result[ipAddress] = location
	}
	s.ipLocationMu.Unlock()
	return result
}

func (s *Service) cacheIPLocationFailures(ipAddresses []string, now time.Time) {
	s.ipLocationMu.Lock()
	defer s.ipLocationMu.Unlock()
	for _, ipAddress := range ipAddresses {
		s.ipLocationCache[ipAddress] = ipLocationCacheEntry{ExpiresAt: now.Add(ipLocationErrorTTL)}
	}
}

func normalizeUsageIPAddress(value string) (string, string) {
	value = strings.TrimSpace(strings.Split(value, ",")[0])
	if value == "" {
		return "", ""
	}
	address, err := netip.ParseAddr(strings.Trim(value, "[]"))
	if err != nil {
		if host, _, splitErr := net.SplitHostPort(value); splitErr == nil {
			address, err = netip.ParseAddr(strings.Trim(host, "[]"))
		}
	}
	if err != nil {
		return value, "无效地址"
	}
	address = address.Unmap()
	switch {
	case address.IsLoopback():
		return address.String(), "本机地址"
	case address.IsPrivate():
		return address.String(), "内网地址"
	case address.IsLinkLocalUnicast(), address.IsLinkLocalMulticast():
		return address.String(), "链路本地地址"
	case address.IsUnspecified():
		return address.String(), "未指定地址"
	default:
		return address.String(), ""
	}
}

func formatIPLocation(countryCode, regionName, city string) string {
	parts := make([]string, 0, 3)
	for _, value := range []string{countryCode, regionName, city} {
		value = strings.TrimSpace(value)
		if value != "" && (len(parts) == 0 || parts[len(parts)-1] != value) {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " · ")
}

// Users returns a complete, range-aware user view. Sub2API can sort persisted
// user columns server-side, but range usage and live concurrency require a
// merged full-result sort before this service applies pagination.
func (s *Service) Users(ctx context.Context, stationID uint, query UserListQuery) (UserManagementPage, error) {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 || query.PageSize > 100 {
		query.PageSize = 50
	}
	query.Search = strings.TrimSpace(query.Search)
	query.RangeName = strings.TrimSpace(query.RangeName)
	query.RiskLevel = strings.ToLower(strings.TrimSpace(query.RiskLevel))
	query.RegistrationIP = strings.TrimSpace(query.RegistrationIP)
	if query.RangeName == "" {
		query.RangeName = "today"
	}

	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return UserManagementPage{}, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return UserManagementPage{}, fmt.Errorf("decrypt admin API key: %w", err)
	}
	allUsers, err := s.fetchUsers(ctx, station.BaseURL, apiKey, "")
	if err != nil {
		return UserManagementPage{}, err
	}
	totalBalance := 0.0
	for _, user := range allUsers {
		if strings.EqualFold(strings.TrimSpace(user.Role), "admin") {
			continue
		}
		totalBalance += user.Balance
	}
	users := filterUsersBySearch(allUsers, query.Search)
	usage, failedUsers := s.userUsage(ctx, stationID, station.BaseURL, apiKey, users, query.RangeName, query.Since)
	registrationIPs, riskDataComplete := s.registrationIPs(ctx, station.BaseURL, apiKey, allUsers)

	items := make([]UserManagementItem, 0, len(users))
	for _, user := range users {
		userUsage := usage[user.ID]
		items = append(items, UserManagementItem{
			ID: user.ID, Email: user.Email, Username: user.Username, Role: user.Role,
			Balance: user.Balance, Usage: userUsage.UserCharge, UsageTotalTokens: userUsage.TotalTokens, Concurrency: user.Concurrency,
			CurrentConcurrency: user.CurrentConcurrency, RPMLimit: user.RPMLimit, Status: user.Status,
			LastUsedAt: user.LastUsedAt, CreatedAt: user.CreatedAt,
		})
		if info, ok := registrationIPs[user.ID]; ok {
			item := &items[len(items)-1]
			item.RegistrationIP = info.IP
			item.RegistrationIPCount = info.Count
			item.RegistrationBurstCount = info.BurstCount
			item.RiskScore, item.RiskLevel, item.RiskReasons = userRiskScore(user, info, usage[user.ID])
		} else {
			item := &items[len(items)-1]
			item.RiskScore, item.RiskLevel, item.RiskReasons = userRiskScore(user, registrationIPInfo{}, usage[user.ID])
		}
	}
	if query.RegistrationIP != "" {
		filtered := items[:0]
		for _, item := range items {
			if item.RegistrationIP == query.RegistrationIP {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if level := strings.ToLower(strings.TrimSpace(query.RiskLevel)); level != "" && level != "all" {
		filtered := items[:0]
		for _, item := range items {
			if item.RiskLevel == level {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	sortUserManagementItems(items, query.SortBy, query.SortOrder)

	total := len(items)
	pages := (total + query.PageSize - 1) / query.PageSize
	if pages < 1 {
		pages = 1
	}
	if query.Page > pages {
		query.Page = pages
	}
	start := (query.Page - 1) * query.PageSize
	if start > total {
		start = total
	}
	end := start + query.PageSize
	if end > total {
		end = total
	}
	return UserManagementPage{
		Items: items[start:end], Total: total, TotalBalance: totalBalance, Page: query.Page, PageSize: query.PageSize, Pages: pages,
		Range: query.RangeName, Complete: failedUsers == 0, FailedUsers: failedUsers, RiskDataComplete: riskDataComplete,
	}, nil
}

type registrationIPInfo struct {
	IP         string
	Count      int
	BurstCount int
}

// registrationIPs joins successful auth.register audit events to users by the
// same short creation-time window used by sub2api-risk-monitor. The endpoint
// supports action filtering, so this remains small even on busy deployments.
func (s *Service) registrationIPs(ctx context.Context, baseURL, apiKey string, users []remoteUser) (map[int64]registrationIPInfo, bool) {
	logs := make([]remoteAuditLog, 0, 500)
	complete := true
	for page := 1; page <= 100; page++ {
		var result remoteAuditLogPage
		endpoint := fmt.Sprintf("%s/api/v1/admin/audit-logs?action=auth.register&page=%d&page_size=500", strings.TrimRight(baseURL, "/"), page)
		if err := s.get(ctx, endpoint, apiKey, &result); err != nil {
			return map[int64]registrationIPInfo{}, false
		}
		logs = append(logs, result.Items...)
		if len(result.Items) == 0 || (result.Pages > 0 && page >= result.Pages) || (result.Total > 0 && len(logs) >= result.Total) {
			break
		}
		if page == 100 {
			complete = false
		}
	}
	counts := make(map[string]int)
	burst := make(map[string]map[int64]int)
	matched := make(map[int64]string)
	for _, user := range users {
		var best remoteAuditLog
		bestDistance := time.Duration(1<<63 - 1)
		for _, log := range logs {
			if log.StatusCode < 200 || log.StatusCode >= 300 || strings.TrimSpace(log.ClientIP) == "" {
				continue
			}
			delta := log.CreatedAt.Sub(user.CreatedAt)
			if delta < -3*time.Second || delta > 15*time.Second {
				continue
			}
			if distance := absDuration(delta); distance < bestDistance {
				best, bestDistance = log, distance
			}
		}
		if best.ClientIP == "" {
			continue
		}
		matched[user.ID] = best.ClientIP
		counts[best.ClientIP]++
		minute := best.CreatedAt.Truncate(time.Minute).Unix()
		if burst[best.ClientIP] == nil {
			burst[best.ClientIP] = make(map[int64]int)
		}
		burst[best.ClientIP][minute]++
	}
	result := make(map[int64]registrationIPInfo, len(matched))
	for userID, ip := range matched {
		peak := 0
		for _, count := range burst[ip] {
			if count > peak {
				peak = count
			}
		}
		result[userID] = registrationIPInfo{IP: ip, Count: counts[ip], BurstCount: peak}
	}
	return result, complete
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func userRiskScore(user remoteUser, info registrationIPInfo, usage UserUsageStats) (int, string, []string) {
	score := 0
	reasons := make([]string, 0, 3)
	if info.Count >= 2 {
		score += 25
		reasons = append(reasons, fmt.Sprintf("同注册 IP：%d 个账号", info.Count))
	}
	if info.Count >= 5 {
		score += 20
	}
	if info.BurstCount >= 3 {
		score += 25
		reasons = append(reasons, fmt.Sprintf("注册突增：%d 个/分钟", info.BurstCount))
	}
	if suspiciousEmail(user.Email) {
		score += 15
		reasons = append(reasons, "疑似机器注册邮箱")
	}
	if time.Since(user.CreatedAt) <= 24*time.Hour && usage.TotalTokens == 0 {
		score += 5
		reasons = append(reasons, "新账号暂无 API 请求")
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
	return score, level, reasons
}

func suspiciousEmail(email string) bool {
	value := strings.ToLower(strings.TrimSpace(email))
	if !strings.HasSuffix(value, "@gmail.com") {
		return false
	}
	local := strings.TrimSuffix(value, "@gmail.com")
	parts := strings.FieldsFunc(local, func(r rune) bool { return r == '.' || r == '_' })
	if len(parts) != 2 || len(parts[0]) < 2 || len(parts[1]) < 2 {
		return false
	}
	for _, r := range parts[1] {
		if r < '0' || r > '9' {
			continue
		}
		return len(parts[1]) >= 5
	}
	return false
}

func filterUsersBySearch(users []remoteUser, search string) []remoteUser {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return users
	}
	filtered := make([]remoteUser, 0, len(users))
	for _, user := range users {
		if strings.Contains(strings.ToLower(user.Username), search) || strings.Contains(strings.ToLower(user.Email), search) {
			filtered = append(filtered, user)
		}
	}
	return filtered
}

func (s *Service) fetchUsers(ctx context.Context, baseURL, apiKey, search string) ([]remoteUser, error) {
	users := make([]remoteUser, 0, 128)
	for pageNumber := 1; pageNumber <= 1000; pageNumber++ {
		query := url.Values{}
		query.Set("page", strconv.Itoa(pageNumber))
		query.Set("page_size", "500")
		query.Set("sort_by", "id")
		query.Set("sort_order", "asc")
		if search != "" {
			query.Set("search", search)
		}
		var page remoteUserPage
		endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/admin/users?" + query.Encode()
		var fetchErr error
		for attempt := 0; attempt < 2; attempt++ {
			fetchErr = s.get(ctx, endpoint, apiKey, &page)
			if fetchErr == nil {
				break
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt == 0 {
				select {
				case <-time.After(250 * time.Millisecond):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
		if fetchErr != nil {
			return nil, fmt.Errorf("读取中转站用户列表失败: %w", fetchErr)
		}
		users = append(users, page.Items...)
		if len(page.Items) == 0 || (page.Pages > 0 && pageNumber >= page.Pages) || (page.Total > 0 && len(users) >= page.Total) {
			break
		}
	}
	return users, nil
}

func (s *Service) userUsage(ctx context.Context, stationID uint, baseURL, apiKey string, users []remoteUser, rangeName string, since time.Time) (map[int64]UserUsageStats, int) {
	usage := make(map[int64]UserUsageStats, len(users))
	if len(users) == 0 {
		return usage, 0
	}
	hash := sha256.New()
	for _, user := range users {
		_, _ = fmt.Fprintf(hash, "%d,", user.ID)
	}
	cacheKey := fmt.Sprintf("users:%d:%s:%s:%x", stationID, rangeName, usageSinceKey(since), hash.Sum(nil))
	s.usageMu.Lock()
	if cached, ok := s.userUsageCache[cacheKey]; ok && time.Now().Before(cached.ExpiresAt) {
		s.usageMu.Unlock()
		return cached.Usage, cached.FailedUsers
	}
	s.usageMu.Unlock()

	until := time.Now()
	remaining := userUsageCandidates(users, rangeName, since)
	if rangeName != "24h" {
		var ranking remoteUserRanking
		if err := s.get(ctx, userRankingEndpoint(baseURL, since, until), apiKey, &ranking); err == nil {
			for _, item := range ranking.Ranking {
				if _, wanted := remaining[item.UserID]; !wanted {
					continue
				}
				usage[item.UserID] = UserUsageStats{TotalTokens: item.Tokens, UserCharge: item.ActualCost}
				delete(remaining, item.UserID)
			}
			// Sub2API caps rankings at 50 rows. Matching aggregate totals prove
			// that the returned ranking nevertheless covers the complete range.
			if userRankingComplete(ranking) {
				clear(remaining)
			}
		} else {
			// If the ranking endpoint is unavailable, favor correctness over
			// the last-used optimization and verify every requested user.
			remaining = make(map[int64]struct{}, len(users))
			for _, user := range users {
				remaining[user.ID] = struct{}{}
			}
		}
	}
	failedUsers := s.fetchUserUsageStats(ctx, baseURL, apiKey, since, until, remaining, usage)
	if failedUsers < len(users) {
		s.usageMu.Lock()
		s.userUsageCache[cacheKey] = userUsageCacheEntry{ExpiresAt: time.Now().Add(time.Minute), Usage: usage, FailedUsers: failedUsers}
		s.usageMu.Unlock()
	}
	return usage, failedUsers
}

func userUsageCandidates(users []remoteUser, rangeName string, since time.Time) map[int64]struct{} {
	candidates := make(map[int64]struct{})
	for _, user := range users {
		if user.LastUsedAt == nil {
			continue
		}
		if rangeName == "all" || since.IsZero() || !user.LastUsedAt.Before(since) {
			candidates[user.ID] = struct{}{}
		}
	}
	return candidates
}

func userRankingComplete(ranking remoteUserRanking) bool {
	var tokens int64
	var actualCost float64
	for _, item := range ranking.Ranking {
		tokens += item.Tokens
		actualCost += item.ActualCost
	}
	costScale := math.Max(1, math.Max(math.Abs(actualCost), math.Abs(ranking.TotalActualCost)))
	return tokens == ranking.TotalTokens && math.Abs(actualCost-ranking.TotalActualCost) <= costScale*1e-9
}

func (s *Service) fetchUserUsageStats(ctx context.Context, baseURL, apiKey string, since, until time.Time, userIDs map[int64]struct{}, usage map[int64]UserUsageStats) int {
	if len(userIDs) == 0 {
		return 0
	}
	type usageResult struct {
		userID int64
		usage  UserUsageStats
		err    error
	}
	jobs := make(chan int64)
	results := make(chan usageResult)
	workers := 8
	if len(userIDs) < workers {
		workers = len(userIDs)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for userID := range jobs {
				var stats remoteUsageStats
				err := s.get(ctx, userUsageStatsEndpoint(baseURL, userID, since, until), apiKey, &stats)
				select {
				case results <- usageResult{userID: userID, usage: UserUsageStats{TotalTokens: stats.TotalTokens, UserCharge: stats.TotalActualCost}, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(results)
		for userID := range userIDs {
			select {
			case jobs <- userID:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return
			}
		}
		close(jobs)
		wg.Wait()
	}()
	failedUsers := 0
	for result := range results {
		if result.err != nil {
			failedUsers++
			continue
		}
		usage[result.userID] = result.usage
	}
	return failedUsers
}

func sortUserManagementItems(items []UserManagementItem, sortBy, sortOrder string) {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	switch sortBy {
	case "id", "balance", "usage", "risk_score", "registration_ip_count", "current_concurrency", "last_used_at", "created_at":
	default:
		sortBy = "balance"
	}
	descending := !strings.EqualFold(strings.TrimSpace(sortOrder), "asc")
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if sortBy == "last_used_at" {
			if a.LastUsedAt == nil || b.LastUsedAt == nil {
				if a.LastUsedAt == nil && b.LastUsedAt == nil {
					return a.ID < b.ID
				}
				return a.LastUsedAt != nil
			}
			if a.LastUsedAt.Equal(*b.LastUsedAt) {
				return a.ID < b.ID
			}
			if descending {
				return a.LastUsedAt.After(*b.LastUsedAt)
			}
			return a.LastUsedAt.Before(*b.LastUsedAt)
		}
		comparison := 0
		switch sortBy {
		case "id":
			comparison = cmpInt64(a.ID, b.ID)
		case "balance":
			comparison = cmpFloat64(a.Balance, b.Balance)
		case "usage":
			comparison = cmpFloat64(a.Usage, b.Usage)
		case "risk_score":
			comparison = cmpInt64(int64(a.RiskScore), int64(b.RiskScore))
		case "registration_ip_count":
			comparison = cmpInt64(int64(a.RegistrationIPCount), int64(b.RegistrationIPCount))
		case "current_concurrency":
			comparison = cmpInt64(int64(a.CurrentConcurrency), int64(b.CurrentConcurrency))
		case "created_at":
			comparison = cmpTime(a.CreatedAt, b.CreatedAt)
		}
		if comparison == 0 {
			return a.ID < b.ID
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func cmpInt64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpFloat64(a, b float64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpTime(a, b time.Time) int {
	if a.Before(b) {
		return -1
	}
	if a.After(b) {
		return 1
	}
	return 0
}

func (s *Service) UpdateUserStatus(ctx context.Context, stationID uint, userID int64, status string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	if userID <= 0 || (status != "active" && status != "disabled") {
		return errors.New("用户状态参数无效")
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return fmt.Errorf("decrypt admin API key: %w", err)
	}
	endpoint := fmt.Sprintf("%s/api/v1/admin/users/%d", station.BaseURL, userID)
	if err := s.put(ctx, endpoint, apiKey, map[string]any{"status": status}); err != nil {
		return fmt.Errorf("更新中转站用户状态失败: %w", err)
	}
	return nil
}

func (s *Service) UpdateUserLimits(ctx context.Context, stationID uint, input UserBatchLimitsInput) (int, error) {
	if len(input.UserIDs) == 0 || len(input.UserIDs) > 500 {
		return 0, errors.New("user_ids 必须包含 1 到 500 个用户")
	}
	if input.Concurrency == nil && input.RPMLimit == nil {
		return 0, errors.New("至少需要设置并发数或每分钟请求数")
	}
	if input.Concurrency != nil && *input.Concurrency < 0 {
		return 0, errors.New("并发数不能小于 0")
	}
	if input.RPMLimit != nil && *input.RPMLimit < 0 {
		return 0, errors.New("每分钟请求数不能小于 0")
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return 0, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return 0, fmt.Errorf("decrypt admin API key: %w", err)
	}
	body := map[string]any{"user_ids": input.UserIDs, "all": false}
	if input.Concurrency != nil {
		body["concurrency"] = *input.Concurrency
	}
	if input.RPMLimit != nil {
		body["rpm_limit"] = *input.RPMLimit
	}
	var result remoteBatchUpdateResult
	if err := s.post(ctx, station.BaseURL+"/api/v1/admin/users/batch-limits", apiKey, body, &result); err != nil {
		return 0, fmt.Errorf("批量更新中转站用户限额失败: %w", err)
	}
	return result.Affected, nil
}

// DeleteUsers soft-deletes remote users through the Sub2API admin endpoint.
// Admin accounts are never sent to the remote API.
func (s *Service) DeleteUsers(ctx context.Context, stationID uint, userIDs []int64) (UserDeleteResult, error) {
	if len(userIDs) == 0 || len(userIDs) > 300 {
		return UserDeleteResult{}, errors.New("user_ids 必须包含 1 到 300 个用户")
	}
	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return UserDeleteResult{}, errors.New("没有有效的用户 ID")
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return UserDeleteResult{}, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return UserDeleteResult{}, fmt.Errorf("decrypt admin API key: %w", err)
	}
	admins := make(map[int64]struct{})
	for page := 1; page <= 1000; page++ {
		var users remoteUserPage
		endpoint := fmt.Sprintf("%s/api/v1/admin/users?page=%d&page_size=500&sort_by=id&sort_order=asc", strings.TrimRight(station.BaseURL, "/"), page)
		if err := s.get(ctx, endpoint, apiKey, &users); err != nil {
			return UserDeleteResult{}, fmt.Errorf("读取用户角色失败: %w", err)
		}
		for _, user := range users.Items {
			if strings.EqualFold(strings.TrimSpace(user.Role), "admin") {
				admins[user.ID] = struct{}{}
			}
		}
		if len(users.Items) == 0 || (users.Pages > 0 && page >= users.Pages) || (users.Total > 0 && page*500 >= users.Total) {
			break
		}
	}
	result := UserDeleteResult{Failed: make([]int64, 0)}
	for _, id := range unique {
		if _, ok := admins[id]; ok {
			result.SkippedAdmins++
			continue
		}
		if err := s.deleteRemote(ctx, fmt.Sprintf("%s/api/v1/admin/users/%d", strings.TrimRight(station.BaseURL, "/"), id), apiKey); err != nil {
			result.Failed = append(result.Failed, id)
			continue
		}
		result.Affected++
	}
	return result, nil
}

func (s *Service) UserBalanceHistory(ctx context.Context, stationID uint, userID int64, page, pageSize int, kind string) (UserBalanceHistory, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 15
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return UserBalanceHistory{}, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return UserBalanceHistory{}, fmt.Errorf("decrypt admin API key: %w", err)
	}
	var user remoteUser
	if err := s.get(ctx, fmt.Sprintf("%s/api/v1/admin/users/%d", station.BaseURL, userID), apiKey, &user); err != nil {
		return UserBalanceHistory{}, err
	}
	var result UserBalanceHistory
	result.User = user
	endpoint := fmt.Sprintf("%s/api/v1/admin/users/%d/balance-history?page=%d&page_size=%d", station.BaseURL, userID, page, pageSize)
	if strings.TrimSpace(kind) != "" {
		endpoint += "&type=" + url.QueryEscape(strings.TrimSpace(kind))
	}
	if err := s.get(ctx, endpoint, apiKey, &result); err != nil {
		return UserBalanceHistory{}, err
	}
	result.User = user
	return result, nil
}

func (s *Service) TestAccount(ctx context.Context, stationID uint, accountID int64, modelID, mode string) (AccountTestResult, error) {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return AccountTestResult{}, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return AccountTestResult{}, fmt.Errorf("decrypt admin API key: %w", err)
	}
	endpoint := fmt.Sprintf("%s/api/v1/admin/accounts/%d/test", station.BaseURL, accountID)
	var body io.Reader
	if modelID != "" || mode != "" {
		encoded, _ := json.Marshal(map[string]any{"model_id": modelID, "mode": mode})
		body = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return AccountTestResult{}, err
	}
	req.Header.Set("Accept", "text/event-stream, application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("x-api-key", apiKey)
	startedAt := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		return AccountTestResult{}, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if readErr != nil {
		return AccountTestResult{}, readErr
	}
	result := AccountTestResult{StatusCode: resp.StatusCode, DurationMS: time.Since(startedAt).Milliseconds(), Output: string(data)}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return result, nil
}

type remoteGroupAPIKey struct {
	Key     string      `json:"key"`
	Status  string      `json:"status"`
	GroupID *int64      `json:"group_id"`
	User    *remoteUser `json:"user"`
}

type remoteGroupAPIKeyPage struct {
	Items []remoteGroupAPIKey `json:"items"`
	Page  int                 `json:"page"`
	Pages int                 `json:"pages"`
}

func (s *Service) GroupModels(ctx context.Context, stationID uint, groupID int64) ([]string, error) {
	station, gatewayKey, err := s.groupGatewayCredential(ctx, stationID, groupID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, station.BaseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("读取分组模型失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("读取分组模型失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := sanitizeGatewayMessage(gatewayResponseMessage(data), gatewayKey)
		return nil, fmt.Errorf("读取分组模型失败（HTTP %d）: %s", resp.StatusCode, message)
	}
	models := extractAccountModelIDs(json.RawMessage(data))
	if len(models) == 0 {
		return nil, errors.New("该分组没有可用模型")
	}
	return models, nil
}

func (s *Service) TestGroup(ctx context.Context, stationID uint, groupID int64, model string, count int) (GroupTestResult, error) {
	station, gatewayKey, err := s.groupGatewayCredential(ctx, stationID, groupID)
	if err != nil {
		return GroupTestResult{}, err
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return GroupTestResult{}, errors.New("请选择测试模型")
	}
	if count < 1 || count > 10 {
		return GroupTestResult{}, errors.New("调用次数必须是 1 到 10 次")
	}

	result := GroupTestResult{Model: model, Requested: count, Results: make([]GroupTestCallResult, 0, count)}
	for index := 1; index <= count; index++ {
		call := s.testGroupCall(ctx, station.BaseURL, gatewayKey, model, index)
		result.Results = append(result.Results, call)
		if call.Success {
			result.Succeeded++
		} else {
			result.Failed++
		}
		if ctx.Err() != nil {
			break
		}
	}
	s.invalidatePublicMonitor(stationID)
	return result, nil
}

func (s *Service) groupGatewayCredential(ctx context.Context, stationID uint, groupID int64) (*storage.RelayStation, string, error) {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return nil, "", err
	}
	if _, err := s.stations.FindGroupByExternalID(stationID, groupID); err != nil {
		return nil, "", errors.New("中转站分组不存在，请先同步分组")
	}
	adminKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt admin API key: %w", err)
	}
	for page := 1; page <= 1000; page++ {
		var keys remoteGroupAPIKeyPage
		endpoint := fmt.Sprintf("%s/api/v1/admin/groups/%d/api-keys?page=%d&page_size=100", station.BaseURL, groupID, page)
		if err := s.get(ctx, endpoint, adminKey, &keys); err != nil {
			return nil, "", fmt.Errorf("查询分组的管理员 API 密钥失败: %w", err)
		}
		if key := selectAdminGroupAPIKey(keys.Items, groupID); key != "" {
			return station, key, nil
		}
		if len(keys.Items) == 0 || keys.Pages <= page {
			break
		}
	}
	return nil, "", ErrGroupAdminAPIKeyMissing
}

func selectAdminGroupAPIKey(items []remoteGroupAPIKey, groupID int64) string {
	for _, item := range items {
		if strings.TrimSpace(item.Key) == "" || !strings.EqualFold(strings.TrimSpace(item.Status), "active") || item.User == nil || !strings.EqualFold(strings.TrimSpace(item.User.Role), "admin") {
			continue
		}
		if item.GroupID != nil && *item.GroupID != groupID {
			continue
		}
		return strings.TrimSpace(item.Key)
	}
	return ""
}

func (s *Service) testGroupCall(ctx context.Context, baseURL, gatewayKey, model string, index int) GroupTestCallResult {
	payload, err := json.Marshal(map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": "Reply with OK."}},
		"stream":      false,
		"temperature": 0,
	})
	if err != nil {
		return GroupTestCallResult{Index: index, Output: "构造测试请求失败"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/chat/completions", strings.NewReader(string(payload)))
	if err != nil {
		return GroupTestCallResult{Index: index, Output: "构造测试请求失败"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)
	startedAt := time.Now()
	resp, err := s.client.Do(req)
	duration := time.Since(startedAt).Milliseconds()
	if err != nil {
		return GroupTestCallResult{Index: index, DurationMS: duration, Output: sanitizeGatewayMessage(err.Error(), gatewayKey)}
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if readErr != nil {
		return GroupTestCallResult{Index: index, StatusCode: resp.StatusCode, DurationMS: duration, Output: "读取测试响应失败"}
	}
	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	output := gatewayResponseMessage(data)
	if success {
		if content := gatewayAssistantContent(data); content != "" {
			output = content
		}
		if strings.TrimSpace(output) == "" {
			output = "调用成功"
		}
	}
	return GroupTestCallResult{
		Index: index, Success: success, StatusCode: resp.StatusCode, DurationMS: duration,
		Output: sanitizeGatewayMessage(output, gatewayKey),
	}
}

func gatewayAssistantContent(data []byte) string {
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Text string `json:"text"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Choices) == 0 {
		return ""
	}
	if content := strings.TrimSpace(response.Choices[0].Message.Content); content != "" {
		return stripMarkdownCodeFence(content)
	}
	return stripMarkdownCodeFence(response.Choices[0].Text)
}

func stripMarkdownCodeFence(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 6 || !strings.HasPrefix(value, "```") || !strings.HasSuffix(value, "```") {
		return value
	}
	inner := value[3 : len(value)-3]
	if strings.HasPrefix(inner, "\r\n") {
		inner = inner[2:]
	} else if strings.HasPrefix(inner, "\n") {
		inner = inner[1:]
	} else if newline := strings.IndexByte(inner, '\n'); newline >= 0 {
		// A first line directly after the opening fence is the optional language tag.
		inner = inner[newline+1:]
	}
	return strings.TrimSpace(inner)
}

func gatewayResponseMessage(data []byte) string {
	var response struct {
		Message string `json:"message"`
		Error   any    `json:"error"`
	}
	if json.Unmarshal(data, &response) == nil {
		if message := strings.TrimSpace(response.Message); message != "" {
			return message
		}
		switch value := response.Error.(type) {
		case string:
			if message := strings.TrimSpace(value); message != "" {
				return message
			}
		case map[string]any:
			if message, ok := value["message"].(string); ok && strings.TrimSpace(message) != "" {
				return strings.TrimSpace(message)
			}
		}
	}
	message := strings.TrimSpace(string(data))
	if len(message) > 2000 {
		message = message[:2000] + "..."
	}
	if message == "" {
		return "无响应内容"
	}
	return message
}

func sanitizeGatewayMessage(message, credential string) string {
	message = strings.TrimSpace(message)
	if credential != "" {
		message = strings.ReplaceAll(message, credential, "[已隐藏]")
	}
	if len(message) > 2000 {
		message = message[:2000] + "..."
	}
	return message
}

// UsageTotal reads the aggregate usage directly from Sub2API. The dashboard
// only needs this total, so it should not fan out one request per account.
func (s *Service) UsageTotal(ctx context.Context, stationID uint, rangeName string, since time.Time) (UsageSummary, error) {
	cacheKey := usageCacheKey("total", stationID, rangeName, since)
	s.usageMu.Lock()
	if cached, ok := s.usageCache[cacheKey]; ok && time.Now().Before(cached.ExpiresAt) {
		s.usageMu.Unlock()
		return cached.Summary, nil
	}
	s.usageMu.Unlock()

	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return UsageSummary{}, err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return UsageSummary{}, fmt.Errorf("decrypt admin API key: %w", err)
	}
	until := time.Now()
	var remote remoteUsageStats
	if err := s.get(ctx, usageTotalEndpoint(station.BaseURL, since, until), apiKey, &remote); err != nil {
		return UsageSummary{}, err
	}
	result := UsageSummary{TotalTokens: remote.TotalTokens, UserCharge: remote.TotalActualCost}
	s.usageMu.Lock()
	s.usageCache[cacheKey] = usageCacheEntry{ExpiresAt: time.Now().Add(time.Minute), Summary: result}
	s.usageMu.Unlock()
	return result, nil
}

func usageStatsEndpoint(baseURL string, accountID int64, since, until time.Time) string {
	return usageStatsURL(baseURL, &accountID, since, until)
}

func userUsageStatsEndpoint(baseURL string, userID int64, since, until time.Time) string {
	endpoint, err := url.Parse(usageStatsURL(baseURL, nil, since, until))
	if err != nil {
		return usageStatsURL(baseURL, nil, since, until)
	}
	query := endpoint.Query()
	query.Set("user_id", strconv.FormatInt(userID, 10))
	endpoint.RawQuery = query.Encode()
	return endpoint.String()
}

func userRankingEndpoint(baseURL string, since, until time.Time) string {
	if since.IsZero() {
		since = time.Unix(0, 0).UTC()
	}
	query := url.Values{}
	query.Set("start_date", since.In(time.Local).Format("2006-01-02"))
	query.Set("end_date", until.In(time.Local).Format("2006-01-02"))
	// Sub2API currently caps this endpoint at 50 rows even when a larger
	// limit is requested. Completeness is checked against its aggregate totals.
	query.Set("limit", "50")
	return strings.TrimRight(baseURL, "/") + "/api/v1/admin/dashboard/users-ranking?" + query.Encode()
}

func usageTotalEndpoint(baseURL string, since, until time.Time) string {
	return usageStatsURL(baseURL, nil, since, until)
}

func usageStatsURL(baseURL string, accountID *int64, since, until time.Time) string {
	query := url.Values{}
	// Sub2API applies its own default range when start_time is omitted. An
	// explicit epoch start is therefore required for a true "all" query.
	if since.IsZero() {
		since = time.Unix(0, 0).UTC()
	}
	query.Set("start_date", since.In(time.Local).Format("2006-01-02"))
	query.Set("start_time", since.Format(time.RFC3339))
	if !until.IsZero() {
		query.Set("end_date", until.In(time.Local).Format("2006-01-02"))
		query.Set("end_time", until.Format(time.RFC3339))
	}
	if accountID != nil {
		query.Set("account_id", strconv.FormatInt(*accountID, 10))
	}
	return strings.TrimRight(baseURL, "/") + "/api/v1/admin/usage/stats?" + query.Encode()
}

// Sync keeps the manual action comprehensive: it probes current rates and then
// replaces the complete relay snapshot.
func (s *Service) Sync(ctx context.Context, stationID uint) error {
	return s.syncSnapshot(ctx, stationID, true)
}

// ReconcileManualChannelLinks persists the safe subset of URL matches for
// manually managed channels. A matching account must have a successfully
// probed multiplier, and an existing user-created binding always wins.
func (s *Service) ReconcileManualChannelLinks() error {
	if s.channels == nil {
		return nil
	}
	channels, err := s.channels.List()
	if err != nil {
		return err
	}
	manualChannels := make([]storage.Channel, 0, len(channels))
	for _, channel := range channels {
		if channel.BalanceMode == storage.BalanceModeManual {
			manualChannels = append(manualChannels, channel)
		}
	}
	stations, err := s.stations.List()
	if err != nil {
		return err
	}
	bindings := make([]storage.AutomaticChannelAccountBinding, 0)
	for _, station := range stations {
		accounts, err := s.stations.ListAccounts(station.ID)
		if err != nil {
			return err
		}
		overrides, err := s.stations.ListCostOverrides(station.ID)
		if err != nil {
			return err
		}
		bindings = append(bindings, automaticManualChannelBindings(manualChannels, station, accounts, overrides)...)
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].RelayStationID != bindings[j].RelayStationID {
			return bindings[i].RelayStationID < bindings[j].RelayStationID
		}
		return bindings[i].RelayAccountExternalID < bindings[j].RelayAccountExternalID
	})
	return s.stations.ReconcileAutomaticChannelAccountBindings(bindings)
}

func automaticManualChannelBindings(channels []storage.Channel, station storage.RelayStation, accounts []storage.RelayAccount, overrides []storage.RelayAccountCostOverride) []storage.AutomaticChannelAccountBinding {
	manualOverrideByAccount := make(map[int64]struct{}, len(overrides))
	for _, override := range overrides {
		if override.Mode != "auto_link" {
			manualOverrideByAccount[override.RelayAccountExternalID] = struct{}{}
		}
	}
	bindings := make([]storage.AutomaticChannelAccountBinding, 0)
	for _, account := range accounts {
		if _, manuallyBound := manualOverrideByAccount[account.ExternalID]; manuallyBound {
			continue
		}
		if account.RateMultiplier == nil || account.RateSource != "upstream_probe" || *account.RateMultiplier < 0 {
			continue
		}
		channelID, matches := uniqueChannelForAccountBaseURL(channels, account.BaseURL)
		if !matches {
			continue
		}
		bindings = append(bindings, storage.AutomaticChannelAccountBinding{
			ChannelID: channelID, RelayStationID: station.ID, RelayStationName: station.Name,
			RelayAccountExternalID: account.ExternalID, RelayAccountName: account.Name,
			RateMultiplier: *account.RateMultiplier, RateObservedAt: account.RateObservedAt,
		})
	}
	return bindings
}

// SyncSnapshot refreshes groups, channels, accounts, assignments, and latency
// samples without starting a billing probe.
func (s *Service) SyncSnapshot(ctx context.Context, stationID uint) error {
	return s.syncSnapshot(ctx, stationID, false)
}

func (s *Service) syncSnapshot(ctx context.Context, stationID uint, probeRates bool) error {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return fmt.Errorf("decrypt admin API key: %w", err)
	}
	groups, err := s.fetchGroups(ctx, station.BaseURL, apiKey)
	if err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return err
	}
	channels, err := s.fetchChannels(ctx, station.BaseURL, apiKey)
	if err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return err
	}
	accounts, err := s.fetchAccounts(ctx, station.BaseURL, apiKey)
	if err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return err
	}
	if probeRates {
		if err := s.probeAPIKeyAccounts(ctx, station.BaseURL, apiKey, accounts); err != nil {
			_ = s.stations.SetSyncError(station.ID, err.Error())
			return err
		}
		// 探测会异步写回账号 extra/rate snapshot，必须重新读取账号列表。
		if len(accounts) > 0 {
			accounts, err = s.fetchAccounts(ctx, station.BaseURL, apiKey)
			if err != nil {
				_ = s.stations.SetSyncError(station.ID, err.Error())
				return err
			}
		}
	}
	previousAccounts, err := s.stations.ListAccounts(station.ID)
	if err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return fmt.Errorf("读取上一次账号成本快照失败: %w", err)
	}
	previousAccountByExternalID := make(map[int64]storage.RelayAccount, len(previousAccounts))
	previousLatency := make(map[int64]string, len(previousAccounts))
	for _, account := range previousAccounts {
		previousAccountByExternalID[account.ExternalID] = account
		if account.LatencySamplesJSON != "" {
			previousLatency[account.ExternalID] = account.LatencySamplesJSON
		}
	}
	latencySnapshots := s.fetchAccountLatencies(ctx, station.BaseURL, apiKey, accounts, groups)

	now := time.Now().UTC()
	localGroups := make([]storage.RelayGroup, 0, len(groups))
	for _, group := range groups {
		localGroups = append(localGroups, storage.RelayGroup{
			RelayStationID: station.ID, ExternalID: group.ID, Name: group.Name,
			Description: group.Description, Platform: group.Platform, Status: group.Status,
			IsExclusive:          group.IsExclusive,
			RequireOAuthOnly:     group.RequireOAuthOnly,
			SortOrder:            group.SortOrder,
			RateMultiplier:       group.RateMultiplier,
			AllowImageGeneration: group.AllowImageGeneration,
			ImageRateIndependent: group.ImageRateIndependent,
			ImageRateMultiplier:  group.ImageRateMultiplier,
			ImagePrice1K:         group.ImagePrice1K,
			ImagePrice2K:         group.ImagePrice2K,
			ImagePrice4K:         group.ImagePrice4K,
			SyncedAt:             now,
		})
	}

	localChannels := make([]storage.RelayChannel, 0, len(channels))
	for _, channel := range channels {
		pricingJSON, pricingHash, pricingModels, modelCount, ruleCount, err := channelPricing(channel)
		if err != nil {
			_ = s.stations.SetSyncError(station.ID, err.Error())
			return fmt.Errorf("整理中转站渠道 %d 定价失败: %w", channel.ID, err)
		}
		localChannels = append(localChannels, storage.RelayChannel{
			RelayStationID: station.ID, ExternalID: channel.ID, Name: channel.Name,
			Description: channel.Description, Status: channel.Status,
			BillingModelSource: channel.BillingModelSource, ApplyPricingToAccountStats: channel.ApplyPricingToAccountStats,
			PricingJSON: pricingJSON, PricingHash: pricingHash, PricingModelsJSON: pricingModels,
			PricingModelCount: modelCount, PricingRuleCount: ruleCount, SyncedAt: now,
		})
	}
	links := make([]storage.RelaySnapshotLink, 0)
	for _, channel := range channels {
		for _, groupID := range channel.GroupIDs {
			links = append(links, storage.RelaySnapshotLink{ChannelExternalID: channel.ID, GroupExternalID: groupID})
		}
	}
	localAccounts := make([]storage.RelayAccount, 0, len(accounts))
	accountLinks := make([]storage.RelayAccountSnapshotLink, 0)
	for _, account := range accounts {
		cost, observed := upstreamProbeCost(account.Extra)
		cost, observed = retainLastProbeCost(cost, observed, previousAccountByExternalID[account.ID])
		latencyJSON := latencySnapshots[account.ID]
		if latencyJSON == "" {
			latencyJSON = previousLatency[account.ID]
		}
		localAccounts = append(localAccounts, storage.RelayAccount{
			RelayStationID: station.ID, ExternalID: account.ID, Name: account.Name,
			BaseURL:  account.Credentials.BaseURL,
			Platform: account.Platform, Type: account.Type, Status: account.Status, Schedulable: account.Schedulable,
			Concurrency: account.Concurrency, CurrentConcurrency: account.CurrentConcurrency, Priority: account.Priority,
			PoolMode: account.Credentials.PoolMode, PoolModeRetryCount: accountPoolModeRetryCount(account),
			AccountPlan: remoteAccountPlan(account), LastUsedAt: account.LastUsedAt,
			RateMultiplier: cost, RateSource: func() string {
				if cost != nil {
					return "upstream_probe"
				}
				return ""
			}(), RateObservedAt: observed, LatencySamplesJSON: latencyJSON, SyncedAt: now,
		})
		for _, groupID := range account.GroupIDs {
			accountLinks = append(accountLinks, storage.RelayAccountSnapshotLink{AccountExternalID: account.ID, GroupExternalID: groupID})
		}
	}
	if err := s.stations.ReplaceSnapshot(station.ID, localGroups, localChannels, links, localAccounts, accountLinks, now); err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return err
	}
	s.invalidatePublicModelPricing(station.ID)
	if err := s.ReconcileManualChannelLinks(); err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return fmt.Errorf("同步手动渠道账号关联失败: %w", err)
	}
	if station.AutoAdjustEnabled || station.AutoAdjustNoProfitEnabled {
		if err := s.applyAutomaticAdjustments(ctx, station); err != nil {
			_ = s.stations.SetSyncError(station.ID, err.Error())
			return err
		}
	}
	if station.AutoPriorityEnabled {
		if err := s.applyAutomaticPriorities(ctx, station); err != nil {
			_ = s.stations.SetSyncError(station.ID, err.Error())
			return err
		}
	}
	return nil
}

// SyncRates probes billing declarations and updates only the local account
// multiplier fields. Snapshot membership and latency data remain untouched.
func (s *Service) SyncRates(ctx context.Context, stationID uint) error {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return fmt.Errorf("decrypt admin API key: %w", err)
	}
	accounts, err := s.fetchAccounts(ctx, station.BaseURL, apiKey)
	if err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return err
	}
	if err := s.probeAPIKeyAccounts(ctx, station.BaseURL, apiKey, accounts); err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return err
	}
	if len(accounts) > 0 {
		accounts, err = s.fetchAccounts(ctx, station.BaseURL, apiKey)
		if err != nil {
			_ = s.stations.SetSyncError(station.ID, err.Error())
			return err
		}
	}
	now := time.Now().UTC()
	previousAccounts, err := s.stations.ListAccounts(station.ID)
	if err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return fmt.Errorf("读取上一次账号成本快照失败: %w", err)
	}
	previousAccountByExternalID := make(map[int64]storage.RelayAccount, len(previousAccounts))
	for _, account := range previousAccounts {
		previousAccountByExternalID[account.ExternalID] = account
	}
	updates := make([]storage.RelayAccountRateUpdate, 0, len(accounts))
	for _, account := range accounts {
		cost, observed := upstreamProbeCost(account.Extra)
		cost, observed = retainLastProbeCost(cost, observed, previousAccountByExternalID[account.ID])
		source := ""
		if cost != nil {
			source = "upstream_probe"
		}
		updates = append(updates, storage.RelayAccountRateUpdate{
			ExternalID: account.ID, RateMultiplier: cost,
			RateSource: source, RateObservedAt: observed,
		})
	}
	if err := s.stations.UpdateAccountRates(station.ID, updates, now); err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return err
	}
	if err := s.ReconcileManualChannelLinks(); err != nil {
		_ = s.stations.SetSyncError(station.ID, err.Error())
		return fmt.Errorf("同步手动渠道账号关联失败: %w", err)
	}
	if station.AutoAdjustEnabled || station.AutoAdjustNoProfitEnabled {
		if err := s.applyAutomaticAdjustments(ctx, station); err != nil {
			_ = s.stations.SetSyncError(station.ID, err.Error())
			return err
		}
	}
	return s.stations.SetSyncError(station.ID, "")
}

func channelPricing(channel remoteChannel) (string, string, string, int, int, error) {
	snapshot := channelPricingSnapshot{
		BillingModelSource: channel.BillingModelSource, ModelPricing: channel.ModelPricing,
		RestrictModels: channel.RestrictModels, ModelMapping: channel.ModelMapping,
		ApplyPricingToAccountStats: channel.ApplyPricingToAccountStats,
		AccountStatsPricingRules:   channel.AccountStatsPricingRules,
	}
	normalizePricing(&snapshot)
	body, err := json.Marshal(snapshot)
	if err != nil {
		return "", "", "", 0, 0, err
	}
	models := make(map[string]struct{})
	collect := func(pricing []remoteModelPricing) {
		for _, item := range pricing {
			for _, model := range item.Models {
				if name := strings.TrimSpace(model); name != "" {
					models[name] = struct{}{}
				}
			}
		}
	}
	collect(snapshot.ModelPricing)
	for _, rule := range snapshot.AccountStatsPricingRules {
		collect(rule.Pricing)
	}
	modelNames := make([]string, 0, len(models))
	for model := range models {
		modelNames = append(modelNames, model)
	}
	sort.Strings(modelNames)
	modelJSON, err := json.Marshal(modelNames)
	if err != nil {
		return "", "", "", 0, 0, err
	}
	sum := sha256.Sum256(body)
	return string(body), fmt.Sprintf("%x", sum), string(modelJSON), len(modelNames), len(snapshot.AccountStatsPricingRules), nil
}

func normalizePricing(snapshot *channelPricingSnapshot) {
	normalizeItems := func(items []remoteModelPricing) {
		for i := range items {
			sort.Strings(items[i].Models)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].ID != items[j].ID {
				return items[i].ID < items[j].ID
			}
			return strings.Join(items[i].Models, "\x00") < strings.Join(items[j].Models, "\x00")
		})
	}
	normalizeItems(snapshot.ModelPricing)
	for i := range snapshot.AccountStatsPricingRules {
		sort.Slice(snapshot.AccountStatsPricingRules[i].GroupIDs, func(a, b int) bool {
			return snapshot.AccountStatsPricingRules[i].GroupIDs[a] < snapshot.AccountStatsPricingRules[i].GroupIDs[b]
		})
		sort.Slice(snapshot.AccountStatsPricingRules[i].AccountIDs, func(a, b int) bool {
			return snapshot.AccountStatsPricingRules[i].AccountIDs[a] < snapshot.AccountStatsPricingRules[i].AccountIDs[b]
		})
		normalizeItems(snapshot.AccountStatsPricingRules[i].Pricing)
	}
	sort.Slice(snapshot.AccountStatsPricingRules, func(i, j int) bool {
		return snapshot.AccountStatsPricingRules[i].ID < snapshot.AccountStatsPricingRules[j].ID
	})
}

// SyncAll 同步所有已配置的中转站，单站失败不影响后续站点。
func (s *Service) SyncAll(ctx context.Context) (int, int, error) {
	return s.syncAll(ctx, s.Sync)
}

func (s *Service) SyncAllRates(ctx context.Context) (int, int, error) {
	return s.syncAll(ctx, s.SyncRates)
}

func (s *Service) SyncAllSnapshots(ctx context.Context) (int, int, error) {
	return s.syncAll(ctx, s.SyncSnapshot)
}

func (s *Service) syncAll(ctx context.Context, syncStation func(context.Context, uint) error) (int, int, error) {
	stations, err := s.stations.List()
	if err != nil {
		return 0, 0, err
	}
	synced, failed := 0, 0
	for _, station := range stations {
		if err := syncStation(ctx, station.ID); err != nil {
			failed++
			continue
		}
		synced++
	}
	return synced, failed, nil
}

// ProbeAccount refreshes one API Key account's declared upstream cost without
// running the station-wide probe. Account groups and latency history are left
// untouched because the operation only targets the billing declaration.
func (s *Service) ProbeAccount(ctx context.Context, stationID uint, accountExternalID int64) error {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return errors.New("中转站不存在")
	}
	account, err := s.stations.FindAccountByExternalID(stationID, accountExternalID)
	if err != nil {
		return errors.New("中转站账号不存在或快照已过期")
	}
	if !strings.EqualFold(strings.TrimSpace(account.Type), "apikey") {
		return errors.New("只有 API Key 类型账号支持上游倍率探测")
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return fmt.Errorf("decrypt admin API key: %w", err)
	}
	if err := s.probeAPIKeyAccounts(ctx, station.BaseURL, apiKey, []remoteAccount{{
		ID: accountExternalID, Type: "apikey",
	}}); err != nil {
		return err
	}
	accounts, err := s.fetchAccounts(ctx, station.BaseURL, apiKey)
	if err != nil {
		return err
	}
	for _, remote := range accounts {
		if remote.ID != accountExternalID {
			continue
		}
		cost, observedAt := upstreamProbeCost(remote.Extra)
		if cost == nil {
			return errors.New("本次未探测到有效成本，已保留上一次成功结果")
		}
		source := ""
		if cost != nil {
			source = "upstream_probe"
		}
		if err := s.stations.UpdateAccountProbe(stationID, accountExternalID, storage.RelayAccount{
			Name: remote.Name, BaseURL: remote.Credentials.BaseURL,
			Platform: remote.Platform, Type: remote.Type, Status: remote.Status,
			Schedulable: remote.Schedulable, Concurrency: remote.Concurrency, Priority: remote.Priority,
			RateMultiplier: cost, RateSource: source,
			RateObservedAt: observedAt, SyncedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		return s.ReconcileManualChannelLinks()
	}
	return errors.New("远端账号不存在或已被删除")
}

func (s *Service) fetchGroups(ctx context.Context, baseURL, apiKey string) ([]remoteGroup, error) {
	var groups []remoteGroup
	if err := s.getRetry(ctx, baseURL+"/api/v1/admin/groups/all?include_inactive=true", apiKey, &groups); err != nil {
		return nil, fmt.Errorf("读取中转站分组失败: %w", err)
	}
	return groups, nil
}

func (s *Service) fetchChannels(ctx context.Context, baseURL, apiKey string) ([]remoteChannel, error) {
	channels := make([]remoteChannel, 0)
	for page := 1; ; page++ {
		var data remotePage
		endpoint := fmt.Sprintf("%s/api/v1/admin/channels?page=%d&page_size=100", baseURL, page)
		if err := s.getRetry(ctx, endpoint, apiKey, &data); err != nil {
			return nil, fmt.Errorf("读取中转站渠道失败: %w", err)
		}
		channels = append(channels, data.Items...)
		if data.Pages <= page || len(data.Items) == 0 {
			break
		}
		if page >= 1000 {
			return nil, errors.New("中转站渠道分页异常")
		}
	}
	return channels, nil
}

func (s *Service) fetchAccounts(ctx context.Context, baseURL, apiKey string) ([]remoteAccount, error) {
	accounts := make([]remoteAccount, 0)
	for page := 1; ; page++ {
		var data remoteAccountPage
		endpoint := fmt.Sprintf("%s/api/v1/admin/accounts?page=%d&page_size=100", baseURL, page)
		if err := s.getRetry(ctx, endpoint, apiKey, &data); err != nil {
			return nil, fmt.Errorf("读取中转站账号失败: %w", err)
		}
		accounts = append(accounts, data.Items...)
		if data.Pages <= page || len(data.Items) == 0 {
			break
		}
		if page >= 1000 {
			return nil, errors.New("中转站账号分页异常")
		}
	}
	return accounts, nil
}

func (s *Service) fetchAccount(ctx context.Context, baseURL, apiKey string, accountID int64) (*remoteAccount, error) {
	for page := 1; ; page++ {
		var data remoteAccountPage
		endpoint := fmt.Sprintf("%s/api/v1/admin/accounts?page=%d&page_size=100", baseURL, page)
		if err := s.getRetry(ctx, endpoint, apiKey, &data); err != nil {
			return nil, fmt.Errorf("读取中转站账号失败: %w", err)
		}
		for i := range data.Items {
			if data.Items[i].ID == accountID {
				return &data.Items[i], nil
			}
		}
		if data.Pages <= page || len(data.Items) == 0 {
			break
		}
		if page >= 1000 {
			return nil, errors.New("中转站账号分页异常")
		}
	}
	return nil, errors.New("远端账号不存在或已被删除")
}

func (s *Service) probeAPIKeyAccounts(ctx context.Context, baseURL, apiKey string, accounts []remoteAccount) error {
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if strings.EqualFold(strings.TrimSpace(account.Type), "apikey") && account.ID > 0 {
			ids = append(ids, account.ID)
		}
	}
	for start := 0; start < len(ids); start += 20 {
		end := start + 20
		if end > len(ids) {
			end = len(ids)
		}
		var result struct {
			Results []json.RawMessage `json:"results"`
		}
		endpoint := baseURL + "/api/v1/admin/accounts/upstream-billing-probe/batch"
		if err := s.post(ctx, endpoint, apiKey, map[string]any{"account_ids": ids[start:end]}, &result); err != nil {
			return fmt.Errorf("实时探测中转站账号倍率失败: %w", err)
		}
	}
	return nil
}

// fetchAccountLatencies reads a bounded usage window for each API Key account.
// Usage is optional telemetry: one account failing or an older Sub2API build
// lacking the endpoint must not make an otherwise valid station sync fail.
func (s *Service) fetchAccountLatencies(ctx context.Context, baseURL, apiKey string, accounts []remoteAccount, groups []remoteGroup) map[int64]string {
	cacheKey := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	now := time.Now()
	s.latencyMu.Lock()
	if cached, ok := s.latencyCache[cacheKey]; ok && cached.ExpiresAt.After(now) {
		result := make(map[int64]string, len(cached.Samples))
		for accountID, samples := range cached.Samples {
			result[accountID] = samples
		}
		s.latencyMu.Unlock()
		return result
	}
	s.latencyMu.Unlock()

	userEmails := make(map[int64]string)
	if users, err := s.fetchUsers(ctx, baseURL, apiKey, ""); err == nil {
		for _, user := range users {
			userEmails[user.ID] = strings.TrimSpace(user.Email)
		}
	}
	groupByID := make(map[int64]remoteGroup, len(groups))
	for _, group := range groups {
		groupByID[group.ID] = group
	}
	type job struct {
		id       int64
		platform string
	}
	jobs := make(chan job)
	results := make(chan struct {
		id   int64
		json string
	})
	workers := 8
	if len(accounts) < workers {
		workers = len(accounts)
	}
	if workers == 0 {
		return map[int64]string{}
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				var page remoteUsagePage
				endpoint := fmt.Sprintf("%s/api/v1/admin/usage?page=1&page_size=%d&account_id=%d", baseURL, latencySampleLimit, item.id)
				if err := s.get(ctx, endpoint, apiKey, &page); err != nil {
					continue
				}
				samples := make([]storage.RelayLatencySample, 0, len(page.Items))
				for _, usage := range page.Items {
					created, err := time.Parse(time.RFC3339Nano, usage.CreatedAt)
					if err != nil {
						continue
					}
					userEmail := strings.TrimSpace(usage.UserEmail)
					if userEmail == "" {
						userEmail = userEmails[usage.UserID]
					}
					groupName := strings.TrimSpace(usage.GroupName)
					var groupMultiplier *float64
					if group, ok := groupByID[usage.GroupID]; ok {
						if groupName == "" {
							groupName = strings.TrimSpace(group.Name)
						}
						value := group.RateMultiplier
						groupMultiplier = &value
					}
					samples = append(samples, storage.RelayLatencySample{
						FirstTokenMS: usage.FirstTokenMS, DurationMS: usage.DurationMS,
						CreatedAt: created.UTC(), Model: usage.Model, Platform: item.platform, RequestType: usage.RequestType,
						UserEmail:   userEmail,
						InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
						CacheReadTokens: usage.CacheReadTokens, CacheCreationTokens: usage.CacheCreationTokens,
						CacheCreation5mTokens: usage.CacheCreation5mTokens, CacheCreation1hTokens: usage.CacheCreation1hTokens,
						GroupID: usage.GroupID, GroupName: groupName, GroupMultiplier: groupMultiplier,
					})
				}
				if len(samples) == 0 {
					continue
				}
				encoded, err := json.Marshal(samples)
				if err == nil {
					results <- struct {
						id   int64
						json string
					}{id: item.id, json: string(encoded)}
				}
			}
		}()
	}
	go func() {
		for _, account := range accounts {
			if strings.EqualFold(strings.TrimSpace(account.Type), "apikey") &&
				strings.EqualFold(strings.TrimSpace(account.Status), "active") && account.Schedulable && account.ID > 0 {
				select {
				case jobs <- job{id: account.ID, platform: account.Platform}:
				case <-ctx.Done():
					close(jobs)
					wg.Wait()
					close(results)
					return
				}
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	snapshots := make(map[int64]string)
	for result := range results {
		snapshots[result.id] = result.json
	}
	s.latencyMu.Lock()
	s.latencyCache[cacheKey] = latencyCacheEntry{ExpiresAt: time.Now().Add(latencyCacheTTL), Samples: snapshots}
	s.latencyMu.Unlock()
	return snapshots
}

func (s *Service) get(ctx context.Context, endpoint, apiKey string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", apiKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var envelope remoteEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		return errors.New(envelope.Message)
	}
	return json.Unmarshal(envelope.Data, out)
}

// getRetry smooths over a transient Cloudflare/upstream stall. The caller's
// context remains the hard upper bound, so this cannot extend a cancelled
// request indefinitely.
func (s *Service) getRetry(ctx context.Context, endpoint, apiKey string, out any) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = s.get(ctx, endpoint, apiKey, out)
		if err == nil || ctx.Err() != nil || attempt == 1 {
			break
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (s *Service) put(ctx context.Context, endpoint, apiKey string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var envelope remoteEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		return errors.New(envelope.Message)
	}
	return nil
}

func (s *Service) deleteRemote(ctx context.Context, endpoint, apiKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", apiKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var envelope remoteEnvelope
	if len(strings.TrimSpace(string(payload))) > 0 {
		_ = json.Unmarshal(payload, &envelope)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			var body struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(payload, &body) == nil {
				message = strings.TrimSpace(body.Error)
			}
		}
		if message == "" {
			message = strings.TrimSpace(string(payload))
		}
		if message == "" {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, message)
	}
	if envelope.Code != 0 {
		if strings.TrimSpace(envelope.Message) == "" {
			return fmt.Errorf("远端返回错误码 %d", envelope.Code)
		}
		return errors.New(strings.TrimSpace(envelope.Message))
	}
	return nil
}

func (s *Service) post(ctx context.Context, endpoint, apiKey string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var envelope remoteEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		return errors.New(envelope.Message)
	}
	if out == nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

func (s *Service) duplicateRemote(ctx context.Context, baseURL, apiKey string, accountID int64, idempotencyKey string) (*remoteAccount, error) {
	endpoint := fmt.Sprintf("%s/api/v1/admin/accounts/%d/duplicate", strings.TrimRight(baseURL, "/"), accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var envelope remoteEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, errors.New(envelope.Message)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, errors.New("远端克隆账号响应缺少账号数据")
	}
	var account remoteAccount
	if err := json.Unmarshal(envelope.Data, &account); err != nil {
		return nil, err
	}
	if account.ID <= 0 {
		return nil, errors.New("远端克隆账号响应缺少有效账号 ID")
	}
	return &account, nil
}

func normalizeBaseURL(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("中转站地址必须是完整的 http(s) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("中转站地址仅支持 http 或 https")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("中转站地址不能包含查询参数或片段")
	}
	return value, nil
}

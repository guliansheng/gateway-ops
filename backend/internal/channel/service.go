// Package channel 提供渠道领域服务：把存储层的加密字段解开成 connector.Channel，
// 处理登录会话的复用与刷新、手动测试登录、手动刷新余额 / 倍率等。
package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guliansheng/gateway-ops/internal/captcha"
	"github.com/guliansheng/gateway-ops/internal/connector"
	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/progress"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"gorm.io/gorm"
)

// SessionRefreshThreshold 距离过期还有多久就提前刷新登录。
const SessionRefreshThreshold = 5 * time.Minute

// tokenSessionTTL token 模式下"假装"给 AuthSession 的有效期。
// token 由用户提供，我们没法续期，这里设一年只是为了避免 SessionRefreshThreshold 把它判过期。
// 真正失效检测靠 connector.CheckAuth + 上游 401/403。
const tokenSessionTTL = 365 * 24 * time.Hour

// Service 渠道领域服务。
type Service struct {
	Channels     *storage.Channels
	AuthSessions *storage.AuthSessions
	Captchas     *storage.Captchas
	MonitorLogs  *storage.MonitorLogs
	Cipher       *crypto.Cipher
}

// ErrNameConflict indicates that another channel already owns the requested
// display name. Channel names remain unique because the UI and notifications
// use them as the human-readable identifier for an upstream.
var ErrNameConflict = errors.New("渠道名称已被其他渠道占用")

func NewService(
	channels *storage.Channels,
	authSessions *storage.AuthSessions,
	captchas *storage.Captchas,
	monitorLogs *storage.MonitorLogs,
	cipher *crypto.Cipher,
) *Service {
	return &Service{
		Channels:     channels,
		AuthSessions: authSessions,
		Captchas:     captchas,
		MonitorLogs:  monitorLogs,
		Cipher:       cipher,
	}
}

// NewAPITokenCredential token 模式下 NewAPI 的凭据 JSON 结构。
//
// Cookie：浏览器 DevTools 里拷出来的整条 Cookie 头
// UserID：上游账号 ID（NewAPI 个人设置页可见，作为 New-Api-User 请求头必填）
type NewAPITokenCredential struct {
	Cookie string `json:"cookie"`
	UserID string `json:"user_id"`
}

// Sub2APITokenCredential token 模式下 Sub2API 的凭据。
type Sub2APITokenCredential struct {
	AccessToken string `json:"access_token"`
}

// CreateInput 新建渠道使用的明文输入。
//
// CredentialMode 决定字段语义：
//   - password: Password 必填；Username 为登录账号
//   - token:    TokenCredential 必填（已序列化为 JSON 字符串）；Username 仅作展示备注
type CreateInput struct {
	Name               string
	Type               storage.ChannelType
	SiteURL            string
	Username           string
	Password           string
	CredentialMode     storage.CredentialMode
	BalanceMode        storage.BalanceMode
	ManualBalance      float64
	Remark             string
	TokenCredential    string // JSON：password 模式时为空
	TurnstileEnabled   bool
	CaptchaConfigID    *uint
	BalanceThreshold   float64
	MonitorEnabled     bool
	AdditionalAccounts []AdditionalAccountInput
}

// AdditionalAccountInput is a credential record under an automatically read
// channel. Password and TokenCredential stay nil when an existing credential
// should be kept during an edit.
type AdditionalAccountInput struct {
	ID               uint
	Username         string
	Password         *string
	CredentialMode   storage.CredentialMode
	TokenCredential  *string
	TurnstileEnabled bool
	CaptchaConfigID  *uint
}

func (s *Service) Create(in CreateInput) (*storage.Channel, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, errors.New("渠道名称不能为空")
	}
	if err := s.ensureUniqueName(name, 0); err != nil {
		return nil, err
	}
	balanceMode := in.BalanceMode
	if balanceMode == "" {
		balanceMode = storage.BalanceModeAuto
	}
	if balanceMode != storage.BalanceModeAuto && balanceMode != storage.BalanceModeManual {
		return nil, fmt.Errorf("unknown balance mode: %s", balanceMode)
	}
	if in.ManualBalance < 0 {
		return nil, errors.New("手动余额不能小于 0")
	}
	remark := strings.TrimSpace(in.Remark)
	if len(remark) > 512 {
		return nil, errors.New("备注不能超过 512 个字符")
	}
	mode := in.CredentialMode
	if mode == "" {
		mode = storage.CredentialModePassword
	}
	var enc string
	if balanceMode == storage.BalanceModeAuto {
		rawCred, err := selectRawCredential(mode, in.Password, in.TokenCredential)
		if err != nil {
			return nil, err
		}
		if err := validateCredential(in.Type, mode, rawCred); err != nil {
			return nil, err
		}
		enc, err = s.Cipher.Encrypt(rawCred)
		if err != nil {
			return nil, fmt.Errorf("encrypt credential: %w", err)
		}
	} else {
		mode = storage.CredentialModePassword
	}
	c := &storage.Channel{
		Name:             name,
		Type:             in.Type,
		SiteURL:          in.SiteURL,
		Username:         in.Username,
		PasswordCipher:   enc,
		CredentialMode:   mode,
		BalanceMode:      balanceMode,
		ManualBalance:    in.ManualBalance,
		Remark:           remark,
		TurnstileEnabled: in.TurnstileEnabled && mode == storage.CredentialModePassword, // token 模式不需要打码
		CaptchaConfigID:  in.CaptchaConfigID,
		BalanceThreshold: in.BalanceThreshold,
		MonitorEnabled:   in.MonitorEnabled,
	}
	if balanceMode == storage.BalanceModeManual {
		c.Username = ""
		c.TurnstileEnabled = false
		c.CaptchaConfigID = nil
		resetManualBalance(c, time.Now())
	}
	if mode == storage.CredentialModeToken {
		// token 模式不依赖打码 provider
		c.CaptchaConfigID = nil
	}
	if err := s.Channels.Create(c); err != nil {
		return nil, err
	}
	if balanceMode == storage.BalanceModeAuto {
		if err := s.Channels.EnsurePrimaryAccount(c); err != nil {
			_ = s.Channels.Delete(c.ID)
			return nil, err
		}
		if err := s.syncAdditionalAccounts(c, in.AdditionalAccounts); err != nil {
			_ = s.Channels.Delete(c.ID)
			return nil, err
		}
	}
	return c, nil
}

// UpdateInput 编辑渠道的可选字段。Password / TokenCredential 为空表示不修改凭据。
type UpdateInput struct {
	Name               *string
	SiteURL            *string
	Username           *string
	Password           *string
	CredentialMode     *storage.CredentialMode
	BalanceMode        *storage.BalanceMode
	ManualBalance      *float64
	Remark             *string
	TokenCredential    *string // JSON
	TurnstileEnabled   *bool
	CaptchaConfigID    *uint
	BalanceThreshold   *float64
	MonitorEnabled     *bool
	AdditionalAccounts *[]AdditionalAccountInput
}

func (s *Service) Update(id uint, in UpdateInput) (*storage.Channel, error) {
	c, err := s.Channels.FindByID(id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, errors.New("渠道名称不能为空")
		}
		if err := s.ensureUniqueName(name, id); err != nil {
			return nil, err
		}
		c.Name = name
	}
	if in.SiteURL != nil {
		c.SiteURL = *in.SiteURL
	}
	if in.Username != nil {
		c.Username = *in.Username
	}
	if in.Remark != nil {
		remark := strings.TrimSpace(*in.Remark)
		if len(remark) > 512 {
			return nil, errors.New("备注不能超过 512 个字符")
		}
		c.Remark = remark
	}
	manualBalanceChanged := in.ManualBalance != nil
	if manualBalanceChanged {
		if *in.ManualBalance < 0 {
			return nil, errors.New("手动余额不能小于 0")
		}
		c.ManualBalance = *in.ManualBalance
	}
	currentBalanceMode := c.BalanceMode
	if currentBalanceMode == "" {
		currentBalanceMode = storage.BalanceModeAuto
	}
	finalBalanceMode := currentBalanceMode
	if in.BalanceMode != nil && *in.BalanceMode != "" {
		finalBalanceMode = *in.BalanceMode
	}
	if finalBalanceMode != storage.BalanceModeAuto && finalBalanceMode != storage.BalanceModeManual {
		return nil, fmt.Errorf("unknown balance mode: %s", finalBalanceMode)
	}
	balanceModeChanged := finalBalanceMode != currentBalanceMode
	if finalBalanceMode == storage.BalanceModeManual {
		c.BalanceMode = finalBalanceMode
		c.Username = ""
		c.PasswordCipher = ""
		c.CredentialMode = storage.CredentialModePassword
		c.TurnstileEnabled = false
		c.CaptchaConfigID = nil
		_ = s.AuthSessions.Delete(c.ID)
		// Entering manual mode or explicitly editing its balance starts a new
		// accounting period. Other edits (such as a remark) must not recharge it.
		if balanceModeChanged || manualBalanceChanged || c.LastBalance == nil {
			resetManualBalance(c, time.Now())
		}
	} else {
		c.BalanceMode = finalBalanceMode
		if balanceModeChanged {
			c.LastBalance = nil
			c.LastBalanceAt = nil
			c.ManualUsageBaseline = nil
			c.ManualUsageBasis = ""
		}
	}

	if finalBalanceMode == storage.BalanceModeAuto {
		// 决定本次更新后的最终凭据模式。
		finalMode := c.CredentialMode
		if in.CredentialMode != nil && *in.CredentialMode != "" {
			finalMode = *in.CredentialMode
		}
		if finalMode == "" {
			finalMode = storage.CredentialModePassword
		}

		// 是否切换了模式 → 强制重写凭据并清空 session
		modeChanged := finalMode != c.CredentialMode
		var rawCred string
		switch finalMode {
		case storage.CredentialModePassword:
			if in.Password != nil && *in.Password != "" {
				rawCred = *in.Password
			} else if modeChanged {
				return nil, errors.New("切换到账号密码模式时必须填写密码")
			}
		case storage.CredentialModeToken:
			if in.TokenCredential != nil && *in.TokenCredential != "" {
				rawCred = *in.TokenCredential
			} else if modeChanged {
				return nil, errors.New("切换到 token 模式时必须填写凭据")
			}
		default:
			return nil, fmt.Errorf("unknown credential mode: %s", finalMode)
		}
		if balanceModeChanged && rawCred == "" {
			return nil, errors.New("切换到自动余额模式时必须填写凭据")
		}
		if rawCred != "" {
			if err := validateCredential(c.Type, finalMode, rawCred); err != nil {
				return nil, err
			}
			enc, err := s.Cipher.Encrypt(rawCred)
			if err != nil {
				return nil, fmt.Errorf("encrypt credential: %w", err)
			}
			c.PasswordCipher = enc
			c.CredentialMode = finalMode
			_ = s.AuthSessions.Delete(c.ID)
		} else if modeChanged {
			return nil, errors.New("凭据模式变更必须同时提供新凭据")
		}
		if in.TurnstileEnabled != nil {
			c.TurnstileEnabled = *in.TurnstileEnabled && finalMode == storage.CredentialModePassword
		}
		if in.CaptchaConfigID != nil {
			if finalMode == storage.CredentialModePassword {
				c.CaptchaConfigID = in.CaptchaConfigID
			} else {
				c.CaptchaConfigID = nil
			}
		} else if finalMode == storage.CredentialModeToken {
			c.CaptchaConfigID = nil
		}
	}
	if in.BalanceThreshold != nil {
		c.BalanceThreshold = *in.BalanceThreshold
	}
	if in.MonitorEnabled != nil {
		c.MonitorEnabled = *in.MonitorEnabled
	}
	if err := s.Channels.Update(c); err != nil {
		return nil, err
	}
	if finalBalanceMode == storage.BalanceModeAuto {
		if err := s.Channels.EnsurePrimaryAccount(c); err != nil {
			return nil, err
		}
		if in.AdditionalAccounts != nil {
			if err := s.syncAdditionalAccounts(c, *in.AdditionalAccounts); err != nil {
				return nil, err
			}
		}
	} else if balanceModeChanged {
		if err := s.Channels.DeleteAccounts(c.ID); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (s *Service) syncAdditionalAccounts(c *storage.Channel, inputs []AdditionalAccountInput) error {
	existing, err := s.Channels.ListAccounts(c.ID)
	if err != nil {
		return err
	}
	existingByID := make(map[uint]storage.ChannelAccount, len(existing))
	for _, account := range existing {
		if !account.IsPrimary {
			existingByID[account.ID] = account
		}
	}
	accounts := make([]storage.ChannelAccount, 0, len(inputs))
	resetSessions := make([]uint, 0, len(inputs))
	seen := make(map[uint]struct{}, len(inputs))
	for _, input := range inputs {
		if input.ID != 0 {
			if _, duplicate := seen[input.ID]; duplicate {
				return errors.New("附加账号不能重复")
			}
			seen[input.ID] = struct{}{}
		}
		previous, exists := existingByID[input.ID]
		if input.ID != 0 && !exists {
			return errors.New("附加账号不存在或不属于当前渠道")
		}
		account, resetSession, err := s.buildAdditionalAccount(c.Type, input, previous, exists)
		if err != nil {
			return err
		}
		accounts = append(accounts, account)
		if resetSession && account.ID != 0 {
			resetSessions = append(resetSessions, account.ID)
		}
	}
	return s.Channels.ReplaceAdditionalAccounts(c.ID, accounts, resetSessions)
}

func (s *Service) buildAdditionalAccount(channelType storage.ChannelType, input AdditionalAccountInput, previous storage.ChannelAccount, exists bool) (storage.ChannelAccount, bool, error) {
	username := strings.TrimSpace(input.Username)
	if username == "" {
		return storage.ChannelAccount{}, false, errors.New("附加账号名称不能为空")
	}
	mode := input.CredentialMode
	if mode == "" && exists {
		mode = previous.CredentialMode
	}
	if mode == "" {
		mode = storage.CredentialModePassword
	}
	if mode != storage.CredentialModePassword && mode != storage.CredentialModeToken {
		return storage.ChannelAccount{}, false, fmt.Errorf("unknown credential mode: %s", mode)
	}
	account := previous
	account.ID = input.ID
	account.Username = username
	account.CredentialMode = mode
	resetSession := !exists || mode != previous.CredentialMode

	var rawCredential string
	switch mode {
	case storage.CredentialModePassword:
		if input.Password != nil && *input.Password != "" {
			rawCredential = *input.Password
		} else if !exists || mode != previous.CredentialMode {
			return storage.ChannelAccount{}, false, errors.New("附加账号的账号密码不能为空")
		}
	case storage.CredentialModeToken:
		if input.TokenCredential != nil && *input.TokenCredential != "" {
			rawCredential = *input.TokenCredential
		} else if !exists || mode != previous.CredentialMode {
			return storage.ChannelAccount{}, false, errors.New("切换附加账号到 token 模式时必须填写凭据")
		}
	}
	if rawCredential != "" {
		if err := validateCredential(channelType, mode, rawCredential); err != nil {
			return storage.ChannelAccount{}, false, err
		}
		ciphertext, err := s.Cipher.Encrypt(rawCredential)
		if err != nil {
			return storage.ChannelAccount{}, false, fmt.Errorf("encrypt additional credential: %w", err)
		}
		account.PasswordCipher = ciphertext
		resetSession = true
	}

	if mode == storage.CredentialModeToken {
		account.TurnstileEnabled = false
		account.CaptchaConfigID = nil
	} else {
		account.TurnstileEnabled = input.TurnstileEnabled
		account.CaptchaConfigID = input.CaptchaConfigID
	}
	if exists && (account.TurnstileEnabled != previous.TurnstileEnabled || !sameUintPtr(account.CaptchaConfigID, previous.CaptchaConfigID)) {
		resetSession = true
	}
	return account, resetSession, nil
}

func sameUintPtr(left, right *uint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func resetManualBalance(c *storage.Channel, at time.Time) {
	balance := c.ManualBalance
	c.LastBalance = &balance
	c.LastBalanceAt = &at
	c.LastError = ""
	c.ManualUsageBaseline = nil
	c.ManualUsageBasis = ""
}

func (s *Service) ensureUniqueName(name string, excludeID uint) error {
	existing, err := s.Channels.FindByName(name)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if existing.ID == excludeID {
		return nil
	}
	return fmt.Errorf("%w：渠道 #%d（%s）正在使用该名称", ErrNameConflict, existing.ID, existing.Name)
}

// selectRawCredential 在 Create 时根据 mode 决定要落库的明文凭据字符串。
func selectRawCredential(mode storage.CredentialMode, password, tokenCredential string) (string, error) {
	switch mode {
	case storage.CredentialModePassword:
		if password == "" {
			return "", errors.New("账号密码模式下密码不能为空")
		}
		return password, nil
	case storage.CredentialModeToken:
		if tokenCredential == "" {
			return "", errors.New("token 模式下必须提供凭据")
		}
		return tokenCredential, nil
	default:
		return "", fmt.Errorf("unknown credential mode: %s", mode)
	}
}

// validateCredential 在保存前对凭据做语法 / 必填字段校验，能尽早把无效输入挡在 connector 外。
//
// 注意：这里只做语法层校验，不做"凭据是否真的有效"的网络验证——
// 那个交给后续 TestLogin / 第一次同步去发现。
func validateCredential(channelType storage.ChannelType, mode storage.CredentialMode, raw string) error {
	if mode != storage.CredentialModeToken {
		return nil
	}
	switch channelType {
	case storage.ChannelTypeNewAPI:
		var cred NewAPITokenCredential
		if err := json.Unmarshal([]byte(raw), &cred); err != nil {
			return fmt.Errorf("解析 NewAPI 凭据 JSON 失败：%w", err)
		}
		if strings.TrimSpace(cred.Cookie) == "" {
			return errors.New("NewAPI token 模式需要 Cookie")
		}
		if strings.TrimSpace(cred.UserID) == "" {
			return errors.New("NewAPI token 模式需要 User ID（在 NewAPI 个人设置页查看）")
		}
	case storage.ChannelTypeSub2API:
		var cred Sub2APITokenCredential
		if err := json.Unmarshal([]byte(raw), &cred); err != nil {
			return fmt.Errorf("解析 Sub2API 凭据 JSON 失败：%w", err)
		}
		if strings.TrimSpace(cred.AccessToken) == "" {
			return errors.New("Sub2API token 模式需要 access_token")
		}
	default:
		return fmt.Errorf("unknown channel type: %s", channelType)
	}
	return nil
}

func (s *Service) Delete(id uint) error {
	return s.Channels.Delete(id)
}

// Resolve 把存储层的加密渠道解密成 connector 可用的 Channel。
//
// 注意：这一步**不**求解 Turnstile —— 打码只在真正要登录时做（见 prepareTurnstile），
// 复用现有 session 的路径无需任何打码消耗。
//
// token 模式下 connector.Channel.Password 留空——connector 永远不会读到它。
func (s *Service) Resolve(ctx context.Context, c *storage.Channel) (*connector.Channel, error) {
	_ = ctx
	raw, err := s.Cipher.Decrypt(c.PasswordCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}
	resolved := &connector.Channel{
		ID:               c.ID,
		Name:             c.Name,
		Type:             connector.ChannelType(c.Type),
		SiteURL:          c.SiteURL,
		Username:         c.Username,
		TurnstileEnabled: c.TurnstileEnabled,
	}
	if c.CredentialMode == storage.CredentialModeToken {
		// token 模式：raw 是 JSON，Password 留空避免被 connector 误用
		resolved.Password = ""
	} else {
		resolved.Password = raw
	}
	return resolved, nil
}

// AccountChannel projects an additional account onto the connector's existing
// channel shape. It keeps the parent channel ID for logs and notifications,
// while AuthSessionKey isolates the additional account's cached session.
func (s *Service) AccountChannel(parent *storage.Channel, account storage.ChannelAccount) *storage.Channel {
	if account.IsPrimary {
		return parent
	}
	resolved := *parent
	resolved.Username = account.Username
	resolved.PasswordCipher = account.PasswordCipher
	resolved.CredentialMode = account.CredentialMode
	resolved.TurnstileEnabled = account.TurnstileEnabled
	resolved.CaptchaConfigID = account.CaptchaConfigID
	resolved.AuthSessionKey = storage.AdditionalAccountSessionKey(account.ID)
	return &resolved
}

// buildSessionFromToken 在 token 模式下，把用户提供的凭据 JSON 解析成 AuthSession。
// 不发任何 HTTP 请求——失效检测留给 connector.CheckAuth + 后续 GetBalance / GetRates。
func (s *Service) buildSessionFromToken(c *storage.Channel) (*connector.AuthSession, error) {
	raw, err := s.Cipher.Decrypt(c.PasswordCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}
	switch c.Type {
	case storage.ChannelTypeNewAPI:
		var cred NewAPITokenCredential
		if err := json.Unmarshal([]byte(raw), &cred); err != nil {
			return nil, fmt.Errorf("parse newapi token credential: %w", err)
		}
		return &connector.AuthSession{
			UserID:    cred.UserID,
			Cookie:    cred.Cookie,
			ExpiresAt: time.Now().Add(tokenSessionTTL),
		}, nil
	case storage.ChannelTypeSub2API:
		var cred Sub2APITokenCredential
		if err := json.Unmarshal([]byte(raw), &cred); err != nil {
			return nil, fmt.Errorf("parse sub2api token credential: %w", err)
		}
		return &connector.AuthSession{
			AccessToken: cred.AccessToken,
			ExpiresAt:   time.Now().Add(tokenSessionTTL),
		}, nil
	default:
		return nil, fmt.Errorf("unknown channel type: %s", c.Type)
	}
}

// prepareTurnstile 在调用 conn.Login 之前求解 Turnstile token。
// 没启用 turnstile 或者上游 site 公开接口说"未开启 Turnstile"时是空操作。
func (s *Service) prepareTurnstile(
	ctx context.Context,
	c *storage.Channel,
	resolved *connector.Channel,
	conn connector.Connector,
) error {
	if !c.TurnstileEnabled || c.CaptchaConfigID == nil {
		return nil
	}
	progress.Start(ctx, progress.StageCaptcha, "求解 Turnstile…")
	siteKey, err := conn.GetTurnstileSiteKey(ctx, resolved)
	if err != nil {
		progress.Fail(ctx, progress.StageCaptcha, err.Error())
		return fmt.Errorf("fetch turnstile site key: %w", err)
	}
	if siteKey == "" {
		progress.OK(ctx, progress.StageCaptcha, "上游未开启 Turnstile，跳过")
		return nil
	}
	token, err := s.solveCaptcha(ctx, *c.CaptchaConfigID, siteKey, c.SiteURL)
	if err != nil {
		progress.Fail(ctx, progress.StageCaptcha, err.Error())
		return fmt.Errorf("solve captcha: %w", err)
	}
	resolved.TurnstileToken = token
	progress.OK(ctx, progress.StageCaptcha, "打码完成")
	return nil
}

func (s *Service) solveCaptcha(ctx context.Context, captchaID uint, siteKey, pageURL string) (string, error) {
	cfg, err := s.Captchas.FindByID(captchaID)
	if err != nil {
		return "", err
	}
	if !cfg.Enabled {
		return "", errors.New("captcha config disabled")
	}
	apiKey, err := s.Cipher.Decrypt(cfg.APIKeyCipher)
	if err != nil {
		return "", err
	}
	provider, err := captcha.Build(cfg, apiKey)
	if err != nil {
		return "", err
	}
	return provider.SolveTurnstile(ctx, siteKey, pageURL)
}

// EnsureSession 优先复用未过期的 session，否则重新登录并加密回写。
//
// token 模式：
//   - 跳过 AuthSessions 表与 Login 调用
//   - 每次构造一个临时 AuthSession（基于用户提供的凭据）返回
//   - CheckAuth 用来发现 token 是否还有效；失效会在 last_error 显示
func (s *Service) EnsureSession(
	ctx context.Context,
	c *storage.Channel,
	resolved *connector.Channel,
	conn connector.Connector,
) (*connector.AuthSession, error) {
	sessionKey := c.ID
	if c.AuthSessionKey != 0 {
		sessionKey = c.AuthSessionKey
	}
	if c.CredentialMode == storage.CredentialModeToken {
		progress.Start(ctx, progress.StageSession, "使用用户提供的 token…")
		session, err := s.buildSessionFromToken(c)
		if err != nil {
			progress.Fail(ctx, progress.StageSession, err.Error())
			s.setCredentialError(c, err.Error())
			return nil, err
		}
		// 走一次 CheckAuth 确认 token 仍有效。失败立即标 last_error，调用方往上抛错。
		if err := conn.CheckAuth(ctx, resolved, session); err != nil {
			msg := "token 已失效，请重新粘贴凭据：" + err.Error()
			progress.Fail(ctx, progress.StageSession, msg)
			s.setCredentialError(c, msg)
			return nil, errors.New(msg)
		}
		s.setCredentialError(c, "")
		progress.OK(ctx, progress.StageSession, "token 有效，跳过登录")
		return session, nil
	}

	saved, err := s.AuthSessions.FindByChannel(sessionKey)
	if err != nil {
		return nil, err
	}
	if saved != nil && saved.ExpiresAt != nil && time.Until(*saved.ExpiresAt) > SessionRefreshThreshold {
		session, err := s.decryptSession(saved)
		if err != nil {
			return nil, err
		}
		// 轻量校验现有 session，不通过则继续走重新登录。
		progress.Start(ctx, progress.StageSession, "校验已有会话…")
		if err := conn.CheckAuth(ctx, resolved, session); err == nil {
			progress.OK(ctx, progress.StageSession, "复用现有会话")
			return session, nil
		}
		progress.OK(ctx, progress.StageSession, "会话已失效，重新登录")
	}
	return s.login(ctx, c, resolved, conn)
}

func (s *Service) login(
	ctx context.Context,
	c *storage.Channel,
	resolved *connector.Channel,
	conn connector.Connector,
) (*connector.AuthSession, error) {
	if err := s.prepareTurnstile(ctx, c, resolved, conn); err != nil {
		return nil, err
	}
	progress.Start(ctx, progress.StageLogin, "登录上游…")
	started := time.Now()
	session, err := conn.Login(ctx, resolved)
	finished := time.Now()
	_ = s.MonitorLogs.Append(&storage.MonitorLog{
		ChannelID:    c.ID,
		Job:          storage.MonitorJobLogin,
		Success:      err == nil,
		ErrorMessage: errString(err),
		StartedAt:    started,
		FinishedAt:   finished,
	})
	if err != nil {
		progress.Fail(ctx, progress.StageLogin, err.Error())
		s.setCredentialError(c, err.Error())
		return nil, err
	}
	sessionKey := c.ID
	if c.AuthSessionKey != 0 {
		sessionKey = c.AuthSessionKey
	}
	if err := s.persistSession(sessionKey, session); err != nil {
		progress.Fail(ctx, progress.StageLogin, err.Error())
		return nil, err
	}
	s.setCredentialError(c, "")
	progress.OK(ctx, progress.StageLogin, "登录成功")
	return session, nil
}

func (s *Service) setCredentialError(c *storage.Channel, message string) {
	if c.AuthSessionKey >= storage.AdditionalAccountSessionOffset {
		_ = s.Channels.SetAccountError(c.AuthSessionKey-storage.AdditionalAccountSessionOffset, message)
		return
	}
	_ = s.Channels.SetLastError(c.ID, message)
}

func (s *Service) persistSession(channelID uint, session *connector.AuthSession) error {
	acc, err := s.Cipher.Encrypt(session.AccessToken)
	if err != nil {
		return fmt.Errorf("encrypt access token: %w", err)
	}
	cookie, err := s.Cipher.Encrypt(session.Cookie)
	if err != nil {
		return fmt.Errorf("encrypt cookie: %w", err)
	}
	csrf, err := s.Cipher.Encrypt(session.CSRFToken)
	if err != nil {
		return fmt.Errorf("encrypt csrf: %w", err)
	}
	now := time.Now()
	expires := session.ExpiresAt
	return s.AuthSessions.Upsert(&storage.AuthSession{
		ChannelID:         channelID,
		UserID:            session.UserID,
		AccessTokenCipher: acc,
		CookieCipher:      cookie,
		CSRFTokenCipher:   csrf,
		ExpiresAt:         &expires,
		LastLoginAt:       &now,
	})
}

func (s *Service) decryptSession(saved *storage.AuthSession) (*connector.AuthSession, error) {
	acc, err := s.Cipher.Decrypt(saved.AccessTokenCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt access token: %w", err)
	}
	cookie, err := s.Cipher.Decrypt(saved.CookieCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt cookie: %w", err)
	}
	csrf, err := s.Cipher.Decrypt(saved.CSRFTokenCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt csrf: %w", err)
	}
	expires := time.Time{}
	if saved.ExpiresAt != nil {
		expires = *saved.ExpiresAt
	}
	return &connector.AuthSession{
		UserID:      saved.UserID,
		AccessToken: acc,
		Cookie:      cookie,
		CSRFToken:   csrf,
		ExpiresAt:   expires,
	}, nil
}

// TestLogin 手动测试登录：
//   - password 模式：复用 login() 的完整流程（打码 → 登录 → 持久化）
//   - token 模式：直接走 EnsureSession，等同于检查 CheckAuth 是否通过
func (s *Service) TestLogin(ctx context.Context, channelID uint) error {
	c, err := s.Channels.FindByID(channelID)
	if err != nil {
		return err
	}
	if c.BalanceMode == storage.BalanceModeManual {
		return errors.New("手动余额渠道不需要测试登录")
	}
	accounts, err := s.Channels.ListAccounts(channelID)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return s.testLoginForChannel(ctx, c)
	}
	var failures []error
	for _, account := range accounts {
		target := s.AccountChannel(c, account)
		if err := s.testLoginForChannel(ctx, target); err != nil {
			_ = s.Channels.SetAccountError(account.ID, err.Error())
			failures = append(failures, fmt.Errorf("%s: %w", account.Username, err))
			continue
		}
		_ = s.Channels.SetAccountError(account.ID, "")
	}
	return errors.Join(failures...)
}

func (s *Service) testLoginForChannel(ctx context.Context, c *storage.Channel) error {
	resolved, err := s.Resolve(ctx, c)
	if err != nil {
		return err
	}
	conn, err := connector.For(connector.ChannelType(c.Type))
	if err != nil {
		return err
	}
	if c.CredentialMode == storage.CredentialModeToken {
		_, err = s.EnsureSession(ctx, c, resolved, conn)
		return err
	}
	_, err = s.login(ctx, c, resolved, conn)
	return err
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

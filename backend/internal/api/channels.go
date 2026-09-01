package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/channel"
	"github.com/guliansheng/gateway-ops/internal/progress"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"gorm.io/gorm"
)

func registerChannels(g *gin.RouterGroup, d *Deps) {
	gp := g.Group("/channels")
	gp.GET("", func(c *gin.Context) { listChannels(c, d) })
	gp.GET("/metrics", func(c *gin.Context) { channelMetrics(c, d) })
	gp.GET("/latency-trends", func(c *gin.Context) { channelLatencyTrends(c, d) })
	gp.POST("/sync-all", func(c *gin.Context) { syncAllChannels(c, d) })
	gp.POST("", func(c *gin.Context) { createChannel(c, d) })
	gp.GET("/:id", func(c *gin.Context) { getChannel(c, d) })
	gp.PUT("/:id", func(c *gin.Context) { updateChannel(c, d) })
	gp.DELETE("/:id", func(c *gin.Context) { deleteChannel(c, d) })
	gp.POST("/:id/enable", func(c *gin.Context) { toggleChannel(c, d, true) })
	gp.POST("/:id/disable", func(c *gin.Context) { toggleChannel(c, d, false) })
	gp.POST("/:id/test-login", func(c *gin.Context) { testLogin(c, d) })
	gp.POST("/:id/refresh-balance", func(c *gin.Context) { refreshBalance(c, d) })
	gp.POST("/:id/refresh-rates", func(c *gin.Context) { refreshRates(c, d) })
	gp.POST("/:id/sync", func(c *gin.Context) { syncChannel(c, d) })
	gp.GET("/:id/rates", func(c *gin.Context) { channelRates(c, d) })
	gp.POST("/:id/rates", func(c *gin.Context) { createChannelRate(c, d) })
	gp.PUT("/:id/rates/:rate_id", func(c *gin.Context) { updateChannelRate(c, d) })
	gp.DELETE("/:id/rates/:rate_id", func(c *gin.Context) { deleteChannelRate(c, d) })
	gp.GET("/:id/balance-history", func(c *gin.Context) { balanceHistory(c, d) })
}

type channelInput struct {
	Name             string                 `json:"name" binding:"required"`
	Type             storage.ChannelType    `json:"type" binding:"required"`
	SiteURL          string                 `json:"site_url" binding:"required"`
	Username         string                 `json:"username"`
	Password         string                 `json:"password"`
	CredentialMode   storage.CredentialMode `json:"credential_mode"`
	BalanceMode      storage.BalanceMode    `json:"balance_mode"`
	ManualBalance    float64                `json:"manual_balance"`
	Remark           string                 `json:"remark"`
	TokenCredential  string                 `json:"token_credential"` // JSON：token 模式时填写
	TurnstileEnabled bool                   `json:"turnstile_enabled"`
	CaptchaConfigID  *uint                  `json:"captcha_config_id"`
	BalanceThreshold float64                `json:"balance_threshold"`
	MonitorEnabled   bool                   `json:"monitor_enabled"`
	Accounts         []channelAccountInput  `json:"accounts"`
}

type channelAccountInput struct {
	ID               uint                   `json:"id,omitempty"`
	Username         string                 `json:"username"`
	Password         *string                `json:"password,omitempty"`
	CredentialMode   storage.CredentialMode `json:"credential_mode"`
	TokenCredential  *string                `json:"token_credential,omitempty"`
	TurnstileEnabled bool                   `json:"turnstile_enabled"`
	CaptchaConfigID  *uint                  `json:"captcha_config_id"`
}

type channelUpdateInput struct {
	Name             *string                 `json:"name"`
	SiteURL          *string                 `json:"site_url"`
	Username         *string                 `json:"username"`
	Password         *string                 `json:"password"`
	CredentialMode   *storage.CredentialMode `json:"credential_mode"`
	BalanceMode      *storage.BalanceMode    `json:"balance_mode"`
	ManualBalance    *float64                `json:"manual_balance"`
	Remark           *string                 `json:"remark"`
	TokenCredential  *string                 `json:"token_credential"`
	TurnstileEnabled *bool                   `json:"turnstile_enabled"`
	CaptchaConfigID  *uint                   `json:"captcha_config_id"`
	BalanceThreshold *float64                `json:"balance_threshold"`
	MonitorEnabled   *bool                   `json:"monitor_enabled"`
	Accounts         *[]channelAccountInput  `json:"accounts"`
}

func listChannels(c *gin.Context, d *Deps) {
	list, err := d.Channels.ListForManagement()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	views, err := channelViews(d, list)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": views})
}

type channelView struct {
	storage.Channel
	Accounts []storage.ChannelAccount `json:"accounts"`
}

func channelViews(d *Deps, channels []storage.Channel) ([]channelView, error) {
	ids := make([]uint, 0, len(channels))
	for _, item := range channels {
		ids = append(ids, item.ID)
	}
	accountsByChannel, err := d.Channels.ListAccountsForChannels(ids)
	if err != nil {
		return nil, err
	}
	views := make([]channelView, 0, len(channels))
	for _, item := range channels {
		accounts := accountsByChannel[item.ID]
		if accounts == nil {
			accounts = []storage.ChannelAccount{}
		}
		views = append(views, channelView{Channel: item, Accounts: accounts})
	}
	return views, nil
}

func additionalAccountInputs(items []channelAccountInput) []channel.AdditionalAccountInput {
	accounts := make([]channel.AdditionalAccountInput, 0, len(items))
	for _, item := range items {
		accounts = append(accounts, channel.AdditionalAccountInput{
			ID: item.ID, Username: item.Username, Password: item.Password,
			CredentialMode: item.CredentialMode, TokenCredential: item.TokenCredential,
			TurnstileEnabled: item.TurnstileEnabled, CaptchaConfigID: item.CaptchaConfigID,
		})
	}
	return accounts
}

type channelMetricView struct {
	ChannelID                uint    `json:"channel_id"`
	ConsumptionAmount        float64 `json:"consumption_amount"`
	CumulativeRechargeAmount float64 `json:"cumulative_recharge_amount"`
	UserChargeAmount         float64 `json:"user_charge_amount"`
	MatchedAccountCount      int     `json:"matched_account_count"`
	UserChargeComplete       bool    `json:"user_charge_complete"`
	CurrentBalance           float64 `json:"current_balance"`
}

type channelLatencyTrendView struct {
	ChannelID uint                         `json:"channel_id"`
	Samples   []storage.RelayLatencySample `json:"samples"`
}

func channelMetrics(c *gin.Context, d *Deps) {
	since, rangeName := operationSince(c.DefaultQuery("range", "today"))
	channels, err := d.Channels.List()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	recharges, err := d.Operations.ChannelRechargeTotals()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	consumption, err := d.Rates.ChannelConsumptionTotals(since)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	usage, err := d.Relay.ChannelUsageTotals(c.Request.Context(), channels, rangeName, since)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	result := make([]channelMetricView, 0, len(channels))
	for _, item := range channels {
		interval := usage[item.ID]
		currentBalance := float64(0)
		if item.LastBalance != nil {
			currentBalance = *item.LastBalance
		}
		if item.BalanceMode == storage.BalanceModeManual && item.LastBalance == nil {
			currentBalance = item.ManualBalance
		}
		if item.BalanceMode == storage.BalanceModeManual {
			consumption[item.ID] = interval.Cost
		}
		result = append(result, channelMetricView{
			ChannelID: item.ID, ConsumptionAmount: consumption[item.ID], CumulativeRechargeAmount: recharges[item.ID],
			UserChargeAmount: interval.UserCharge, MatchedAccountCount: interval.MatchedAccountCount,
			UserChargeComplete: interval.Complete, CurrentBalance: currentBalance,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func channelLatencyTrends(c *gin.Context, d *Deps) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "60"))
	channels, err := d.Channels.List()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	trends, err := d.Relay.ChannelLatencyTrends(channels, limit)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	result := make([]channelLatencyTrendView, 0, len(channels))
	for _, channel := range channels {
		samples := trends[channel.ID]
		if samples == nil {
			samples = []storage.RelayLatencySample{}
		}
		result = append(result, channelLatencyTrendView{ChannelID: channel.ID, Samples: samples})
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func syncAllChannels(c *gin.Context, d *Deps) {
	result := d.Monitor.SyncAll(c.Request.Context(), true)
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func createChannel(c *gin.Context, d *Deps) {
	var in channelInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	created, err := d.ChannelSvc.Create(channel.CreateInput{
		Name:               in.Name,
		Type:               in.Type,
		SiteURL:            in.SiteURL,
		Username:           in.Username,
		Password:           in.Password,
		CredentialMode:     in.CredentialMode,
		TokenCredential:    in.TokenCredential,
		BalanceMode:        in.BalanceMode,
		ManualBalance:      in.ManualBalance,
		Remark:             in.Remark,
		TurnstileEnabled:   in.TurnstileEnabled,
		CaptchaConfigID:    in.CaptchaConfigID,
		BalanceThreshold:   in.BalanceThreshold,
		MonitorEnabled:     in.MonitorEnabled,
		AdditionalAccounts: additionalAccountInputs(in.Accounts),
	})
	if err != nil {
		fail(c, channelErrorStatus(err), err)
		return
	}
	if created.BalanceMode == storage.BalanceModeManual {
		if err := d.Relay.ReconcileManualChannelLinks(); err != nil {
			fail(c, http.StatusInternalServerError, fmt.Errorf("渠道已创建，但自动关联账号失败: %w", err))
			return
		}
	}
	views, err := channelViews(d, []storage.Channel{*created})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": views[0]})
}

func getChannel(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ch, err := d.Channels.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	views, err := channelViews(d, []storage.Channel{*ch})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": views[0]})
}

func updateChannel(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in channelUpdateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	updated, err := d.ChannelSvc.Update(id, channel.UpdateInput{
		Name:               in.Name,
		SiteURL:            in.SiteURL,
		Username:           in.Username,
		Password:           in.Password,
		CredentialMode:     in.CredentialMode,
		TokenCredential:    in.TokenCredential,
		BalanceMode:        in.BalanceMode,
		ManualBalance:      in.ManualBalance,
		Remark:             in.Remark,
		TurnstileEnabled:   in.TurnstileEnabled,
		CaptchaConfigID:    in.CaptchaConfigID,
		BalanceThreshold:   in.BalanceThreshold,
		MonitorEnabled:     in.MonitorEnabled,
		AdditionalAccounts: additionalAccountInputsPtr(in.Accounts),
	})
	if err != nil {
		fail(c, channelErrorStatus(err), err)
		return
	}
	if err := d.Relay.ReconcileManualChannelLinks(); err != nil {
		fail(c, http.StatusInternalServerError, fmt.Errorf("渠道已更新，但同步自动关联账号失败: %w", err))
		return
	}
	views, err := channelViews(d, []storage.Channel{*updated})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": views[0]})
}

func additionalAccountInputsPtr(items *[]channelAccountInput) *[]channel.AdditionalAccountInput {
	if items == nil {
		return nil
	}
	accounts := additionalAccountInputs(*items)
	return &accounts
}

func channelErrorStatus(err error) int {
	if errors.Is(err, channel.ErrNameConflict) || errors.Is(err, gorm.ErrDuplicatedKey) {
		return http.StatusConflict
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func deleteChannel(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.ChannelSvc.Delete(id); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func toggleChannel(c *gin.Context, d *Deps, enabled bool) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	_, err = d.ChannelSvc.Update(id, channel.UpdateInput{MonitorEnabled: &enabled})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "monitor_enabled": enabled})
}

func testLogin(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}

	obs := setupSSE(c)
	ctx := progress.WithObserver(c.Request.Context(), obs)

	if err := d.ChannelSvc.TestLogin(ctx, id); err != nil {
		progress.Fail(ctx, progress.StageError, err.Error())
		return
	}
	progress.OK(ctx, progress.StageDone, "登录测试成功")
}

func refreshBalance(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ch, err := d.Channels.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	if err := d.Monitor.RefreshBalance(c.Request.Context(), ch); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func refreshRates(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ch, err := d.Channels.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	if err := d.Monitor.RefreshRates(c.Request.Context(), ch); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func channelRates(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if _, err := d.Channels.FindByID(id); err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	list, err := d.Rates.ListByChannel(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	bindings, err := d.RelayStations.ListChannelRateBoundAccounts(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	bindingsByGroup := make(map[string][]channelRateBoundAccountView)
	for _, binding := range bindings {
		accountName := strings.TrimSpace(binding.RelayAccountName)
		if accountName == "" {
			accountName = "账号 #" + strconv.FormatInt(binding.RelayAccountExternalID, 10)
		}
		bindingsByGroup[binding.UpstreamGroup] = append(bindingsByGroup[binding.UpstreamGroup], channelRateBoundAccountView{
			RelayStationID: binding.RelayStationID, RelayStationName: binding.RelayStationName,
			RelayAccountExternalID: binding.RelayAccountExternalID, RelayAccountName: accountName,
		})
	}
	result := make([]channelRateView, 0, len(list))
	for _, rate := range list {
		result = append(result, channelRateView{
			RateSnapshot:  rate,
			BoundAccounts: bindingsByGroup[rate.ModelName],
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

type channelRateBoundAccountView struct {
	RelayStationID         uint   `json:"relay_station_id"`
	RelayStationName       string `json:"relay_station_name"`
	RelayAccountExternalID int64  `json:"relay_account_external_id"`
	RelayAccountName       string `json:"relay_account_name"`
}

type channelRateView struct {
	storage.RateSnapshot
	BoundAccounts []channelRateBoundAccountView `json:"bound_accounts"`
}

type channelRateInput struct {
	ModelName       string  `json:"model_name"`
	Description     string  `json:"description"`
	Ratio           float64 `json:"ratio"`
	CompletionRatio float64 `json:"completion_ratio"`
}

func saveChannelRate(c *gin.Context, d *Deps, id uint, rateID uint) {
	channel, err := d.Channels.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	if channel.BalanceMode != storage.BalanceModeManual {
		fail(c, http.StatusBadRequest, errors.New("只有手动管理渠道可以编辑自定义分组"))
		return
	}
	var in channelRateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	in.ModelName = strings.TrimSpace(in.ModelName)
	in.Description = strings.TrimSpace(in.Description)
	if in.ModelName == "" || len(in.ModelName) > 256 {
		fail(c, http.StatusBadRequest, errors.New("分组名称不能为空且不能超过 256 个字符"))
		return
	}
	if in.Ratio < 0 || in.CompletionRatio < 0 {
		fail(c, http.StatusBadRequest, errors.New("分组倍率必须是非负数"))
		return
	}
	now := time.Now()
	snapshot := &storage.RateSnapshot{ID: rateID, ChannelID: id, ModelName: in.ModelName, Description: in.Description, Ratio: in.Ratio, CompletionRatio: in.CompletionRatio, FirstSeenAt: now, LastSeenAt: now}
	if err := d.Rates.SaveManual(snapshot); err != nil {
		if errors.Is(err, storage.ErrRelayAccountRateReadOnly) {
			fail(c, http.StatusBadRequest, err)
		} else if errors.Is(err, gorm.ErrDuplicatedKey) {
			fail(c, http.StatusConflict, errors.New("该分组名称已存在"))
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("自定义分组不存在"))
		} else {
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	if rateID == 0 {
		list, listErr := d.Rates.ListByChannel(id)
		if listErr != nil {
			fail(c, http.StatusInternalServerError, listErr)
			return
		}
		for _, item := range list {
			if item.ModelName == in.ModelName {
				c.JSON(http.StatusOK, gin.H{"data": item})
				return
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": snapshot})
}

func createChannelRate(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	saveChannelRate(c, d, id, 0)
}

func updateChannelRate(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	rateID, err := uintParam(c, "rate_id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	saveChannelRate(c, d, id, rateID)
}

func deleteChannelRate(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	rateID, err := uintParam(c, "rate_id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	channel, err := d.Channels.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	if channel.BalanceMode != storage.BalanceModeManual {
		fail(c, http.StatusBadRequest, errors.New("只有手动管理渠道可以删除自定义分组"))
		return
	}
	rate, err := d.Rates.FindByID(rateID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("自定义分组不存在"))
		} else {
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	if rate.Source == storage.RateSnapshotSourceRelayAccount {
		fail(c, http.StatusBadRequest, storage.ErrRelayAccountRateReadOnly)
		return
	}
	if err := d.Rates.Delete(rateID, id); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func balanceHistory(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	list, err := d.Rates.BalanceHistory(id, limit)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func uintParam(c *gin.Context, name string) (uint, error) {
	id, err := strconv.ParseUint(c.Param(name), 10, 64)
	return uint(id), err
}

// setupSSE 给 ResponseWriter 设上 text/event-stream 头，返回一个就绪的 sseObserver。
// 调用方接下来一般是：
//
//	obs := setupSSE(c)
//	ctx := progress.WithObserver(c.Request.Context(), obs)
//	// ... 业务逻辑里的 progress.Start / OK / Fail 会被实时 stream 出去 ...
//	obs.Emit(progress.Event{Stage: progress.StageDone, Message: "完成"})
func setupSSE(c *gin.Context) *sseObserver {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache, no-transform")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // disable nginx-style proxy buffering
	c.Writer.WriteHeader(http.StatusOK)

	obs := &sseObserver{w: c.Writer}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		obs.flush = flusher.Flush
	}
	return obs
}

// sseObserver 把 progress.Event 序列化成 SSE 格式写入 ResponseWriter。
// 因为 gin 的 Handler 在一个 goroutine 中跑，而 emit 可能从下游同步 / 异步发起，
// 这里加锁保证 writer 串行写。
type sseObserver struct {
	mu     sync.Mutex
	w      io.Writer
	flush  func()
	closed bool
}

func (o *sseObserver) Emit(ev progress.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	// SSE: "data: <json>\n\n"
	if _, err := io.WriteString(o.w, "data: "); err != nil {
		o.closed = true
		return
	}
	if _, err := o.w.Write(payload); err != nil {
		o.closed = true
		return
	}
	if _, err := io.WriteString(o.w, "\n\n"); err != nil {
		o.closed = true
		return
	}
	if o.flush != nil {
		o.flush()
	}
}

// syncChannel 通过 SSE 把整个同步过程的子步骤实时推给前端。
//
//	GET / POST /api/channels/:id/sync
//	响应 Content-Type: text/event-stream，每条事件形如
//	  data: {"stage":"login","message":"登录上游…","time":"..."}
//
// 前端用 fetch + ReadableStream 读取，按 "\n\n" 切片解析。
func syncChannel(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ch, err := d.Channels.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}

	obs := setupSSE(c)
	ctx := progress.WithObserver(c.Request.Context(), obs)

	// 串行执行：先余额，再倍率。任一步失败仍尝试下一个，但用 done 表示整体状态。
	balErr := d.Monitor.RefreshBalance(ctx, ch)
	rateErr := d.Monitor.RefreshRates(ctx, ch)

	switch {
	case balErr != nil && rateErr != nil:
		progress.Fail(ctx, progress.StageError, balErr.Error()+" | "+rateErr.Error())
	case balErr != nil:
		progress.Fail(ctx, progress.StageError, balErr.Error())
	case rateErr != nil:
		progress.Fail(ctx, progress.StageError, rateErr.Error())
	default:
		progress.OK(ctx, progress.StageDone, "同步完成")
	}
}

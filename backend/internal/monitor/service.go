// Package monitor 周期性扫描渠道，采集余额 / 倍率并写入快照、变化日志和通知。
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/guliansheng/gateway-ops/internal/channel"
	"github.com/guliansheng/gateway-ops/internal/connector"
	"github.com/guliansheng/gateway-ops/internal/notify"
	"github.com/guliansheng/gateway-ops/internal/progress"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

// Service 监控扫描服务。
type Service struct {
	channels    *storage.Channels
	rates       *storage.Rates
	monitorLogs *storage.MonitorLogs
	channelSvc  *channel.Service
	dispatcher  *notify.Dispatcher
	relay       *relay.Service
	manualMu    sync.Mutex
	log         *slog.Logger
}

type SyncAllResult struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

func NewService(
	channels *storage.Channels,
	rates *storage.Rates,
	monitorLogs *storage.MonitorLogs,
	channelSvc *channel.Service,
	dispatcher *notify.Dispatcher,
	relaySvc *relay.Service,
	log *slog.Logger,
) *Service {
	return &Service{
		channels:    channels,
		rates:       rates,
		monitorLogs: monitorLogs,
		channelSvc:  channelSvc,
		dispatcher:  dispatcher,
		relay:       relaySvc,
		log:         log,
	}
}

// ScanAllBalances 扫描所有启用监控的渠道余额。单个失败不影响其他。
func (s *Service) ScanAllBalances(ctx context.Context) {
	list, err := s.channels.ListMonitorEnabled()
	if err != nil {
		s.log.Error("list channels", "err", err)
		return
	}
	for i := range list {
		c := list[i]
		if err := s.RefreshBalance(ctx, &c); err != nil {
			s.log.Warn("refresh balance failed", "channel", c.Name, "err", err)
		}
	}
}

// ScanAllRates 扫描所有启用监控的渠道倍率。
func (s *Service) ScanAllRates(ctx context.Context) {
	list, err := s.channels.ListMonitorEnabled()
	if err != nil {
		s.log.Error("list channels", "err", err)
		return
	}
	for i := range list {
		c := list[i]
		if err := s.RefreshRates(ctx, &c); err != nil {
			s.log.Warn("refresh rates failed", "channel", c.Name, "err", err)
		}
	}
}

// SyncAll 依次同步余额和倍率。手动“同步全部”可包含已暂停监控的渠道，
// 自动调度则传 false，遵循渠道的监控开关。
func (s *Service) SyncAll(ctx context.Context, includeDisabled bool) SyncAllResult {
	list, err := s.channels.List()
	if !includeDisabled {
		list, err = s.channels.ListMonitorEnabled()
	}
	result := SyncAllResult{Total: len(list)}
	if err != nil {
		s.log.Error("list channels for full sync", "err", err)
		result.Failed = result.Total
		return result
	}
	for i := range list {
		if ctx.Err() != nil {
			result.Failed += result.Total - result.Succeeded - result.Failed
			return result
		}
		channel := &list[i]
		balanceErr := s.RefreshBalance(ctx, channel)
		rateErr := s.RefreshRates(ctx, channel)
		if balanceErr == nil && rateErr == nil {
			result.Succeeded++
		} else {
			result.Failed++
		}
	}
	return result
}

// RefreshBalance 单个渠道余额刷新，可被 API 手动触发。
func (s *Service) RefreshBalance(ctx context.Context, c *storage.Channel) error {
	if c.BalanceMode == storage.BalanceModeManual {
		return s.refreshManualBalance(ctx, c)
	}
	accounts, err := s.channels.ListAccounts(c.ID)
	if err != nil {
		return err
	}
	if len(accounts) > 0 {
		return s.refreshAutomaticBalances(ctx, c, accounts)
	}
	return s.refreshLegacyAutomaticBalance(ctx, c)
}

func (s *Service) refreshLegacyAutomaticBalance(ctx context.Context, c *storage.Channel) error {
	balance, sampledAt, err := s.fetchAutomaticBalance(ctx, c)
	if err != nil {
		s.notifyError(ctx, c, storage.EventLoginFailed, "登录失败", err)
		return err
	}
	if err := s.channels.UpdateBalance(c.ID, balance, &sampledAt, ""); err != nil {
		return err
	}
	_ = s.rates.AppendBalance(&storage.BalanceSnapshot{
		ChannelID: c.ID,
		Balance:   balance,
		SampledAt: sampledAt,
	})
	progress.OK(ctx, progress.StageBalance, fmt.Sprintf("当前余额 %.4f", balance), map[string]any{"balance": balance})
	s.notifyAutomaticBalanceLow(ctx, c, balance)
	return nil
}

func (s *Service) refreshAutomaticBalances(ctx context.Context, c *storage.Channel, accounts []storage.ChannelAccount) error {
	var totalBalance float64
	knownBalances := 0
	failedAccounts := make([]string, 0)
	latest := time.Now()
	for _, account := range accounts {
		target := s.channelSvc.AccountChannel(c, account)
		balance, sampledAt, err := s.fetchAutomaticBalance(ctx, target)
		if err != nil {
			_ = s.channels.SetAccountError(account.ID, err.Error())
			if account.LastBalance != nil {
				totalBalance += *account.LastBalance
				knownBalances++
			}
			failedAccounts = append(failedAccounts, account.Username)
			continue
		}
		if err := s.channels.UpdateAccountBalance(account.ID, balance, &sampledAt, ""); err != nil {
			return err
		}
		totalBalance += balance
		knownBalances++
		if sampledAt.After(latest) {
			latest = sampledAt
		}
	}
	if knownBalances == 0 {
		err := fmt.Errorf("所有账号余额采集失败：%s", strings.Join(failedAccounts, "、"))
		_ = s.channels.SetLastError(c.ID, err.Error())
		progress.Fail(ctx, progress.StageBalance, "所有账号余额采集失败")
		s.notifyError(ctx, c, storage.EventMonitorFailed, "余额采集失败", err)
		return err
	}

	lastErr := ""
	if len(failedAccounts) > 0 {
		lastErr = fmt.Sprintf("%d 个账号余额采集失败：%s", len(failedAccounts), strings.Join(failedAccounts, "、"))
	}
	if err := s.channels.UpdateBalance(c.ID, totalBalance, latest, lastErr); err != nil {
		return err
	}
	if s.rates != nil {
		if err := s.rates.AppendBalance(&storage.BalanceSnapshot{ChannelID: c.ID, Balance: totalBalance, SampledAt: latest}); err != nil {
			return err
		}
	}
	if len(failedAccounts) > 0 {
		err := errors.New(lastErr)
		progress.Fail(ctx, progress.StageBalance, lastErr)
		s.notifyError(ctx, c, storage.EventMonitorFailed, "余额采集失败", err)
		return err
	}
	progress.OK(ctx, progress.StageBalance, fmt.Sprintf("当前余额 %.4f（%d 个账号）", totalBalance, len(accounts)), map[string]any{"balance": totalBalance, "account_count": len(accounts)})
	s.notifyAutomaticBalanceLow(ctx, c, totalBalance)
	return nil
}

func (s *Service) fetchAutomaticBalance(ctx context.Context, c *storage.Channel) (float64, time.Time, error) {
	resolved, conn, session, err := s.prepare(ctx, c)
	if err != nil {
		return 0, time.Time{}, err
	}

	progress.Start(ctx, progress.StageBalance, "拉取余额…")
	started := time.Now()
	res, err := conn.GetBalance(ctx, resolved, session)
	finished := time.Now()
	_ = s.monitorLogs.Append(&storage.MonitorLog{
		ChannelID:    c.ID,
		Job:          storage.MonitorJobBalance,
		Success:      err == nil,
		ErrorMessage: errString(err),
		StartedAt:    started,
		FinishedAt:   finished,
	})
	if err != nil {
		progress.Fail(ctx, progress.StageBalance, err.Error())
		return 0, time.Time{}, err
	}

	sampledAt := res.SampledAt
	if sampledAt.IsZero() {
		sampledAt = time.Now()
	}
	return res.Balance, sampledAt, nil
}

func (s *Service) notifyAutomaticBalanceLow(ctx context.Context, c *storage.Channel, balance float64) {
	if c.BalanceThreshold > 0 && balance < c.BalanceThreshold {
		body := fmt.Sprintf("当前余额：%.4f\n告警阈值：%.4f\n建议及时检查该渠道余额。", balance, c.BalanceThreshold)
		_ = s.dispatcher.Dispatch(ctx, notify.Message{
			Event:     storage.EventBalanceLow,
			ChannelID: c.ID,
			Subject:   fmt.Sprintf("[GatewayOps] %s 余额低于阈值", c.Name),
			Body:      body,
		})
	}
}

func (s *Service) refreshManualBalance(ctx context.Context, c *storage.Channel) error {
	s.manualMu.Lock()
	defer s.manualMu.Unlock()

	progress.Start(ctx, progress.StageBalance, "读取手动余额…")
	// The caller may have loaded the channel before another refresh completed.
	// Reload inside the settlement lock so the latest baseline is always used.
	current, err := s.channels.FindByID(c.ID)
	if err != nil {
		return err
	}
	c = current
	balance := c.ManualBalance
	if c.LastBalance != nil {
		balance = *c.LastBalance
	}
	observedBalance := balance
	if s.relay == nil {
		progress.OK(ctx, progress.StageBalance, fmt.Sprintf("当前余额 %.4f（手动管理）", balance), map[string]any{"balance": balance, "manual": true})
		s.notifyManualBalanceLow(ctx, c, balance)
		return nil
	}

	// The upstream endpoint returns a lifetime cumulative value even when a
	// later start timestamp is supplied. Read the true cumulative total and
	// settle only the change from our persisted baseline.
	usage, err := s.relay.ChannelUsageTotals(ctx, []storage.Channel{*c}, "all", time.Time{})
	if err != nil {
		progress.Fail(ctx, progress.StageBalance, "手动余额消费统计失败")
		return fmt.Errorf("读取手动余额消费失败: %w", err)
	}
	total, ok := usage[c.ID]
	// Without a complete, bound account set we cannot safely advance the
	// settlement cursor: doing so would permanently lose later consumption.
	if !ok || total.MatchedAccountCount == 0 {
		progress.OK(ctx, progress.StageBalance, fmt.Sprintf("当前余额 %.4f（等待账号归属）", balance), map[string]any{"balance": balance, "manual": true, "account_bound": false})
		s.notifyManualBalanceLow(ctx, c, balance)
		return nil
	}
	if !total.Complete {
		progress.Fail(ctx, progress.StageBalance, "手动余额消费统计不完整")
		return fmt.Errorf("手动余额消费统计不完整，暂不扣减余额")
	}
	balance, baseline, basis, deducted, initialized := applyManualUsage(balance, c.ManualUsageBaseline, c.ManualUsageBasis, total.Cost, total.CostBasis)
	now := time.Now()
	settledBalance, err := s.channels.SettleManualBalance(c.ID, observedBalance, balance, now, "", baseline, basis)
	if err != nil {
		return err
	}
	balance = settledBalance
	if s.rates != nil {
		if err := s.rates.AppendBalance(&storage.BalanceSnapshot{ChannelID: c.ID, Balance: balance, SampledAt: now}); err != nil {
			return err
		}
	}
	message := fmt.Sprintf("当前余额 %.4f（已扣除消费 %.4f）", balance, deducted)
	if initialized {
		message = fmt.Sprintf("当前余额 %.4f（已建立消费起点）", balance)
	}
	progress.OK(ctx, progress.StageBalance, message, map[string]any{"balance": balance, "manual": true, "consumption": deducted, "account_bound": true})
	s.notifyManualBalanceLow(ctx, c, balance)
	return nil
}

// applyManualUsage derives a one-time deduction from a cumulative relay cost.
// A new or invalid baseline starts a fresh accounting period; a relay reset
// (cumulative cost falling) similarly advances the baseline without charging
// the balance a negative amount.
func applyManualUsage(balance float64, baseline *float64, settledBasis string, cumulativeCost float64, currentBasis string) (nextBalance float64, nextBaseline *float64, nextBasis string, deducted float64, initialized bool) {
	nextBalance = balance
	nextBasis = settledBasis
	if !validManualUsage(cumulativeCost) || strings.TrimSpace(currentBasis) == "" {
		return nextBalance, baseline, nextBasis, 0, false
	}
	next := cumulativeCost
	nextBaseline = &next
	nextBasis = currentBasis
	if baseline == nil || !validManualUsage(*baseline) || settledBasis == "" || settledBasis != currentBasis {
		return nextBalance, nextBaseline, nextBasis, 0, true
	}
	deducted = cumulativeCost - *baseline
	if !validManualUsage(deducted) || deducted < 0 {
		return nextBalance, nextBaseline, nextBasis, 0, false
	}
	nextBalance -= deducted
	if nextBalance < 0 || math.IsNaN(nextBalance) || math.IsInf(nextBalance, 0) {
		nextBalance = 0
	}
	return nextBalance, nextBaseline, nextBasis, deducted, false
}

func validManualUsage(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (s *Service) notifyManualBalanceLow(ctx context.Context, c *storage.Channel, balance float64) {
	if c.BalanceThreshold > 0 && balance < c.BalanceThreshold {
		body := fmt.Sprintf("当前余额：%.4f\n告警阈值：%.4f\n建议及时检查该渠道余额。", balance, c.BalanceThreshold)
		_ = s.dispatcher.Dispatch(ctx, notify.Message{
			Event: storage.EventBalanceLow, ChannelID: c.ID,
			Subject: fmt.Sprintf("[GatewayOps] %s 余额低于阈值", c.Name), Body: body,
		})
	}
}

// RefreshRates 单个渠道倍率刷新，可被 API 手动触发。
func (s *Service) RefreshRates(ctx context.Context, c *storage.Channel) error {
	if c.BalanceMode == storage.BalanceModeManual {
		progress.OK(ctx, progress.StageRates, "手动余额渠道不读取分组", map[string]any{"manual": true})
		return nil
	}
	resolved, conn, session, err := s.prepare(ctx, c)
	if err != nil {
		s.notifyError(ctx, c, storage.EventLoginFailed, "登录失败", err)
		return err
	}

	progress.Start(ctx, progress.StageRates, "拉取分组倍率…")
	started := time.Now()
	results, err := conn.GetRates(ctx, resolved, session)
	finished := time.Now()
	_ = s.monitorLogs.Append(&storage.MonitorLog{
		ChannelID:    c.ID,
		Job:          storage.MonitorJobRates,
		Success:      err == nil,
		ErrorMessage: errString(err),
		StartedAt:    started,
		FinishedAt:   finished,
	})
	if err != nil {
		progress.Fail(ctx, progress.StageRates, err.Error())
		s.notifyError(ctx, c, storage.EventMonitorFailed, "倍率采集失败", err)
		return err
	}

	now := time.Now()
	changes := make([]notify.RateChange, 0, len(results))
	seenNames := make([]string, 0, len(results))
	upsertFailed := false
	for _, r := range results {
		prev, err := s.rates.Upsert(&storage.RateSnapshot{
			ChannelID:       c.ID,
			ModelName:       r.ModelName,
			Description:     r.Description,
			Ratio:           r.Ratio,
			CompletionRatio: r.CompletionRatio,
			Source:          storage.RateSnapshotSourceUpstream,
			LastSeenAt:      now,
		})
		if err != nil {
			upsertFailed = true
			s.log.Warn("rate upsert failed", "channel", c.Name, "model", r.ModelName, "err", err)
			continue
		}
		seenNames = append(seenNames, r.ModelName)
		if prev == nil {
			continue
		}
		if prev.Ratio == r.Ratio && prev.CompletionRatio == r.CompletionRatio {
			continue
		}
		oldRatio := prev.Ratio
		oldComp := prev.CompletionRatio
		changes = append(changes, notify.RateChange{
			GroupName: r.ModelName,
			OldRatio:  oldRatio,
			NewRatio:  r.Ratio,
			OldComp:   oldComp,
			NewComp:   r.CompletionRatio,
			ChangedAt: now,
		})
	}
	// A successful scan is a snapshot of the upstream's current visible groups.
	// Remove rows from older scans so the UI does not count retired groups as
	// current. If any upsert failed, keep the old rows and retry cleanup next scan.
	if !upsertFailed {
		if removed, err := s.rates.DeleteMissing(c.ID, seenNames); err != nil {
			s.log.Warn("remove stale rate snapshots failed", "channel", c.Name, "err", err)
		} else if removed > 0 {
			s.log.Info("removed stale rate snapshots", "channel", c.Name, "count", removed)
		}
	}
	// 一次扫描的所有变化打包推送：去抖策略（合并 / 涨跌幅过滤）由 Dispatcher.Policy 决定。
	if len(changes) > 0 {
		_ = s.dispatcher.DispatchRateBatch(ctx, c, changes)
	}
	progress.OK(ctx, progress.StageRates, fmt.Sprintf("拉到 %d 个分组", len(results)),
		map[string]any{"count": len(results)})
	return nil
}

func (s *Service) prepare(ctx context.Context, c *storage.Channel) (*connector.Channel, connector.Connector, *connector.AuthSession, error) {
	resolved, err := s.channelSvc.Resolve(ctx, c)
	if err != nil {
		return nil, nil, nil, err
	}
	conn, err := connector.For(resolved.Type)
	if err != nil {
		return nil, nil, nil, err
	}
	session, err := s.channelSvc.EnsureSession(ctx, c, resolved, conn)
	if err != nil {
		return nil, nil, nil, err
	}
	return resolved, conn, session, nil
}

func (s *Service) notifyError(ctx context.Context, c *storage.Channel, event storage.NotificationEvent, subject string, err error) {
	_ = s.dispatcher.Dispatch(ctx, notify.Message{
		Event:     event,
		ChannelID: c.ID,
		Subject:   fmt.Sprintf("[GatewayOps] %s %s", c.Name, subject),
		Body:      err.Error(),
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

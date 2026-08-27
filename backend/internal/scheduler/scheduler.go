// Package scheduler 用 robfig/cron 触发周期性扫描。
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/guliansheng/gateway-ops/internal/config"
	"github.com/guliansheng/gateway-ops/internal/monitor"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

type Scheduler struct {
	cfg                  config.SchedulerConfig
	log                  *slog.Logger
	cron                 *cron.Cron
	monitor              *monitor.Service
	monLogs              *storage.MonitorLogs
	rates                *storage.Rates
	notifies             *storage.Notifications
	settings             *storage.Settings
	relay                *relay.Service
	legacyBalanceID      cron.EntryID
	legacyRateID         cron.EntryID
	channelSyncID        cron.EntryID
	relayRateSyncID      cron.EntryID
	relaySnapshotSyncID  cron.EntryID
	dailyBalanceID       cron.EntryID
	jobsMu               sync.Mutex
	channelSyncMu        sync.Mutex
	relaySyncMu          sync.Mutex
	relayRateRunning     atomic.Bool
	relaySnapshotRunning atomic.Bool
}

func New(
	cfg config.SchedulerConfig,
	m *monitor.Service,
	monLogs *storage.MonitorLogs,
	rates *storage.Rates,
	notifies *storage.Notifications,
	settings *storage.Settings,
	relaySvc *relay.Service,
	log *slog.Logger,
) *Scheduler {
	return &Scheduler{
		cfg:      cfg,
		log:      log,
		cron:     cron.New(cron.WithSeconds()),
		monitor:  m,
		monLogs:  monLogs,
		rates:    rates,
		notifies: notifies,
		settings: settings,
		relay:    relaySvc,
	}
}

func (s *Scheduler) Start() error {
	// Recover today's row before cron starts. ON CONFLICT keeps this harmless
	// when the midnight job already ran.
	if rows, err := s.rates.CaptureDailyBalances(time.Now()); err != nil {
		s.log.Warn("capture startup daily channel balances failed", "err", err)
	} else if rows > 0 {
		s.log.Info("captured startup daily channel balances", "rows", rows)
	}
	entryID, err := s.cron.AddFunc("0 0 0 * * *", s.runDailyBalances)
	if err != nil {
		return err
	}
	s.dailyBalanceID = entryID
	if s.cfg.BalanceCron != "" {
		entryID, err := s.cron.AddFunc(s.cfg.BalanceCron, s.runBalance)
		if err != nil {
			return err
		}
		s.legacyBalanceID = entryID
	}
	if s.cfg.RateCron != "" {
		entryID, err := s.cron.AddFunc(s.cfg.RateCron, s.runRates)
		if err != nil {
			return err
		}
		s.legacyRateID = entryID
	}
	if s.cfg.Retention.Cron != "" && s.hasRetention() {
		if _, err := s.cron.AddFunc(s.cfg.Retention.Cron, s.runRetention); err != nil {
			return err
		}
	}
	if err := s.configureSyncJobs(); err != nil {
		return err
	}
	s.cron.Start()
	s.log.Info("scheduler started",
		"balanceCron", s.cfg.BalanceCron,
		"rateCron", s.cfg.RateCron,
		"retentionCron", s.cfg.Retention.Cron,
		"dailyBalanceCron", "0 0 0 * * *",
		"concurrency", s.cfg.Concurrency,
	)
	return nil
}

func (s *Scheduler) runDailyBalances() {
	rows, err := s.rates.CaptureDailyBalances(time.Now())
	if err != nil {
		s.log.Warn("capture daily channel balances failed", "err", err)
		return
	}
	s.log.Info("captured daily channel balances", "rows", rows)
}

func (s *Scheduler) SyncSettings() (storage.SyncSettings, error) {
	return s.settings.SyncSettings()
}

func (s *Scheduler) UpdateSyncSettings(settings storage.SyncSettings) error {
	if err := s.settings.SaveSyncSettings(settings); err != nil {
		return err
	}
	return s.configureSyncJobs()
}

func (s *Scheduler) configureSyncJobs() error {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()

	settings, err := s.settings.SyncSettings()
	if err != nil {
		return err
	}
	if settings.ChannelConfigured {
		if s.legacyBalanceID != 0 {
			s.cron.Remove(s.legacyBalanceID)
			s.legacyBalanceID = 0
		}
		if s.legacyRateID != 0 {
			s.cron.Remove(s.legacyRateID)
			s.legacyRateID = 0
		}
		if s.channelSyncID != 0 {
			s.cron.Remove(s.channelSyncID)
			s.channelSyncID = 0
		}
		if settings.ChannelEnabled {
			s.channelSyncID, err = s.cron.AddFunc(fmt.Sprintf("@every %dm", settings.ChannelIntervalMinutes), s.runChannelSync)
			if err != nil {
				return err
			}
		}
	}
	if s.relayRateSyncID != 0 {
		s.cron.Remove(s.relayRateSyncID)
		s.relayRateSyncID = 0
	}
	if s.relaySnapshotSyncID != 0 {
		s.cron.Remove(s.relaySnapshotSyncID)
		s.relaySnapshotSyncID = 0
	}
	if settings.RelaySnapshotConfigured && settings.RelaySnapshotEnabled {
		// The snapshot job is registered first so short intervals start promptly.
		// Per-job running flags prevent queued invocations while the shared relay
		// mutex serializes a snapshot refresh with a rate probe.
		s.relaySnapshotSyncID, err = s.cron.AddFunc(fmt.Sprintf("@every %ds", settings.RelaySnapshotIntervalSeconds), s.runRelaySnapshotSync)
		if err != nil {
			return err
		}
	}
	if settings.RelayRateConfigured && settings.RelayRateEnabled {
		s.relayRateSyncID, err = s.cron.AddFunc(fmt.Sprintf("@every %dm", settings.RelayRateIntervalMinutes), s.runRelayRateSync)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) Stop() {
	if s.cron != nil {
		<-s.cron.Stop().Done()
	}
}

func (s *Scheduler) runBalance() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s.monitor.ScanAllBalances(ctx)
}

func (s *Scheduler) runRates() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s.monitor.ScanAllRates(ctx)
}

func (s *Scheduler) runChannelSync() {
	if !s.channelSyncMu.TryLock() {
		s.log.Warn("skip overlapping channel sync")
		return
	}
	defer s.channelSyncMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	s.monitor.SyncAll(ctx, false)
}

func (s *Scheduler) runRelayRateSync() {
	if !s.relayRateRunning.CompareAndSwap(false, true) {
		s.log.Warn("skip overlapping relay rate sync")
		return
	}
	defer s.relayRateRunning.Store(false)
	s.relaySyncMu.Lock()
	defer s.relaySyncMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, _, err := s.relay.SyncAllRates(ctx); err != nil {
		s.log.Warn("sync relay station rates failed", "err", err)
	}
}

func (s *Scheduler) runRelaySnapshotSync() {
	if !s.relaySnapshotRunning.CompareAndSwap(false, true) {
		s.log.Warn("skip overlapping relay snapshot sync")
		return
	}
	defer s.relaySnapshotRunning.Store(false)
	s.relaySyncMu.Lock()
	defer s.relaySyncMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, _, err := s.relay.SyncAllSnapshots(ctx); err != nil {
		s.log.Warn("sync relay station snapshots failed", "err", err)
	}
}

func (s *Scheduler) hasRetention() bool {
	r := s.cfg.Retention
	return r.MonitorLogsDays > 0 || r.BalanceSnapshotsDays > 0 || r.NotificationLogsDays > 0
}

// runRetention 按配置删除过期历史。任一表失败不影响其它，全部错误写日志。
func (s *Scheduler) runRetention() {
	r := s.cfg.Retention
	now := time.Now()

	if r.MonitorLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.MonitorLogsDays)
		n, err := s.monLogs.DeleteBefore(cutoff)
		if err != nil {
			s.log.Warn("retention monitor_logs failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention monitor_logs deleted", "rows", n, "before", cutoff)
		}
	}

	if r.BalanceSnapshotsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.BalanceSnapshotsDays)
		n, err := s.rates.DeleteBalanceSnapshotsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention balance_snapshots failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention balance_snapshots deleted", "rows", n, "before", cutoff)
		}
	}

	if r.NotificationLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.NotificationLogsDays)
		n, err := s.notifies.DeleteLogsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention notification_logs failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention notification_logs deleted", "rows", n, "before", cutoff)
		}
	}
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/api"
	"github.com/guliansheng/gateway-ops/internal/auth"
	"github.com/guliansheng/gateway-ops/internal/channel"
	"github.com/guliansheng/gateway-ops/internal/config"
	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/logger"
	"github.com/guliansheng/gateway-ops/internal/monitor"
	"github.com/guliansheng/gateway-ops/internal/notify"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/scheduler"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"github.com/guliansheng/gateway-ops/web"

	// 注册 connector 实现。
	_ "github.com/guliansheng/gateway-ops/internal/connector/newapi"
	_ "github.com/guliansheng/gateway-ops/internal/connector/sub2api"
)

func main() {
	configPath := flag.String("config", "", "path to config.yaml (optional; env vars also supported)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Log.Level, cfg.Log.Format)
	log.Info("starting GatewayOps", "port", cfg.Server.Port, "mode", cfg.Server.Mode)

	cipher, err := crypto.NewCipher(cfg.Security.AppSecret)
	if err != nil {
		log.Error("init cipher failed (set APP_SECRET)", "err", err)
		os.Exit(1)
	}

	// Auth：默认禁用（AUTH_ENABLED=false），所有 /api/* 免 token；
	// 显式开启时账号/密码必填，token secret 缺省回退到 AppSecret。
	//
	// 登录失败限流（guard）在当前进程内计数：同一来源 IP / 账号连续失败会临时锁定，
	// 锁定时通过通知渠道告警。dispatcher 在下面才构造，这里用闭包延后取值。
	var dispatcher *notify.Dispatcher
	var authSvc *auth.Service
	if cfg.Auth.Enabled {
		tokenSecret := cfg.Auth.TokenSecret
		if tokenSecret == "" {
			tokenSecret = cfg.Security.AppSecret
		}
		guardCfg := cfg.Auth.ToGuardConfig()
		guardCfg.OnLockout = func(ev auth.LockoutEvent) {
			log.Warn("admin login locked out",
				"scope", ev.Scope, "key", ev.Key, "failures", ev.Failures,
				"lock_seconds", int(ev.LockDuration.Seconds()))
			if dispatcher == nil {
				return
			}
			// 通知发送可能阻塞，放到 goroutine 里，不拖慢登录响应。
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = dispatcher.Dispatch(ctx, notify.Message{
					Event:   storage.EventAdminLoginBlocked,
					Subject: "[GatewayOps] 后台登录被限流锁定",
					Body: fmt.Sprintf("连续 %d 次后台登录失败，已临时锁定。\n触发维度：%s\n触发值：%s\n锁定时长：%s",
						ev.Failures, ev.Scope, ev.Key, ev.LockDuration),
				})
			}()
		}
		guard := auth.NewGuard(guardCfg)
		authSvc, err = auth.New(
			cfg.Auth.Username,
			cfg.Auth.Password,
			tokenSecret,
			time.Duration(cfg.Auth.SessionTTLHours)*time.Hour,
			guard,
		)
		if err != nil {
			log.Error("init auth failed (set ADMIN_USERNAME / ADMIN_PASSWORD or AUTH_ENABLED=false)", "err", err)
			os.Exit(1)
		}
		log.Info("auth enabled", "username", cfg.Auth.Username)
		// 公网暴露的后台只有一个管理员账号，密码强度就是最后一道防线：
		// 这里只告警不拦截，避免升级后因为旧密码过短直接起不来。
		if len(cfg.Auth.Password) < 16 {
			log.Warn("admin password is shorter than 16 characters; use a long random password for public exposure",
				"length", len(cfg.Auth.Password))
		}
		if cfg.Auth.TokenSecret == "" {
			log.Warn("AUTH_TOKEN_SECRET is empty, falling back to APP_SECRET; set a dedicated random secret")
		}
	} else {
		log.Warn("auth disabled — all /api/* endpoints are open; set AUTH_ENABLED=true for production exposure")
	}

	db, err := storage.Open(cfg.Database.ToStorageConfig())
	if err != nil {
		log.Error("open database failed", "err", err)
		os.Exit(1)
	}
	if err := storage.AutoMigrate(db); err != nil {
		log.Error("auto migrate failed", "err", err)
		os.Exit(1)
	}

	channels := storage.NewChannels(db)
	authSessions := storage.NewAuthSessions(db)
	captchas := storage.NewCaptchas(db)
	notifies := storage.NewNotifications(db)
	rates := storage.NewRates(db)
	monLogs := storage.NewMonitorLogs(db)
	settings := storage.NewSettings(db)
	relayStations := storage.NewRelayStations(db)
	operations := storage.NewOperations(db)
	helpDocuments := storage.NewHelpDocuments(db)

	channelSvc := channel.NewService(channels, authSessions, captchas, monLogs, cipher)
	dispatcher = notify.NewDispatcher(notifies, cipher, log, notify.Policy{
		BatchRateChanges:   cfg.Notifications.BatchRateChanges,
		MinChangePct:       cfg.Notifications.MinChangePct,
		BalanceLowCooldown: time.Duration(cfg.Notifications.BalanceLowCooldownMinutes) * time.Minute,
		SendMaxAttempts:    cfg.Notifications.SendMaxAttempts,
	})
	relaySvc := relay.NewService(relayStations, cipher, channels, rates)
	monitorSvc := monitor.NewService(channels, rates, monLogs, channelSvc, dispatcher, relaySvc, log)

	sch := scheduler.New(cfg.Scheduler, monitorSvc, monLogs, rates, notifies, settings, relaySvc, log)
	if err := sch.Start(); err != nil {
		log.Error("start scheduler failed", "err", err)
		os.Exit(1)
	}
	defer sch.Stop()

	gin.SetMode(cfg.Server.Mode)
	router := gin.New()
	router.Use(gin.Recovery())
	// 信任代理决定 Gin 从哪里取客户端 IP：登录限流按 IP 计数依赖它。
	// 默认只信任回环与私有网段，避免外部直接伪造 X-Forwarded-For 绕过限流。
	if len(cfg.Server.TrustedProxies) > 0 {
		if err := router.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
			log.Error("set trusted proxies failed", "err", err)
			os.Exit(1)
		}
		log.Info("trusted proxies configured", "count", len(cfg.Server.TrustedProxies))
	} else {
		log.Warn("no trusted proxies configured; client IP will be the direct peer address")
	}

	// 仅在嵌入了真实前端产物时挂载静态 handler。
	// 本地 `go run` 跑出来的二进制 dist 是空占位，此时由 vite dev server 接管 :3010。
	var frontendFS fs.FS
	if web.HasFrontend() {
		frontendFS = web.DistFS()
		log.Info("frontend embedded, serving SPA on /")
	} else {
		log.Info("no embedded frontend, run vite dev server separately for UI")
	}

	api.Register(router, &api.Deps{
		DB:            db,
		Cipher:        cipher,
		Auth:          authSvc,
		Channels:      channels,
		Sessions:      authSessions,
		Captchas:      captchas,
		Notifies:      notifies,
		Rates:         rates,
		MonLogs:       monLogs,
		ChannelSvc:    channelSvc,
		Monitor:       monitorSvc,
		Relay:         relaySvc,
		RelayStations: relayStations,
		Operations:    operations,
		Help:          helpDocuments,
		Scheduler:     sch,
		Dispatcher:    dispatcher,
		Log:           log,
		Frontend:      frontendFS,
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "err", err)
			os.Exit(1)
		}
	}()
	log.Info("http server listening", "addr", srv.Addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Info("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("http shutdown error", "err", err)
	}
	log.Info("bye")
}

// Package api 注册所有 HTTP 路由，组装各业务 handler。
//
// 单用户场景下走 HMAC token 鉴权：账号密码写在 config 里，登录后下发 token。
package api

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/auth"
	"github.com/guliansheng/gateway-ops/internal/channel"
	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/monitor"
	"github.com/guliansheng/gateway-ops/internal/notify"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/scheduler"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"gorm.io/gorm"
)

// Deps 把所有 handler 需要的依赖打包传入。
type Deps struct {
	DB            *gorm.DB
	Cipher        *crypto.Cipher
	Auth          *auth.Service
	Channels      *storage.Channels
	Sessions      *storage.AuthSessions
	Captchas      *storage.Captchas
	Notifies      *storage.Notifications
	Rates         *storage.Rates
	MonLogs       *storage.MonitorLogs
	ChannelSvc    *channel.Service
	Monitor       *monitor.Service
	Relay         *relay.Service
	RelayStations *storage.RelayStations
	Operations    *storage.Operations
	Scheduler     *scheduler.Scheduler
	Dispatcher    *notify.Dispatcher
	Log           *slog.Logger

	// Frontend 可选：传入嵌入的前端 dist 文件系统。nil 表示不挂载（本地开发用 vite dev server）。
	Frontend fs.FS
}

// Register 把所有路由挂到给定 gin engine。
func Register(r *gin.Engine, d *Deps) {
	r.GET("/healthz", func(c *gin.Context) {
		sqlDB, err := d.DB.DB()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "down", "err": err.Error()})
			return
		}
		if err := sqlDB.Ping(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db_down", "err": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	publicAPI := r.Group("/api/public")
	registerPublicRelayStations(publicAPI, d)
	registerPublicModelPricing(publicAPI, d)

	api := r.Group("/api")
	if d.Auth != nil {
		api.Use(d.Auth.Middleware())
	}
	{
		api.GET("/version", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"name": "GatewayOps", "version": "0.1.0-dev"})
		})

		registerAuth(api, d)
		registerChannels(api, d)
		registerCaptchas(api, d)
		registerNotifications(api, d)
		registerRates(api, d)
		registerMonitorLogs(api, d)
		registerDashboard(api, d)
		registerRelayStations(api, d)
		registerSyncSettings(api, d)
		registerOperations(api, d)
	}

	if d.Frontend != nil {
		registerFrontend(r, d.Frontend)
	}
}

// registerFrontend 把嵌入的前端 dist 挂在根路径，并处理 SPA fallback：
//
//   - GET /assets/*  → 直接返回文件
//   - GET /          → 返回 index.html
//   - GET /channels  → 返回 index.html（React Router 客户端路由）
//
// /api/*、/healthz 都已被前面的具体路由占了，不会走到这里。
// 安全起见仍然做一次前缀拦截，避免任何意外情况下"未鉴权读 index.html"压到 /api 上。
func registerFrontend(r *gin.Engine, dist fs.FS) {
	fileServer := http.FileServer(http.FS(dist))

	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// 永远不让 SPA fallback 覆盖 API / 健康检查路径。
		if strings.HasPrefix(path, "/api/") || path == "/healthz" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		// 文件存在就直接 serve，否则回落到 index.html。
		clean := strings.TrimPrefix(path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if _, err := fs.Stat(dist, clean); err != nil {
			c.Request.URL.Path = "/"
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

// fail 统一错误响应。
func fail(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"error": err.Error()})
}

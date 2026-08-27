package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

type syncSettingsInput struct {
	ChannelEnabled               bool `json:"channel_enabled"`
	ChannelIntervalMinutes       int  `json:"channel_interval_minutes"`
	RelayRateEnabled             bool `json:"relay_rate_enabled"`
	RelayRateIntervalMinutes     int  `json:"relay_rate_interval_minutes"`
	RelaySnapshotEnabled         bool `json:"relay_snapshot_enabled"`
	RelaySnapshotIntervalMinutes int  `json:"relay_snapshot_interval_minutes"`
	RelaySnapshotIntervalSeconds int  `json:"relay_snapshot_interval_seconds"`
}

func registerSyncSettings(g *gin.RouterGroup, d *Deps) {
	g.GET("/sync-settings", func(c *gin.Context) {
		settings, err := d.Scheduler.SyncSettings()
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": settings})
	})
	g.PUT("/sync-settings", func(c *gin.Context) {
		var in syncSettingsInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		settings := storage.SyncSettings{
			ChannelEnabled: in.ChannelEnabled, ChannelIntervalMinutes: in.ChannelIntervalMinutes,
			RelayRateEnabled: in.RelayRateEnabled, RelayRateIntervalMinutes: in.RelayRateIntervalMinutes,
			RelaySnapshotEnabled: in.RelaySnapshotEnabled, RelaySnapshotIntervalMinutes: in.RelaySnapshotIntervalMinutes,
			RelaySnapshotIntervalSeconds: in.RelaySnapshotIntervalSeconds,
		}
		if err := d.Scheduler.UpdateSyncSettings(settings); err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		saved, err := d.Scheduler.SyncSettings()
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": saved})
	})
}

package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"gorm.io/gorm"
)

func registerPublicRelayStations(g *gin.RouterGroup, d *Deps) {
	g.GET("/relay-stations/:id/group-monitor", func(c *gin.Context) {
		publicRelayGroupMonitor(c, d)
	})
}

func publicRelayGroupMonitor(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil || stationID == 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站 ID"))
		return
	}
	rawRange := c.DefaultQuery("range", "90m")
	monitorRange, ok := relay.NormalizePublicMonitorRange(rawRange)
	if !ok {
		fail(c, http.StatusBadRequest, errors.New("时间范围仅支持 90m、24h、7d 或 30d"))
		return
	}
	view, err := d.Relay.PublicGroupMonitorForRange(c.Request.Context(), stationID, monitorRange)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("中转站不存在"))
			return
		}
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

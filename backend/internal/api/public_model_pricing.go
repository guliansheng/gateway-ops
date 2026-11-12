package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func failPublicModelPricing(c *gin.Context, logger *slog.Logger, stationID uint, err error) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error("public model pricing unavailable", "station_id", stationID, "err", err)
	fail(c, http.StatusBadGateway, errors.New("模型价格信息暂时无法获取"))
}

func registerPublicModelPricing(g *gin.RouterGroup, d *Deps) {
	g.GET("/relay-stations/:id/model-pricing", func(c *gin.Context) {
		stationID, err := uintParam(c, "id")
		if err != nil || stationID == 0 {
			fail(c, http.StatusBadRequest, errors.New("无效的中转站 ID"))
			return
		}
		view, err := d.Relay.PublicModelPricing(c.Request.Context(), stationID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				fail(c, http.StatusNotFound, errors.New("中转站不存在"))
				return
			}
			failPublicModelPricing(c, d.Log, stationID, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": view})
	})
}

package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

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
			fail(c, http.StatusBadGateway, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": view})
	})
}

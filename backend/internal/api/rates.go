package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func registerRates(g *gin.RouterGroup, d *Deps) {
	g.GET("/rate-changes", func(c *gin.Context) {
		var channelID uint
		if s := c.Query("channel_id"); s != "" {
			id, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				fail(c, http.StatusBadRequest, err)
				return
			}
			channelID = uint(id)
		}
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		ratioOnly, err := strconv.ParseBool(c.DefaultQuery("ratio_only", "false"))
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		latestPerGroup, err := strconv.ParseBool(c.DefaultQuery("latest_per_group", "false"))
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		var list []storage.RateChangeLog
		if latestPerGroup {
			list, err = d.Rates.ListLatestRatioChanges(channelID)
		} else {
			list, err = d.Rates.ListChanges(channelID, limit, ratioOnly)
		}
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": list})
	})
}

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPublicMonitorRejectsUnsupportedTimeRangesBeforeQuerying(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerPublicRelayStations(router.Group("/api/public"), &Deps{})
	for _, query := range []string{"12h", "all", "", "0", "90m%26range=30d"} {
		t.Run(query, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/public/relay-stations/1/group-monitor?range="+query, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("HTTP %d, want 400; response = %s", response.Code, response.Body.String())
			}
		})
	}
}

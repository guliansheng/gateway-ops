package api

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// 注册阶段就要能发现路由冲突（静态段与 :external_id 参数段同级），否则线上
// 启动会直接 panic。这里同时锁住两个新增入口。
func TestRegisterExposesUserChargeRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Register(engine, &Deps{})

	want := map[string]bool{
		"PUT /api/relay-stations/:id/accounts/user-charge-excluded": false,
		"GET /api/dashboard/unlinked-accounts":                      false,
		"PUT /api/relay-stations/:id/dynamic-monitor":               false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, tracked := want[key]; tracked {
			want[key] = true
		}
	}
	for key, registered := range want {
		if !registered {
			t.Errorf("route %s 未注册", key)
		}
	}
}

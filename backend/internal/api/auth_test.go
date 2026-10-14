package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/auth"
)

// newAuthTestEngine 注册带鉴权的路由，返回引擎与登录用的真实密码。
func newAuthTestEngine(t *testing.T, guard *auth.Guard) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc, err := auth.New("admin", "correct-horse-battery", "0123456789abcdef0123456789abcdef", time.Hour, guard)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	engine := gin.New()
	Register(engine, &Deps{Auth: svc, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return engine
}

func postLogin(engine *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestLoginSucceedsAndReturnsToken(t *testing.T) {
	engine := newAuthTestEngine(t, auth.NewDefaultGuard())
	rec := postLogin(engine, `{"username":"admin","password":"correct-horse-battery"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if out.Data.Token == "" {
		t.Fatal("响应应包含 token")
	}
}

func TestLoginRateLimitedReturns429(t *testing.T) {
	guard := auth.NewGuard(auth.GuardConfig{
		Window:                time.Minute,
		MaxFailuresPerIP:      3,
		MaxFailuresPerAccount: 100,
		BaseLock:              time.Minute,
		MaxLock:               time.Hour,
	})
	engine := newAuthTestEngine(t, guard)

	for i := 0; i < 3; i++ {
		if rec := postLogin(engine, `{"username":"admin","password":"nope"}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败应为 401，实际 %d", i+1, rec.Code)
		}
	}
	rec := postLogin(engine, `{"username":"admin","password":"nope"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("达到阈值后应为 429，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 响应应带 Retry-After")
	}
	// 即使密码正确也被拒绝，直到锁定过期。
	if rec := postLogin(engine, `{"username":"admin","password":"correct-horse-battery"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("锁定期间正确密码也应 429，实际 %d", rec.Code)
	}
}

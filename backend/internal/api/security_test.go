package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestPublicCommenterNeverLooksUpIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Nil dependencies intentionally fail the test if any lookup is attempted.
	registerPublicHelp(r.Group("/api/public"), nil)
	for _, query := range []string{"", "?user_id=1", "?user_id=2", "?user_id=-1", "?user_id=not-a-number"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/public/relay-stations/1/help/commenter"+query, nil))
		if w.Code != http.StatusOK || w.Body.String() != `{"data":{"author_name":""}}` {
			t.Fatalf("identity probe %q: %d %s", query, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("identity response must not be cached")
		}
	}
}

func TestPublicCommentGuardLimitsAttemptsAndChunkedBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/comments", newPublicCommentGuard(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for i := 0; i < 6; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/comments", strings.NewReader(`{}`))
		req.RemoteAddr = "203.0.113.1:1234"
		r.ServeHTTP(w, req)
		want := http.StatusNoContent
		if i == 5 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("attempt %d: got %d want %d", i, w.Code, want)
		}
		if i == 5 && w.Header().Get("Retry-After") == "" {
			t.Fatal("missing retry delay")
		}
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/comments", strings.NewReader(strings.Repeat("x", maxPublicCommentBytes+1)))
	req.ContentLength = -1
	req.RemoteAddr = "203.0.113.2:1234"
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked body: %d", w.Code)
	}
}

func TestPublicCommentLimiterGlobalExpiryAndCapacity(t *testing.T) {
	start := time.Now()
	l := &publicCommentLimiter{clients: make(map[string]commentWindow)}
	for i := 0; i < 60; i++ {
		if retry := l.allow(fmt.Sprintf("client-%d", i), start); retry != 0 {
			t.Fatalf("unexpected block at %d", i)
		}
	}
	if l.allow("another-client", start) == 0 {
		t.Fatal("global quota bypassed")
	}
	if l.allow("another-client", start.Add(time.Minute)) != 0 {
		t.Fatal("quota did not expire")
	}
	if len(l.clients) != 1 {
		t.Fatalf("expired keys not pruned: %d", len(l.clients))
	}
	l = &publicCommentLimiter{clients: make(map[string]commentWindow)}
	for i := 0; i < 4096; i++ {
		l.clients[fmt.Sprint(i)] = commentWindow{start: start}
	}
	if l.allow("overflow", start) == 0 || len(l.clients) != 4096 {
		t.Fatal("capacity must fail closed without growing")
	}
}

func TestSecurityHeadersAndBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(securityHeaders(), limitRequestBody(32))
	r.POST("/api/echo", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusNoContent)
	})
	for _, chunked := range []bool{false, true} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/echo", strings.NewReader(strings.Repeat("x", 33)))
		if chunked {
			req.ContentLength = -1
		}
		r.ServeHTTP(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("chunked=%v: %d", chunked, w.Code)
		}
		for key, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Cache-Control": "no-store", "Referrer-Policy": "no-referrer"} {
			if w.Header().Get(key) != want {
				t.Errorf("%s=%q", key, w.Header().Get(key))
			}
		}
	}
}

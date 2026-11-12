package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/auth"
)

type frameMemoryStore struct {
	value string
	found bool
	err   error
}

func (s *frameMemoryStore) Get(string) (string, bool, error) { return s.value, s.found, s.err }
func (s *frameMemoryStore) Set(_ string, value string) error {
	if s.err != nil {
		return s.err
	}
	s.value, s.found = value, true
	return nil
}

type blockingFrameStore struct {
	frameMemoryStore
	entered chan struct{}
	release chan struct{}
}

func (s *blockingFrameStore) Set(string, string) error {
	close(s.entered)
	<-s.release
	return nil
}

func TestFramePolicyReadsDoNotWaitForSettingsDatabase(t *testing.T) {
	store := &blockingFrameStore{
		frameMemoryStore: frameMemoryStore{value: `["https://qidian7.top"]`, found: true},
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	policy := newFramePolicy(store, nil)
	done := make(chan error, 1)
	go func() { done <- policy.save([]string{}) }()
	defer func() { close(store.release); <-done }()
	select {
	case <-store.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("save did not reach storage")
	}
	read := make(chan string, 1)
	go func() { read <- policy.ancestors("/public/service-status") }()
	select {
	case got := <-read:
		if got != "https://qidian7.top" {
			t.Fatalf("uncommitted policy became visible: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("public response blocked on settings database write")
	}
}

func TestFrameOriginsRejectPolicyInjectionAndBroadSources(t *testing.T) {
	for _, bad := range []string{"*", "https://*.qidian7.top", "http://qidian7.top", "https://qidian7.top/path", "https://user:synthetic-password@qidian7.top", "https://qidian7.top?x=1", "https://qidian7.top?", "https://qidian7.top#", "https://qidian7.top; frame-ancestors *", "https://qidian7.top\r\nX-Evil: yes", "'self'", "https://qidian7.top:", "https://qidian7.top:0", "https://qidian7.top:65536", "https://qidian7.top\\@evil.test", "https://.qidian7.top", "https://qidian7.top/%2f"} {
		if _, err := normalizeFrameOrigins([]string{bad}); err == nil {
			t.Errorf("accepted unsafe source %q", bad)
		}
	}
	if _, err := normalizeFrameOrigins(make([]string, 21)); err == nil {
		t.Fatal("accepted excessive origins")
	}
	got, err := normalizeFrameOrigins([]string{"https://QIDIAN7.TOP/", "https://qidian7.top:443", "https://qidian7.top:8443"})
	want := []string{"https://qidian7.top", "https://qidian7.top:8443"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized: %v %v", got, err)
	}
}

func TestFrameHeadersOnlyAllowExplicitPublicDocuments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := newFramePolicy(&frameMemoryStore{value: `["https://qidian7.top"]`, found: true}, nil)
	r := gin.New()
	r.Use(securityHeaders(p))
	r.NoRoute(func(c *gin.Context) { c.String(200, "document") })
	for _, path := range []string{"/public/service-status", "/public/help/1", "/public/relay-monitor/1", "/public/model-pricing/2/?x=1", "/", "/settings", "/login", "/api/security-settings", "/api/public/relay-stations/1/help", "/public/help/1/settings", "/public/help/0", "/public//service-status", "/public/service-status/../settings"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		// Spoofing Origin/Referer must never add an origin to the policy.
		req.Header.Set("Origin", "https://evil.test")
		req.Header.Set("Referer", "https://evil.test/")
		r.ServeHTTP(w, req)
		allowed := publicFramePath.MatchString(req.URL.Path)
		if allowed && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("public document can cache a stale embedding policy: %s", path)
		}
		csp := w.Header().Get("Content-Security-Policy")
		if allowed {
			if !strings.HasSuffix(csp, "frame-ancestors https://qidian7.top") || w.Header().Get("X-Frame-Options") != "" {
				t.Fatalf("public frame blocked: %s %v", path, w.Header())
			}
		} else if !strings.HasSuffix(csp, "frame-ancestors 'none'") || w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("private route weakened: %s %v", path, w.Header())
		}
		if strings.Contains(csp, "evil.test") || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("spoofed source or CORS permission")
		}
	}
}

func TestFramePolicyFailsClosedAndPersistsBeforeActivation(t *testing.T) {
	for _, store := range []*frameMemoryStore{{}, {value: `["*"]`, found: true}, {value: `broken`, found: true}, {err: errors.New("database private error")}} {
		p := newFramePolicy(store, nil)
		if p.ancestors("/public/help/1") != "'none'" {
			t.Fatal("invalid policy did not fail closed")
		}
	}
	store := &frameMemoryStore{}
	p := newFramePolicy(store, nil)
	if err := p.save([]string{"https://qidian7.top"}); err != nil {
		t.Fatal(err)
	}
	if newFramePolicy(store, nil).ancestors("/public/help/1") != "https://qidian7.top" {
		t.Fatal("policy not durable")
	}
	store.err = errors.New("database private error")
	if p.save([]string{"https://evil.test"}) == nil {
		t.Fatal("expected persistence error")
	}
	if p.ancestors("/public/help/1") != "https://qidian7.top" {
		t.Fatal("failed save modified live policy")
	}
	copy := p.view()
	copy.PublicFrameOrigins[0] = "https://evil.test"
	if p.ancestors("/public/help/1") != "https://qidian7.top" {
		t.Fatal("view mutated policy")
	}
	store.err = nil
	if err := p.save([]string{}); err != nil || p.ancestors("/public/help/1") != "'none'" {
		t.Fatal("clear did not revoke policy")
	}
}

func TestSecuritySettingsAuthenticationAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a, err := auth.New("test-admin", "test-password", "test-token-secret", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Full production registration proves neither endpoint is accidentally public.
	full := gin.New()
	Register(full, &Deps{Auth: a})
	for _, method := range []string{"GET", "PUT"} {
		w := httptest.NewRecorder()
		full.ServeHTTP(w, httptest.NewRequest(method, "/api/security-settings", nil))
		if w.Code != 401 {
			t.Fatalf("unauthorized %s: %d", method, w.Code)
		}
	}
	store := &frameMemoryStore{}
	p := newFramePolicy(store, nil)
	r := gin.New()
	r.Use(securityHeaders(p))
	g := r.Group("/api")
	g.Use(a.Middleware())
	registerSecuritySettings(g, p)
	token, _, err := a.Login("127.0.0.1", "test-admin", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		body   string
		status int
	}{{`{}`, 400}, {`{"public_frame_origins":null}`, 400}, {`{"public_frame_origins":["*"]}`, 400}, {`{"public_frame_origins":["https://qidian7.top"]}`, 200}, {`{"public_frame_origins":[]}`, 200}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/security-settings", strings.NewReader(test.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)
		if w.Code != test.status {
			t.Fatalf("body %s: %d %s", test.body, w.Code, w.Body)
		}
	}
}

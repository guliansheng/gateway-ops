package relay

import (
	"context"
	"database/sql/driver"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/crypto"
)

func TestPublicGroupMonitorCoalescesColdMiss(t *testing.T) {
	cipher, err := crypto.NewCipher("offline-group-monitor-singleflight-test")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Encrypt("fake-monitor-admin-key")
	if err != nil {
		t.Fatal(err)
	}

	const responseBody = `{"code":0,"message":"ok","data":{"items":[],"total":0,"pages":1}}`
	const callers = 24
	arrived := make(chan string, callers*2)
	release := make(chan struct{})
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.Header.Get("x-api-key") != "fake-monitor-admin-key" {
			t.Errorf("monitor request missing fake admin key")
		}
		if r.URL.Query().Get("group_id") != "42" {
			t.Errorf("group_id = %q, want 42", r.URL.Query().Get("group_id"))
		}
		arrived <- r.URL.Path
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, responseBody)
	}))
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		server.Close()
	}()

	repo, _ := newDegradationRunSQL(t,
		degradationRunSQLStep{
			contains: `FROM "relay_stations"`,
			columns:  []string{"id", "name", "base_url", "api_key_cipher"},
			rows:     [][]driver.Value{{int64(1), "Offline station", server.URL, ciphertext}},
		},
		degradationRunSQLStep{
			contains: `FROM "relay_groups"`,
			columns:  []string{"id", "relay_station_id", "external_id", "name", "description", "platform", "status", "monitor_enabled", "rate_multiplier"},
			rows:     [][]driver.Value{{int64(1), int64(1), int64(42), "Fake GPT group", "fixture", "openai", "active", true, float64(0.5)}},
		},
	)
	service := NewService(repo, cipher)

	start := make(chan struct{})
	entered := make(chan struct{}, callers)
	ready := sync.WaitGroup{}
	ready.Add(callers)
	type result struct {
		view PublicGroupMonitor
		err  error
	}
	results := make(chan result, callers)
	for i := 0; i < callers; i++ {
		go func() {
			ready.Done()
			<-start
			entered <- struct{}{}
			view, err := service.PublicGroupMonitorForRange(context.Background(), 1, "90m")
			results <- result{view: view, err: err}
		}()
	}
	ready.Wait()
	close(start)

	seenPaths := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case path := <-arrived:
			seenPaths[path] = true
		case <-time.After(3 * time.Second):
			t.Fatal("single monitor load did not reach both fake endpoints")
		}
	}
	for i := 0; i < callers; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent caller did not reach the cache barrier")
		}
	}
	if !seenPaths["/api/v1/admin/usage"] || !seenPaths["/api/v1/admin/ops/request-errors"] {
		t.Fatalf("monitor endpoints = %#v", seenPaths)
	}
	close(release)

	views := make([]PublicGroupMonitor, 0, callers)
	for i := 0; i < callers; i++ {
		select {
		case got := <-results:
			if got.err != nil {
				t.Fatalf("PublicGroupMonitorForRange error: %v", got.err)
			}
			if got.view.Summary.Total != 1 || len(got.view.Groups) != 1 {
				t.Fatalf("monitor result = %+v", got.view)
			}
			views = append(views, got.view)
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent monitor lookup did not complete")
		}
	}
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("cold miss made %d upstream requests, want 2 (usage + errors)", got)
	}

	views[0].Groups[0].Name = "caller mutation"
	cached, err := service.PublicGroupMonitorForRange(context.Background(), 1, "90m")
	if err != nil {
		t.Fatalf("cached lookup: %v", err)
	}
	if cached.Groups[0].Name != "Fake GPT group" {
		t.Fatalf("caller mutation changed cached monitor result: %q", cached.Groups[0].Name)
	}
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("cache hit made additional upstream requests: %d", got)
	}
}

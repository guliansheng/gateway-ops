package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"

	"github.com/gin-gonic/gin"
)

func TestPublicHelpStationURLRemovesLegacyUserinfo(t *testing.T) {
	got := publicHelpStationURL("https://legacy-user@example.invalid/help?keep=yes#section")
	if got != "https://example.invalid/help?keep=yes#section" {
		t.Fatalf("publicHelpStationURL() = %q", got)
	}
	if strings.Contains(got, "legacy-user") {
		t.Fatalf("legacy credentials escaped: %q", got)
	}
	if got := publicHelpStationURL("https://example.invalid/help"); got != "https://example.invalid/help" {
		t.Fatalf("URL without userinfo changed: %q", got)
	}
	if got := publicHelpStationURL("https://bad host"); got != "" {
		t.Fatalf("malformed URL = %q, want empty", got)
	}
}

func TestPublicModelPricingFailureHidesUpstreamDetailAndLogsIt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/public/relay-stations/7/model-pricing", nil)

	failPublicModelPricing(ctx, logger, 7, errors.New("fake upstream envelope: internal detail"))

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("HTTP status = %d, want 502", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "fake upstream envelope") || strings.Contains(recorder.Body.String(), "internal detail") {
		t.Fatalf("public response leaked upstream details: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "模型价格信息暂时无法获取") {
		t.Fatalf("public response lacks generic message: %s", recorder.Body.String())
	}
	if !strings.Contains(logOutput.String(), "fake upstream envelope: internal detail") {
		t.Fatalf("server log omitted diagnostic detail: %s", logOutput.String())
	}
}

func TestPublicServiceStatusCacheCoalescesColdMissAndCopiesResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &publicServiceStatusCache{}
	const callers = 24
	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(callers)
	started := make(chan struct{}, callers)
	entered := make(chan struct{}, callers)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var calls atomic.Int32

	updatedAt := time.Date(2026, 11, 12, 11, 0, 0, 0, time.UTC)
	fixture := PublicServiceStatusView{
		UpdatedAt: updatedAt,
		Days:      60,
		Services: []PublicServiceStatus{{
			ID:        "fake",
			UpdatedAt: &updatedAt,
			Components: []PublicServiceStatusComponent{{
				ID: "component",
				History: []PublicServiceStatusDay{{
					Date:      "2026-11-12",
					Incidents: []PublicServiceStatusBlip{{ID: "incident", Name: "original"}},
				}},
			}},
		}},
	}
	loader := func(ctx context.Context) (PublicServiceStatusView, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return fixture, nil
		case <-ctx.Done():
			return PublicServiceStatusView{}, ctx.Err()
		}
	}

	type result struct {
		view PublicServiceStatusView
		err  error
	}
	results := make(chan result, callers)
	for i := 0; i < callers; i++ {
		go func() {
			ready.Done()
			<-start
			entered <- struct{}{}
			view, err := cache.get(context.Background(), loader)
			results <- result{view: view, err: err}
		}()
	}
	ready.Wait()
	close(start)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("status loader did not start")
	}
	// Keep the first load blocked until every concurrent caller has crossed the barrier.
	for i := 0; i < callers; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent caller did not reach the cache barrier")
		}
	}
	close(release)

	views := make([]PublicServiceStatusView, 0, callers)
	for i := 0; i < callers; i++ {
		select {
		case got := <-results:
			if got.err != nil {
				t.Fatalf("cache.get error: %v", got.err)
			}
			views = append(views, got.view)
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent cache lookup did not complete")
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("loader ran %d times, want one cold-cache load", got)
	}

	views[0].Services[0].Components[0].History[0].Incidents[0].Name = "mutated by caller"
	cached, err := cache.get(context.Background(), func(context.Context) (PublicServiceStatusView, error) {
		return PublicServiceStatusView{}, errors.New("cache miss unexpectedly loaded again")
	})
	if err != nil {
		t.Fatalf("cached lookup: %v", err)
	}
	if got := cached.Services[0].Components[0].History[0].Incidents[0].Name; got != "original" {
		t.Fatalf("caller mutation changed cached nested result: %q", got)
	}
}

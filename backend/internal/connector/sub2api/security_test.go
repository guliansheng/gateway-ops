package sub2api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/guliansheng/gateway-ops/internal/connector"
	"github.com/guliansheng/gateway-ops/internal/httpclient"
)

func TestClientRejectsTokenRedirects(t *testing.T) {
	var received atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer sink.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(sink.URL, "127.0.0.1", "localhost", 1), http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	client := New()
	if client.http.ResponseBodyLimit != httpclient.MaxResponseBodyBytes {
		t.Fatal("connector is missing the shared response body limit")
	}
	_, err := client.RefreshToken(context.Background(), &connector.Channel{SiteURL: origin.URL}, "local-fake-refresh-token")
	if !errors.Is(err, resty.ErrAutoRedirectDisabled) {
		t.Fatalf("refresh error = %v, want redirect rejection", err)
	}
	if received.Load() != 0 {
		t.Fatal("redirect destination received the refresh token")
	}

	sameHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusFound)
	}))
	defer sameHost.Close()
	err = client.CheckAuth(context.Background(), &connector.Channel{SiteURL: sameHost.URL}, &connector.AuthSession{AccessToken: "local-fake-access-token"})
	if !errors.Is(err, resty.ErrAutoRedirectDisabled) {
		t.Fatalf("auth error = %v, want redirect rejection", err)
	}
	if received.Load() != 0 {
		t.Fatal("same-host cross-port destination received the bearer token")
	}
}

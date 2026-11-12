package newapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/guliansheng/gateway-ops/internal/connector"
	"github.com/guliansheng/gateway-ops/internal/httpclient"
)

func TestClientRejectsCredentialRedirects(t *testing.T) {
	var received atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		_, _ = io.WriteString(w, `{"success":true,"data":{}}`)
	}))
	defer sink.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(sink.URL, "127.0.0.1", "localhost", 1), http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	client := New()
	if client.http.ResponseBodyLimit != httpclient.MaxResponseBodyBytes {
		t.Fatal("connector is missing the shared response body limit")
	}
	_, err := client.Login(context.Background(), &connector.Channel{
		SiteURL: origin.URL, Username: "local-fake-user", Password: "local-fake-password",
		LoginParams: []connector.RequestKV{{Key: "password", Value: "{{password}}"}},
	})
	if !errors.Is(err, resty.ErrAutoRedirectDisabled) {
		t.Fatalf("login error = %v, want redirect rejection", err)
	}
	if received.Load() != 0 {
		t.Fatal("redirect destination received the login password")
	}

	sameHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusFound)
	}))
	defer sameHost.Close()
	err = client.CheckAuth(context.Background(), &connector.Channel{SiteURL: sameHost.URL}, &connector.AuthSession{
		Cookie:  "session=local-fake-cookie",
		Headers: map[string]string{"Authorization": "Bearer local-fake-token", "x-api-key": "local-fake-key"},
	})
	if !errors.Is(err, resty.ErrAutoRedirectDisabled) {
		t.Fatalf("auth error = %v, want redirect rejection", err)
	}
	if received.Load() != 0 {
		t.Fatal("same-host cross-port destination received authentication headers")
	}
}

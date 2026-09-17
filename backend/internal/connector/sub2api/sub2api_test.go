package sub2api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/connector"
)

func TestRefreshTokenKeepsExistingRefreshTokenWhenResponseOmitsReplacement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"access_token":"access-new","expires_in":3600,"token_type":"Bearer"}}`))
	}))
	defer server.Close()

	session, err := New().RefreshToken(context.Background(), &connector.Channel{SiteURL: server.URL}, "refresh-old")
	if err != nil {
		t.Fatal(err)
	}
	if session.RefreshToken != "refresh-old" {
		t.Fatalf("refresh token = %q, want old token retained", session.RefreshToken)
	}
}

func TestRefreshTokenPostsRefreshCredentialAndParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/refresh" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("refresh must not send Authorization header, got %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["refresh_token"] != "refresh-old" {
			t.Fatalf("refresh_token = %q", body["refresh_token"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"access_token":"access-new","refresh_token":"refresh-new","expires_in":86400,"token_type":"Bearer"}}`))
	}))
	defer server.Close()

	client := New()
	before := time.Now()
	session, err := client.RefreshToken(context.Background(), &connector.Channel{SiteURL: server.URL}, "refresh-old")
	if err != nil {
		t.Fatal(err)
	}
	if session.AccessToken != "access-new" || session.RefreshToken != "refresh-new" {
		t.Fatalf("session tokens = %#v", session)
	}
	if session.ExpiresAt.Before(before.Add(23*time.Hour)) || session.ExpiresAt.After(before.Add(25*time.Hour)) {
		t.Fatalf("unexpected expiry %s", session.ExpiresAt)
	}
}

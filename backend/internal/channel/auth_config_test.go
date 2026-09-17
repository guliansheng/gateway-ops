package channel

import (
	"encoding/json"
	"reflect"
	"testing"

	appcrypto "github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestAuthConfigDefaultsAndTemplateExpansion(t *testing.T) {
	headers, params := DefaultLoginConfig(storage.ChannelTypeNewAPI)
	if !reflect.DeepEqual(headers, []RequestKV{{Key: "Content-Type", Value: "application/json"}}) {
		t.Fatalf("unexpected newapi headers: %#v", headers)
	}
	wantParams := []RequestKV{{Key: "username", Value: "{{username}}"}, {Key: "password", Value: "{{password}}"}}
	if !reflect.DeepEqual(params, wantParams) {
		t.Fatalf("unexpected newapi params: %#v", params)
	}

	got := ExpandRequestKV([]RequestKV{
		{Key: "X-User", Value: "{{username}}"},
		{Key: "X-Password", Value: "prefix-{{password}}"},
	}, map[string]string{"username": "alice", "password": "secret"})
	want := map[string]string{"X-User": "alice", "X-Password": "prefix-secret"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded config = %#v, want %#v", got, want)
	}
}

func TestAuthConfigEmptyJSONFallsBackToDefaults(t *testing.T) {
	headers, params, err := ParseLoginConfig(storage.ChannelTypeSub2API, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(headers, []RequestKV{{Key: "Content-Type", Value: "application/json"}}) {
		t.Fatalf("unexpected sub2api headers: %#v", headers)
	}
	wantParams := []RequestKV{
		{Key: "email", Value: "{{username}}"},
		{Key: "password", Value: "{{password}}"},
		{Key: "turnstile_token", Value: "{{turnstile_token}}"},
	}
	if !reflect.DeepEqual(params, wantParams) {
		t.Fatalf("unexpected sub2api params: %#v", params)
	}
}

func TestExpandRequestKVSkipsEmptyTurnstilePlaceholder(t *testing.T) {
	got := ExpandRequestKV([]RequestKV{
		{Key: "email", Value: "{{username}}"},
		{Key: "turnstile_token", Value: "{{turnstile_token}}"},
	}, map[string]string{"username": "alice", "turnstile_token": ""})
	want := map[string]string{"email": "alice"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded config = %#v, want %#v", got, want)
	}
}

func TestNewAPIAccessTokenExplicitEmptyHeadersStayEmpty(t *testing.T) {
	cipher, err := appcrypto.NewCipher("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"auth_type":"access_token","token":"secret-token","headers":[]}`
	encrypted, err := cipher.Encrypt(raw)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Cipher: cipher}
	session, err := service.buildSessionFromToken(&storage.Channel{
		Type:           storage.ChannelTypeNewAPI,
		CredentialMode: storage.CredentialModeToken,
		PasswordCipher: encrypted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Headers == nil {
		t.Fatal("explicit empty headers should remain an explicit empty map")
	}
	if len(session.Headers) != 0 {
		t.Fatalf("headers = %#v, want empty", session.Headers)
	}
}

func TestMergeNewAPIEditCredentialKeepsStoredTokenAndUpdatesHeaders(t *testing.T) {
	cipher, err := appcrypto.NewCipher("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	existingRaw := `{"auth_type":"access_token","token":"secret-token","headers":[{"key":"Authorization","value":"Bearer {{token}}"}]}`
	existingCipher, err := cipher.Encrypt(existingRaw)
	if err != nil {
		t.Fatal(err)
	}
	incomingRaw := `{"auth_type":"access_token","token":"","headers":[{"key":"X-API-Key","value":"••••••••"},{"key":"X-Mode","value":"custom"}]}`

	mergedRaw, err := mergeNewAPIEditCredential(cipher, existingCipher, incomingRaw)
	if err != nil {
		t.Fatal(err)
	}
	var merged NewAPITokenCredential
	if err := json.Unmarshal([]byte(mergedRaw), &merged); err != nil {
		t.Fatal(err)
	}
	if merged.Token != "secret-token" {
		t.Fatalf("token = %q, want stored token", merged.Token)
	}
	wantHeaders := []RequestKV{{Key: "X-API-Key", Value: "secret-token"}, {Key: "X-Mode", Value: "custom"}}
	if !reflect.DeepEqual(merged.Headers, wantHeaders) {
		t.Fatalf("headers = %#v, want %#v", merged.Headers, wantHeaders)
	}
}

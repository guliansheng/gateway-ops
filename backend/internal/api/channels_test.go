package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/guliansheng/gateway-ops/internal/channel"
	appcrypto "github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestNewAPIEditMetadataPreservesTemplateAndMasksLiteralToken(t *testing.T) {
	cipher, err := appcrypto.NewCipher("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		headers []channel.RequestKV
		want    []channel.RequestKV
	}{
		{
			name:    "template stays exact",
			headers: []channel.RequestKV{{Key: "Authorization", Value: "{{token}}"}},
			want:    []channel.RequestKV{{Key: "Authorization", Value: "{{token}}"}},
		},
		{
			name:    "literal token keeps surrounding format but masks secret",
			headers: []channel.RequestKV{{Key: "Authorization", Value: "Token secret-token"}, {Key: "X-Mode", Value: "custom"}},
			want:    []channel.RequestKV{{Key: "Authorization", Value: "Token " + channel.MaskedTokenPlaceholder}, {Key: "X-Mode", Value: "custom"}},
		},
		{
			name:    "direct literal token is visibly different from bearer template",
			headers: []channel.RequestKV{{Key: "Authorization", Value: "secret-token"}},
			want:    []channel.RequestKV{{Key: "Authorization", Value: channel.MaskedTokenPlaceholder}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(channel.NewAPITokenCredential{AuthType: "access_token", Token: "secret-token", Headers: tt.headers})
			if err != nil {
				t.Fatal(err)
			}
			encrypted, err := cipher.Encrypt(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			authType, headers, err := newAPIEditMetadata(&Deps{Cipher: cipher}, storage.ChannelTypeNewAPI, storage.CredentialModeToken, encrypted)
			if err != nil {
				t.Fatal(err)
			}
			if authType != "access_token" {
				t.Fatalf("auth type = %q", authType)
			}
			if !reflect.DeepEqual(headers, tt.want) {
				t.Fatalf("headers = %#v, want %#v", headers, tt.want)
			}
		})
	}
}

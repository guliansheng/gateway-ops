package api

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/guliansheng/gateway-ops/internal/channel"
	appcrypto "github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestChannelAmountsConvertCostButPreserveCashAndUserCharge(t *testing.T) {
	balance := 100.0
	usage := relay.ChannelUsageTotal{Cost: 20, UserCharge: 7, MatchedAccountCount: 2, Complete: true}
	for _, mode := range []storage.BalanceMode{storage.BalanceModeAuto, storage.BalanceModeManual} {
		ch := storage.Channel{ID: 1, BalanceMode: mode, LastBalance: &balance, RechargeMultiplier: 0.1}
		got := channelAmounts(ch, usage, 30, 10)
		wantBalance, wantConsumption := 10.0, 3.0
		if mode == storage.BalanceModeManual {
			wantBalance, wantConsumption = 100, 2
		}
		if math.Abs(got.CurrentBalance-wantBalance) > 1e-9 || math.Abs(got.ConsumptionAmount-wantConsumption) > 1e-9 {
			t.Fatalf("%s amounts = %#v", mode, got)
		}
		if got.CumulativeRechargeAmount != 10 || got.UserChargeAmount != 7 || got.MatchedAccountCount != 2 || !got.UserChargeComplete {
			t.Fatalf("%s cash or user charge was converted: %#v", mode, got)
		}
	}
}

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
			want:    []channel.RequestKV{{Key: "Authorization", Value: "{{token}}"}, {Key: "New-Api-User", Value: "{{user_id}}"}},
		},
		{
			name:    "literal token keeps surrounding format but masks secret",
			headers: []channel.RequestKV{{Key: "Authorization", Value: "Token secret-token"}, {Key: "X-Mode", Value: "custom"}},
			want:    []channel.RequestKV{{Key: "Authorization", Value: "Token " + channel.MaskedTokenPlaceholder}, {Key: "X-Mode", Value: "custom"}, {Key: "New-Api-User", Value: "{{user_id}}"}},
		},
		{
			name:    "direct literal token is visibly different from bearer template",
			headers: []channel.RequestKV{{Key: "Authorization", Value: "secret-token"}},
			want:    []channel.RequestKV{{Key: "Authorization", Value: channel.MaskedTokenPlaceholder}, {Key: "New-Api-User", Value: "{{user_id}}"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(channel.NewAPITokenCredential{AuthType: "access_token", UserID: "123", Token: "secret-token", Headers: tt.headers})
			if err != nil {
				t.Fatal(err)
			}
			encrypted, err := cipher.Encrypt(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			authType, userID, headers, err := newAPIEditMetadata(&Deps{Cipher: cipher}, storage.ChannelTypeNewAPI, storage.CredentialModeToken, encrypted)
			if err != nil {
				t.Fatal(err)
			}
			if authType != "access_token" {
				t.Fatalf("auth type = %q", authType)
			}
			if userID != "123" {
				t.Fatalf("user id = %q, want 123", userID)
			}
			if !reflect.DeepEqual(headers, tt.want) {
				t.Fatalf("headers = %#v, want %#v", headers, tt.want)
			}
		})
	}
}

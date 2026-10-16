package api

import (
	"encoding/json"
	"testing"

	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestRelayAccountViewFromRiskPreservesPlatformAndScheduling(t *testing.T) {
	view := relayAccountViewFromRisk(relay.AccountRisk{
		Account: storage.RelayAccount{
			ExternalID:  42,
			Name:        "测试账号",
			Platform:    "openai",
			Type:        "apikey",
			Status:      "active",
			Schedulable: true,
		},
	})

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal relay account view: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal relay account view: %v", err)
	}

	if got["platform"] != "openai" {
		t.Errorf("platform = %v, want openai", got["platform"])
	}
	if got["type"] != "apikey" {
		t.Errorf("type = %v, want apikey", got["type"])
	}
	if got["status"] != "active" {
		t.Errorf("status = %v, want active", got["status"])
	}
	if got["schedulable"] != true {
		t.Errorf("schedulable = %v, want true", got["schedulable"])
	}
}

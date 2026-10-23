package relay

import (
	"testing"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestGroupPriceVisibilityLocalToggleAndPublicDefaults(t *testing.T) {
	group := &storage.RelayGroup{MonitorEnabled: true, ModelPricingEnabled: true}
	disabled := false
	body, err := applyGroupUpdate(group, GroupUpdateInput{ModelPricingEnabled: &disabled})
	if err != nil || len(body) != 0 || group.ModelPricingEnabled || !group.MonitorEnabled {
		t.Fatalf("price toggle must be local and independent: group=%+v body=%v err=%v", group, body, err)
	}
	body, err = applyGroupUpdate(group, GroupUpdateInput{MonitorEnabled: &disabled})
	if err != nil || len(body) != 0 || group.ModelPricingEnabled || group.MonitorEnabled {
		t.Fatalf("monitor toggle changed price setting: %+v %v %v", group, body, err)
	}
	body, err = applyGroupUpdate(group, GroupUpdateInput{IsExclusive: &disabled})
	if err != nil || group.IsExclusive || !group.ModelPricingEnabled || !group.MonitorEnabled || len(body) != 1 || body["is_exclusive"] != false {
		t.Fatalf("public defaults or remote body changed: %+v %v %v", group, body, err)
	}
	enabled := true
	body, err = applyGroupUpdate(group, GroupUpdateInput{IsExclusive: &enabled})
	if err != nil || !group.IsExclusive || group.ModelPricingEnabled || group.MonitorEnabled || len(body) != 1 || body["is_exclusive"] != true {
		t.Fatalf("exclusive group must disable defaults: %+v %v %v", group, body, err)
	}
}

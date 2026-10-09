package relay

import (
	"testing"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

// 首页“未关联渠道账号扣费”的明细必须与渠道侧拆分使用同一套绑定规则：URL 唯一
// 命中的账号和显式渠道分组绑定的账号都算已关联，剩下的才是未关联账号；账号是否
// 被排除在用户扣费统计之外，只影响汇总金额，不影响这里是否列出。
func TestUnlinkedAccountsKeepsOnlyAccountsWithoutChannelBinding(t *testing.T) {
	automaticID := uint(1)
	manualID := uint(2)
	channels := []storage.Channel{
		{ID: automaticID, BalanceMode: storage.BalanceModeAuto, SiteURL: "https://qidian.example"},
		{ID: manualID, BalanceMode: storage.BalanceModeManual, SiteURL: "https://manual.example"},
	}
	accounts := []storage.RelayAccount{
		{ExternalID: 11, Name: "奇点直连", BaseURL: "https://qidian.example"},
		{ExternalID: 12, Name: "凡人", BaseURL: "https://other.example"},
		{ExternalID: 13, Name: "手动渠道绑定", BaseURL: "https://other.example", UserChargeExcluded: true},
		{ExternalID: 14, Name: "临时号", BaseURL: "https://other.example", UserChargeExcluded: true},
	}
	overrides := []storage.RelayAccountCostOverride{{
		RelayAccountExternalID: 13, Mode: "channel_group", MonitorChannelID: &manualID, UpstreamGroup: "标准组",
	}}

	bindings := resolveChannelUsageCostBindings(channels, accounts, overrides, nil)
	got := unlinkedAccounts(accounts, bindings)
	if len(got) != 2 || got[0].ExternalID != 12 || got[1].ExternalID != 14 {
		t.Fatalf("unlinked accounts = %#v, want external IDs 12 and 14", got)
	}
	if !got[1].UserChargeExcluded {
		t.Fatalf("excluded account %#v must stay in the detail list", got[1])
	}
	if _, bound := bindings[12]; bound {
		t.Fatalf("account 12 must not be bound, got %#v", bindings[12])
	}
}

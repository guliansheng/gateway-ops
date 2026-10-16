package storage

import "testing"

// firstGroupModelType 取 model_types 的第一个非空值，转小写；无值时返回空串。
func TestFirstGroupModelType(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`["GPT","Claude"]`, "gpt"},
		{`["  Claude  ","GPT"]`, "claude"},
		{`["","GPT"]`, "gpt"},
		{`[]`, ""},
		{``, ""},
		{`not-json`, ""},
	}
	for _, item := range cases {
		if got := firstGroupModelType(item.raw); got != item.want {
			t.Fatalf("firstGroupModelType(%q) = %q，期望 %q", item.raw, got, item.want)
		}
	}
}

// 新账号继承所在分组的模型类型（取第一个值）。
func TestResolveNewAccountModelTypesUsesLinkedGroup(t *testing.T) {
	groups := []RelayGroup{
		{ExternalID: 1, Name: "A", RateMultiplier: 1, ModelTypesJSON: `["gpt"]`},
		{ExternalID: 2, Name: "B", RateMultiplier: 2, ModelTypesJSON: `["claude"]`},
	}
	links := []RelayAccountSnapshotLink{
		{AccountExternalID: 500, GroupExternalID: 1},
	}
	got := resolveNewAccountModelTypes(groups, links)
	if got[500] != "gpt" {
		t.Fatalf("账号 500 模型类型 = %q，期望 gpt：%+v", got[500], got)
	}
}

// 账号同属多个分组时取倍率最高的分组；倍率相同取名称靠前者。
func TestResolveNewAccountModelTypesPrefersHigherRate(t *testing.T) {
	groups := []RelayGroup{
		{ExternalID: 1, Name: "低倍率", RateMultiplier: 0.4, ModelTypesJSON: `["gpt"]`},
		{ExternalID: 2, Name: "高倍率", RateMultiplier: 1.2, ModelTypesJSON: `["claude"]`},
		{ExternalID: 3, Name: "同名A", RateMultiplier: 1.2, ModelTypesJSON: `["gemini"]`},
	}
	links := []RelayAccountSnapshotLink{
		{AccountExternalID: 500, GroupExternalID: 1},
		{AccountExternalID: 500, GroupExternalID: 2},
	}
	got := resolveNewAccountModelTypes(groups, links)
	if got[500] != "claude" {
		t.Fatalf("账号 500 模型类型 = %q，期望 claude（倍率更高的分组）：%+v", got[500], got)
	}
}

// 倍率相同取名称靠前的分组，结果稳定。
func TestResolveNewAccountModelTypesTieBreaksByName(t *testing.T) {
	groups := []RelayGroup{
		{ExternalID: 1, Name: "B组", RateMultiplier: 1, ModelTypesJSON: `["gpt"]`},
		{ExternalID: 2, Name: "A组", RateMultiplier: 1, ModelTypesJSON: `["claude"]`},
	}
	links := []RelayAccountSnapshotLink{
		{AccountExternalID: 500, GroupExternalID: 1},
		{AccountExternalID: 500, GroupExternalID: 2},
	}
	got := resolveNewAccountModelTypes(groups, links)
	if got[500] != "claude" {
		t.Fatalf("倍率相同应取名称靠前的 A组（claude），实际 %q：%+v", got[500], got)
	}
}

// 分组未配置模型类型时不设置。
func TestResolveNewAccountModelTypesSkipsGroupWithoutTypes(t *testing.T) {
	groups := []RelayGroup{
		{ExternalID: 1, Name: "空类型", RateMultiplier: 1, ModelTypesJSON: ``},
	}
	links := []RelayAccountSnapshotLink{
		{AccountExternalID: 500, GroupExternalID: 1},
	}
	if got := resolveNewAccountModelTypes(groups, links); len(got) != 0 {
		t.Fatalf("分组无模型类型时不应设置，实际 %+v", got)
	}
}

// 账号关联到未知分组时忽略。
func TestResolveNewAccountModelTypesIgnoresUnknownGroup(t *testing.T) {
	groups := []RelayGroup{
		{ExternalID: 1, Name: "A", RateMultiplier: 1, ModelTypesJSON: `["gpt"]`},
	}
	links := []RelayAccountSnapshotLink{
		{AccountExternalID: 500, GroupExternalID: 999},
	}
	if got := resolveNewAccountModelTypes(groups, links); len(got) != 0 {
		t.Fatalf("未知分组关联应被忽略，实际 %+v", got)
	}

}
func TestNewAccountNameMatcher(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		pattern string
		account string
		want    bool
	}{
		{name: "empty keeps old inheritance behavior", mode: "keyword", account: "https://example.com", want: false},
		{name: "keyword case insensitive", mode: "keyword", pattern: "HTTPS://", account: "https://example.com", want: true},
		{name: "keyword no match", mode: "keyword", pattern: "https://", account: "provider-a", want: false},
		{name: "regex match", mode: "regex", pattern: `^https?://`, account: "https://example.com", want: true},
		{name: "regex no match", mode: "regex", pattern: `^https?://`, account: "provider-a", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matcher, err := newAccountNameMatcher(test.mode, test.pattern)
			if err != nil {
				t.Fatal(err)
			}
			if got := matcher(test.account); got != test.want {
				t.Fatalf("matcher(%q) = %v, want %v", test.account, got, test.want)
			}
		})
	}
}

func TestValidateNewAccountModelTypeMatchRejectsInvalidRegex(t *testing.T) {
	if err := ValidateNewAccountModelTypeMatch("regex", "["); err == nil {
		t.Fatal("invalid regex must be rejected")
	}
	if err := ValidateNewAccountModelTypeMatch("unknown", "foo"); err == nil {
		t.Fatal("unknown match mode must be rejected")
	}
}

func TestFilterNewAccountModelTypeLinksOnlyExcludesMatchingNewNames(t *testing.T) {
	accounts := []RelayAccount{
		{ExternalID: 1, Name: "https://vendor.example"},
		{ExternalID: 2, Name: "regular-account"},
	}
	links := []RelayAccountSnapshotLink{
		{AccountExternalID: 1, GroupExternalID: 10},
		{AccountExternalID: 2, GroupExternalID: 10},
	}
	eligible, err := filterNewAccountModelTypeLinks(accounts, links, "keyword", "https://")
	if err != nil {
		t.Fatal(err)
	}
	modelTypes := resolveNewAccountModelTypes([]RelayGroup{{ExternalID: 10, Name: "gpt", RateMultiplier: 1, ModelTypesJSON: `["gpt"]`}}, eligible)
	if _, ok := modelTypes[1]; ok {
		t.Fatal("matching account must not inherit a model type")
	}
	if modelTypes[2] != "gpt" {
		t.Fatalf("non-matching account model type = %q, want gpt", modelTypes[2])
	}
}

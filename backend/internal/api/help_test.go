package api

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 公开帮助文档与留言名称共用 /help 前缀，这里保证 gin 不会因路由前缀冲突在启动时 panic，
// 并确认两条公开路由都真的挂上了。
func TestRegisterRoutesHelpPublicPathsCoexist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("注册路由时冲突: %v", recovered)
		}
	}()
	engine := gin.New()
	Register(engine, &Deps{})

	want := []string{
		"GET /api/public/relay-stations/:id/help",
		"GET /api/public/relay-stations/:id/help/commenter",
		"POST /api/public/relay-stations/:id/help/comments",
		"GET /api/help/documents/:id/comments",
	}
	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, route := range want {
		if !registered[route] {
			t.Errorf("缺少路由 %s", route)
		}
	}
}

func TestMaskHelpCommentAuthorKeepsPlainNames(t *testing.T) {
	for _, name := range []string{"游客", "张三丰", "  ", "", "@example.com", "admin@"} {
		if got := maskHelpCommentAuthor(name); got != name {
			t.Fatalf("maskHelpCommentAuthor(%q) = %q，非邮箱名称必须原样展示", name, got)
		}
	}
}

func TestMaskHelpCommentAuthorShowsDomainAndMasksLocalPart(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "a@x.com", want: "*@x.com"},
		{name: "ab@x.com", want: "**@x.com"},
		{name: "abc@x.com", want: "a*c@x.com"},
		{name: "john.doe@example.com", want: "jo*****e@example.com"},
		{name: "abcdefghij@x.com", want: "ab******ij@x.com"},
		{name: "verylonglocalpartname@example.org", want: "very*************name@example.org"},
	}
	for _, tt := range tests {
		got := maskHelpCommentAuthor(tt.name)
		if got != tt.want {
			t.Errorf("maskHelpCommentAuthor(%q) = %q，期望 %q", tt.name, got, tt.want)
		}
		if len([]rune(got)) != len([]rune(tt.name)) {
			t.Errorf("maskHelpCommentAuthor(%q) = %q，遮蔽后长度不能变", tt.name, got)
		}
		domain := tt.name[strings.LastIndex(tt.name, "@"):]
		if !strings.HasSuffix(got, domain) {
			t.Errorf("maskHelpCommentAuthor(%q) = %q，@ 后面的域名 %q 必须完整保留", tt.name, got, domain)
		}
	}
}

// 星号按用户名长度取约 60%，且中间打码、首尾各留字符。
func TestMaskHelpCommentAuthorStarRatioAroundSixtyPercent(t *testing.T) {
	for _, local := range []string{"alice", "alex2024", "user_name_01", "gatewayops.user"} {
		name := local + "@example.com"
		got := maskHelpCommentAuthor(name)
		stars := strings.Count(strings.SplitN(got, "@", 2)[0], "*")
		total := len([]rune(local))
		ratio := float64(stars) / float64(total)
		if ratio < 0.5 || ratio > 0.7 {
			t.Errorf("maskHelpCommentAuthor(%q) = %q，星号占比 %.2f 超出 60%% 附近", name, got, ratio)
		}
		if !strings.HasPrefix(got, local[:1]) || !strings.HasSuffix(strings.SplitN(got, "@", 2)[0], local[len(local)-1:]) {
			t.Errorf("maskHelpCommentAuthor(%q) = %q，必须保留用户名首尾字符", name, got)
		}
	}
}

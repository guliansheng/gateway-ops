package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

const frameOriginsSettingKey = "security.public_frame_origins"

var publicFramePath = regexp.MustCompile(`^/public/(service-status|(?:relay-monitor|model-pricing|help)/[1-9][0-9]*)/?$`)

type frameSettingsStore interface {
	Get(string) (string, bool, error)
	Set(string, string) error
}

type framePolicy struct {
	mu      sync.RWMutex
	saveMu  sync.Mutex
	store   frameSettingsStore
	origins []string
}

type securitySettingsView struct {
	PublicFrameOrigins []string `json:"public_frame_origins"`
}

// An absent, invalid or unreadable policy fails closed. Load once at startup;
// successful admin saves update the same process only after durable persistence.
func newFramePolicy(store frameSettingsStore, log *slog.Logger) *framePolicy {
	p := &framePolicy{store: store, origins: []string{}}
	if store == nil {
		return p
	}
	raw, found, err := store.Get(frameOriginsSettingKey)
	if err == nil && found {
		var origins []string
		err = json.Unmarshal([]byte(raw), &origins)
		if err == nil {
			p.origins, err = normalizeFrameOrigins(origins)
		}
	}
	if err != nil {
		p.origins = []string{}
		if log != nil {
			log.Error("unable to load public iframe policy; denying all embedding", "err", err)
		}
	}
	return p
}

func normalizeFrameOrigins(origins []string) ([]string, error) {
	if len(origins) > 20 {
		return nil, errors.New("最多允许 20 个嵌入来源")
	}
	out := []string{}
	seen := make(map[string]bool)
	for _, raw := range origins {
		raw = strings.TrimSpace(raw)
		if len(raw) > 255 || raw == "" || strings.ContainsAny(raw, " \t\r\n;,'\"*\\") {
			return nil, errors.New("来源必须是完整的 HTTPS 域名，不允许通配符或 CSP 指令")
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") {
			return nil, errors.New("来源只能包含 https://域名 和可选端口，不可带路径、账号、查询或片段")
		}
		host := strings.ToLower(u.Hostname())
		if net.ParseIP(host) == nil {
			for _, label := range strings.Split(host, ".") {
				if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
					return nil, errors.New("嵌入来源域名格式无效")
				}
				for _, ch := range label {
					if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
						return nil, errors.New("域名请使用 ASCII 或 punycode 格式")
					}
				}
			}
		}
		port := u.Port()
		if port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return nil, errors.New("嵌入来源端口无效")
			}
			port = strconv.Itoa(n)
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		if port != "" && port != "443" {
			host += ":" + port
		}
		origin := "https://" + host
		if !seen[origin] {
			seen[origin] = true
			out = append(out, origin)
		}
	}
	return out, nil
}

func (p *framePolicy) view() securitySettingsView {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return securitySettingsView{PublicFrameOrigins: append([]string{}, p.origins...)}
}

func (p *framePolicy) ancestors(path string) string {
	if p == nil || !publicFramePath.MatchString(path) {
		return "'none'"
	}
	origins := p.view().PublicFrameOrigins
	if len(origins) == 0 {
		return "'none'"
	}
	return strings.Join(origins, " ")
}

func (p *framePolicy) save(origins []string) error {
	normalized, err := normalizeFrameOrigins(origins)
	if err != nil {
		return err
	}
	origins = normalized
	// Serialize durable writes without stalling public page reads on database IO.
	p.saveMu.Lock()
	defer p.saveMu.Unlock()
	if p.store == nil {
		return errors.New("security settings storage unavailable")
	}
	value, err := json.Marshal(origins)
	if err != nil {
		return err
	}
	if err := p.store.Set(frameOriginsSettingKey, string(value)); err != nil {
		return err
	}
	p.mu.Lock()
	p.origins = append([]string{}, origins...)
	p.mu.Unlock()
	return nil
}

// Register only inside the authenticated /api group, never under /api/public.
func registerSecuritySettings(g *gin.RouterGroup, p *framePolicy) {
	g.GET("/security-settings", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": p.view()})
	})
	g.PUT("/security-settings", func(c *gin.Context) {
		var in struct {
			PublicFrameOrigins *[]string `json:"public_frame_origins" binding:"required"`
		}
		if err := c.ShouldBindJSON(&in); err != nil || in.PublicFrameOrigins == nil {
			fail(c, http.StatusBadRequest, errors.New("请提供 public_frame_origins 数组；空数组表示禁止所有嵌入"))
			return
		}
		origins, err := normalizeFrameOrigins(*in.PublicFrameOrigins)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if err := p.save(origins); err != nil {
			fail(c, http.StatusInternalServerError, errors.New("无法保存安全策略，请稍后重试"))
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": p.view()})
	})
}

package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/auth"
)

func registerAuth(g *gin.RouterGroup, d *Deps) {
	g.POST("/auth/login", func(c *gin.Context) { login(c, d) })
	g.GET("/auth/me", func(c *gin.Context) { whoami(c, d) })
	g.POST("/auth/logout", func(c *gin.Context) {
		// 无状态 token，客户端丢弃即可；这个接口仅作语义存在。
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
}

type loginInput struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func login(c *gin.Context, d *Deps) {
	// 鉴权关闭：任何登录请求都直接成功；前端在 /auth/me 已经知道无需登录。
	if d.Auth == nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"auth_disabled": true,
				"username":      "anonymous",
			},
		})
		return
	}
	var in loginInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ip := c.ClientIP()
	token, exp, err := d.Auth.Login(ip, in.Username, in.Password)
	if err != nil {
		var locked *auth.ErrLoginLocked
		if errors.As(err, &locked) {
			retry := int(locked.RetryAfter.Seconds())
			if retry < 1 {
				retry = 1
			}
			c.Header("Retry-After", strconv.Itoa(retry))
			if d.Log != nil {
				d.Log.Warn("admin login blocked by rate limit",
					"scope", locked.Scope, "ip", ip, "username", in.Username,
					"retry_after_seconds", retry)
			}
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many failed attempts, retry later"})
			return
		}
		// 凭证错误：返回统一提示，不回显具体是账号还是密码错。
		if d.Log != nil {
			d.Log.Warn("admin login failed", "ip", ip, "username", in.Username, "err", err)
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"token":      token,
			"expires_at": exp.Unix(),
			"username":   d.Auth.Username(),
		},
	})
}

// whoami 既是"前端启动探测"接口也是"已登录信息"接口。
//
//   - 鉴权关闭 → 返回 {auth_disabled: true}，前端据此跳过登录页
//   - 鉴权开启但未带 token → 中间件已经在前面 401 拦截，根本走不到这里
//   - 鉴权开启 + 有效 token → 返回 username
func whoami(c *gin.Context, d *Deps) {
	if d.Auth == nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"auth_disabled": true,
				"username":      "anonymous",
			},
		})
		return
	}
	sub, _ := c.Get("authSubject")
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"username": sub}})
}

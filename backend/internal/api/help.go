package api

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

// 帮助文档：每个中转站一篇富文本（HTML）文档。
// 管理端全部在中转站页面的弹窗里完成（含留言查看 / 删除），这里只提供读取 / 保存 / 删除和公开访问。

func registerHelp(g *gin.RouterGroup, d *Deps) {
	gp := g.Group("/help")
	gp.GET("/stations/:station_id/document", func(c *gin.Context) { getStationHelpDocument(c, d) })
	gp.PUT("/stations/:station_id/document", func(c *gin.Context) { putStationHelpDocument(c, d) })
	gp.DELETE("/documents/:id", func(c *gin.Context) { deleteHelpDocument(c, d) })
	gp.GET("/documents/:id/comments", func(c *gin.Context) { listHelpComments(c, d) })
	gp.DELETE("/comments/:id", func(c *gin.Context) { deleteHelpComment(c, d) })
	gp.POST("/comments/batch-delete", func(c *gin.Context) { batchDeleteHelpComments(c, d) })
}

func registerPublicHelp(g *gin.RouterGroup, d *Deps) {
	g.GET("/relay-stations/:id/help", func(c *gin.Context) { publicHelp(c, d) })
	g.GET("/relay-stations/:id/help/commenter", func(c *gin.Context) { publicHelpCommenter(c, d) })
	g.POST("/relay-stations/:id/help/comments", func(c *gin.Context) { createPublicHelpComment(c, d) })
}

type helpDocumentInput struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Enabled *bool  `json:"enabled"`
}

type helpCommentInput struct {
	AuthorName string `json:"author_name"`
	Body       string `json:"body"`
}

type helpCommentBatchInput struct {
	CommentIDs []uint `json:"comment_ids"`
}

var (
	scriptBlockRE = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	scriptTagRE   = regexp.MustCompile(`(?is)<script\b[^>]*>`)
	eventAttrRE   = regexp.MustCompile(`(?is)\son\w+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)
)

// sanitizeHelpHTML 去掉脚本与内联事件属性。正文由管理员在富文本编辑器里产生，
// 这里只做保守的防线，避免手工构造的请求把脚本带到公开页面。
func sanitizeHelpHTML(content string) string {
	content = scriptBlockRE.ReplaceAllString(content, "")
	content = scriptTagRE.ReplaceAllString(content, "")
	return eventAttrRE.ReplaceAllString(content, "")
}

func helpEnabled(in helpDocumentInput) bool {
	return in.Enabled == nil || *in.Enabled
}

// maskHelpCommentAuthor 遮蔽留言名称里的邮箱：@ 后面的域名完整展示，
// @ 前的用户名只露首尾、中间用 * 覆盖——星号约占用户名长度的 60%，
// 再按字符数夹取上下限（不足 3 字整段打码，最多只遮到剩首尾各 1 字）。
// 不带 @ 的自定义名称不是邮箱，原样展示。
// 管理端留言列表走 /help/documents/:id/comments，看到的始终是完整邮箱。
func maskHelpCommentAuthor(name string) string {
	at := strings.LastIndex(name, "@")
	if at <= 0 || at == len(name)-1 {
		return name
	}
	local := []rune(name[:at])
	domain := name[at:]
	total := len(local)
	stars := int(math.Round(float64(total) * 0.6))
	if total < 3 {
		// 一两个字符露出来等于没遮，整段打码。
		stars = total
	} else {
		// 至少留 1 个 *，最多遮到剩首尾各 1 字。
		if stars < 1 {
			stars = 1
		}
		if stars > total-2 {
			stars = total - 2
		}
	}
	visible := total - stars
	left := (visible + 1) / 2
	right := visible - left
	return string(local[:left]) + strings.Repeat("*", stars) + string(local[total-right:]) + domain
}

func getStationHelpDocument(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "station_id")
	if err != nil || stationID == 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站 ID"))
		return
	}
	if _, err := d.RelayStations.FindByID(stationID); err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	document, err := d.Help.FindByStation(stationID)
	if errors.Is(err, storage.ErrHelpDocumentNotFound) {
		c.JSON(http.StatusOK, gin.H{"data": nil})
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": document})
}

func putStationHelpDocument(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "station_id")
	if err != nil || stationID == 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站 ID"))
		return
	}
	station, err := d.RelayStations.FindByID(stationID)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	var in helpDocumentInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		fail(c, http.StatusBadRequest, errors.New("文档标题不能为空"))
		return
	}
	if len([]rune(title)) > 200 {
		fail(c, http.StatusBadRequest, errors.New("文档标题不能超过 200 字"))
		return
	}
	if len(in.Content) > 1_000_000 {
		fail(c, http.StatusBadRequest, errors.New("文档内容过大（超过 1MB）"))
		return
	}
	document := &storage.HelpDocument{
		RelayStationID: station.ID,
		Title:          title,
		Content:        sanitizeHelpHTML(in.Content),
		ContentFormat:  "html",
		Enabled:        helpEnabled(in),
	}
	if err := d.Help.Upsert(document); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": document})
}

func deleteHelpDocument(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil || id == 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的文档 ID"))
		return
	}
	if err := d.Help.Delete(id); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}

func listHelpComments(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil || id == 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的文档 ID"))
		return
	}
	comments, err := d.Help.Comments(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": comments})
}

func deleteHelpComment(c *gin.Context, d *Deps) {
	id, err := uintParam(c, "id")
	if err != nil || id == 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的留言 ID"))
		return
	}
	if err := d.Help.DeleteComment(id); err != nil {
		if errors.Is(err, storage.ErrHelpCommentNotFound) {
			fail(c, http.StatusNotFound, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}

func batchDeleteHelpComments(c *gin.Context, d *Deps) {
	var in helpCommentBatchInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(in.CommentIDs) == 0 {
		fail(c, http.StatusBadRequest, errors.New("请选择要删除的留言"))
		return
	}
	if len(in.CommentIDs) > 500 {
		fail(c, http.StatusBadRequest, errors.New("单次最多删除 500 条"))
		return
	}
	for _, id := range in.CommentIDs {
		if id == 0 {
			fail(c, http.StatusBadRequest, errors.New("无效的留言 ID"))
			return
		}
	}
	deleted, err := d.Help.DeleteComments(in.CommentIDs)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": deleted}})
}

func publicHelp(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil || stationID == 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站 ID"))
		return
	}
	document, comments, err := d.Help.Public(stationID)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("该中转站未发布帮助文档"))
		return
	}
	station, _ := d.RelayStations.FindByID(stationID)
	stationName := ""
	if station != nil {
		stationName = station.Name
	}
	// 公开页只给遮蔽后的名称；完整邮箱属于管理端视图。
	for i := range comments {
		comments[i].AuthorName = maskHelpCommentAuthor(comments[i].AuthorName)
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"document_id":  document.ID,
		"station_id":   stationID,
		"station_name": stationName,
		"title":        document.Title,
		"content":      document.Content,
		"updated_at":   document.UpdatedAt,
		"comments":     comments,
	}})
}

// publicHelpCommenter 用 user_id 换留言名称的默认值（该用户的邮箱），前端拿去预填名称输入框，
// 访客仍可自行修改。查不到用户或邮箱时回空串，公开文档与留言不受影响（名称回落到「游客」）。
func publicHelpCommenter(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil || stationID == 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站 ID"))
		return
	}
	raw := strings.TrimSpace(c.Query("user_id"))
	if raw == "" {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"author_name": ""}})
		return
	}
	userID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || userID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("用户 ID 无效"))
		return
	}
	email, err := d.Relay.UserEmail(c.Request.Context(), stationID, userID)
	if err != nil {
		if d.Log != nil {
			d.Log.Warn("help commenter email lookup failed", "station_id", stationID, "user_id", userID, "err", err)
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"author_name": ""}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"author_name": email}})
}

func createPublicHelpComment(c *gin.Context, d *Deps) {
	stationID, err := uintParam(c, "id")
	if err != nil || stationID == 0 {
		fail(c, http.StatusBadRequest, errors.New("无效的中转站 ID"))
		return
	}
	var in helpCommentInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(in.AuthorName)
	if name == "" {
		name = "游客"
	}
	if len([]rune(name)) > 128 {
		fail(c, http.StatusBadRequest, errors.New("留言名称不能超过 128 字"))
		return
	}
	body := strings.TrimSpace(in.Body)
	if body == "" || len([]rune(body)) > 2000 {
		fail(c, http.StatusBadRequest, errors.New("留言不能为空且不能超过 2000 字"))
		return
	}
	document, err := d.Help.FindByStation(stationID)
	if err != nil || !document.Enabled {
		fail(c, http.StatusNotFound, errors.New("该中转站未发布帮助文档"))
		return
	}
	comment := &storage.HelpComment{HelpDocumentID: document.ID, RelayStationID: stationID, AuthorName: name, Body: body}
	if err := d.Help.AddComment(comment); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	// 公开接口不回传完整邮箱，落库仍是原文（管理端留言列表可见）。
	publicComment := *comment
	publicComment.AuthorName = maskHelpCommentAuthor(comment.AuthorName)
	c.JSON(http.StatusOK, gin.H{"data": publicComment})
}

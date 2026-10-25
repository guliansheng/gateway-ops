package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"gorm.io/gorm"
)

const (
	groupDegradationRoute       = "/relay-stations/:id/groups/:external_id/degradation"
	degradationPromptsRoute     = "/relay-stations/:id/degradation-prompts"
	degradationPromptsBodyLimit = 80 * 1024
)

func registerGroupDegradation(g *gin.RouterGroup, d *Deps) {
	g.GET(groupDegradationRoute, func(c *gin.Context) { getGroupDegradation(c, d, false) })
	g.PUT(groupDegradationRoute, func(c *gin.Context) { putGroupDegradation(c, d) })
	g.POST(groupDegradationRoute+"/run", func(c *gin.Context) { runGroupDegradation(c, d) })
	g.DELETE(groupDegradationRoute+"/history", func(c *gin.Context) { clearGroupDegradationHistory(c, d) })
	g.DELETE(groupDegradationRoute+"/history/:result_id", func(c *gin.Context) { deleteGroupDegradationResult(c, d) })
	g.PATCH(groupDegradationRoute+"/history/:result_id", func(c *gin.Context) { patchGroupDegradationResult(c, d) })
	g.POST(groupDegradationRoute+"/history/:result_id/review", func(c *gin.Context) { reviewGroupDegradation(c, d) })
	g.POST(groupDegradationRoute+"/import", func(c *gin.Context) { importGroupDegradationResult(c, d) })
	g.GET(degradationPromptsRoute, func(c *gin.Context) { getDegradationPrompts(c, d) })
	g.PUT(degradationPromptsRoute, func(c *gin.Context) { putDegradationPrompts(c, d) })
	g.GET("/relay-stations/:id/accounts/:external_id/degradation", func(c *gin.Context) { getAccountDegradation(c, d) })
	g.POST("/relay-stations/:id/accounts/:external_id/degradation/run", func(c *gin.Context) { runAccountDegradation(c, d) })
	g.GET("/relay-stations/:id/accounts/:external_id/degradation/config", func(c *gin.Context) { getAccountDegradationConfig(c, d) })
	g.PUT("/relay-stations/:id/accounts/:external_id/degradation/config", func(c *gin.Context) { putAccountDegradationConfig(c, d) })
	g.GET("/relay-stations/:id/account-degradation/latest", func(c *gin.Context) { getLatestAccountDegradation(c, d) })
	g.DELETE("/relay-stations/:id/accounts/:external_id/degradation/history", func(c *gin.Context) { clearAccountDegradationHistory(c, d) })
	g.DELETE("/relay-stations/:id/accounts/:external_id/degradation/history/:result_id", func(c *gin.Context) { deleteAccountDegradationResult(c, d) })
	g.GET("/relay-stations/:id/degradation-usage", func(c *gin.Context) { getDegradationUsage(c, d) })
}

func registerPublicGroupDegradation(g *gin.RouterGroup, d *Deps) {
	g.GET(groupDegradationRoute, func(c *gin.Context) { getGroupDegradation(c, d, true) })
}

func groupDegradationStationParam(c *gin.Context, d *Deps) (uint, bool) {
	c.Header("Cache-Control", "no-store")
	stationID, err := uintParam(c, "id")
	if err != nil || stationID == 0 {
		fail(c, http.StatusBadRequest, errors.New("中转站 ID 无效"))
		return 0, false
	}
	if d.Relay == nil {
		fail(c, http.StatusServiceUnavailable, errors.New("降智检测服务尚未就绪"))
		return 0, false
	}
	return stationID, true
}

func groupDegradationParams(c *gin.Context, d *Deps) (uint, int64, bool) {
	stationID, ok := groupDegradationStationParam(c, d)
	if !ok {
		return 0, 0, false
	}
	groupID, err := strconv.ParseInt(c.Param("external_id"), 10, 64)
	if err != nil || groupID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("分组或账号 ID 无效"))
		return 0, 0, false
	}
	return stationID, groupID, true
}

func getGroupDegradation(c *gin.Context, d *Deps, public bool) {
	stationID, groupID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	view, err := d.Relay.GroupDegradation(c.Request.Context(), stationID, groupID, public)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func putGroupDegradation(c *gin.Context, d *Deps) {
	stationID, groupID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	config := storage.DefaultGroupDegradationConfig()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	// The PUT body is Config itself, not {config: Config}.
	if err := decodeDegradationObject(c.Request.Body, &config); err != nil {
		fail(c, http.StatusBadRequest, errors.New("请求体必须是有效的降智检测配置对象，且字段不能为 null 或包含多余内容"))
		return
	}
	view, err := d.Relay.UpdateGroupDegradation(c.Request.Context(), stationID, groupID, config)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func decodeGroupDegradationConfig(raw []byte, config *storage.GroupDegradationConfig) error {
	return decodeDegradationObject(bytes.NewReader(raw), config)
}

// Strict object decoding rejects top-level and field nulls, unknown fields,
// non-objects, and any trailing JSON/content. Never expose decoder errors.
func decodeDegradationObject(body io.Reader, target any) error {
	decoder := json.NewDecoder(body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return errors.New("expected JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing content")
	}
	fields := json.NewDecoder(bytes.NewReader(raw))
	if _, err := fields.Token(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for fields.More() {
		key, err := fields.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return errors.New("invalid or duplicate field")
		}
		seen[name] = true
		var value json.RawMessage
		if err := fields.Decode(&value); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("null field")
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func runGroupDegradation(c *gin.Context, d *Deps) {
	stationID, groupID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	method := c.Query("method")
	var result storage.GroupDegradationResult
	var err error
	if method == "" {
		result, err = d.Relay.RunGroupDegradation(c.Request.Context(), stationID, groupID)
	} else {
		result, err = d.Relay.RunGroupDegradationMethod(c.Request.Context(), stationID, groupID, method)
	}
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func failGroupDegradation(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		fail(c, http.StatusNotFound, errors.New("中转站或分组不存在"))
	case errors.Is(err, storage.ErrGroupDegradationDisabled):
		fail(c, http.StatusConflict, storage.ErrGroupDegradationDisabled)
	case errors.Is(err, storage.ErrGroupDegradationBusy):
		fail(c, http.StatusConflict, storage.ErrGroupDegradationBusy)
	case errors.Is(err, storage.ErrInvalidGroupDegradationConfig), errors.Is(err, storage.ErrInvalidDegradationFilter):
		fail(c, http.StatusBadRequest, err)
	default:
		// Database and upstream errors can include credentials or raw responses.
		fail(c, http.StatusInternalServerError, errors.New("降智检测操作失败，请稍后重试"))
	}
}

func getDegradationPrompts(c *gin.Context, d *Deps) {
	stationID, ok := groupDegradationStationParam(c, d)
	if !ok {
		return
	}
	prompts, err := d.Relay.DegradationPrompts(c.Request.Context(), stationID)
	if err != nil {
		failDegradationPrompts(c, err, false)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": prompts})
}

func putDegradationPrompts(c *gin.Context, d *Deps) {
	stationID, ok := groupDegradationStationParam(c, d)
	if !ok {
		return
	}
	var prompts storage.DegradationPromptSettings
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, degradationPromptsBodyLimit)
	if err := decodeDegradationObject(c.Request.Body, &prompts); err != nil {
		fail(c, http.StatusBadRequest, errors.New("请求体必须是有效的提示词设置对象，且字段不能为 null 或包含多余内容"))
		return
	}
	prompts, err := d.Relay.UpdateDegradationPrompts(c.Request.Context(), stationID, prompts)
	if err != nil {
		failDegradationPrompts(c, err, true)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": prompts})
}

func failDegradationPrompts(c *gin.Context, err error, writing bool) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		fail(c, http.StatusNotFound, errors.New("中转站不存在"))
	case writing && errors.Is(err, storage.ErrInvalidDegradationPromptSettings):
		// Do not echo any supplied prompt/answer or wrapped storage error.
		fail(c, http.StatusBadRequest, storage.ErrInvalidDegradationPromptSettings)
	default:
		fail(c, http.StatusInternalServerError, errors.New("降智检测提示词设置操作失败，请稍后重试"))
	}
}

func clearGroupDegradationHistory(c *gin.Context, d *Deps) {
	stationID, groupID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	// The filter narrows which rows are removed (error/success/pelican/candy);
	// invalid values are rejected inside the storage transaction with a 400.
	view, err := d.Relay.ClearGroupDegradationHistory(c.Request.Context(), stationID, groupID, c.Query("filter"))
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

// Re-review one history row: re-run the machine verdict on the stored evidence
// and stamp the fresh outcome back. The body is intentionally ignored — the
// judge decides, never the caller.
func reviewGroupDegradation(c *gin.Context, d *Deps) {
	stationID, groupID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	resultID, err := uintParam(c, "result_id")
	if err != nil || resultID == 0 {
		fail(c, http.StatusBadRequest, errors.New("检测记录 ID 无效"))
		return
	}
	view, err := d.Relay.ReviewGroupDegradation(c.Request.Context(), stationID, groupID, resultID)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func deleteGroupDegradationResult(c *gin.Context, d *Deps) {
	stationID, groupID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	resultID, err := uintParam(c, "result_id")
	if err != nil || resultID == 0 {
		fail(c, http.StatusBadRequest, errors.New("检测记录 ID 无效"))
		return
	}
	view, err := d.Relay.DeleteGroupDegradationResult(c.Request.Context(), stationID, groupID, resultID)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

// Manual review of one history row. The verdict is the only thing an admin can
// change: the judge's reason and the stored HTML/answer stay as evidence.
func patchGroupDegradationResult(c *gin.Context, d *Deps) {
	stationID, groupID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	resultID, err := uintParam(c, "result_id")
	if err != nil || resultID == 0 {
		fail(c, http.StatusBadRequest, errors.New("检测记录 ID 无效"))
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4*1024)
	if err := decodeDegradationObject(c.Request.Body, &body); err != nil || !storage.ValidGroupDegradationStatus(body.Status) {
		fail(c, http.StatusBadRequest, errors.New("判定状态必须是 normal、degraded、inconclusive 或 error"))
		return
	}
	view, err := d.Relay.UpdateGroupDegradationResult(c.Request.Context(), stationID, groupID, resultID, body.Status)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

// Copy one account test into this group's history. The account must belong to
// the same station, and the account result must belong to that account, so an
// id from another tenant can never be imported.
func importGroupDegradationResult(c *gin.Context, d *Deps) {
	stationID, groupID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	var body struct {
		AccountExternalID int64 `json:"account_external_id"`
		AccountResultID   uint  `json:"account_result_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4*1024)
	if err := decodeDegradationObject(c.Request.Body, &body); err != nil || body.AccountExternalID <= 0 || body.AccountResultID == 0 {
		fail(c, http.StatusBadRequest, errors.New("请求体必须包含有效的 account_external_id 与 account_result_id"))
		return
	}
	view, created, err := d.Relay.ImportGroupDegradationResult(c.Request.Context(), stationID, groupID, body.AccountExternalID, body.AccountResultID)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"imported": created, "view": view}})
}

func getAccountDegradation(c *gin.Context, d *Deps) {
	stationID, accountID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	results, err := d.Relay.AccountDegradationHistory(c.Request.Context(), stationID, accountID)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"results": results}})
}

func getAccountDegradationConfig(c *gin.Context, d *Deps) {
	stationID, accountID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	config, err := d.Relay.AccountDegradationConfig(c.Request.Context(), stationID, accountID)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": config})
}

func putAccountDegradationConfig(c *gin.Context, d *Deps) {
	stationID, accountID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	config := storage.DefaultAccountDegradationConfig()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	if err := decodeDegradationObject(c.Request.Body, &config); err != nil {
		fail(c, http.StatusBadRequest, errors.New("账号降智检测配置无效"))
		return
	}
	config, err := d.Relay.SaveAccountDegradationConfig(c.Request.Context(), stationID, accountID, config)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidAccountDegradationConfig) {
			fail(c, http.StatusBadRequest, err)
		} else {
			failGroupDegradation(c, err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": config})
}

func getLatestAccountDegradation(c *gin.Context, d *Deps) {
	stationID, ok := groupDegradationStationParam(c, d)
	if !ok {
		return
	}
	results, err := d.Relay.LatestAccountDegradation(c.Request.Context(), stationID)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}

type accountDegradationRunRequest struct {
	storage.GroupDegradationConfig
	FallbackModel string `json:"fallback_model"`
	Protocol      string `json:"protocol"`
}

func runAccountDegradation(c *gin.Context, d *Deps) {
	stationID, accountID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	request := accountDegradationRunRequest{GroupDegradationConfig: storage.DefaultGroupDegradationConfig()}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	if err := decodeDegradationObject(c.Request.Body, &request); err != nil {
		fail(c, http.StatusBadRequest, errors.New("账号降智测试参数无效"))
		return
	}
	request.GroupDegradationConfig.Protocol = request.Protocol
	result, err := d.Relay.RunAccountDegradation(c.Request.Context(), stationID, accountID, request.GroupDegradationConfig, request.FallbackModel)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func clearAccountDegradationHistory(c *gin.Context, d *Deps) {
	stationID, accountID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	if err := d.Relay.ClearAccountDegradationHistory(c.Request.Context(), stationID, accountID); err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"results": []storage.AccountDegradationResult{}}})
}

func deleteAccountDegradationResult(c *gin.Context, d *Deps) {
	stationID, accountID, ok := groupDegradationParams(c, d)
	if !ok {
		return
	}
	resultID, err := uintParam(c, "result_id")
	if err != nil || resultID == 0 {
		fail(c, http.StatusBadRequest, errors.New("检测记录 ID 无效"))
		return
	}
	results, err := d.Relay.DeleteAccountDegradationResult(c.Request.Context(), stationID, accountID, resultID)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"results": results}})
}

func getDegradationUsage(c *gin.Context, d *Deps) {
	stationID, ok := groupDegradationStationParam(c, d)
	if !ok {
		return
	}
	page, pageSize := 1, 20
	if value, err := strconv.Atoi(c.Query("page")); err == nil && value > 0 {
		page = value
	}
	if value, err := strconv.Atoi(c.Query("page_size")); err == nil && value > 0 {
		pageSize = value
	}
	usage, err := d.Relay.DegradationUsage(c.Request.Context(), stationID, c.Query("scope"), page, pageSize)
	if err != nil {
		failGroupDegradation(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": usage})
}

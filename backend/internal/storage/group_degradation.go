package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrGroupDegradationDisabled      = errors.New("分组降智检测尚未启用")
	ErrGroupDegradationBusy          = errors.New("降智检测正在运行，请稍后重试")
	ErrInvalidGroupDegradationConfig = errors.New("降智检测配置无效")
	ErrGroupDegradationLeaseLost     = errors.New("降智检测执行租约已失效")
	ErrInvalidDegradationFilter      = errors.New("降智检测历史筛选方式无效")
)

// These rows deliberately have no relation to the replaceable relay_groups snapshot.
// Only model parameters are serialized; scheduling state and credentials are never public.
type GroupDegradationConfig struct {
	ID                     uint       `gorm:"primaryKey" json:"-"`
	StationID              uint       `gorm:"not null;uniqueIndex:idx_group_degradation_scope" json:"-"`
	GroupExternalID        int64      `gorm:"not null;uniqueIndex:idx_group_degradation_scope" json:"-"`
	Enabled                bool       `gorm:"not null;default:false;index:idx_group_degradation_due,priority:1" json:"enabled"`
	Method                 string     `gorm:"size:16;not null;default:pelican;check:chk_group_degradation_method,method IN ('pelican','candy','both')" json:"method"`
	Model                  string     `gorm:"size:256;not null;default:''" json:"model"`
	JudgeModel             string     `gorm:"size:256;not null;default:''" json:"judge_model"`
	Effort                 string     `gorm:"size:16;not null;default:none;check:chk_group_degradation_effort,effort IN ('none','low','medium','high')" json:"effort"`
	IntervalMinutes        int        `gorm:"not null;default:15;check:chk_group_degradation_interval,interval_minutes BETWEEN 5 AND 1440" json:"interval_minutes"`
	PelicanIntervalMinutes int        `gorm:"not null;default:0;check:chk_degradation_pelican_interval,pelican_interval_minutes = 0 OR pelican_interval_minutes BETWEEN 5 AND 1440" json:"pelican_interval_minutes"`
	CandyIntervalMinutes   int        `gorm:"not null;default:0;check:chk_degradation_candy_interval,candy_interval_minutes = 0 OR candy_interval_minutes BETWEEN 5 AND 1440" json:"candy_interval_minutes"`
	RunMethod              string     `gorm:"-" json:"-"`
	Protocol               string     `gorm:"-" json:"-"`
	PreviewMode            string     `gorm:"size:16;not null;default:hover;check:chk_group_degradation_preview,preview_mode IN ('hover','click')" json:"preview_mode"`
	ResultVisibility       string     `gorm:"size:16;not null;default:show;check:chk_group_degradation_visibility,result_visibility IN ('hide','show')" json:"result_visibility"`
	HistoryLimit           int        `gorm:"not null;default:20;check:chk_group_degradation_history_limit,history_limit BETWEEN 1 AND 1000" json:"history_limit"`
	LastRunAt              *time.Time `json:"-"`
	NextRunAt              *time.Time `gorm:"index:idx_group_degradation_due,priority:2" json:"-"`
	PelicanLastRunAt       *time.Time `json:"-"`
	CandyLastRunAt         *time.Time `json:"-"`
	PelicanNextRunAt       *time.Time `json:"-"`
	CandyNextRunAt         *time.Time `json:"-"`
	LeaseToken             string     `gorm:"size:64;not null;default:''" json:"-"`
	LeaseUntil             *time.Time `json:"-"`
	CreatedAt              time.Time  `json:"-"`
	UpdatedAt              time.Time  `json:"-"`
}

func (GroupDegradationConfig) TableName() string { return "group_degradation_configs" }

type GroupDegradationResult struct {
	ID              uint   `gorm:"primaryKey" json:"id"`
	StationID       uint   `gorm:"not null;index:idx_group_degradation_results,priority:1" json:"-"`
	GroupExternalID int64  `gorm:"not null;index:idx_group_degradation_results,priority:2" json:"-"`
	Method          string `gorm:"size:16;not null;check:chk_group_degradation_result_method,method IN ('pelican', 'candy')" json:"method"`
	Model           string `gorm:"size:256;not null" json:"model"`
	JudgeModel      string `gorm:"size:256;not null" json:"judge_model"`
	Effort          string `gorm:"size:16;not null;check:chk_group_degradation_result_effort,effort IN ('none','low','medium','high')" json:"effort"`
	Status          string `gorm:"size:16;not null;check:chk_group_degradation_result_status,status IN ('normal','degraded','error','inconclusive')" json:"status"`
	Reason          string `gorm:"type:text;not null" json:"reason"`
	HTML            string `gorm:"type:text;not null;default:''" json:"html,omitempty"`
	Answer          string `gorm:"type:text;not null;default:''" json:"answer,omitempty"`
	// Provenance of rows that this group's own schedule did not produce. An
	// imported account test keeps its original test time and verdict; a manual
	// override keeps the superseded verdict so the history can still show what
	// the judge said. Both are admin-only and cleared from public reads.
	SourceAccountID   int64      `gorm:"not null;default:0" json:"source_account_id,omitempty"`
	SourceAccountName string     `gorm:"size:256;not null;default:''" json:"source_account_name,omitempty"`
	SourceResultID    uint       `gorm:"not null;default:0" json:"-"`
	OriginalStatus    string     `gorm:"size:16;not null;default:''" json:"original_status,omitempty"`
	AdjustedAt        *time.Time `json:"adjusted_at,omitempty"`
	// Admin-only accounting. The upstream account and Sub2API charge are captured
	// after a run so the usage table never re-queries the station per record.
	// Public reads zero these fields out.
	UsedAccountID   int64      `gorm:"not null;default:0" json:"used_account_id,omitempty"`
	UsedAccountName string     `gorm:"size:256;not null;default:''" json:"used_account_name,omitempty"`
	InputTokens     int64      `gorm:"not null;default:0" json:"input_tokens,omitempty"`
	OutputTokens    int64      `gorm:"not null;default:0" json:"output_tokens,omitempty"`
	BaseCost        float64    `gorm:"not null;default:0" json:"base_cost,omitempty"`
	CostMultiplier  float64    `gorm:"not null;default:0" json:"cost_multiplier,omitempty"`
	ActualCost      float64    `gorm:"not null;default:0" json:"actual_cost,omitempty"`
	StartedAt       *time.Time `json:"-"`
	UsageSyncedAt   *time.Time `json:"-"`
	CreatedAt       time.Time  `gorm:"not null;index:idx_group_degradation_results,priority:3,sort:desc" json:"created_at"`
}

func (GroupDegradationResult) TableName() string { return "group_degradation_results" }

type GroupDegradationView struct {
	Config  GroupDegradationConfig   `json:"config"`
	Results []GroupDegradationResult `json:"results"`
}

const (
	DefaultGroupDegradationHistoryLimit = 20
	MaxGroupDegradationHistoryLimit     = 1000
	PublicGroupDegradationHistoryLimit  = 18
)

func DefaultGroupDegradationConfig() GroupDegradationConfig {
	return GroupDegradationConfig{Method: "pelican", Effort: "none", IntervalMinutes: 15, PreviewMode: "hover", ResultVisibility: "show", HistoryLimit: DefaultGroupDegradationHistoryLimit}
}

// Run before AutoMigrate adds the 1..1000 check to a legacy column. Repairs
// only unset/zero values, so repeated startups preserve positive user choices.
func migrateGroupDegradationHistory(db *gorm.DB) error {
	if !db.Migrator().HasColumn(&GroupDegradationConfig{}, "history_limit") {
		return nil
	}
	return db.Exec(fmt.Sprintf("UPDATE group_degradation_configs SET history_limit = %d WHERE history_limit = 0 OR history_limit IS NULL", DefaultGroupDegradationHistoryLimit)).Error
}

// Zero is the legacy client/database value, never a request to keep no history.
func groupDegradationHistoryLimit(limit int) int {
	if limit == 0 {
		return DefaultGroupDegradationHistoryLimit
	}
	return limit
}

func NormalizeGroupDegradationConfig(c GroupDegradationConfig) (GroupDegradationConfig, error) {
	c.Method = strings.TrimSpace(c.Method)
	c.Model = strings.TrimSpace(c.Model)
	c.JudgeModel = strings.TrimSpace(c.JudgeModel)
	c.Effort = strings.TrimSpace(c.Effort)
	c.PreviewMode = strings.TrimSpace(c.PreviewMode)
	c.ResultVisibility = strings.TrimSpace(c.ResultVisibility)
	if c.Method == "" {
		c.Method = "pelican"
	}
	if c.Effort == "" {
		c.Effort = "none"
	}
	if c.PreviewMode == "" {
		c.PreviewMode = "hover"
	}
	if c.ResultVisibility == "" {
		c.ResultVisibility = "show"
	}
	c.HistoryLimit = groupDegradationHistoryLimit(c.HistoryLimit)
	invalid := func(reason string) (GroupDegradationConfig, error) {
		return c, fmt.Errorf("%w：%s", ErrInvalidGroupDegradationConfig, reason)
	}
	if c.Method != "pelican" && c.Method != "candy" && c.Method != "both" {
		return invalid("method 必须是 pelican、candy 或 both")
	}
	if c.Effort != "none" && c.Effort != "low" && c.Effort != "medium" && c.Effort != "high" {
		return invalid("effort 必须是 none、low、medium 或 high")
	}
	if c.PreviewMode != "hover" && c.PreviewMode != "click" {
		return invalid("preview_mode 必须是 hover 或 click")
	}
	if c.ResultVisibility != "hide" && c.ResultVisibility != "show" {
		return invalid("result_visibility 必须是 hide 或 show")
	}
	if c.IntervalMinutes < 5 || c.IntervalMinutes > 1440 {
		return invalid("interval_minutes 必须在 5 到 1440 之间")
	}
	for _, interval := range []int{c.PelicanIntervalMinutes, c.CandyIntervalMinutes} {
		if interval != 0 && (interval < 5 || interval > 1440) {
			return invalid("鹈鹕与糖果检测间隔必须在 5 到 1440 分钟之间")
		}
	}
	if c.HistoryLimit < 1 || c.HistoryLimit > MaxGroupDegradationHistoryLimit {
		return invalid("history_limit 必须在 1 到 1000 之间")
	}
	for _, model := range []string{c.Model, c.JudgeModel} {
		if len(model) > 256 || strings.ContainsFunc(model, unicode.IsControl) {
			return invalid("模型名称过长或包含控制字符")
		}
	}
	if c.Enabled && c.Model == "" {
		return invalid("启用时必须选择 model")
	}
	return c, nil
}

func (r *RelayStations) groupDegradationScope(ctx context.Context, stationID uint, groupID int64) (*RelayGroup, error) {
	var station RelayStation
	if err := r.db.WithContext(ctx).Select("id").First(&station, stationID).Error; err != nil {
		return nil, err
	}
	var group RelayGroup
	err := r.db.WithContext(ctx).Where("relay_station_id = ? AND external_id = ?", stationID, groupID).First(&group).Error
	return &group, err
}

// GroupDegradationView never creates configuration or performs an upstream request.
func (r *RelayStations) GroupDegradationView(ctx context.Context, stationID uint, groupID int64, public bool) (GroupDegradationView, error) {
	view := GroupDegradationView{Config: DefaultGroupDegradationConfig(), Results: []GroupDegradationResult{}}
	group, err := r.groupDegradationScope(ctx, stationID, groupID)
	if err != nil {
		return view, err
	}
	config := DefaultGroupDegradationConfig()
	err = r.db.WithContext(ctx).Where("station_id = ? AND group_external_id = ?", stationID, groupID).First(&config).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	config.HistoryLimit = groupDegradationHistoryLimit(config.HistoryLimit)
	view.Config = config
	if public && (!group.MonitorEnabled || !config.Enabled) {
		view.Config.Enabled = false
		return view, nil
	}
	if config.HistoryLimit < 1 || config.HistoryLimit > MaxGroupDegradationHistoryLimit {
		return view, ErrInvalidGroupDegradationConfig
	}
	limit := config.HistoryLimit
	if public && limit > PublicGroupDegradationHistoryLimit {
		limit = PublicGroupDegradationHistoryLimit
	}
	// 成功与失败分桶各按上限截取：失败的检测不把名额让给成功的记录。查询
	// 顺序固定（先成功后失败），合并时再按 created_at DESC, id DESC 排序，
	// 与单条 SQL 的顺序完全一致，避免前端“最新一条”随桶切换而跳动。
	build := func(failed bool) *gorm.DB {
		base := r.db.WithContext(ctx).Where("station_id = ? AND group_external_id = ?", stationID, groupID)
		if failed {
			base = base.Where("status = ?", "error")
		} else {
			base = base.Where("status <> ?", "error")
		}
		return base.Order("created_at DESC").Order("id DESC").Limit(limit)
	}
	var success, failed []GroupDegradationResult
	selectFields := ""
	if public && config.ResultVisibility == "hide" {
		selectFields = "id, method, model, judge_model, effort, status, created_at"
	}
	if selectFields != "" {
		if err = build(false).Select(selectFields).Find(&success).Error; err != nil {
			return view, err
		}
		if err = build(true).Select(selectFields).Find(&failed).Error; err != nil {
			return view, err
		}
	} else {
		if err = build(false).Find(&success).Error; err != nil {
			return view, err
		}
		if err = build(true).Find(&failed).Error; err != nil {
			return view, err
		}
	}
	view.Results = mergeDegradationBuckets(success, failed)
	if public && config.ResultVisibility == "hide" {
		for i := range view.Results {
			switch view.Results[i].Status {
			case "normal":
				view.Results[i].Reason = "通过检测"
			case "degraded":
				view.Results[i].Reason = "未通过检测，疑似降智"
			case "error":
				view.Results[i].Reason = "检测失败，本次不判定是否通过"
			default:
				view.Results[i].Reason = "结果待确认，暂无法判定是否通过"
			}
		}
	}
	if public {
		// Accounting and provenance are admin-only: the public page must never
		// learn which upstream account served a run, what it cost, or that a
		// verdict was reviewed by hand.
		for i := range view.Results {
			view.Results[i].UsedAccountID, view.Results[i].UsedAccountName = 0, ""
			view.Results[i].InputTokens, view.Results[i].OutputTokens = 0, 0
			view.Results[i].BaseCost, view.Results[i].CostMultiplier, view.Results[i].ActualCost = 0, 0, 0
			view.Results[i].StartedAt, view.Results[i].UsageSyncedAt = nil, nil
			view.Results[i].SourceAccountID, view.Results[i].SourceAccountName, view.Results[i].SourceResultID = 0, "", 0
			view.Results[i].OriginalStatus, view.Results[i].AdjustedAt = "", nil
		}
	}
	return view, err
}

func (r *RelayStations) SaveGroupDegradationConfig(ctx context.Context, stationID uint, groupID int64, config GroupDegradationConfig) error {
	config, err := NormalizeGroupDegradationConfig(config)
	if err != nil {
		return err
	}
	if _, err := r.groupDegradationScope(ctx, stationID, groupID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seed := DefaultGroupDegradationConfig()
		seed.StationID, seed.GroupExternalID = stationID, groupID
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "station_id"}, {Name: "group_external_id"}}, DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		var current GroupDegradationConfig
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("station_id = ? AND group_external_id = ?", stationID, groupID).First(&current).Error; err != nil {
			return err
		}
		if current.LeaseUntil != nil && current.LeaseUntil.After(time.Now()) {
			return ErrGroupDegradationBusy
		}
		pelicanNext, candyNext := degradationSchedule(current)
		pelicanInterval, candyInterval := DegradationIntervals(config)
		if !current.Enabled || !config.Enabled {
			pelicanNext, candyNext = nil, nil
		} else {
			oldPelican, oldCandy := DegradationIntervals(current)
			if oldPelican != pelicanInterval && current.PelicanLastRunAt != nil {
				t := current.PelicanLastRunAt.Add(time.Duration(pelicanInterval) * time.Minute)
				pelicanNext = &t
			}
			if oldCandy != candyInterval && current.CandyLastRunAt != nil {
				t := current.CandyLastRunAt.Add(time.Duration(candyInterval) * time.Minute)
				candyNext = &t
			}
			if current.PelicanLastRunAt == nil && current.CandyLastRunAt == nil && current.LastRunAt != nil {
				p := current.LastRunAt.Add(time.Duration(pelicanInterval) * time.Minute)
				c := current.LastRunAt.Add(time.Duration(candyInterval) * time.Minute)
				pelicanNext, candyNext = &p, &c
			}
			if !degradationMethodEnabled(config.Method, "pelican") {
				pelicanNext = nil
			} else if pelicanNext == nil {
				t := time.Now().Add(time.Duration(pelicanInterval) * time.Minute)
				pelicanNext = &t
			}
			if !degradationMethodEnabled(config.Method, "candy") {
				candyNext = nil
			} else if candyNext == nil {
				t := time.Now().Add(time.Duration(candyInterval) * time.Minute)
				candyNext = &t
			}
		}
		if config.Enabled && !current.Enabled {
			// A group that is only now enabled has no cadence to keep: make the
			// first check due right away instead of one whole interval later.
			now := time.Now()
			if degradationMethodEnabled(config.Method, "pelican") {
				first := now
				pelicanNext = &first
			}
			if degradationMethodEnabled(config.Method, "candy") {
				first := now
				candyNext = &first
			}
		}
		next := earliestSelectedDegradationRun(config.Method, pelicanNext, candyNext)
		if err := tx.Model(&current).Updates(map[string]any{
			"enabled": config.Enabled, "method": config.Method, "model": config.Model,
			"judge_model": config.JudgeModel, "effort": config.Effort,
			"interval_minutes": config.IntervalMinutes, "preview_mode": config.PreviewMode,
			"result_visibility": config.ResultVisibility, "history_limit": config.HistoryLimit,
			"pelican_interval_minutes": config.PelicanIntervalMinutes, "candy_interval_minutes": config.CandyIntervalMinutes,
			"pelican_next_run_at": pelicanNext, "candy_next_run_at": candyNext,
			"next_run_at": next, "lease_token": "", "lease_until": nil,
		}).Error; err != nil {
			return err
		}
		return pruneGroupDegradationHistory(tx, stationID, groupID, config.HistoryLimit)
	})
}

// Call only while holding this scope's configuration row lock. Ordering by ID
// breaks equal timestamp ties; both sides of the delete remain tenant-scoped.
func pruneGroupDegradationHistory(tx *gorm.DB, stationID uint, groupID int64, limit int) error {
	limit = groupDegradationHistoryLimit(limit)
	if limit < 1 || limit > MaxGroupDegradationHistoryLimit {
		return ErrInvalidGroupDegradationConfig
	}
	staleFrom := func(failed bool) *gorm.DB {
		q := tx.Model(&GroupDegradationResult{}).Select("id").
			Where("station_id = ? AND group_external_id = ?", stationID, groupID)
		if failed {
			q = q.Where("status = ?", "error")
		} else {
			q = q.Where("status <> ?", "error")
		}
		return q.Order("created_at DESC").Order("id DESC").Offset(limit)
	}
	// Either bucket can contain expired rows. Intersecting the two disjoint
	// status buckets would delete nothing; keep their union tenant-scoped.
	return tx.Where("station_id = ? AND group_external_id = ?", stationID, groupID).
		Where("(id IN (?) OR id IN (?))", staleFrom(false), staleFrom(true)).
		Delete(&GroupDegradationResult{}).Error
}

// ClearGroupDegradationHistory shares the claim/completion row lock and leaves
// configuration, scheduling state, and station-owned prompts untouched.
func (r *RelayStations) ClearGroupDegradationHistory(ctx context.Context, stationID uint, groupID int64, filter string) (GroupDegradationView, error) {
	view := GroupDegradationView{Config: DefaultGroupDegradationConfig(), Results: []GroupDegradationResult{}}
	if _, err := r.groupDegradationScope(ctx, stationID, groupID); err != nil {
		return view, err
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		config := DefaultGroupDegradationConfig()
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("station_id = ? AND group_external_id = ?", stationID, groupID).First(&config).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if config.LeaseUntil != nil && config.LeaseUntil.After(time.Now()) {
			return ErrGroupDegradationBusy
		}
		config.HistoryLimit = groupDegradationHistoryLimit(config.HistoryLimit)
		view.Config = config
		query := tx
		switch strings.TrimSpace(filter) {
		case "", "all":
			// No narrowing clause: the whole scoped history is cleared.
		case "error":
			query = query.Where("status = ?", "error")
		case "success":
			query = query.Where("status <> ?", "error")
		case "pelican", "candy":
			query = query.Where("method = ?", filter)
		default:
			return ErrInvalidDegradationFilter
		}
		return query.Where("station_id = ? AND group_external_id = ?", stationID, groupID).Delete(&GroupDegradationResult{}).Error
	})
	return view, err
}

// DeleteGroupDegradationResult removes one history row. The record is scoped to
// the station and group, so an id from another scope cannot be deleted and a
// missing row is not an error. Configuration and scheduling stay untouched.
func (r *RelayStations) DeleteGroupDegradationResult(ctx context.Context, stationID uint, groupID int64, resultID uint) error {
	if _, err := r.groupDegradationScope(ctx, stationID, groupID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Where("id = ? AND station_id = ? AND group_external_id = ?", resultID, stationID, groupID).
		Delete(&GroupDegradationResult{}).Error
}

func ValidGroupDegradationStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "normal", "degraded", "inconclusive", "error":
		return true
	}
	return false
}

// UpdateGroupDegradationResult overwrites the verdict of one history row, for
// the case where an admin reviewed the evidence and disagrees with the judge.
// The judge's reason is never rewritten: OriginalStatus keeps the superseded
// verdict so the row still shows what was overridden. Setting the row back to
// its automatic verdict clears the manual marker instead of stacking overrides.
func (r *RelayStations) UpdateGroupDegradationResult(ctx context.Context, stationID uint, groupID int64, resultID uint, status string) error {
	status = strings.TrimSpace(status)
	if !ValidGroupDegradationStatus(status) {
		return ErrInvalidGroupDegradationConfig
	}
	if _, err := r.groupDegradationScope(ctx, stationID, groupID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current GroupDegradationResult
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND station_id = ? AND group_external_id = ?", resultID, stationID, groupID).First(&current).Error; err != nil {
			return err
		}
		original := current.OriginalStatus
		if original == "" {
			original = current.Status
		}
		if status == original {
			return tx.Model(&current).Updates(map[string]any{"status": status, "original_status": "", "adjusted_at": nil}).Error
		}
		return tx.Model(&current).Updates(map[string]any{"status": status, "original_status": original, "adjusted_at": time.Now()}).Error
	})
}

// ImportAccountDegradationResult copies one account test into a group history so
// a manual account run can be published on the group monitor page. The copy
// keeps the original test time and verdict, and drops the accounting columns on
// purpose: the same run is already reported from the account table, so copying
// tokens or cost here would double count the usage report. Importing the same
// account result twice returns the existing row instead of a duplicate.
func (r *RelayStations) ImportAccountDegradationResult(ctx context.Context, stationID uint, groupID int64, accountID int64, resultID uint) (GroupDegradationResult, bool, error) {
	var imported GroupDegradationResult
	if _, err := r.groupDegradationScope(ctx, stationID, groupID); err != nil {
		return imported, false, err
	}
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source AccountDegradationResult
		if err := tx.Where("id = ? AND station_id = ? AND account_external_id = ?", resultID, stationID, accountID).First(&source).Error; err != nil {
			return err
		}
		var existing GroupDegradationResult
		err := tx.Where("station_id = ? AND group_external_id = ? AND source_result_id = ?", stationID, groupID, resultID).First(&existing).Error
		if err == nil {
			imported = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		name := source.UsedAccountName
		var account RelayAccount
		if err := tx.Select("name").Where("relay_station_id = ? AND external_id = ?", stationID, accountID).First(&account).Error; err == nil && account.Name != "" {
			name = account.Name
		}
		copied := GroupDegradationResult{
			StationID: stationID, GroupExternalID: groupID,
			Method: source.Method, Model: source.Model, JudgeModel: source.JudgeModel, Effort: source.Effort,
			Status: source.Status, Reason: source.Reason, HTML: source.HTML, Answer: source.Answer,
			UsedAccountID: source.UsedAccountID, UsedAccountName: source.UsedAccountName,
			CreatedAt:       source.CreatedAt,
			SourceAccountID: accountID, SourceAccountName: name, SourceResultID: resultID,
		}
		if copied.CreatedAt.IsZero() {
			copied.CreatedAt = time.Now()
		}
		if err := tx.Create(&copied).Error; err != nil {
			return err
		}
		imported, created = copied, true
		return nil
	})
	return imported, created, err
}

// SaveGroupDegradationVerdict stamps a fresh machine verdict onto one history
// row during a re-review. Unlike the manual override path, there is no superseded
// verdict worth keeping: the re-judge outcome replaces the row outright, clears
// any earlier manual marker, and leaves the stored evidence untouched.
func (r *RelayStations) SaveGroupDegradationVerdict(ctx context.Context, stationID uint, groupID int64, resultID uint, status, reason string) error {
	status = strings.TrimSpace(status)
	if !ValidGroupDegradationStatus(status) {
		return ErrInvalidGroupDegradationConfig
	}
	if _, err := r.groupDegradationScope(ctx, stationID, groupID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&GroupDegradationResult{}).
		Where("id = ? AND station_id = ? AND group_external_id = ?", resultID, stationID, groupID).
		Updates(map[string]any{
			"status": status, "reason": reason,
			"original_status": "", "adjusted_at": nil,
		}).Error
}

// GroupDegradationResultByID reads one archived history row for a re-review.
// The read is scoped to the station and group so a foreign id is reported as
// missing instead of leaking another group's evidence.
func (r *RelayStations) GroupDegradationResultByID(ctx context.Context, stationID uint, groupID int64, resultID uint) (GroupDegradationResult, error) {
	var result GroupDegradationResult
	if _, err := r.groupDegradationScope(ctx, stationID, groupID); err != nil {
		return result, err
	}
	err := r.db.WithContext(ctx).
		Where("id = ? AND station_id = ? AND group_external_id = ?", resultID, stationID, groupID).
		First(&result).Error
	return result, err
}

// GroupDegradationConfigFor reads the config needed to re-run one group's
// degradation check. History is deliberately not loaded: the re-review flow
// reads the archived row separately, so this call stays a single scoped read.
func (r *RelayStations) GroupDegradationConfigFor(ctx context.Context, stationID uint, groupID int64) (GroupDegradationConfig, error) {
	var config GroupDegradationConfig
	if _, err := r.groupDegradationScope(ctx, stationID, groupID); err != nil {
		return config, err
	}
	err := r.db.WithContext(ctx).
		Where("station_id = ? AND group_external_id = ?", stationID, groupID).
		First(&config).Error
	return config, err
}

// mergeDegradationBuckets restores the single created_at DESC, id DESC order a
// one-query fetch would have produced. The two-bucket reads each cap their own
// limit so failing checks never evict other verdicts; equal timestamps tie-break
// on id, mirroring ORDER BY created_at DESC, id DESC.
func mergeDegradationBuckets(success, failed []GroupDegradationResult) []GroupDegradationResult {
	merged := make([]GroupDegradationResult, 0, len(success)+len(failed))
	merged = append(merged, success...)
	merged = append(merged, failed...)
	sort.SliceStable(merged, func(i, j int) bool {
		if !merged[i].CreatedAt.Equal(merged[j].CreatedAt) {
			return merged[i].CreatedAt.After(merged[j].CreatedAt)
		}
		return merged[i].ID > merged[j].ID
	})
	return merged
}
func newGroupDegradationLease() (string, error) {
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}

func claimGroupDegradation(tx *gorm.DB, c *GroupDegradationConfig, now time.Time, lease time.Duration) error {
	token, err := newGroupDegradationLease()
	if err != nil {
		return err
	}
	until := now.Add(lease)
	pelicanNext, candyNext := degradationSchedule(*c)
	pelicanInterval, candyInterval := DegradationIntervals(*c)
	method, skipCandy := selectDegradationMethod(c.Method, pelicanNext, candyNext, now)
	if c.RunMethod != "" && degradationMethodEnabled(c.Method, c.RunMethod) {
		method, skipCandy = c.RunMethod, false
	}
	if method == "" {
		return ErrGroupDegradationDisabled
	}
	runMethod := method
	if method == "pelican" {
		pelicanNext = advanceDegradationRun(pelicanNext, now, pelicanInterval)
		if skipCandy {
			candyNext = advanceDegradationRun(candyNext, now, candyInterval)
		}
	} else {
		candyNext = advanceDegradationRun(candyNext, now, candyInterval)
	}
	next := earliestSelectedDegradationRun(c.Method, pelicanNext, candyNext)
	if err := tx.Model(c).Updates(map[string]any{"lease_token": token, "lease_until": until, "next_run_at": next, "pelican_next_run_at": pelicanNext, "candy_next_run_at": candyNext}).Error; err != nil {
		return err
	}
	c.LeaseToken, c.LeaseUntil, c.NextRunAt, c.RunMethod = token, &until, next, runMethod
	c.PelicanNextRunAt, c.CandyNextRunAt = pelicanNext, candyNext
	return nil
}

func (r *RelayStations) ClaimGroupDegradation(ctx context.Context, stationID uint, groupID int64, lease time.Duration) (GroupDegradationConfig, error) {
	return r.claimGroupDegradationMethod(ctx, stationID, groupID, "", lease)
}

func (r *RelayStations) ClaimGroupDegradationMethod(ctx context.Context, stationID uint, groupID int64, method string, lease time.Duration) (GroupDegradationConfig, error) {
	return r.claimGroupDegradationMethod(ctx, stationID, groupID, method, lease)
}

func (r *RelayStations) claimGroupDegradationMethod(ctx context.Context, stationID uint, groupID int64, requestedMethod string, lease time.Duration) (GroupDegradationConfig, error) {
	var config GroupDegradationConfig
	if _, err := r.groupDegradationScope(ctx, stationID, groupID); err != nil {
		return config, err
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("station_id = ? AND group_external_id = ?", stationID, groupID).First(&config).Error; err != nil {
			return err
		}
		if !config.Enabled {
			return ErrGroupDegradationDisabled
		}
		if config.LeaseUntil != nil && config.LeaseUntil.After(time.Now()) {
			return ErrGroupDegradationBusy
		}
		if requestedMethod != "" {
			if !degradationMethodEnabled(config.Method, requestedMethod) {
				return ErrGroupDegradationDisabled
			}
			config.RunMethod = requestedMethod
			if requestedMethod == "pelican" {
				config.IntervalMinutes, _ = DegradationIntervals(config)
			} else {
				_, config.IntervalMinutes = DegradationIntervals(config)
			}
		}
		return claimGroupDegradation(tx, &config, time.Now(), lease)
	})
	return config, err
}

// ClaimDueGroupDegradations holds row locks only while claiming, never during HTTP.
func (r *RelayStations) ClaimDueGroupDegradations(ctx context.Context, limit int, lease time.Duration) ([]GroupDegradationConfig, error) {
	configs := []GroupDegradationConfig{}
	if limit <= 0 {
		return configs, nil
	}
	if limit > 4 {
		limit = 4
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("enabled = ? AND method IN ('pelican','candy','both') AND (next_run_at IS NULL OR next_run_at <= ?) AND (lease_until IS NULL OR lease_until <= ?)", true, now, now).
			Where("EXISTS (SELECT 1 FROM relay_stations s WHERE s.id = group_degradation_configs.station_id AND s.deleted_at IS NULL)").
			Where("EXISTS (SELECT 1 FROM relay_groups g WHERE g.relay_station_id = group_degradation_configs.station_id AND g.external_id = group_degradation_configs.group_external_id)").
			Order("next_run_at ASC NULLS FIRST").Order("id ASC").Limit(limit).Find(&configs).Error; err != nil {
			return err
		}
		for i := range configs {
			if err := claimGroupDegradation(tx, &configs[i], now, lease); err != nil {
				return err
			}
		}
		return nil
	})
	return configs, err
}

// CompleteGroupDegradation atomically saves the result and marks completion.
func (r *RelayStations) CompleteGroupDegradation(ctx context.Context, claimed GroupDegradationConfig, result *GroupDegradationResult) error {
	if claimed.ID == 0 || claimed.StationID == 0 || claimed.GroupExternalID <= 0 || result == nil {
		return ErrGroupDegradationLeaseLost
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current GroupDegradationConfig
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("station_id = ? AND group_external_id = ?", claimed.StationID, claimed.GroupExternalID).First(&current, claimed.ID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrGroupDegradationLeaseLost
		}
		if err != nil {
			return err
		}
		now := time.Now()
		if current.ID != claimed.ID || current.StationID != claimed.StationID || current.GroupExternalID != claimed.GroupExternalID || claimed.LeaseToken == "" || current.LeaseToken != claimed.LeaseToken || current.LeaseUntil == nil || !current.LeaseUntil.After(now) {
			return ErrGroupDegradationLeaseLost
		}
		result.StationID, result.GroupExternalID, result.CreatedAt = current.StationID, current.GroupExternalID, now
		// 常规轮询产生的记录不携带导入/复评溯源；Omit 让 INSERT 保持与断言
		// 一致的参数顺序，数据库仍为这些列提供默认值。
		if err := tx.Omit("source_account_id", "source_account_name", "source_result_id", "original_status", "adjusted_at").Create(result).Error; err != nil {
			return err
		}
		if err := pruneGroupDegradationHistory(tx, current.StationID, current.GroupExternalID, current.HistoryLimit); err != nil {
			return err
		}
		runMethod := claimed.RunMethod
		if runMethod == "" {
			runMethod = claimed.Method
		}
		updates := map[string]any{"last_run_at": now, "lease_token": "", "lease_until": nil}
		if runMethod == "pelican" {
			updates["pelican_last_run_at"] = now
		} else {
			updates["candy_last_run_at"] = now
		}
		return tx.Model(&current).Updates(updates).Error
	})
}

// DegradationIntervals keeps old single-method configurations meaningful on upgrade.
func DegradationIntervals(c GroupDegradationConfig) (int, int) {
	pelican, candy := c.PelicanIntervalMinutes, c.CandyIntervalMinutes
	if pelican == 0 && candy == 0 && c.IntervalMinutes >= 5 && c.Method != "both" {
		if c.Method == "candy" {
			candy = c.IntervalMinutes
		} else {
			pelican = c.IntervalMinutes
		}
	}
	if pelican == 0 {
		pelican = 60
	}
	if candy == 0 {
		candy = 10
	}
	return pelican, candy
}

func degradationSchedule(c GroupDegradationConfig) (*time.Time, *time.Time) {
	pelican, candy := c.PelicanNextRunAt, c.CandyNextRunAt
	pelicanInterval, candyInterval := DegradationIntervals(c)
	if pelican == nil && c.LastRunAt != nil {
		t := c.LastRunAt.Add(time.Duration(pelicanInterval) * time.Minute)
		pelican = &t
	}
	if candy == nil && c.LastRunAt != nil {
		t := c.LastRunAt.Add(time.Duration(candyInterval) * time.Minute)
		candy = &t
	}
	return pelican, candy
}

func earliestDegradationRun(pelican, candy *time.Time) *time.Time {
	if pelican == nil || candy == nil {
		return nil
	}
	if pelican.Before(*candy) {
		return pelican
	}
	return candy
}

func earliestSelectedDegradationRun(method string, pelican, candy *time.Time) *time.Time {
	switch method {
	case "pelican":
		return pelican
	case "candy":
		return candy
	default:
		return earliestDegradationRun(pelican, candy)
	}
}

func degradationMethodEnabled(selection, method string) bool {
	return selection == "both" || selection == method
}

func selectDegradationMethod(selection string, pelican, candy *time.Time, now time.Time) (string, bool) {
	endOfMinute := now.Truncate(time.Minute).Add(time.Minute)
	pelicanEnabled := degradationMethodEnabled(selection, "pelican")
	candyEnabled := degradationMethodEnabled(selection, "candy")
	pelicanDue := pelicanEnabled && (pelican == nil || !pelican.After(now))
	candyDue := candyEnabled && (candy == nil || !candy.After(now))
	if candyDue && pelicanEnabled && pelican != nil && pelican.Before(endOfMinute) {
		pelicanDue = true
	}
	if pelicanDue {
		return "pelican", candyEnabled && (candy == nil || candy.Before(endOfMinute))
	}
	if candyDue {
		return "candy", false
	}
	if pelicanEnabled {
		return "pelican", false
	}
	if candyEnabled {
		return "candy", false
	}
	return "", false
}

// advanceDegradationRun moves one method's slot forward. A run that starts
// before a pending slot (a manual 「立即检测」 or an early tick) must not consume
// it, otherwise every manual test pushes the next automatic check a whole
// interval further away.
func advanceDegradationRun(previous *time.Time, now time.Time, interval int) *time.Time {
	step := time.Duration(interval) * time.Minute
	if previous != nil && previous.After(now) {
		return previous
	}
	next := now.Add(step)
	if previous != nil {
		next = previous.Add(step)
		if !next.After(now) {
			next = next.Add((now.Sub(next)/step + 1) * step)
		}
	}
	return &next
}

func DegradationMethodEnabled(selection, method string) bool {
	return degradationMethodEnabled(selection, method)
}

func DegradationMethodInterval(c GroupDegradationConfig, method string) int {
	pelican, candy := DegradationIntervals(c)
	if method == "candy" {
		return candy
	}
	return pelican
}

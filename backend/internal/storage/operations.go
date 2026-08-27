package storage

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	LedgerSourceManual       = "manual"
	LedgerSourceLocalAccount = "local_account"
	LedgerCategoryPurchase   = "account_purchase"
)

var ErrInvalidOperationInput = errors.New("运营数据无效")
var ErrOperationConflict = errors.New("运营数据冲突")

var validLocalAccountStatuses = map[string]bool{
	"pending":  true,
	"ready":    true,
	"deployed": true,
	"disabled": true,
	"retired":  true,
}

type Operations struct{ db *gorm.DB }

func NewOperations(db *gorm.DB) *Operations { return &Operations{db: db} }

type LedgerFilter struct {
	Since     time.Time
	Direction string
	Category  string
	Limit     int
}

type LedgerSummary struct {
	IncomeAmount           float64 `json:"income_amount"`
	ExpenseAmount          float64 `json:"expense_amount"`
	NetAmount              float64 `json:"net_amount"`
	AccountPurchaseAmount  float64 `json:"account_purchase_amount"`
	UpstreamRechargeAmount float64 `json:"upstream_recharge_amount"`
	EntryCount             int64   `json:"entry_count"`
	RelayRevenueAmount     float64 `json:"relay_revenue_amount"`
}

type LedgerCategorySummary struct {
	Direction string  `json:"direction"`
	Category  string  `json:"category"`
	Amount    float64 `json:"amount"`
	Count     int64   `json:"count"`
}

type OperationLedgerEntryView struct {
	OperationLedgerEntry
	ChannelName      string `json:"channel_name,omitempty"`
	RelayStationName string `json:"relay_station_name,omitempty"`
}

func (o *Operations) ListLedger(filter LedgerFilter) ([]OperationLedgerEntryView, error) {
	query := o.db.Table("operation_ledger_entries AS entry").
		Select(`entry.*,
			COALESCE(channel.name, '') AS channel_name,
			COALESCE(station.name, '') AS relay_station_name`).
		Joins("LEFT JOIN channels AS channel ON channel.id = entry.channel_id AND channel.deleted_at IS NULL").
		Joins("LEFT JOIN relay_stations AS station ON station.id = entry.relay_station_id AND station.deleted_at IS NULL")
	if !filter.Since.IsZero() {
		query = query.Where("entry.occurred_at >= ?", filter.Since)
	}
	if filter.Direction != "" && filter.Direction != "all" {
		query = query.Where("entry.direction = ?", filter.Direction)
	}
	if filter.Category != "" && filter.Category != "all" {
		query = query.Where("entry.category = ?", filter.Category)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	list := make([]OperationLedgerEntryView, 0)
	err := query.Order("entry.occurred_at DESC, entry.id DESC").Limit(limit).Scan(&list).Error
	return list, err
}

func (o *Operations) LedgerSummary(since time.Time) (LedgerSummary, []LedgerCategorySummary, error) {
	var summary LedgerSummary
	summaryQuery := o.db.Model(&OperationLedgerEntry{}).
		Select(`
			COALESCE(SUM(CASE WHEN direction = 'income' THEN amount ELSE 0 END), 0) AS income_amount,
			COALESCE(SUM(CASE WHEN direction = 'expense' THEN amount ELSE 0 END), 0) AS expense_amount,
			COALESCE(SUM(CASE WHEN direction = 'income' THEN amount ELSE -amount END), 0) AS net_amount,
			COALESCE(SUM(CASE WHEN category = 'account_purchase' THEN amount ELSE 0 END), 0) AS account_purchase_amount,
			COALESCE(SUM(CASE WHEN category = 'upstream_recharge' THEN amount ELSE 0 END), 0) AS upstream_recharge_amount,
			COUNT(*) AS entry_count
		`)
	if !since.IsZero() {
		summaryQuery = summaryQuery.Where("occurred_at >= ?", since)
	}
	if err := summaryQuery.Scan(&summary).Error; err != nil {
		return LedgerSummary{}, nil, err
	}
	breakdown := make([]LedgerCategorySummary, 0)
	breakdownQuery := o.db.Model(&OperationLedgerEntry{}).
		Select("direction, category, COALESCE(SUM(amount), 0) AS amount, COUNT(*) AS count").
		Group("direction, category").
		Order("amount DESC")
	if !since.IsZero() {
		breakdownQuery = breakdownQuery.Where("occurred_at >= ?", since)
	}
	if err := breakdownQuery.Scan(&breakdown).Error; err != nil {
		return LedgerSummary{}, nil, err
	}
	return summary, breakdown, nil
}

// ChannelRechargeTotals returns all-time upstream recharge amounts linked to
// each monitored channel. Unlinked ledger entries are intentionally omitted.
func (o *Operations) ChannelRechargeTotals() (map[uint]float64, error) {
	type row struct {
		ChannelID uint
		Amount    float64
	}
	rows := make([]row, 0)
	if err := o.db.Model(&OperationLedgerEntry{}).
		Select("channel_id, COALESCE(SUM(amount), 0) AS amount").
		Where("channel_id IS NOT NULL AND direction = ? AND category = ?", "expense", "upstream_recharge").
		Group("channel_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	totals := make(map[uint]float64, len(rows))
	for _, item := range rows {
		totals[item.ChannelID] = item.Amount
	}
	return totals, nil
}

func (o *Operations) CreateLedger(entry *OperationLedgerEntry) error {
	entry.Source = LedgerSourceManual
	entry.LocalAccountID = nil
	if err := validateLedgerReferences(o.db, entry); err != nil {
		return err
	}
	return o.db.Create(entry).Error
}

func (o *Operations) UpdateLedger(entry *OperationLedgerEntry) error {
	var current OperationLedgerEntry
	if err := o.db.First(&current, entry.ID).Error; err != nil {
		return err
	}
	if current.Source != LedgerSourceManual {
		return errors.New("自动采购成本记录需要在本地号池中修改")
	}
	if err := validateLedgerReferences(o.db, entry); err != nil {
		return err
	}
	return o.db.Model(&current).Updates(map[string]any{
		"direction": entry.Direction, "category": entry.Category,
		"amount": entry.Amount, "currency": entry.Currency,
		"description": entry.Description, "channel_id": entry.ChannelID,
		"relay_station_id": entry.RelayStationID, "occurred_at": entry.OccurredAt,
	}).Error
}

func validateLedgerReferences(tx *gorm.DB, entry *OperationLedgerEntry) error {
	if entry.ChannelID != nil {
		var count int64
		if err := tx.Model(&Channel{}).Where("id = ? AND deleted_at IS NULL", *entry.ChannelID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("%w：关联渠道不存在", ErrInvalidOperationInput)
		}
	}
	if entry.RelayStationID != nil {
		var count int64
		if err := tx.Model(&RelayStation{}).Where("id = ? AND deleted_at IS NULL", *entry.RelayStationID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("%w：关联中转站不存在", ErrInvalidOperationInput)
		}
	}
	return nil
}

func (o *Operations) DeleteLedger(id uint) error {
	var current OperationLedgerEntry
	if err := o.db.First(&current, id).Error; err != nil {
		return err
	}
	if current.Source != LedgerSourceManual {
		return errors.New("自动采购成本记录需要在本地号池中删除或修改")
	}
	return o.db.Delete(&current).Error
}

type LocalAccountFilter struct {
	Status   string
	Platform string
	Query    string
}

type LocalAccountView struct {
	LocalAccount
	RelayStationName  string   `json:"relay_station_name,omitempty"`
	RelayAccountName  string   `json:"relay_account_name,omitempty"`
	RelayStatus       string   `json:"relay_status,omitempty"`
	RelaySchedulable  *bool    `json:"relay_schedulable,omitempty"`
	RelayConcurrency  *int     `json:"relay_concurrency,omitempty"`
	RelayPriority     *int     `json:"relay_priority,omitempty"`
	RelayCost         *float64 `json:"relay_cost,omitempty"`
	RelayGroupNames   string   `json:"relay_group_names,omitempty"`
	RelaySnapshotMiss bool     `json:"relay_snapshot_missing"`
}

type LocalAccountSummary struct {
	TotalCount         int64   `json:"total_count"`
	ReadyCount         int64   `json:"ready_count"`
	DeployedCount      int64   `json:"deployed_count"`
	DisabledCount      int64   `json:"disabled_count"`
	UnlinkedCount      int64   `json:"unlinked_count"`
	PurchaseCost       float64 `json:"purchase_cost"`
	ActivePurchaseCost float64 `json:"active_purchase_cost"`
	ExpectedQuota      float64 `json:"expected_quota"`
}

func (o *Operations) ListLocalAccounts(filter LocalAccountFilter) ([]LocalAccountView, LocalAccountSummary, error) {
	query := o.db.Table("local_accounts AS local").
		Select(`local.*,
			COALESCE(station.name, '') AS relay_station_name,
			COALESCE(account.name, '') AS relay_account_name,
			COALESCE(account.status, '') AS relay_status,
			account.schedulable AS relay_schedulable,
			account.concurrency AS relay_concurrency,
			account.priority AS relay_priority,
			account.rate_multiplier AS relay_cost,
			COALESCE((
				SELECT STRING_AGG(relay_groups.name, '、' ORDER BY relay_groups.name)
				FROM relay_account_groups
				JOIN relay_groups ON relay_groups.id = relay_account_groups.relay_group_id
				WHERE relay_account_groups.relay_account_id = account.id
			), '') AS relay_group_names,
			(local.relay_station_id IS NOT NULL AND account.id IS NULL) AS relay_snapshot_miss`).
		Joins("LEFT JOIN relay_stations AS station ON station.id = local.relay_station_id AND station.deleted_at IS NULL").
		Joins("LEFT JOIN relay_accounts AS account ON account.relay_station_id = local.relay_station_id AND account.external_id = local.relay_account_external_id")
	if filter.Status != "" && filter.Status != "all" {
		query = query.Where("local.status = ?", filter.Status)
	}
	if filter.Platform != "" && filter.Platform != "all" {
		query = query.Where("local.platform = ?", normalizeLocalAccountText(filter.Platform))
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		query = query.Where("LOWER(local.name) LIKE ? OR LOWER(local.identifier) LIKE ? OR LOWER(local.notes) LIKE ?", like, like, like)
	}
	list := make([]LocalAccountView, 0)
	if err := query.Order("local.purchased_at DESC, local.id DESC").Scan(&list).Error; err != nil {
		return nil, LocalAccountSummary{}, err
	}
	var summary LocalAccountSummary
	if err := o.db.Model(&LocalAccount{}).Select(`
		COUNT(*) AS total_count,
		COUNT(*) FILTER (WHERE status = 'ready') AS ready_count,
		COUNT(*) FILTER (WHERE status = 'deployed') AS deployed_count,
		COUNT(*) FILTER (WHERE status = 'disabled') AS disabled_count,
		COUNT(*) FILTER (WHERE relay_station_id IS NULL OR relay_account_external_id IS NULL) AS unlinked_count,
		COALESCE(SUM(purchase_cost), 0) AS purchase_cost,
		COALESCE(SUM(CASE WHEN status NOT IN ('retired') THEN purchase_cost ELSE 0 END), 0) AS active_purchase_cost,
		COALESCE(SUM(CASE WHEN status NOT IN ('retired') THEN expected_quota ELSE 0 END), 0) AS expected_quota
	`).Scan(&summary).Error; err != nil {
		return nil, LocalAccountSummary{}, err
	}
	return list, summary, nil
}

func (o *Operations) ListLocalAccountPlatforms() ([]string, error) {
	platforms := make([]string, 0)
	err := o.db.Model(&LocalAccount{}).Distinct().Order("platform ASC").Pluck("platform", &platforms).Error
	return platforms, err
}

func (o *Operations) CreateLocalAccounts(accounts []LocalAccount) error {
	return o.db.Transaction(func(tx *gorm.DB) error {
		seen := make(map[string]bool, len(accounts))
		for i := range accounts {
			account := &accounts[i]
			if err := prepareLocalAccount(tx, account, nil); err != nil {
				return err
			}
			identity := account.Platform + "\x00" + account.Identifier
			if seen[identity] {
				return fmt.Errorf("%w：批量内容中存在重复账号 %s / %s", ErrOperationConflict, account.Platform, account.Identifier)
			}
			seen[identity] = true
			if err := ensureLocalAccountIdentityAvailable(tx, account); err != nil {
				return err
			}
			if err := tx.Create(account).Error; err != nil {
				return err
			}
			if err := syncLocalAccountLedger(tx, account); err != nil {
				return err
			}
		}
		return nil
	})
}

func (o *Operations) UpdateLocalAccount(account *LocalAccount) error {
	return o.db.Transaction(func(tx *gorm.DB) error {
		var current LocalAccount
		if err := tx.First(&current, account.ID).Error; err != nil {
			return err
		}
		if err := prepareLocalAccount(tx, account, &current); err != nil {
			return err
		}
		if err := ensureLocalAccountIdentityAvailable(tx, account); err != nil {
			return err
		}
		if err := tx.Model(&LocalAccount{}).Where("id = ?", account.ID).Updates(map[string]any{
			"name": account.Name, "identifier": account.Identifier, "platform": account.Platform,
			"account_type": account.AccountType, "status": account.Status,
			"purchase_cost": account.PurchaseCost, "expected_quota": account.ExpectedQuota,
			"purchased_at": account.PurchasedAt, "expires_at": account.ExpiresAt, "notes": account.Notes,
			"relay_station_id": account.RelayStationID, "relay_account_external_id": account.RelayAccountExternalID,
			"linked_at": account.LinkedAt,
		}).Error; err != nil {
			return err
		}
		return syncLocalAccountLedger(tx, account)
	})
}

func ensureLocalAccountIdentityAvailable(tx *gorm.DB, account *LocalAccount) error {
	query := tx.Model(&LocalAccount{}).Where("platform = ? AND identifier = ?", account.Platform, account.Identifier)
	if account.ID != 0 {
		query = query.Where("id <> ?", account.ID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("%w：账号标识 %s / %s 已存在", ErrOperationConflict, account.Platform, account.Identifier)
	}
	return nil
}

func prepareLocalAccount(tx *gorm.DB, account *LocalAccount, current *LocalAccount) error {
	normalizeLocalAccount(account)
	if account.Name == "" || account.Identifier == "" || account.Platform == "" {
		return fmt.Errorf("%w：本地账号名称、唯一标识和平台不能为空", ErrInvalidOperationInput)
	}
	if account.PurchaseCost < 0 || account.ExpectedQuota < 0 {
		return fmt.Errorf("%w：账号 %s 的采购成本和预期额度不能小于 0", ErrInvalidOperationInput, account.Name)
	}
	if !validLocalAccountStatuses[account.Status] {
		return fmt.Errorf("%w：账号 %s 状态无效", ErrInvalidOperationInput, account.Name)
	}
	if account.RelayStationID == nil && account.RelayAccountExternalID == nil {
		account.LinkedAt = nil
		if account.Status == "deployed" {
			account.Status = "ready"
		}
		return nil
	}
	if account.RelayStationID == nil || account.RelayAccountExternalID == nil {
		return fmt.Errorf("%w：中转站和中转站账号必须同时关联", ErrInvalidOperationInput)
	}
	var count int64
	if err := tx.Model(&RelayAccount{}).
		Where("relay_station_id = ? AND external_id = ?", *account.RelayStationID, *account.RelayAccountExternalID).
		Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w：关联的中转站账号不存在，请先刷新中转站快照", ErrInvalidOperationInput)
	}
	if account.Status == "pending" || account.Status == "ready" {
		account.Status = "deployed"
	}
	linkChanged := current == nil || !sameUintPointer(current.RelayStationID, account.RelayStationID) ||
		!sameInt64Pointer(current.RelayAccountExternalID, account.RelayAccountExternalID)
	if linkChanged || account.LinkedAt == nil {
		now := time.Now()
		account.LinkedAt = &now
	}
	return nil
}

func sameUintPointer(left, right *uint) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func sameInt64Pointer(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func (o *Operations) FindLocalAccount(id uint) (*LocalAccount, error) {
	var account LocalAccount
	if err := o.db.First(&account, id).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

func (o *Operations) DeleteLocalAccount(id uint) error {
	return o.db.Transaction(func(tx *gorm.DB) error {
		var account LocalAccount
		if err := tx.First(&account, id).Error; err != nil {
			return err
		}
		// 资产删除后仍保留已经发生的采购事实，只解除可变资产记录的引用。
		if err := tx.Model(&OperationLedgerEntry{}).Where("local_account_id = ?", id).Update("local_account_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&account).Error
	})
}

type AutoLinkResult struct {
	Linked    int `json:"linked"`
	Ambiguous int `json:"ambiguous"`
	Unmatched int `json:"unmatched"`
}

func (o *Operations) AutoLinkLocalAccounts() (AutoLinkResult, error) {
	var accounts []LocalAccount
	if err := o.db.Where("relay_station_id IS NULL OR relay_account_external_id IS NULL").Find(&accounts).Error; err != nil {
		return AutoLinkResult{}, err
	}
	var relayAccounts []RelayAccount
	if err := o.db.Find(&relayAccounts).Error; err != nil {
		return AutoLinkResult{}, err
	}
	type relayKey struct {
		stationID  uint
		externalID int64
	}
	byName := make(map[string][]relayKey)
	for _, account := range relayAccounts {
		key := normalizeMatchKey(account.Name)
		if key != "" {
			byName[key] = append(byName[key], relayKey{account.RelayStationID, account.ExternalID})
		}
	}
	result := AutoLinkResult{}
	err := o.db.Transaction(func(tx *gorm.DB) error {
		for _, account := range accounts {
			candidates := make(map[relayKey]bool)
			for _, value := range []string{account.Identifier, account.Name} {
				for _, match := range byName[normalizeMatchKey(value)] {
					candidates[match] = true
				}
			}
			switch len(candidates) {
			case 0:
				result.Unmatched++
			case 1:
				var matched relayKey
				for key := range candidates {
					matched = key
				}
				now := time.Now()
				updates := map[string]any{
					"relay_station_id":          matched.stationID,
					"relay_account_external_id": matched.externalID,
					"linked_at":                 now,
				}
				if account.Status == "pending" || account.Status == "ready" {
					updates["status"] = "deployed"
				}
				if err := tx.Model(&LocalAccount{}).Where("id = ?", account.ID).Updates(updates).Error; err != nil {
					return err
				}
				result.Linked++
			default:
				result.Ambiguous++
			}
		}
		return nil
	})
	return result, err
}

func syncLocalAccountLedger(tx *gorm.DB, account *LocalAccount) error {
	if account.PurchaseCost <= 0 {
		return tx.Where("local_account_id = ? AND source = ?", account.ID, LedgerSourceLocalAccount).
			Delete(&OperationLedgerEntry{}).Error
	}
	description := fmt.Sprintf("本地号池采购：%s", account.Name)
	entry := OperationLedgerEntry{LocalAccountID: &account.ID}
	return tx.Where("local_account_id = ?", account.ID).
		Assign(map[string]any{
			"direction": "expense", "category": LedgerCategoryPurchase,
			"amount": account.PurchaseCost, "currency": "CNY", "description": description,
			"source": LedgerSourceLocalAccount, "occurred_at": account.PurchasedAt,
		}).FirstOrCreate(&entry).Error
}

func normalizeLocalAccount(account *LocalAccount) {
	account.Name = strings.TrimSpace(account.Name)
	account.Identifier = normalizeMatchKey(account.Identifier)
	account.Platform = normalizeLocalAccountText(account.Platform)
	account.AccountType = normalizeLocalAccountText(account.AccountType)
	account.Status = normalizeLocalAccountText(account.Status)
	account.Notes = strings.TrimSpace(account.Notes)
	if account.AccountType == "" {
		account.AccountType = "oauth"
	}
	if account.Status == "" {
		account.Status = "ready"
	}
	if account.PurchasedAt.IsZero() {
		account.PurchasedAt = time.Now()
	}
}

func normalizeLocalAccountText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeMatchKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

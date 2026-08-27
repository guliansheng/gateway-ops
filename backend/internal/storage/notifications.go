package storage

import (
	"time"

	"gorm.io/gorm"
)

type Notifications struct{ db *gorm.DB }

func NewNotifications(db *gorm.DB) *Notifications { return &Notifications{db: db} }

func (r *Notifications) ListChannels() ([]NotificationChannel, error) {
	var list []NotificationChannel
	if err := r.db.Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *Notifications) ListEnabledChannels() ([]NotificationChannel, error) {
	var list []NotificationChannel
	if err := r.db.Where("enabled = ?", true).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *Notifications) FindChannel(id uint) (*NotificationChannel, error) {
	var c NotificationChannel
	if err := r.db.First(&c, id).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Notifications) CreateChannel(c *NotificationChannel) error { return r.db.Create(c).Error }
func (r *Notifications) UpdateChannel(c *NotificationChannel) error { return r.db.Save(c).Error }
func (r *Notifications) DeleteChannel(id uint) error {
	return r.db.Delete(&NotificationChannel{}, id).Error
}

func (r *Notifications) AppendLog(l *NotificationLog) error {
	if l.SentAt.IsZero() {
		l.SentAt = time.Now()
	}
	return r.db.Create(l).Error
}

func (r *Notifications) ListLogs(limit int) ([]NotificationLog, error) {
	if limit <= 0 {
		limit = 100
	}
	var list []NotificationLog
	if err := r.db.Order("sent_at DESC").Limit(limit).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// DeleteLogsBefore 删除 sent_at < cutoff 的通知日志，返回删除行数。
func (r *Notifications) DeleteLogsBefore(cutoff time.Time) (int64, error) {
	res := r.db.Where("sent_at < ?", cutoff).Delete(&NotificationLog{})
	return res.RowsAffected, res.Error
}

// TryClaimCooldown 原子地尝试占用 (channelID, event) 的发送名额。
//
// 语义：
//   - 不存在该记录 → 插入 (last_sent_at=now)，返回 true（应该发送）
//   - 存在但 last_sent_at < now - cooldown → 更新成 now，返回 true
//   - 存在且仍在冷却窗口 → 不动，返回 false（跳过发送）
//
// 使用 PostgreSQL 的 INSERT ... ON CONFLICT DO UPDATE ... WHERE 一句 SQL 完成，
// 避免并发扫描下"两个 goroutine 同时认为 cooldown 已过、都发出去"的竞态。
//
// cooldown <= 0 时直接返回 true 不写表。
func (r *Notifications) TryClaimCooldown(channelID uint, event NotificationEvent, cooldown time.Duration) (bool, error) {
	if cooldown <= 0 {
		return true, nil
	}
	now := time.Now()
	threshold := now.Add(-cooldown)

	// 关键 SQL：
	//   INSERT 命中时（无记录） → 写入成功 → RowsAffected=1 → 应该发
	//   UPDATE 命中时（cooldown 已过） → RowsAffected=1 → 应该发
	//   ON CONFLICT 但 WHERE 不过 → RowsAffected=0 → 不发
	//
	// 注意：必须用 EXCLUDED 拿到 INSERT 试图写入的 last_sent_at，
	// 否则更新成传入的 ? 而不是 EXCLUDED 的 now，会少一次参数绑定但语义一样；这里偏向显式。
	res := r.db.Exec(`
		INSERT INTO notification_cooldowns (channel_id, event, last_sent_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (channel_id, event) DO UPDATE
		SET last_sent_at = EXCLUDED.last_sent_at,
		    updated_at   = EXCLUDED.updated_at
		WHERE notification_cooldowns.last_sent_at < ?
	`, channelID, event, now, now, threshold)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ResetCooldown 删除某个 (channelID, event) 的冷却记录。
// 主要给测试 / 调试用，业务路径不需要主动调用。
func (r *Notifications) ResetCooldown(channelID uint, event NotificationEvent) error {
	return r.db.Where("channel_id = ? AND event = ?", channelID, event).
		Delete(&NotificationCooldown{}).Error
}

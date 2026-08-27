package storage

import (
	"errors"

	"gorm.io/gorm"
)

type AuthSessions struct{ db *gorm.DB }

func NewAuthSessions(db *gorm.DB) *AuthSessions { return &AuthSessions{db: db} }

// FindByChannel 取渠道的会话凭据。返回 (nil, nil) 表示尚无 session。
func (r *AuthSessions) FindByChannel(channelID uint) (*AuthSession, error) {
	var s AuthSession
	err := r.db.First(&s, "channel_id = ?", channelID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Upsert 写入或更新会话。
func (r *AuthSessions) Upsert(s *AuthSession) error {
	var existing AuthSession
	err := r.db.First(&existing, "channel_id = ?", s.ChannelID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.Create(s).Error
	}
	if err != nil {
		return err
	}
	return r.db.Save(s).Error
}

// Delete 删除渠道的 session（例如手动重置）。
func (r *AuthSessions) Delete(channelID uint) error {
	return r.db.Delete(&AuthSession{}, "channel_id = ?", channelID).Error
}

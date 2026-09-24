package storage

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

var ErrHelpDocumentNotFound = errors.New("帮助文档不存在")

// ErrHelpCommentNotFound 表示要操作的留言不存在（或已被删除）。
var ErrHelpCommentNotFound = errors.New("留言不存在")

// HelpDocuments 每个中转站一篇帮助文档的数据访问层。
type HelpDocuments struct{ db *gorm.DB }

func NewHelpDocuments(db *gorm.DB) *HelpDocuments { return &HelpDocuments{db: db} }

// FindByStation 返回中转站当前的帮助文档；不存在时返回 ErrHelpDocumentNotFound。
func (r *HelpDocuments) FindByStation(stationID uint) (*HelpDocument, error) {
	var item HelpDocument
	err := r.db.Where("relay_station_id = ?", stationID).
		Order("updated_at DESC").Order("id DESC").
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrHelpDocumentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// Upsert 按中转站创建或更新帮助正文，格式统一落为富文本 HTML。
func (r *HelpDocuments) Upsert(item *HelpDocument) error {
	existing, err := r.FindByStation(item.RelayStationID)
	if errors.Is(err, ErrHelpDocumentNotFound) {
		item.ContentFormat = "html"
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now()
		}
		return r.db.Create(item).Error
	}
	if err != nil {
		return err
	}
	existing.Title = item.Title
	existing.Content = item.Content
	existing.ContentFormat = "html"
	existing.Enabled = item.Enabled
	if err := r.db.Save(existing).Error; err != nil {
		return err
	}
	*item = *existing
	return nil
}

// Delete 删除帮助文档及其留言。
func (r *HelpDocuments) Delete(id uint) error {
	if err := r.db.Where("help_document_id = ?", id).Delete(&HelpComment{}).Error; err != nil {
		return err
	}
	return r.db.Delete(&HelpDocument{}, id).Error
}

// Public 读取中转站已发布的帮助文档和留言。
func (r *HelpDocuments) Public(stationID uint) (*HelpDocument, []HelpComment, error) {
	document, err := r.FindByStation(stationID)
	if err != nil || !document.Enabled {
		return nil, nil, ErrHelpDocumentNotFound
	}
	var comments []HelpComment
	if err := r.db.Where("help_document_id = ?", document.ID).
		Order("created_at ASC").Limit(500).Find(&comments).Error; err != nil {
		return nil, nil, err
	}
	return document, comments, nil
}

func (r *HelpDocuments) AddComment(comment *HelpComment) error {
	comment.CreatedAt = time.Now()
	return r.db.Create(comment).Error
}

// Comments 读取某篇帮助文档的全部留言（管理弹窗用），新的在前。
func (r *HelpDocuments) Comments(documentID uint) ([]HelpComment, error) {
	var comments []HelpComment
	if err := r.db.Where("help_document_id = ?", documentID).
		Order("created_at DESC").Order("id DESC").
		Find(&comments).Error; err != nil {
		return nil, err
	}
	return comments, nil
}

// DeleteComment 删除单条留言；留言不存在时返回 ErrHelpCommentNotFound。
func (r *HelpDocuments) DeleteComment(id uint) error {
	result := r.db.Delete(&HelpComment{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrHelpCommentNotFound
	}
	return nil
}

// DeleteComments 批量删除留言，返回实际删除条数。
func (r *HelpDocuments) DeleteComments(ids []uint) (int64, error) {
	result := r.db.Delete(&HelpComment{}, ids)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

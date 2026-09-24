package storage

import (
	"gorm.io/gorm"
)

// migrateHelpSchema 在 GORM AutoMigrate 之前把旧版帮助文档表结构
// （slug + markdown + help_station_configs 多站发布）升级到新版
// （每站一篇、content 存富文本 HTML），全程幂等，可重复执行。
func migrateHelpSchema(db *gorm.DB) error {
	if !db.Migrator().HasTable(&HelpDocument{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&HelpDocument{}, "markdown") {
		return nil
	}
	// 先落 content_format 列并打上 md 标记，再改列名：
	// 这样即使中途重启，也能继续识别哪些行还需要转换。
	if err := db.Exec("ALTER TABLE help_documents ADD COLUMN IF NOT EXISTS content_format VARCHAR(16) NOT NULL DEFAULT 'html'").Error; err != nil {
		return err
	}
	if db.Migrator().HasColumn(&HelpDocument{}, "content") {
		// 半升级状态：content 已存在（例如 AutoMigrate 先建了列），把旧列数据补过来。
		if err := db.Exec("UPDATE help_documents SET content = markdown WHERE COALESCE(content, '') = '' AND markdown IS NOT NULL").Error; err != nil {
			return err
		}
		if err := db.Exec("DROP COLUMN markdown FROM help_documents").Error; err != nil {
			return err
		}
	} else if err := db.Exec("ALTER TABLE help_documents RENAME COLUMN markdown TO content").Error; err != nil {
		return err
	}
	return db.Exec("UPDATE help_documents SET content_format = 'md' WHERE content_format <> 'md'").Error
}

// migrateHelpData 在表结构就绪后执行数据迁移：
//  1. 用旧的 help_station_configs 把文档绑定到中转站（每个站保留 updated_at 最新的一篇）
//  2. 删除废弃的多站发布配置表
//  3. 把遗留 Markdown 正文转换成富文本 HTML
func migrateHelpData(db *gorm.DB) error {
	if !db.Migrator().HasTable(&HelpDocument{}) {
		return nil
	}
	// AutoMigrate 新增的 relay_station_id 在旧行上是 NULL，先归一成 0，
	// 否则下面 "= 0" 的绑定条件匹配不到，文档会永久丢失中转站归属。
	if err := db.Exec("UPDATE help_documents SET relay_station_id = 0 WHERE relay_station_id IS NULL").Error; err != nil {
		return err
	}
	// 旧版 slug 是 NOT NULL 唯一列，新版按站点查询不再使用；
	// 不删掉的话新文档 INSERT 会因缺少 slug 直接失败。
	if db.Migrator().HasColumn(&HelpDocument{}, "slug") {
		if err := db.Exec("ALTER TABLE help_documents DROP COLUMN IF EXISTS slug").Error; err != nil {
			return err
		}
	}
	if db.Migrator().HasTable("help_station_configs") {
		// 同一中转站若发布了多篇旧文档，只保留最近更新的一篇。
		if err := db.Exec(`
			UPDATE help_documents d
			SET relay_station_id = c.relay_station_id
			FROM (
				SELECT c.help_document_id, c.relay_station_id,
					ROW_NUMBER() OVER (
						PARTITION BY c.relay_station_id
						ORDER BY d.updated_at DESC NULLS LAST, d.id DESC
					) AS rank
				FROM help_station_configs c
				JOIN help_documents d ON d.id = c.help_document_id
				WHERE d.relay_station_id = 0
			) c
			WHERE d.id = c.help_document_id AND c.rank = 1
		`).Error; err != nil {
			return err
		}
		if err := db.Exec("DROP TABLE IF EXISTS help_station_configs").Error; err != nil {
			return err
		}
	}
	// 转换遗留 Markdown 正文；转换成功后立即标记为 html，重启不会重复转换。
	var pending []HelpDocument
	if err := db.Where("content_format <> 'html'").Find(&pending).Error; err != nil {
		return err
	}
	for i := range pending {
		item := &pending[i]
		item.Content = MarkdownToHTML(item.Content)
		item.ContentFormat = "html"
		if err := db.Save(item).Error; err != nil {
			return err
		}
	}
	return nil
}

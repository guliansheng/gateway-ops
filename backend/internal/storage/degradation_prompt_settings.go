package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DegradationPromptMaxBytes         = 32 * 1024
	DegradationExpectedAnswerMaxBytes = 4 * 1024
	DegradationReferenceMaxBytes      = 8 * 1024

	DefaultPelicanDegradationPrompt = `创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试，只靠你自己完成，不要使用任何skill，不要依赖我本地的AGENTS.md

只返回一份完整的原始 HTML 文档，不要 Markdown 或解释。所有 SVG、CSS、动画和脚本必须自包含，不加载任何外部资源。
主角必须是完整、可辨认的白色鹈鹕，具有长喙、喉囊、身体、翅膀和腿；自行车要有合理连接的车架、双轮和踏板。双腿与踏板同步，轮子持续旋转，保持连贯踏频。
有前后景层次和景深背景，构图清晰。提供可用的暂停/继续按钮，暂停时须停止全部动画；支持 prefers-reduced-motion 的减弱动态效果。`

	// DefaultPelicanDegradationReference is the built-in review checklist for the
	// pelican test. Stations may replace it with their own criteria.
	DefaultPelicanDegradationReference = `逐项核对，任一项不满足即判 degraded：
1. 主体完整可辨：题目指定的主体与载具都不缺失、不残缺。
2. 题目要求的联动动作真实成立：例如骑乘者的腿与脚蹬、曲柄同步，脚始终落在其上，而不是整条腿绕固定点摆动、悬空或穿模。
3. 持续运动真实发生：车轮等部件确实在转动。
4. 暂停/继续等交互控件真实控制动画，而不是只改文字。
5. 文档与 SVG 完整自包含，不加载外部资源。

外观类内容不作为降智依据：配色、光影、背景层次数量、坐标数值精度、轮辐数量、文案措辞、代码风格、可访问性细节。`

	DefaultCandyDegradationPrompt = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

| 形状 | 苹果味 | 桃子味 | 西瓜味 |
| --- | --- | --- | --- |
| 圆形 | 7 | 9 | 8 |
| 五角星形 | 7 | 6 | 4 |

禁止联网，禁止写代码算出答案，禁止使用外部工具。请直接作答。`

	DefaultCandyDegradationExpectedAnswer = "21"
)

var ErrInvalidDegradationPromptSettings = errors.New("降智检测提示词设置无效")

// Station-owned settings have no foreign key or association with replaceable
// RelayStation/RelayGroup snapshots. Only the authenticated settings API uses
// this JSON shape; never embed it in public degradation configurations/results.
type DegradationPromptSettings struct {
	ID                  uint      `gorm:"primaryKey" json:"-"`
	StationID           uint      `gorm:"not null;uniqueIndex:idx_degradation_prompt_station" json:"-"`
	CandyPrompt         string    `gorm:"type:text;not null" json:"candy_prompt"`
	CandyExpectedAnswer string    `gorm:"type:text;not null" json:"candy_expected_answer"`
	PelicanPrompt       string    `gorm:"type:text;not null" json:"pelican_prompt"`
	PelicanReference    string    `gorm:"type:text;not null;default:''" json:"pelican_reference"`
	CreatedAt           time.Time `json:"-"`
	UpdatedAt           time.Time `json:"-"`
}

func (DegradationPromptSettings) TableName() string { return "degradation_prompt_settings" }

func DefaultDegradationPromptSettings() DegradationPromptSettings {
	return DegradationPromptSettings{
		CandyPrompt:         DefaultCandyDegradationPrompt,
		CandyExpectedAnswer: DefaultCandyDegradationExpectedAnswer,
		PelicanPrompt:       DefaultPelicanDegradationPrompt,
		PelicanReference:    DefaultPelicanDegradationReference,
	}
}

func NormalizeDegradationPromptSettings(settings DegradationPromptSettings) (DegradationPromptSettings, error) {
	// The reference standard is optional for API clients: an empty value falls
	// back to the built-in checklist instead of failing validation.
	if strings.TrimSpace(settings.PelicanReference) == "" {
		settings.PelicanReference = DefaultPelicanDegradationReference
	}
	for _, field := range []struct {
		name  string
		value *string
		limit int
	}{
		{"candy_prompt", &settings.CandyPrompt, DegradationPromptMaxBytes},
		{"candy_expected_answer", &settings.CandyExpectedAnswer, DegradationExpectedAnswerMaxBytes},
		{"pelican_prompt", &settings.PelicanPrompt, DegradationPromptMaxBytes},
		{"pelican_reference", &settings.PelicanReference, DegradationReferenceMaxBytes},
	} {
		// Check before trimming: forbidden controls at either edge must not be
		// silently removed and accepted (in particular NUL, CR, VT and FF).
		if !utf8.ValidString(*field.value) || strings.ContainsFunc(*field.value, func(r rune) bool {
			return unicode.IsControl(r) && r != '\n' && r != '\t'
		}) {
			return settings, fmt.Errorf("%w：%s 包含无效字符或不允许的控制字符", ErrInvalidDegradationPromptSettings, field.name)
		}
		*field.value = strings.TrimSpace(*field.value)
		if *field.value == "" || len(*field.value) > field.limit {
			return settings, fmt.Errorf("%w：%s 不能为空且最多 %d 字节", ErrInvalidDegradationPromptSettings, field.name, field.limit)
		}
	}
	return settings, nil
}

// GetDegradationPromptSettings is a read-only logical-default lookup. A GET or
// a first run must not materialize a settings row merely to store defaults.
func (r *RelayStations) GetDegradationPromptSettings(ctx context.Context, stationID uint) (DegradationPromptSettings, error) {
	var station RelayStation
	if err := r.db.WithContext(ctx).Select("id").First(&station, stationID).Error; err != nil {
		return DegradationPromptSettings{}, err
	}
	var settings DegradationPromptSettings
	err := r.db.WithContext(ctx).Where("station_id = ?", stationID).First(&settings).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DefaultDegradationPromptSettings(), nil
	}
	if err != nil {
		return DegradationPromptSettings{}, err
	}
	return NormalizeDegradationPromptSettings(settings)
}

func (r *RelayStations) SaveDegradationPromptSettings(ctx context.Context, stationID uint, settings DegradationPromptSettings) error {
	settings, err := NormalizeDegradationPromptSettings(settings)
	if err != nil {
		return err
	}
	var station RelayStation
	if err := r.db.WithContext(ctx).Select("id").First(&station, stationID).Error; err != nil {
		return err
	}
	settings.ID, settings.StationID = 0, stationID
	settings.CreatedAt, settings.UpdatedAt = time.Time{}, time.Time{}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "station_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"candy_prompt", "candy_expected_answer", "pelican_prompt", "pelican_reference", "updated_at"}),
	}).Create(&settings).Error
}

// Package model
// media.go 对齐 Media / Category / Tag / MediaTag。
package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Media 对应 media 表。
// 索引设计对齐 Prisma：
//   - (status, createdAt)：首页按状态分组 + 按创建时间倒序
//   - categoryId：分类筛选
//   - views：热门排序
//   - artist：按艺人筛选
//   - m3u8Url：代理端点校验媒体归属
type Media struct {
	ID                string    `gorm:"primaryKey;type:text" json:"id"`
	Title             string    `gorm:"type:text;not null" json:"title"`
	M3u8URL           string    `gorm:"column:m3u8_url;type:text;not null;index:idx_media_m3u8" json:"m3u8Url"`
	PosterURL         *string   `gorm:"column:poster_url;type:text" json:"posterUrl,omitempty"`
	OriginalPosterURL *string   `gorm:"column:original_poster_url;type:text" json:"originalPosterUrl,omitempty"`
	Description       *string   `gorm:"type:text" json:"description,omitempty"`
	Year              *int      `gorm:"type:integer" json:"year,omitempty"`
	Rating            *float64  `gorm:"type:real" json:"rating,omitempty"`
	Duration          *int      `gorm:"type:integer" json:"duration,omitempty"`
	Artist            *string   `gorm:"type:text;index:idx_media_artist" json:"artist,omitempty"`
	Views             int       `gorm:"type:integer;not null;default:0;index:idx_media_views" json:"views"`
	Status            string    `gorm:"type:text;not null;default:ACTIVE;index:idx_media_status_created,priority:1" json:"status"`
	CategoryID        *string   `gorm:"column:category_id;type:text;index:idx_media_category" json:"categoryId,omitempty"`
	CreatedAt         time.Time `gorm:"autoCreateTime;index:idx_media_status_created,priority:2" json:"createdAt"`
	UpdatedAt         time.Time `gorm:"autoUpdateTime" json:"updatedAt"`

	Category      *Category      `gorm:"foreignKey:CategoryID;constraint:OnDelete:SET NULL" json:"category,omitempty"`
	Tags          []MediaTag     `gorm:"foreignKey:MediaID;constraint:OnDelete:CASCADE" json:"-"`
	Favorites     []Favorite     `gorm:"foreignKey:MediaID;constraint:OnDelete:CASCADE" json:"-"`
	PlaylistItems []PlaylistItem `gorm:"foreignKey:MediaID;constraint:OnDelete:CASCADE" json:"-"`
	WatchHistory  []WatchHistory `gorm:"foreignKey:MediaID;constraint:OnDelete:CASCADE" json:"-"`
}

func (Media) TableName() string { return "media" }

func (m *Media) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

// Category 对应 categories 表。
type Category struct {
	ID        string    `gorm:"primaryKey;type:text" json:"id"`
	Name      string    `gorm:"uniqueIndex;type:text;not null" json:"name"`
	Slug      string    `gorm:"uniqueIndex;type:text;not null" json:"slug"`
	PosterURL *string   `gorm:"column:poster_url;type:text" json:"posterUrl,omitempty"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`

	Media []Media `gorm:"foreignKey:CategoryID" json:"-"`
}

func (Category) TableName() string { return "categories" }

func (c *Category) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return nil
}

// Tag 对应 tags 表。
type Tag struct {
	ID        string    `gorm:"primaryKey;type:text" json:"id"`
	Name      string    `gorm:"uniqueIndex;type:text;not null" json:"name"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`

	Media []MediaTag `gorm:"foreignKey:TagID" json:"-"`
}

func (Tag) TableName() string { return "tags" }

func (t *Tag) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	return nil
}

// MediaTag 是 Media 与 Tag 的多对多关联表，使用 (mediaId, tagId) 联合主键。
// TagID 上单独索引，便于按标签反查媒体。
type MediaTag struct {
	MediaID string `gorm:"column:media_id;primaryKey;type:text" json:"mediaId"`
	TagID   string `gorm:"column:tag_id;primaryKey;type:text;index:idx_media_tag" json:"tagId"`

	Media Media `gorm:"foreignKey:MediaID;constraint:OnDelete:CASCADE" json:"-"`
	Tag   Tag   `gorm:"foreignKey:TagID;constraint:OnDelete:CASCADE" json:"-"`
}

func (MediaTag) TableName() string { return "media_tags" }

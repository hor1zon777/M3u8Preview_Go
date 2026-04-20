// Package model
// playlist.go 对齐 Playlist / PlaylistItem / Favorite / WatchHistory。
package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Favorite 对应 favorites 表。(userId, mediaId) 唯一，(userId, createdAt) 为按用户倒序查询的索引。
type Favorite struct {
	ID        string    `gorm:"primaryKey;type:text" json:"id"`
	UserID    string    `gorm:"column:user_id;type:text;not null;uniqueIndex:uniq_fav_user_media,priority:1;index:idx_fav_user_time,priority:1" json:"userId"`
	MediaID   string    `gorm:"column:media_id;type:text;not null;uniqueIndex:uniq_fav_user_media,priority:2" json:"mediaId"`
	CreatedAt time.Time `gorm:"autoCreateTime;index:idx_fav_user_time,priority:2" json:"createdAt"`

	User  User  `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	Media Media `gorm:"foreignKey:MediaID;constraint:OnDelete:CASCADE" json:"media,omitempty"`
}

func (Favorite) TableName() string { return "favorites" }

func (f *Favorite) BeforeCreate(tx *gorm.DB) error {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	return nil
}

// Playlist 对应 playlists 表。IsPublic=true 的可被非 owner 查看。
type Playlist struct {
	ID          string    `gorm:"primaryKey;type:text" json:"id"`
	Name        string    `gorm:"type:text;not null" json:"name"`
	Description *string   `gorm:"type:text" json:"description,omitempty"`
	PosterURL   *string   `gorm:"column:poster_url;type:text" json:"posterUrl,omitempty"`
	UserID      string    `gorm:"column:user_id;type:text;not null;index:idx_playlist_user" json:"userId"`
	IsPublic    bool      `gorm:"column:is_public;not null;default:false" json:"isPublic"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updatedAt"`

	User  User           `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	Items []PlaylistItem `gorm:"foreignKey:PlaylistID;constraint:OnDelete:CASCADE" json:"-"`
}

func (Playlist) TableName() string { return "playlists" }

func (p *Playlist) BeforeCreate(tx *gorm.DB) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	return nil
}

// PlaylistItem 对应 playlist_items 表。Position 为用户定义的排序位。
type PlaylistItem struct {
	ID         string    `gorm:"primaryKey;type:text" json:"id"`
	PlaylistID string    `gorm:"column:playlist_id;type:text;not null;uniqueIndex:uniq_pitem_pl_media,priority:1;index:idx_pitem_playlist" json:"playlistId"`
	MediaID    string    `gorm:"column:media_id;type:text;not null;uniqueIndex:uniq_pitem_pl_media,priority:2" json:"mediaId"`
	Position   int       `gorm:"not null" json:"position"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"createdAt"`

	Playlist Playlist `gorm:"foreignKey:PlaylistID;constraint:OnDelete:CASCADE" json:"-"`
	Media    Media    `gorm:"foreignKey:MediaID;constraint:OnDelete:CASCADE" json:"media,omitempty"`
}

func (PlaylistItem) TableName() string { return "playlist_items" }

func (p *PlaylistItem) BeforeCreate(tx *gorm.DB) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	return nil
}

// WatchHistory 对应 watch_history 表。
// (userId, mediaId) 唯一——每个用户对每个媒体只保留一条最新进度。
// (userId, updatedAt DESC) 是 "继续观看" 的热查询路径。
type WatchHistory struct {
	ID         string    `gorm:"primaryKey;type:text" json:"id"`
	UserID     string    `gorm:"column:user_id;type:text;not null;uniqueIndex:uniq_wh_user_media,priority:1;index:idx_wh_user_time,priority:1" json:"userId"`
	MediaID    string    `gorm:"column:media_id;type:text;not null;uniqueIndex:uniq_wh_user_media,priority:2" json:"mediaId"`
	Progress   float64   `gorm:"type:real;not null;default:0" json:"progress"`
	Duration   float64   `gorm:"type:real;not null;default:0" json:"duration"`
	Percentage float64   `gorm:"type:real;not null;default:0" json:"percentage"`
	Completed  bool      `gorm:"not null;default:false" json:"completed"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime;index:idx_wh_user_time,priority:2" json:"updatedAt"`

	User  User  `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	Media Media `gorm:"foreignKey:MediaID;constraint:OnDelete:CASCADE" json:"media,omitempty"`
}

func (WatchHistory) TableName() string { return "watch_history" }

func (w *WatchHistory) BeforeCreate(tx *gorm.DB) error {
	if w.ID == "" {
		w.ID = uuid.NewString()
	}
	return nil
}

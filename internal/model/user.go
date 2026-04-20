// Package model
// user.go 对齐 packages/server/prisma/schema.prisma 中的 User / RefreshToken / LoginRecord。
package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// User 对应 users 表。密码以 bcrypt hash 存储（cost=12）。
type User struct {
	ID           string    `gorm:"primaryKey;type:text" json:"id"`
	Username     string    `gorm:"uniqueIndex;type:text;not null" json:"username"`
	PasswordHash string    `gorm:"type:text;not null" json:"-"`
	Role         string    `gorm:"type:text;not null;default:USER" json:"role"`
	Avatar       *string   `gorm:"type:text" json:"avatar,omitempty"`
	IsActive     bool      `gorm:"not null;default:true" json:"isActive"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updatedAt"`

	RefreshTokens []RefreshToken `gorm:"constraint:OnDelete:CASCADE" json:"-"`
	Favorites     []Favorite     `gorm:"constraint:OnDelete:CASCADE" json:"-"`
	Playlists     []Playlist     `gorm:"constraint:OnDelete:CASCADE" json:"-"`
	WatchHistory  []WatchHistory `gorm:"constraint:OnDelete:CASCADE" json:"-"`
	ImportLogs    []ImportLog    `gorm:"constraint:OnDelete:CASCADE" json:"-"`
	LoginRecords  []LoginRecord  `gorm:"constraint:OnDelete:CASCADE" json:"-"`
}

func (User) TableName() string { return "users" }

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == "" {
		u.ID = uuid.NewString()
	}
	return nil
}

// RefreshToken 对应 refresh_tokens 表。
// FamilyID 用于 token rotation + reuse 检测：同一 family 的老 token 被使用会触发全端注销。
type RefreshToken struct {
	ID        string    `gorm:"primaryKey;type:text" json:"id"`
	Token     string    `gorm:"uniqueIndex;type:text;not null" json:"token"`
	FamilyID  string    `gorm:"index:idx_refresh_family;type:text;not null" json:"familyId"`
	UserID    string    `gorm:"index:idx_refresh_user;type:text;not null" json:"userId"`
	ExpiresAt time.Time `gorm:"not null" json:"expiresAt"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`

	User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}

func (RefreshToken) TableName() string { return "refresh_tokens" }

func (r *RefreshToken) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	return nil
}

// LoginRecord 记录每次登录成功的上下文（IP + UA 解析），用于管理面板活动审计。
// Index (userId, createdAt DESC) 已经覆盖了按用户查询最近登录的热路径。
type LoginRecord struct {
	ID        string    `gorm:"primaryKey;type:text" json:"id"`
	UserID    string    `gorm:"index:idx_login_user_time,priority:1;type:text;not null" json:"userId"`
	IP        *string   `gorm:"type:text" json:"ip,omitempty"`
	UserAgent *string   `gorm:"type:text" json:"userAgent,omitempty"`
	Browser   *string   `gorm:"type:text" json:"browser,omitempty"`
	OS        *string   `gorm:"type:text" json:"os,omitempty"`
	Device    *string   `gorm:"type:text" json:"device,omitempty"`
	CreatedAt time.Time `gorm:"autoCreateTime;index:idx_login_user_time,priority:2;index:idx_login_time" json:"createdAt"`

	User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}

func (LoginRecord) TableName() string { return "login_records" }

func (l *LoginRecord) BeforeCreate(tx *gorm.DB) error {
	if l.ID == "" {
		l.ID = uuid.NewString()
	}
	return nil
}

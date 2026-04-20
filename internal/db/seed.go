// Package db
// seed.go 对齐 packages/server/prisma/seed.ts：
// - 创建 admin / demo 用户（密码来自环境变量 ADMIN_SEED_PASSWORD / DEMO_SEED_PASSWORD；
//   缺省时开发环境降级 Admin123 / Demo1234，生产环境 fatal）
// - 创建 5 个默认分类 + 8 个默认标签（若不存在）
// - 仅在非生产环境插入 5 条测试媒体
package db

import (
	"errors"
	"fmt"
	"log"
	"os"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// Seed 幂等地写入初始数据。cfg.NodeEnv=="production" 时跳过测试媒体。
func Seed(db *gorm.DB, cfg *config.Config) error {
	if err := seedUsers(db, cfg); err != nil {
		return err
	}
	categories, err := seedCategories(db)
	if err != nil {
		return err
	}
	tags, err := seedTags(db)
	if err != nil {
		return err
	}
	if cfg.NodeEnv != "production" {
		if err := seedTestMedia(db, categories, tags); err != nil {
			return err
		}
	}
	return nil
}

func seedUsers(db *gorm.DB, cfg *config.Config) error {
	adminPass := os.Getenv("ADMIN_SEED_PASSWORD")
	if adminPass == "" {
		if cfg.NodeEnv == "production" {
			return fmt.Errorf("FATAL: ADMIN_SEED_PASSWORD required in production")
		}
		adminPass = "Admin123"
	}
	demoPass := os.Getenv("DEMO_SEED_PASSWORD")
	if demoPass == "" {
		if cfg.NodeEnv == "production" {
			return fmt.Errorf("FATAL: DEMO_SEED_PASSWORD required in production")
		}
		demoPass = "Demo1234"
	}

	if err := upsertUser(db, "admin", adminPass, model.RoleAdmin); err != nil {
		return err
	}
	log.Println("[seed] admin user ensured")

	if err := upsertUser(db, "demo", demoPass, model.RoleUser); err != nil {
		return err
	}
	log.Println("[seed] demo user ensured")
	return nil
}

// upsertUser 存在则不动密码（保留用户改过的密码），不存在则创建。
func upsertUser(db *gorm.DB, username, password, role string) error {
	var existing model.User
	err := db.Where("username = ?", username).First(&existing).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("query user: %w", err)
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("bcrypt: %w", err)
	}
	u := model.User{
		Username:     username,
		PasswordHash: string(hashed),
		Role:         role,
		IsActive:     true,
	}
	return db.Create(&u).Error
}

// seedCategories 返回按 slug→Category 索引的 map，方便后续关联测试媒体。
func seedCategories(db *gorm.DB) (map[string]model.Category, error) {
	defs := []model.Category{
		{Name: "电影", Slug: "movies"},
		{Name: "电视剧", Slug: "tv-shows"},
		{Name: "动漫", Slug: "anime"},
		{Name: "纪录片", Slug: "documentary"},
		{Name: "直播", Slug: "live"},
	}
	// ON CONFLICT(slug) DO NOTHING，避免重复 seed 报错
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&defs).Error; err != nil {
		return nil, fmt.Errorf("upsert categories: %w", err)
	}

	var cats []model.Category
	if err := db.Find(&cats).Error; err != nil {
		return nil, err
	}
	idx := make(map[string]model.Category, len(cats))
	for _, c := range cats {
		idx[c.Slug] = c
	}
	return idx, nil
}

func seedTags(db *gorm.DB) (map[string]model.Tag, error) {
	defs := []model.Tag{
		{Name: "动作"}, {Name: "喜剧"}, {Name: "科幻"}, {Name: "爱情"},
		{Name: "悬疑"}, {Name: "恐怖"}, {Name: "4K"}, {Name: "高清"},
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&defs).Error; err != nil {
		return nil, fmt.Errorf("upsert tags: %w", err)
	}
	var tags []model.Tag
	if err := db.Find(&tags).Error; err != nil {
		return nil, err
	}
	idx := make(map[string]model.Tag, len(tags))
	for _, t := range tags {
		idx[t.Name] = t
	}
	return idx, nil
}

// seedTestMedia 与 TS 版 seed.ts 的 5 条测试媒体保持 URL 一致。
// 仅在 media 表为空时插入，避免重跑 seed 时出现重复项。
func seedTestMedia(db *gorm.DB, categories map[string]model.Category, tags map[string]model.Tag) error {
	var count int64
	if err := db.Model(&model.Media{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	type testItem struct {
		Title        string
		M3u8URL      string
		Description  string
		Year         *int
		Rating       *float64
		CategorySlug string
		TagNames     []string
	}
	intP := func(n int) *int { return &n }
	floatP := func(f float64) *float64 { return &f }

	items := []testItem{
		{
			Title:        "Big Buck Bunny",
			M3u8URL:      "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8",
			Description:  "Big Buck Bunny - 开源动画短片，讲述了一只大兔子的冒险故事。常用于流媒体测试。",
			Year:         intP(2008),
			Rating:       floatP(7.5),
			CategorySlug: "movies",
			TagNames:     []string{"动作", "喜剧"},
		},
		{
			Title:        "Sintel",
			M3u8URL:      "https://bitdash-a.akamaihd.net/content/sintel/hls/playlist.m3u8",
			Description:  "Sintel - Blender基金会制作的开源动画短片，讲述了一位年轻女孩寻找宠物龙的故事。",
			Year:         intP(2010),
			Rating:       floatP(8.0),
			CategorySlug: "movies",
			TagNames:     []string{"动作", "科幻"},
		},
		{
			Title:        "Tears of Steel",
			M3u8URL:      "https://demo.unified-streaming.com/k8s/features/stable/video/tears-of-steel/tears-of-steel.ism/.m3u8",
			Description:  "Tears of Steel - Blender基金会制作的开源科幻短片。",
			Year:         intP(2012),
			Rating:       floatP(6.5),
			CategorySlug: "movies",
			TagNames:     []string{"科幻"},
		},
		{
			Title:        "HLS Test Stream 1",
			M3u8URL:      "https://cph-p2p-msl.akamaized.net/hls/live/2000341/test/master.m3u8",
			Description:  "用于测试的HLS直播流。",
			CategorySlug: "live",
			TagNames:     []string{"高清"},
		},
		{
			Title:        "Elephant Dream",
			M3u8URL:      "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8",
			Description:  "Elephant Dream - 世界上第一部使用开源工具制作的动画短片。",
			Year:         intP(2006),
			Rating:       floatP(6.0),
			CategorySlug: "anime",
			TagNames:     []string{"科幻", "动作"},
		},
	}

	for _, it := range items {
		cat, ok := categories[it.CategorySlug]
		if !ok {
			continue
		}
		desc := it.Description
		m := model.Media{
			Title:       it.Title,
			M3u8URL:     it.M3u8URL,
			Description: &desc,
			Year:        it.Year,
			Rating:      it.Rating,
			CategoryID:  &cat.ID,
			Status:      model.MediaStatusActive,
		}
		if err := db.Create(&m).Error; err != nil {
			return fmt.Errorf("create media %s: %w", it.Title, err)
		}
		for _, name := range it.TagNames {
			if t, ok := tags[name]; ok {
				if err := db.Create(&model.MediaTag{MediaID: m.ID, TagID: t.ID}).Error; err != nil {
					return fmt.Errorf("link tag %s: %w", name, err)
				}
			}
		}
	}
	log.Printf("[seed] %d test media created", len(items))
	return nil
}

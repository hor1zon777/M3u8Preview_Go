// Package service
// admin.go 对齐 adminService.ts：dashboard / users / settings / media batch。
package service

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// AdminService 管理员业务入口。
type AdminService struct {
	db *gorm.DB
}

// NewAdminService 构造。
func NewAdminService(db *gorm.DB) *AdminService { return &AdminService{db: db} }

// Dashboard 6 并行查询构建主页统计。
func (s *AdminService) Dashboard() (dto.AdminDashboardResponse, error) {
	var (
		totalMedia      int64
		totalUsers      int64
		totalCategories int64
		totalViews      int64
		recentMedia     []model.Media
		topMedia        []model.Media

		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	saveErr := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	wg.Add(6)
	go func() {
		defer wg.Done()
		saveErr(s.db.Model(&model.Media{}).Count(&totalMedia).Error)
	}()
	go func() {
		defer wg.Done()
		saveErr(s.db.Model(&model.User{}).Count(&totalUsers).Error)
	}()
	go func() {
		defer wg.Done()
		saveErr(s.db.Model(&model.Category{}).Count(&totalCategories).Error)
	}()
	go func() {
		defer wg.Done()
		type agg struct{ Total int64 }
		var a agg
		saveErr(s.db.Model(&model.Media{}).Select("COALESCE(SUM(views),0) AS total").Scan(&a).Error)
		mu.Lock()
		totalViews = a.Total
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		var rows []model.Media
		saveErr(s.db.Preload("Category").Order("created_at DESC").Limit(5).Find(&rows).Error)
		mu.Lock()
		recentMedia = rows
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		var rows []model.Media
		saveErr(s.db.Preload("Category").Order("views DESC").Limit(5).Find(&rows).Error)
		mu.Lock()
		topMedia = rows
		mu.Unlock()
	}()
	wg.Wait()

	if firstErr != nil {
		return dto.AdminDashboardResponse{}, middleware.WrapAppError(http.StatusInternalServerError, "dashboard 查询失败", firstErr)
	}
	return dto.AdminDashboardResponse{
		TotalMedia:      totalMedia,
		TotalUsers:      totalUsers,
		TotalCategories: totalCategories,
		TotalViews:      totalViews,
		RecentMedia:     serializeMediaList(recentMedia),
		TopMedia:        serializeMediaList(topMedia),
	}, nil
}

// ListUsers 分页用户列表 + 三项 count。
func (s *AdminService) ListUsers(page, limit int, search string) ([]dto.AdminUserListItem, int64, error) {
	page, limit = util.SafePagination(page, limit, 100)
	base := s.db.Model(&model.User{})
	if search != "" {
		base = base.Where("username LIKE ?", "%"+search+"%")
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "统计失败", err)
	}
	var users []model.User
	if err := base.Order("created_at DESC").
		Offset(util.Offset(page, limit)).Limit(limit).
		Find(&users).Error; err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	if len(users) == 0 {
		return []dto.AdminUserListItem{}, total, nil
	}

	ids := make([]string, 0, len(users))
	for i := range users {
		ids = append(ids, users[i].ID)
	}
	favMap := countGroup(s.db, &model.Favorite{}, "user_id", ids)
	plMap := countGroup(s.db, &model.Playlist{}, "user_id", ids)
	whMap := countGroup(s.db, &model.WatchHistory{}, "user_id", ids)

	out := make([]dto.AdminUserListItem, 0, len(users))
	for i := range users {
		item := dto.AdminUserListItem{
			ID:        users[i].ID,
			Username:  users[i].Username,
			Role:      users[i].Role,
			Avatar:    users[i].Avatar,
			IsActive:  users[i].IsActive,
			CreatedAt: util.FormatISO(users[i].CreatedAt),
			UpdatedAt: util.FormatISO(users[i].UpdatedAt),
		}
		item.Count.Favorites = favMap[users[i].ID]
		item.Count.Playlists = plMap[users[i].ID]
		item.Count.WatchHistory = whMap[users[i].ID]
		out = append(out, item)
	}
	return out, total, nil
}

// UpdateUser 修改角色 / 激活状态，含业务约束。
func (s *AdminService) UpdateUser(id, currentUID string, req dto.AdminUpdateUserRequest) (*dto.AdminUserListItem, error) {
	var u model.User
	if err := s.db.Take(&u, "id = ?", id).Error; err != nil {
		return nil, middleware.NewAppError(http.StatusNotFound, "User not found")
	}
	if req.Role != nil && u.Role == "ADMIN" && *req.Role == "USER" {
		var count int64
		s.db.Model(&model.User{}).Where("role = ?", "ADMIN").Count(&count)
		if count <= 1 {
			return nil, middleware.NewAppError(http.StatusBadRequest, "Cannot demote the last admin user")
		}
	}
	if req.IsActive != nil && id == currentUID && !*req.IsActive {
		return nil, middleware.NewAppError(http.StatusBadRequest, "Cannot deactivate yourself")
	}
	updates := map[string]any{}
	if req.Role != nil {
		updates["role"] = *req.Role
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}
	if len(updates) > 0 {
		if err := s.db.Model(&model.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return nil, middleware.WrapAppError(http.StatusInternalServerError, "更新失败", err)
		}
	}
	var fresh model.User
	if err := s.db.Take(&fresh, "id = ?", id).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	item := dto.AdminUserListItem{
		ID: fresh.ID, Username: fresh.Username, Role: fresh.Role, Avatar: fresh.Avatar,
		IsActive:  fresh.IsActive,
		CreatedAt: util.FormatISO(fresh.CreatedAt),
		UpdatedAt: util.FormatISO(fresh.UpdatedAt),
	}
	return &item, nil
}

// DeleteUser 删除；不允许删 ADMIN。
func (s *AdminService) DeleteUser(id string) error {
	var u model.User
	if err := s.db.Take(&u, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return middleware.NewAppError(http.StatusNotFound, "User not found")
		}
		return middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	if u.Role == "ADMIN" {
		return middleware.NewAppError(http.StatusBadRequest, "Cannot delete admin user")
	}
	if err := s.db.Delete(&model.User{}, "id = ?", id).Error; err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "删除失败", err)
	}
	return nil
}

// GetSettings 列出全部 key/value。
func (s *AdminService) GetSettings() ([]dto.AdminSettingEntry, error) {
	var rows []model.SystemSetting
	if err := s.db.Order("key ASC").Find(&rows).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	out := make([]dto.AdminSettingEntry, 0, len(rows))
	for i := range rows {
		out = append(out, dto.AdminSettingEntry{Key: rows[i].Key, Value: rows[i].Value})
	}
	return out, nil
}

// allowedSettingKeys 是系统设置允许的 key 白名单。
var allowedSettingKeys = map[string]struct{}{
	"siteName":                {},
	"allowRegistration":       {},
	"enableRateLimit":         {},
	"proxyAllowedExtensions":  {},
}

// UpdateSetting upsert 单个 key，返回当前值。
func (s *AdminService) UpdateSetting(key, value string) (dto.AdminSettingEntry, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return dto.AdminSettingEntry{}, middleware.NewAppError(http.StatusBadRequest, "key 不能为空")
	}
	if _, ok := allowedSettingKeys[key]; !ok {
		return dto.AdminSettingEntry{}, middleware.NewAppError(http.StatusBadRequest, "不支持的设置项: "+key)
	}
	var existing model.SystemSetting
	err := s.db.Where("key = ?", key).Take(&existing).Error
	if err == nil {
		if err := s.db.Model(&model.SystemSetting{}).Where("key = ?", key).Update("value", value).Error; err != nil {
			return dto.AdminSettingEntry{}, middleware.WrapAppError(http.StatusInternalServerError, "更新失败", err)
		}
		return dto.AdminSettingEntry{Key: key, Value: value}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return dto.AdminSettingEntry{}, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	n := model.SystemSetting{Key: key, Value: value}
	if err := s.db.Create(&n).Error; err != nil {
		return dto.AdminSettingEntry{}, middleware.WrapAppError(http.StatusInternalServerError, "写入失败", err)
	}
	return dto.AdminSettingEntry{Key: key, Value: value}, nil
}

// AdminListMedia 管理员视角：额外支持 status 过滤；其他复用 media service 相同逻辑。
func (s *AdminService) AdminListMedia(page, limit int, search, status string) (dto.MediaListResponse, error) {
	page, limit = util.SafePagination(page, limit, 100)
	base := s.db.Model(&model.Media{})
	if search != "" {
		kw := "%" + search + "%"
		base = base.Where("title LIKE ? OR description LIKE ? OR m3u8_url LIKE ?", kw, kw, kw)
	}
	if status != "" {
		base = base.Where("status = ?", status)
	}
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return dto.MediaListResponse{}, middleware.WrapAppError(http.StatusInternalServerError, "统计失败", err)
	}
	var rows []model.Media
	if err := base.Preload("Category").Preload("Tags").Preload("Tags.Tag").
		Order("created_at DESC").
		Offset(util.Offset(page, limit)).Limit(limit).
		Find(&rows).Error; err != nil {
		return dto.MediaListResponse{}, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	totalPages := int((total + int64(limit) - 1) / int64(limit))
	return dto.MediaListResponse{
		Items: serializeMediaList(rows), Total: total,
		Page: page, Limit: limit, TotalPages: totalPages,
	}, nil
}

// BatchDelete 批量删除。
func (s *AdminService) BatchDelete(ids []string) (dto.BatchOperationResponse, error) {
	tx := s.db.Where("id IN ?", ids).Delete(&model.Media{})
	if tx.Error != nil {
		return dto.BatchOperationResponse{}, middleware.WrapAppError(http.StatusInternalServerError, "删除失败", tx.Error)
	}
	if tx.RowsAffected == 0 {
		return dto.BatchOperationResponse{}, middleware.NewAppError(http.StatusNotFound, "未找到匹配记录")
	}
	return dto.BatchOperationResponse{AffectedCount: tx.RowsAffected}, nil
}

// BatchUpdateStatus 批量改状态（仅 ACTIVE / INACTIVE）。
func (s *AdminService) BatchUpdateStatus(ids []string, status string) (dto.BatchOperationResponse, error) {
	tx := s.db.Model(&model.Media{}).Where("id IN ?", ids).Update("status", status)
	if tx.Error != nil {
		return dto.BatchOperationResponse{}, middleware.WrapAppError(http.StatusInternalServerError, "更新失败", tx.Error)
	}
	if tx.RowsAffected == 0 {
		return dto.BatchOperationResponse{}, middleware.NewAppError(http.StatusNotFound, "未找到匹配记录")
	}
	return dto.BatchOperationResponse{AffectedCount: tx.RowsAffected}, nil
}

// BatchUpdateCategory 批量改分类（categoryID 可为 nil）。
func (s *AdminService) BatchUpdateCategory(ids []string, categoryID *string) (dto.BatchOperationResponse, error) {
	if categoryID != nil && *categoryID != "" {
		var count int64
		s.db.Model(&model.Category{}).Where("id = ?", *categoryID).Count(&count)
		if count == 0 {
			return dto.BatchOperationResponse{}, middleware.NewAppError(http.StatusNotFound, "分类不存在")
		}
	}
	var val any
	if categoryID == nil || *categoryID == "" {
		val = nil
	} else {
		val = *categoryID
	}
	tx := s.db.Model(&model.Media{}).Where("id IN ?", ids).Update("category_id", val)
	if tx.Error != nil {
		return dto.BatchOperationResponse{}, middleware.WrapAppError(http.StatusInternalServerError, "更新失败", tx.Error)
	}
	if tx.RowsAffected == 0 {
		return dto.BatchOperationResponse{}, middleware.NewAppError(http.StatusNotFound, "未找到匹配记录")
	}
	return dto.BatchOperationResponse{AffectedCount: tx.RowsAffected}, nil
}

// ---- helpers ----

func countGroup(db *gorm.DB, model any, col string, ids []string) map[string]int64 {
	out := map[string]int64{}
	if len(ids) == 0 {
		return out
	}
	type row struct {
		Key string
		C   int64
	}
	var rs []row
	db.Model(model).
		Select(col+" AS key, COUNT(id) AS c").
		Where(col+" IN ?", ids).
		Group(col).
		Scan(&rs)
	for _, r := range rs {
		out[r.Key] = r.C
	}
	return out
}

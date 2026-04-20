// Package service
// activity.go 对齐 loginRecordService.ts：聚合查询 + 单用户活动。
package service

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// ActivityService 封装 login records + 观看记录聚合。
type ActivityService struct{ db *gorm.DB }

// NewActivityService 构造。
func NewActivityService(db *gorm.DB) *ActivityService { return &ActivityService{db: db} }

// CreateLoginRecord 登录成功后调用，解析 UA 并写入。
func (s *ActivityService) CreateLoginRecord(userID string, ip string, rawUA string) error {
	info := util.ParseUserAgent(rawUA)
	var uaPtr *string
	if rawUA != "" {
		ua2 := rawUA
		uaPtr = &ua2
	}
	var ipPtr *string
	if ip != "" {
		i := ip
		ipPtr = &i
	}
	rec := model.LoginRecord{
		UserID:    userID,
		IP:        ipPtr,
		UserAgent: uaPtr,
		Browser:   info.Browser,
		OS:        info.OS,
		Device:    info.Device,
	}
	if err := s.db.Create(&rec).Error; err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "写入登录记录失败", err)
	}
	return nil
}

// ListUserLoginRecords 分页某用户的登录记录。
func (s *ActivityService) ListUserLoginRecords(userID string, page, limit int) ([]dto.LoginRecordResponse, int64, error) {
	page, limit = util.SafePagination(page, limit, 100)
	base := s.db.Model(&model.LoginRecord{}).Where("user_id = ?", userID)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "统计失败", err)
	}
	var rows []model.LoginRecord
	if err := base.Order("created_at DESC").
		Offset(util.Offset(page, limit)).Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	return serializeLoginRecords(rows, nil), total, nil
}

// UserSummary 单用户活动概要。
func (s *ActivityService) UserSummary(userID string) (dto.UserActivitySummaryResponse, error) {
	var u model.User
	if err := s.db.Take(&u, "id = ?", userID).Error; err != nil {
		return dto.UserActivitySummaryResponse{}, middleware.NewAppError(http.StatusNotFound, "User not found")
	}
	var (
		loginCount     int64
		watchCount     int64
		completedCount int64
		lastLogin      model.LoginRecord
		hasLastLogin   = true
	)
	s.db.Model(&model.LoginRecord{}).Where("user_id = ?", userID).Count(&loginCount)
	s.db.Model(&model.WatchHistory{}).Where("user_id = ?", userID).Count(&watchCount)
	s.db.Model(&model.WatchHistory{}).Where("user_id = ? AND completed = ?", userID, true).Count(&completedCount)
	if err := s.db.Where("user_id = ?", userID).Order("created_at DESC").Take(&lastLogin).Error; err != nil {
		hasLastLogin = false
	}

	resp := dto.UserActivitySummaryResponse{
		TotalLogins:    loginCount,
		TotalWatched:   watchCount,
		TotalCompleted: completedCount,
	}
	userInfo := struct {
		Username  string `json:"username"`
		Role      string `json:"role"`
		IsActive  bool   `json:"isActive"`
		CreatedAt string `json:"createdAt"`
	}{u.Username, u.Role, u.IsActive, util.FormatISO(u.CreatedAt)}
	resp.User = &userInfo
	if hasLastLogin {
		rr := serializeLoginRecords([]model.LoginRecord{lastLogin}, nil)[0]
		resp.LastLogin = &rr
	}
	return resp, nil
}

// Aggregate 全局活动聚合（对齐 loginRecordService.getActivityAggregate）。
// 并行执行 10 个聚合查询，再对 topN 结果做二次查询。
func (s *ActivityService) Aggregate() (dto.UserActivityAggregateResponse, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterdayStart := todayStart.Add(-24 * time.Hour)
	weekStart := todayStart.Add(-7 * 24 * time.Hour)

	var (
		resp         dto.UserActivityAggregateResponse
		recentLogin  []model.LoginRecord
		recentWatch  []model.WatchHistory
		topMediaGroup []struct {
			MediaID string
			C       int64
		}
		topUserGroup []struct {
			UserID string
			C      int64
		}
		wg  sync.WaitGroup
		mu  sync.Mutex
		errFirst error
		saveErr = func(e error) {
			if e == nil {
				return
			}
			mu.Lock()
			if errFirst == nil {
				errFirst = e
			}
			mu.Unlock()
		}
	)

	wg.Add(10)
	go func() { defer wg.Done(); saveErr(s.db.Model(&model.LoginRecord{}).Count(&resp.LoginStats.TotalLogins).Error) }()
	go func() {
		defer wg.Done()
		type row struct{ C int64 }
		var r row
		saveErr(s.db.Raw("SELECT COUNT(DISTINCT user_id) AS c FROM login_records").Scan(&r).Error)
		resp.LoginStats.UniqueUsers = r.C
	}()
	go func() {
		defer wg.Done()
		saveErr(s.db.Model(&model.LoginRecord{}).Where("created_at >= ?", todayStart).Count(&resp.LoginStats.TodayLogins).Error)
	}()
	go func() {
		defer wg.Done()
		saveErr(s.db.Model(&model.LoginRecord{}).
			Where("created_at >= ? AND created_at < ?", yesterdayStart, todayStart).
			Count(&resp.LoginStats.YesterdayLogins).Error)
	}()
	go func() {
		defer wg.Done()
		saveErr(s.db.Model(&model.LoginRecord{}).Where("created_at >= ?", weekStart).
			Count(&resp.LoginStats.Last7DaysLogins).Error)
	}()
	go func() {
		defer wg.Done()
		saveErr(s.db.Model(&model.WatchHistory{}).Count(&resp.WatchStats.TotalWatchRecords).Error)
	}()
	go func() {
		defer wg.Done()
		saveErr(s.db.Model(&model.WatchHistory{}).Where("completed = ?", true).Count(&resp.WatchStats.TotalCompleted).Error)
	}()
	go func() {
		defer wg.Done()
		type agg struct{ Total float64 }
		var a agg
		saveErr(s.db.Model(&model.WatchHistory{}).Select("COALESCE(SUM(progress),0) AS total").Scan(&a).Error)
		resp.WatchStats.TotalWatchTime = a.Total
	}()
	go func() {
		defer wg.Done()
		saveErr(s.db.Order("created_at DESC").Limit(20).Find(&recentLogin).Error)
	}()
	go func() {
		defer wg.Done()
		saveErr(s.db.Order("updated_at DESC").Limit(20).Find(&recentWatch).Error)
	}()
	wg.Wait()
	if errFirst != nil {
		return resp, middleware.WrapAppError(http.StatusInternalServerError, "活动聚合查询失败", errFirst)
	}

	// topWatched / topActiveUsers 在 SQLite 下用 group by 语句直接查
	if err := s.db.Model(&model.WatchHistory{}).
		Select("media_id, COUNT(id) AS c").
		Group("media_id").
		Order("c DESC").
		Limit(10).
		Scan(&topMediaGroup).Error; err != nil {
		return resp, middleware.WrapAppError(http.StatusInternalServerError, "top media 查询失败", err)
	}
	if err := s.db.Model(&model.LoginRecord{}).
		Select("user_id, COUNT(id) AS c").
		Group("user_id").
		Order("c DESC").
		Limit(10).
		Scan(&topUserGroup).Error; err != nil {
		return resp, middleware.WrapAppError(http.StatusInternalServerError, "top user 查询失败", err)
	}

	// 补齐 title / username 等关联信息
	mediaIDs := make([]string, 0, len(topMediaGroup))
	for _, r := range topMediaGroup {
		mediaIDs = append(mediaIDs, r.MediaID)
	}
	mediaTitles := loadMediaTitles(s.db, mediaIDs)

	// 各 media 的 completed 数
	type completed struct {
		MediaID string
		C       int64
	}
	var completedRows []completed
	if len(mediaIDs) > 0 {
		s.db.Model(&model.WatchHistory{}).
			Select("media_id, COUNT(id) AS c").
			Where("media_id IN ? AND completed = ?", mediaIDs, true).
			Group("media_id").
			Scan(&completedRows)
	}
	completedMap := map[string]int64{}
	for _, r := range completedRows {
		completedMap[r.MediaID] = r.C
	}
	for _, r := range topMediaGroup {
		resp.TopWatchedMedia = append(resp.TopWatchedMedia, dto.TopWatchedMedia{
			MediaID:        r.MediaID,
			Title:          firstNonEmptyStr(mediaTitles[r.MediaID], "未知"),
			WatchCount:     r.C,
			CompletedCount: completedMap[r.MediaID],
		})
	}

	userIDs := make([]string, 0, len(topUserGroup))
	for _, r := range topUserGroup {
		userIDs = append(userIDs, r.UserID)
	}
	usernames := loadUsernames(s.db, userIDs)
	type userWatch struct {
		UserID string
		C      int64
	}
	var watchCountRows []userWatch
	if len(userIDs) > 0 {
		s.db.Model(&model.WatchHistory{}).
			Select("user_id, COUNT(id) AS c").
			Where("user_id IN ?", userIDs).
			Group("user_id").
			Scan(&watchCountRows)
	}
	watchCountMap := map[string]int64{}
	for _, r := range watchCountRows {
		watchCountMap[r.UserID] = r.C
	}
	for _, r := range topUserGroup {
		resp.TopActiveUsers = append(resp.TopActiveUsers, dto.TopActiveUser{
			UserID:     r.UserID,
			Username:   firstNonEmptyStr(usernames[r.UserID], "未知"),
			LoginCount: r.C,
			WatchCount: watchCountMap[r.UserID],
		})
	}

	// recent logins / watches 补 username / title
	{
		uids := uniqueUserIDs(recentLogin)
		unameMap := loadUsernames(s.db, uids)
		resp.RecentLogins = serializeLoginRecords(recentLogin, unameMap)
	}
	{
		uids := make([]string, 0, len(recentWatch))
		mids := make([]string, 0, len(recentWatch))
		for i := range recentWatch {
			uids = append(uids, recentWatch[i].UserID)
			mids = append(mids, recentWatch[i].MediaID)
		}
		unameMap := loadUsernames(s.db, uniqueStrings(uids))
		mtitleMap := loadMediaTitles(s.db, uniqueStrings(mids))
		resp.RecentWatchRecords = make([]dto.RecentWatchRecord, 0, len(recentWatch))
		for i := range recentWatch {
			r := dto.RecentWatchRecord{
				ID:         recentWatch[i].ID,
				UserID:     recentWatch[i].UserID,
				MediaID:    recentWatch[i].MediaID,
				MediaTitle: firstNonEmptyStr(mtitleMap[recentWatch[i].MediaID], "未知"),
				Progress:   recentWatch[i].Progress,
				Duration:   recentWatch[i].Duration,
				Percentage: recentWatch[i].Percentage,
				Completed:  recentWatch[i].Completed,
				UpdatedAt:  util.FormatISO(recentWatch[i].UpdatedAt),
			}
			if u, ok := unameMap[recentWatch[i].UserID]; ok {
				uu := u
				r.Username = &uu
			}
			resp.RecentWatchRecords = append(resp.RecentWatchRecords, r)
		}
	}

	// 保持聚合结果输出顺序（Go map 随机序，排序后落表更稳）
	sort.SliceStable(resp.TopWatchedMedia, func(i, j int) bool {
		return resp.TopWatchedMedia[i].WatchCount > resp.TopWatchedMedia[j].WatchCount
	})
	sort.SliceStable(resp.TopActiveUsers, func(i, j int) bool {
		return resp.TopActiveUsers[i].LoginCount > resp.TopActiveUsers[j].LoginCount
	})

	return resp, nil
}

// ---- helpers ----

func serializeLoginRecords(rows []model.LoginRecord, nameMap map[string]string) []dto.LoginRecordResponse {
	out := make([]dto.LoginRecordResponse, 0, len(rows))
	for i := range rows {
		r := dto.LoginRecordResponse{
			ID:        rows[i].ID,
			UserID:    rows[i].UserID,
			IP:        rows[i].IP,
			UserAgent: rows[i].UserAgent,
			Browser:   rows[i].Browser,
			OS:        rows[i].OS,
			Device:    rows[i].Device,
			CreatedAt: util.FormatISO(rows[i].CreatedAt),
		}
		if nameMap != nil {
			if n, ok := nameMap[rows[i].UserID]; ok {
				nn := n
				r.Username = &nn
			}
		}
		out = append(out, r)
	}
	return out
}

func loadUsernames(db *gorm.DB, ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	var rows []model.User
	db.Select("id", "username").Where("id IN ?", ids).Find(&rows)
	for i := range rows {
		out[rows[i].ID] = rows[i].Username
	}
	return out
}

func loadMediaTitles(db *gorm.DB, ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	var rows []model.Media
	db.Select("id", "title").Where("id IN ?", ids).Find(&rows)
	for i := range rows {
		out[rows[i].ID] = rows[i].Title
	}
	return out
}

func uniqueUserIDs(rows []model.LoginRecord) []string {
	s := make([]string, 0, len(rows))
	for i := range rows {
		s = append(s, rows[i].UserID)
	}
	return uniqueStrings(s)
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

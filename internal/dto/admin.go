// Package dto
// admin.go 汇总 Admin 相关请求/响应 DTO。
// 对齐 shared/validation.ts 的 updateUserSchema / updateSettingSchema /
// batchDeleteSchema / batchStatusSchema / batchCategorySchema。
package dto

// AdminDashboardResponse GET /admin/dashboard。
type AdminDashboardResponse struct {
	TotalMedia      int64           `json:"totalMedia"`
	TotalUsers      int64           `json:"totalUsers"`
	TotalCategories int64           `json:"totalCategories"`
	TotalViews      int64           `json:"totalViews"`
	RecentMedia     []MediaResponse `json:"recentMedia"`
	TopMedia        []MediaResponse `json:"topMedia"`
}

// AdminUserListItem /admin/users 元素。
type AdminUserListItem struct {
	ID        string  `json:"id"`
	Username  string  `json:"username"`
	Role      string  `json:"role"`
	Avatar    *string `json:"avatar,omitempty"`
	IsActive  bool    `json:"isActive"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	Count     struct {
		Favorites    int64 `json:"favorites"`
		Playlists    int64 `json:"playlists"`
		WatchHistory int64 `json:"watchHistory"`
	} `json:"_count"`
}

// AdminUpdateUserRequest PUT /admin/users/:id。
type AdminUpdateUserRequest struct {
	Role     *string `json:"role,omitempty" binding:"omitempty,oneof=USER ADMIN"`
	IsActive *bool   `json:"isActive,omitempty"`
}

// AdminSettingEntry GET /admin/settings 元素。
type AdminSettingEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// AdminUpdateSettingRequest PUT /admin/settings。
type AdminUpdateSettingRequest struct {
	Key   string `json:"key" binding:"required"`
	Value string `json:"value"` // 允许空值，用于清空配置
}

// AdminBatchDeleteRequest POST /admin/media/batch-delete。
type AdminBatchDeleteRequest struct {
	IDs []string `json:"ids" binding:"required,min=1,max=500,dive,required"`
}

// AdminBatchStatusRequest PUT /admin/media/batch-status。
type AdminBatchStatusRequest struct {
	IDs    []string `json:"ids" binding:"required,min=1,max=500,dive,required"`
	Status string   `json:"status" binding:"required,oneof=ACTIVE INACTIVE"`
}

// AdminBatchCategoryRequest PUT /admin/media/batch-category。
// CategoryID 可为空字符串（代表清空分类）。
type AdminBatchCategoryRequest struct {
	IDs        []string `json:"ids" binding:"required,min=1,max=500,dive,required"`
	CategoryID *string  `json:"categoryId"`
}

// BatchOperationResponse 批量操作响应。
type BatchOperationResponse struct {
	AffectedCount int64 `json:"affectedCount"`
}

// LoginRecordResponse /admin/users/:id/login-records 元素。
type LoginRecordResponse struct {
	ID        string  `json:"id"`
	UserID    string  `json:"userId"`
	IP        *string `json:"ip,omitempty"`
	UserAgent *string `json:"userAgent,omitempty"`
	Browser   *string `json:"browser,omitempty"`
	OS        *string `json:"os,omitempty"`
	Device    *string `json:"device,omitempty"`
	Username  *string `json:"username,omitempty"`
	CreatedAt string  `json:"createdAt"`
}

// UserActivitySummaryResponse /admin/users/:id/activity-summary。
type UserActivitySummaryResponse struct {
	User *struct {
		Username  string `json:"username"`
		Role      string `json:"role"`
		IsActive  bool   `json:"isActive"`
		CreatedAt string `json:"createdAt"`
	} `json:"user,omitempty"`
	TotalLogins    int64                `json:"totalLogins"`
	LastLogin      *LoginRecordResponse `json:"lastLogin,omitempty"`
	TotalWatched   int64                `json:"totalWatched"`
	TotalCompleted int64                `json:"totalCompleted"`
}

// UserActivityAggregateResponse /admin/activity。
type UserActivityAggregateResponse struct {
	LoginStats struct {
		TotalLogins      int64 `json:"totalLogins"`
		UniqueUsers      int64 `json:"uniqueUsers"`
		TodayLogins      int64 `json:"todayLogins"`
		YesterdayLogins  int64 `json:"yesterdayLogins"`
		Last7DaysLogins  int64 `json:"last7DaysLogins"`
	} `json:"loginStats"`
	WatchStats struct {
		TotalWatchRecords int64   `json:"totalWatchRecords"`
		TotalCompleted    int64   `json:"totalCompleted"`
		TotalWatchTime    float64 `json:"totalWatchTime"`
	} `json:"watchStats"`
	RecentLogins       []LoginRecordResponse `json:"recentLogins"`
	TopWatchedMedia    []TopWatchedMedia     `json:"topWatchedMedia"`
	TopActiveUsers     []TopActiveUser       `json:"topActiveUsers"`
	RecentWatchRecords []RecentWatchRecord   `json:"recentWatchRecords"`
}

// TopWatchedMedia 聚合元素。
type TopWatchedMedia struct {
	MediaID        string `json:"mediaId"`
	Title          string `json:"title"`
	WatchCount     int64  `json:"watchCount"`
	CompletedCount int64  `json:"completedCount"`
}

// TopActiveUser 聚合元素。
type TopActiveUser struct {
	UserID     string `json:"userId"`
	Username   string `json:"username"`
	LoginCount int64  `json:"loginCount"`
	WatchCount int64  `json:"watchCount"`
}

// RecentWatchRecord 最近观看记录（含 username + mediaTitle）。
type RecentWatchRecord struct {
	ID         string  `json:"id"`
	UserID     string  `json:"userId"`
	Username   *string `json:"username,omitempty"`
	MediaID    string  `json:"mediaId"`
	MediaTitle string  `json:"mediaTitle"`
	Progress   float64 `json:"progress"`
	Duration   float64 `json:"duration"`
	Percentage float64 `json:"percentage"`
	Completed  bool    `json:"completed"`
	UpdatedAt  string  `json:"updatedAt"`
}

// Package dto
// import_.go 定义批量导入的输入输出 DTO。
// 对齐 shared/types 的 ImportItem / ImportPreviewResponse / ImportResult / ImportError。
package dto

// ImportItem 是 parser 统一输出结构。
type ImportItem struct {
	Title        string   `json:"title"`
	M3u8URL      string   `json:"m3u8Url"`
	PosterURL    *string  `json:"posterUrl,omitempty"`
	Description  *string  `json:"description,omitempty"`
	Year         *int     `json:"year,omitempty"`
	Artist       *string  `json:"artist,omitempty"`
	CategoryName *string  `json:"categoryName,omitempty"`
	TagNames     []string `json:"tagNames,omitempty"`
}

// ImportError 单条错误。
type ImportError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ImportPreviewResponse /import/preview 响应。
type ImportPreviewResponse struct {
	Items        []ImportItem  `json:"items"`
	TotalCount   int           `json:"totalCount"`
	ValidCount   int           `json:"validCount"`
	InvalidCount int           `json:"invalidCount"`
	Errors       []ImportError `json:"errors"`
	Format       string        `json:"format"`
	FileName     string        `json:"fileName,omitempty"`
}

// ImportExecuteRequest /import/execute 请求。
type ImportExecuteRequest struct {
	Items    []ImportItem `json:"items" binding:"required"`
	Format   string       `json:"format"`
	FileName string       `json:"fileName,omitempty"`
}

// ImportResult /import/execute 响应。
type ImportResult struct {
	TotalCount   int           `json:"totalCount"`
	SuccessCount int           `json:"successCount"`
	FailedCount  int           `json:"failedCount"`
	Errors       []ImportError `json:"errors"`
}

// ImportLogResponse /import/logs 元素。
type ImportLogResponse struct {
	ID           string  `json:"id"`
	UserID       string  `json:"userId"`
	Format       string  `json:"format"`
	FileName     *string `json:"fileName,omitempty"`
	TotalCount   int     `json:"totalCount"`
	SuccessCount int     `json:"successCount"`
	FailedCount  int     `json:"failedCount"`
	Status       string  `json:"status"`
	Errors       *string `json:"errors,omitempty"`
	CreatedAt    string  `json:"createdAt"`
}

// UploadPosterResponse POST /upload/poster 响应。
type UploadPosterResponse struct {
	URL string `json:"url"`
}

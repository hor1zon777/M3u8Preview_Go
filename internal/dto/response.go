// Package dto 定义请求与响应 DTO 和通用响应信封。
package dto

// APIResponse 是后端所有 JSON 响应的统一信封，与 TypeScript ApiResponse<T> 等价。
// 为简化处理，Data 使用 interface{}（any），Meta 可为空。
//
// Code 是机器可读的错误码（如 "WORKER_AUDIO_SHA256_MISMATCH"），仅在失败响应时填充；
// 客户端可据此区分不同失败原因，无需 parse Error 文本。
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Code    string      `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
}

// OK 是语义化构造器，便于 handler 使用。
func OK(data interface{}) APIResponse {
	return APIResponse{Success: true, Data: data}
}

// PaginatedData 对齐前端 PaginatedResponse<T>，将 items 与分页元信息打包到 data 字段。
type PaginatedData struct {
	Items      interface{} `json:"items"`
	Total      int64       `json:"total"`
	Page       int         `json:"page"`
	Limit      int         `json:"limit"`
	TotalPages int         `json:"totalPages"`
}

// Paginated 构造分页响应。
func Paginated(data interface{}, total int64, page, limit int) APIResponse {
	totalPages := 0
	if limit > 0 {
		totalPages = int((total + int64(limit) - 1) / int64(limit))
	}
	return APIResponse{
		Success: true,
		Data: PaginatedData{
			Items:      data,
			Total:      total,
			Page:       page,
			Limit:      limit,
			TotalPages: totalPages,
		},
	}
}

// Fail 构造失败响应（一般由 errorHandler 统一调用，handler 直接抛 AppError 即可）。
func Fail(message string) APIResponse {
	return APIResponse{Success: false, Error: message}
}

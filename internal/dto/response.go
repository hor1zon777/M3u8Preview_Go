// Package dto 定义请求与响应 DTO 和通用响应信封。
package dto

// APIResponse 是后端所有 JSON 响应的统一信封，与 TypeScript ApiResponse<T> 等价。
// 为简化处理，Data 使用 interface{}（any），Meta 可为空。
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
	Meta    *PageMeta   `json:"meta,omitempty"`
}

// PageMeta 对齐 TS PaginatedResponse.meta，只有分页端点使用。
type PageMeta struct {
	Total int64 `json:"total"`
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
}

// OK 是语义化构造器，便于 handler 使用。
func OK(data interface{}) APIResponse {
	return APIResponse{Success: true, Data: data}
}

// Paginated 构造分页响应。
func Paginated(data interface{}, total int64, page, limit int) APIResponse {
	return APIResponse{
		Success: true,
		Data:    data,
		Meta:    &PageMeta{Total: total, Page: page, Limit: limit},
	}
}

// Fail 构造失败响应（一般由 errorHandler 统一调用，handler 直接抛 AppError 即可）。
func Fail(message string) APIResponse {
	return APIResponse{Success: false, Error: message}
}

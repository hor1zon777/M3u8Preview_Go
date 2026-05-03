// Package middleware 提供 Gin 中间件。
// error.go 对齐 packages/server/src/middleware/errorHandler.ts：
// - AppError 带 statusCode
// - 未知错误生成 eventId 并脱敏返回
// - panic 恢复统一走同一路径
package middleware

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
)

// AppError 是业务层主动抛出的可预期错误。
// 对齐 TS 版 AppError：携带 HTTP 状态码与用户可见消息。
//
// Code 是机器可读的错误码（如 "WORKER_AUDIO_SHA256_MISMATCH"），让 worker 客户端
// 能区分不同失败原因；HTTP status 仍然是粗粒度区分手段。Code 为空时只回 message。
type AppError struct {
	Status  int
	Code    string
	Message string
	Cause   error
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *AppError) Unwrap() error { return e.Cause }

// NewAppError 构造 AppError（不带 Code）。
func NewAppError(status int, message string) *AppError {
	return &AppError{Status: status, Message: message}
}

// NewAppErrorWithCode 构造带机器可读 code 的 AppError。
func NewAppErrorWithCode(status int, code, message string) *AppError {
	return &AppError{Status: status, Code: code, Message: message}
}

// WithCode 链式给已有 AppError 添加 code（保留原 status / message）。
func (e *AppError) WithCode(code string) *AppError {
	e.Code = code
	return e
}

// WrapAppError 在保留原始错误的前提下转成 AppError。
func WrapAppError(status int, message string, cause error) *AppError {
	return &AppError{Status: status, Message: message, Cause: cause}
}

// AbortWithAppError 让 handler 可以直接 c.Error(appErr) + c.Abort()。
// 业务层只要 return err，顶层 ErrorHandler 中间件负责转响应。
func AbortWithAppError(c *gin.Context, err *AppError) {
	_ = c.Error(err)
	c.Abort()
}

// Recovery 捕获 panic 并转成 500 响应（统一走 errorHandler 逻辑）。
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, rec any) {
		eventID := uuid.NewString()
		log.Printf("[panic] eventId=%s value=%v path=%s", eventID, rec, c.Request.URL.Path)
		if !c.Writer.Written() {
			c.AbortWithStatusJSON(http.StatusInternalServerError, dto.APIResponse{
				Success: false,
				Error:   "Internal server error",
				Message: eventID, // 借 message 字段回传 eventId，前端只用来关联日志
			})
		}
	})
}

// ErrorHandler 是挂在路由最后的统一错误处理器。
// Gin 没有 express 的 next(err) 机制，handler 层用 c.Error(err) 注册错误，本中间件在 Next 后读取并写响应。
func ErrorHandler(production bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}
		// 以第一个注册错误为主
		first := c.Errors[0].Err

		var appErr *AppError
		if errors.As(first, &appErr) {
			if !c.Writer.Written() {
				c.AbortWithStatusJSON(appErr.Status, dto.APIResponse{
					Success: false,
					Error:   appErr.Message,
					Code:    appErr.Code,
				})
			}
			return
		}

		// 未知错误：生成 eventId 脱敏返回
		eventID := uuid.NewString()
		if production {
			log.Printf("[error] eventId=%s type=%T", eventID, first)
		} else {
			log.Printf("[error] eventId=%s err=%v", eventID, first)
		}
		if !c.Writer.Written() {
			c.AbortWithStatusJSON(http.StatusInternalServerError, dto.APIResponse{
				Success: false,
				Error:   "Internal server error",
				Message: eventID,
			})
		}
	}
}

// Package sse 提供 Server-Sent Events 写入与进度模型。
// 对齐 TS 版备份导出 / 恢复流程中 res.write("data: {json}\n\n") 的协议。
package sse

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// PrepareResponse 设置 SSE 必需响应头。
// X-Accel-Buffering: no 让 nginx 关闭缓冲，保证前端实时收到事件（nginx.conf 不显式关 proxy_buffering）。
func PrepareResponse(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
}

// Writer 封装 gin.ResponseWriter，提供类型安全的 SSE 写入。
type Writer struct {
	c *gin.Context
}

// NewWriter 构造。调用前应先 PrepareResponse。
func NewWriter(c *gin.Context) *Writer { return &Writer{c: c} }

// WriteJSON 把任意 JSON 可序列化对象写成一条 SSE 消息。
// 格式严格 "data: <json>\n\n"，与 TS 版完全一致。
func (w *Writer) WriteJSON(payload interface{}) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := w.c.Writer.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := w.c.Writer.Write(buf); err != nil {
		return err
	}
	if _, err := w.c.Writer.Write([]byte("\n\n")); err != nil {
		return err
	}
	w.c.Writer.Flush()
	return nil
}

// WriteComment 发送一条 ": ping" 保活注释（SSE 忽略以 : 开头的行）。
func (w *Writer) WriteComment(text string) error {
	if _, err := w.c.Writer.Write([]byte(": " + text + "\n\n")); err != nil {
		return err
	}
	w.c.Writer.Flush()
	return nil
}

// ErrClientGone 用于在连接已关闭时提前退出 producer 循环。
var ErrClientGone = errors.New("sse: client connection closed")

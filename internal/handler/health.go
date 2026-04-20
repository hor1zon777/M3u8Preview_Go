// Package handler 存放 Gin HTTP 处理器。每个领域一个文件。
package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Health 实现 GET /api/health，供 Docker HEALTHCHECK 与 nginx 探活使用。
// 对齐 packages/server/src/app.ts 中的同名端点。
func Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

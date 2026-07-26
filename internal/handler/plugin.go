// Package handler
// plugin.go 对接插件中心端点（均需 ADMIN 角色，路由组在 app.Build 挂鉴权）：
//
//	GET /api/v1/admin/plugins              插件列表（meta + enabled + 运行时状态摘要）
//	PUT /api/v1/admin/plugins/:id/enabled  切换启用开关，返回该插件最新状态
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/plugin"
)

// PluginHandler 插件中心端点。
type PluginHandler struct {
	reg *plugin.Registry
}

// NewPluginHandler 构造。
func NewPluginHandler(reg *plugin.Registry) *PluginHandler {
	return &PluginHandler{reg: reg}
}

// Register 挂载端点（调用方需已应用 Authenticate + RequireRole("ADMIN")）。
func (h *PluginHandler) Register(rg *gin.RouterGroup) {
	rg.GET("", h.list)
	rg.PUT("/:id/enabled", h.setEnabled)
}

// list 返回全部插件（注册顺序即展示顺序）。
func (h *PluginHandler) list(c *gin.Context) {
	plugins := h.reg.List()
	out := make([]dto.PluginInfo, 0, len(plugins))
	for _, p := range plugins {
		out = append(out, buildPluginInfo(p))
	}
	c.JSON(http.StatusOK, dto.OK(out))
}

// setEnabled 切换某插件启用状态并回显最新快照。
func (h *PluginHandler) setEnabled(c *gin.Context) {
	p, ok := h.reg.Get(c.Param("id"))
	if !ok {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotFound, "插件不存在"))
		return
	}

	var req dto.PluginSetEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "缺少 enabled 字段"))
		return
	}

	if err := p.SetEnabled(*req.Enabled); err != nil {
		// SetEnabled 复用各模块自身的配置更新路径，错误信息已是 user-facing 中文
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, err.Error()))
		return
	}
	c.JSON(http.StatusOK, dto.OK(buildPluginInfo(p)))
}

// buildPluginInfo 汇总单个插件的 meta + enabled + status 为响应 DTO。
func buildPluginInfo(p plugin.Plugin) dto.PluginInfo {
	meta := p.Meta()
	st := p.Status()

	items := make([]dto.PluginStatusItem, 0, len(st.Items))
	for _, it := range st.Items {
		items = append(items, dto.PluginStatusItem{Label: it.Label, Value: it.Value, Tone: it.Tone})
	}

	return dto.PluginInfo{
		ID:          meta.ID,
		Name:        meta.Name,
		Description: meta.Description,
		Version:     meta.Version,
		Icon:        meta.Icon,
		Category:    meta.Category,
		Enabled:     p.Enabled(),
		Healthy:     st.Healthy,
		Status:      items,
	}
}

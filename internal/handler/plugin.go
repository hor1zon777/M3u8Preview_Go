// Package handler
// plugin.go 对接插件中心端点（均需 ADMIN 角色，路由组在 app.Build 挂鉴权）：
//
//	GET    /api/v1/admin/plugins              插件列表（meta + enabled + 运行时状态摘要）
//	PUT    /api/v1/admin/plugins/:id/enabled  切换启用开关，返回该插件最新状态
//	POST   /api/v1/admin/plugins/import       导入声明式外部插件（multipart manifest.json）
//	DELETE /api/v1/admin/plugins/:id          删除外部插件（内置插件受保护）
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/plugin"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// 插件导入/删除的机器可读错误码。
const (
	// CodePluginIDConflictBuiltin 导入的 id 与内置插件冲突。
	CodePluginIDConflictBuiltin = "PLUGIN_ID_CONFLICT_BUILTIN"
	// CodePluginExists 同 id 外部插件已存在（可带 ?overwrite=true 升级）。
	CodePluginExists = "PLUGIN_EXISTS"
	// CodePluginBuiltinProtected 内置插件不允许删除。
	CodePluginBuiltinProtected = "PLUGIN_BUILTIN_PROTECTED"
)

// PluginHandler 插件中心端点。
type PluginHandler struct {
	reg *plugin.Registry
	ext *service.ExternalPluginService
}

// NewPluginHandler 构造。
func NewPluginHandler(reg *plugin.Registry, ext *service.ExternalPluginService) *PluginHandler {
	return &PluginHandler{reg: reg, ext: ext}
}

// Register 挂载端点（调用方需已应用 Authenticate + RequireRole("ADMIN")）。
func (h *PluginHandler) Register(rg *gin.RouterGroup) {
	rg.GET("", h.list)
	rg.PUT("/:id/enabled", h.setEnabled)
	rg.POST("/import", h.importManifest)
	rg.DELETE("/:id", h.deleteExternal)
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

// importManifest 导入声明式外部插件（multipart file 字段 = manifest.json）。
// ?overwrite=true 时对已存在的同 id 外部插件按升级处理（保留 enabled 状态）。
func (h *PluginHandler) importManifest(c *gin.Context) {
	overwrite := c.Query("overwrite") == "true"

	fh, err := c.FormFile("file")
	if err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "缺少 file 字段（manifest.json）"))
		return
	}
	// size 前置校验 + service 内 LimitReader 双重防护（仿 backup 导入模式）。
	if fh.Size > service.MaxManifestBytes {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "manifest 超过 64KB 上限"))
		return
	}
	f, err := fh.Open()
	if err != nil {
		middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusBadRequest, "读取文件失败", err))
		return
	}
	defer func() { _ = f.Close() }()

	m, raw, err := h.ext.ParseManifest(f)
	if err != nil {
		abortPluginErr(c, err)
		return
	}

	// id 与内置插件冲突：外部插件与内置共用命名空间，内置优先。
	if p, ok := h.reg.Get(m.ID); ok {
		if _, isExt := p.(plugin.ExternalMarker); !isExt {
			middleware.AbortWithAppError(c, middleware.NewAppErrorWithCode(
				http.StatusConflict, CodePluginIDConflictBuiltin, "id 与内置插件冲突: "+m.ID))
			return
		}
	}

	rec, isUpdate, err := h.ext.Import(m, raw, overwrite)
	if err != nil {
		if errors.Is(err, service.ErrExternalPluginExists) {
			middleware.AbortWithAppError(c, middleware.NewAppErrorWithCode(
				http.StatusConflict, CodePluginExists, "同 ID 的外部插件已存在，可选择覆盖升级"))
			return
		}
		abortPluginErr(c, err)
		return
	}

	// 注册表同步：升级时就地替换记录（保持展示顺序），新导入则注册。
	if p, ok := h.reg.Get(rec.ID); ok && isUpdate {
		if ext, isExt := p.(*plugin.External); isExt {
			ext.UpdateRecord(*rec)
		}
	} else {
		ext := plugin.NewExternal(h.ext, *rec)
		if err := h.reg.Register(ext); err != nil {
			middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusInternalServerError, "注册插件失败", err))
			return
		}
	}

	p, _ := h.reg.Get(rec.ID)
	c.JSON(http.StatusCreated, dto.OK(buildPluginInfo(p)))
}

// deleteExternal 删除外部插件；内置插件受保护。
func (h *PluginHandler) deleteExternal(c *gin.Context) {
	id := c.Param("id")
	p, ok := h.reg.Get(id)
	if !ok {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotFound, "插件不存在"))
		return
	}
	if _, isExt := p.(plugin.ExternalMarker); !isExt {
		middleware.AbortWithAppError(c, middleware.NewAppErrorWithCode(
			http.StatusBadRequest, CodePluginBuiltinProtected, "内置插件不允许删除"))
		return
	}
	// 先删 DB 再摘注册：DB 失败时保留注册项，避免"删除失败但卡片消失"的假象。
	if err := h.ext.Delete(id); err != nil {
		abortPluginErr(c, err)
		return
	}
	h.reg.Unregister(id)
	c.JSON(http.StatusOK, dto.OK(gin.H{"deleted": true}))
}

// abortPluginErr 透传 service 层的 AppError，其余包装为 500。
func abortPluginErr(c *gin.Context, err error) {
	var appErr *middleware.AppError
	if errors.As(err, &appErr) {
		middleware.AbortWithAppError(c, appErr)
		return
	}
	middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusInternalServerError, "操作失败", err))
}

// buildPluginInfo 汇总单个插件的 meta + enabled + status 为响应 DTO。
func buildPluginInfo(p plugin.Plugin) dto.PluginInfo {
	meta := p.Meta()
	st := p.Status()

	items := make([]dto.PluginStatusItem, 0, len(st.Items))
	for _, it := range st.Items {
		items = append(items, dto.PluginStatusItem{Label: it.Label, Value: it.Value, Tone: it.Tone})
	}

	info := dto.PluginInfo{
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
	if _, isExt := p.(plugin.ExternalMarker); isExt {
		info.External = true
		if hp, ok := p.(interface{ Homepage() string }); ok {
			info.Homepage = hp.Homepage()
		}
	}
	return info
}

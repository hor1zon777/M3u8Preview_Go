// plugin.go 插件中心相关 DTO。
//
// 对应端点（internal/handler/plugin.go）：
//
//	GET /api/v1/admin/plugins              插件列表（meta + enabled + 运行时状态）
//	PUT /api/v1/admin/plugins/:id/enabled  切换启用开关
package dto

// PluginStatusItem 插件卡片上的一行运行时指标。
// Tone："" 中性 / "ok" / "warn" / "error"，供前端着色。
type PluginStatusItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Tone  string `json:"tone,omitempty"`
}

// PluginInfo 插件列表项：静态元数据 + 启用状态 + 状态快照。
type PluginInfo struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Version     string             `json:"version"`
	Icon        string             `json:"icon"`
	Category    string             `json:"category"`
	Enabled     bool               `json:"enabled"`
	Healthy     bool               `json:"healthy"`
	Status      []PluginStatusItem `json:"status"`
	// External 管理员导入的声明式外部插件（可删除）；内置插件为 false。
	External bool `json:"external"`
	// Homepage 外部插件的可选主页外链（仅展示）。
	Homepage string `json:"homepage,omitempty"`
}

// PluginSetEnabledRequest 切换启用开关请求体。
// 指针 + binding:required 区分「显式传 false」与「漏传字段」。
type PluginSetEnabledRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

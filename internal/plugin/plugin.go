// Package plugin 提供「插件中心」的最小抽象：把可选功能模块（如字幕 worker）
// 统一注册为 Plugin，让 admin 面板用同一套 API 查看运行状态、切换启用开关。
//
// 边界说明：Go 是编译型语言，这里不做动态加载（.so / 进程外插件）——
// "插件"是产品层概念，指编译期注册的可选模块。新增插件的接入步骤：
//  1. 实现 Plugin 接口（Meta / Enabled / SetEnabled / Status）
//  2. 在 app.Build 里 Register 到 Registry
//  3. 前端插件中心按 Meta.ID 映射详情管理页（无映射时仅展示卡片）
//
// 与业务模块的依赖方向：plugin → service（适配器包装现有 service），
// service 一律不得反向 import plugin，保持单向依赖。
package plugin

import (
	"fmt"
	"sync"
)

// StatusItem 插件卡片上的一行运行时指标（如「在线 Worker: 2 / 3」）。
// Tone 供前端着色："" 中性 / "ok" 绿 / "warn" 黄 / "error" 红。
type StatusItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Tone  string `json:"tone,omitempty"`
}

// Status 插件运行时状态快照，由各插件在被查询时实时汇总。
// 实现方应保证该方法轻量且不 panic：底层查询失败时降级为空 Items，
// 不要把一次监控读取变成 500。
type Status struct {
	Healthy bool         `json:"healthy"`
	Items   []StatusItem `json:"items"`
}

// Meta 插件静态元数据（编译期确定，不随运行时变化）。
type Meta struct {
	// ID 全局唯一，kebab-case（如 "subtitle-worker"），
	// 同时是 admin API 路径参数与前端详情页映射键。
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Version 插件自身的版本标识（协议版本或模块版本，与应用版本解耦）。
	Version string `json:"version"`
	// Icon 前端图标名提示（lucide 名称，小写）；前端可按 ID 覆盖映射。
	Icon string `json:"icon"`
	// Category 分类展示用（如「媒体处理」）。
	Category string `json:"category"`
}

// Plugin 可选功能模块的统一抽象。
//
// SetEnabled 语义：持久化启用状态并让其即刻生效（无需重启进程）；
// 实现方应复用模块自身既有的配置更新路径，避免出现第二份真相。
type Plugin interface {
	Meta() Meta
	Enabled() bool
	SetEnabled(enabled bool) error
	Status() Status
}

// ExternalMarker 标记"管理员导入的声明式外部插件"（见 external.go）。
// handler 借类型断言区分内置/外部：只有外部插件允许删除，
// 内置插件的删除保护放在 handler 而非 Registry——注册表不该知道谁是内置。
type ExternalMarker interface {
	IsExternal() bool
}

// Registry 线程安全的插件注册表。注册发生在启动期（app.Build），
// 查询发生在请求期，读多写少，用 RWMutex 足够。
type Registry struct {
	mu      sync.RWMutex
	ordered []Plugin          // 保持注册顺序，决定前端卡片展示顺序
	byID    map[string]Plugin // ID 索引
}

// NewRegistry 构造空注册表。
func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]Plugin)}
}

// Register 注册一个插件。ID 为空或重复时返回错误——
// 这类问题属于编码错误，调用方（app.Build）应把它当 fatal 处理。
func (r *Registry) Register(p Plugin) error {
	if p == nil {
		return fmt.Errorf("plugin: register nil plugin")
	}
	id := p.Meta().ID
	if id == "" {
		return fmt.Errorf("plugin: empty plugin id")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[id]; exists {
		return fmt.Errorf("plugin: duplicate plugin id %q", id)
	}
	r.byID[id] = p
	r.ordered = append(r.ordered, p)
	return nil
}

// List 按注册顺序返回全部插件（副本切片，调用方可安全遍历）。
func (r *Registry) List() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Plugin, len(r.ordered))
	copy(out, r.ordered)
	return out
}

// Get 按 ID 查找插件。
func (r *Registry) Get(id string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byID[id]
	return p, ok
}

// Unregister 按 ID 摘除插件（外部插件删除时用），返回是否存在。
// 注册表本身不区分内置/外部——调用方（handler）负责内置插件的删除保护。
func (r *Registry) Unregister(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[id]; !ok {
		return false
	}
	delete(r.byID, id)
	for i, p := range r.ordered {
		if p.Meta().ID == id {
			r.ordered = append(r.ordered[:i], r.ordered[i+1:]...)
			break
		}
	}
	return true
}

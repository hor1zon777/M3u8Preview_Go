// external.go 声明式外部插件的 Plugin 适配器。
//
// External 把一行 external_plugins 记录适配成 Plugin 接口，与内置插件共用
// 插件中心的列表/开关 API。它不执行任何导入的内容——Status 的唯一动态行为
// 是可选的 healthUrl 探测（由 service 层缓存与限流，永不阻塞列表接口）。
package plugin

import (
	"sync"
	"time"

	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// External 声明式外部插件。
type External struct {
	svc *service.ExternalPluginService

	mu  sync.RWMutex
	rec model.ExternalPlugin
}

// NewExternal 构造适配器。
func NewExternal(svc *service.ExternalPluginService, rec model.ExternalPlugin) *External {
	return &External{svc: svc, rec: rec}
}

// IsExternal 实现 ExternalMarker：只有外部插件允许被删除。
func (e *External) IsExternal() bool { return true }

// UpdateRecord 覆盖导入（升级）后就地替换记录，避免摘除重注册打乱展示顺序。
func (e *External) UpdateRecord(rec model.ExternalPlugin) {
	e.mu.Lock()
	e.rec = rec
	e.mu.Unlock()
}

// Homepage 可选主页外链（仅展示）；handler 借类型断言读取。
func (e *External) Homepage() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.rec.Homepage == nil {
		return ""
	}
	return *e.rec.Homepage
}

// Meta 实现 Plugin。
func (e *External) Meta() Meta {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return Meta{
		ID:          e.rec.ID,
		Name:        e.rec.Name,
		Description: e.rec.Description,
		Version:     e.rec.Version,
		Icon:        e.rec.Icon,
		Category:    e.rec.Category,
	}
}

// Enabled 实现 Plugin。
func (e *External) Enabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rec.Enabled
}

// SetEnabled 实现 Plugin：先落库再更新内存快照。
func (e *External) SetEnabled(enabled bool) error {
	e.mu.RLock()
	id := e.rec.ID
	e.mu.RUnlock()
	if err := e.svc.SetEnabled(id, enabled); err != nil {
		return err
	}
	e.mu.Lock()
	e.rec.Enabled = enabled
	e.mu.Unlock()
	return nil
}

// Status 实现 Plugin。
// 停用时跳过探测（不打扰外部服务）；未配置 healthUrl 时恒 Healthy。
func (e *External) Status() Status {
	e.mu.RLock()
	id := e.rec.ID
	enabled := e.rec.Enabled
	var healthURL string
	if e.rec.HealthURL != nil {
		healthURL = *e.rec.HealthURL
	}
	e.mu.RUnlock()

	if healthURL == "" {
		return Status{Healthy: true, Items: []StatusItem{{Label: "健康检查", Value: "未配置"}}}
	}
	if !enabled {
		return Status{Healthy: true, Items: []StatusItem{{Label: "健康检查", Value: "已停用，跳过"}}}
	}

	r := e.svc.Health(id, healthURL)
	tone := "ok"
	if !r.OK {
		tone = "error"
	}
	return Status{
		Healthy: r.OK,
		Items: []StatusItem{
			{Label: "健康检查", Value: r.Detail, Tone: tone},
			{Label: "上次检查", Value: r.CheckedAt.Format(time.TimeOnly)},
		},
	}
}

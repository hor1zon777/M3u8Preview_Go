// subtitle_worker.go 把字幕 worker 子系统适配为插件中心的第一个插件。
//
// 适配器不新造任何统计 / 配置逻辑，全部转调 SubtitleService 的现成方法：
//   - 启用开关 = system_settings 的 subtitle.enabled（走 UpdateSettings，
//     与 admin 配置弹窗完全同一条代码路径，写 DB + 同步 in-memory cfg）
//   - 状态摘要 = ListOnlineWorkers / QueueStatus / Alerts 的汇总
package plugin

import (
	"fmt"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// SubtitleWorkerPlugin 字幕 worker 插件适配器。
type SubtitleWorkerPlugin struct {
	svc *service.SubtitleService
}

// NewSubtitleWorkerPlugin 构造。svc 由 app.Build 注入（Build 中恒非 nil）。
func NewSubtitleWorkerPlugin(svc *service.SubtitleService) *SubtitleWorkerPlugin {
	return &SubtitleWorkerPlugin{svc: svc}
}

// Meta 静态元数据。Version 取 worker 协议版本（docs/worker-protocol.md）。
func (p *SubtitleWorkerPlugin) Meta() Meta {
	return Meta{
		ID:          "subtitle-worker",
		Name:        "字幕 Worker",
		Description: "远程 GPU worker 生成 AI 字幕：下载抽音 → ASR 语音识别 → LLM 翻译 → WebVTT。含 worker 节点、Token 与任务队列管理。",
		Version:     "v3",
		Icon:        "subtitles",
		Category:    "媒体处理",
	}
}

// Enabled 读当前生效配置（.env 基线 + system_settings 覆盖后的合并值）。
func (p *SubtitleWorkerPlugin) Enabled() bool {
	return p.svc.Enabled()
}

// SetEnabled 只 patch enabled 字段，其余字幕配置不动。
func (p *SubtitleWorkerPlugin) SetEnabled(enabled bool) error {
	_, err := p.svc.UpdateSettings(dto.SubtitleSettingsUpdateRequest{Enabled: &enabled})
	return err
}

// Status 汇总运行时状态。任一底层查询失败时跳过对应指标（降级展示），
// 不让监控读取失败拖垮整个插件列表接口。
func (p *SubtitleWorkerPlugin) Status() Status {
	items := make([]StatusItem, 0, 5)

	// 在线 worker 数（last_seen 在 staleThreshold 内视为在线）
	if workers, err := p.svc.ListOnlineWorkers(); err == nil {
		online := 0
		for _, w := range workers {
			if w.Online {
				online++
			}
		}
		tone := ""
		if online > 0 {
			tone = "ok"
		} else if len(workers) > 0 {
			tone = "warn"
		}
		items = append(items, StatusItem{
			Label: "在线 Worker",
			Value: fmt.Sprintf("%d / %d", online, len(workers)),
			Tone:  tone,
		})
	}

	// 任务队列概况
	if q, err := p.svc.QueueStatus(); err == nil {
		items = append(items,
			StatusItem{Label: "排队中", Value: fmt.Sprintf("%d", q.Pending)},
			StatusItem{Label: "处理中", Value: fmt.Sprintf("%d", q.Running)},
		)
		if q.Failed > 0 {
			items = append(items, StatusItem{Label: "失败", Value: fmt.Sprintf("%d", q.Failed), Tone: "warn"})
		}
	}

	// 告警：有任何告警即视为不健康（当前均为 warn 级，如「有任务等待但无 worker 在线」）
	alerts := p.svc.Alerts()
	if len(alerts) > 0 {
		items = append(items, StatusItem{Label: "告警", Value: fmt.Sprintf("%d 条", len(alerts)), Tone: "warn"})
	}

	return Status{
		Healthy: len(alerts) == 0,
		Items:   items,
	}
}

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import {
  Puzzle,
  RefreshCw,
  Subtitles,
  Loader2,
  ChevronRight,
  CircleCheck,
  TriangleAlert,
  CirclePause,
} from 'lucide-react';
import { pluginApi } from '../services/pluginApi.js';
import { Toggle } from '../components/ui/Toggle.js';
import type { PluginInfo, PluginStatusItem } from '@m3u8-preview/shared';

/**
 * 插件中心：列出后端注册的全部插件卡片。
 * 每张卡 = 元数据（图标/名称/版本/分类/描述）+ 启用开关 + 运行时状态摘要 + 进入管理入口。
 *
 * 插件的详情管理页由前端按 id 映射（PLUGIN_DETAIL_ROUTES）；
 * 后端新注册而前端尚无对应页面的插件只展示卡片（开关 + 状态可用）。
 */

/** 插件 id → 详情管理页路由。新插件有管理界面时在此登记。 */
const PLUGIN_DETAIL_ROUTES: Record<string, string> = {
  'subtitle-worker': '/admin/plugins/subtitle-worker',
};

/** 插件 id → 卡片图标；未登记时兜底 Puzzle。 */
const PLUGIN_ICONS: Record<string, React.ComponentType<{ className?: string }>> = {
  'subtitle-worker': Subtitles,
};

export function PluginCenterPage() {
  const queryClient = useQueryClient();

  const { data: plugins = [], isLoading, refetch, isFetching } = useQuery({
    queryKey: ['admin', 'plugins'],
    queryFn: () => pluginApi.list(),
    refetchInterval: 10_000,
  });

  return (
    <div className="px-4 sm:px-6 lg:px-8 py-6 max-w-[1400px] mx-auto">
      {/* 标题区 */}
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-3">
          <Puzzle className="w-7 h-7 text-emby-green" />
          <h1 className="text-2xl font-semibold text-white">插件中心</h1>
          <span className="text-sm text-emby-text-secondary">{plugins.length} 个插件</span>
        </div>
        <button
          onClick={() => refetch()}
          className="px-3 py-1.5 text-sm rounded-md bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated transition-colors flex items-center gap-2"
        >
          <RefreshCw className={`w-4 h-4 ${isFetching ? 'animate-spin' : ''}`} /> 刷新
        </button>
      </div>

      {isLoading ? (
        <div className="py-16 text-center text-emby-text-secondary text-sm">
          <Loader2 className="w-5 h-5 inline animate-spin mr-2" />
          加载中...
        </div>
      ) : plugins.length === 0 ? (
        <div className="py-16 text-center text-emby-text-secondary text-sm">暂无已注册的插件</div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {plugins.map((p) => (
            <PluginCard
              key={p.id}
              plugin={p}
              onChanged={(next) => {
                // 用返回的最新快照就地更新列表缓存，避免等下一轮轮询
                queryClient.setQueryData<PluginInfo[]>(['admin', 'plugins'], (old) =>
                  (old ?? []).map((it) => (it.id === next.id ? next : it)),
                );
                // 字幕插件的 enabled 与 /admin/subtitle/settings 是同一底层值，联动刷新
                queryClient.invalidateQueries({ queryKey: ['admin', 'subtitle'] });
              }}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function PluginCard({ plugin, onChanged }: { plugin: PluginInfo; onChanged: (next: PluginInfo) => void }) {
  const Icon = PLUGIN_ICONS[plugin.id] ?? Puzzle;
  const detailRoute = PLUGIN_DETAIL_ROUTES[plugin.id];

  const toggleMutation = useMutation({
    mutationFn: (enabled: boolean) => pluginApi.setEnabled(plugin.id, enabled),
    onSuccess: onChanged,
    onError: (err: unknown) => {
      const msg =
        (err as { response?: { data?: { error?: string } } })?.response?.data?.error
        ?? (err as { message?: string })?.message
        ?? '操作失败';
      alert(msg);
    },
  });

  function handleToggle() {
    if (toggleMutation.isPending) return;
    if (plugin.enabled && !confirm(`停用「${plugin.name}」？停用后该功能的端点将不可用，worker 不再领取任务。`)) {
      return;
    }
    toggleMutation.mutate(!plugin.enabled);
  }

  return (
    <div className="bg-emby-bg-card border border-emby-border rounded-lg p-4 flex flex-col gap-3">
      {/* 头：图标 + 名称/版本 + 开关 */}
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-lg bg-emby-bg-elevated border border-emby-border flex items-center justify-center flex-shrink-0">
          <Icon className="w-5 h-5 text-emby-green" />
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-white font-medium truncate" title={plugin.name}>{plugin.name}</span>
            <span className="px-1.5 py-0.5 text-[10px] rounded bg-emby-bg-elevated border border-emby-border text-emby-text-secondary font-mono">
              {plugin.version}
            </span>
          </div>
          <div className="text-[11px] text-emby-text-muted mt-0.5">{plugin.category}</div>
        </div>
        <Toggle
          checked={plugin.enabled}
          pending={toggleMutation.isPending}
          onToggle={handleToggle}
          label={plugin.enabled ? '停用插件' : '启用插件'}
        />
      </div>

      {/* 描述 */}
      <p className="text-xs text-emby-text-secondary leading-relaxed">{plugin.description}</p>

      {/* 运行状态 */}
      <div className="rounded-md bg-emby-bg-elevated/40 border border-emby-border px-3 py-2 space-y-1.5">
        <HealthLine plugin={plugin} />
        {plugin.status.map((it, idx) => (
          <StatusLine key={idx} item={it} />
        ))}
      </div>

      {/* 底部操作 */}
      <div className="mt-auto pt-1">
        {detailRoute ? (
          <Link
            to={detailRoute}
            className="w-full px-3 py-2 text-sm rounded-md bg-emby-bg-elevated border border-emby-border text-emby-text-primary hover:bg-emby-bg-input transition-colors flex items-center justify-center gap-1.5"
          >
            进入管理 <ChevronRight className="w-4 h-4" />
          </Link>
        ) : (
          <div className="text-center text-[11px] text-emby-text-muted py-2">该插件没有独立管理界面</div>
        )}
      </div>
    </div>
  );
}

/** 健康状态行：停用 &gt; 告警 &gt; 正常。 */
function HealthLine({ plugin }: { plugin: PluginInfo }) {
  if (!plugin.enabled) {
    return (
      <div className="flex items-center gap-1.5 text-xs text-emby-text-muted">
        <CirclePause className="w-3.5 h-3.5" /> 已停用
      </div>
    );
  }
  if (!plugin.healthy) {
    return (
      <div className="flex items-center gap-1.5 text-xs text-yellow-300">
        <TriangleAlert className="w-3.5 h-3.5" /> 有告警，请进入管理页查看
      </div>
    );
  }
  return (
    <div className="flex items-center gap-1.5 text-xs text-emby-green">
      <CircleCheck className="w-3.5 h-3.5" /> 运行正常
    </div>
  );
}

function StatusLine({ item }: { item: PluginStatusItem }) {
  const valueCls =
    item.tone === 'ok'
      ? 'text-emby-green'
      : item.tone === 'warn'
      ? 'text-yellow-300'
      : item.tone === 'error'
      ? 'text-red-400'
      : 'text-emby-text-primary';
  return (
    <div className="flex items-center justify-between text-xs">
      <span className="text-emby-text-muted">{item.label}</span>
      <span className={`tabular-nums ${valueCls}`}>{item.value}</span>
    </div>
  );
}

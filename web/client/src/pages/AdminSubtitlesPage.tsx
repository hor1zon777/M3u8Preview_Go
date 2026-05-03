import { useState, useMemo, useEffect } from 'react';
import { createPortal } from 'react-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Subtitles,
  RotateCcw,
  Trash2,
  AlertCircle,
  CheckCircle2,
  Loader2,
  Search,
  RefreshCw,
  XCircle,
  Settings as SettingsIcon,
  PauseCircle,
  Play,
  X,
  Eye,
  EyeOff,
} from 'lucide-react';
import { subtitleApi } from '../services/subtitleApi.js';
import { categoryApi } from '../services/categoryApi.js';
import type { SubtitleJob, SubtitleStatus, SubtitleSettings, SubtitleSettingsUpdate } from '@m3u8-preview/shared';
import { SubtitleWorkersPanel } from '../components/admin/SubtitleWorkersPanel.js';

const STATUS_FILTERS: Array<{ value: string; label: string }> = [
  { value: '', label: '全部' },
  { value: 'PENDING', label: '排队中' },
  { value: 'RUNNING', label: '处理中' },
  { value: 'DONE', label: '已完成' },
  { value: 'FAILED', label: '失败' },
  { value: 'DISABLED', label: '已禁用' },
];

const STAGE_LABELS: Record<string, string> = {
  queued: '排队',
  extracting: '抽取音频',
  asr: '语音识别',
  translate: '翻译',
  writing: '写入字幕',
  done: '完成',
};

/**
 * 独立的字幕管理面板。
 * 功能：
 *   - 顶部 5 张状态卡 + 当前配置（脱敏）
 *   - 列表分页 + 状态筛选 + 搜索
 *   - 单条：重试 / 删除 / 切换禁用
 *   - 批量：重试全部失败 / 重新生成全部
 *   - 失败任务的错误信息弹窗
 */
export function AdminSubtitlesPage() {
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [statusFilter, setStatusFilter] = useState('');
  const [categoryFilter, setCategoryFilter] = useState(''); // ''=全部, '_none'=未分类, 其它=具体 categoryId
  const [search, setSearch] = useState('');
  const [errorDetail, setErrorDetail] = useState<SubtitleJob | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  // 跨页保留的选中 mediaId 集合
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  const { data: queue, refetch: refetchQueue } = useQuery({
    queryKey: ['admin', 'subtitle', 'queue'],
    queryFn: () => subtitleApi.queueStatus(),
    refetchInterval: 5000,
  });

  const { data: settings } = useQuery({
    queryKey: ['admin', 'subtitle', 'settings'],
    queryFn: () => subtitleApi.settings(),
  });

  // 分类下拉数据：管理员看到全部分类
  const { data: categories = [] } = useQuery({
    queryKey: ['categories'],
    queryFn: () => categoryApi.getAll(),
    staleTime: 60_000,
  });

  const { data, isLoading, refetch } = useQuery({
    queryKey: ['admin', 'subtitle', 'jobs', page, pageSize, statusFilter, categoryFilter, search],
    queryFn: () =>
      subtitleApi.listJobs({
        page,
        limit: pageSize,
        status: statusFilter || undefined,
        categoryId: categoryFilter || undefined,
        search: search || undefined,
      }),
    refetchInterval: 5000,
  });

  const items = useMemo<SubtitleJob[]>(() => data?.items ?? [], [data]);

  // 当前页的 mediaId 列表（用于全选 / 反选当前页）
  const currentPageIds = useMemo(() => items.map((it) => it.mediaId), [items]);
  const allCurrentPageSelected =
    currentPageIds.length > 0 && currentPageIds.every((id) => selectedIds.has(id));
  const someCurrentPageSelected = currentPageIds.some((id) => selectedIds.has(id));

  function toggleSelect(mediaId: string) {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(mediaId)) next.delete(mediaId);
      else next.add(mediaId);
      return next;
    });
  }

  function toggleSelectCurrentPage() {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (allCurrentPageSelected) {
        // 全部已选 → 取消当前页
        currentPageIds.forEach((id) => next.delete(id));
      } else {
        // 未全选 → 加入当前页全部
        currentPageIds.forEach((id) => next.add(id));
      }
      return next;
    });
  }

  function clearSelection() {
    setSelectedIds(new Set());
  }

  // 切换筛选条件时不自动清空选择，方便「先选 A 分类几条 + 再选 B 分类几条」的场景。
  // 如需清空可点工具栏的清空按钮。

  // 卸载时清空，避免回到页面残留
  useEffect(() => {
    return () => clearSelection();
  }, []);

  const retryMutation = useMutation({
    mutationFn: (mediaId: string) => subtitleApi.retry(mediaId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'subtitle'] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (mediaId: string) => subtitleApi.deleteJob(mediaId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'subtitle'] });
    },
  });

  const setDisabledMutation = useMutation({
    mutationFn: ({ mediaId, disabled }: { mediaId: string; disabled: boolean }) =>
      subtitleApi.setDisabled(mediaId, disabled),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'subtitle'] });
    },
  });

  const batchMutation = useMutation({
    mutationFn: (req: { all?: boolean; onlyFailed?: boolean; categoryId?: string; mediaIds?: string[] }) =>
      subtitleApi.batchRegenerate(req),
    onSuccess: (resp, vars) => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'subtitle'] });
      // 用 mediaIds 维度入队成功后清空选择，避免重复操作
      if (vars.mediaIds && vars.mediaIds.length > 0) {
        clearSelection();
      }
      alert(`已入队 ${resp.enqueued} 条，跳过 ${resp.skipped} 条`);
    },
  });

  // 批量禁用 / 取消 / 删除：三者共用一个轻量 mutation，
  // 调用时通过 op 参数路由到对应 service 方法，便于在按钮上展示统一的"处理中"态。
  const batchOpMutation = useMutation({
    mutationFn: async ({ op, mediaIds, disabled }: {
      op: 'disable' | 'enable' | 'cancel' | 'delete';
      mediaIds: string[];
      disabled?: boolean;
    }) => {
      switch (op) {
        case 'disable':
          return { op, ...(await subtitleApi.batchSetDisabled(mediaIds, true)) };
        case 'enable':
          return { op, ...(await subtitleApi.batchSetDisabled(mediaIds, false)) };
        case 'cancel':
          return { op, ...(await subtitleApi.batchCancel(mediaIds)) };
        case 'delete':
          return { op, ...(await subtitleApi.batchDelete(mediaIds)) };
        default:
          throw new Error(`unknown op: ${op satisfies never}`);
      }
    },
    onSuccess: (resp) => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'subtitle'] });
      clearSelection();
      const label =
        resp.op === 'disable' ? '禁用'
          : resp.op === 'enable' ? '启用'
          : resp.op === 'cancel' ? '取消'
          : '删除';
      alert(`已${label} ${resp.affected} 条，跳过 ${resp.skipped} 条`);
    },
    onError: (err: unknown) => {
      const msg =
        (err as { response?: { data?: { error?: string } } })?.response?.data?.error
        ?? (err as { message?: string })?.message
        ?? '批量操作失败';
      alert(msg);
    },
  });

  function runBatchOp(op: 'disable' | 'enable' | 'cancel' | 'delete', confirmText: string) {
    const ids = Array.from(selectedIds);
    if (ids.length === 0) return;
    if (!confirm(confirmText)) return;
    batchOpMutation.mutate({ op, mediaIds: ids });
  }

  const totalPages = data?.totalPages ?? 1;

  return (
    <div className="px-4 sm:px-6 lg:px-8 py-6 max-w-[1400px] mx-auto">
      {/* 标题区 */}
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-3">
          <Subtitles className="w-7 h-7 text-emby-green" />
          <h1 className="text-2xl font-semibold text-white">字幕管理</h1>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => {
              refetch();
              refetchQueue();
            }}
            className="px-3 py-1.5 text-sm rounded-md bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated transition-colors flex items-center gap-2"
          >
            <RefreshCw className="w-4 h-4" /> 刷新
          </button>
          <button
            onClick={() => setShowSettings(true)}
            className="px-3 py-1.5 text-sm rounded-md bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated transition-colors flex items-center gap-2"
          >
            <SettingsIcon className="w-4 h-4" /> 配置
          </button>
        </div>
      </div>

      {/* 状态卡 */}
      <div className="grid grid-cols-2 md:grid-cols-5 gap-3 mb-6">
        <StatCard label="排队中" value={queue?.pending ?? 0} color="text-yellow-400" />
        <StatCard label="处理中" value={queue?.running ?? 0} color="text-blue-400" />
        <StatCard label="已完成" value={queue?.done ?? 0} color="text-emby-green" />
        <StatCard label="失败" value={queue?.failed ?? 0} color="text-red-400" />
        <StatCard label="已禁用" value={queue?.disabled ?? 0} color="text-emby-text-muted" />
      </div>

      {/* 远程 worker + token 管理 */}
      <SubtitleWorkersPanel />

      {/* 配置开关提示 */}
      {settings && !settings.enabled && (
        <div className="mb-6 px-4 py-3 rounded-md bg-yellow-900/30 border border-yellow-700/50 text-sm text-yellow-300 flex items-center gap-2">
          <AlertCircle className="w-4 h-4" />
          字幕功能未启用，点击右上角"配置"打开设置面板，开启"启用"开关并填写 whisper.cpp / 翻译 API 配置后保存即可生效。
        </div>
      )}

      {/* 工具栏 */}
      <div className="flex flex-wrap items-center gap-3 mb-4">
        <div className="relative flex-1 min-w-[200px]">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-emby-text-muted" />
          <input
            type="text"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setPage(1);
            }}
            placeholder="按 mediaId 或标题搜索…"
            className="w-full pl-10 pr-4 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green text-sm"
          />
        </div>
        <select
          value={categoryFilter}
          onChange={(e) => {
            setCategoryFilter(e.target.value);
            setPage(1);
          }}
          className="px-3 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm focus:outline-none focus:ring-2 focus:ring-emby-green min-w-[140px]"
          aria-label="按分类筛选"
        >
          <option value="">全部分类</option>
          <option value="_none">未分类</option>
          {categories.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        <select
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value);
            setPage(1);
          }}
          className="px-3 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm focus:outline-none focus:ring-2 focus:ring-emby-green"
        >
          {STATUS_FILTERS.map((f) => (
            <option key={f.value} value={f.value}>
              {f.label}
            </option>
          ))}
        </select>
        <button
          disabled={!settings?.enabled || batchMutation.isPending || selectedIds.size === 0}
          onClick={() => {
            const ids = Array.from(selectedIds);
            if (ids.length === 0) return;
            if (confirm(`对所选 ${ids.length} 条媒体重新生成字幕？`)) {
              batchMutation.mutate({ mediaIds: ids });
            }
          }}
          className="px-3 py-2 text-sm rounded-md bg-emby-green text-white hover:bg-emby-green-dark disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
          title={selectedIds.size === 0 ? '请先勾选要处理的媒体' : `已选 ${selectedIds.size} 条`}
        >
          <Play className="w-4 h-4" /> 重新生成所选 ({selectedIds.size})
        </button>
        {/* 批量禁用 / 取消 / 删除：仅在选中时启用，统一通过 batchOpMutation 路由 */}
        <button
          disabled={!settings?.enabled || batchOpMutation.isPending || selectedIds.size === 0}
          onClick={() => runBatchOp('disable', `禁用所选 ${selectedIds.size} 条字幕任务？禁用后 worker 将不再处理。`)}
          className="px-3 py-2 text-sm rounded-md bg-zinc-700 text-white hover:bg-zinc-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
          title={selectedIds.size === 0 ? '请先勾选要处理的媒体' : `已选 ${selectedIds.size} 条`}
        >
          <PauseCircle className="w-4 h-4" /> 禁用所选 ({selectedIds.size})
        </button>
        <button
          disabled={!settings?.enabled || batchOpMutation.isPending || selectedIds.size === 0}
          onClick={() => runBatchOp('cancel', `取消所选 ${selectedIds.size} 条字幕任务？排队 / 处理中 / 失败的会被标记为已禁用，已完成的不变。`)}
          className="px-3 py-2 text-sm rounded-md bg-orange-600/80 text-white hover:bg-orange-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
          title={selectedIds.size === 0 ? '请先勾选要处理的媒体' : `已选 ${selectedIds.size} 条`}
        >
          <XCircle className="w-4 h-4" /> 取消所选 ({selectedIds.size})
        </button>
        <button
          disabled={batchOpMutation.isPending || selectedIds.size === 0}
          onClick={() => runBatchOp('delete', `删除所选 ${selectedIds.size} 条字幕任务及其 VTT 文件？此操作不可撤销。`)}
          className="px-3 py-2 text-sm rounded-md bg-red-700/80 text-white hover:bg-red-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
          title={selectedIds.size === 0 ? '请先勾选要处理的媒体' : `已选 ${selectedIds.size} 条`}
        >
          <Trash2 className="w-4 h-4" /> 删除所选 ({selectedIds.size})
        </button>
        <button
          disabled={!settings?.enabled || batchMutation.isPending}
          onClick={() => {
            if (confirm('重试所有失败任务？')) batchMutation.mutate({ onlyFailed: true });
          }}
          className="px-3 py-2 text-sm rounded-md bg-yellow-600/80 text-white hover:bg-yellow-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
        >
          <RotateCcw className="w-4 h-4" /> 重试全部失败
        </button>
        <button
          disabled={!settings?.enabled || batchMutation.isPending}
          onClick={() => {
            const label = categoryFilter === ''
              ? '全部 ACTIVE 媒体'
              : categoryFilter === '_none'
              ? '所有未分类媒体'
              : `分类 "${categories.find((c) => c.id === categoryFilter)?.name ?? categoryFilter}"`;
            if (confirm(`对${label}重新生成字幕？这会重置已完成的任务。`)) {
              if (categoryFilter === '') batchMutation.mutate({ all: true });
              else batchMutation.mutate({ categoryId: categoryFilter });
            }
          }}
          className="px-3 py-2 text-sm rounded-md bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
        >
          <RefreshCw className="w-4 h-4" />
          {categoryFilter === '' ? '重新生成全部' : '按当前分类重新生成'}
        </button>
      </div>

      {/* 选择条 */}
      {selectedIds.size > 0 && (
        <div className="mb-3 px-4 py-2 rounded-md bg-emby-green/10 border border-emby-green/40 text-sm text-white flex items-center gap-3">
          <span className="text-emby-green font-medium">已选 {selectedIds.size} 条媒体</span>
          <span className="text-emby-text-secondary text-xs">跨页保留，点"重新生成所选"批量入队</span>
          <button
            onClick={clearSelection}
            className="ml-auto px-2 py-1 rounded text-xs bg-emby-bg-card border border-emby-border hover:bg-emby-bg-elevated text-emby-text-primary flex items-center gap-1"
          >
            <X className="w-3 h-3" /> 清空
          </button>
        </div>
      )}

      {/* 列表 */}
      <div className="bg-emby-bg-card border border-emby-border rounded-lg overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-emby-bg-elevated text-emby-text-secondary text-xs uppercase">
              <tr>
                <th className="text-left pl-4 pr-2 py-3 w-10">
                  <input
                    type="checkbox"
                    aria-label="全选当前页"
                    title="全选当前页"
                    checked={allCurrentPageSelected}
                    ref={(el) => {
                      // 部分选中显示 indeterminate（半选）
                      if (el) el.indeterminate = !allCurrentPageSelected && someCurrentPageSelected;
                    }}
                    onChange={toggleSelectCurrentPage}
                    disabled={items.length === 0}
                    className="w-4 h-4 cursor-pointer accent-emby-green"
                  />
                </th>
                <th className="text-left px-4 py-3">媒体</th>
                <th className="text-left px-4 py-3">分类</th>
                <th className="text-left px-4 py-3">状态</th>
                <th className="text-left px-4 py-3">阶段</th>
                <th className="text-left px-4 py-3">进度</th>
                <th className="text-left px-4 py-3">语言</th>
                <th className="text-left px-4 py-3">段落</th>
                <th className="text-left px-4 py-3">模型</th>
                <th className="text-left px-4 py-3">更新时间</th>
                <th className="text-right px-4 py-3">操作</th>
              </tr>
            </thead>
            <tbody>
              {isLoading ? (
                <tr>
                  <td colSpan={11} className="text-center py-12 text-emby-text-secondary">
                    <Loader2 className="w-5 h-5 inline animate-spin mr-2" />
                    加载中...
                  </td>
                </tr>
              ) : items.length === 0 ? (
                <tr>
                  <td colSpan={11} className="text-center py-12 text-emby-text-secondary">
                    暂无字幕任务
                  </td>
                </tr>
              ) : (
                items.map((job) => {
                  const checked = selectedIds.has(job.mediaId);
                  return (
                  <tr
                    key={job.id}
                    className={`border-t border-emby-border hover:bg-emby-bg-elevated/50 ${checked ? 'bg-emby-green/5' : ''}`}
                  >
                    <td className="pl-4 pr-2 py-3">
                      <input
                        type="checkbox"
                        aria-label={`选择 ${job.mediaTitle ?? job.mediaId}`}
                        checked={checked}
                        onChange={() => toggleSelect(job.mediaId)}
                        className="w-4 h-4 cursor-pointer accent-emby-green"
                      />
                    </td>
                    <td className="px-4 py-3">
                      <div className="text-white font-medium truncate max-w-[280px]" title={job.mediaTitle ?? job.mediaId}>
                        {job.mediaTitle || '(媒体已删除)'}
                      </div>
                      <div className="text-xs text-emby-text-muted font-mono truncate max-w-[280px]" title={job.mediaId}>
                        {job.mediaId}
                      </div>
                    </td>
                    <td className="px-4 py-3 text-xs text-emby-text-secondary truncate max-w-[140px]" title={job.categoryName ?? '未分类'}>
                      {job.categoryName ? (
                        <span className="px-2 py-0.5 rounded bg-emby-bg-elevated border border-emby-border">{job.categoryName}</span>
                      ) : (
                        <span className="text-emby-text-muted">未分类</span>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge status={job.status} />
                    </td>
                    <td className="px-4 py-3 text-emby-text-secondary">
                      {STAGE_LABELS[job.stage] ?? job.stage}
                    </td>
                    <td className="px-4 py-3">
                      <ProgressBar value={job.progress} status={job.status} />
                    </td>
                    <td className="px-4 py-3 text-emby-text-secondary text-xs">
                      {job.sourceLang} → {job.targetLang}
                    </td>
                    <td className="px-4 py-3 text-emby-text-secondary tabular-nums">{job.segmentCount}</td>
                    <td className="px-4 py-3 text-xs text-emby-text-muted">
                      {job.asrModel && <div className="truncate max-w-[160px]" title={job.asrModel}>ASR: {job.asrModel}</div>}
                      {job.mtModel && <div className="truncate max-w-[160px]" title={job.mtModel}>MT: {job.mtModel}</div>}
                    </td>
                    <td className="px-4 py-3 text-emby-text-secondary text-xs">
                      {new Date(job.updatedAt).toLocaleString('zh-CN')}
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-end gap-1">
                        {job.status === 'FAILED' && (
                          <button
                            onClick={() => setErrorDetail(job)}
                            title="查看错误"
                            className="p-1.5 rounded hover:bg-red-900/40 text-red-400 transition-colors"
                          >
                            <AlertCircle className="w-4 h-4" />
                          </button>
                        )}
                        <button
                          disabled={!settings?.enabled || job.status === 'RUNNING'}
                          onClick={() => retryMutation.mutate(job.mediaId)}
                          title="重新生成"
                          className="p-1.5 rounded hover:bg-emby-bg-elevated text-emby-text-secondary disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                        >
                          <RotateCcw className="w-4 h-4" />
                        </button>
                        <button
                          onClick={() =>
                            setDisabledMutation.mutate({
                              mediaId: job.mediaId,
                              disabled: job.status !== 'DISABLED',
                            })
                          }
                          title={job.status === 'DISABLED' ? '启用' : '禁用'}
                          className="p-1.5 rounded hover:bg-emby-bg-elevated text-emby-text-secondary transition-colors"
                        >
                          <PauseCircle className="w-4 h-4" />
                        </button>
                        <button
                          onClick={() => {
                            if (confirm('删除此字幕任务和已生成的 VTT 文件？')) {
                              deleteMutation.mutate(job.mediaId);
                            }
                          }}
                          title="删除"
                          className="p-1.5 rounded hover:bg-red-900/40 text-red-400 transition-colors"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </div>
                    </td>
                  </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>

        {/* 分页 */}
        <div className="flex items-center justify-between px-4 py-3 border-t border-emby-border bg-emby-bg-elevated/40 text-sm">
          <div className="flex items-center gap-2 text-emby-text-secondary">
            共 {data?.total ?? 0} 条
            <select
              value={pageSize}
              onChange={(e) => {
                setPageSize(Number(e.target.value));
                setPage(1);
              }}
              className="ml-2 px-2 py-1 bg-emby-bg-input border border-emby-border rounded text-xs text-white"
            >
              <option value={10}>10</option>
              <option value={20}>20</option>
              <option value={50}>50</option>
              <option value={100}>100</option>
              <option value={200}>200</option>
              <option value={500}>500</option>
              <option value={1000}>1000</option>
            </select>
            条/页
          </div>
          <div className="flex items-center gap-2">
            <button
              disabled={page <= 1}
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              className="px-3 py-1 rounded bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            >
              上一页
            </button>
            <span className="text-emby-text-secondary">
              第 {page} / {totalPages} 页
            </span>
            <button
              disabled={page >= totalPages}
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              className="px-3 py-1 rounded bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            >
              下一页
            </button>
          </div>
        </div>
      </div>

      {/* 错误详情弹窗 */}
      {errorDetail && (
        <Modal onClose={() => setErrorDetail(null)} title="字幕生成失败">
          <div className="space-y-3">
            <div className="text-sm">
              <div className="text-emby-text-secondary">媒体：</div>
              <div className="text-white">{errorDetail.mediaTitle || errorDetail.mediaId}</div>
            </div>
            <div className="text-sm">
              <div className="text-emby-text-secondary">错误信息：</div>
              <pre className="text-red-300 bg-black/30 p-3 rounded font-mono text-xs whitespace-pre-wrap break-words">
                {errorDetail.errorMsg || '(空)'}
              </pre>
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <button
                onClick={() => {
                  retryMutation.mutate(errorDetail.mediaId);
                  setErrorDetail(null);
                }}
                className="px-3 py-1.5 rounded bg-emby-green text-white hover:bg-emby-green-dark text-sm flex items-center gap-2"
              >
                <RotateCcw className="w-4 h-4" /> 重新生成
              </button>
              <button
                onClick={() => setErrorDetail(null)}
                className="px-3 py-1.5 rounded bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated text-sm"
              >
                关闭
              </button>
            </div>
          </div>
        </Modal>
      )}

      {/* 配置弹窗：可编辑表单 */}
      {showSettings && settings && (
        <SubtitleSettingsModal
          settings={settings}
          onClose={() => setShowSettings(false)}
          onSaved={(next) => {
            // 用新配置主动更新缓存，避免等下一次 5s 轮询
            queryClient.setQueryData(['admin', 'subtitle', 'settings'], next);
            queryClient.invalidateQueries({ queryKey: ['admin', 'subtitle'] });
          }}
        />
      )}
    </div>
  );
}

function StatCard({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div className="bg-emby-bg-card border border-emby-border rounded-lg px-4 py-3">
      <div className="text-xs text-emby-text-secondary">{label}</div>
      <div className={`text-2xl font-bold tabular-nums ${color}`}>{value}</div>
    </div>
  );
}

function StatusBadge({ status }: { status: SubtitleStatus }) {
  const map: Record<SubtitleStatus, { label: string; cls: string; icon: React.ReactNode }> = {
    PENDING: { label: '排队', cls: 'bg-yellow-900/40 text-yellow-300 border-yellow-700/40', icon: <Loader2 className="w-3 h-3" /> },
    RUNNING: { label: '处理中', cls: 'bg-blue-900/40 text-blue-300 border-blue-700/40', icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    DONE: { label: '已完成', cls: 'bg-emerald-900/40 text-emerald-300 border-emerald-700/40', icon: <CheckCircle2 className="w-3 h-3" /> },
    FAILED: { label: '失败', cls: 'bg-red-900/40 text-red-300 border-red-700/40', icon: <XCircle className="w-3 h-3" /> },
    DISABLED: { label: '已禁用', cls: 'bg-zinc-800 text-emby-text-muted border-emby-border', icon: <PauseCircle className="w-3 h-3" /> },
    MISSING: { label: '缺失', cls: 'bg-zinc-800 text-emby-text-muted border-emby-border', icon: <AlertCircle className="w-3 h-3" /> },
  };
  const cfg = map[status] ?? map.MISSING;
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 text-xs rounded border ${cfg.cls}`}>
      {cfg.icon}
      {cfg.label}
    </span>
  );
}

function ProgressBar({ value, status }: { value: number; status: SubtitleStatus }) {
  const color =
    status === 'FAILED'
      ? 'bg-red-500'
      : status === 'DONE'
      ? 'bg-emby-green'
      : status === 'DISABLED'
      ? 'bg-emby-text-muted'
      : 'bg-blue-500';
  return (
    <div className="w-32">
      <div className="h-2 rounded-full bg-emby-bg-elevated overflow-hidden">
        <div className={`h-full ${color} transition-all`} style={{ width: `${Math.max(2, Math.min(100, value))}%` }} />
      </div>
      <div className="text-[10px] text-emby-text-muted mt-0.5 tabular-nums">{value}%</div>
    </div>
  );
}

function Modal({ children, onClose, title }: { children: React.ReactNode; onClose: () => void; title: string }) {
  // 弹窗本身负责滚动：
  //   - 外层遮罩 fixed inset-0 + flex 居中，确保弹窗永远在视口内
  //   - 弹窗最大高度 calc(100vh-2rem)，flex 列布局
  //   - 标题栏 flex-shrink-0：始终钉在弹窗顶部，不随内容滚动
  //   - 内容区 flex-1 overflow-y-auto min-h-0：撑满剩余高度，超出时
  //     在弹窗内部出现滚动条（不污染浏览器主滚动条，也不再"漏到黑框上"）
  //
  // min-h-0 必不可少：flex 子元素默认 min-height: auto，
  // 不显式覆盖会让 overflow-y-auto 失效（容器被内容撑高 → 永不滚动）。
  //
  // 用 createPortal 挂到 body：避开父级的 positioning / transform / overflow context，
  // 防止弹窗在某些页面下被截断或定位偏移。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  return createPortal(
    <div
      className="fixed inset-0 z-50 bg-black/60 flex items-center justify-center p-4"
      onClick={onClose}
    >
      <div
        className="bg-emby-bg-dialog border border-emby-border rounded-lg shadow-xl w-full max-w-2xl max-h-[calc(100vh-2rem)] flex flex-col overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="px-5 py-3 border-b border-emby-border flex-shrink-0">
          <h3 className="text-white font-medium">{title}</h3>
        </div>
        <div className="px-5 py-4 overflow-y-auto flex-1 min-h-0">
          {children}
        </div>
      </div>
    </div>,
    document.body,
  );
}

function SubtitleSettingsModal({
  settings,
  onClose,
  onSaved,
}: {
  settings: SubtitleSettings;
  onClose: () => void;
  onSaved: (next: SubtitleSettings) => void;
}) {
  // 受控表单。初始值用服务端最新配置；翻译 API Key 字段保留服务端脱敏占位，
  // 用户没改时不会回传，避免覆盖真实值（service 端也对 "***" 做了忽略保护）。
  const [enabled, setEnabled] = useState(settings.enabled);
  const [whisperBin, setWhisperBin] = useState(settings.whisperBin);
  const [whisperModel, setWhisperModel] = useState(settings.whisperModel);
  const [whisperLanguage, setWhisperLanguage] = useState(settings.whisperLanguage);
  const [whisperThreadsRaw, setWhisperThreadsRaw] = useState(String(settings.whisperThreads ?? 0));
  const [translateBaseUrl, setTranslateBaseUrl] = useState(settings.translateBaseUrl);
  const [translateModel, setTranslateModel] = useState(settings.translateModel);
  const [translateApiKey, setTranslateApiKey] = useState(settings.translateApiKey);
  const [targetLang, setTargetLang] = useState(settings.targetLang);
  const [batchSizeRaw, setBatchSizeRaw] = useState(String(settings.batchSize ?? 8));
  const [showApiKey, setShowApiKey] = useState(false);
  const [apiKeyTouched, setApiKeyTouched] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const saveMutation = useMutation({
    mutationFn: (payload: SubtitleSettingsUpdate) => subtitleApi.updateSettings(payload),
    onSuccess: (next) => {
      onSaved(next);
      onClose();
    },
    onError: (err: unknown) => {
      // axios error 默认带 response.data.error 字段；做一次温和取值
      const e = err as { response?: { data?: { error?: string } }; message?: string };
      setError(e?.response?.data?.error ?? e?.message ?? '保存失败');
    },
  });

  function buildPatch(): SubtitleSettingsUpdate | null {
    const patch: SubtitleSettingsUpdate = {};

    // 仅在和服务端值不同的字段加入 patch，减少误覆盖
    if (enabled !== settings.enabled) patch.enabled = enabled;
    if (whisperBin !== settings.whisperBin) patch.whisperBin = whisperBin;
    if (whisperModel !== settings.whisperModel) patch.whisperModel = whisperModel;
    if (whisperLanguage !== settings.whisperLanguage) patch.whisperLanguage = whisperLanguage;
    if (translateBaseUrl !== settings.translateBaseUrl) patch.translateBaseUrl = translateBaseUrl;
    if (translateModel !== settings.translateModel) patch.translateModel = translateModel;
    if (targetLang !== settings.targetLang) patch.targetLang = targetLang;

    const threads = Number.parseInt(whisperThreadsRaw, 10);
    if (Number.isFinite(threads)) {
      if (threads < 0 || threads > 64) {
        setError('CPU 线程数应在 0-64 之间');
        return null;
      }
      if (threads !== settings.whisperThreads) patch.whisperThreads = threads;
    }
    const batch = Number.parseInt(batchSizeRaw, 10);
    if (Number.isFinite(batch)) {
      if (batch < 1 || batch > 50) {
        setError('批大小应在 1-50 之间');
        return null;
      }
      if (batch !== settings.batchSize) patch.batchSize = batch;
    }

    // 仅在用户主动改过 API Key 输入框时才发送（避免脱敏占位被回传）
    if (apiKeyTouched) {
      patch.translateApiKey = translateApiKey;
    }
    return patch;
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const patch = buildPatch();
    if (!patch) return;
    if (Object.keys(patch).length === 0) {
      onClose();
      return;
    }
    saveMutation.mutate(patch);
  }

  return (
    <Modal onClose={onClose} title="字幕配置（网页编辑）">
      <form onSubmit={handleSubmit} className="space-y-4 text-sm">
        {/* 开关组 */}
        <div className="grid grid-cols-1 gap-3">
          <ToggleRow
            label="启用字幕生成"
            hint="关闭后所有字幕端点返回 503，worker 不消费任务。启用后字幕仅在管理员手动选中并点击「重新生成所选」时才会入队。"
            checked={enabled}
            onChange={setEnabled}
          />
        </div>

        {/* Whisper.cpp */}
        <fieldset className="border border-emby-border rounded-md p-3 space-y-3">
          <legend className="px-2 text-xs text-emby-text-secondary">whisper.cpp（本地 ASR）</legend>
          <FieldRow label="二进制路径" hint="留空恢复默认 whisper-cli">
            <input
              type="text"
              value={whisperBin}
              onChange={(e) => setWhisperBin(e.target.value)}
              placeholder="whisper-cli"
              className={inputCls}
            />
          </FieldRow>
          <FieldRow label="GGML 模型路径" hint="例如 /opt/whisper-models/ggml-medium-q5_0.bin">
            <input
              type="text"
              value={whisperModel}
              onChange={(e) => setWhisperModel(e.target.value)}
              placeholder="/path/to/ggml-medium-q5_0.bin"
              className={inputCls}
            />
          </FieldRow>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <FieldRow label="源语言" hint="ISO-639-1，例如 ja / en / zh">
              <input
                type="text"
                value={whisperLanguage}
                onChange={(e) => setWhisperLanguage(e.target.value)}
                placeholder="ja"
                className={inputCls}
                maxLength={16}
              />
            </FieldRow>
            <FieldRow label="CPU 线程" hint="0=自动按 NumCPU；最大 64">
              <input
                type="number"
                min={0}
                max={64}
                value={whisperThreadsRaw}
                onChange={(e) => setWhisperThreadsRaw(e.target.value)}
                className={inputCls}
              />
            </FieldRow>
          </div>
        </fieldset>

        {/* 翻译 */}
        <fieldset className="border border-emby-border rounded-md p-3 space-y-3">
          <legend className="px-2 text-xs text-emby-text-secondary">翻译（OpenAI 兼容 API）</legend>
          <FieldRow label="baseURL" hint="不含 /v1，如 https://api.deepseek.com">
            <input
              type="url"
              value={translateBaseUrl}
              onChange={(e) => setTranslateBaseUrl(e.target.value)}
              placeholder="https://api.deepseek.com"
              className={inputCls}
            />
          </FieldRow>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <FieldRow label="模型名" hint="例如 deepseek-chat / qwen2.5-7b-instruct / gpt-4o-mini">
              <input
                type="text"
                value={translateModel}
                onChange={(e) => setTranslateModel(e.target.value)}
                placeholder="deepseek-chat"
                className={inputCls}
              />
            </FieldRow>
            <FieldRow label="目标语言" hint="ISO-639-1，例如 zh / en">
              <input
                type="text"
                value={targetLang}
                onChange={(e) => setTargetLang(e.target.value)}
                placeholder="zh"
                className={inputCls}
                maxLength={16}
              />
            </FieldRow>
          </div>
          <FieldRow label="API Key" hint="留空时保留旧值；未修改的脱敏占位会被自动忽略">
            <div className="relative">
              <input
                type={showApiKey ? 'text' : 'password'}
                value={translateApiKey}
                onChange={(e) => {
                  setTranslateApiKey(e.target.value);
                  setApiKeyTouched(true);
                }}
                placeholder="sk-..."
                autoComplete="new-password"
                className={`${inputCls} pr-9`}
              />
              <button
                type="button"
                onClick={() => setShowApiKey((v) => !v)}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-emby-text-muted hover:text-emby-text-primary"
                title={showApiKey ? '隐藏' : '显示'}
              >
                {showApiKey ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
              </button>
            </div>
          </FieldRow>
          <FieldRow label="批大小" hint="一次请求翻译的字幕条数；1-50">
            <input
              type="number"
              min={1}
              max={50}
              value={batchSizeRaw}
              onChange={(e) => setBatchSizeRaw(e.target.value)}
              className={inputCls}
            />
          </FieldRow>
        </fieldset>

        {error && (
          <div className="px-3 py-2 rounded bg-red-900/30 border border-red-700/40 text-red-300 text-xs flex items-center gap-2">
            <XCircle className="w-4 h-4 flex-shrink-0" />
            {error}
          </div>
        )}

        <p className="text-xs text-emby-text-muted">
          配置即时生效，下一条字幕任务即应用新值；不会中断正在运行的任务。
          部署相关字段（如本地 worker 开关、心跳超时）仍由 .env 控制。
        </p>

        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1.5 rounded bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated text-sm"
          >
            取消
          </button>
          <button
            type="submit"
            disabled={saveMutation.isPending}
            className="px-3 py-1.5 rounded bg-emby-green text-white hover:bg-emby-green-dark disabled:opacity-40 disabled:cursor-not-allowed text-sm flex items-center gap-2"
          >
            {saveMutation.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <CheckCircle2 className="w-4 h-4" />}
            保存
          </button>
        </div>
      </form>
    </Modal>
  );
}

const inputCls =
  'w-full px-3 py-1.5 bg-emby-bg-input border border-emby-border rounded text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green text-sm';

function FieldRow({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <div className="text-emby-text-secondary text-xs mb-1">{label}</div>
      {children}
      {hint && <div className="text-[11px] text-emby-text-muted mt-1">{hint}</div>}
    </label>
  );
}

function ToggleRow({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="flex items-start gap-3 px-3 py-2 rounded-md bg-emby-bg-input border border-emby-border cursor-pointer">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5 w-4 h-4 accent-emby-green cursor-pointer"
      />
      <div className="flex-1">
        <div className="text-white text-sm">{label}</div>
        {hint && <div className="text-[11px] text-emby-text-muted mt-0.5">{hint}</div>}
      </div>
    </label>
  );
}

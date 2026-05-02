import { useState, useMemo, useEffect } from 'react';
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
} from 'lucide-react';
import { subtitleApi } from '../services/subtitleApi.js';
import { categoryApi } from '../services/categoryApi.js';
import type { SubtitleJob, SubtitleStatus } from '@m3u8-preview/shared';
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
          字幕功能未启用，请设置 <code className="px-1 rounded bg-black/40">SUBTITLE_ENABLED=true</code> 并提供 whisper.cpp 二进制 / 模型 / 翻译 API 配置后重启服务。
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

      {/* 配置弹窗 */}
      {showSettings && settings && (
        <Modal onClose={() => setShowSettings(false)} title="字幕配置（环境变量回显）">
          <div className="grid grid-cols-2 gap-3 text-sm">
            <SettingsRow label="启用" value={settings.enabled ? '是' : '否'} />
            <SettingsRow label="自动生成" value={settings.autoGenerate ? '是' : '否'} />
            <SettingsRow label="Whisper 二进制" value={settings.whisperBin || '(未设置)'} mono />
            <SettingsRow label="Whisper 模型" value={settings.whisperModel || '(未设置)'} mono />
            <SettingsRow label="源语言" value={settings.whisperLanguage} />
            <SettingsRow label="目标语言" value={settings.targetLang} />
            <SettingsRow label="CPU 线程" value={settings.whisperThreads === 0 ? '自动' : String(settings.whisperThreads)} />
            <SettingsRow label="批大小" value={String(settings.batchSize)} />
            <SettingsRow label="翻译 baseURL" value={settings.translateBaseUrl || '(未设置)'} mono />
            <SettingsRow label="翻译模型" value={settings.translateModel || '(未设置)'} mono />
            <SettingsRow label="翻译 API Key" value={settings.translateApiKey || '(未设置)'} mono />
          </div>
          <p className="text-xs text-emby-text-muted mt-4">
            修改配置需要在 <code className="px-1 rounded bg-black/40">.env</code> 中调整后重启服务。
          </p>
          <div className="flex justify-end pt-3">
            <button
              onClick={() => setShowSettings(false)}
              className="px-3 py-1.5 rounded bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated text-sm"
            >
              关闭
            </button>
          </div>
        </Modal>
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
  return (
    <div
      className="fixed inset-0 z-50 bg-black/60 flex items-center justify-center px-4"
      onClick={onClose}
    >
      <div
        className="bg-emby-bg-dialog border border-emby-border rounded-lg shadow-xl max-w-2xl w-full max-h-[80vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="px-5 py-3 border-b border-emby-border">
          <h3 className="text-white font-medium">{title}</h3>
        </div>
        <div className="px-5 py-4">{children}</div>
      </div>
    </div>
  );
}

function SettingsRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <>
      <div className="text-emby-text-secondary">{label}</div>
      <div className={`text-white break-all ${mono ? 'font-mono text-xs' : ''}`}>{value}</div>
    </>
  );
}

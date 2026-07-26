import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  ArrowUpCircle,
  CircleCheck,
  Download,
  Loader2,
  RefreshCw,
  ServerCog,
  TriangleAlert,
} from 'lucide-react';
import type { UpdateState, UpdateStatus } from '@m3u8-preview/shared';
import { updateApi } from '../../services/updateApi.js';
import { haApi } from '../../services/haApi.js';

/** 处于这些阶段时用 2s 快轮询跟踪进度。 */
const ACTIVE_STATES: UpdateState[] = ['downloading', 'verifying', 'staged', 'restarting'];

/**
 * 软件更新卡片（Dashboard）：当前版本 + 检查更新 + 一键更新。
 *
 * 更新流程全在容器内完成（下载 → 校验 → 暂存 → 进程自退重启装载），
 * 用户无需执行任何 docker 命令。重启窗口的探活复用 AdminHaPage 的模式：
 * status 请求失败且此前有更新进行 → 轮询无鉴权 /api/health，恢复后比对版本判定结果。
 */
export function UpdateCard() {
  const queryClient = useQueryClient();
  const [restarting, setRestarting] = useState(false);
  const [restartResult, setRestartResult] = useState<'ok' | 'rollback' | null>(null);
  const sawActiveUpdate = useRef(false);
  const targetVersion = useRef('');

  const { data: status, error } = useQuery({
    queryKey: ['update-status'],
    queryFn: () => updateApi.getStatus(),
    refetchInterval: (query) => {
      const state = query.state.data?.state;
      return state && ACTIVE_STATES.includes(state) ? 2000 : 60_000;
    },
    retry: false,
    refetchOnWindowFocus: true,
  });

  // HA 状态（与 Dashboard 引导卡/HA 页共享缓存）：完整 HA 时展示双端版本与滚动升级引导
  const { data: haStatus } = useQuery({
    queryKey: ['ha-status'],
    queryFn: () => haApi.getStatus(),
    staleTime: 60_000,
    retry: false,
  });

  const state = status?.state ?? 'idle';
  const inProgress = ACTIVE_STATES.includes(state);

  useEffect(() => {
    if (inProgress) {
      sawActiveUpdate.current = true;
      if (status?.staged?.version) targetVersion.current = status.staged.version;
      else if (status?.latest?.version) targetVersion.current = status.latest.version;
    }
  }, [inProgress, status]);

  // status 请求失败 + 之前有更新进行 → 节点在重启装载新版
  useEffect(() => {
    if (error && sawActiveUpdate.current && !restarting) {
      setRestarting(true);
    }
  }, [error, restarting]);

  // 重启窗口探活；恢复后比对版本判定更新成功还是被回滚
  useEffect(() => {
    if (!restarting) return;
    let stopped = false;
    const timer = setInterval(async () => {
      const health = await haApi.pollHealth();
      if (stopped || !health) return;
      clearInterval(timer);
      sawActiveUpdate.current = false;
      setRestarting(false);
      setRestartResult(
        (health as { version?: string }).version === targetVersion.current ? 'ok' : 'rollback',
      );
      queryClient.invalidateQueries({ queryKey: ['update-status'] });
      queryClient.invalidateQueries({ queryKey: ['app-version'] });
      queryClient.invalidateQueries({ queryKey: ['ha-status'] });
    }, 3000);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
  }, [restarting, queryClient]);

  const checkMutation = useMutation({
    mutationFn: () => updateApi.check(),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['update-status'] }),
  });

  const applyMutation = useMutation({
    mutationFn: (version: string) => updateApi.apply(version),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['update-status'] }),
    onError: (err: unknown) => {
      const msg =
        (err as { response?: { data?: { error?: string } } })?.response?.data?.error ??
        (err as { message?: string })?.message ??
        '更新请求失败';
      alert(msg);
    },
  });

  function handleApply() {
    const latest = status?.latest;
    if (!latest) return;
    const isHa = haStatus?.mode === 'full';
    const tip = isHa
      ? `更新到 v${latest.version}？\n\n本操作只更新当前访问的节点，完成后进程会自动重启（页面短暂不可用）。\nHA 建议顺序：先更新备节点 → 确认健康 → 主备切换 → 再更新原主节点。`
      : `更新到 v${latest.version}？\n\n下载校验完成后进程会自动重启装载新版本，页面短暂不可用（通常几十秒）。`;
    if (!confirm(tip)) return;
    setRestartResult(null);
    applyMutation.mutate(latest.version);
  }

  if (!status) return null;

  const hasUpdate = state === 'update-available' && !!status.latest;

  return (
    <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5">
      <div className="flex items-center gap-2 mb-4">
        <ArrowUpCircle className="w-5 h-5 text-emby-text-secondary" />
        <h3 className="text-white font-semibold">软件更新</h3>
        {hasUpdate && (
          <span className="px-1.5 py-0.5 text-[10px] rounded bg-emby-green/15 border border-emby-green/40 text-emby-green">
            有新版本
          </span>
        )}
      </div>

      {restarting ? (
        <div className="text-center py-4 space-y-2">
          <ServerCog className="w-8 h-8 text-emby-green mx-auto animate-pulse" />
          <p className="text-sm text-white">正在重启并装载新版本…</p>
          <p className="text-xs text-emby-text-muted flex items-center justify-center gap-1.5">
            <Loader2 className="w-3.5 h-3.5 animate-spin" /> 等待节点恢复，恢复后自动刷新
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {/* 版本行 + 检查按钮 */}
          <div className="flex items-center justify-between gap-3">
            <div className="text-sm text-emby-text-secondary">
              当前版本 <span className="text-white font-mono">v{status.currentVersion}</span>
              {haStatus?.mode === 'full' && haStatus.peer?.version && (
                <span className="ml-3">
                  对端节点 <span className="text-white font-mono">v{haStatus.peer.version}</span>
                </span>
              )}
            </div>
            <button
              onClick={() => checkMutation.mutate()}
              disabled={!status.enabled || checkMutation.isPending || inProgress}
              className="px-3 py-1.5 text-sm rounded-md bg-emby-bg-elevated border border-emby-border text-emby-text-primary hover:bg-emby-bg-input transition-colors flex items-center gap-2 disabled:opacity-50"
            >
              <RefreshCw className={`w-4 h-4 ${checkMutation.isPending || state === 'checking' ? 'animate-spin' : ''}`} />
              检查更新
            </button>
          </div>

          {/* 结果提示（重启探活后） */}
          {restartResult === 'ok' && (
            <p className="text-xs text-emby-green flex items-center gap-1.5">
              <CircleCheck className="w-3.5 h-3.5" /> 更新完成，当前已运行新版本。
            </p>
          )}
          {restartResult === 'rollback' && (
            <p className="text-xs text-yellow-300 flex items-start gap-1.5">
              <TriangleAlert className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
              更新未生效，可能已被自动回滚（新版本连续启动失败时会回退镜像版本），请查看容器日志。
            </p>
          )}

          {/* 禁用说明 */}
          {!status.enabled && (
            <p className="text-xs text-emby-text-muted">
              {status.disabledReason === 'dev-build'
                ? '开发构建（dev）不支持在线更新。'
                : '在线更新已被 UPDATE_DISABLED 关闭。'}
            </p>
          )}

          {/* 新版本 banner */}
          {hasUpdate && status.latest && !inProgress && (
            <div className="rounded-md border border-emby-green/30 bg-emby-green/5 p-3 space-y-2">
              <div className="flex items-center justify-between gap-3">
                <p className="text-sm text-white">
                  新版本 <span className="font-mono">v{status.latest.version}</span>
                  <span className="ml-2 text-xs text-emby-text-muted">
                    {formatSize(status.latest.assetSize)}
                    {status.latest.publishedAt && ` · ${new Date(status.latest.publishedAt).toLocaleDateString()}`}
                  </span>
                </p>
                <button
                  onClick={handleApply}
                  disabled={applyMutation.isPending}
                  className="px-3 py-1.5 text-sm font-medium rounded-md bg-emby-green text-white hover:bg-emby-green-dark transition-colors flex items-center gap-1.5 disabled:opacity-50"
                >
                  <Download className="w-4 h-4" /> 立即更新
                </button>
              </div>
              {status.latest.notes && (
                <pre className="text-xs text-emby-text-secondary whitespace-pre-wrap font-sans max-h-40 overflow-y-auto">
                  {truncate(status.latest.notes, 500)}
                </pre>
              )}
              {haStatus?.mode === 'full' && (
                <p className="text-[11px] text-emby-text-muted">
                  HA 滚动升级建议：先在备节点执行更新 → 确认健康 → 在高可用管理页切换主备 → 再更新原主节点。
                </p>
              )}
            </div>
          )}

          {/* 进行中 */}
          {state === 'downloading' && status.progress && (
            <div className="space-y-1.5">
              <div className="flex items-center justify-between text-xs text-emby-text-secondary">
                <span className="flex items-center gap-1.5">
                  <Loader2 className="w-3.5 h-3.5 animate-spin" /> 正在下载更新包…
                </span>
                <span className="font-mono">
                  {formatSize(status.progress.downloadedBytes)} / {formatSize(status.progress.totalBytes)}
                </span>
              </div>
              <div className="h-1.5 rounded-full bg-emby-bg-input overflow-hidden">
                <div
                  className="h-full bg-emby-green transition-all"
                  style={{
                    width: `${status.progress.totalBytes > 0
                      ? Math.min(100, (status.progress.downloadedBytes / status.progress.totalBytes) * 100)
                      : 0}%`,
                  }}
                />
              </div>
            </div>
          )}
          {state === 'verifying' && (
            <p className="text-xs text-emby-text-secondary flex items-center gap-1.5">
              <Loader2 className="w-3.5 h-3.5 animate-spin" /> 正在校验并解包更新（sha256）…
            </p>
          )}
          {(state === 'staged' || state === 'restarting') && (
            <p className="text-xs text-emby-text-secondary flex items-center gap-1.5">
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
              版本 v{status.staged?.version} 已暂存，进程即将重启装载…
            </p>
          )}

          {/* 失败 */}
          {(state === 'failed' || (status.error && state !== 'checking')) && status.error && (
            <p className="text-xs text-red-400 flex items-start gap-1.5">
              <TriangleAlert className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
              {status.error.message}
            </p>
          )}

          {/* 已是最新 */}
          {state === 'idle' && status.lastCheckedAt && status.enabled && (
            <p className="text-xs text-emby-text-muted">
              已是最新版本（检查于 {new Date(status.lastCheckedAt).toLocaleString()}）。
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function formatSize(bytes: number): string {
  if (!bytes || bytes <= 0) return '-';
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function truncate(s: string, n: number): string {
  return s.length > n ? s.slice(0, n) + '…' : s;
}

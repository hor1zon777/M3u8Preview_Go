import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { Loader2, Network, RefreshCw, ServerCog } from 'lucide-react';
import type { HaStatus, HaSwitchPhase } from '@m3u8-preview/shared';
import { haApi } from '../services/haApi.js';
import { adminApi } from '../services/adminApi.js';
import { HaStatusPanel, RoleBadge } from '../components/admin/ha/HaStatusPanel.js';
import { HaSwitchDialog } from '../components/admin/ha/HaSwitchDialog.js';
import { HaSwitchProgress } from '../components/admin/ha/HaSwitchProgress.js';
import { HaSetupWizard } from '../components/admin/ha/HaSetupWizard.js';

/** 处于这些阶段时用 2s 快轮询跟踪进度。 */
const ACTIVE_PHASES: HaSwitchPhase[] = ['requested', 'waiting-streams', 'draining', 'switching'];

/**
 * 高可用管理页。四种视图：
 *   - HA 未配置/不完整（mode !== full）→ 配置向导
 *   - 已配置且空闲 → 状态面板 + 手动切换
 *   - 交接进行中 → 进度视图
 *   - 节点重启窗口（status 请求失败且此前有切换在进行）→ 探活视图
 */
export function AdminHaPage() {
  const queryClient = useQueryClient();
  const [searchParams] = useSearchParams();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [switchError, setSwitchError] = useState('');
  const [restarting, setRestarting] = useState(false);
  // 记住是否观察到过进行中的切换：status 失败 + 该标记 = 节点在重启，而不是普通网络错误。
  const sawActiveSwitch = useRef(false);

  const { data: status, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['ha-status'],
    queryFn: () => haApi.getStatus(),
    refetchInterval: (query) => {
      const phase = query.state.data?.switch.phase;
      return phase && ACTIVE_PHASES.includes(phase) ? 2000 : 15000;
    },
    retry: false,
    refetchOnWindowFocus: true,
  });

  const phase = status?.switch.phase ?? 'idle';
  const inProgress = ACTIVE_PHASES.includes(phase);

  useEffect(() => {
    if (inProgress) sawActiveSwitch.current = true;
  }, [inProgress]);

  // status 请求失败 + 之前有切换在进行 → 进入"节点重启中"探活视图
  useEffect(() => {
    if (error && sawActiveSwitch.current && !restarting) {
      setRestarting(true);
    }
  }, [error, restarting]);

  // 重启窗口：轮询无鉴权 /api/health，恢复后刷新全部相关缓存
  useEffect(() => {
    if (!restarting) return;
    let stopped = false;
    const timer = setInterval(async () => {
      const health = await haApi.pollHealth();
      if (stopped || !health) return;
      clearInterval(timer);
      sawActiveSwitch.current = false;
      setRestarting(false);
      queryClient.invalidateQueries({ queryKey: ['ha-status'] });
      // SystemInfoCard / 用户菜单的角色徽标与版本信息也读 /api/health
      queryClient.invalidateQueries({ queryKey: ['app-version'] });
    }, 3000);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
  }, [restarting, queryClient]);

  const switchMutation = useMutation({
    mutationFn: (mode: 'graceful' | 'force') => haApi.requestSwitch(mode),
    onSuccess: () => {
      setDialogOpen(false);
      setSwitchError('');
      queryClient.invalidateQueries({ queryKey: ['ha-status'] });
    },
    onError: (err: unknown) => {
      setSwitchError(
        (err as { response?: { data?: { error?: string } } })?.response?.data?.error ??
          (err as { message?: string })?.message ??
          '切换请求失败',
      );
    },
  });

  const cancelMutation = useMutation({
    mutationFn: () => haApi.cancelSwitch(),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['ha-status'] }),
  });

  const autoFailbackMutation = useMutation({
    mutationFn: (next: boolean) => adminApi.updateSetting('haAutoFailback', next ? 'true' : 'false'),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['ha-status'] }),
  });

  return (
    <div className="px-4 sm:px-6 lg:px-8 py-6 max-w-[1100px] mx-auto space-y-6">
      {/* 标题区 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Network className="w-7 h-7 text-emby-green" />
          <h1 className="text-2xl font-semibold text-white">高可用管理</h1>
          {status && status.mode === 'full' && <RoleBadge role={status.local.role} />}
        </div>
        {status && status.mode === 'full' && (
          <button
            onClick={() => refetch()}
            className="px-3 py-1.5 text-sm rounded-md bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated transition-colors flex items-center gap-2"
          >
            <RefreshCw className={`w-4 h-4 ${isFetching ? 'animate-spin' : ''}`} /> 刷新
          </button>
        )}
      </div>

      {restarting ? (
        <RestartingView />
      ) : isLoading ? (
        <div className="py-16 text-center text-emby-text-secondary text-sm">
          <Loader2 className="w-5 h-5 inline animate-spin mr-2" />
          加载中...
        </div>
      ) : error ? (
        <div className="py-16 text-center text-emby-text-secondary text-sm">
          状态获取失败，
          <button onClick={() => refetch()} className="text-emby-green hover:underline mx-1">
            重试
          </button>
        </div>
      ) : !status ? null : status.mode !== 'full' ? (
        <HaSetupWizard mode={status.mode} initialRole={searchParams.get('role') ?? undefined} />
      ) : inProgress ? (
        <HaSwitchProgress
          status={status}
          onForceUpgrade={() => switchMutation.mutate('force')}
          onCancel={() => cancelMutation.mutate()}
          actionPending={switchMutation.isPending || cancelMutation.isPending}
        />
      ) : (
        <HaStatusPanel
          status={status}
          onSwitchClick={() => {
            setSwitchError('');
            setDialogOpen(true);
          }}
          onToggleAutoFailback={(next) => autoFailbackMutation.mutate(next)}
          autoFailbackPending={autoFailbackMutation.isPending}
        />
      )}

      {dialogOpen && status && (
        <HaSwitchDialog
          status={status}
          pending={switchMutation.isPending}
          error={switchError}
          onConfirm={(mode) => switchMutation.mutate(mode)}
          onClose={() => setDialogOpen(false)}
        />
      )}
    </div>
  );
}

function RestartingView() {
  return (
    <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-8 text-center space-y-3">
      <ServerCog className="w-10 h-10 text-emby-green mx-auto animate-pulse" />
      <h3 className="text-white font-semibold">节点正在重启以切换角色…</h3>
      <p className="text-sm text-emby-text-secondary max-w-md mx-auto">
        交接已完成，本节点进程正在以新角色重新挂载数据库（通常几十秒内恢复）。
        主域名 A 记录已指向新的主节点，浏览器 DNS 缓存约 1 分钟内生效。
      </p>
      <p className="text-xs text-emby-text-muted flex items-center justify-center gap-2">
        <Loader2 className="w-3.5 h-3.5 animate-spin" /> 正在等待节点恢复，恢复后自动刷新
      </p>
    </div>
  );
}

import { useEffect, useRef } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Image, Download, RefreshCw, Globe, HardDrive, ImageOff, Loader2, Database } from 'lucide-react';
import { adminApi } from '../../services/adminApi.js';

export function PosterSection() {
  const queryClient = useQueryClient();
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const { data: stats, isLoading: statsLoading } = useQuery({
    queryKey: ['admin', 'poster-stats'],
    queryFn: () => adminApi.getPosterStats(),
  });

  const { data: migrationStatus } = useQuery({
    queryKey: ['admin', 'poster-migration-status'],
    queryFn: () => adminApi.getPosterMigrationStatus(),
    refetchInterval: false,
  });

  const isRunning = migrationStatus?.running ?? false;

  // 迁移进行中时轮询状态
  useEffect(() => {
    if (isRunning) {
      pollRef.current = setInterval(() => {
        queryClient.invalidateQueries({ queryKey: ['admin', 'poster-migration-status'] });
      }, 2000);
    }
    return () => {
      if (pollRef.current) {
        clearInterval(pollRef.current);
        pollRef.current = null;
      }
    };
  }, [isRunning, queryClient]);

  // 迁移结束后刷新统计
  const prevRunning = useRef(false);
  useEffect(() => {
    if (prevRunning.current && !isRunning) {
      queryClient.invalidateQueries({ queryKey: ['admin', 'poster-stats'] });
    }
    prevRunning.current = isRunning;
  }, [isRunning, queryClient]);

  const migrateMutation = useMutation({
    mutationFn: () => adminApi.migratePosterImages(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'poster-migration-status'] });
    },
  });

  const retryMutation = useMutation({
    mutationFn: () => adminApi.retryFailedPosters(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'poster-migration-status'] });
    },
  });

  const formatSize = (bytes: number) => {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    return (bytes / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
  };

  const statItems = stats
    ? [
        { label: '总媒体', value: stats.total.toLocaleString(), icon: Image, color: 'text-blue-400' },
        { label: '外部封面', value: stats.external.toLocaleString(), icon: Globe, color: 'text-amber-400' },
        { label: '本地封面', value: stats.local.toLocaleString(), icon: HardDrive, color: 'text-green-400' },
        { label: '无封面', value: stats.missing.toLocaleString(), icon: ImageOff, color: 'text-red-400' },
        { label: '封面总体积', value: formatSize(stats.totalSizeBytes), icon: Database, color: 'text-purple-400' },
      ]
    : [];

  const hasExternal = (stats?.external ?? 0) > 0;
  const hasFailed = (migrationStatus?.failed ?? 0) > 0 && !isRunning;

  return (
    <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5">
      <div className="flex items-center gap-2 mb-4">
        <Image className="w-5 h-5 text-emby-text-secondary" />
        <h3 className="text-white font-semibold">封面管理</h3>
      </div>

      {/* 统计卡片 */}
      {statsLoading ? (
        <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="h-16 bg-emby-bg-input rounded animate-pulse" />
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
          {statItems.map((item) => (
            <div key={item.label} className="bg-emby-bg-input rounded p-3">
              <div className="flex items-center gap-1.5">
                <item.icon className={`w-4 h-4 ${item.color}`} />
                <span className="text-emby-text-muted text-xs">{item.label}</span>
              </div>
              <p className="text-white text-lg font-bold mt-1">{item.value}</p>
            </div>
          ))}
        </div>
      )}

      {/* 操作按钮 */}
      <div className="flex flex-wrap items-center gap-3 mt-4">
        <button
          onClick={() => migrateMutation.mutate()}
          disabled={migrateMutation.isPending || isRunning || !hasExternal}
          className="inline-flex items-center gap-2 px-4 py-2 bg-emby-green hover:bg-emby-green-light text-white text-sm font-medium rounded transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <Download className="w-4 h-4" />
          {isRunning ? '迁移中...' : '迁移外部封面到本地'}
        </button>

        {hasFailed && (
          <button
            onClick={() => retryMutation.mutate()}
            disabled={retryMutation.isPending || isRunning}
            className="inline-flex items-center gap-2 px-4 py-2 bg-amber-600 hover:bg-amber-500 text-white text-sm font-medium rounded transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <RefreshCw className="w-4 h-4" />
            重试失败项
          </button>
        )}

        {!hasExternal && !isRunning && (
          <span className="text-emby-text-muted text-xs">所有封面均已本地化</span>
        )}
      </div>

      {/* 迁移错误 */}
      {migrateMutation.isError && (
        <p className="text-red-400 text-xs mt-2">
          迁移启动失败: {(migrateMutation.error as Error)?.message || '未知错误'}
        </p>
      )}

      {/* 迁移进度 */}
      {migrationStatus && migrationStatus.total > 0 && (
        <div className="mt-4 p-3 bg-emby-bg-input rounded">
          <div className="flex items-center gap-2 mb-2">
            {isRunning && <Loader2 className="w-4 h-4 text-emby-green animate-spin" />}
            <span className="text-white text-sm font-medium">
              {isRunning ? '迁移进行中' : '迁移已完成'}
            </span>
          </div>

          {/* 进度条 */}
          {migrationStatus.total > 0 && (
            <div className="w-full bg-emby-bg-card rounded-full h-2 mb-3">
              <div
                className="bg-emby-green h-2 rounded-full transition-all duration-500"
                style={{
                  width: `${Math.round(((migrationStatus.completed + migrationStatus.failed) / migrationStatus.total) * 100)}%`,
                }}
              />
            </div>
          )}

          <div className="grid grid-cols-2 md:grid-cols-5 gap-2 text-xs text-emby-text-muted">
            <div>
              总计 <span className="text-white font-medium">{migrationStatus.total}</span>
            </div>
            <div>
              等待中 <span className="text-white font-medium">{migrationStatus.pending}</span>
            </div>
            <div>
              进行中 <span className="text-white font-medium">{migrationStatus.active}</span>
            </div>
            <div>
              成功 <span className="text-green-400 font-medium">{migrationStatus.completed}</span>
            </div>
            <div>
              失败 <span className={`font-medium ${migrationStatus.failed > 0 ? 'text-red-400' : 'text-white'}`}>
                {migrationStatus.failed}
              </span>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

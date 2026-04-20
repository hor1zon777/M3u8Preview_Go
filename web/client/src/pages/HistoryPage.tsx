import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Trash2 } from 'lucide-react';
import { historyApi } from '../services/historyApi.js';
import { MediaThumbnail } from '../components/media/MediaThumbnail.js';
import { formatDuration } from '../lib/utils.js';
import type { WatchHistory } from '@m3u8-preview/shared';

export function HistoryPage() {
  const [page, setPage] = useState(1);
  const queryClient = useQueryClient();

  const { data, isLoading } = useQuery({
    queryKey: ['history', page],
    queryFn: () => historyApi.getAll(page, 20),
  });

  const clearMutation = useMutation({
    mutationFn: () => historyApi.clearAll(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['history'] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => historyApi.deleteOne(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['history'] });
    },
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-white">观看历史</h1>
        <div className="flex items-center gap-3">
          {data && <span className="text-sm text-emby-text-secondary">共 {data.total} 条</span>}
          {data && data.total > 0 && (
            <button
              onClick={() => {
                if (confirm('确定要清空所有观看历史吗？')) {
                  clearMutation.mutate();
                }
              }}
              className="px-3 py-1.5 text-sm text-red-400 hover:bg-red-500/10 rounded-md transition-colors"
            >
              清空历史
            </button>
          )}
        </div>
      </div>

      {isLoading ? (
        <div className="space-y-4">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="animate-pulse flex gap-4">
              <div className="w-48 aspect-video bg-emby-bg-input rounded-md flex-shrink-0" />
              <div className="flex-1 space-y-2">
                <div className="h-5 bg-emby-bg-input rounded w-1/3" />
                <div className="h-4 bg-emby-bg-input rounded w-1/4" />
              </div>
            </div>
          ))}
        </div>
      ) : data && data.items.length > 0 ? (
        <div className="space-y-0">
          {data.items.map((item: WatchHistory) => (
            <div key={item.id} className="flex gap-4 p-3 border-b border-emby-border-subtle/50 hover:bg-white/5 transition-colors group">
              <Link to={`/media/${item.mediaId}`} className="w-48 flex-shrink-0">
                <div className="aspect-video bg-emby-bg-input rounded-md overflow-hidden relative">
                  <MediaThumbnail
                    mediaId={item.mediaId}
                    m3u8Url={item.media?.m3u8Url || ''}
                    posterUrl={item.media?.posterUrl}
                    title={item.media?.title}
                    watchedPercentage={item.percentage}
                  />
                  {/* Progress bar */}
                  <div className="absolute bottom-0 left-0 right-0 h-1 bg-emby-border">
                    <div
                      className="h-full bg-emby-green"
                      style={{ width: `${Math.min(100, item.percentage)}%` }}
                    />
                  </div>
                </div>
              </Link>
              <div className="flex-1 min-w-0">
                <Link to={`/media/${item.mediaId}`} className="text-white font-medium hover:text-emby-green-light transition-colors line-clamp-1">
                  {item.media?.title || '未知媒体'}
                </Link>
                <p className="text-sm text-emby-text-secondary mt-1">
                  {item.completed ? '已看完' : `观看至 ${formatDuration(item.progress)} / ${formatDuration(item.duration)}`}
                </p>
                <p className="text-xs text-emby-text-muted mt-1">
                  进度: {Math.round(item.percentage)}%
                </p>
              </div>
              <button
                onClick={() => deleteMutation.mutate(item.id)}
                className="opacity-0 group-hover:opacity-100 text-emby-text-muted hover:text-red-400 transition-all p-1 self-start"
                title="删除"
              >
                <Trash2 className="w-4 h-4" />
              </button>
            </div>
          ))}

          {/* Pagination */}
          {data.totalPages > 1 && (
            <div className="flex items-center justify-center gap-2 pt-4">
              <button
                onClick={() => setPage(Math.max(1, page - 1))}
                disabled={page === 1}
                className="px-4 py-2 bg-emby-bg-input text-white rounded-md disabled:opacity-50 hover:bg-emby-bg-elevated text-sm"
              >
                上一页
              </button>
              <span className="text-emby-text-secondary text-sm">{page} / {data.totalPages}</span>
              <button
                onClick={() => setPage(Math.min(data.totalPages, page + 1))}
                disabled={page === data.totalPages}
                className="px-4 py-2 bg-emby-bg-input text-white rounded-md disabled:opacity-50 hover:bg-emby-bg-elevated text-sm"
              >
                下一页
              </button>
            </div>
          )}
        </div>
      ) : (
        <div className="text-center py-12 text-emby-text-muted">暂无观看记录</div>
      )}
    </div>
  );
}

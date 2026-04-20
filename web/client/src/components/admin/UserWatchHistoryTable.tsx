import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { CheckCircle2 } from 'lucide-react';
import { adminApi } from '../../services/adminApi.js';

export function UserWatchHistoryTable({ userId }: { userId: string }) {
  const [page, setPage] = useState(1);

  const { data, isLoading } = useQuery({
    queryKey: ['admin', 'user-watch-history', userId, page],
    queryFn: () => adminApi.getUserWatchHistory(userId, page, 20),
  });

  return (
    <div className="space-y-4">
      <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-emby-border-subtle">
              <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">媒体标题</th>
              <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">观看进度</th>
              <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">已看完</th>
              <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">最后观看</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              Array.from({ length: 5 }).map((_, i) => (
                <tr key={i} className="border-b border-emby-border-subtle/50">
                  <td colSpan={4} className="px-4 py-3"><div className="h-5 bg-emby-bg-input rounded animate-pulse" /></td>
                </tr>
              ))
            ) : !data?.items?.length ? (
              <tr>
                <td colSpan={4} className="px-4 py-8 text-center text-emby-text-muted">暂无观看记录</td>
              </tr>
            ) : (
              data.items.map((item) => (
                <tr key={item.id} className="border-b border-emby-border-subtle/50 hover:bg-emby-bg-input/30">
                  <td className="px-4 py-3 text-white text-xs">
                    {item.media ? (
                      <Link to={`/media/${item.mediaId}`} className="text-emby-green hover:text-emby-green/80">
                        {item.media.title}
                      </Link>
                    ) : (
                      <span className="text-emby-text-muted">已删除</span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-2">
                      <div className="w-24 h-1.5 bg-emby-bg-input rounded-full overflow-hidden">
                        <div
                          className="h-full bg-emby-green rounded-full"
                          style={{ width: `${Math.min(item.percentage, 100)}%` }}
                        />
                      </div>
                      <span className="text-emby-text-muted text-xs">{Math.round(item.percentage)}%</span>
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    {item.completed && <CheckCircle2 className="w-4 h-4 text-green-400" />}
                  </td>
                  <td className="px-4 py-3 text-emby-text-muted text-xs">
                    {new Date(item.updatedAt).toLocaleString('zh-CN')}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {data && data.totalPages > 1 && (
        <div className="flex items-center justify-center gap-2">
          <button onClick={() => setPage(Math.max(1, page - 1))} disabled={page === 1} className="px-4 py-2 bg-emby-bg-input text-white rounded-lg disabled:opacity-50 hover:bg-emby-bg-elevated text-sm">上一页</button>
          <span className="text-emby-text-secondary text-sm">{page} / {data.totalPages}</span>
          <button onClick={() => setPage(Math.min(data.totalPages, page + 1))} disabled={page === data.totalPages} className="px-4 py-2 bg-emby-bg-input text-white rounded-lg disabled:opacity-50 hover:bg-emby-bg-elevated text-sm">下一页</button>
        </div>
      )}
    </div>
  );
}

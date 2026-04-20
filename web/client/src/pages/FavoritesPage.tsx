import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { favoriteApi } from '../services/favoriteApi.js';
import { MediaGrid } from '../components/media/MediaGrid.js';

export function FavoritesPage() {
  const [page, setPage] = useState(1);

  const { data, isLoading } = useQuery({
    queryKey: ['favorites', page],
    queryFn: () => favoriteApi.getAll(page, 24),
  });

  const mediaItems = data?.items?.map((f: any) => f.media).filter(Boolean) || [];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-white">我的收藏</h1>
        {data && <span className="text-sm text-emby-text-secondary">共 {data.total} 项</span>}
      </div>

      {isLoading ? (
        <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-x-4 gap-y-6">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="animate-pulse">
              <div className="aspect-[2/3] bg-emby-bg-input rounded-lg" />
              <div className="h-4 bg-emby-bg-input rounded mt-2 w-3/4" />
            </div>
          ))}
        </div>
      ) : (
        <>
          <MediaGrid items={mediaItems} emptyMessage="还没有收藏任何内容" />

          {data && data.totalPages > 1 && (
            <div className="flex items-center justify-center gap-2 pt-4">
              <button
                onClick={() => setPage(Math.max(1, page - 1))}
                disabled={page === 1}
                className="px-4 py-2 bg-emby-bg-input text-white rounded-lg disabled:opacity-50 hover:bg-emby-bg-elevated text-sm"
              >
                上一页
              </button>
              <span className="text-emby-text-secondary text-sm">{page} / {data.totalPages}</span>
              <button
                onClick={() => setPage(Math.min(data.totalPages, page + 1))}
                disabled={page === data.totalPages}
                className="px-4 py-2 bg-emby-bg-input text-white rounded-lg disabled:opacity-50 hover:bg-emby-bg-elevated text-sm"
              >
                下一页
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

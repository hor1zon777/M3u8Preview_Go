import { useRef, useEffect, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useInfiniteQuery } from '@tanstack/react-query';
import { ArrowLeft, User, Film } from 'lucide-react';
import { mediaApi } from '../services/mediaApi.js';
import { MediaGrid } from '../components/media/MediaGrid.js';
import { useProgressMap } from '../hooks/useProgressMap.js';

const PAGE_SIZE = 24;

export function ArtistDetailPage() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const artistName = name ? decodeURIComponent(name) : '';
  const sentinelRef = useRef<HTMLDivElement>(null);

  const {
    data,
    isLoading,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery({
    queryKey: ['media', 'artist', artistName],
    queryFn: ({ pageParam }) =>
      mediaApi.getAll({ artist: artistName, limit: PAGE_SIZE, page: pageParam }),
    initialPageParam: 1,
    getNextPageParam: (lastPage) =>
      lastPage.page < lastPage.totalPages ? lastPage.page + 1 : undefined,
    enabled: !!artistName,
  });

  const { data: progressMap } = useProgressMap();

  // 哨兵元素进入视口时自动加载下一页
  const handleIntersect = useCallback(() => {
    if (hasNextPage && !isFetchingNextPage) {
      fetchNextPage();
    }
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

  useEffect(() => {
    const el = sentinelRef.current;
    if (!el) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          handleIntersect();
        }
      },
      { rootMargin: '300px' },
    );

    observer.observe(el);
    return () => observer.disconnect();
  }, [handleIntersect]);

  const allItems = data?.pages.flatMap((page) => page.items) ?? [];
  const total = data?.pages[0]?.total ?? 0;

  if (isLoading) {
    return (
      <div className="animate-pulse space-y-6">
        <div className="h-8 bg-emby-bg-input rounded w-48" />
        <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-x-4 gap-y-6">
          {Array.from({ length: 12 }).map((_, i) => (
            <div key={i} className="aspect-[2/3] bg-emby-bg-input rounded-md" />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* 头部 */}
      <div className="flex items-center gap-4">
        <button
          onClick={() => navigate(-1)}
          className="p-2 rounded-lg hover:bg-emby-bg-elevated text-emby-text-secondary hover:text-white transition-colors"
          aria-label="返回"
        >
          <ArrowLeft className="w-5 h-5" />
        </button>

        <div className="w-14 h-14 rounded-full bg-emby-bg-elevated flex items-center justify-center flex-shrink-0">
          <User className="w-7 h-7 text-emby-text-secondary" />
        </div>

        <div>
          <h1 className="text-2xl font-bold text-white">{artistName}</h1>
          <p className="text-sm text-emby-text-secondary flex items-center gap-1">
            <Film className="w-3.5 h-3.5" />
            {total} 部作品
          </p>
        </div>
      </div>

      {/* 视频网格 */}
      <MediaGrid
        items={allItems}
        emptyMessage="该作者暂无关联视频"
        progressMap={progressMap}
      />

      {/* 哨兵元素：滚动到此处时自动加载下一页 */}
      <div ref={sentinelRef}>
        {isFetchingNextPage && (
          <div className="flex justify-center py-6">
            <div className="h-8 w-8 animate-spin rounded-full border-2 border-emby-primary border-t-transparent" />
          </div>
        )}
      </div>
    </div>
  );
}

import { useState, useRef, useEffect, useCallback } from 'react';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { mediaApi } from '../services/mediaApi.js';
import { categoryApi } from '../services/categoryApi.js';
import { MediaGrid } from '../components/media/MediaGrid.js';
import { useProgressMap } from '../hooks/useProgressMap.js';
import type { Category } from '@m3u8-preview/shared';

const PAGE_SIZE = 24;

export function LibraryPage() {
  const [searchParams] = useSearchParams();
  const [selectedCategory, setSelectedCategory] = useState<string>('');
  const [sortBy, setSortBy] = useState<string>('createdAt');
  const [sortOrder, setSortOrder] = useState<'asc' | 'desc'>('desc');
  const search = searchParams.get('search') || '';
  const sentinelRef = useRef<HTMLDivElement>(null);

  const { data: categories } = useQuery({
    queryKey: ['categories'],
    queryFn: () => categoryApi.getAll(),
  });

  const {
    data,
    isLoading,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery({
    queryKey: ['media', 'list', { search, categoryId: selectedCategory, sortBy, sortOrder }],
    queryFn: ({ pageParam }) => mediaApi.getAll({
      page: pageParam,
      limit: PAGE_SIZE,
      search: search || undefined,
      categoryId: selectedCategory || undefined,
      sortBy: sortBy as any,
      sortOrder,
    }),
    initialPageParam: 1,
    getNextPageParam: (lastPage) =>
      lastPage.page < lastPage.totalPages ? lastPage.page + 1 : undefined,
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

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-4">
        <h1 className="text-2xl font-bold text-white">
          {search ? `搜索: "${search}"` : '媒体库'}
        </h1>
        {data && <span className="text-sm text-emby-text-secondary">共 {total} 项</span>}
      </div>

      {/* Filters */}
      <div className="flex items-center gap-3 flex-wrap">
        {/* Category filter */}
        <select
          value={selectedCategory}
          onChange={(e) => setSelectedCategory(e.target.value)}
          className="px-3 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm focus:outline-none focus:ring-2 focus:ring-emby-green"
        >
          <option value="">全部分类</option>
          {categories?.map((cat: Category) => (
            <option key={cat.id} value={cat.id}>{cat.name} ({cat._count?.media || 0})</option>
          ))}
        </select>

        {/* Sort */}
        <select
          value={sortBy}
          onChange={(e) => setSortBy(e.target.value)}
          className="px-3 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm focus:outline-none focus:ring-2 focus:ring-emby-green"
        >
          <option value="createdAt">添加时间</option>
          <option value="title">标题</option>
          <option value="year">年份</option>
          <option value="rating">评分</option>
          <option value="views">播放量</option>
        </select>

        <button
          onClick={() => setSortOrder(sortOrder === 'desc' ? 'asc' : 'desc')}
          className="px-3 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm hover:bg-emby-bg-elevated"
        >
          {sortOrder === 'desc' ? '降序' : '升序'}
        </button>
      </div>

      {isLoading ? (
        <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-x-4 gap-y-6">
          {Array.from({ length: 12 }).map((_, i) => (
            <div key={i} className="animate-pulse">
              <div className="aspect-[2/3] bg-emby-bg-input rounded-lg" />
              <div className="h-4 bg-emby-bg-input rounded mt-2 w-3/4" />
            </div>
          ))}
        </div>
      ) : (
        <>
          <MediaGrid items={allItems} emptyMessage="没有找到匹配的媒体" progressMap={progressMap} />

          {/* 哨兵元素：滚动到此处时自动加载下一页 */}
          <div ref={sentinelRef}>
            {isFetchingNextPage && (
              <div className="flex justify-center py-6">
                <div className="h-8 w-8 animate-spin rounded-full border-2 border-emby-primary border-t-transparent" />
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}

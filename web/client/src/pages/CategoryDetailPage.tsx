import { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import { useQuery, useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Trash2 } from 'lucide-react';
import { categoryApi } from '../services/categoryApi.js';
import { mediaApi } from '../services/mediaApi.js';
import { MediaGrid } from '../components/media/MediaGrid.js';
import { useProgressMap } from '../hooks/useProgressMap.js';
import { useAuthStore } from '../stores/authStore.js';

const PAGE_SIZE = 24;

// ==================== 分类详情页 ====================

export function CategoryDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const sentinelRef = useRef<HTMLDivElement>(null);
  const queryClient = useQueryClient();
  const user = useAuthStore(s => s.user);
  const isAdmin = user?.role === 'ADMIN';

  // 排序与筛选状态
  const [sortBy, setSortBy] = useState<string>('createdAt');
  const [sortOrder, setSortOrder] = useState<'asc' | 'desc'>('desc');
  const [yearFilter, setYearFilter] = useState<string>('');

  // 查询分类详情
  const {
    data: category,
    isLoading: isCategoryLoading,
    isError: isCategoryError,
  } = useQuery({
    queryKey: ['category', id],
    queryFn: () => categoryApi.getById(id!),
    enabled: !!id,
  });

  // 管理员删除分类
  const deleteMutation = useMutation({
    mutationFn: () => categoryApi.delete(id!),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['categories'] });
      navigate('/categories');
    },
  });

  function handleDelete() {
    if (!category) return;
    const mediaCount = category._count?.media ?? 0;
    const msg = mediaCount > 0
      ? `分类"${category.name}"下有 ${mediaCount} 个媒体，删除后这些媒体将变为"未分类"。确定删除吗？`
      : `确定要删除分类"${category.name}"吗？`;
    if (confirm(msg)) {
      deleteMutation.mutate();
    }
  }

  const {
    data,
    isLoading,
    isError,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery({
    queryKey: ['media', 'category', id, sortBy, sortOrder],
    queryFn: ({ pageParam }) => mediaApi.getAll({
      page: pageParam,
      limit: PAGE_SIZE,
      categoryId: id,
      sortBy: sortBy as 'title' | 'createdAt' | 'year' | 'rating' | 'views',
      sortOrder,
    }),
    initialPageParam: 1,
    getNextPageParam: (lastPage) =>
      lastPage.page < lastPage.totalPages ? lastPage.page + 1 : undefined,
    enabled: !!id && !!category,
    gcTime: 5 * 60 * 1000,
    staleTime: 2 * 60 * 1000,
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

  // 去重 + 年份前端过滤
  const allItems = useMemo(() => {
    const items = data?.pages.flatMap((page) => page.items) ?? [];
    const seen = new Set<string>();
    const deduped = items.filter((item) => {
      if (seen.has(item.id)) return false;
      seen.add(item.id);
      return true;
    });

    // 年份筛选（后端无 year 过滤参数，前端实现）
    const yearNum = yearFilter ? parseInt(yearFilter, 10) : NaN;
    if (!isNaN(yearNum)) {
      return deduped.filter((item) => item.year === yearNum);
    }
    return deduped;
  }, [data, yearFilter]);

  const total = data?.pages[0]?.total ?? 0;

  // 加载中骨架屏
  if (isCategoryLoading) {
    return (
      <div className="space-y-6 animate-pulse">
        <div className="h-48 bg-emby-bg-input rounded-lg" />
        <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-x-4 gap-y-6">
          {Array.from({ length: 12 }).map((_, i) => (
            <div key={i}>
              <div className="aspect-[2/3] bg-emby-bg-input rounded-lg" />
              <div className="h-4 bg-emby-bg-input rounded mt-2 w-3/4" />
            </div>
          ))}
        </div>
      </div>
    );
  }

  // 加载失败或不存在
  if (isCategoryError || !category) {
    return (
      <div className="text-center py-16">
        <p className="text-emby-text-secondary mb-4">
          {isCategoryError ? '加载分类失败' : '分类不存在'}
        </p>
        <Link
          to="/categories"
          className="px-4 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark text-sm"
        >
          返回分类列表
        </Link>
      </div>
    );
  }

  const hasPoster = !!category.posterUrl;

  return (
    <div className="space-y-6">
      {/* Hero 头部 */}
      <div className="relative -mx-4 -mt-4 sm:-mx-6 sm:-mt-6 mb-6">
        {/* 背景图 */}
        {hasPoster && (
          <div className="absolute inset-0 overflow-hidden">
            <img
              src={category.posterUrl!}
              alt=""
              className="w-full h-full object-cover blur-xl scale-110"
            />
            <div className="absolute inset-0 bg-emby-bg-primary/80" />
          </div>
        )}

        <div className={`relative px-4 sm:px-6 ${hasPoster ? 'py-10' : 'py-6'}`}>
          {/* 返回按钮 + 管理员删除按钮 */}
          <div className="flex items-center justify-between mb-4">
            <Link
              to="/categories"
              className="flex items-center gap-1 text-emby-text-secondary hover:text-white transition-colors"
            >
              <ArrowLeft className="w-5 h-5" />
              <span className="text-sm">返回</span>
            </Link>
            {isAdmin && (
              <button
                onClick={handleDelete}
                disabled={deleteMutation.isPending}
                className="flex items-center gap-1.5 px-3 py-1.5 text-sm text-red-400 hover:text-red-300 bg-red-500/10 rounded-md hover:bg-red-500/20 transition-colors disabled:opacity-50"
              >
                <Trash2 className="w-4 h-4" />
                {deleteMutation.isPending ? '删除中...' : '删除分类'}
              </button>
            )}
          </div>

          {/* 分类信息 */}
          <div className="flex items-end gap-4">
            {hasPoster && (
              <img
                src={category.posterUrl!}
                alt={category.name}
                className="w-24 h-24 sm:w-32 sm:h-32 object-cover rounded-lg shadow-lg flex-shrink-0"
              />
            )}
            <div>
              <h1 className="text-2xl sm:text-3xl font-bold text-white">{category.name}</h1>
              {data && (
                <p className="text-sm text-emby-text-secondary mt-1">共 {total} 项媒体</p>
              )}
            </div>
          </div>
        </div>
      </div>

      {/* 排序与筛选控件 */}
      <div className="flex flex-wrap items-center gap-3">
        {/* sortBy 下拉 */}
        <select
          value={sortBy}
          onChange={(e) => setSortBy(e.target.value)}
          className="px-3 py-2 bg-emby-bg-input border border-emby-border-subtle rounded-lg text-sm text-white focus:outline-none focus:ring-1 focus:ring-emby-green"
        >
          <option value="createdAt">添加时间</option>
          <option value="title">标题</option>
          <option value="year">年份</option>
          <option value="rating">评分</option>
          <option value="views">播放量</option>
        </select>

        {/* sortOrder 切换 */}
        <button
          onClick={() => setSortOrder((prev) => (prev === 'desc' ? 'asc' : 'desc'))}
          className="px-3 py-2 bg-emby-bg-input border border-emby-border-subtle rounded-lg text-sm text-white hover:bg-emby-bg-elevated transition-colors"
        >
          {sortOrder === 'desc' ? '降序' : '升序'}
        </button>

        {/* 年份筛选 */}
        <input
          type="text"
          value={yearFilter}
          onChange={(e) => {
            // 只允许数字输入
            const val = e.target.value.replace(/\D/g, '');
            setYearFilter(val);
          }}
          placeholder="年份筛选"
          className="w-28 px-3 py-2 bg-emby-bg-input border border-emby-border-subtle rounded-lg text-sm text-white placeholder:text-emby-text-muted focus:outline-none focus:ring-1 focus:ring-emby-green"
        />
      </div>

      {/* 媒体列表 */}
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
          <MediaGrid items={allItems} emptyMessage="该分类暂无媒体" progressMap={progressMap} />

          {/* 加载失败重试 */}
          {isError && (
            <div className="flex flex-col items-center gap-3 py-8">
              <p className="text-red-400 text-sm">加载失败</p>
              <button
                onClick={() => fetchNextPage()}
                className="px-4 py-2 bg-emby-green text-white rounded-lg text-sm hover:opacity-90 transition-opacity"
              >
                重试
              </button>
            </div>
          )}

          {/* 哨兵元素：滚动到此处时自动加载下一页 */}
          <div ref={sentinelRef}>
            {isFetchingNextPage && (
              <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-x-4 gap-y-6">
                {Array.from({ length: 8 }).map((_, i) => (
                  <div key={i} className="animate-pulse">
                    <div className="aspect-[2/3] bg-emby-bg-input rounded-lg" />
                    <div className="h-4 bg-emby-bg-input rounded mt-2 w-3/4" />
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* 到底提示 */}
          {!hasNextPage && allItems.length > 0 && (
            <div className="text-center py-6 text-emby-text-muted text-sm">没有更多了</div>
          )}
        </>
      )}
    </div>
  );
}

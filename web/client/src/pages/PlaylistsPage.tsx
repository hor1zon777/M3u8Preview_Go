import { useState, useRef, useEffect, useCallback } from 'react';
import { Link } from 'react-router-dom';
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Search } from 'lucide-react';
import { playlistApi } from '../services/playlistApi.js';
import { useAuthStore } from '../stores/authStore.js';
import type { Playlist } from '@m3u8-preview/shared';

// 相对时间格式化
function formatRelativeTime(dateStr: string): string {
  const now = Date.now();
  const date = new Date(dateStr).getTime();
  const diff = now - date;
  const minutes = Math.floor(diff / 60000);
  const hours = Math.floor(diff / 3600000);
  const days = Math.floor(diff / 86400000);
  if (minutes < 1) return '刚刚';
  if (minutes < 60) return `${minutes}分钟前`;
  if (hours < 24) return `${hours}小时前`;
  if (days < 30) return `${days}天前`;
  if (days < 365) return `${Math.floor(days / 30)}个月前`;
  return `${Math.floor(days / 365)}年前`;
}

// 防抖 hook
function useDebouncedValue<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return debounced;
}

// 排序选项
const SORT_OPTIONS = [
  { label: '最近更新', value: 'updatedAt', order: 'desc' as const },
  { label: '名称', value: 'name', order: 'asc' as const },
  { label: '媒体数量', value: 'itemCount', order: 'desc' as const },
];

const PAGE_SIZE = 20;

export function PlaylistsPage() {
  const [showCreate, setShowCreate] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDesc, setNewDesc] = useState('');
  const [searchText, setSearchText] = useState('');
  const [sortIndex, setSortIndex] = useState(0);
  const queryClient = useQueryClient();
  const user = useAuthStore(s => s.user);
  const isAdmin = user?.role === 'ADMIN';
  const loadMoreRef = useRef<HTMLDivElement>(null);

  const debouncedSearch = useDebouncedValue(searchText, 300);
  const currentSort = SORT_OPTIONS[sortIndex];

  // 公开合集无限滚动查询
  const {
    data,
    isLoading,
    isFetchingNextPage,
    hasNextPage,
    fetchNextPage,
  } = useInfiniteQuery({
    queryKey: ['playlists-public', debouncedSearch, currentSort.value, currentSort.order],
    queryFn: ({ pageParam = 1 }) =>
      playlistApi.getPublicPlaylists({
        page: pageParam,
        limit: PAGE_SIZE,
        search: debouncedSearch || undefined,
        sortBy: currentSort.value,
        sortOrder: currentSort.order,
      }),
    getNextPageParam: (lastPage) =>
      lastPage.page < lastPage.totalPages ? lastPage.page + 1 : undefined,
    initialPageParam: 1,
  });

  // IntersectionObserver 自动加载更多
  const handleObserver = useCallback(
    (entries: IntersectionObserverEntry[]) => {
      const [entry] = entries;
      if (entry.isIntersecting && hasNextPage && !isFetchingNextPage) {
        fetchNextPage();
      }
    },
    [hasNextPage, isFetchingNextPage, fetchNextPage],
  );

  useEffect(() => {
    const el = loadMoreRef.current;
    if (!el) return;
    const observer = new IntersectionObserver(handleObserver, { threshold: 0.1 });
    observer.observe(el);
    return () => observer.disconnect();
  }, [handleObserver]);

  // 管理员创建合集
  const createMutation = useMutation({
    mutationFn: () => playlistApi.create(newName, newDesc || undefined),
    onSuccess: () => {
      // resetQueries 会清除 infinite query 的所有页面缓存并重新 fetch
      queryClient.resetQueries({ queryKey: ['playlists-public'] });
      setShowCreate(false);
      setNewName('');
      setNewDesc('');
    },
  });

  const allPlaylists = data?.pages.flatMap(p => p.items) ?? [];
  const totalCount = data?.pages[0]?.total ?? 0;

  return (
    <div className="space-y-6">
      {/* 顶栏: 标题 + 管理员创建按钮 */}
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-white">合集</h1>
        {isAdmin && (
          <button
            onClick={() => setShowCreate(!showCreate)}
            className="flex items-center gap-1.5 px-4 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark text-sm"
          >
            <Plus className="w-4 h-4" />
            新建合集
          </button>
        )}
      </div>

      {/* 管理员创建表单 */}
      {isAdmin && showCreate && (
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg p-4 space-y-3">
          {createMutation.error && (
            <div className="bg-red-500/10 border border-red-500/20 text-red-400 px-3 py-2 rounded-md text-sm">
              {(createMutation.error as any)?.response?.data?.error || '创建失败，请重试'}
            </div>
          )}
          <input
            type="text"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="合集名称"
            className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green"
            autoFocus
          />
          <input
            type="text"
            value={newDesc}
            onChange={(e) => setNewDesc(e.target.value)}
            placeholder="描述（可选）"
            className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green"
          />
          <div className="flex gap-2">
            <button
              onClick={() => createMutation.mutate()}
              disabled={!newName.trim() || createMutation.isPending}
              className="px-4 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark disabled:opacity-50 text-sm"
            >
              创建
            </button>
            <button
              onClick={() => setShowCreate(false)}
              className="px-4 py-2 bg-emby-bg-input text-emby-text-primary rounded-lg hover:bg-emby-bg-elevated text-sm"
            >
              取消
            </button>
          </div>
        </div>
      )}

      {/* 搜索和排序 */}
      <div className="flex flex-col sm:flex-row gap-3">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-emby-text-muted" />
          <input
            type="text"
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            placeholder="搜索合集..."
            className="w-full pl-9 pr-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green text-sm"
          />
        </div>
        <select
          value={sortIndex}
          onChange={(e) => setSortIndex(Number(e.target.value))}
          className="px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white text-sm focus:outline-none focus:ring-2 focus:ring-emby-green"
        >
          {SORT_OPTIONS.map((opt, i) => (
            <option key={opt.value} value={i}>{opt.label}</option>
          ))}
        </select>
      </div>

      {/* 合集网格 */}
      {isLoading ? (
        // 骨架屏
        <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 gap-4">
          {Array.from({ length: 10 }).map((_, i) => (
            <div key={i} className="animate-pulse">
              <div className="aspect-video bg-emby-bg-input rounded-t-lg" />
              <div className="bg-emby-bg-card rounded-b-lg p-3 space-y-2">
                <div className="h-4 bg-emby-bg-input rounded w-3/4" />
                <div className="h-3 bg-emby-bg-input rounded w-full" />
                <div className="h-3 bg-emby-bg-input rounded w-1/2" />
              </div>
            </div>
          ))}
        </div>
      ) : allPlaylists.length > 0 ? (
        <>
          <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 gap-4">
            {allPlaylists.map((playlist: Playlist) => (
              <Link
                key={playlist.id}
                to={`/playlists/${playlist.id}`}
                className="group bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-hidden hover:border-emby-border-light transition-colors"
              >
                {/* 封面图 */}
                <div className="aspect-video bg-emby-bg-input overflow-hidden">
                  {playlist.posterUrl ? (
                    <img
                      src={playlist.posterUrl}
                      alt={playlist.name}
                      className="w-full h-full object-cover group-hover:scale-105 transition-transform duration-300"
                    />
                  ) : (
                    <div className="w-full h-full bg-gradient-to-br from-emby-green/30 to-emby-green/5 flex items-center justify-center">
                      <span className="text-3xl font-bold text-emby-green/60">
                        {playlist.name.charAt(0).toUpperCase()}
                      </span>
                    </div>
                  )}
                </div>
                {/* 信息 */}
                <div className="p-3 space-y-1">
                  <h3 className="text-white font-medium text-sm truncate group-hover:text-emby-green-light transition-colors">
                    {playlist.name}
                  </h3>
                  {playlist.description && (
                    <p className="text-emby-text-muted text-xs line-clamp-2">
                      {playlist.description}
                    </p>
                  )}
                  <div className="flex items-center justify-between text-xs text-emby-text-secondary pt-1">
                    <span>{playlist._count?.items ?? 0} 个视频</span>
                    <span>{formatRelativeTime(playlist.updatedAt)}</span>
                  </div>
                </div>
              </Link>
            ))}
          </div>

          {/* 加载更多触发点 */}
          <div ref={loadMoreRef} className="py-4 text-center">
            {isFetchingNextPage ? (
              <div className="flex items-center justify-center gap-2 text-emby-text-muted text-sm">
                <div className="w-4 h-4 border-2 border-emby-green border-t-transparent rounded-full animate-spin" />
                加载中...
              </div>
            ) : hasNextPage ? (
              <span className="text-emby-text-muted text-sm">向下滚动加载更多</span>
            ) : totalCount > 0 ? (
              <span className="text-emby-text-muted text-sm">没有更多了</span>
            ) : null}
          </div>
        </>
      ) : debouncedSearch ? (
        // 搜索无结果
        <div className="text-center py-12 text-emby-text-muted">
          <p>没有找到匹配 "{debouncedSearch}" 的合集</p>
        </div>
      ) : (
        // 空态
        <div className="text-center py-12 text-emby-text-muted">
          <p>还没有公开合集</p>
          {isAdmin && <p className="text-sm mt-1">点击上方按钮创建一个</p>}
        </div>
      )}
    </div>
  );
}

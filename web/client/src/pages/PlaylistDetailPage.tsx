import { useState, useRef, useEffect, useCallback, useMemo, Fragment } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import { useQuery, useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Plus, Edit2, Search, Check, X, ChevronRight,
  MoreHorizontal, Trash2, Share2, ListVideo, Globe, Lock,
} from 'lucide-react';
import { playlistApi } from '../services/playlistApi.js';
import { mediaApi } from '../services/mediaApi.js';
import { MediaGrid } from '../components/media/MediaGrid.js';
import { useAuthStore } from '../stores/authStore.js';
import { useProgressMap } from '../hooks/useProgressMap.js';
import type { PlaylistItem, Media } from '@m3u8-preview/shared';

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

/** 相对时间格式化 */
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

// ---------------------------------------------------------------------------
// Toast 通知系统
// ---------------------------------------------------------------------------

interface Toast {
  id: number;
  type: 'success' | 'error';
  message: string;
}

let toastIdCounter = 0;

function useToast() {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const showToast = useCallback((type: 'success' | 'error', message: string) => {
    const id = ++toastIdCounter;
    setToasts((prev) => [...prev, { id, type, message }]);
    setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id));
    }, 3000);
  }, []);

  const removeToast = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  return { toasts, showToast, removeToast };
}

function ToastContainer({ toasts, onRemove }: { toasts: Toast[]; onRemove: (id: number) => void }) {
  return (
    <div className="fixed bottom-6 right-6 z-[100] flex flex-col gap-2">
      {toasts.map((t) => (
        <div
          key={t.id}
          className={`flex items-center gap-2 px-4 py-2.5 rounded-lg shadow-lg text-sm text-white transition-opacity duration-300 ${
            t.type === 'success' ? 'bg-green-600' : 'bg-red-600'
          }`}
        >
          <span className="flex-1">{t.message}</span>
          <button onClick={() => onRemove(t.id)} className="opacity-70 hover:opacity-100">
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// 确认弹窗组件
// ---------------------------------------------------------------------------

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  description: string;
  confirmText?: string;
  cancelText?: string;
  danger?: boolean;
  loading?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

function ConfirmDialog({
  open, title, description, confirmText = '确认', cancelText = '取消',
  danger = false, loading = false, onConfirm, onCancel,
}: ConfirmDialogProps) {
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* 遮罩 */}
      <div className="absolute inset-0 bg-black/60" onClick={onCancel} />
      {/* 弹窗内容 */}
      <div className="relative bg-emby-bg-card border border-emby-border-subtle rounded-xl p-6 max-w-sm w-full mx-4 shadow-2xl">
        <h3 className="text-white font-semibold text-lg">{title}</h3>
        <p className="text-emby-text-secondary text-sm mt-2">{description}</p>
        <div className="flex justify-end gap-2 mt-6">
          <button
            onClick={onCancel}
            className="px-4 py-2 text-sm rounded-lg bg-emby-bg-input text-emby-text-primary hover:bg-emby-bg-elevated transition-colors"
          >
            {cancelText}
          </button>
          <button
            onClick={onConfirm}
            disabled={loading}
            className={`px-4 py-2 text-sm rounded-lg text-white transition-colors disabled:opacity-50 ${
              danger
                ? 'bg-red-600 hover:bg-red-700'
                : 'bg-emby-green hover:bg-emby-green-dark'
            }`}
          >
            {loading ? '处理中...' : confirmText}
          </button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// 更多菜单
// ---------------------------------------------------------------------------

interface MoreMenuProps {
  onShare: () => void;
  onDelete: () => void;
}

function MoreMenu({ onShare, onDelete }: MoreMenuProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // 点击外部关闭
  useEffect(() => {
    if (!open) return;
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        className="p-2 rounded-lg bg-emby-bg-input text-emby-text-secondary hover:text-white hover:bg-emby-bg-elevated transition-colors"
        title="更多操作"
      >
        <MoreHorizontal className="w-4 h-4" />
      </button>
      {open && (
        <div className="absolute right-0 top-full mt-1 w-44 bg-emby-bg-card border border-emby-border-subtle rounded-lg shadow-xl overflow-hidden z-30">
          <button
            onClick={() => { onShare(); setOpen(false); }}
            className="flex items-center gap-2 w-full px-4 py-2.5 text-sm text-emby-text-primary hover:bg-emby-bg-elevated transition-colors"
          >
            <Share2 className="w-4 h-4" />
            分享 / 复制链接
          </button>
          <button
            onClick={() => { onDelete(); setOpen(false); }}
            className="flex items-center gap-2 w-full px-4 py-2.5 text-sm text-red-400 hover:bg-emby-bg-elevated transition-colors"
          >
            <Trash2 className="w-4 h-4" />
            删除合集
          </button>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// 排序选项
// ---------------------------------------------------------------------------

const SORT_OPTIONS = [
  { value: 'position', label: '添加顺序' },
  { value: 'title', label: '标题' },
  { value: 'year', label: '年份' },
  { value: 'createdAt', label: '创建时间' },
] as const;

type SortByValue = (typeof SORT_OPTIONS)[number]['value'];

// ---------------------------------------------------------------------------
// 主组件
// ---------------------------------------------------------------------------

export function PlaylistDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const user = useAuthStore((s) => s.user);
  const isAdmin = user?.role === 'ADMIN';

  // Toast
  const { toasts, showToast, removeToast } = useToast();

  // 确认弹窗
  const [confirmOpen, setConfirmOpen] = useState(false);

  // 编辑面板
  const [showEdit, setShowEdit] = useState(false);
  const [editName, setEditName] = useState('');
  const [editDesc, setEditDesc] = useState('');
  const [editPosterUrl, setEditPosterUrl] = useState('');

  // 添加媒体面板
  const [showAddPanel, setShowAddPanel] = useState(false);
  const [mediaSearchText, setMediaSearchText] = useState('');

  // 媒体墙搜索 & 排序
  const [wallSearch, setWallSearch] = useState('');
  const [sortBy, setSortBy] = useState<SortByValue>('position');
  const [sortOrder, setSortOrder] = useState<'asc' | 'desc'>('asc');

  // 无限滚动哨兵
  const sentinelRef = useRef<HTMLDivElement>(null);

  // 播放进度
  const { data: progressMap } = useProgressMap();

  // ---- 查询合集详情 ----
  const {
    data: playlist,
    isLoading,
    isError: isPlaylistError,
    refetch: refetchPlaylist,
  } = useQuery({
    queryKey: ['playlist', id],
    queryFn: () => playlistApi.getById(id!),
    enabled: !!id,
  });

  // ---- 无限滚动查询合集媒体 ----
  const ITEMS_PER_PAGE = 24;

  const {
    data: infiniteData,
    isLoading: isItemsLoading,
    isError: isItemsError,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    refetch: refetchItems,
  } = useInfiniteQuery({
    queryKey: ['playlist-items', id, wallSearch, sortBy, sortOrder],
    queryFn: ({ pageParam = 1 }) =>
      playlistApi.getPlaylistItems(id!, {
        page: pageParam,
        limit: ITEMS_PER_PAGE,
        search: wallSearch || undefined,
        sortBy,
        sortOrder,
      }),
    getNextPageParam: (lastPage) => {
      if (lastPage.page < lastPage.totalPages) return lastPage.page + 1;
      return undefined;
    },
    initialPageParam: 1,
    enabled: !!id,
  });

  // 合并分页数据，提取 media
  const allItems = useMemo(() => {
    if (!infiniteData) return [];
    return infiniteData.pages.flatMap((page) => page.items);
  }, [infiniteData]);

  const mediaList = useMemo(() => {
    return allItems
      .map((item) => item.media)
      .filter((m): m is Media => !!m);
  }, [allItems]);

  const totalItemCount = infiniteData?.pages[0]?.total ?? playlist?._count?.items ?? 0;

  // ---- IntersectionObserver 无限滚动 ----
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el) return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries[0]?.isIntersecting && hasNextPage && !isFetchingNextPage) {
          fetchNextPage();
        }
      },
      { rootMargin: '200px' },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

  // ---- 搜索媒体（添加面板用）----
  const { data: mediaResults, isLoading: isSearching } = useQuery({
    queryKey: ['media-search', mediaSearchText],
    queryFn: () => mediaApi.getAll({ search: mediaSearchText, limit: 10 }),
    enabled: showAddPanel && mediaSearchText.length > 0,
  });

  // 已在合集中的媒体ID
  const existingMediaIds = useMemo(() => {
    const ids = new Set<string>();
    allItems.forEach((item) => ids.add(item.mediaId));
    // 也包括从 playlist.items 中的（兜底）
    playlist?.items?.forEach((item: PlaylistItem) => ids.add(item.mediaId));
    return ids;
  }, [allItems, playlist?.items]);

  // ---- Mutations ----
  const addItemMutation = useMutation({
    mutationFn: (mediaId: string) => playlistApi.addItem(id!, mediaId),
    onSuccess: () => {
      showToast('success', '媒体已添加到合集');
      queryClient.invalidateQueries({ queryKey: ['playlist', id] });
      queryClient.invalidateQueries({ queryKey: ['playlist-items', id] });
    },
    onError: (error: any) => {
      if (error?.response?.status === 409) {
        showToast('error', '该媒体已在合集中');
      } else {
        showToast('error', '添加失败，请重试');
      }
    },
  });

  const removeMutation = useMutation({
    mutationFn: (mediaId: string) => playlistApi.removeItem(id!, mediaId),
    onSuccess: () => {
      showToast('success', '已从合集中移除');
      queryClient.invalidateQueries({ queryKey: ['playlist', id] });
      queryClient.invalidateQueries({ queryKey: ['playlist-items', id] });
    },
    onError: () => {
      showToast('error', '移除失败，请重试');
    },
  });

  const updateMutation = useMutation({
    mutationFn: (payload: { name?: string; description?: string; posterUrl?: string }) =>
      playlistApi.update(id!, payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['playlist', id] });
      queryClient.invalidateQueries({ queryKey: ['playlists-public'] });
      setShowEdit(false);
      showToast('success', '合集信息已更新');
    },
    onError: () => {
      showToast('error', '更新失败，请重试');
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => playlistApi.delete(id!),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['playlists-public'] });
      navigate('/playlists');
    },
    onError: () => {
      showToast('error', '删除失败，请重试');
    },
  });

  // ---- 操作回调 ----
  const openEdit = useCallback(() => {
    if (!playlist) return;
    setEditName(playlist.name);
    setEditDesc(playlist.description ?? '');
    setEditPosterUrl(playlist.posterUrl ?? '');
    setShowEdit(true);
  }, [playlist]);

  const handleSaveEdit = () => {
    updateMutation.mutate({
      name: editName,
      description: editDesc,
      posterUrl: editPosterUrl,
    });
  };

  const handleShare = useCallback(() => {
    navigator.clipboard.writeText(window.location.href).then(
      () => showToast('success', '链接已复制到剪贴板'),
      () => showToast('error', '复制失败，请手动复制'),
    );
  }, [showToast]);

  const handleDeleteConfirm = () => {
    setConfirmOpen(false);
    deleteMutation.mutate();
  };

  // ---------------------------------------------------------------------------
  // 渲染
  // ---------------------------------------------------------------------------

  // 加载骨架屏
  if (isLoading) {
    return (
      <div className="animate-pulse space-y-6 max-w-6xl mx-auto">
        <div className="flex items-center gap-2 h-5 w-48 bg-emby-bg-input rounded" />
        <div className="h-44 bg-emby-bg-input rounded-lg" />
        <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-4">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="aspect-[2/3] bg-emby-bg-input rounded-lg" />
          ))}
        </div>
      </div>
    );
  }

  // 加载失败
  if (isPlaylistError || !playlist) {
    return (
      <div className="text-center py-16">
        <ListVideo className="w-16 h-16 text-emby-text-muted mx-auto mb-4" />
        <p className="text-emby-text-secondary mb-4">
          {isPlaylistError ? '加载合集失败' : '合集不存在'}
        </p>
        <div className="flex items-center justify-center gap-3">
          {isPlaylistError && (
            <button
              onClick={() => refetchPlaylist()}
              className="px-4 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark text-sm"
            >
              重试
            </button>
          )}
          <Link
            to="/playlists"
            className="px-4 py-2 bg-emby-bg-input text-emby-text-primary rounded-lg hover:bg-emby-bg-elevated text-sm"
          >
            返回列表
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6 max-w-6xl mx-auto">
      {/* 面包屑导航 */}
      <nav className="flex items-center gap-1.5 text-sm text-emby-text-muted">
        <Link to="/" className="hover:text-white transition-colors">首页</Link>
        <ChevronRight className="w-3.5 h-3.5" />
        <Link to="/playlists" className="hover:text-white transition-colors">合集</Link>
        <ChevronRight className="w-3.5 h-3.5" />
        <span className="text-emby-text-primary truncate max-w-[200px]">{playlist.name}</span>
      </nav>

      {/* Hero 头部 */}
      <div className="relative rounded-lg overflow-hidden">
        {/* 背景 */}
        {playlist.posterUrl ? (
          <>
            <img
              src={playlist.posterUrl}
              alt=""
              className="absolute inset-0 w-full h-full object-cover blur-xl scale-110"
            />
            <div className="absolute inset-0 bg-black/60" />
          </>
        ) : (
          <div className="absolute inset-0 bg-gradient-to-br from-emby-green/20 to-emby-bg-card" />
        )}

        {/* 内容 */}
        <div className="relative z-10 p-5 sm:p-6 flex gap-5">
          {/* 封面图 */}
          <div className="w-28 h-28 sm:w-36 sm:h-36 rounded-lg overflow-hidden flex-shrink-0 bg-emby-bg-input">
            {playlist.posterUrl ? (
              <img
                src={playlist.posterUrl}
                alt={playlist.name}
                className="w-full h-full object-cover"
              />
            ) : (
              <div className="w-full h-full flex items-center justify-center">
                <ListVideo className="w-10 h-10 text-emby-text-muted" />
              </div>
            )}
          </div>

          {/* 信息区 */}
          <div className="flex-1 min-w-0 flex flex-col justify-center gap-1.5">
            <h1 className="text-2xl sm:text-3xl font-bold text-white truncate">{playlist.name}</h1>
            <div className="flex items-center gap-2 flex-wrap">
              {/* 公开/私密 badge */}
              {playlist.isPublic ? (
                <span className="inline-flex items-center gap-1 px-2 py-0.5 text-xs rounded-full bg-green-600/20 text-green-400 border border-green-600/30">
                  <Globe className="w-3 h-3" /> 公开
                </span>
              ) : (
                <span className="inline-flex items-center gap-1 px-2 py-0.5 text-xs rounded-full bg-yellow-600/20 text-yellow-400 border border-yellow-600/30">
                  <Lock className="w-3 h-3" /> 私密
                </span>
              )}
              <span className="text-sm text-emby-text-muted">
                {totalItemCount} 个媒体
              </span>
              <span className="text-emby-text-muted">·</span>
              <span className="text-sm text-emby-text-muted">
                更新于 {formatRelativeTime(playlist.updatedAt)}
              </span>
            </div>
            {playlist.description && (
              <p className="text-emby-text-secondary text-sm mt-1 line-clamp-2">{playlist.description}</p>
            )}
          </div>

          {/* 管理员操作按钮（右上） */}
          {isAdmin && (
            <div className="flex items-start gap-2 flex-shrink-0">
              <button
                onClick={() => {
                  setShowAddPanel((v) => !v);
                  setMediaSearchText('');
                }}
                className="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-emby-green text-white rounded-lg hover:bg-emby-green-dark transition-colors"
              >
                <Plus className="w-4 h-4" />
                <span className="hidden sm:inline">添加媒体</span>
              </button>
              <button
                onClick={openEdit}
                className="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-emby-bg-input text-emby-text-secondary hover:text-white rounded-lg hover:bg-emby-bg-elevated transition-colors"
              >
                <Edit2 className="w-4 h-4" />
                <span className="hidden sm:inline">编辑</span>
              </button>
              <MoreMenu
                onShare={handleShare}
                onDelete={() => setConfirmOpen(true)}
              />
            </div>
          )}
        </div>
      </div>

      {/* 编辑面板 */}
      {isAdmin && showEdit && (
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg p-4 space-y-3">
          <h3 className="text-white font-medium text-sm">编辑合集</h3>
          <input
            type="text"
            value={editName}
            onChange={(e) => setEditName(e.target.value)}
            placeholder="合集名称"
            className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green text-sm"
          />
          <textarea
            value={editDesc}
            onChange={(e) => setEditDesc(e.target.value)}
            placeholder="描述（可选）"
            rows={3}
            className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green text-sm resize-none"
          />
          <input
            type="text"
            value={editPosterUrl}
            onChange={(e) => setEditPosterUrl(e.target.value)}
            placeholder="封面图 URL（可选）"
            className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green text-sm"
          />
          {/* 封面 URL 实时预览 */}
          {editPosterUrl && (
            <div className="flex items-center gap-3">
              <span className="text-xs text-emby-text-muted">预览:</span>
              <img
                src={editPosterUrl}
                alt="封面预览"
                className="w-20 h-20 rounded object-cover bg-emby-bg-input"
                onError={(e) => { (e.target as HTMLImageElement).style.display = 'none'; }}
                onLoad={(e) => { (e.target as HTMLImageElement).style.display = 'block'; }}
              />
            </div>
          )}
          <div className="flex gap-2">
            <button
              onClick={handleSaveEdit}
              disabled={!editName.trim() || updateMutation.isPending}
              className="px-4 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark disabled:opacity-50 text-sm"
            >
              {updateMutation.isPending ? '保存中...' : '保存'}
            </button>
            <button
              onClick={() => setShowEdit(false)}
              className="px-4 py-2 bg-emby-bg-input text-emby-text-primary rounded-lg hover:bg-emby-bg-elevated text-sm"
            >
              取消
            </button>
          </div>
        </div>
      )}

      {/* 添加媒体面板 */}
      {isAdmin && showAddPanel && (
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg p-4 space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-white font-medium text-sm">添加媒体</h3>
            <button onClick={() => setShowAddPanel(false)} className="text-emby-text-muted hover:text-white">
              <X className="w-4 h-4" />
            </button>
          </div>
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-emby-text-muted" />
            <input
              type="text"
              value={mediaSearchText}
              onChange={(e) => setMediaSearchText(e.target.value)}
              placeholder="搜索媒体..."
              className="w-full pl-9 pr-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green text-sm"
              autoFocus
            />
          </div>

          {isSearching ? (
            <div className="py-4 text-center text-emby-text-muted text-sm">搜索中...</div>
          ) : mediaResults && mediaResults.items.length > 0 ? (
            <div className="space-y-1 max-h-80 overflow-y-auto">
              {mediaResults.items.map((media: Media) => {
                const alreadyAdded = existingMediaIds.has(media.id);
                return (
                  <div
                    key={media.id}
                    className="flex items-center gap-3 p-2 rounded-md hover:bg-emby-bg-elevated transition-colors"
                  >
                    <div className="w-16 flex-shrink-0">
                      <div className="aspect-video bg-emby-bg-input rounded overflow-hidden">
                        {media.posterUrl ? (
                          <img src={media.posterUrl} alt={media.title} className="w-full h-full object-cover" />
                        ) : (
                          <div className="w-full h-full flex items-center justify-center text-emby-text-muted text-xs">
                            无封面
                          </div>
                        )}
                      </div>
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="text-white text-sm truncate">{media.title}</p>
                      {media.category && (
                        <p className="text-emby-text-muted text-xs">{media.category.name}</p>
                      )}
                    </div>
                    {alreadyAdded ? (
                      <span className="flex items-center gap-1 text-emby-text-muted text-xs px-2">
                        <Check className="w-3 h-3" /> 已添加
                      </span>
                    ) : (
                      <button
                        onClick={() => addItemMutation.mutate(media.id)}
                        disabled={addItemMutation.isPending}
                        className="px-3 py-1 bg-emby-green text-white text-xs rounded hover:bg-emby-green-dark disabled:opacity-50"
                      >
                        添加
                      </button>
                    )}
                  </div>
                );
              })}
            </div>
          ) : mediaSearchText ? (
            <div className="py-4 text-center text-emby-text-muted text-sm">没有找到匹配的媒体</div>
          ) : (
            <div className="py-4 text-center text-emby-text-muted text-sm">输入关键词搜索媒体</div>
          )}
        </div>
      )}

      {/* 媒体墙区块 */}
      <section>
        {/* 区块标题 + 搜索 + 排序 */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-4">
          <h2 className="text-lg font-semibold text-white">
            媒体 ({totalItemCount})
          </h2>
          <div className="flex items-center gap-2">
            {/* 合集内搜索 */}
            <div className="relative">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-emby-text-muted" />
              <input
                type="text"
                value={wallSearch}
                onChange={(e) => setWallSearch(e.target.value)}
                placeholder="搜索..."
                className="pl-8 pr-3 py-1.5 w-40 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green text-sm"
              />
            </div>
            {/* 排序 */}
            <select
              value={`${sortBy}-${sortOrder}`}
              onChange={(e) => {
                const [by, order] = e.target.value.split('-') as [SortByValue, 'asc' | 'desc'];
                setSortBy(by);
                setSortOrder(order);
              }}
              className="px-2.5 py-1.5 bg-emby-bg-input border border-emby-border rounded-md text-white text-sm focus:outline-none focus:ring-2 focus:ring-emby-green"
            >
              {SORT_OPTIONS.map((opt) => (
                <Fragment key={opt.value}>
                  <option value={`${opt.value}-asc`}>{opt.label} (升序)</option>
                  <option value={`${opt.value}-desc`}>{opt.label} (降序)</option>
                </Fragment>
              ))}
            </select>
          </div>
        </div>

        {/* 内容区域 */}
        {isItemsLoading ? (
          // 骨架屏
          <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-x-4 gap-y-6">
            {Array.from({ length: 8 }).map((_, i) => (
              <div key={i} className="animate-pulse">
                <div className="aspect-[2/3] bg-emby-bg-input rounded-lg" />
                <div className="mt-2 h-3 w-3/4 bg-emby-bg-input rounded" />
              </div>
            ))}
          </div>
        ) : isItemsError ? (
          // 加载失败
          <div className="text-center py-12">
            <p className="text-emby-text-muted mb-3">加载媒体列表失败</p>
            <button
              onClick={() => refetchItems()}
              className="px-4 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark text-sm"
            >
              重试
            </button>
          </div>
        ) : mediaList.length === 0 ? (
          // 空态
          <div className="text-center py-16">
            <ListVideo className="w-16 h-16 text-emby-text-muted mx-auto mb-4" />
            <p className="text-emby-text-secondary mb-1">
              {wallSearch ? '没有找到匹配的媒体' : '合集暂无内容'}
            </p>
            {!wallSearch && isAdmin && (
              <button
                onClick={() => { setShowAddPanel(true); setMediaSearchText(''); }}
                className="mt-4 flex items-center gap-1.5 px-4 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark text-sm mx-auto"
              >
                <Plus className="w-4 h-4" />
                添加媒体
              </button>
            )}
            {!wallSearch && !isAdmin && (
              <p className="text-emby-text-muted text-sm mt-1">合集暂无内容</p>
            )}
          </div>
        ) : (
          <>
            <MediaGrid
              items={mediaList}
              progressMap={progressMap}
            />

            {/* 哨兵元素 - 无限滚动 */}
            <div ref={sentinelRef} className="h-1" />

            {/* 加载更多状态 */}
            {isFetchingNextPage && (
              <div className="flex justify-center py-6">
                <div className="flex items-center gap-2 text-emby-text-muted text-sm">
                  <div className="w-4 h-4 border-2 border-emby-text-muted border-t-transparent rounded-full animate-spin" />
                  加载更多...
                </div>
              </div>
            )}

            {/* 没有更多 */}
            {!hasNextPage && mediaList.length > 0 && (
              <p className="text-center text-emby-text-muted text-sm py-4">没有更多了</p>
            )}
          </>
        )}
      </section>

      {/* 确认删除弹窗 */}
      <ConfirmDialog
        open={confirmOpen}
        title="删除合集"
        description="确定要删除这个合集吗？此操作不可撤销，合集中的媒体不会被删除。"
        confirmText="删除"
        danger
        loading={deleteMutation.isPending}
        onConfirm={handleDeleteConfirm}
        onCancel={() => setConfirmOpen(false)}
      />

      {/* Toast 容器 */}
      <ToastContainer toasts={toasts} onRemove={removeToast} />
    </div>
  );
}

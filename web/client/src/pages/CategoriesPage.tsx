import { useState, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { FolderTree, Search, Plus } from 'lucide-react';
import { categoryApi } from '../services/categoryApi.js';
import { useAuthStore } from '../stores/authStore.js';
import type { Category, CategoryCreateRequest } from '@m3u8-preview/shared';

// ==================== 相对时间格式化 ====================

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

// ==================== Slug 生成 ====================

function generateSlug(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[\s_]+/g, '-')
    .replace(/[^\w\u4e00-\u9fff-]/g, '')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');
}

// ==================== 排序类型 ====================

type OverviewSortKey = 'name-asc' | 'name-desc' | 'count-desc' | 'count-asc' | 'updated';

// ==================== 主组件（分类概览） ====================

export function CategoriesPage() {
  const [searchQuery, setSearchQuery] = useState('');
  const [sortKey, setSortKey] = useState<OverviewSortKey>('name-asc');
  const [showCreate, setShowCreate] = useState(false);
  const [newName, setNewName] = useState('');
  const [newSlug, setNewSlug] = useState('');
  const [newPosterUrl, setNewPosterUrl] = useState('');
  const [autoSlug, setAutoSlug] = useState(true);
  const queryClient = useQueryClient();
  const user = useAuthStore(s => s.user);
  const isAdmin = user?.role === 'ADMIN';

  const { data: categories, isLoading } = useQuery({
    queryKey: ['categories'],
    queryFn: () => categoryApi.getAll(),
  });

  // 管理员创建分类
  const createMutation = useMutation({
    mutationFn: (payload: CategoryCreateRequest) => categoryApi.create(payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['categories'] });
      setShowCreate(false);
      setNewName('');
      setNewSlug('');
      setNewPosterUrl('');
      setAutoSlug(true);
    },
  });

  function handleNameChange(name: string) {
    setNewName(name);
    if (autoSlug) {
      setNewSlug(generateSlug(name));
    }
  }

  function handleCreateSubmit() {
    createMutation.mutate({
      name: newName.trim(),
      slug: newSlug.trim(),
      posterUrl: newPosterUrl.trim() || undefined,
    });
  }

  // 搜索 + 排序
  const filteredCategories = useMemo(() => {
    if (!categories) return [];

    const query = searchQuery.trim().toLowerCase();
    const filtered = query
      ? categories.filter((cat) => cat.name.toLowerCase().includes(query))
      : [...categories];

    switch (sortKey) {
      case 'name-asc':
        return filtered.sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));
      case 'name-desc':
        return filtered.sort((a, b) => b.name.localeCompare(a.name, 'zh-CN'));
      case 'count-desc':
        return filtered.sort((a, b) => (b._count?.media ?? 0) - (a._count?.media ?? 0));
      case 'count-asc':
        return filtered.sort((a, b) => (a._count?.media ?? 0) - (b._count?.media ?? 0));
      case 'updated':
        return filtered.sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime());
      default:
        return filtered;
    }
  }, [categories, searchQuery, sortKey]);

  const hasSearch = searchQuery.trim().length > 0;
  const isEmpty = filteredCategories.length === 0;

  return (
    <div className="space-y-6">
      {/* 标题 + 管理员创建按钮 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <FolderTree className="w-6 h-6 text-emby-green" />
          <h1 className="text-2xl font-bold text-white">分类</h1>
        </div>
        {isAdmin && (
          <button
            onClick={() => { setShowCreate(!showCreate); setNewName(''); setNewSlug(''); setNewPosterUrl(''); setAutoSlug(true); }}
            className="flex items-center gap-1.5 px-4 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark text-sm"
          >
            <Plus className="w-4 h-4" />
            新建分类
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
          <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
            <input
              value={newName}
              onChange={(e) => handleNameChange(e.target.value)}
              placeholder="分类名称 *"
              className="px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green"
              autoFocus
            />
            <div className="relative">
              <input
                value={newSlug}
                onChange={(e) => { setAutoSlug(false); setNewSlug(e.target.value); }}
                placeholder="Slug *（URL 标识）"
                className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green"
              />
              {autoSlug && newName && (
                <span className="absolute right-2 top-1/2 -translate-y-1/2 text-xs text-emby-text-muted">自动</span>
              )}
            </div>
            <input
              value={newPosterUrl}
              onChange={(e) => setNewPosterUrl(e.target.value)}
              placeholder="海报 URL（可选）"
              className="px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green"
            />
          </div>
          <div className="flex gap-2">
            <button
              onClick={handleCreateSubmit}
              disabled={!newName.trim() || !newSlug.trim() || createMutation.isPending}
              className="px-4 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark disabled:opacity-50 text-sm"
            >
              {createMutation.isPending ? '创建中...' : '创建'}
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

      {/* 搜索 + 排序控件 */}
      {!isLoading && categories && categories.length > 0 && (
        <div className="flex flex-wrap items-center gap-3">
          {/* 搜索框 */}
          <div className="relative flex-1 min-w-[200px] max-w-sm">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-emby-text-muted" />
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="搜索分类..."
              className="w-full pl-9 pr-3 py-2 bg-emby-bg-input border border-emby-border-subtle rounded-lg text-sm text-white placeholder:text-emby-text-muted focus:outline-none focus:ring-1 focus:ring-emby-green"
            />
          </div>

          {/* 排序下拉 */}
          <select
            value={sortKey}
            onChange={(e) => setSortKey(e.target.value as OverviewSortKey)}
            className="px-3 py-2 bg-emby-bg-input border border-emby-border-subtle rounded-lg text-sm text-white focus:outline-none focus:ring-1 focus:ring-emby-green"
          >
            <option value="name-asc">名称 A-Z</option>
            <option value="name-desc">名称 Z-A</option>
            <option value="count-desc">媒体数量 多-少</option>
            <option value="count-asc">媒体数量 少-多</option>
            <option value="updated">最近更新</option>
          </select>
        </div>
      )}

      {/* 分类卡片网格 */}
      {isLoading ? (
        <CategorySkeleton />
      ) : !categories || categories.length === 0 ? (
        <div className="text-center py-12 text-emby-text-muted">暂无分类</div>
      ) : isEmpty && hasSearch ? (
        <div className="text-center py-12 text-emby-text-muted">未找到匹配的分类</div>
      ) : (
        <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
          {filteredCategories.map((cat) => (
            <CategoryCard key={cat.id} category={cat} />
          ))}
        </div>
      )}
    </div>
  );
}

// ==================== 分类卡片 ====================

function CategoryCard({ category }: { category: Category }) {
  const mediaCount = category._count?.media ?? 0;

  return (
    <Link
      to={`/categories/${category.id}`}
      className="group text-left rounded-lg overflow-hidden bg-emby-bg-card border border-emby-border-subtle hover:scale-105 transition-transform focus:outline-none focus:ring-2 focus:ring-emby-green"
    >
      {/* 封面 */}
      <div className="aspect-video relative overflow-hidden rounded-t-lg">
        {category.posterUrl ? (
          <img
            src={category.posterUrl}
            alt={category.name}
            className="w-full h-full object-cover"
            loading="lazy"
          />
        ) : (
          <div className="w-full h-full bg-gradient-to-br from-emby-green/20 to-emby-bg-elevated flex items-center justify-center">
            <span className="text-3xl font-bold text-emby-text-secondary">
              {category.name.charAt(0)}
            </span>
          </div>
        )}
      </div>

      {/* 信息 */}
      <div className="p-3">
        <h3 className="text-sm font-medium text-white truncate">{category.name}</h3>
        <div className="flex items-center justify-between mt-1">
          <p className="text-xs text-emby-text-secondary">{mediaCount} 项</p>
          <p className="text-xs text-emby-text-muted">{formatRelativeTime(category.updatedAt)}</p>
        </div>
      </div>
    </Link>
  );
}

// ==================== 骨架屏 ====================

function CategorySkeleton() {
  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
      {Array.from({ length: 12 }).map((_, i) => (
        <div key={i} className="animate-pulse rounded-lg overflow-hidden">
          <div className="aspect-video bg-emby-bg-input rounded-t-lg" />
          <div className="p-3 space-y-2">
            <div className="h-4 bg-emby-bg-input rounded w-3/4" />
            <div className="h-3 bg-emby-bg-input rounded w-1/3" />
          </div>
        </div>
      ))}
    </div>
  );
}

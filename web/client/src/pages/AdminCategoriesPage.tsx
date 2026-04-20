import { useState, useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import {
  FolderTree,
  Plus,
  Pencil,
  Trash2,
  Search,
  Film,
  Calendar,
  Link as LinkIcon,
  X,
  Check,
  Image,
  FolderPlus,
  AlertTriangle,
} from 'lucide-react';
import { categoryApi } from '../services/categoryApi.js';
import type { Category, CategoryCreateRequest } from '@m3u8-preview/shared';

/** 将中文/英文名称转换为 URL 友好的 slug */
function generateSlug(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[\s_]+/g, '-')
    .replace(/[^\w\u4e00-\u9fff-]/g, '')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');
}

const emptyForm: CategoryCreateRequest = { name: '', slug: '', posterUrl: '' };

export function AdminCategoriesPage() {
  const [showForm, setShowForm] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [form, setForm] = useState<CategoryCreateRequest>({ ...emptyForm });
  const [autoSlug, setAutoSlug] = useState(true);
  const [search, setSearch] = useState('');
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null);
  const queryClient = useQueryClient();

  const { data: categories, isLoading } = useQuery({
    queryKey: ['categories'],
    queryFn: () => categoryApi.getAll(),
  });

  const createMutation = useMutation({
    mutationFn: (payload: CategoryCreateRequest) => categoryApi.create(payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['categories'] });
      resetForm();
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, payload }: { id: string; payload: Partial<CategoryCreateRequest> }) =>
      categoryApi.update(id, payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['categories'] });
      resetForm();
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => categoryApi.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['categories'] });
      setDeleteConfirmId(null);
    },
  });

  function resetForm() {
    setShowForm(false);
    setEditId(null);
    setForm({ ...emptyForm });
    setAutoSlug(true);
  }

  function startEdit(cat: Category) {
    setEditId(cat.id);
    setForm({ name: cat.name, slug: cat.slug, posterUrl: cat.posterUrl || '' });
    setAutoSlug(false);
    setShowForm(true);
  }

  function handleNameChange(name: string) {
    const updated = { ...form, name };
    if (autoSlug) {
      updated.slug = generateSlug(name);
    }
    setForm(updated);
  }

  function handleSlugChange(slug: string) {
    setAutoSlug(false);
    setForm({ ...form, slug });
  }

  function handleSubmit() {
    const payload: CategoryCreateRequest = {
      name: form.name.trim(),
      slug: form.slug.trim(),
      posterUrl: form.posterUrl?.trim() || undefined,
    };

    if (editId) {
      updateMutation.mutate({ id: editId, payload });
    } else {
      createMutation.mutate(payload);
    }
  }

  function handleDelete(id: string) {
    deleteMutation.mutate(id);
  }

  const mutationError = createMutation.error || updateMutation.error || deleteMutation.error;
  const isPending = createMutation.isPending || updateMutation.isPending;

  // 搜索过滤
  const filteredCategories = categories?.filter((cat: Category) =>
    !search || cat.name.toLowerCase().includes(search.toLowerCase()) || cat.slug.toLowerCase().includes(search.toLowerCase())
  );

  // 统计
  const totalMedia = categories?.reduce((sum: number, cat: Category) => sum + (cat._count?.media ?? 0), 0) ?? 0;

  return (
    <div className="space-y-6">
      {/* 页头 */}
      <div className="flex items-center justify-between flex-wrap gap-4">
        <div className="flex items-center gap-3">
          <div className="p-2 bg-emerald-500/10 rounded-lg">
            <FolderTree className="w-6 h-6 text-emerald-400" />
          </div>
          <div>
            <h1 className="text-2xl font-bold text-white">分类管理</h1>
            <p className="text-sm text-emby-text-muted mt-0.5">
              共 {categories?.length ?? 0} 个分类，{totalMedia} 个媒体
            </p>
          </div>
        </div>

        <div className="flex items-center gap-3">
          {/* 搜索框 */}
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-emby-text-muted" />
            <input
              type="text"
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder="搜索分类..."
              className="pl-10 pr-4 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:border-transparent w-56 transition-all"
            />
            {search && (
              <button
                onClick={() => setSearch('')}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-emby-text-muted hover:text-white transition-colors"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            )}
          </div>

          {/* 新建按钮 */}
          <button
            onClick={() => { setShowForm(!showForm); setEditId(null); setForm({ ...emptyForm }); setAutoSlug(true); }}
            className="inline-flex items-center gap-2 px-4 py-2.5 bg-emerald-500 text-white rounded-lg hover:bg-emerald-600 transition-colors text-sm font-medium"
          >
            <FolderPlus className="w-4 h-4" />
            新建分类
          </button>
        </div>
      </div>

      {/* 错误提示 */}
      {mutationError && (
        <div className="bg-red-500/10 border border-red-500/20 text-red-400 px-4 py-3 rounded-lg text-sm flex items-center gap-2">
          <AlertTriangle className="w-4 h-4 flex-shrink-0" />
          {(mutationError as any)?.response?.data?.error || '操作失败，请重试'}
        </div>
      )}

      {/* 创建/编辑表单 */}
      {showForm && (
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-hidden">
          <div className="px-5 py-4 border-b border-emby-border-subtle flex items-center justify-between">
            <h3 className="text-white font-semibold flex items-center gap-2">
              {editId ? (
                <><Pencil className="w-4 h-4 text-emerald-400" /> 编辑分类</>
              ) : (
                <><FolderPlus className="w-4 h-4 text-emerald-400" /> 新建分类</>
              )}
            </h3>
            <button
              onClick={resetForm}
              className="text-emby-text-muted hover:text-white transition-colors"
            >
              <X className="w-5 h-5" />
            </button>
          </div>

          <div className="p-5 space-y-4">
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              {/* 名称 */}
              <div className="space-y-1.5">
                <label className="text-xs font-medium text-emby-text-secondary flex items-center gap-1.5">
                  <span className="w-1 h-1 rounded-full bg-emerald-400" />
                  分类名称 <span className="text-red-400">*</span>
                </label>
                <input
                  value={form.name}
                  onChange={(e) => handleNameChange(e.target.value)}
                  placeholder="例如：动作片"
                  className="w-full px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:border-transparent transition-all"
                />
              </div>

              {/* Slug */}
              <div className="space-y-1.5">
                <label className="text-xs font-medium text-emby-text-secondary flex items-center gap-1.5">
                  <LinkIcon className="w-3 h-3" />
                  Slug <span className="text-red-400">*</span>
                  {autoSlug && form.name && (
                    <span className="text-emerald-400 text-[10px] font-normal">自动生成</span>
                  )}
                </label>
                <div className="relative">
                  <input
                    value={form.slug}
                    onChange={(e) => handleSlugChange(e.target.value)}
                    placeholder="url-friendly-slug"
                    className="w-full px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm font-mono placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:border-transparent transition-all"
                  />
                </div>
              </div>

              {/* 海报 URL */}
              <div className="space-y-1.5">
                <label className="text-xs font-medium text-emby-text-secondary flex items-center gap-1.5">
                  <Image className="w-3 h-3" />
                  海报 URL <span className="text-emby-text-muted font-normal">（可选）</span>
                </label>
                <input
                  value={form.posterUrl || ''}
                  onChange={(e) => setForm({ ...form, posterUrl: e.target.value })}
                  placeholder="https://example.com/poster.jpg"
                  className="w-full px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:border-transparent transition-all"
                />
              </div>
            </div>

            {/* 操作按钮 */}
            <div className="flex items-center gap-3 pt-1">
              <button
                onClick={handleSubmit}
                disabled={!form.name.trim() || !form.slug.trim() || isPending}
                className="inline-flex items-center gap-2 px-5 py-2.5 bg-emerald-500 text-white rounded-lg hover:bg-emerald-600 disabled:opacity-50 disabled:cursor-not-allowed text-sm font-medium transition-colors"
              >
                {isPending ? (
                  <><span className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" /> 提交中...</>
                ) : editId ? (
                  <><Check className="w-4 h-4" /> 保存修改</>
                ) : (
                  <><Plus className="w-4 h-4" /> 创建</>
                )}
              </button>
              <button
                onClick={resetForm}
                className="px-5 py-2.5 bg-emby-bg-input text-emby-text-secondary rounded-lg hover:bg-emby-bg-elevated hover:text-white text-sm transition-colors"
              >
                取消
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 分类卡片网格 */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
        {isLoading ? (
          Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-hidden animate-pulse">
              <div className="h-28 bg-emby-bg-input" />
              <div className="p-4 space-y-3">
                <div className="h-4 bg-emby-bg-input rounded w-2/3" />
                <div className="h-3 bg-emby-bg-input rounded w-1/2" />
                <div className="flex gap-2">
                  <div className="h-6 bg-emby-bg-input rounded w-12" />
                  <div className="h-6 bg-emby-bg-input rounded w-12" />
                </div>
              </div>
            </div>
          ))
        ) : filteredCategories?.length === 0 ? (
          <div className="col-span-full py-16 text-center">
            <FolderTree className="w-12 h-12 text-emby-text-muted mx-auto mb-3" />
            <p className="text-emby-text-muted text-sm">
              {search ? '未找到匹配的分类' : '暂无分类，点击"新建分类"添加'}
            </p>
          </div>
        ) : filteredCategories?.map((cat: Category) => {
          const mediaCount = cat._count?.media ?? 0;
          const isDeleting = deleteConfirmId === cat.id;

          return (
            <div
              key={cat.id}
              className="group bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-hidden hover:border-emerald-500/30 transition-all"
            >
              {/* 海报区域 */}
              <div className="relative h-28 bg-gradient-to-br from-emerald-900/40 to-emby-bg-input overflow-hidden">
                {cat.posterUrl ? (
                  <img
                    src={cat.posterUrl}
                    alt={cat.name}
                    className="w-full h-full object-cover opacity-80 group-hover:opacity-100 transition-opacity"
                    onError={e => { (e.target as HTMLImageElement).style.display = 'none'; }}
                  />
                ) : (
                  <div className="w-full h-full flex items-center justify-center">
                    <FolderTree className="w-10 h-10 text-emerald-500/30" />
                  </div>
                )}
                {/* 媒体数量角标 */}
                <div className="absolute top-2 right-2 flex items-center gap-1 px-2 py-1 bg-black/60 backdrop-blur-sm rounded-md text-xs text-white">
                  <Film className="w-3 h-3" />
                  {mediaCount}
                </div>
              </div>

              {/* 信息区域 */}
              <div className="p-4 space-y-3">
                <div>
                  <h3 className="text-white font-medium text-sm truncate">{cat.name}</h3>
                  <div className="flex items-center gap-1.5 mt-1">
                    <LinkIcon className="w-3 h-3 text-emby-text-muted" />
                    <span className="text-emby-text-muted text-xs font-mono truncate">{cat.slug}</span>
                  </div>
                </div>

                <div className="flex items-center gap-1.5 text-xs text-emby-text-muted">
                  <Calendar className="w-3 h-3" />
                  {new Date(cat.createdAt).toLocaleDateString('zh-CN', {
                    year: 'numeric',
                    month: '2-digit',
                    day: '2-digit',
                  })}
                </div>

                {/* 操作按钮 */}
                {isDeleting ? (
                  <div className="flex items-center gap-2 pt-1 border-t border-emby-border-subtle">
                    <div className="flex items-center gap-1.5 text-xs text-amber-400 flex-1">
                      <AlertTriangle className="w-3.5 h-3.5" />
                      <span>{mediaCount > 0 ? `${mediaCount} 个媒体将变为未分类` : '确认删除？'}</span>
                    </div>
                    <button
                      onClick={() => handleDelete(cat.id)}
                      disabled={deleteMutation.isPending}
                      className="px-2.5 py-1 bg-red-500/10 text-red-400 rounded-md text-xs font-medium hover:bg-red-500/20 disabled:opacity-50 transition-colors"
                    >
                      确认
                    </button>
                    <button
                      onClick={() => setDeleteConfirmId(null)}
                      className="px-2.5 py-1 bg-emby-bg-input text-emby-text-secondary rounded-md text-xs hover:bg-emby-bg-elevated transition-colors"
                    >
                      取消
                    </button>
                  </div>
                ) : (
                  <div className="flex items-center gap-2 pt-1 border-t border-emby-border-subtle">
                    <button
                      onClick={() => startEdit(cat)}
                      className="inline-flex items-center gap-1 px-3 py-1.5 bg-emerald-500/10 text-emerald-400 rounded-md text-xs font-medium hover:bg-emerald-500/20 transition-colors"
                    >
                      <Pencil className="w-3 h-3" /> 编辑
                    </button>
                    <button
                      onClick={() => setDeleteConfirmId(cat.id)}
                      disabled={deleteMutation.isPending}
                      className="inline-flex items-center gap-1 px-3 py-1.5 bg-red-500/10 text-red-400 rounded-md text-xs font-medium hover:bg-red-500/20 disabled:opacity-50 transition-colors"
                    >
                      <Trash2 className="w-3 h-3" /> 删除
                    </button>
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* 底部统计 */}
      {categories && categories.length > 0 && (
        <div className="flex items-center justify-between text-sm text-emby-text-muted">
          <span>共 {categories.length} 个分类，{totalMedia} 个媒体</span>
          {search && filteredCategories && (
            <span>搜索到 {filteredCategories.length} 个结果</span>
          )}
        </div>
      )}
    </div>
  );
}

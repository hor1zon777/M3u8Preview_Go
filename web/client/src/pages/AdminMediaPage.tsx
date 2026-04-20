import { useState, useEffect, useMemo, useCallback, useRef, useId } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import {
  Film,
  Plus,
  Trash2,
  ToggleLeft,
  X,
  ChevronDown,
  Check,
  Search,
  Pencil,
  Eye,
  FolderOpen,
  Play,
  CheckCircle2,
} from 'lucide-react';
import { mediaApi } from '../services/mediaApi.js';
import { adminApi } from '../services/adminApi.js';
import { categoryApi } from '../services/categoryApi.js';
import { MediaThumbnail } from '../components/media/MediaThumbnail.js';
import type { Media, MediaCreateRequest, Category } from '@m3u8-preview/shared';

export function AdminMediaPage() {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [search, setSearch] = useState('');
  const [showAdd, setShowAdd] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [form, setForm] = useState<Partial<MediaCreateRequest>>({ title: '', m3u8Url: '' });
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [batchCategoryValue, setBatchCategoryValue] = useState('');
  const [filterCategoryId, setFilterCategoryId] = useState('');
  const queryClient = useQueryClient();
  const fieldIdBase = useId();
  const titleFieldId = `${fieldIdBase}-title`;
  const m3u8FieldId = `${fieldIdBase}-m3u8-url`;
  const posterFieldId = `${fieldIdBase}-poster-url`;
  const artistFieldId = `${fieldIdBase}-artist`;
  const yearFieldId = `${fieldIdBase}-year`;
  const ratingFieldId = `${fieldIdBase}-rating`;
  const categoryFieldId = `${fieldIdBase}-category`;
  const descriptionFieldId = `${fieldIdBase}-description`;
  const pageSizeFieldId = `${fieldIdBase}-page-size`;
  const filterCategoryFieldId = `${fieldIdBase}-filter-category`;

  // 搜索变化时重置分页和选择
  useEffect(() => {
    setPage(1);
    setSelectedIds(new Set());
  }, [search]);

  // 分类筛选变化时重置分页和选择
  useEffect(() => {
    setPage(1);
    setSelectedIds(new Set());
  }, [filterCategoryId]);

  const handlePageSizeChange = useCallback((newSize: number) => {
    setPageSize(newSize);
    setPage(1);
    setSelectedIds(new Set());
  }, []);

  const { data, isLoading } = useQuery({
    queryKey: ['admin', 'media', page, pageSize, search, filterCategoryId],
    queryFn: () => mediaApi.getAll({
      page,
      limit: pageSize,
      search: search || undefined,
      categoryId: filterCategoryId || undefined,
    }),
  });

  const { data: categories } = useQuery({
    queryKey: ['categories'],
    queryFn: () => categoryApi.getAll(),
  });

  // 当前页所有 ID
  const currentPageIds = useMemo(
    () => data?.items?.map((m: Media) => m.id) ?? [],
    [data],
  );

  const isAllSelected = currentPageIds.length > 0 && currentPageIds.every((id: string) => selectedIds.has(id));
  const isPartialSelected = !isAllSelected && currentPageIds.some((id: string) => selectedIds.has(id));

  const toggleSelect = useCallback((id: string) => {
    setSelectedIds(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const toggleSelectAll = useCallback(() => {
    if (isAllSelected) {
      setSelectedIds(prev => {
        const next = new Set(prev);
        currentPageIds.forEach((id: string) => next.delete(id));
        return next;
      });
    } else {
      setSelectedIds(prev => {
        const next = new Set(prev);
        currentPageIds.forEach((id: string) => next.add(id));
        return next;
      });
    }
  }, [isAllSelected, currentPageIds]);

  const clearSelection = useCallback(() => setSelectedIds(new Set()), []);

  const invalidateBatch = () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'media'] });
    queryClient.invalidateQueries({ queryKey: ['media'] });
    clearSelection();
  };

  const createMutation = useMutation({
    mutationFn: (payload: MediaCreateRequest) => mediaApi.create(payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'media'] });
      queryClient.invalidateQueries({ queryKey: ['media'] });
      setShowAdd(false);
      setForm({ title: '', m3u8Url: '' });
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, payload }: { id: string; payload: Partial<MediaCreateRequest> }) => mediaApi.update(id, payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'media'] });
      queryClient.invalidateQueries({ queryKey: ['media'] });
      setEditId(null);
      setForm({ title: '', m3u8Url: '' });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => mediaApi.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'media'] });
      queryClient.invalidateQueries({ queryKey: ['media'] });
    },
  });

  const batchDeleteMutation = useMutation({
    mutationFn: (ids: string[]) => adminApi.batchDeleteMedia(ids),
    onSuccess: invalidateBatch,
  });

  const batchStatusMutation = useMutation({
    mutationFn: ({ ids, status }: { ids: string[]; status: 'ACTIVE' | 'INACTIVE' }) =>
      adminApi.batchUpdateMediaStatus(ids, status),
    onSuccess: invalidateBatch,
  });

  const batchCategoryMutation = useMutation({
    mutationFn: ({ ids, categoryId }: { ids: string[]; categoryId: string | null }) =>
      adminApi.batchUpdateMediaCategory(ids, categoryId),
    onSuccess: invalidateBatch,
  });

  const batchError = batchDeleteMutation.error || batchStatusMutation.error || batchCategoryMutation.error;
  const isBatchPending = batchDeleteMutation.isPending || batchStatusMutation.isPending || batchCategoryMutation.isPending;
  const actionError = (createMutation.error as any)?.response?.data?.error
    || (updateMutation.error as any)?.response?.data?.error
    || (deleteMutation.error as any)?.response?.data?.error
    || (batchError as any)?.response?.data?.error
    || '';

  const currentPageActiveCount = data?.items?.filter((media: Media) => media.status === 'ACTIVE').length ?? 0;
  const currentPageInactiveCount = data?.items?.filter((media: Media) => media.status === 'INACTIVE').length ?? 0;
  const currentPageViews = data?.items?.reduce((sum: number, media: Media) => sum + media.views, 0) ?? 0;
  const currentPageUncategorizedCount = data?.items?.filter((media: Media) => !media.category?.name).length ?? 0;
  const statCards = [
    {
      label: '总媒体数',
      value: data?.total ?? 0,
      hint: '当前搜索结果总量',
      icon: Film,
      color: 'text-blue-400',
      tone: 'bg-blue-500/10 border-blue-500/20',
    },
    {
      label: '当前页已上线',
      value: currentPageActiveCount,
      hint: 'ACTIVE 状态媒体',
      icon: CheckCircle2,
      color: 'text-green-400',
      tone: 'bg-green-500/10 border-green-500/20',
    },
    {
      label: '当前页未分类',
      value: currentPageUncategorizedCount,
      hint: '便于整理分类信息',
      icon: FolderOpen,
      color: 'text-purple-400',
      tone: 'bg-purple-500/10 border-purple-500/20',
    },
    {
      label: '当前页播放量',
      value: currentPageViews,
      hint: `${currentPageInactiveCount} 个内容处于停用状态`,
      icon: Play,
      color: 'text-orange-400',
      tone: 'bg-orange-500/10 border-orange-500/20',
    },
  ];

  function startEdit(media: Media) {
    setEditId(media.id);
    setForm({
      title: media.title,
      m3u8Url: media.m3u8Url,
      posterUrl: media.posterUrl || '',
      description: media.description || '',
      year: media.year || undefined,
      rating: media.rating || undefined,
      artist: media.artist || '',
      categoryId: media.categoryId || '',
    });
    setShowAdd(true);
  }

  function handleSubmit() {
    if (editId) {
      updateMutation.mutate({ id: editId, payload: form });
    } else {
      createMutation.mutate(form as MediaCreateRequest);
    }
  }

  function handleBatchDelete() {
    const ids = Array.from(selectedIds);
    if (confirm(`确定要删除选中的 ${ids.length} 项吗？此操作不可撤销。`)) {
      batchDeleteMutation.mutate(ids);
    }
  }

  function handleBatchCategoryChange(value: string) {
    const ids = Array.from(selectedIds);
    const categoryId = value === '__remove__' ? null : value;
    batchCategoryMutation.mutate({ ids, categoryId });
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-4">
        <div className="flex items-center gap-3">
          <div className="p-2 bg-blue-500/10 rounded-lg">
            <Film className="w-6 h-6 text-blue-400" />
          </div>
          <div>
            <h1 className="text-2xl font-bold text-white">媒体管理</h1>
            <p className="text-sm text-emby-text-muted mt-0.5">
              共 {data?.total || 0} 个媒体，当前页 {data?.items?.length || 0} 条
            </p>
          </div>
        </div>

        <div className="flex items-center gap-3 flex-wrap">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-emby-text-muted" />
            <input
              type="text"
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder="搜索标题或作者..."
              aria-label="搜索媒体"
              className="pl-10 pr-4 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent w-64 transition-all"
            />
          </div>
          <div className="flex items-center gap-2">
            <label htmlFor={filterCategoryFieldId} className="sr-only">分类筛选</label>
            <select
              id={filterCategoryFieldId}
              title="分类筛选"
              value={filterCategoryId}
              onChange={e => setFilterCategoryId(e.target.value)}
              className="px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm focus:outline-none focus:ring-2 focus:ring-emby-green min-w-[140px]"
            >
              <option value="">全部分类</option>
              {categories?.map((cat: Category) => (
                <option key={cat.id} value={cat.id}>{cat.name}</option>
              ))}
            </select>
          </div>
          <button
            onClick={() => {
              setShowAdd(!showAdd);
              setEditId(null);
              setForm({ title: '', m3u8Url: '' });
            }}
            className="inline-flex items-center gap-2 px-4 py-2.5 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark transition-colors text-sm font-medium"
          >
            <Plus className="w-4 h-4" />
            添加媒体
          </button>
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4">
        {statCards.map((card) => (
          <div key={card.label} className="bg-emby-bg-card border border-emby-border-subtle rounded-lg p-5">
            <div className="flex items-start justify-between gap-3">
              <div>
                <p className="text-sm text-emby-text-secondary">{card.label}</p>
                <p className="text-3xl font-bold text-white mt-3">{card.value.toLocaleString()}</p>
                <p className="text-xs text-emby-text-muted mt-1">{card.hint}</p>
              </div>
              <div className={`p-2.5 rounded-lg border ${card.tone}`}>
                <card.icon className={`w-5 h-5 ${card.color}`} />
              </div>
            </div>
          </div>
        ))}
      </div>

      {actionError && (
        <div className="bg-red-500/10 border border-red-500/20 text-red-400 px-4 py-3 rounded-lg text-sm">
          {actionError || '操作失败，请重试'}
        </div>
      )}

      {showAdd && (
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-hidden">
          <div className="px-5 py-4 border-b border-emby-border-subtle bg-emby-bg-input/30">
            <div className="flex items-center justify-between gap-3 flex-wrap">
              <div>
                <h3 className="text-white font-semibold text-base">{editId ? '编辑媒体' : '添加媒体'}</h3>
                <p className="text-sm text-emby-text-muted mt-1">
                  {editId ? '更新媒体信息与展示内容' : '填写基础信息后即可创建新的媒体条目'}
                </p>
              </div>
              <div className="text-xs text-emby-text-muted">标题与 M3U8 URL 为必填项</div>
            </div>
          </div>

          <div className="p-5 space-y-5">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="space-y-2">
                <label htmlFor={titleFieldId} className="text-sm text-emby-text-secondary">标题</label>
                <input
                  id={titleFieldId}
                  value={form.title || ''}
                  onChange={e => setForm({ ...form, title: e.target.value })}
                  placeholder="请输入媒体标题"
                  className="w-full px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
                />
              </div>
              <div className="space-y-2">
                <label htmlFor={m3u8FieldId} className="text-sm text-emby-text-secondary">M3U8 URL</label>
                <input
                  id={m3u8FieldId}
                  value={form.m3u8Url || ''}
                  onChange={e => setForm({ ...form, m3u8Url: e.target.value })}
                  placeholder="请输入 M3U8 地址"
                  className="w-full px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
                />
              </div>
              <div className="space-y-2">
                <label htmlFor={posterFieldId} className="text-sm text-emby-text-secondary">海报 URL</label>
                <input
                  id={posterFieldId}
                  value={form.posterUrl || ''}
                  onChange={e => setForm({ ...form, posterUrl: e.target.value })}
                  placeholder="可选，优先展示该图片"
                  className="w-full px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
                />
              </div>
              <div className="space-y-2">
                <label htmlFor={artistFieldId} className="text-sm text-emby-text-secondary">作者</label>
                <input
                  id={artistFieldId}
                  value={form.artist || ''}
                  onChange={e => setForm({ ...form, artist: e.target.value })}
                  placeholder="可选，填写作者或来源"
                  className="w-full px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
                />
              </div>
              <div className="space-y-2">
                <label className="text-sm text-emby-text-secondary" htmlFor={yearFieldId}>年份 / 评分</label>
                <div className="flex gap-3">
                  <input
                    id={yearFieldId}
                    type="number"
                    value={form.year || ''}
                    onChange={e => setForm({ ...form, year: e.target.value ? parseInt(e.target.value) : undefined })}
                    placeholder="年份"
                    className="flex-1 px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
                  />
                  <input
                    id={ratingFieldId}
                    type="number"
                    step="0.1"
                    value={form.rating || ''}
                    onChange={e => setForm({ ...form, rating: e.target.value ? parseFloat(e.target.value) : undefined })}
                    placeholder="评分"
                    aria-label="评分"
                    className="flex-1 px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
                  />
                </div>
              </div>
              <div className="space-y-2">
                <label htmlFor={categoryFieldId} className="text-sm text-emby-text-secondary">分类</label>
                <CategoryCombobox
                  inputId={categoryFieldId}
                  categories={categories || []}
                  value={form.categoryId}
                  onChange={(categoryId) => setForm({ ...form, categoryId })}
                />
              </div>
            </div>

            <div className="space-y-2">
              <label htmlFor={descriptionFieldId} className="text-sm text-emby-text-secondary">描述</label>
              <textarea
                id={descriptionFieldId}
                value={form.description || ''}
                onChange={e => setForm({ ...form, description: e.target.value })}
                placeholder="补充媒体内容介绍、来源或备注"
                rows={3}
                className="w-full px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
              />
            </div>

            <div className="flex items-center gap-3 flex-wrap pt-1">
              <button
                onClick={handleSubmit}
                disabled={!form.title?.trim() || !form.m3u8Url?.trim()}
                className="inline-flex items-center gap-2 px-4 py-2.5 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark disabled:opacity-50 disabled:cursor-not-allowed transition-colors text-sm font-medium"
              >
                <Check className="w-4 h-4" />
                {editId ? '保存修改' : '添加媒体'}
              </button>
              <button
                onClick={() => {
                  setShowAdd(false);
                  setEditId(null);
                }}
                className="inline-flex items-center gap-2 px-4 py-2.5 bg-emby-bg-input text-emby-text-primary rounded-lg hover:bg-emby-bg-elevated transition-colors text-sm font-medium"
              >
                <X className="w-4 h-4" />
                取消
              </button>
            </div>
          </div>
        </div>
      )}

      {selectedIds.size > 0 && (
        <div className="bg-emby-bg-card border border-emby-green/30 rounded-lg px-4 py-4 space-y-3">
          <div className="flex items-center justify-between gap-3 flex-wrap">
            <div>
              <p className="text-sm font-medium text-white">已选中 {selectedIds.size} 个媒体</p>
              <p className="text-xs text-emby-text-muted mt-1">可批量调整状态、分类或直接删除当前选中项</p>
            </div>
            <button
              onClick={clearSelection}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-emby-bg-input text-emby-text-secondary rounded-lg hover:bg-emby-bg-elevated transition-colors text-xs font-medium"
            >
              <X className="w-3.5 h-3.5" />
              取消选择
            </button>
          </div>

          <div className="flex items-center gap-2 flex-wrap">
            <button
              onClick={() => batchStatusMutation.mutate({ ids: Array.from(selectedIds), status: 'ACTIVE' })}
              disabled={isBatchPending}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-green-500/10 text-green-400 border border-green-500/30 rounded-lg hover:bg-green-500/20 transition-colors text-xs font-medium disabled:opacity-50"
            >
              <ToggleLeft className="w-3.5 h-3.5" />
              设为 ACTIVE
            </button>
            <button
              onClick={() => batchStatusMutation.mutate({ ids: Array.from(selectedIds), status: 'INACTIVE' })}
              disabled={isBatchPending}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-yellow-500/10 text-yellow-400 border border-yellow-500/30 rounded-lg hover:bg-yellow-500/20 transition-colors text-xs font-medium disabled:opacity-50"
            >
              <ToggleLeft className="w-3.5 h-3.5" />
              设为 INACTIVE
            </button>
            <select
              aria-label="批量修改分类"
              title="批量修改分类"
              value={batchCategoryValue}
              onChange={e => {
                const val = e.target.value;
                if (!val) return;
                handleBatchCategoryChange(val);
                setBatchCategoryValue('');
              }}
              disabled={isBatchPending}
              className="px-3 py-1.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-xs disabled:opacity-50 focus:outline-none focus:ring-2 focus:ring-emby-green"
            >
              <option value="" disabled>批量修改分类</option>
              <option value="__remove__">移除分类</option>
              {categories?.map((cat: Category) => (
                <option key={cat.id} value={cat.id}>{cat.name}</option>
              ))}
            </select>
            <button
              onClick={handleBatchDelete}
              disabled={isBatchPending}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-red-500/10 text-red-400 border border-red-500/30 rounded-lg hover:bg-red-500/20 transition-colors text-xs font-medium disabled:opacity-50 sm:ml-auto"
            >
              <Trash2 className="w-3.5 h-3.5" />
              批量删除
            </button>
          </div>
        </div>
      )}

      <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-emby-bg-input/50 border-b border-emby-border-subtle">
                <th className="px-4 py-4 text-left w-10">
                  <input
                    type="checkbox"
                    checked={isAllSelected}
                    aria-label={isAllSelected ? '取消选择当前页全部媒体' : '选择当前页全部媒体'}
                    ref={el => {
                      if (el) el.indeterminate = isPartialSelected;
                    }}
                    onChange={toggleSelectAll}
                    className="rounded border-emby-border text-emby-green focus:ring-emby-green"
                  />
                </th>
                <th className="px-4 py-4 text-left text-emby-text-secondary font-semibold min-w-[320px]">媒体信息</th>
                <th className="px-4 py-4 text-left text-emby-text-secondary font-semibold">分类</th>
                <th className="px-4 py-4 text-left text-emby-text-secondary font-semibold">年份</th>
                <th className="px-4 py-4 text-left text-emby-text-secondary font-semibold">评分</th>
                <th className="px-4 py-4 text-left text-emby-text-secondary font-semibold">播放量</th>
                <th className="px-4 py-4 text-left text-emby-text-secondary font-semibold">状态</th>
                <th className="px-4 py-4 text-right text-emby-text-secondary font-semibold">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-emby-border-subtle/50">
              {isLoading ? (
                Array.from({ length: 5 }).map((_, i) => (
                  <tr key={i}>
                    <td colSpan={8} className="px-4 py-4">
                      <div className="h-16 bg-emby-bg-input/30 rounded animate-pulse" />
                    </td>
                  </tr>
                ))
              ) : data?.items?.length === 0 ? (
                <tr>
                  <td colSpan={8} className="px-6 py-14 text-center">
                    <div className="max-w-sm mx-auto space-y-2">
                      <p className="text-white font-medium">{search ? '未找到匹配的媒体' : '暂无媒体内容'}</p>
                      <p className="text-sm text-emby-text-muted">
                        {search ? '可以尝试更换关键词，或清空搜索后查看全部媒体。' : '点击右上角“添加媒体”开始创建内容。'}
                      </p>
                    </div>
                  </td>
                </tr>
              ) : (
                data?.items?.map((media: Media) => (
                  <tr
                    key={media.id}
                    className={`hover:bg-emby-bg-input/20 transition-colors ${selectedIds.has(media.id) ? 'bg-emby-green/5' : ''}`}
                  >
                    <td className="px-4 py-4 align-top">
                      <input
                        type="checkbox"
                        checked={selectedIds.has(media.id)}
                        aria-label={selectedIds.has(media.id) ? `取消选择媒体 ${media.title}` : `选择媒体 ${media.title}`}
                        onChange={() => toggleSelect(media.id)}
                        className="mt-2 rounded border-emby-border text-emby-green focus:ring-emby-green"
                      />
                    </td>
                    <td className="px-4 py-4">
                      <div className="flex items-start gap-3 min-w-0">
                        <Link
                          to={`/media/${media.id}`}
                          className="w-24 aspect-video bg-emby-bg-input rounded-lg flex-shrink-0 overflow-hidden"
                        >
                          <MediaThumbnail
                            mediaId={media.id}
                            m3u8Url={media.m3u8Url}
                            posterUrl={media.posterUrl}
                            title={media.title}
                            iconSize="w-4 h-4"
                          />
                        </Link>
                        <div className="min-w-0 space-y-1.5">
                          <Link
                            to={`/media/${media.id}`}
                            className="text-white hover:text-emby-green-light font-medium line-clamp-1 transition-colors"
                          >
                            {media.title}
                          </Link>
                          <div className="flex items-center gap-2 flex-wrap text-xs text-emby-text-muted">
                            <span>ID: {media.id.slice(0, 8)}</span>
                            {media.artist && <span>作者：{media.artist}</span>}
                            {media.category?.name && <span>分类：{media.category.name}</span>}
                          </div>
                          {media.description && (
                            <p className="text-xs text-emby-text-muted line-clamp-2 max-w-xl">{media.description}</p>
                          )}
                        </div>
                      </div>
                    </td>
                    <td className="px-4 py-4 text-emby-text-secondary align-top">
                      {media.category?.name || '未分类'}
                    </td>
                    <td className="px-4 py-4 text-emby-text-secondary align-top">{media.year || '-'}</td>
                    <td className="px-4 py-4 text-emby-text-secondary align-top">{media.rating?.toFixed(1) || '-'}</td>
                    <td className="px-4 py-4 text-emby-text-secondary align-top">{media.views.toLocaleString()}</td>
                    <td className="px-4 py-4 align-top">
                      <span
                        className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border ${
                          media.status === 'ACTIVE'
                            ? 'bg-green-500/10 text-green-400 border-green-500/30'
                            : 'bg-red-500/10 text-red-400 border-red-500/30'
                        }`}
                      >
                        {media.status === 'ACTIVE' ? <CheckCircle2 className="w-3.5 h-3.5" /> : <X className="w-3.5 h-3.5" />}
                        {media.status}
                      </span>
                    </td>
                    <td className="px-4 py-4 align-top">
                      <div className="flex items-center justify-end gap-2">
                        <Link
                          to={`/media/${media.id}`}
                          className="inline-flex items-center gap-1 px-3 py-1.5 bg-emby-green/10 text-emby-green rounded-lg text-xs font-medium hover:bg-emby-green/20 transition-colors"
                        >
                          <Eye className="w-3.5 h-3.5" />
                          查看
                        </Link>
                        <button
                          onClick={() => startEdit(media)}
                          className="inline-flex items-center gap-1 px-3 py-1.5 bg-blue-500/10 text-blue-400 rounded-lg text-xs font-medium hover:bg-blue-500/20 transition-colors"
                        >
                          <Pencil className="w-3.5 h-3.5" />
                          编辑
                        </button>
                        <button
                          onClick={() => {
                            if (confirm(`确定要删除 "${media.title}" 吗？`)) {
                              deleteMutation.mutate(media.id);
                            }
                          }}
                          disabled={deleteMutation.isPending}
                          className="inline-flex items-center gap-1 px-3 py-1.5 bg-red-500/10 text-red-400 rounded-lg text-xs font-medium hover:bg-red-500/20 transition-colors disabled:opacity-50"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                          删除
                        </button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {data && (
        <div className="flex items-center justify-between flex-wrap gap-4">
          <div className="text-sm text-emby-text-muted">
            显示第 {(page - 1) * pageSize + 1} - {Math.min(page * pageSize, data.total)} 条，共 {data.total} 条
          </div>
          <div className="flex items-center gap-2">
            {data.totalPages > 1 && (
              <>
                <button
                  onClick={() => setPage(Math.max(1, page - 1))}
                  disabled={page === 1}
                  className="px-4 py-2 bg-emby-bg-input text-white rounded-lg disabled:opacity-50 disabled:cursor-not-allowed hover:bg-emby-bg-elevated transition-colors text-sm font-medium"
                >
                  上一页
                </button>
                <span className="text-emby-text-secondary text-sm px-2">{page} / {data.totalPages}</span>
                <button
                  onClick={() => setPage(Math.min(data.totalPages, page + 1))}
                  disabled={page === data.totalPages}
                  className="px-4 py-2 bg-emby-bg-input text-white rounded-lg disabled:opacity-50 disabled:cursor-not-allowed hover:bg-emby-bg-elevated transition-colors text-sm font-medium"
                >
                  下一页
                </button>
              </>
            )}
          </div>
          <div className="flex items-center gap-2">
            <label htmlFor={pageSizeFieldId} className="text-emby-text-secondary text-sm">每页显示</label>
            <select
              id={pageSizeFieldId}
              title="每页显示数量"
              value={pageSize}
              onChange={(e) => handlePageSizeChange(Number(e.target.value))}
              className="px-3 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm focus:outline-none focus:ring-2 focus:ring-emby-green"
            >
              <option value={10}>10</option>
              <option value={20}>20</option>
              <option value={50}>50</option>
              <option value={100}>100</option>
            </select>
            <span className="text-emby-text-secondary text-sm">条</span>
          </div>
        </div>
      )}
    </div>
  );
}

// ==================== 可搜索分类选择器 ====================

interface CategoryComboboxProps {
  inputId: string;
  categories: Category[];
  value: string | undefined;
  onChange: (categoryId: string | undefined) => void;
}

function CategoryCombobox({ inputId, categories, value, onChange }: CategoryComboboxProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [searchText, setSearchText] = useState('');
  const [highlightIndex, setHighlightIndex] = useState(-1);
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listboxId = `${inputId}-listbox`;

  // 选中的分类名
  const selectedName = value ? categories.find(c => c.id === value)?.name || '' : '';

  // 过滤后的选项
  const filtered = useMemo(() => {
    if (!searchText) return categories;
    const lower = searchText.toLowerCase();
    return categories.filter(c => c.name.toLowerCase().includes(lower));
  }, [categories, searchText]);

  const activeOptionId = highlightIndex >= 0
    ? `${inputId}-option-${highlightIndex}`
    : undefined;

  // 点击外部关闭
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setIsOpen(false);
        setSearchText('');
        setHighlightIndex(-1);
      }
    }
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  // 打开时重置
  function handleOpen() {
    setIsOpen(true);
    setSearchText('');
    setHighlightIndex(-1);
    requestAnimationFrame(() => inputRef.current?.focus());
  }

  // 选择分类
  function handleSelect(categoryId: string | undefined) {
    onChange(categoryId);
    setIsOpen(false);
    setSearchText('');
    setHighlightIndex(-1);
    triggerRef.current?.focus();
  }

  // 键盘导航
  function handleKeyDown(e: React.KeyboardEvent) {
    const total = filtered.length + 1; // +1 "无分类"选项
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setHighlightIndex(prev => (prev + 1) % total);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setHighlightIndex(prev => (prev - 1 + total) % total);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (highlightIndex === 0) {
        handleSelect(undefined);
      } else if (highlightIndex > 0 && highlightIndex <= filtered.length) {
        handleSelect(filtered[highlightIndex - 1].id);
      }
    } else if (e.key === 'Escape') {
      setIsOpen(false);
      setSearchText('');
      setHighlightIndex(-1);
      triggerRef.current?.focus();
    }
  }

  return (
    <div ref={containerRef} className="relative">
      {/* 触发器 */}
      {isOpen ? (
        <input
          id={inputId}
          ref={inputRef}
          role="combobox"
          aria-expanded={true}
          aria-controls={listboxId}
          aria-haspopup="listbox"
          aria-activedescendant={activeOptionId}
          value={searchText}
          onChange={e => {
            setSearchText(e.target.value);
            setHighlightIndex(-1);
          }}
          onKeyDown={handleKeyDown}
          placeholder="搜索分类..."
          className="w-full px-3 py-2 bg-emby-bg-input border border-emby-green rounded-md text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green"
        />
      ) : (
        <div className="flex items-center gap-2">
          <button
            id={inputId}
            ref={triggerRef}
            type="button"
            role="combobox"
            aria-expanded={false}
            aria-controls={listboxId}
            aria-haspopup="listbox"
            onClick={handleOpen}
            className="flex-1 px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-sm text-left flex items-center justify-between focus:outline-none focus:ring-2 focus:ring-emby-green"
          >
            <span className={value ? 'text-white' : 'text-emby-text-muted'}>
              {selectedName || '选择分类'}
            </span>
            <ChevronDown className="w-4 h-4 text-emby-text-muted" />
          </button>
          {value && (
            <button
              type="button"
              aria-label="清除分类"
              onClick={() => handleSelect(undefined)}
              className="px-2.5 py-2 bg-emby-bg-input border border-emby-border rounded-md text-emby-text-muted hover:text-white focus:outline-none focus:ring-2 focus:ring-emby-green"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          )}
        </div>
      )}

      {/* 下拉菜单 */}
      {isOpen && (
        <div
          id={listboxId}
          role="listbox"
          className="absolute z-50 mt-1 w-full bg-emby-bg-dialog border border-emby-border rounded-md shadow-xl max-h-48 overflow-y-auto"
        >
          {/* 无分类选项 */}
          <button
            id={`${inputId}-option-0`}
            role="option"
            aria-selected={!value ? true : false}
            type="button"
            onClick={() => handleSelect(undefined)}
            className={`w-full px-3 py-2 text-sm text-left flex items-center justify-between transition-colors ${
              highlightIndex === 0 ? 'bg-emby-bg-elevated' : 'hover:bg-emby-bg-elevated'
            } ${!value ? 'text-emby-green' : 'text-emby-text-secondary'}`}
          >
            无分类
            {!value && <Check className="w-4 h-4" />}
          </button>

          {/* 分类选项 */}
          {filtered.map((cat, i) => (
            <button
              id={`${inputId}-option-${i + 1}`}
              key={cat.id}
              role="option"
              aria-selected={value === cat.id ? true : false}
              type="button"
              onClick={() => handleSelect(cat.id)}
              className={`w-full px-3 py-2 text-sm text-left flex items-center justify-between transition-colors ${
                highlightIndex === i + 1 ? 'bg-emby-bg-elevated' : 'hover:bg-emby-bg-elevated'
              } ${value === cat.id ? 'text-emby-green' : 'text-white'}`}
            >
              {cat.name}
              {value === cat.id && <Check className="w-4 h-4" />}
            </button>
          ))}

          {/* 无结果 */}
          {filtered.length === 0 && searchText && (
            <div className="px-3 py-2 text-sm text-emby-text-muted">未找到分类</div>
          )}
        </div>
      )}
    </div>
  );
}

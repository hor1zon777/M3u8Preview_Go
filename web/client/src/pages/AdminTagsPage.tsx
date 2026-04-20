import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Tag as TagIcon,
  Plus,
  Pencil,
  Trash2,
  Search,
  Film,
  Calendar,
  X,
  Check,
  AlertTriangle,
} from 'lucide-react';
import { tagApi } from '../services/tagApi.js';
import type { Tag, TagCreateRequest } from '@m3u8-preview/shared';

const emptyForm: TagCreateRequest = { name: '' };

export function AdminTagsPage() {
  const [showForm, setShowForm] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [form, setForm] = useState<TagCreateRequest>({ ...emptyForm });
  const [search, setSearch] = useState('');
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null);
  const queryClient = useQueryClient();

  const { data: tags, isLoading } = useQuery({
    queryKey: ['admin', 'tags'],
    queryFn: () => tagApi.getAll(),
  });

  const createMutation = useMutation({
    mutationFn: (payload: TagCreateRequest) => tagApi.create(payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'tags'] });
      resetForm();
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, payload }: { id: string; payload: Partial<TagCreateRequest> }) =>
      tagApi.update(id, payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'tags'] });
      resetForm();
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => tagApi.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'tags'] });
      setDeleteConfirmId(null);
    },
  });

  function resetForm() {
    setShowForm(false);
    setEditId(null);
    setForm({ ...emptyForm });
  }

  function startEdit(tag: Tag) {
    setEditId(tag.id);
    setForm({ name: tag.name });
    setShowForm(true);
  }

  function handleSubmit() {
    const payload: TagCreateRequest = { name: form.name.trim() };
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

  const filteredTags = tags?.filter((tag: Tag) =>
    !search || tag.name.toLowerCase().includes(search.toLowerCase())
  );

  const totalMedia = tags?.reduce((sum: number, tag: Tag) => sum + (tag._count?.media ?? 0), 0) ?? 0;

  return (
    <div className="space-y-6">
      {/* 页头 */}
      <div className="flex items-center justify-between flex-wrap gap-4">
        <div className="flex items-center gap-3">
          <div className="p-2 bg-violet-500/10 rounded-lg">
            <TagIcon className="w-6 h-6 text-violet-400" />
          </div>
          <div>
            <h1 className="text-2xl font-bold text-white">标签管理</h1>
            <p className="text-sm text-emby-text-muted mt-0.5">
              共 {tags?.length ?? 0} 个标签，{totalMedia} 个媒体
            </p>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-emby-text-muted" />
            <input
              type="text"
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder="搜索标签..."
              className="pl-10 pr-4 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-violet-500 focus:border-transparent w-56 transition-all"
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

          <button
            onClick={() => { setShowForm(!showForm); setEditId(null); setForm({ ...emptyForm }); }}
            className="inline-flex items-center gap-2 px-4 py-2.5 bg-violet-500 text-white rounded-lg hover:bg-violet-600 transition-colors text-sm font-medium"
          >
            <Plus className="w-4 h-4" />
            新建标签
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
                <><Pencil className="w-4 h-4 text-violet-400" /> 编辑标签</>
              ) : (
                <><Plus className="w-4 h-4 text-violet-400" /> 新建标签</>
              )}
            </h3>
            <button onClick={resetForm} className="text-emby-text-muted hover:text-white transition-colors">
              <X className="w-5 h-5" />
            </button>
          </div>

          <div className="p-5 space-y-4">
            <div className="max-w-md space-y-1.5">
              <label className="text-xs font-medium text-emby-text-secondary flex items-center gap-1.5">
                <span className="w-1 h-1 rounded-full bg-violet-400" />
                标签名称 <span className="text-red-400">*</span>
              </label>
              <input
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="例如：高清"
                onKeyDown={(e) => e.key === 'Enter' && form.name.trim() && handleSubmit()}
                className="w-full px-3 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-violet-500 focus:border-transparent transition-all"
              />
            </div>

            <div className="flex items-center gap-3 pt-1">
              <button
                onClick={handleSubmit}
                disabled={!form.name.trim() || isPending}
                className="inline-flex items-center gap-2 px-5 py-2.5 bg-violet-500 text-white rounded-lg hover:bg-violet-600 disabled:opacity-50 disabled:cursor-not-allowed text-sm font-medium transition-colors"
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

      {/* 标签卡片网格 */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6 gap-3">
        {isLoading ? (
          Array.from({ length: 12 }).map((_, i) => (
            <div key={i} className="bg-emby-bg-card border border-emby-border-subtle rounded-lg p-4 animate-pulse">
              <div className="h-4 bg-emby-bg-input rounded w-2/3 mb-3" />
              <div className="h-3 bg-emby-bg-input rounded w-1/2" />
            </div>
          ))
        ) : filteredTags?.length === 0 ? (
          <div className="col-span-full py-16 text-center">
            <TagIcon className="w-12 h-12 text-emby-text-muted mx-auto mb-3" />
            <p className="text-emby-text-muted text-sm">
              {search ? '未找到匹配的标签' : '暂无标签，点击"新建标签"添加'}
            </p>
          </div>
        ) : filteredTags?.map((tag: Tag) => {
          const mediaCount = tag._count?.media ?? 0;
          const isDeleting = deleteConfirmId === tag.id;

          return (
            <div
              key={tag.id}
              className="group bg-emby-bg-card border border-emby-border-subtle rounded-lg p-4 hover:border-violet-500/30 transition-all"
            >
              <div className="flex items-start justify-between mb-2">
                <h3 className="text-white font-medium text-sm truncate flex-1">{tag.name}</h3>
                <div className="flex items-center gap-1 px-1.5 py-0.5 bg-violet-500/10 rounded text-xs text-violet-400 ml-2 flex-shrink-0">
                  <Film className="w-3 h-3" />
                  {mediaCount}
                </div>
              </div>

              <div className="flex items-center gap-1.5 text-xs text-emby-text-muted mb-3">
                <Calendar className="w-3 h-3" />
                {new Date(tag.createdAt).toLocaleDateString('zh-CN', {
                  year: 'numeric',
                  month: '2-digit',
                  day: '2-digit',
                })}
              </div>

              {isDeleting ? (
                <div className="flex items-center gap-1.5 pt-2 border-t border-emby-border-subtle">
                  <span className="text-xs text-amber-400 flex-1 truncate">
                    {mediaCount > 0 ? `${mediaCount} 个关联` : '确认？'}
                  </span>
                  <button
                    onClick={() => handleDelete(tag.id)}
                    disabled={deleteMutation.isPending}
                    className="px-2 py-1 bg-red-500/10 text-red-400 rounded text-xs hover:bg-red-500/20 disabled:opacity-50 transition-colors"
                  >
                    确认
                  </button>
                  <button
                    onClick={() => setDeleteConfirmId(null)}
                    className="px-2 py-1 bg-emby-bg-input text-emby-text-secondary rounded text-xs hover:bg-emby-bg-elevated transition-colors"
                  >
                    取消
                  </button>
                </div>
              ) : (
                <div className="flex items-center gap-1.5 pt-2 border-t border-emby-border-subtle opacity-0 group-hover:opacity-100 transition-opacity">
                  <button
                    onClick={() => startEdit(tag)}
                    className="inline-flex items-center gap-1 px-2 py-1 bg-violet-500/10 text-violet-400 rounded text-xs hover:bg-violet-500/20 transition-colors"
                  >
                    <Pencil className="w-3 h-3" /> 编辑
                  </button>
                  <button
                    onClick={() => setDeleteConfirmId(tag.id)}
                    disabled={deleteMutation.isPending}
                    className="inline-flex items-center gap-1 px-2 py-1 bg-red-500/10 text-red-400 rounded text-xs hover:bg-red-500/20 disabled:opacity-50 transition-colors"
                  >
                    <Trash2 className="w-3 h-3" /> 删除
                  </button>
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* 底部统计 */}
      {tags && tags.length > 0 && (
        <div className="flex items-center justify-between text-sm text-emby-text-muted">
          <span>共 {tags.length} 个标签，{totalMedia} 个媒体</span>
          {search && filteredTags && (
            <span>搜索到 {filteredTags.length} 个结果</span>
          )}
        </div>
      )}
    </div>
  );
}

import { useState, useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Users, Heart, ListVideo, Clock, Search, Shield, UserCheck, UserX, Trash2, Eye } from 'lucide-react';
import { adminApi } from '../services/adminApi.js';

export function AdminUsersPage() {
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const queryClient = useQueryClient();

  // H2: 搜索变化时重置分页
  useEffect(() => {
    setPage(1);
  }, [search]);

  const { data, isLoading } = useQuery({
    queryKey: ['admin', 'users', page, search],
    queryFn: () => adminApi.getUsers(page, 20, search || undefined),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, payload }: { id: string; payload: any }) => adminApi.updateUser(id, payload),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'users'] }),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => adminApi.deleteUser(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'users'] }),
  });

  return (
    <div className="space-y-6">
      {/* 页头 */}
      <div className="flex items-center justify-between flex-wrap gap-4">
        <div className="flex items-center gap-3">
          <div className="p-2 bg-blue-500/10 rounded-lg">
            <Users className="w-6 h-6 text-blue-400" />
          </div>
          <div>
            <h1 className="text-2xl font-bold text-white">用户管理</h1>
            <p className="text-sm text-emby-text-muted mt-0.5">
              共 {data?.total || 0} 个用户
            </p>
          </div>
        </div>

        {/* 搜索框 */}
        <div className="relative">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-emby-text-muted" />
          <input
            type="text"
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="搜索用户名..."
            className="pl-10 pr-4 py-2.5 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent w-64 transition-all"
          />
        </div>
      </div>

      {/* 用户表格 */}
      <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-emby-bg-input/50 border-b border-emby-border-subtle">
                <th className="px-6 py-4 text-left text-emby-text-secondary font-semibold">用户</th>
                <th className="px-6 py-4 text-left text-emby-text-secondary font-semibold">角色</th>
                <th className="px-6 py-4 text-left text-emby-text-secondary font-semibold">状态</th>
                <th className="px-6 py-4 text-left text-emby-text-secondary font-semibold">活动统计</th>
                <th className="px-6 py-4 text-left text-emby-text-secondary font-semibold">注册时间</th>
                <th className="px-6 py-4 text-right text-emby-text-secondary font-semibold">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-emby-border-subtle/50">
              {isLoading ? (
                Array.from({ length: 5 }).map((_, i) => (
                  <tr key={i}>
                    <td colSpan={6} className="px-6 py-4">
                      <div className="h-12 bg-emby-bg-input/30 rounded animate-pulse" />
                    </td>
                  </tr>
                ))
              ) : data?.items?.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-6 py-12 text-center text-emby-text-muted">
                    {search ? '未找到匹配的用户' : '暂无用户'}
                  </td>
                </tr>
              ) : (
                data?.items?.map((user: any) => (
                  <tr key={user.id} className="hover:bg-emby-bg-input/20 transition-colors">
                    {/* 用户名 */}
                    <td className="px-6 py-4">
                      <div className="flex items-center gap-3">
                        <div className="w-10 h-10 rounded-full bg-gradient-to-br from-blue-500 to-purple-500 flex items-center justify-center text-white font-semibold">
                          {user.username.charAt(0).toUpperCase()}
                        </div>
                        <div>
                          <div className="text-white font-medium">{user.username}</div>
                          <div className="text-xs text-emby-text-muted">ID: {user.id.slice(0, 8)}</div>
                        </div>
                      </div>
                    </td>

                    {/* 角色 */}
                    <td className="px-6 py-4">
                      <div className="relative inline-block">
                        <select
                          aria-label="用户角色"
                          value={user.role}
                          onChange={e => updateMutation.mutate({ id: user.id, payload: { role: e.target.value } })}
                          disabled={updateMutation.isPending}
                          className={`appearance-none pl-3 pr-8 py-1.5 rounded-lg text-xs font-medium border transition-all focus:outline-none focus:ring-2 focus:ring-emby-green ${
                            user.role === 'ADMIN'
                              ? 'bg-amber-500/10 text-amber-400 border-amber-500/30 hover:bg-amber-500/20'
                              : 'bg-blue-500/10 text-blue-400 border-blue-500/30 hover:bg-blue-500/20'
                          } disabled:opacity-50 cursor-pointer`}
                          style={{
                            backgroundImage: `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath fill='${user.role === 'ADMIN' ? '%23fbbf24' : '%2360a5fa'}' d='M6 9L1 4h10z'/%3E%3C/svg%3E")`,
                            backgroundRepeat: 'no-repeat',
                            backgroundPosition: 'right 0.5rem center',
                            backgroundSize: '12px',
                          }}
                        >
                          <option value="USER" className="bg-emby-bg-card text-white">普通用户</option>
                          <option value="ADMIN" className="bg-emby-bg-card text-white">管理员</option>
                        </select>
                      </div>
                    </td>

                    {/* 状态 */}
                    <td className="px-6 py-4">
                      <button
                        onClick={() => updateMutation.mutate({ id: user.id, payload: { isActive: !user.isActive } })}
                        disabled={updateMutation.isPending}
                        className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium transition-all hover:brightness-110 disabled:opacity-50 ${
                          user.isActive
                            ? 'bg-green-500/10 text-green-400 border border-green-500/30'
                            : 'bg-red-500/10 text-red-400 border border-red-500/30'
                        }`}
                      >
                        {user.isActive ? (
                          <>
                            <UserCheck className="w-3.5 h-3.5" />
                            活跃
                          </>
                        ) : (
                          <>
                            <UserX className="w-3.5 h-3.5" />
                            禁用
                          </>
                        )}
                      </button>
                    </td>

                    {/* 统计 */}
                    <td className="px-6 py-4">
                      <div className="flex items-center gap-3 text-xs text-emby-text-muted">
                        <div className="flex items-center gap-1" title="收藏数">
                          <Heart className="w-3.5 h-3.5 text-pink-400" />
                          <span>{user._count?.favorites || 0}</span>
                        </div>
                        <div className="flex items-center gap-1" title="播放列表">
                          <ListVideo className="w-3.5 h-3.5 text-purple-400" />
                          <span>{user._count?.playlists || 0}</span>
                        </div>
                        <div className="flex items-center gap-1" title="观看历史">
                          <Clock className="w-3.5 h-3.5 text-blue-400" />
                          <span>{user._count?.watchHistory || 0}</span>
                        </div>
                      </div>
                    </td>

                    {/* 注册时间 */}
                    <td className="px-6 py-4 text-emby-text-muted text-xs">
                      {new Date(user.createdAt).toLocaleDateString('zh-CN', {
                        year: 'numeric',
                        month: '2-digit',
                        day: '2-digit',
                      })}
                    </td>

                    {/* 操作 */}
                    <td className="px-6 py-4">
                      <div className="flex items-center justify-end gap-2">
                        <Link
                          to={`/admin/users/${user.id}`}
                          className="inline-flex items-center gap-1 px-3 py-1.5 bg-emby-green/10 text-emby-green rounded-lg text-xs font-medium hover:bg-emby-green/20 transition-colors"
                        >
                          <Eye className="w-3.5 h-3.5" />
                          详情
                        </Link>
                        {user.role !== 'ADMIN' && (
                          <button
                            onClick={() => {
                              if (confirm(`确定要删除用户 ${user.username} 吗？此操作不可恢复。`)) {
                                deleteMutation.mutate(user.id);
                              }
                            }}
                            disabled={deleteMutation.isPending}
                            className="inline-flex items-center gap-1 px-3 py-1.5 bg-red-500/10 text-red-400 rounded-lg text-xs font-medium hover:bg-red-500/20 transition-colors disabled:opacity-50"
                          >
                            <Trash2 className="w-3.5 h-3.5" />
                            删除
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* 分页 */}
      {data && data.totalPages > 1 && (
        <div className="flex items-center justify-between">
          <div className="text-sm text-emby-text-muted">
            显示第 {(page - 1) * 20 + 1} - {Math.min(page * 20, data.total)} 条，共 {data.total} 条
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setPage(Math.max(1, page - 1))}
              disabled={page === 1}
              className="px-4 py-2 bg-emby-bg-input text-white rounded-lg disabled:opacity-50 disabled:cursor-not-allowed hover:bg-emby-bg-elevated transition-colors text-sm font-medium"
            >
              上一页
            </button>
            <div className="flex items-center gap-1">
              {Array.from({ length: Math.min(5, data.totalPages) }, (_, i) => {
                let pageNum;
                if (data.totalPages <= 5) {
                  pageNum = i + 1;
                } else if (page <= 3) {
                  pageNum = i + 1;
                } else if (page >= data.totalPages - 2) {
                  pageNum = data.totalPages - 4 + i;
                } else {
                  pageNum = page - 2 + i;
                }
                return (
                  <button
                    key={pageNum}
                    onClick={() => setPage(pageNum)}
                    className={`w-10 h-10 rounded-lg text-sm font-medium transition-colors ${
                      page === pageNum
                        ? 'bg-emby-green text-white'
                        : 'bg-emby-bg-input text-emby-text-secondary hover:bg-emby-bg-elevated hover:text-white'
                    }`}
                  >
                    {pageNum}
                  </button>
                );
              })}
            </div>
            <button
              onClick={() => setPage(Math.min(data.totalPages, page + 1))}
              disabled={page === data.totalPages}
              className="px-4 py-2 bg-emby-bg-input text-white rounded-lg disabled:opacity-50 disabled:cursor-not-allowed hover:bg-emby-bg-elevated transition-colors text-sm font-medium"
            >
              下一页
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

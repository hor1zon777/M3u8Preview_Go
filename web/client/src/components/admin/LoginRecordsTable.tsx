import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Monitor, Smartphone, Tablet } from 'lucide-react';
import { adminApi } from '../../services/adminApi.js';

const deviceIcon: Record<string, typeof Monitor> = {
  Desktop: Monitor,
  mobile: Smartphone,
  tablet: Tablet,
};

export function LoginRecordsTable({ userId }: { userId: string }) {
  const [page, setPage] = useState(1);

  const { data, isLoading } = useQuery({
    queryKey: ['admin', 'user-login-records', userId, page],
    queryFn: () => adminApi.getUserLoginRecords(userId, page, 20),
  });

  return (
    <div className="space-y-4">
      <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-emby-border-subtle">
              <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">登录时间</th>
              <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">IP 地址</th>
              <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">浏览器</th>
              <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">操作系统</th>
              <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">设备</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              Array.from({ length: 5 }).map((_, i) => (
                <tr key={i} className="border-b border-emby-border-subtle/50">
                  <td colSpan={5} className="px-4 py-3"><div className="h-5 bg-emby-bg-input rounded animate-pulse" /></td>
                </tr>
              ))
            ) : !data?.items?.length ? (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-emby-text-muted">暂无登录记录</td>
              </tr>
            ) : (
              data.items.map((record) => {
                const DeviceIcon = deviceIcon[record.device || ''] || Monitor;
                return (
                  <tr key={record.id} className="border-b border-emby-border-subtle/50 hover:bg-emby-bg-input/30">
                    <td className="px-4 py-3 text-white text-xs">
                      {new Date(record.createdAt).toLocaleString('zh-CN')}
                    </td>
                    <td className="px-4 py-3 text-emby-text-muted text-xs font-mono">
                      {record.ip || '-'}
                    </td>
                    <td className="px-4 py-3 text-emby-text-muted text-xs">
                      {record.browser || '-'}
                    </td>
                    <td className="px-4 py-3 text-emby-text-muted text-xs">
                      {record.os || '-'}
                    </td>
                    <td className="px-4 py-3 text-emby-text-muted text-xs">
                      <span className="inline-flex items-center gap-1">
                        <DeviceIcon className="w-3.5 h-3.5" />
                        {record.device || '-'}
                      </span>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      {data && data.totalPages > 1 && (
        <div className="flex items-center justify-center gap-2">
          <button onClick={() => setPage(Math.max(1, page - 1))} disabled={page === 1} className="px-4 py-2 bg-emby-bg-input text-white rounded-lg disabled:opacity-50 hover:bg-emby-bg-elevated text-sm">上一页</button>
          <span className="text-emby-text-secondary text-sm">{page} / {data.totalPages}</span>
          <button onClick={() => setPage(Math.min(data.totalPages, page + 1))} disabled={page === data.totalPages} className="px-4 py-2 bg-emby-bg-input text-white rounded-lg disabled:opacity-50 hover:bg-emby-bg-elevated text-sm">下一页</button>
        </div>
      )}
    </div>
  );
}

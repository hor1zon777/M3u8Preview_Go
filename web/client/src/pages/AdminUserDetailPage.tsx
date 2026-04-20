import { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, LogIn, Eye, CheckCircle2, Monitor, Smartphone, Tablet, Clock } from 'lucide-react';
import { adminApi } from '../services/adminApi.js';
import { LoginRecordsTable } from '../components/admin/LoginRecordsTable.js';
import { UserWatchHistoryTable } from '../components/admin/UserWatchHistoryTable.js';

const deviceIcon: Record<string, typeof Monitor> = {
  Desktop: Monitor,
  mobile: Smartphone,
  tablet: Tablet,
};

type Tab = 'logins' | 'history';

export function AdminUserDetailPage() {
  const { userId } = useParams<{ userId: string }>();
  const [activeTab, setActiveTab] = useState<Tab>('logins');

  const { data: summary, isLoading: summaryLoading } = useQuery({
    queryKey: ['admin', 'user-activity-summary', userId],
    queryFn: () => adminApi.getUserActivitySummary(userId!),
    enabled: !!userId,
  });

  if (!userId) return null;

  const user = summary?.user;
  const LastDeviceIcon = deviceIcon[summary?.lastLogin?.device || ''] || Monitor;

  const tabs: { key: Tab; label: string; icon: typeof LogIn }[] = [
    { key: 'logins', label: '登录记录', icon: LogIn },
    { key: 'history', label: '观看历史', icon: Eye },
  ];

  return (
    <div className="space-y-6">
      {/* 页头 */}
      <div className="flex items-center gap-3">
        <Link to="/admin/users" className="text-emby-text-secondary hover:text-white">
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <div>
          <h1 className="text-2xl font-bold text-white">
            {user?.username || '用户详情'}
          </h1>
          {user && (
            <div className="flex items-center gap-2 mt-1">
              <span className={`px-2 py-0.5 rounded text-xs ${user.role === 'ADMIN' ? 'bg-amber-500/20 text-amber-400' : 'bg-blue-500/20 text-blue-400'}`}>
                {user.role}
              </span>
              <span className={`px-2 py-0.5 rounded text-xs ${user.isActive ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                {user.isActive ? '活跃' : '禁用'}
              </span>
              <span className="text-emby-text-muted text-xs">
                注册于 {new Date(user.createdAt).toLocaleDateString('zh-CN')}
              </span>
            </div>
          )}
        </div>
      </div>

      {/* 活动概览卡片 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <SummaryCard
          icon={<LogIn className="w-5 h-5 text-blue-400" />}
          label="总登录次数"
          value={summaryLoading ? '-' : String(summary?.totalLogins ?? 0)}
        />
        <SummaryCard
          icon={<Clock className="w-5 h-5 text-amber-400" />}
          label="最后登录"
          value={summaryLoading ? '-' : summary?.lastLogin ? new Date(summary.lastLogin.createdAt).toLocaleString('zh-CN') : '从未登录'}
          sub={summary?.lastLogin ? (
            <span className="inline-flex items-center gap-1 text-emby-text-muted text-xs mt-0.5">
              <LastDeviceIcon className="w-3 h-3" />
              {summary.lastLogin.browser || '-'} · {summary.lastLogin.ip || '-'}
            </span>
          ) : undefined}
        />
        <SummaryCard
          icon={<Eye className="w-5 h-5 text-green-400" />}
          label="观看数"
          value={summaryLoading ? '-' : String(summary?.totalWatched ?? 0)}
        />
        <SummaryCard
          icon={<CheckCircle2 className="w-5 h-5 text-emerald-400" />}
          label="已看完"
          value={summaryLoading ? '-' : String(summary?.totalCompleted ?? 0)}
        />
      </div>

      {/* Tab 切换 */}
      <div className="border-b border-emby-border-subtle">
        <div className="flex gap-4">
          {tabs.map(tab => (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={`flex items-center gap-1.5 px-3 py-2 text-sm border-b-2 transition-colors ${
                activeTab === tab.key
                  ? 'border-emby-green text-emby-green'
                  : 'border-transparent text-emby-text-muted hover:text-white'
              }`}
            >
              <tab.icon className="w-4 h-4" />
              {tab.label}
            </button>
          ))}
        </div>
      </div>

      {/* Tab 内容 */}
      {activeTab === 'logins' && <LoginRecordsTable userId={userId} />}
      {activeTab === 'history' && <UserWatchHistoryTable userId={userId} />}
    </div>
  );
}

function SummaryCard({ icon, label, value, sub }: {
  icon: React.ReactNode;
  label: string;
  value: string;
  sub?: React.ReactNode;
}) {
  return (
    <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg p-4">
      <div className="flex items-center gap-2 mb-2">
        {icon}
        <span className="text-emby-text-secondary text-xs">{label}</span>
      </div>
      <div className="text-white font-semibold text-lg truncate">{value}</div>
      {sub}
    </div>
  );
}

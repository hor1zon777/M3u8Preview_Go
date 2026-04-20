import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import {
  LogIn, Eye, Monitor, Smartphone, Tablet, Clock,
  TrendingUp, Film, Users, CheckCircle2, Play,
} from 'lucide-react';
import { adminApi } from '../services/adminApi.js';

const deviceIcon: Record<string, typeof Monitor> = {
  Desktop: Monitor,
  mobile: Smartphone,
  tablet: Tablet,
};

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)}秒`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}分钟`;
  const h = Math.floor(seconds / 3600);
  const m = Math.round((seconds % 3600) / 60);
  return m > 0 ? `${h}小时${m}分钟` : `${h}小时`;
}

function StatCard({
  icon,
  label,
  value,
  sub,
  color,
}: {
  icon: React.ReactNode;
  label: string;
  value: string | number;
  sub?: string;
  color: string;
}) {
  return (
    <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg p-4">
      <div className="flex items-center gap-2 mb-2">
        {icon}
        <span className="text-emby-text-secondary text-xs">{label}</span>
      </div>
      <div className="text-white font-bold text-xl truncate">{value}</div>
      {sub && <div className="text-emby-text-muted text-xs mt-0.5">{sub}</div>}
    </div>
  );
}

export function AdminActivityPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['admin', 'activity-aggregate'],
    queryFn: () => adminApi.getActivityAggregate(),
  });

  if (isLoading || !data) {
    return (
      <div className="space-y-6 animate-pulse">
        <div className="h-8 w-48 bg-emby-bg-input rounded" />
        <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="h-20 bg-emby-bg-input rounded-lg" />
          ))}
        </div>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <div className="h-64 bg-emby-bg-input rounded-lg" />
          <div className="h-64 bg-emby-bg-input rounded-lg" />
        </div>
      </div>
    );
  }

  const { loginStats, watchStats, recentLogins, topWatchedMedia, topActiveUsers, recentWatchRecords } = data;

  return (
    <div className="space-y-8">
      {/* 页面标题 */}
      <div className="flex items-center gap-3">
        <TrendingUp className="w-6 h-6 text-emby-text-secondary" />
        <h1 className="text-2xl font-bold text-white">用户行为</h1>
      </div>

      {/* 登录统计 */}
      <div>
        <h2 className="text-white font-semibold mb-3 flex items-center gap-2">
          <LogIn className="w-4 h-4 text-emby-text-muted" />
          登录统计
        </h2>
        <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
          <StatCard icon={<LogIn className="w-4 h-4 text-blue-400" />} label="总登录次数" value={loginStats.totalLogins} color="blue" />
          <StatCard icon={<Users className="w-4 h-4 text-green-400" />} label="活跃用户数" value={loginStats.uniqueUsers} color="green" />
          <StatCard icon={<Clock className="w-4 h-4 text-amber-400" />} label="今日登录" value={loginStats.todayLogins} color="amber" />
          <StatCard icon={<Clock className="w-4 h-4 text-orange-400" />} label="昨日登录" value={loginStats.yesterdayLogins} color="orange" />
          <StatCard icon={<Clock className="w-4 h-4 text-purple-400" />} label="近7天登录" value={loginStats.last7DaysLogins} color="purple" />
        </div>
      </div>

      {/* 观看统计 */}
      <div>
        <h2 className="text-white font-semibold mb-3 flex items-center gap-2">
          <Eye className="w-4 h-4 text-emby-text-muted" />
          观看统计
        </h2>
        <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
          <StatCard
            icon={<Film className="w-4 h-4 text-green-400" />}
            label="观看记录总数"
            value={watchStats.totalWatchRecords}
            color="green"
          />
          <StatCard
            icon={<CheckCircle2 className="w-4 h-4 text-emerald-400" />}
            label="已完成播放"
            value={watchStats.totalCompleted}
            color="emerald"
          />
          <StatCard
            icon={<Clock className="w-4 h-4 text-cyan-400" />}
            label="累计观看时长"
            value={formatDuration(watchStats.totalWatchTime)}
            color="cyan"
          />
        </div>
      </div>

      {/* 双列区域：活跃用户 + 热门媒体 */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* 活跃用户 Top10 */}
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg p-5">
          <h3 className="text-white font-semibold mb-4 flex items-center gap-2">
            <Users className="w-4 h-4 text-emby-text-muted" />
            活跃用户 Top10
          </h3>
          {topActiveUsers.length === 0 ? (
            <p className="text-emby-text-muted text-sm text-center py-8">暂无数据</p>
          ) : (
            <div className="space-y-2">
              {topActiveUsers.map((user, idx) => (
                <Link
                  key={user.userId}
                  to={`/admin/users/${user.userId}`}
                  className="flex items-center gap-3 p-2 rounded hover:bg-emby-bg-input/50 transition-colors group"
                >
                  <span className="text-emby-text-muted font-mono w-5 text-right text-xs">
                    {idx + 1}
                  </span>
                  <span className="text-white text-sm flex-1 truncate group-hover:text-emby-green">
                    {user.username}
                  </span>
                  <div className="flex items-center gap-3 text-emby-text-muted text-xs flex-shrink-0">
                    <span className="inline-flex items-center gap-1">
                      <LogIn className="w-3 h-3" />
                      {user.loginCount}
                    </span>
                    <span className="inline-flex items-center gap-1">
                      <Eye className="w-3 h-3" />
                      {user.watchCount}
                    </span>
                  </div>
                </Link>
              ))}
            </div>
          )}
        </div>

        {/* 热门媒体 Top10 */}
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg p-5">
          <h3 className="text-white font-semibold mb-4 flex items-center gap-2">
            <Film className="w-4 h-4 text-emby-text-muted" />
            热门媒体 Top10
          </h3>
          {topWatchedMedia.length === 0 ? (
            <p className="text-emby-text-muted text-sm text-center py-8">暂无数据</p>
          ) : (
            <div className="space-y-2">
              {topWatchedMedia.map((item, idx) => (
                <Link
                  key={item.mediaId}
                  to={`/media/${item.mediaId}`}
                  className="flex items-center gap-3 p-2 rounded hover:bg-emby-bg-input/50 transition-colors group"
                >
                  <span className="text-emby-text-muted font-mono w-5 text-right text-xs">
                    {idx + 1}
                  </span>
                  <span className="text-white text-sm flex-1 truncate group-hover:text-emby-green">
                    {item.title}
                  </span>
                  <div className="flex items-center gap-3 text-emby-text-muted text-xs flex-shrink-0">
                    <span className="inline-flex items-center gap-1">
                      <Eye className="w-3 h-3" />
                      {item.watchCount}
                    </span>
                    {item.completedCount > 0 && (
                      <CheckCircle2 className="w-3 h-3 text-green-400" />
                    )}
                  </div>
                </Link>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* 最近播放记录 */}
      <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-x-auto">
        <div className="p-5 border-b border-emby-border-subtle">
          <h3 className="text-white font-semibold flex items-center gap-2">
            <Play className="w-4 h-4 text-emby-text-muted" />
            最近播放记录
          </h3>
        </div>
        {recentWatchRecords.length === 0 ? (
          <div className="p-8 text-center text-emby-text-muted text-sm">暂无数据</div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-emby-border-subtle">
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">用户</th>
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">媒体</th>
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">进度</th>
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">状态</th>
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">更新时间</th>
              </tr>
            </thead>
            <tbody>
              {recentWatchRecords.map((record) => (
                <tr
                  key={record.id}
                  className="border-b border-emby-border-subtle/50 hover:bg-emby-bg-input/30"
                >
                  <td className="px-5 py-3">
                    <Link
                      to={`/admin/users/${record.userId}`}
                      className="text-emby-green hover:text-emby-green/80 text-sm"
                    >
                      {record.username || record.userId.slice(0, 8)}
                    </Link>
                  </td>
                  <td className="px-5 py-3">
                    <Link
                      to={`/media/${record.mediaId}`}
                      className="text-white hover:text-emby-green text-sm truncate block max-w-[200px]"
                      title={record.mediaTitle}
                    >
                      {record.mediaTitle}
                    </Link>
                  </td>
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <div className="w-20 h-1.5 bg-emby-bg-input rounded-full overflow-hidden">
                        <div
                          className={`h-full rounded-full ${record.completed ? 'bg-green-500' : 'bg-blue-500'}`}
                          style={{ width: `${Math.min(record.percentage, 100)}%` }}
                        />
                      </div>
                      <span className="text-emby-text-muted text-xs whitespace-nowrap">
                        {Math.round(record.percentage)}%
                      </span>
                    </div>
                    <div className="text-emby-text-muted text-xs mt-0.5">
                      {formatDuration(record.progress)} / {formatDuration(record.duration)}
                    </div>
                  </td>
                  <td className="px-5 py-3">
                    {record.completed ? (
                      <span className="inline-flex items-center gap-1 text-green-400 text-xs">
                        <CheckCircle2 className="w-3 h-3" />
                        已完成
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-blue-400 text-xs">
                        <Play className="w-3 h-3" />
                        观看中
                      </span>
                    )}
                  </td>
                  <td className="px-5 py-3 text-emby-text-muted text-xs">
                    {new Date(record.updatedAt).toLocaleString('zh-CN')}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* 最近登录记录 */}
      <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-x-auto">
        <div className="p-5 border-b border-emby-border-subtle">
          <h3 className="text-white font-semibold flex items-center gap-2">
            <LogIn className="w-4 h-4 text-emby-text-muted" />
            最近登录记录
          </h3>
        </div>
        {recentLogins.length === 0 ? (
          <div className="p-8 text-center text-emby-text-muted text-sm">暂无数据</div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-emby-border-subtle">
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">用户</th>
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">登录时间</th>
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">IP</th>
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">浏览器</th>
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">操作系统</th>
                <th className="px-5 py-3 text-left text-emby-text-secondary font-medium">设备</th>
              </tr>
            </thead>
            <tbody>
              {recentLogins.map((record) => {
                const DevIcon = deviceIcon[record.device || ''] || Monitor;
                return (
                  <tr
                    key={record.id}
                    className="border-b border-emby-border-subtle/50 hover:bg-emby-bg-input/30"
                  >
                    <td className="px-5 py-3">
                      <Link
                        to={`/admin/users/${record.userId}`}
                        className="text-emby-green hover:text-emby-green/80 text-sm"
                      >
                        {record.username || record.userId.slice(0, 8)}
                      </Link>
                    </td>
                    <td className="px-5 py-3 text-emby-text-muted text-xs">
                      {new Date(record.createdAt).toLocaleString('zh-CN')}
                    </td>
                    <td className="px-5 py-3 text-emby-text-muted text-xs font-mono">
                      {record.ip || '-'}
                    </td>
                    <td className="px-5 py-3 text-emby-text-muted text-xs">
                      {record.browser || '-'}
                    </td>
                    <td className="px-5 py-3 text-emby-text-muted text-xs">
                      {record.os || '-'}
                    </td>
                    <td className="px-5 py-3 text-emby-text-muted text-xs">
                      <span className="inline-flex items-center gap-1">
                        <DevIcon className="w-3.5 h-3.5" />
                        {record.device || '-'}
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
import { useState, useCallback } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Film, Users, FolderOpen, Play, Settings, Download, Shield, Activity, X, Plus } from 'lucide-react';
import { adminApi } from '../services/adminApi.js';
import { MediaThumbnail } from '../components/media/MediaThumbnail.js';
import { BackupSection } from '../components/admin/BackupSection.js';
import { PosterSection } from '../components/admin/PosterSection.js';

export function AdminDashboardPage() {
  const queryClient = useQueryClient();

  const { data: stats, isLoading } = useQuery({
    queryKey: ['admin', 'dashboard'],
    queryFn: () => adminApi.getDashboard(),
  });

  const { data: settings } = useQuery({
    queryKey: ['admin', 'settings'],
    queryFn: () => adminApi.getSettings(),
  });

  const updateSettingMutation = useMutation({
    mutationFn: ({ key, value }: { key: string; value: string }) =>
      adminApi.updateSetting(key, value),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] });
    },
    onError: () => {
      setSettingError('设置更新失败，请重试');
      setTimeout(() => setSettingError(''), 3000);
    },
  });

  const [settingError, setSettingError] = useState('');
  const [newExt, setNewExt] = useState('');
  const settingsLoaded = !!settings;
  const allowRegistration = settings?.find((s) => s.key === 'allowRegistration')?.value !== 'false';
  const enableRateLimit = settings?.find((s) => s.key === 'enableRateLimit')?.value !== 'false';
  const DEFAULT_PROXY_EXTENSIONS = '.m3u8,.ts,.m4s,.mp4,.aac,.key,.jpg,.jpeg,.png,.webp';
  const DEFAULT_EXTENSIONS = new Set(DEFAULT_PROXY_EXTENSIONS.split(','));
  const proxyAllowedExtensions = settings?.find((s) => s.key === 'proxyAllowedExtensions')?.value || DEFAULT_PROXY_EXTENSIONS;
  const extensionTags = proxyAllowedExtensions.split(',').map(s => s.trim()).filter(Boolean);

  const saveExtensions = useCallback((tags: string[]) => {
    updateSettingMutation.mutate({ key: 'proxyAllowedExtensions', value: tags.join(',') });
  }, [updateSettingMutation]);

  const handleAddExt = useCallback(() => {
    let ext = newExt.trim().toLowerCase();
    if (!ext) return;
    if (!ext.startsWith('.')) ext = `.${ext}`;
    if (!/^\.[a-zA-Z0-9]+$/.test(ext)) {
      setSettingError('扩展名格式无效，需以 . 开头且仅含字母数字');
      setTimeout(() => setSettingError(''), 3000);
      return;
    }
    if (extensionTags.includes(ext)) {
      setNewExt('');
      return;
    }
    saveExtensions([...extensionTags, ext]);
    setNewExt('');
  }, [newExt, extensionTags, saveExtensions]);

  const handleRemoveExt = useCallback((ext: string) => {
    const remaining = extensionTags.filter(t => t !== ext);
    if (remaining.length === 0) {
      setSettingError('至少需要保留一个扩展名');
      setTimeout(() => setSettingError(''), 3000);
      return;
    }
    saveExtensions(remaining);
  }, [extensionTags, saveExtensions]);

  if (isLoading) {
    return (
      <div className="space-y-6 animate-pulse">
        <div className="h-8 w-48 bg-emby-bg-input rounded" />
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="h-24 bg-emby-bg-input rounded-lg" />
          ))}
        </div>
      </div>
    );
  }

  if (!stats) return null;

  const statCards = [
    { label: '总媒体数', value: stats.totalMedia, icon: Film, color: 'text-blue-400' },
    { label: '总用户数', value: stats.totalUsers, icon: Users, color: 'text-green-400' },
    { label: '分类数', value: stats.totalCategories, icon: FolderOpen, color: 'text-purple-400' },
    { label: '总播放量', value: stats.totalViews, icon: Play, color: 'text-orange-400' },
  ];

  return (
    <div className="space-y-8">
      <div className="flex items-center gap-3">
        <Settings className="w-6 h-6 text-emby-text-secondary" />
        <h1 className="text-2xl font-bold text-white">管理面板</h1>
      </div>

      {/* Stats Cards */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {statCards.map((card) => (
          <div key={card.label} className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5">
            <div className="flex items-center justify-between">
              <card.icon className={`w-6 h-6 ${card.color}`} />
            </div>
            <p className="text-3xl font-bold text-white mt-3">{card.value.toLocaleString()}</p>
            <p className="text-sm text-emby-text-secondary mt-1">{card.label}</p>
          </div>
        ))}
      </div>

      {/* Quick Links */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <Link
          to="/admin/users"
          className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5 hover:border-emby-border-light transition-colors group"
        >
          <div className="flex items-center gap-2">
            <Users className="w-5 h-5 text-emby-text-secondary group-hover:text-emby-green-light" />
            <h3 className="text-white font-semibold group-hover:text-emby-green-light">用户管理</h3>
          </div>
          <p className="text-emby-text-muted text-sm mt-1">管理用户角色和状态</p>
        </Link>
        <Link
          to="/admin/activity"
          className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5 hover:border-emby-border-light transition-colors group"
        >
          <div className="flex items-center gap-2">
            <Activity className="w-5 h-5 text-emby-text-secondary group-hover:text-emby-green-light" />
            <h3 className="text-white font-semibold group-hover:text-emby-green-light">用户行为</h3>
          </div>
          <p className="text-emby-text-muted text-sm mt-1">查看登录记录、观看历史</p>
        </Link>
        <Link
          to="/admin/media"
          className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5 hover:border-emby-border-light transition-colors group"
        >
          <div className="flex items-center gap-2">
            <Film className="w-5 h-5 text-emby-text-secondary group-hover:text-emby-green-light" />
            <h3 className="text-white font-semibold group-hover:text-emby-green-light">媒体管理</h3>
          </div>
          <p className="text-emby-text-muted text-sm mt-1">管理所有媒体内容</p>
        </Link>
        <Link
          to="/import"
          className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5 hover:border-emby-border-light transition-colors group"
        >
          <div className="flex items-center gap-2">
            <Download className="w-5 h-5 text-emby-text-secondary group-hover:text-emby-green-light" />
            <h3 className="text-white font-semibold group-hover:text-emby-green-light">批量导入</h3>
          </div>
          <p className="text-emby-text-muted text-sm mt-1">导入M3U8链接</p>
        </Link>
      </div>

      {/* System Settings */}
      <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5">
        <div className="flex items-center gap-2 mb-4">
          <Shield className="w-5 h-5 text-emby-text-secondary" />
          <h3 className="text-white font-semibold">系统设置</h3>
        </div>
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-white text-sm">允许新用户注册</p>
              <p className="text-emby-text-muted text-xs mt-0.5">关闭后新用户将无法注册账号</p>
            </div>
            <button
              type="button"
              role="switch"
              aria-checked={allowRegistration}
              disabled={updateSettingMutation.isPending || !settingsLoaded}
              onClick={() =>
                updateSettingMutation.mutate({
                  key: 'allowRegistration',
                  value: allowRegistration ? 'false' : 'true',
                })
              }
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-emby-green focus:ring-offset-2 focus:ring-offset-emby-bg-card disabled:opacity-50 ${
                allowRegistration ? 'bg-emby-green' : 'bg-emby-bg-input'
              }`}
            >
              <span
                className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                  allowRegistration ? 'translate-x-6' : 'translate-x-1'
                }`}
              />
            </button>
          </div>
          <div className="flex items-center justify-between">
            <div>
              <p className="text-white text-sm">启用网站速率限制</p>
              <p className="text-emby-text-muted text-xs mt-0.5">
                关闭后将跳过全局、认证、代理、签名与播放量限流
              </p>
            </div>
            <button
              type="button"
              role="switch"
              aria-checked={enableRateLimit}
              disabled={updateSettingMutation.isPending || !settingsLoaded}
              onClick={() =>
                updateSettingMutation.mutate({
                  key: 'enableRateLimit',
                  value: enableRateLimit ? 'false' : 'true',
                })
              }
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-emby-green focus:ring-offset-2 focus:ring-offset-emby-bg-card disabled:opacity-50 ${
                enableRateLimit ? 'bg-emby-green' : 'bg-emby-bg-input'
              }`}
            >
              <span
                className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                  enableRateLimit ? 'translate-x-6' : 'translate-x-1'
                }`}
              />
            </button>
          </div>
          <div>
            <div className="mb-3">
              <p className="text-white text-sm">代理允许的文件扩展名</p>
              <p className="text-emby-text-muted text-xs mt-0.5">
                控制代理端点可转发的资源类型，点击标签上的 × 可移除
              </p>
            </div>
            <div className="flex flex-wrap gap-1.5 mb-3">
              {extensionTags.length === 0 ? (
                <span className="text-emby-text-muted text-xs italic">暂无配置，代理将拒绝所有资源</span>
              ) : (
                extensionTags.map(ext => {
                  const isDefault = DEFAULT_EXTENSIONS.has(ext);
                  return (
                    <span
                      key={ext}
                      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded border text-xs ${
                        isDefault
                          ? 'bg-emby-bg-input border-emby-border-subtle text-emby-text-secondary'
                          : 'bg-blue-500/10 border-blue-500/30 text-blue-400'
                      }`}
                    >
                      {ext}
                      {isDefault && (
                        <span className="text-emby-text-muted text-[10px]">默认</span>
                      )}
                      <button
                        type="button"
                        disabled={updateSettingMutation.isPending}
                        onClick={() => handleRemoveExt(ext)}
                        className="text-emby-text-muted hover:text-red-400 transition-colors disabled:opacity-50"
                        aria-label={`移除 ${ext}`}
                      >
                        <X className="w-3 h-3" />
                      </button>
                    </span>
                  );
                })
              )}
            </div>
            <div className="flex gap-2">
              <input
                type="text"
                value={newExt}
                onChange={(e) => setNewExt(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); handleAddExt(); } }}
                disabled={updateSettingMutation.isPending || !settingsLoaded}
                className="w-36 bg-emby-bg-input border border-emby-border-subtle rounded px-3 py-1.5 text-white text-sm focus:outline-none focus:ring-1 focus:ring-emby-green disabled:opacity-50"
                placeholder="输入扩展名，如 .gif"
              />
              <button
                type="button"
                disabled={updateSettingMutation.isPending || !settingsLoaded || !newExt.trim()}
                onClick={handleAddExt}
                className="inline-flex items-center gap-1 px-3 py-1.5 bg-emby-green text-white text-sm rounded hover:bg-emby-green/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <Plus className="w-3.5 h-3.5" />
                添加
              </button>
            </div>
          </div>
        </div>
        {settingError && <p className="text-red-400 text-xs mt-2">{settingError}</p>}
      </div>

      {/* Poster Management */}
      <PosterSection />

      {/* Backup */}
      <BackupSection />

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Recent Media */}
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5">
          <h3 className="text-white font-semibold mb-4">最近添加</h3>
          <div className="space-y-3">
            {stats.recentMedia.map((media: any) => (
              <Link
                key={media.id}
                to={`/media/${media.id}`}
                className="flex items-center gap-3 text-sm group"
              >
                <div className="w-16 aspect-video bg-emby-bg-input rounded flex-shrink-0 overflow-hidden">
                  <MediaThumbnail
                    mediaId={media.id}
                    m3u8Url={media.m3u8Url}
                    posterUrl={media.posterUrl}
                    title={media.title}
                    iconSize="w-4 h-4"
                  />
                </div>
                <div className="min-w-0">
                  <p className="text-white truncate group-hover:text-emby-green-light">{media.title}</p>
                  <p className="text-emby-text-muted text-xs">{media.category?.name || '未分类'}</p>
                </div>
              </Link>
            ))}
          </div>
        </div>

        {/* Top Media */}
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5">
          <h3 className="text-white font-semibold mb-4">热门内容</h3>
          <div className="space-y-3">
            {stats.topMedia.map((media: any, index: number) => (
              <Link
                key={media.id}
                to={`/media/${media.id}`}
                className="flex items-center gap-3 text-sm group"
              >
                <span className="text-emby-text-muted font-mono w-6 text-center">{index + 1}</span>
                <div className="w-16 aspect-video bg-emby-bg-input rounded flex-shrink-0 overflow-hidden">
                  <MediaThumbnail
                    mediaId={media.id}
                    m3u8Url={media.m3u8Url}
                    posterUrl={media.posterUrl}
                    title={media.title}
                    iconSize="w-4 h-4"
                  />
                </div>
                <div className="min-w-0 flex-1">
                  <p className="text-white truncate group-hover:text-emby-green-light">{media.title}</p>
                </div>
                <span className="text-emby-text-muted text-xs flex-shrink-0">{media.views} 次</span>
              </Link>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
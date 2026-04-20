import { useParams, useNavigate, Link, useLocation } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { ArrowLeft, Film, Play, Plus, Star, RotateCcw, Check, User, RefreshCw } from 'lucide-react';
import { mediaApi } from '../services/mediaApi.js';
import { historyApi } from '../services/historyApi.js';
import { FavoriteButton } from '../components/media/FavoriteButton.js';
import { MediaCard } from '../components/media/MediaCard.js';
import { AddToPlaylistModal } from '../components/playlist/AddToPlaylistModal.js';
import { ScrollRow } from '../components/ui/ScrollRow.js';
import { useVideoThumbnail } from '../hooks/useVideoThumbnail.js';
import { useProgressMap } from '../hooks/useProgressMap.js';
import { formatDate, formatDuration, buildRouteKey, saveCurrentRouteScrollPosition, setPendingScrollRestore } from '../lib/utils.js';
import { useAuth } from '../hooks/useAuth.js';

export function MediaDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const isAdmin = user?.role === 'ADMIN';
  const [showAddToPlaylist, setShowAddToPlaylist] = useState(false);
  const viewIncrementedRef = useRef(false);

  const currentRouteKey = buildRouteKey(location.pathname, location.search);
  const fromRouteKey = (location.state as { fromRouteKey?: string } | null)?.fromRouteKey;

  // 挂载时设置待恢复的滚动路由，确保返回来源页时恢复滚动位置
  useEffect(() => {
    if (fromRouteKey) {
      setPendingScrollRestore(fromRouteKey);
    }
  }, [fromRouteKey]);

  const { data: media, isLoading, error } = useQuery({
    queryKey: ['media', id],
    queryFn: () => mediaApi.getById(id!),
    enabled: !!id,
  });

  // 同类推荐
  const { data: categoryMedia } = useQuery({
    queryKey: ['media', 'category', media?.categoryId],
    queryFn: () => mediaApi.getAll({ categoryId: media!.categoryId!, limit: 12 }),
    enabled: !!media?.categoryId,
  });

  // 随机推荐
  const { data: randomMedia } = useQuery({
    queryKey: ['media', 'random-detail', id],
    queryFn: () => mediaApi.getRandom(12),
    enabled: !!media,
  });

  const { data: progressMap } = useProgressMap();

  // 查询当前媒体的观看进度
  const { data: watchProgress } = useQuery({
    queryKey: ['watchProgress', id],
    queryFn: () => historyApi.getProgress(id!),
    enabled: !!id,
  });

  // 使用缩略图 hook 获取封面
  const thumbnail = useVideoThumbnail(
    media?.id ?? '',
    media?.m3u8Url ?? '',
    media?.posterUrl,
    watchProgress?.percentage,
  );

  // Increment views
  useEffect(() => {
    if (id && !viewIncrementedRef.current) {
      viewIncrementedRef.current = true;
      mediaApi.incrementViews(id).catch(() => {});
    }
  }, [id]);

  function handlePlay() {
    saveCurrentRouteScrollPosition(currentRouteKey);
    navigate(`/play/${media!.id}`, {
      state: { restoreRouteKey: currentRouteKey },
    });
  }

  const thumbnailMutation = useMutation({
    mutationFn: () => mediaApi.regenerateThumbnail(id!),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['media', id] });
    },
  });

  if (isLoading) {
    return (
      <div className="animate-pulse">
        <div className="relative -mx-6 -mt-4 lg:-mx-8 h-[400px] bg-emby-bg-input" />
        <div className="max-w-5xl mx-auto mt-6 space-y-4">
          <div className="h-8 bg-emby-bg-input rounded w-1/3" />
          <div className="h-4 bg-emby-bg-input rounded w-2/3" />
        </div>
      </div>
    );
  }

  if (error || !media) {
    return (
      <div className="text-center py-12">
        <p className="text-emby-text-secondary mb-4">媒体不存在或已被删除</p>
        <button
          onClick={() => navigate('/')}
          className="px-4 py-2 bg-emby-green text-white rounded-md hover:bg-emby-green-dark"
        >
          返回首页
        </button>
      </div>
    );
  }

  return (
    <div>
      {/* Hero section */}
      <div className="relative -mx-6 -mt-4 lg:-mx-8 overflow-hidden">
        {/* 移动端横版封面区块 */}
        <div className="md:hidden relative">
          <div className="relative aspect-video">
            {thumbnail ? (
              <img
                src={thumbnail}
                alt={media.title}
                className="w-full h-full object-cover"
              />
            ) : (
              <div className="w-full h-full bg-gradient-to-b from-emby-bg-elevated to-emby-bg-base flex items-center justify-center">
                <Film className="w-16 h-16 text-emby-text-muted" />
              </div>
            )}
            {/* 返回按钮 */}
            <button
              onClick={() => navigate(-1)}
              className="absolute top-4 left-4 p-2 rounded-lg bg-black/40 hover:bg-black/60 text-white transition-colors z-20"
              aria-label="返回"
            >
              <ArrowLeft className="w-5 h-5" />
            </button>
            {/* 居中播放按钮 */}
            <button
              onClick={handlePlay}
              className="absolute inset-0 flex items-center justify-center z-10"
              aria-label="播放"
            >
              <div className="w-14 h-14 rounded-full bg-black/40 backdrop-blur-sm flex items-center justify-center">
                <Play className="w-7 h-7 text-white fill-white ml-0.5" />
              </div>
            </button>
            {/* 已看完标记 */}
            {watchProgress?.completed && (
              <div className="absolute top-4 right-4 bg-emby-green rounded-full p-1 z-20">
                <Check className="w-4 h-4 text-white" strokeWidth={3} />
              </div>
            )}
            {/* 观看进度条 */}
            {watchProgress && !watchProgress.completed && watchProgress.percentage > 0 && (
              <div className="absolute bottom-0 left-0 right-0 h-1 bg-black/40 z-10">
                <div className="h-full bg-emby-green" style={{ width: `${Math.min(100, watchProgress.percentage)}%` }} />
              </div>
            )}
            {/* 底部渐变过渡 */}
            <div className="absolute bottom-0 left-0 right-0 h-20 bg-gradient-to-t from-emby-bg-base to-transparent" />
          </div>
        </div>

        {/* Background layer */}
        <div className="hidden md:block absolute inset-0">
          {thumbnail ? (
            <img
              src={thumbnail}
              alt=""
              className="w-full h-full object-cover blur-2xl scale-110 opacity-30"
            />
          ) : (
            <div className="w-full h-full bg-gradient-to-b from-emby-bg-elevated to-emby-bg-base" />
          )}
          {/* Gradient overlays */}
          <div className="absolute inset-0 bg-gradient-to-t from-emby-bg-base via-emby-bg-base/60 to-transparent" />
          <div className="absolute inset-0 bg-gradient-to-r from-emby-bg-base/80 to-transparent" />
        </div>

        {/* Foreground content */}
        <div className="relative px-6 lg:px-8 py-4 md:py-10 flex gap-4 md:gap-8 items-end md:min-h-[360px]">
          {/* 返回按钮 */}
          <button
            onClick={() => navigate(-1)}
            className="hidden md:block absolute top-4 left-4 lg:left-6 p-2 rounded-lg bg-black/40 hover:bg-black/60 text-white transition-colors z-10"
            aria-label="返回"
          >
            <ArrowLeft className="w-5 h-5" />
          </button>

          {/* Poster - hidden on mobile */}
          <div className="hidden md:block w-52 flex-shrink-0">
            <div className="relative aspect-[2/3] rounded-md overflow-hidden shadow-2xl">
              {thumbnail ? (
                <img src={thumbnail} alt={media.title} className="w-full h-full object-cover" />
              ) : (
                <div className="w-full h-full bg-emby-bg-card flex items-center justify-center">
                  <Film className="w-16 h-16 text-emby-text-muted" />
                </div>
              )}
              {/* Emby 风格封面进度条 */}
              {watchProgress && !watchProgress.completed && watchProgress.percentage > 0 && (
                <div className="absolute bottom-0 left-0 right-0 h-1 bg-black/40">
                  <div className="h-full bg-emby-green" style={{ width: `${Math.min(100, watchProgress.percentage)}%` }} />
                </div>
              )}
              {/* 已看完标记 */}
              {watchProgress?.completed && (
                <div className="absolute bottom-2 right-2 bg-emby-green rounded-full p-0.5">
                  <Check className="w-3.5 h-3.5 text-white" strokeWidth={3} />
                </div>
              )}
            </div>
          </div>

          {/* Info */}
          <div className="flex-1 min-w-0 space-y-4">
            <h1 className="text-3xl font-bold text-white">{media.title}</h1>

            {/* Metadata row */}
            <div className="flex items-center gap-4 text-sm text-emby-text-secondary flex-wrap">
              {media.year && <span>{media.year}</span>}
              {media.rating && (
                <span className="flex items-center gap-1 text-yellow-400">
                  <Star className="w-4 h-4 fill-yellow-400" />
                  {media.rating.toFixed(1)}
                </span>
              )}
              {media.category && (
                <span className="px-2 py-0.5 bg-white/10 rounded text-emby-text-primary text-xs">{media.category.name}</span>
              )}
              {media.artist && (
                <Link
                  to={`/artist/${encodeURIComponent(media.artist)}`}
                  className="flex items-center gap-1 hover:text-emby-green-light transition-colors"
                >
                  <User className="w-4 h-4" />
                  {media.artist}
                </Link>
              )}
              <span>{media.views} 次播放</span>
              <span>添加于 {formatDate(media.createdAt)}</span>
            </div>

            {/* Tags */}
            {media.tags && media.tags.length > 0 && (
              <div className="flex flex-wrap gap-2">
                {media.tags.map((tag) => (
                  <span
                    key={tag.id}
                    className="px-2.5 py-1 bg-white/10 text-emby-text-primary text-xs rounded-full"
                  >
                    {tag.name}
                  </span>
                ))}
              </div>
            )}

            {/* Description */}
            {media.description && (
              <p className="text-emby-text-primary leading-relaxed line-clamp-3">{media.description}</p>
            )}

            {/* Action buttons */}
            <div className="flex items-center gap-3 pt-2">
              <button
                onClick={handlePlay}
                className="flex items-center gap-2 px-6 py-2.5 bg-emby-green text-white rounded-md hover:bg-emby-green-hover font-medium transition-colors"
              >
                {watchProgress?.completed ? (
                  <>
                    <RotateCcw className="w-5 h-5" />
                    重新播放
                  </>
                ) : watchProgress && watchProgress.progress > 0 ? (
                  <>
                    <Play className="w-5 h-5 fill-white" />
                    继续播放 {formatDuration(watchProgress.progress)}
                  </>
                ) : (
                  <>
                    <Play className="w-5 h-5 fill-white" />
                    播放
                  </>
                )}
              </button>
              <FavoriteButton mediaId={media.id} />
              <button
                onClick={() => setShowAddToPlaylist(true)}
                className="flex items-center gap-1.5 px-3 py-1.5 text-sm text-emby-text-secondary hover:text-white bg-emby-bg-input rounded-md hover:bg-emby-bg-elevated transition-colors"
              >
                <Plus className="w-4 h-4" />
                添加到列表
              </button>
              {isAdmin && (
                <button
                  onClick={() => thumbnailMutation.mutate()}
                  disabled={thumbnailMutation.isPending}
                  className="flex items-center gap-1.5 px-3 py-1.5 text-sm text-emby-text-secondary hover:text-white bg-emby-bg-input rounded-md hover:bg-emby-bg-elevated transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  <RefreshCw className={`w-4 h-4 ${thumbnailMutation.isPending ? 'animate-spin' : ''}`} />
                  {thumbnailMutation.isPending ? '生成中...' : '刷新封面'}
                </button>
              )}
            </div>
          </div>
        </div>

      </div>

      {/* 推荐内容 */}
      <div className="max-w-5xl mx-auto mt-8 space-y-8 pb-8">
        {/* 同类推荐 */}
        {categoryMedia?.items && categoryMedia.items.filter(m => m.id !== media.id).length > 0 && (
          <ScrollRow title="同类推荐">
            {categoryMedia.items
              .filter(m => m.id !== media.id)
              .map(m => {
                const prog = progressMap?.[m.id];
                return (
                  <div key={m.id} className="w-[140px] sm:w-[160px] lg:w-[170px] flex-shrink-0 snap-start">
                    <MediaCard media={m} variant="portrait" showProgress={!!prog} progress={prog?.percentage} completed={prog?.completed} />
                  </div>
                );
              })}
          </ScrollRow>
        )}

        {/* 随机推荐 */}
        {randomMedia && randomMedia.filter(m => m.id !== media.id).length > 0 && (
          <ScrollRow title="你可能喜欢">
            {randomMedia
              .filter(m => m.id !== media.id)
              .map(m => {
                const prog = progressMap?.[m.id];
                return (
                  <div key={m.id} className="w-[140px] sm:w-[160px] lg:w-[170px] flex-shrink-0 snap-start">
                    <MediaCard media={m} variant="portrait" showProgress={!!prog} progress={prog?.percentage} completed={prog?.completed} />
                  </div>
                );
              })}
          </ScrollRow>
        )}
      </div>

      {/* Add to playlist modal */}
      <AddToPlaylistModal
        mediaId={media.id}
        isOpen={showAddToPlaylist}
        onClose={() => setShowAddToPlaylist(false)}
      />
    </div>
  );
}

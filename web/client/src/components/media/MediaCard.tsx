import { Link, useNavigate, useLocation } from 'react-router-dom';
import { Film, Play, Star, Check } from 'lucide-react';
import { useVideoThumbnail } from '../../hooks/useVideoThumbnail.js';
import { buildRouteKey, saveCurrentRouteScrollPosition } from '../../lib/utils.js';
import type { Media } from '@m3u8-preview/shared';

interface MediaCardProps {
  media: Media;
  variant?: 'portrait' | 'landscape';
  showProgress?: boolean;
  progress?: number;
  progressText?: string;
  completed?: boolean;
}

export function MediaCard({ media, variant = 'portrait', showProgress = false, progress = 0, progressText, completed }: MediaCardProps) {
  const thumbnail = useVideoThumbnail(media.id, media.m3u8Url, media.posterUrl);
  const navigate = useNavigate();
  const location = useLocation();

  function handleClick(e: React.MouseEvent) {
    e.preventDefault();
    const routeKey = buildRouteKey(location.pathname, location.search);
    saveCurrentRouteScrollPosition(routeKey);
    navigate(`/media/${media.id}`, { state: { fromRouteKey: routeKey } });
  }

  if (variant === 'landscape') {
    return (
      <Link to={`/media/${media.id}`} onClick={handleClick} className="group block flex-shrink-0">
        {/* Landscape poster */}
        <div className="aspect-video bg-emby-bg-card rounded-md overflow-hidden relative">
          {thumbnail ? (
            <img
              src={thumbnail}
              alt={media.title}
              className="w-full h-full object-cover group-hover:scale-105 transition-all duration-200"
              loading="lazy"
            />
          ) : (
            <div className="w-full h-full bg-gradient-to-br from-emby-bg-elevated to-emby-bg-card flex items-center justify-center">
              <Film className="w-10 h-10 text-emby-text-muted" />
            </div>
          )}

          {/* Hover overlay */}
          <div className="absolute inset-0 bg-black/0 group-hover:bg-black/50 transition-all duration-200 flex items-center justify-center">
            <div className="opacity-0 group-hover:opacity-100 transition-opacity duration-200">
              <div className="w-12 h-12 bg-emby-green/90 rounded-full flex items-center justify-center">
                <Play className="w-6 h-6 text-white fill-white ml-0.5" />
              </div>
            </div>
          </div>

          {/* Progress bar */}
          {showProgress && (
            <div className="absolute bottom-0 left-0 right-0 h-1 bg-emby-border">
              <div className="h-full bg-emby-green" style={{ width: `${Math.min(100, progress)}%` }} />
            </div>
          )}
        </div>

        {/* Title area */}
        <div className="pt-2 px-0.5">
          <p className="text-sm text-white truncate group-hover:text-emby-green-light transition-colors">{media.title}</p>
          {progressText && <p className="text-xs text-emby-text-muted mt-0.5">{progressText}</p>}
        </div>
      </Link>
    );
  }

  // Portrait mode (default)
  return (
    <Link to={`/media/${media.id}`} onClick={handleClick} className="group block">
      {/* Portrait poster */}
      <div className="aspect-[2/3] bg-emby-bg-card rounded-md overflow-hidden relative">
        {thumbnail ? (
          <img
            src={thumbnail}
            alt={media.title}
            className="w-full h-full object-cover group-hover:scale-105 transition-all duration-200"
            loading="lazy"
          />
        ) : (
          <div className="w-full h-full bg-gradient-to-br from-emby-bg-elevated to-emby-bg-card flex items-center justify-center">
            <Film className="w-12 h-12 text-emby-text-muted" />
          </div>
        )}

        {/* Hover overlay */}
        <div className="absolute inset-0 bg-black/0 group-hover:bg-black/50 transition-all duration-200 flex items-center justify-center">
          <div className="opacity-0 group-hover:opacity-100 transition-opacity duration-200">
            <div className="w-12 h-12 bg-emby-green/90 rounded-full flex items-center justify-center">
              <Play className="w-6 h-6 text-white fill-white ml-0.5" />
            </div>
          </div>
        </div>

        {/* Year badge - bottom left */}
        {media.year && (
          <span className="absolute bottom-2 left-2 bg-black/60 text-white text-xs px-2 py-0.5 rounded">
            {media.year}
          </span>
        )}

        {/* Rating badge - bottom right (hidden when completed to avoid overlap) */}
        {!completed && media.rating && (
          <span className="absolute bottom-2 right-2 bg-black/60 text-yellow-400 text-xs px-2 py-0.5 rounded font-medium flex items-center gap-0.5">
            <Star className="w-3 h-3 fill-yellow-400" />
            {media.rating.toFixed(1)}
          </span>
        )}

        {/* 观看进度条 */}
        {showProgress && !completed && progress > 0 && (
          <div className="absolute bottom-0 left-0 right-0 h-1 bg-black/40 z-[1]">
            <div className="h-full bg-emby-green" style={{ width: `${Math.min(100, progress)}%` }} />
          </div>
        )}

        {/* 已看完标记 */}
        {completed && (
          <div className="absolute bottom-2 right-2 bg-emby-green rounded-full p-0.5 z-[1]">
            <Check className="w-3.5 h-3.5 text-white" strokeWidth={3} />
          </div>
        )}
      </div>

      {/* Title area - no background wrapper */}
      <div className="pt-2 px-0.5">
        <h3 className="text-white font-medium text-sm truncate group-hover:text-emby-green-light transition-colors">
          {media.title}
        </h3>
        {media.category && (
          <p className="text-emby-text-muted text-xs mt-0.5">{media.category.name}</p>
        )}
      </div>
    </Link>
  );
}

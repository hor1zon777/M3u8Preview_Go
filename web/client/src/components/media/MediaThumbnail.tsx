import { Film } from 'lucide-react';
import { useVideoThumbnail } from '../../hooks/useVideoThumbnail.js';

interface MediaThumbnailProps {
  mediaId: string;
  m3u8Url: string;
  posterUrl?: string | null;
  title?: string;
  watchedPercentage?: number;
  /** Tailwind size classes for the fallback Film icon, e.g. "w-8 h-8" */
  iconSize?: string;
}

/**
 * 封面缩略图组件：复用 useVideoThumbnail hook，
 * 在 posterUrl 缺失时自动从 m3u8 流提取帧。
 */
export function MediaThumbnail({
  mediaId,
  m3u8Url,
  posterUrl,
  title,
  watchedPercentage,
  iconSize = 'w-8 h-8',
}: MediaThumbnailProps) {
  const thumbnail = useVideoThumbnail(mediaId, m3u8Url, posterUrl, watchedPercentage);

  if (thumbnail) {
    return <img src={thumbnail} alt={title} className="w-full h-full object-cover" />;
  }

  return (
    <div className="w-full h-full flex items-center justify-center">
      <Film className={`${iconSize} text-emby-text-muted`} />
    </div>
  );
}

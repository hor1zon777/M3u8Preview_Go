import { Film } from 'lucide-react';

interface MediaThumbnailProps {
  posterUrl?: string | null;
  title?: string;
  iconSize?: string;
}

export function MediaThumbnail({
  posterUrl,
  title,
  iconSize = 'w-8 h-8',
}: MediaThumbnailProps) {
  if (posterUrl) {
    return <img src={posterUrl} alt={title} className="w-full h-full object-cover" />;
  }

  return (
    <div className="w-full h-full flex items-center justify-center">
      <Film className={`${iconSize} text-emby-text-muted`} />
    </div>
  );
}

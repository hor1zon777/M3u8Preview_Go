import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { User, Film } from 'lucide-react';
import { mediaApi } from '../services/mediaApi.js';

export function ArtistsPage() {
  const { data: artists, isLoading } = useQuery({
    queryKey: ['artists'],
    queryFn: () => mediaApi.getArtists(),
  });

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="h-8 bg-emby-bg-input rounded w-32 animate-pulse" />
        <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
          {Array.from({ length: 12 }).map((_, i) => (
            <div key={i} className="flex flex-col items-center gap-3 p-4 animate-pulse">
              <div className="w-20 h-20 rounded-full bg-emby-bg-input" />
              <div className="h-4 bg-emby-bg-input rounded w-16" />
            </div>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <User className="w-6 h-6 text-emby-green" />
        <h1 className="text-2xl font-bold text-white">作者</h1>
        {artists && (
          <span className="text-sm text-emby-text-secondary">({artists.length})</span>
        )}
      </div>

      {!artists || artists.length === 0 ? (
        <div className="text-center py-12 text-emby-text-muted">暂无作者信息</div>
      ) : (
        <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
          {artists.map((artist) => (
            <Link
              key={artist.name}
              to={`/artist/${encodeURIComponent(artist.name)}`}
              className="group flex flex-col items-center gap-3 p-4 rounded-xl hover:bg-emby-bg-elevated transition-colors"
            >
              <div className="w-20 h-20 rounded-full bg-emby-bg-card border-2 border-emby-border group-hover:border-emby-green flex items-center justify-center transition-colors">
                <User className="w-10 h-10 text-emby-text-secondary group-hover:text-emby-green transition-colors" />
              </div>
              <div className="text-center min-w-0 w-full">
                <p className="text-sm font-medium text-emby-text-primary group-hover:text-white truncate transition-colors">
                  {artist.name}
                </p>
                <p className="text-xs text-emby-text-secondary flex items-center justify-center gap-1 mt-0.5">
                  <Film className="w-3 h-3" />
                  {artist.videoCount} 部作品
                </p>
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}

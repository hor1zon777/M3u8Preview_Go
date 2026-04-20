import { useQuery } from '@tanstack/react-query';
import { mediaApi } from '../services/mediaApi.js';
import { historyApi } from '../services/historyApi.js';
import { ScrollRow } from '../components/ui/ScrollRow.js';
import { MediaCard } from '../components/media/MediaCard.js';
import { useProgressMap } from '../hooks/useProgressMap.js';
import { formatDuration } from '../lib/utils.js';
import type { WatchHistory } from '@m3u8-preview/shared';

export function HomePage() {
  const { data: recentMedia, isLoading: recentLoading } = useQuery({
    queryKey: ['media', 'recent'],
    queryFn: () => mediaApi.getRecent(20),
  });

  const { data: randomMedia } = useQuery({
    queryKey: ['media', 'random'],
    queryFn: () => mediaApi.getRandom(15),
  });

  const { data: continueWatching } = useQuery({
    queryKey: ['history', 'continue'],
    queryFn: () => historyApi.getContinueWatching(15),
  });

  const { data: progressMap } = useProgressMap();

  if (recentLoading) {
    return (
      <div className="space-y-8 animate-pulse">
        <div className="h-6 w-32 bg-emby-bg-input rounded" />
        <div className="flex gap-4 overflow-hidden">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="w-[160px] flex-shrink-0">
              <div className="aspect-[2/3] bg-emby-bg-input rounded-md" />
              <div className="h-4 bg-emby-bg-input rounded mt-2 w-3/4" />
            </div>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-8">
      {/* Continue Watching - landscape cards */}
      {continueWatching && continueWatching.length > 0 && (
        <ScrollRow title="继续观看">
          {continueWatching.map((item: WatchHistory) => (
            item.media && (
              <div key={item.id} className="w-[280px] sm:w-[320px] flex-shrink-0 snap-start">
                <MediaCard
                  media={item.media}
                  variant="landscape"
                  showProgress
                  progress={item.percentage}
                  progressText={`${formatDuration(item.progress)} / ${formatDuration(item.duration)}`}
                />
              </div>
            )
          ))}
        </ScrollRow>
      )}

      {/* Random - portrait cards */}
      {randomMedia && randomMedia.length > 0 && (
        <ScrollRow title="随机推荐" moreLink="/library">
          {randomMedia.map((media) => {
            const prog = progressMap?.[media.id];
            return (
              <div key={media.id} className="w-[140px] sm:w-[160px] lg:w-[170px] flex-shrink-0 snap-start">
                <MediaCard media={media} variant="portrait" showProgress={!!prog} progress={prog?.percentage} completed={prog?.completed} />
              </div>
            );
          })}
        </ScrollRow>
      )}

      {/* Recent - portrait cards */}
      {recentMedia && recentMedia.length > 0 && (
        <ScrollRow title="最近添加" moreLink="/library">
          {recentMedia.map((media) => {
            const prog = progressMap?.[media.id];
            return (
              <div key={media.id} className="w-[140px] sm:w-[160px] lg:w-[170px] flex-shrink-0 snap-start">
                <MediaCard media={media} variant="portrait" showProgress={!!prog} progress={prog?.percentage} completed={prog?.completed} />
              </div>
            );
          })}
        </ScrollRow>
      )}
    </div>
  );
}

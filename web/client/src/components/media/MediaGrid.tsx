import type { Media } from '@m3u8-preview/shared';
import { MediaCard } from './MediaCard.js';

interface MediaGridProps {
  items: Media[];
  title?: string;
  emptyMessage?: string;
  progressMap?: Record<string, { percentage: number; completed: boolean }>;
}

export function MediaGrid({ items, title, emptyMessage = '暂无内容', progressMap }: MediaGridProps) {
  return (
    <div>
      {title && (
        <h2 className="text-xl font-semibold text-white mb-4">{title}</h2>
      )}
      {items.length === 0 ? (
        <div className="text-center py-12 text-emby-text-muted">{emptyMessage}</div>
      ) : (
        <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-x-4 gap-y-6">
          {items.map((item) => {
            const prog = progressMap?.[item.id];
            return (
              <MediaCard
                key={item.id}
                media={item}
                showProgress={!!prog}
                progress={prog?.percentage}
                completed={prog?.completed}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}

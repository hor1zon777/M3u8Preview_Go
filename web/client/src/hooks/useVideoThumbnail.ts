export function useVideoThumbnail(
  _mediaId: string,
  _m3u8Url: string,
  posterUrl?: string | null,
  _watchedPercentage?: number,
  _enabled?: boolean,
): string | null {
  return posterUrl || null;
}

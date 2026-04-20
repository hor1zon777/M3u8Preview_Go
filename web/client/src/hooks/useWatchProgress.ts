import { useEffect, useRef, useCallback } from 'react';
import { historyApi } from '../services/historyApi.js';

interface UseWatchProgressOptions {
  mediaId: string;
  throttleMs?: number;
}

export function useWatchProgress({ mediaId, throttleMs = 15000 }: UseWatchProgressOptions) {
  const lastSyncRef = useRef(0);
  const progressRef = useRef({ progress: 0, duration: 0 });

  const syncProgress = useCallback(async () => {
    if (!mediaId) return;
    const { progress, duration } = progressRef.current;
    if (progress <= 0 || duration <= 0) return;

    try {
      await historyApi.updateProgress({ mediaId, progress, duration });
    } catch {
      // Silently fail - will retry next time
    }
  }, [mediaId]);

  const handleTimeUpdate = useCallback((currentTime: number, duration: number) => {
    progressRef.current = { progress: currentTime, duration };

    const now = Date.now();
    if (now - lastSyncRef.current >= throttleMs) {
      lastSyncRef.current = now;
      syncProgress();
    }
  }, [throttleMs, syncProgress]);

  // Sync on visibility change and page unload
  useEffect(() => {
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'hidden' && mediaId) {
        const { progress, duration } = progressRef.current;
        if (progress > 0 && duration > 0) {
          historyApi.sendBeacon({ mediaId, progress, duration });
        }
      }
    };

    const handleBeforeUnload = () => {
      if (!mediaId) return;
      const { progress, duration } = progressRef.current;
      if (progress > 0 && duration > 0) {
        historyApi.sendBeacon({ mediaId, progress, duration });
      }
    };

    document.addEventListener('visibilitychange', handleVisibilityChange);
    window.addEventListener('beforeunload', handleBeforeUnload);

    return () => {
      // Final sync on unmount — 使用 sendBeacon 保证可靠送达
      if (mediaId) {
        const { progress, duration } = progressRef.current;
        if (progress > 0 && duration > 0) {
          historyApi.sendBeacon({ mediaId, progress, duration });
        }
      }
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      window.removeEventListener('beforeunload', handleBeforeUnload);
    };
  }, [mediaId]);

  return { handleTimeUpdate };
}

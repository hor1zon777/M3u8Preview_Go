import { useParams, useNavigate, useLocation } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useState, useEffect, useRef, useCallback, type PointerEvent as ReactPointerEvent } from 'react';
import { ArrowLeft } from 'lucide-react';
import { mediaApi } from '../services/mediaApi.js';
import { historyApi } from '../services/historyApi.js';
import { VideoPlayer } from '../components/player/VideoPlayer.js';
import { PlayerControls } from '../components/player/PlayerControls.js';
import { QualitySelector } from '../components/media/QualitySelector.js';
import { useWatchProgress } from '../hooks/useWatchProgress.js';
import { setPendingScrollRestore } from '../lib/utils.js';

const OVERLAY_TIMEOUT = 3000;

export function PlaybackPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const queryClient = useQueryClient();
  const [overlayVisible, setOverlayVisible] = useState(true);
  const [rotation, setRotation] = useState<0 | 90 | 180 | 270>(0);
  const hideTimerRef = useRef<ReturnType<typeof setTimeout>>();
  const lastPointerTypeRef = useRef<'mouse' | 'touch' | 'pen'>('mouse');
  const videoRef = useRef<HTMLVideoElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const isDraggingRef = useRef(false);

  const { data: media, isLoading, error } = useQuery({
    queryKey: ['media', id],
    queryFn: () => mediaApi.getById(id!),
    enabled: !!id,
  });

  const { data: progress, isFetched: progressFetched } = useQuery({
    queryKey: ['watchProgress', id],
    queryFn: () => historyApi.getProgress(id!),
    enabled: !!id,
  });

  const { handleTimeUpdate } = useWatchProgress({ mediaId: id ?? '' });

  const restoreRouteKey = (location.state as { restoreRouteKey?: string } | null)?.restoreRouteKey;

  const restorePreviousPage = useCallback(() => {
    if (restoreRouteKey) {
      setPendingScrollRestore(restoreRouteKey);
    }
    navigate(-1);
  }, [navigate, restoreRouteKey]);

  // 重置自动隐藏定时器
  const resetHideTimer = useCallback(() => {
    if (hideTimerRef.current) {
      clearTimeout(hideTimerRef.current);
    }
    hideTimerRef.current = setTimeout(() => {
      setOverlayVisible(false);
    }, OVERLAY_TIMEOUT);
  }, []);

  // 拖拽进度条时暂停自动隐藏
  const handleDragStateChange = useCallback((dragging: boolean) => {
    isDraggingRef.current = dragging;
    if (dragging) {
      if (hideTimerRef.current) clearTimeout(hideTimerRef.current);
    } else {
      resetHideTimer();
    }
  }, [resetHideTimer]);

  // 触摸设备：点击视频区域切换显示/隐藏
  const toggleOverlay = useCallback(() => {
    setOverlayVisible((prev) => {
      const next = !prev;
      if (next) {
        // 显示后启动自动隐藏
        if (hideTimerRef.current) clearTimeout(hideTimerRef.current);
        hideTimerRef.current = setTimeout(() => {
          setOverlayVisible(false);
        }, OVERLAY_TIMEOUT);
      } else {
        if (hideTimerRef.current) clearTimeout(hideTimerRef.current);
      }
      return next;
    });
  }, []);

  const handleSurfaceClick = useCallback(() => {
    if (lastPointerTypeRef.current !== 'mouse') {
      toggleOverlay();
      return;
    }

    const video = videoRef.current;
    if (!video) return;

    setOverlayVisible(true);

    if (video.paused) {
      void video.play().catch(() => undefined);
      resetHideTimer();
      return;
    }

    video.pause();
    if (hideTimerRef.current) {
      clearTimeout(hideTimerRef.current);
    }
  }, [resetHideTimer, toggleOverlay]);

  // 桌面端：鼠标移动时显示 + 重置自动隐藏
  const handleMouseMove = useCallback(() => {
    lastPointerTypeRef.current = 'mouse';
    setOverlayVisible(true);
    // 拖拽进度条期间不重置定时器，避免鼠标移动反复重置
    if (!isDraggingRef.current) {
      resetHideTimer();
    }
  }, [resetHideTimer]);

  const handlePointerDown = useCallback((e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.pointerType === 'mouse' || e.pointerType === 'touch' || e.pointerType === 'pen') {
      lastPointerTypeRef.current = e.pointerType;
    }
  }, []);

  // 初始化：启动自动隐藏定时器
  useEffect(() => {
    resetHideTimer();
    return () => {
      if (hideTimerRef.current) {
        clearTimeout(hideTimerRef.current);
      }
    };
  }, [resetHideTimer]);

  // 返回时刷新进度数据，确保详情页和首页都能看到最新进度
  const cleanupTimerRef = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => {
    return () => {
      if (cleanupTimerRef.current) clearTimeout(cleanupTimerRef.current);
      cleanupTimerRef.current = setTimeout(() => {
        queryClient.invalidateQueries({ queryKey: ['watchProgress', id] });
        queryClient.invalidateQueries({ queryKey: ['progressMap'] });
        queryClient.invalidateQueries({ queryKey: ['history', 'continue'] });
        queryClient.invalidateQueries({ queryKey: ['history'] });
      }, 500);
    };
  }, [id, queryClient]);

  // 键盘快捷键
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.preventDefault();
        // 如果在全屏中，先退出全屏
        if (document.fullscreenElement) {
          document.exitFullscreen();
        } else {
          restorePreviousPage();
        }
      } else if (e.key === 'f') {
        e.preventDefault();
        if (document.fullscreenElement) {
          document.exitFullscreen();
        } else if (containerRef.current?.requestFullscreen) {
          containerRef.current.requestFullscreen();
        } else {
          // iOS Safari 回退
          const video = videoRef.current as HTMLVideoElement & { webkitEnterFullscreen?: () => void };
          if (video?.webkitEnterFullscreen) {
            video.webkitEnterFullscreen();
          }
        }
      }
    }
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [restorePreviousPage]);

  function handleBack() {
    restorePreviousPage();
  }

  if (isLoading) {
    return (
      <div className="fixed inset-0 bg-black z-50 flex items-center justify-center">
        <div className="animate-spin rounded-full h-10 w-10 border-t-2 border-b-2 border-emby-green"></div>
      </div>
    );
  }

  if (error || !media) {
    return (
      <div className="fixed inset-0 bg-black z-50 flex items-center justify-center">
        <div className="text-center">
          <p className="text-emby-text-secondary mb-4">媒体不存在或已被删除</p>
          <button
            onClick={() => navigate('/')}
            className="px-4 py-2 bg-emby-green text-white rounded-md hover:bg-emby-green-dark"
          >
            返回首页
          </button>
        </div>
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      className="fixed inset-0 bg-black z-50"
      style={{ cursor: overlayVisible ? 'default' : 'none' }}
    >
      {/* 全屏播放器 */}
      {progressFetched ? (
        <VideoPlayer
          ref={videoRef}
          media={media}
          startTime={progress?.progress || 0}
          onTimeUpdate={handleTimeUpdate}
          autoPlay
          fillContainer
          controls={false}
          rotation={rotation}
        />
      ) : (
        <div className="w-full h-full flex items-center justify-center">
          <div className="animate-spin rounded-full h-10 w-10 border-t-2 border-b-2 border-emby-green" />
        </div>
      )}

      {/* 点击区域：PC 切换播放，触摸设备切换覆盖层 */}
      <div
        className="absolute inset-0 z-[5]"
        onPointerDown={handlePointerDown}
        onPointerMove={(e) => {
          if (e.pointerType === 'mouse') {
            handleMouseMove();
          }
        }}
        onClick={handleSurfaceClick}
      />

      {/* 顶部覆盖栏 */}
      <div
        className={`absolute top-0 left-0 right-0 z-10 bg-gradient-to-b from-black/70 to-transparent px-4 py-3 flex items-center gap-4 transition-opacity duration-300 ${
          overlayVisible ? 'opacity-100' : 'opacity-0 pointer-events-none'
        }`}
        onClick={(e) => e.stopPropagation()}
      >
        <button
          onClick={handleBack}
          className="p-2 rounded-full hover:bg-white/10 transition-colors text-white"
          aria-label="返回"
        >
          <ArrowLeft className="w-6 h-6" />
        </button>
        <h1 className="text-white font-medium text-lg truncate flex-1">{media.title}</h1>
        <QualitySelector dropDirection="down" />
      </div>

      {/* 底部控制栏 */}
      <div
        className={`absolute bottom-0 left-0 right-0 z-10 bg-gradient-to-t from-black/70 to-transparent transition-opacity duration-300 ${
          overlayVisible ? 'opacity-100' : 'opacity-0 pointer-events-none'
        }`}
      >
        <PlayerControls
          videoRef={videoRef}
          containerRef={containerRef}
          onDragStateChange={handleDragStateChange}
          rotation={rotation}
          onRotate={() => setRotation((r) => ((r + 90) % 360) as 0 | 90 | 180 | 270)}
        />
      </div>
    </div>
  );
}

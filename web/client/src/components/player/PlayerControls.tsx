import { useState, useRef, useCallback, useEffect, type ChangeEvent, type RefObject } from 'react';
import { Play, Pause, Maximize, Minimize, Loader2, Volume1, Volume2, VolumeX, RotateCw } from 'lucide-react';
import { usePlayerStore } from '../../stores/playerStore.js';
import { formatDuration } from '../../lib/utils.js';

const PLAYBACK_RATES = [0.5, 0.75, 1, 1.25, 1.5, 2];

interface PlayerControlsProps {
  videoRef: RefObject<HTMLVideoElement>;
  containerRef: RefObject<HTMLDivElement>;
  onDragStateChange?: (dragging: boolean) => void;
  rotation?: 0 | 90 | 180 | 270;
  onRotate?: () => void;
}

export function PlayerControls({ videoRef, containerRef, onDragStateChange, rotation = 0, onRotate }: PlayerControlsProps) {
  const {
    isPlaying,
    isBuffering,
    currentTime,
    duration,
    isFullscreen,
    volume,
    isMuted,
    setFullscreen,
    setCurrentTime,
  } = usePlayerStore();

  const [isDragging, setIsDragging] = useState(false);
  const [showRateMenu, setShowRateMenu] = useState(false);
  const [playbackRate, setPlaybackRate] = useState(1);
  const lastVolumeRef = useRef(1);
  const progressBarRef = useRef<HTMLDivElement>(null);
  const rateMenuRef = useRef<HTMLDivElement>(null);

  // 拖拽性能优化：DOM ref + 缓存 rect
  const progressFillRef = useRef<HTMLDivElement>(null);
  const progressHandleRef = useRef<HTMLDivElement>(null);
  const timeDisplayRef = useRef<HTMLSpanElement>(null);
  const dragRectRef = useRef<DOMRect | null>(null);
  const dragTimeRef = useRef(0);
  const rafIdRef = useRef(0);

  // 计算进度条位置对应的时间（非拖拽时使用，如点击跳转）
  const getTimeFromPosition = useCallback((clientX: number) => {
    const bar = progressBarRef.current;
    if (!bar || !duration) return 0;
    const rect = bar.getBoundingClientRect();
    const ratio = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
    return ratio * duration;
  }, [duration]);

  // 拖拽期间使用缓存 rect 的快速计算，避免 reflow
  const getTimeFromPositionFast = (clientX: number) => {
    const rect = dragRectRef.current;
    if (!rect || !duration) return 0;
    const ratio = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
    return ratio * duration;
  };

  // 直接 DOM 更新，绕过 React 渲染管线
  const updateProgressDOM = (time: number) => {
    const percent = duration > 0 ? (time / duration) * 100 : 0;
    if (progressFillRef.current) {
      progressFillRef.current.style.transition = 'none';
      progressFillRef.current.style.width = `${percent}%`;
    }
    if (progressHandleRef.current) {
      progressHandleRef.current.style.transition = 'none';
      progressHandleRef.current.style.left = `${percent}%`;
      progressHandleRef.current.style.transform = 'translate(-50%, -50%) scale(1.5)';
    }
    if (timeDisplayRef.current) timeDisplayRef.current.textContent = `${formatDuration(time)} / ${formatDuration(duration)}`;
  };

  // 拖拽结束时清除 inline style，恢复 CSS class 的 transition
  const restoreProgressTransition = () => {
    if (progressFillRef.current) progressFillRef.current.style.transition = '';
    if (progressHandleRef.current) {
      progressHandleRef.current.style.transition = '';
      progressHandleRef.current.style.transform = 'translate(-50%, -50%)';
    }
  };

  // 鼠标拖拽进度条（优化：缓存 rect + RAF + 直接 DOM 更新）
  const handleProgressMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    // 缓存 rect，整个拖拽过程复用
    dragRectRef.current = progressBarRef.current!.getBoundingClientRect();
    const time = getTimeFromPositionFast(e.clientX);
    setIsDragging(true);
    dragTimeRef.current = time;
    updateProgressDOM(time);
    onDragStateChange?.(true);

    const handleMouseMove = (e: MouseEvent) => {
      const t = getTimeFromPositionFast(e.clientX);
      dragTimeRef.current = t;
      // RAF 批量更新，避免每帧多次 DOM 操作
      cancelAnimationFrame(rafIdRef.current);
      rafIdRef.current = requestAnimationFrame(() => updateProgressDOM(t));
    };
    const handleMouseUp = (e: MouseEvent) => {
      cancelAnimationFrame(rafIdRef.current);
      const finalTime = getTimeFromPositionFast(e.clientX);
      if (videoRef.current) videoRef.current.currentTime = finalTime;
      setCurrentTime(finalTime);
      restoreProgressTransition();
      setIsDragging(false);
      dragRectRef.current = null;
      onDragStateChange?.(false);
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
  }, [duration, videoRef, onDragStateChange]);

  // 触摸拖拽进度条（优化：缓存 rect + RAF + 直接 DOM 更新）
  const handleProgressTouchStart = useCallback((e: React.TouchEvent) => {
    const touch = e.touches[0];
    // 缓存 rect，整个拖拽过程复用
    dragRectRef.current = progressBarRef.current!.getBoundingClientRect();
    const time = getTimeFromPositionFast(touch.clientX);
    setIsDragging(true);
    dragTimeRef.current = time;
    updateProgressDOM(time);
    onDragStateChange?.(true);

    const handleTouchMove = (e: TouchEvent) => {
      e.preventDefault(); // 阻止页面滚动
      const t = getTimeFromPositionFast(e.touches[0].clientX);
      dragTimeRef.current = t;
      cancelAnimationFrame(rafIdRef.current);
      rafIdRef.current = requestAnimationFrame(() => updateProgressDOM(t));
    };
    const handleTouchEnd = (e: TouchEvent) => {
      cancelAnimationFrame(rafIdRef.current);
      const finalTouch = e.changedTouches[0];
      const finalTime = getTimeFromPositionFast(finalTouch.clientX);
      if (videoRef.current) videoRef.current.currentTime = finalTime;
      setCurrentTime(finalTime);
      restoreProgressTransition();
      setIsDragging(false);
      dragRectRef.current = null;
      onDragStateChange?.(false);
      document.removeEventListener('touchmove', handleTouchMove);
      document.removeEventListener('touchend', handleTouchEnd);
    };
    document.addEventListener('touchmove', handleTouchMove, { passive: false });
    document.addEventListener('touchend', handleTouchEnd);
  }, [duration, videoRef, onDragStateChange]);

  // 播放/暂停
  const togglePlay = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) {
      video.play();
    } else {
      video.pause();
    }
  }, [videoRef]);

  // 全屏切换
  const toggleFullscreen = useCallback(() => {
    if (document.fullscreenElement) {
      document.exitFullscreen();
    } else if (containerRef.current?.requestFullscreen) {
      containerRef.current.requestFullscreen();
    } else {
      // iOS Safari 不支持 Fullscreen API，回退到 video 原生全屏
      const video = videoRef.current as HTMLVideoElement & { webkitEnterFullscreen?: () => void };
      if (video?.webkitEnterFullscreen) {
        video.webkitEnterFullscreen();
      }
    }
  }, [containerRef, videoRef]);

  // 监听全屏变化
  useEffect(() => {
    const handleFullscreenChange = () => {
      setFullscreen(!!document.fullscreenElement);
    };
    document.addEventListener('fullscreenchange', handleFullscreenChange);
    return () => document.removeEventListener('fullscreenchange', handleFullscreenChange);
  }, [setFullscreen]);

  // 倍速切换
  const handleRateChange = useCallback((rate: number) => {
    if (videoRef.current) {
      videoRef.current.playbackRate = rate;
    }
    setPlaybackRate(rate);
    setShowRateMenu(false);
  }, [videoRef]);

  const handleVolumeChange = useCallback((e: ChangeEvent<HTMLInputElement>) => {
    const video = videoRef.current;
    if (!video) return;

    const nextVolume = Number(e.target.value);
    video.volume = nextVolume;
    video.muted = nextVolume === 0;

    if (nextVolume > 0) {
      lastVolumeRef.current = nextVolume;
    }
  }, [videoRef]);

  const toggleMute = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;

    if (video.muted || video.volume === 0) {
      const restoredVolume = lastVolumeRef.current > 0 ? lastVolumeRef.current : 1;
      video.volume = restoredVolume;
      video.muted = false;
    } else {
      if (video.volume > 0) {
        lastVolumeRef.current = video.volume;
      }
      video.muted = true;
    }
  }, [videoRef]);

  useEffect(() => {
    if (volume > 0) {
      lastVolumeRef.current = volume;
    }
  }, [volume]);

  // 点击外部关闭倍速菜单
  useEffect(() => {
    if (!showRateMenu) return;
    const handleClick = (e: MouseEvent) => {
      if (rateMenuRef.current && !rateMenuRef.current.contains(e.target as Node)) {
        setShowRateMenu(false);
      }
    };
    document.addEventListener('click', handleClick, true);
    return () => document.removeEventListener('click', handleClick, true);
  }, [showRateMenu]);

  // 缓冲区域
  const buffered = videoRef.current?.buffered;
  const bufferedRanges: Array<{ start: number; end: number }> = [];
  if (buffered && duration > 0) {
    for (let i = 0; i < buffered.length; i++) {
      bufferedRanges.push({
        start: buffered.start(i) / duration,
        end: buffered.end(i) / duration,
      });
    }
  }

  const displayTime = isDragging ? dragTimeRef.current : currentTime;
  const progressPercent = duration > 0 ? (displayTime / duration) * 100 : 0;
  const displayVolume = isMuted ? 0 : volume;
  const volumePercent = Math.round(displayVolume * 100);
  const VolumeIcon = isMuted || volume === 0 ? VolumeX : volume < 0.5 ? Volume1 : Volume2;

  return (
    <div className="w-full px-3 pb-2 pt-1" onClick={(e) => e.stopPropagation()}>
      {/* 进度条 */}
      <div
        ref={progressBarRef}
        className="group h-6 flex items-center cursor-pointer touch-none"
        onMouseDown={handleProgressMouseDown}
        onTouchStart={handleProgressTouchStart}
      >
        <div className="relative w-full h-1 group-hover:h-1.5 bg-white/20 rounded-full transition-all">
          {/* 缓冲区域 */}
          {bufferedRanges.map((range, i) => (
            <div
              key={i}
              className="absolute top-0 h-full bg-white/30 rounded-full"
              style={{ left: `${range.start * 100}%`, width: `${(range.end - range.start) * 100}%` }}
            />
          ))}
          {/* 已播放区域 */}
          <div
            ref={progressFillRef}
            className="absolute top-0 h-full bg-emby-green rounded-full transition-[width] duration-150 ease-out"
            style={{ width: `${progressPercent}%` }}
          />
          {/* 拖拽手柄 */}
          <div
            ref={progressHandleRef}
            className="absolute top-1/2 -translate-y-1/2 w-3 h-3 bg-emby-green rounded-full opacity-0 group-hover:opacity-100 transition-[left,opacity,transform] duration-150 ease-out shadow"
            style={{ left: `${progressPercent}%`, transform: `translate(-50%, -50%)` }}
          />
        </div>
      </div>

      {/* 底部按钮行 */}
      <div className="flex items-center gap-3 text-white">
        {/* 播放/暂停 */}
        <button onClick={togglePlay} className="p-1.5 hover:bg-white/10 rounded-full transition-colors">
          {isBuffering ? (
            <Loader2 className="w-5 h-5 animate-spin" />
          ) : isPlaying ? (
            <Pause className="w-5 h-5" />
          ) : (
            <Play className="w-5 h-5" />
          )}
        </button>

        {/* 时间显示 */}
        <span ref={timeDisplayRef} className="text-xs tabular-nums select-none whitespace-nowrap">
          {formatDuration(displayTime)} / {formatDuration(duration)}
        </span>

        {/* 弹性空间 */}
        <div className="flex-1" />

        {/* 旋转按钮 */}
        {onRotate && (
          <button
            onClick={onRotate}
            className="p-1.5 hover:bg-white/10 rounded-full transition-colors relative"
            aria-label={`旋转视频（当前 ${rotation}°）`}
            title={`旋转视频（当前 ${rotation}°）`}
          >
            <RotateCw className="w-5 h-5" />
            {rotation !== 0 && (
              <span className="absolute -bottom-0.5 -right-0.5 text-[10px] font-semibold bg-emby-green text-white rounded-full px-1 leading-tight">
                {rotation}°
              </span>
            )}
          </button>
        )}

        {/* 音量控制 */}
        <button
          onClick={toggleMute}
          className="sm:hidden p-1.5 hover:bg-white/10 rounded-full transition-colors"
          aria-label={isMuted || volume === 0 ? '取消静音' : '静音'}
        >
          <VolumeIcon className="w-5 h-5" />
        </button>

        <div className="hidden sm:flex items-center gap-2 min-w-[132px]">
          <button
            onClick={toggleMute}
            className="p-1.5 hover:bg-white/10 rounded-full transition-colors"
            aria-label={isMuted || volume === 0 ? '取消静音' : '静音'}
          >
            <VolumeIcon className="w-5 h-5" />
          </button>
          <input
            type="range"
            min="0"
            max="1"
            step="0.01"
            value={displayVolume}
            onChange={handleVolumeChange}
            className="h-1 w-20 cursor-pointer accent-emby-green"
            aria-label="音量调节"
          />
          <span className="w-9 text-xs tabular-nums text-white/80 select-none text-right">
            {volumePercent}%
          </span>
        </div>

        {/* 倍速选择器 */}
        <div className="relative" ref={rateMenuRef}>
          <button
            onClick={() => setShowRateMenu(!showRateMenu)}
            className="px-2 py-1 text-xs hover:bg-white/10 rounded transition-colors select-none"
          >
            {playbackRate === 1 ? '倍速' : `${playbackRate}x`}
          </button>
          {showRateMenu && (
            <div className="absolute bottom-full mb-2 right-0 bg-black/90 backdrop-blur-sm rounded-lg py-1 min-w-[72px] shadow-lg">
              {PLAYBACK_RATES.map((rate) => (
                <button
                  key={rate}
                  onClick={() => handleRateChange(rate)}
                  className={`w-full px-3 py-1.5 text-xs text-center hover:bg-white/10 transition-colors ${
                    rate === playbackRate ? 'text-emby-green' : 'text-white'
                  }`}
                >
                  {rate}x
                </button>
              ))}
            </div>
          )}
        </div>

        {/* 全屏按钮 */}
        <button onClick={toggleFullscreen} className="p-1.5 hover:bg-white/10 rounded-full transition-colors">
          {isFullscreen ? (
            <Minimize className="w-5 h-5" />
          ) : (
            <Maximize className="w-5 h-5" />
          )}
        </button>
      </div>
    </div>
  );
}

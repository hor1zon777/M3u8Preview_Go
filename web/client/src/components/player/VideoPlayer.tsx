import { useEffect, useRef, useCallback, useState, forwardRef, useImperativeHandle } from 'react';
import Hls from 'hls.js';
import { usePlayerStore } from '../../stores/playerStore.js';
import api, { getAccessToken } from '../../services/api.js';
import type { Media } from '@m3u8-preview/shared';

const MAX_NETWORK_RETRY = 5;
const MAX_MEDIA_RETRY = 3;

/** 记忆已知需要代理的域名，避免每次都先直连失败再回退 */
const PROXY_DOMAINS_KEY = 'hls-proxy-domains';

function getProxyDomains(): Set<string> {
  try {
    const stored = sessionStorage.getItem(PROXY_DOMAINS_KEY);
    return stored ? new Set(JSON.parse(stored)) : new Set();
  } catch {
    return new Set();
  }
}

function addProxyDomain(url: string): void {
  try {
    const hostname = new URL(url).hostname;
    const domains = getProxyDomains();
    domains.add(hostname);
    sessionStorage.setItem(PROXY_DOMAINS_KEY, JSON.stringify([...domains]));
  } catch {
    // sessionStorage 不可用时静默忽略
  }
}

function needsProxy(url: string): boolean {
  try {
    const hostname = new URL(url).hostname;
    return getProxyDomains().has(hostname);
  } catch {
    return false;
  }
}

/** 调用签名 API 获取带 HMAC 签名的代理 URL（需登录，通过 api 实例自动携带 token） */
async function getSignedProxyUrl(m3u8Url: string): Promise<string> {
  const { data } = await api.get<{ success: boolean; proxyUrl: string }>('/proxy/sign', {
    params: { url: m3u8Url },
  });
  if (!data.success || !data.proxyUrl) {
    throw new Error('签名响应格式错误');
  }
  return data.proxyUrl;
}

interface VideoPlayerProps {
  media: Media;
  startTime?: number;
  onTimeUpdate?: (currentTime: number, duration: number) => void;
  autoPlay?: boolean;
  fillContainer?: boolean;
  controls?: boolean;
  /** 视频旋转角度（度），仅支持 0 / 90 / 180 / 270；在 fillContainer 模式下生效 */
  rotation?: 0 | 90 | 180 | 270;
}

export const VideoPlayer = forwardRef<HTMLVideoElement, VideoPlayerProps>(
  function VideoPlayer({ media, startTime = 0, onTimeUpdate, autoPlay = false, fillContainer = false, controls = true, rotation = 0 }, ref) {
    const videoRef = useRef<HTMLVideoElement>(null);
    const hlsRef = useRef<Hls | null>(null);
    const containerRef = useRef<HTMLDivElement>(null);
    const networkRetryRef = useRef(0);
    const mediaRetryRef = useRef(0);
    const proxyAttemptedRef = useRef(false);
    const mountedRef = useRef(true);
    const [containerSize, setContainerSize] = useState({ width: 0, height: 0 });
    const {
      setPlaying,
      setCurrentTime,
      setDuration,
      setQualities,
      setQuality,
      setBuffering,
      setAudioState,
      quality,
      reset,
    } = usePlayerStore();

    // 暴露内部 videoRef 给父组件
    useImperativeHandle(ref, () => videoRef.current!, []);

    const initHls = useCallback(async (sourceUrl?: string) => {
      const video = videoRef.current;
      if (!video) return;

      // 如果未指定 sourceUrl 且该域名已知需要代理，获取签名代理 URL
      let url = sourceUrl ?? media.m3u8Url;
      if (!sourceUrl && needsProxy(media.m3u8Url)) {
        try {
          url = await getSignedProxyUrl(media.m3u8Url);
        } catch {
          url = media.m3u8Url; // 签名失败回退到直连
        }
        if (!mountedRef.current) return;
        proxyAttemptedRef.current = true;
      }

      // Destroy previous instance
      if (hlsRef.current) {
        hlsRef.current.destroy();
        hlsRef.current = null;
      }

      if (Hls.isSupported()) {
        const hls = new Hls({
          startPosition: startTime,
          maxBufferLength: 30,
          maxMaxBufferLength: 60,
          // 为代理路径（/api/v1/proxy/...）自动附加 Bearer token
          // hls.js 默认使用 XHR 加载（未启用 fetch loader），因此只需 xhrSetup
          xhrSetup: (xhr, url) => {
            if (url.startsWith('/api/') || url.includes('/api/v1/proxy/')) {
              const token = getAccessToken();
              if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`);
            }
          },
        });

        hls.loadSource(url);
        hls.attachMedia(video);

        hls.on(Hls.Events.MANIFEST_PARSED, (_event, data) => {
          const levels = data.levels.map((level, index) => ({
            index,
            height: level.height,
            bitrate: level.bitrate,
          }));
          setQualities(levels);

          if (autoPlay) {
            video.play().catch(() => {
              // Autoplay blocked, user needs to interact
            });
          }
        });

        hls.on(Hls.Events.ERROR, (_event, data) => {
          if (data.fatal) {
            switch (data.type) {
              case Hls.ErrorTypes.NETWORK_ERROR:
                // CORS 失败时 response.code 为 0，尝试回退到代理
                if (
                  !proxyAttemptedRef.current &&
                  data.response && data.response.code === 0
                ) {
                  proxyAttemptedRef.current = true;
                  console.warn('HLS CORS 错误，回退到代理模式');
                  addProxyDomain(media.m3u8Url);
                  hls.destroy();
                  networkRetryRef.current = 0;
                  getSignedProxyUrl(media.m3u8Url)
                    .then(proxyUrl => { if (mountedRef.current) initHls(proxyUrl); })
                    .catch(() => console.error('获取签名代理 URL 失败'));
                  return;
                }

                if (networkRetryRef.current < MAX_NETWORK_RETRY) {
                  networkRetryRef.current++;
                  const delay = Math.min(1000 * Math.pow(2, networkRetryRef.current - 1), 16000);
                  console.warn(`HLS network error, retry ${networkRetryRef.current}/${MAX_NETWORK_RETRY} in ${delay}ms`);
                  setTimeout(() => hls.startLoad(), delay);
                } else {
                  console.error('HLS network error: max retries exceeded');
                  hls.destroy();
                }
                break;
              case Hls.ErrorTypes.MEDIA_ERROR:
                if (mediaRetryRef.current < MAX_MEDIA_RETRY) {
                  mediaRetryRef.current++;
                  console.warn(`HLS media error, retry ${mediaRetryRef.current}/${MAX_MEDIA_RETRY}`);
                  hls.recoverMediaError();
                } else {
                  console.error('HLS media error: max retries exceeded');
                  hls.destroy();
                }
                break;
              default:
                console.error('Fatal HLS error:', data);
                hls.destroy();
                break;
            }
          }
        });

        hlsRef.current = hls;
      } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
        // Safari native HLS
        const src = sourceUrl ?? media.m3u8Url;
        video.src = src;
        video.currentTime = startTime;

        // Safari CORS 回退：加载失败时切换到代理 URL
        const handleError = () => {
          if (!proxyAttemptedRef.current && !src.startsWith('/api/')) {
            proxyAttemptedRef.current = true;
            console.warn('Safari HLS 加载失败，回退到代理模式');
            addProxyDomain(media.m3u8Url);
            video.removeEventListener('error', handleError);
            getSignedProxyUrl(media.m3u8Url)
              .then(proxyUrl => { if (mountedRef.current) initHls(proxyUrl); })
              .catch(() => console.error('获取签名代理 URL 失败'));
          }
        };
        video.addEventListener('error', handleError);

        if (autoPlay) {
          video.play().catch(() => {});
        }
      }
    }, [media.m3u8Url, startTime, setQualities, autoPlay]);

    // Handle quality change
    useEffect(() => {
      if (hlsRef.current && quality !== undefined) {
        hlsRef.current.currentLevel = quality;
      }
    }, [quality]);

    // Initialize HLS
    useEffect(() => {
      mountedRef.current = true;
      proxyAttemptedRef.current = false;
      networkRetryRef.current = 0;
      mediaRetryRef.current = 0;
      initHls();
      return () => {
        mountedRef.current = false;
        if (hlsRef.current) {
          hlsRef.current.destroy();
          hlsRef.current = null;
        }
        reset(); // Reset playerStore on unmount
      };
    }, [initHls, reset]);

    // Video event handlers
    useEffect(() => {
      const video = videoRef.current;
      if (!video) return;

      const handleTimeUpdate = () => {
        setCurrentTime(video.currentTime);
        onTimeUpdate?.(video.currentTime, video.duration || 0);
      };

      const handleDurationChange = () => {
        setDuration(video.duration || 0);
      };

      const handlePlay = () => setPlaying(true);
      const handlePause = () => setPlaying(false);
      const handleWaiting = () => setBuffering(true);
      const handleCanPlay = () => setBuffering(false);
      const handlePlaying = () => setBuffering(false);
      const handleVolumeChange = () => {
        setAudioState({ volume: video.volume, isMuted: video.muted });
      };

      video.addEventListener('timeupdate', handleTimeUpdate);
      video.addEventListener('durationchange', handleDurationChange);
      video.addEventListener('play', handlePlay);
      video.addEventListener('pause', handlePause);
      video.addEventListener('waiting', handleWaiting);
      video.addEventListener('canplay', handleCanPlay);
      video.addEventListener('playing', handlePlaying);
      video.addEventListener('volumechange', handleVolumeChange);

      handleVolumeChange();

      return () => {
        video.removeEventListener('timeupdate', handleTimeUpdate);
        video.removeEventListener('durationchange', handleDurationChange);
        video.removeEventListener('play', handlePlay);
        video.removeEventListener('pause', handlePause);
        video.removeEventListener('waiting', handleWaiting);
        video.removeEventListener('canplay', handleCanPlay);
        video.removeEventListener('playing', handlePlaying);
        video.removeEventListener('volumechange', handleVolumeChange);
      };
    }, [setCurrentTime, setDuration, setPlaying, setBuffering, setAudioState, onTimeUpdate]);

    // Keyboard shortcuts（仅在 controls=true 时由本组件处理全屏）
    useEffect(() => {
      const video = videoRef.current;
      if (!video) return;

      function handleKeyDown(e: KeyboardEvent) {
        if (!video) return;
        const target = e.target as HTMLElement;
        if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement || target.isContentEditable) {
          return;
        }
        switch (e.key) {
          case ' ':
          case 'k':
            e.preventDefault();
            video.paused ? video.play() : video.pause();
            break;
          case 'ArrowLeft':
            e.preventDefault();
            video.currentTime = Math.max(0, video.currentTime - 10);
            break;
          case 'ArrowRight':
            e.preventDefault();
            video.currentTime = Math.min(video.duration, video.currentTime + 10);
            break;
          case 'ArrowUp': {
            e.preventDefault();
            const baseVolume = video.muted ? 0 : video.volume;
            const nextVolume = Math.min(1, Math.round((baseVolume + 0.1) * 10) / 10);
            video.volume = nextVolume;
            video.muted = nextVolume === 0;
            break;
          }
          case 'ArrowDown': {
            e.preventDefault();
            const baseVolume = video.muted ? 0 : video.volume;
            const nextVolume = Math.max(0, Math.round((baseVolume - 0.1) * 10) / 10);
            video.volume = nextVolume;
            video.muted = nextVolume === 0;
            break;
          }
          case 'f':
            // controls=false 时由 PlaybackPage 处理全屏
            if (controls) {
              e.preventDefault();
              if (document.fullscreenElement) {
                document.exitFullscreen();
              } else {
                containerRef.current?.requestFullscreen();
              }
            }
            break;
          case 'm':
            e.preventDefault();
            video.muted = !video.muted;
            break;
        }
      }

      document.addEventListener('keydown', handleKeyDown);
      return () => document.removeEventListener('keydown', handleKeyDown);
    }, [controls]);

    // 监听容器尺寸变化（仅在旋转 + fillContainer 时使用）
    useEffect(() => {
      if (!fillContainer) return;
      const el = containerRef.current;
      if (!el) return;
      const update = () => {
        setContainerSize({ width: el.clientWidth, height: el.clientHeight });
      };
      update();
      const observer = new ResizeObserver(update);
      observer.observe(el);
      return () => observer.disconnect();
    }, [fillContainer]);

    const isRotatedQuarter = rotation === 90 || rotation === 270;
    const rotatedStyle: React.CSSProperties | undefined = fillContainer && rotation !== 0
      ? {
          position: 'absolute',
          top: '50%',
          left: '50%',
          width: isRotatedQuarter ? containerSize.height : containerSize.width,
          height: isRotatedQuarter ? containerSize.width : containerSize.height,
          transform: `translate(-50%, -50%) rotate(${rotation}deg)`,
          transformOrigin: 'center',
        }
      : undefined;

    return (
      <div ref={containerRef} className={fillContainer ? "relative bg-black w-full h-full overflow-hidden" : "relative bg-black rounded-lg overflow-hidden"}>
        <video
          ref={videoRef}
          className={fillContainer ? "w-full h-full object-contain" : "w-full aspect-video"}
          style={rotatedStyle}
          controls={controls}
          playsInline
        />
      </div>
    );
  }
);

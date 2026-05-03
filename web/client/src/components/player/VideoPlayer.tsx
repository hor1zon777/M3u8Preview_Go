import { useEffect, useRef, useCallback, useState, forwardRef, useImperativeHandle } from 'react';
import Hls from 'hls.js';
import { usePlayerStore } from '../../stores/playerStore.js';
import {
  useSubtitleSettingsStore,
  colorWithOpacity,
  edgeStyleToTextShadow,
} from '../../stores/subtitleSettingsStore.js';
import api, { getAccessToken } from '../../services/api.js';
import { subtitleApi } from '../../services/subtitleApi.js';
import type { Media, SubtitleStatusResponse } from '@m3u8-preview/shared';

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
  const { data } = await api.get<{ success: boolean; proxyUrl?: string; data?: { proxyUrl?: string } }>('/proxy/sign', {
    params: { url: m3u8Url },
  });
  const proxyUrl = data.proxyUrl ?? data.data?.proxyUrl;
  if (!data.success || !proxyUrl) {
    throw new Error('签名响应格式错误');
  }
  return proxyUrl;
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
  /** 字幕状态变化时回调（用于父组件展示字幕设置入口的可用提示） */
  onSubtitleStatusChange?: (status: SubtitleStatusResponse | null) => void;
}

export const VideoPlayer = forwardRef<HTMLVideoElement, VideoPlayerProps>(
  function VideoPlayer({ media, startTime = 0, onTimeUpdate, autoPlay = false, fillContainer = false, controls = true, rotation = 0, onSubtitleStatusChange }, ref) {
    const videoRef = useRef<HTMLVideoElement>(null);
    const hlsRef = useRef<Hls | null>(null);
    const containerRef = useRef<HTMLDivElement>(null);
    const trackRef = useRef<HTMLTrackElement>(null);
    const networkRetryRef = useRef(0);
    const mediaRetryRef = useRef(0);
    const proxyAttemptedRef = useRef(false);
    const mountedRef = useRef(true);
    const [containerSize, setContainerSize] = useState({ width: 0, height: 0 });
    const [subtitleStatus, setSubtitleStatus] = useState<SubtitleStatusResponse | null>(null);
    /**
     * 当前激活的字幕行（可能多 cue 同时显示）。
     * 与后端 VTT 写入约定对齐：每个 cue payload 第 1 行为译文，第 2 行（可选）为原文。
     */
    const [activeCues, setActiveCues] = useState<Array<{ translated: string; source: string }>>([]);
    // 字幕外观设置（来自持久化的 zustand store）
    const subtitleSettings = useSubtitleSettingsStore();
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

    // 字幕状态轮询：进入页面立即拉一次，未完成时每 5s 重试，DONE 后停止
    useEffect(() => {
      let cancelled = false;
      let timer: ReturnType<typeof setTimeout> | undefined;

      async function fetchStatus() {
        try {
          const status = await subtitleApi.getStatus(media.id);
          if (cancelled) return;
          setSubtitleStatus(status);
          onSubtitleStatusChange?.(status);
          // PENDING / RUNNING 状态继续轮询
          if (status.status === 'PENDING' || status.status === 'RUNNING') {
            timer = setTimeout(fetchStatus, 5000);
          }
        } catch {
          // 字幕功能未启用或网络错误：静默
          if (!cancelled) onSubtitleStatusChange?.(null);
        }
      }

      fetchStatus();
      return () => {
        cancelled = true;
        if (timer) clearTimeout(timer);
      };
    }, [media.id, onSubtitleStatusChange]);

    // 接管字幕渲染：把原生 track 设为 hidden 模式（关闭浏览器默认渲染），
    // 自己监听 cuechange 把当前 cue 文本拼起来交给自定义渲染层
    useEffect(() => {
      const trackEl = trackRef.current;
      if (!trackEl) {
        setActiveCues([]);
        return;
      }
      const textTrack = trackEl.track;
      if (!textTrack) return;

      // hidden：仍触发 cuechange 但不渲染默认字幕；disabled 不会触发事件
      textTrack.mode = subtitleSettings.enabled ? 'hidden' : 'disabled';

      if (!subtitleSettings.enabled) {
        setActiveCues([]);
        return;
      }

      const handleCueChange = () => {
        const cues = textTrack.activeCues;
        if (!cues || cues.length === 0) {
          setActiveCues([]);
          return;
        }
        const items: Array<{ translated: string; source: string }> = [];
        for (let i = 0; i < cues.length; i++) {
          const cue = cues[i] as VTTCue;
          if (!cue.text) continue;
          // 后端 writeVTT 约定：第 1 行 = 译文，第 2 行 = 原文（如有且与译文不同）。
          // 旧字幕（仅译文）天然兼容 —— split 后 source 为 ''。
          const lines = cue.text.split('\n').map((s) => s.trim()).filter(Boolean);
          const translated = lines[0] ?? '';
          const source = lines.slice(1).join(' ');
          if (translated) items.push({ translated, source });
        }
        setActiveCues(items);
      };

      textTrack.addEventListener('cuechange', handleCueChange);
      // 立即同步一次，避免在切换 enabled 时漏掉当前正在显示的字幕
      handleCueChange();

      return () => {
        textTrack.removeEventListener('cuechange', handleCueChange);
      };
    }, [subtitleSettings.enabled, subtitleStatus?.vttUrl]);

    const isRotatedQuarter = rotation === 90 || rotation === 270;
    // 旋转策略：把 transform 套在外层 wrapper div 上，video 保持 w-full h-full 不变。
    //
    // 历史 bug：直接在 <video> 上应用 transform: rotate(90deg) 时，PC Chrome / Edge
    // 在硬件解码模式下会因 video 自带独立 GPU 合成层与 CSS transform 合成顺序冲突，
    // 导致 90° / 270° 时画面黑屏（音频正常）。
    // 解决：transform 落到 wrapper div，video 不参与 rotate；浏览器对 div 走标准
    // 合成，video 层保持稳定，黑屏问题消失。
    //
    // wrapper 的尺寸需"未旋转前"是目标视觉框的旋转回退尺寸：
    //   - 90° / 270°：wrapper 实际 width=容器高、height=容器宽，再旋转 90° 后视觉
    //     正好填满容器。
    //   - 180°：wrapper 与容器同尺寸，旋转 180° 视觉不变。
    const rotationWrapperStyle: React.CSSProperties | undefined =
      fillContainer && rotation !== 0
        ? {
            position: 'absolute',
            top: '50%',
            left: '50%',
            width: isRotatedQuarter ? containerSize.height : containerSize.width,
            height: isRotatedQuarter ? containerSize.width : containerSize.height,
            transform: `translate(-50%, -50%) rotate(${rotation}deg)`,
            transformOrigin: 'center',
            // 提示浏览器为 wrapper 单独建合成层，避免在旋转切换瞬间触发 video
            // 层重合成抖动；同时缓和某些显卡驱动下的子像素采样问题。
            willChange: 'transform',
            backfaceVisibility: 'hidden',
          }
        : undefined;

    const showSubtitleOverlay =
      subtitleSettings.enabled &&
      subtitleStatus?.status === 'DONE' &&
      !!subtitleStatus.vttUrl &&
      activeCues.length > 0;

    // 字幕样式：1rem 作为基准（容器 vw 计算后），fontSize 是百分比缩放
    const subtitleTextStyle: React.CSSProperties = {
      color: colorWithOpacity(subtitleSettings.textColor, subtitleSettings.textOpacity),
      fontSize: `calc(min(4vw, 28px) * ${subtitleSettings.fontSize / 100})`,
      fontWeight: subtitleSettings.fontWeight,
      textShadow: edgeStyleToTextShadow(subtitleSettings.edgeStyle),
      lineHeight: 1.3,
      whiteSpace: 'pre-line',
      wordBreak: 'break-word',
    };

    // 原文行：字号略小、颜色稍暗、字重还原为 normal，避免抢主字幕视线
    const subtitleSourceStyle: React.CSSProperties = {
      ...subtitleTextStyle,
      fontSize: `calc(min(4vw, 28px) * ${subtitleSettings.fontSize / 100} * 0.82)`,
      fontWeight: 'normal',
      opacity: 0.85,
      marginTop: '0.15em',
    };

    // 背景仅在有不透明度时渲染，避免空 padding 影响视觉
    const subtitleBgStyle: React.CSSProperties = subtitleSettings.bgOpacity > 0
      ? {
          backgroundColor: colorWithOpacity(subtitleSettings.bgColor, subtitleSettings.bgOpacity),
          padding: '0.15em 0.6em',
          borderRadius: '4px',
        }
      : {};

    // video 节点：不再直接承载 rotate，永远使用 className 控制大小。
    // rotation === 0 时直接渲染 video；非 0 时套一层 wrapper 把 transform 隔离出去。
    const videoNode = (
      <video
        ref={videoRef}
        className={fillContainer ? "w-full h-full object-contain" : "w-full aspect-video"}
        controls={controls}
        playsInline
      >
        {/* 字幕 VTT 走同源签名 URL，无需 crossOrigin；不会影响 HLS 播放
            注意：不再使用 default，由副作用根据用户开关切换 mode=hidden/disabled，
            改用自定义渲染层显示字幕文本，方便用户自定义大小/颜色/背景透明度等。 */}
        {subtitleStatus?.status === 'DONE' && subtitleStatus.vttUrl && (
          <track
            ref={trackRef}
            key={subtitleStatus.vttUrl}
            kind="subtitles"
            src={subtitleStatus.vttUrl}
            srcLang={subtitleStatus.targetLang || 'zh'}
            label="中文（机翻）"
          />
        )}
      </video>
    );

    return (
      <div ref={containerRef} className={fillContainer ? "relative bg-black w-full h-full overflow-hidden" : "relative bg-black rounded-lg overflow-hidden"}>
        {rotationWrapperStyle ? (
          <div style={rotationWrapperStyle}>{videoNode}</div>
        ) : (
          videoNode
        )}

        {/* 自定义字幕渲染层；与原生 controls 互不干扰：
            - controls=true 时浏览器原生控制条会盖在最上方，自定义字幕也在 video 上方
            - controls=false 时由 PlayerControls 渲染，bottomOffset 由用户控制以避开按钮 */}
        {showSubtitleOverlay && (
          <div
            className="absolute left-0 right-0 z-[8] flex flex-col items-center pointer-events-none px-4 text-center gap-1"
            style={{ bottom: `${subtitleSettings.bottomOffset}%` }}
          >
            {activeCues.map((item, idx) => (
              <div key={idx} className="flex flex-col items-center max-w-full">
                {/* 主字幕（译文，或回退后的原文） */}
                <div style={{ ...subtitleTextStyle, ...subtitleBgStyle }}>{item.translated}</div>
                {/* 原文行：仅当用户开启 showOriginal 且该 cue 含独立原文时才渲染 */}
                {subtitleSettings.showOriginal && item.source && (
                  <div style={{ ...subtitleSourceStyle, ...subtitleBgStyle }}>{item.source}</div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    );
  }
);

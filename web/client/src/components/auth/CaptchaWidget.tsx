import { useEffect, useRef } from 'react';

declare global {
  interface Window {
    PowCaptcha?: {
      render: (
        el: string | HTMLElement,
        opts: {
          siteKey: string;
          endpoint: string;
          theme?: 'light' | 'dark';
          lang?: string;
          onSuccess?: (token: string) => void;
          onError?: (err: Error) => void;
          onExpired?: () => void;
        },
      ) => { reset: () => void; getResponse: () => string | null; destroy: () => void };
    };
  }
}

interface CaptchaWidgetProps {
  endpoint: string;
  siteKey: string;
  onSuccess: (token: string) => void;
  onError?: (err: Error) => void;
  onExpired?: () => void;
}

const loadPromises = new Map<string, Promise<void>>();

function loadScript(src: string, dataAttrs?: Record<string, string>): Promise<void> {
  const cached = loadPromises.get(src);
  if (cached) return cached;

  // SDK 已通过其他途径加载（含 <script> 已插入但 onload 未触发的情况）
  if (window.PowCaptcha) {
    const p = Promise.resolve();
    loadPromises.set(src, p);
    return p;
  }

  const existingTag = document.querySelector<HTMLScriptElement>(`script[src="${src}"]`);
  const promise = new Promise<void>((resolve, reject) => {
    const attach = (el: HTMLScriptElement) => {
      el.addEventListener('load', () => resolve(), { once: true });
      el.addEventListener(
        'error',
        () => {
          loadPromises.delete(src);
          reject(new Error(`加载验证码 SDK 失败: ${src}`));
        },
        { once: true },
      );
    };
    if (existingTag) {
      attach(existingTag);
      return;
    }
    const script = document.createElement('script');
    script.src = src;
    script.async = true;
    if (dataAttrs) {
      for (const [k, v] of Object.entries(dataAttrs)) {
        script.setAttribute(`data-${k}`, v);
      }
    }
    attach(script);
    document.head.appendChild(script);
  });
  loadPromises.set(src, promise);
  return promise;
}

export function CaptchaWidget({ endpoint, siteKey, onSuccess, onError, onExpired }: CaptchaWidgetProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const widgetRef = useRef<ReturnType<NonNullable<typeof window.PowCaptcha>['render']> | null>(null);

  const stableOnSuccess = useRef(onSuccess);
  const stableOnError = useRef(onError);
  const stableOnExpired = useRef(onExpired);
  stableOnSuccess.current = onSuccess;
  stableOnError.current = onError;
  stableOnExpired.current = onExpired;

  useEffect(() => {
    let cancelled = false;
    const base = endpoint.replace(/\/+$/, '');
    const sdkUrl = `${base}/sdk/pow-captcha.js`;

    loadScript(sdkUrl, {
      'site-key': siteKey,
      'wasm-base': `${base}/sdk/`,
    })
      .then(() => {
        if (cancelled || !containerRef.current || !window.PowCaptcha) return;
        widgetRef.current = window.PowCaptcha.render(containerRef.current, {
          siteKey,
          endpoint,
          theme: 'dark',
          lang: 'zh-CN',
          onSuccess: (token) => stableOnSuccess.current(token),
          onError: (err) => stableOnError.current?.(err),
          onExpired: () => stableOnExpired.current?.(),
        });
      })
      .catch((err) => {
        if (!cancelled) stableOnError.current?.(err);
      });

    return () => {
      cancelled = true;
      widgetRef.current?.destroy();
      widgetRef.current = null;
    };
  }, [endpoint, siteKey]);

  return <div ref={containerRef} className="w-full [&>div]:w-full" />;
}

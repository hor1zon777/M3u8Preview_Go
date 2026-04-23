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

// --- SDK 加载（带 SRI manifest 支持）---
//
// 策略（对齐 captcha 服务 docs/INTEGRATION.md 方式 D）：
//   1. 拉 GET /sdk/manifest.json（3s 超时），带 cache: 'no-store' 避开浏览器缓存漂移
//   2. 用 manifest.artifacts['pow-captcha.js'] 的 {url, integrity} 注入 <script>
//   3. 失败（网络 / 解析 / 超时）→ 降级到旧路径 ${endpoint}/sdk/pow-captcha.js（无 integrity）
//
// 这样 SDK 被篡改时浏览器 SRI 校验直接拒绝执行，同时老 captcha 服务器也仍兼容。

interface PortcullisManifest {
  version: string;
  artifacts: Record<string, { url: string; integrity?: string; size?: number }>;
}

interface SdkLoadPlan {
  scriptSrc: string;
  integrity?: string;
  wasmBase: string;
  /** fromManifest=true 时是带 SRI 的 v{version} 路径；false 表示降级到旧路径 */
  fromManifest: boolean;
}

const manifestCache = new Map<string, Promise<SdkLoadPlan>>();

/**
 * resolveSdkPlan 决定本次应加载哪个 SDK 资源：
 *   - 成功拉到 manifest → 用版本化路径 + integrity
 *   - manifest 不可用（超时 / 404 / 老 captcha 服务未部署 Tier 1）→ 降级到 ${endpoint}/sdk/pow-captcha.js 无 SRI
 * 单个 endpoint 的结果在会话内缓存，避免每次 render 重拉。
 */
function resolveSdkPlan(endpoint: string): Promise<SdkLoadPlan> {
  const cached = manifestCache.get(endpoint);
  if (cached) return cached;

  const legacy: SdkLoadPlan = {
    scriptSrc: `${endpoint}/sdk/pow-captcha.js`,
    wasmBase: `${endpoint}/sdk/`,
    fromManifest: false,
  };

  const promise = (async (): Promise<SdkLoadPlan> => {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), 3000);
    try {
      const resp = await fetch(`${endpoint}/sdk/manifest.json`, {
        cache: 'no-store',
        signal: ctl.signal,
      });
      if (!resp.ok) return legacy;
      const manifest = (await resp.json()) as PortcullisManifest;
      const sdk = manifest?.artifacts?.['pow-captcha.js'];
      if (!sdk || !sdk.url) return legacy;
      const scriptSrc = `${endpoint}${sdk.url}`;
      return {
        scriptSrc,
        integrity: sdk.integrity,
        wasmBase: scriptSrc.replace(/[^/]+$/, ''),
        fromManifest: true,
      };
    } catch {
      return legacy;
    } finally {
      clearTimeout(timer);
    }
  })();

  manifestCache.set(endpoint, promise);
  // 降级后允许下次重试（可能 captcha 服务刚上线 Tier 1）
  promise.then((plan) => {
    if (!plan.fromManifest) manifestCache.delete(endpoint);
  });
  return promise;
}

const loadPromises = new Map<string, Promise<void>>();

function loadScript(
  src: string,
  attrs?: { integrity?: string; siteKey?: string; wasmBase?: string },
): Promise<void> {
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
    const onError = (el: HTMLScriptElement) => {
      loadPromises.delete(src);
      el.remove();
      reject(new Error(`加载验证码 SDK 失败: ${src}`));
    };
    const attach = (el: HTMLScriptElement) => {
      el.addEventListener('load', () => resolve(), { once: true });
      el.addEventListener('error', () => onError(el), { once: true });
    };
    if (existingTag) {
      attach(existingTag);
      return;
    }
    const script = document.createElement('script');
    script.src = src;
    script.async = true;
    // SRI 校验要求同时设置 integrity + crossorigin=anonymous，否则浏览器不 enforce
    if (attrs?.integrity) {
      script.integrity = attrs.integrity;
      script.crossOrigin = 'anonymous';
    } else {
      // 没有 integrity 时也加 crossorigin，方便 captcha 服务将来追加 SRI 响应头
      script.crossOrigin = 'anonymous';
    }
    if (attrs?.siteKey) script.setAttribute('data-site-key', attrs.siteKey);
    if (attrs?.wasmBase) script.setAttribute('data-wasm-base', attrs.wasmBase);
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

    (async () => {
      const plan = await resolveSdkPlan(base);
      try {
        await loadScript(plan.scriptSrc, {
          integrity: plan.integrity,
          siteKey,
          wasmBase: plan.wasmBase,
        });
      } catch (err) {
        if (!cancelled) stableOnError.current?.(err as Error);
        return;
      }
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
    })();

    return () => {
      cancelled = true;
      widgetRef.current?.destroy();
      widgetRef.current = null;
    };
  }, [endpoint, siteKey]);

  return <div ref={containerRef} className="w-full [&>div]:w-full" />;
}

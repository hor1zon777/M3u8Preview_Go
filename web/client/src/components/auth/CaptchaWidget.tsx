import { useEffect, useRef } from 'react';
import * as ed from '@noble/ed25519';

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
  /**
   * Portcullis Tier 2 Ed25519 公钥（base64 32 字节），由 /api/v1/auth/captcha-config 返回。
   * 非空时强制校验 /sdk/manifest.json 的 X-Portcullis-Signature；缺失 header 或失配直接 onError。
   * 为空时跳过签名校验（Tier 1 行为）。
   */
  manifestPubKey?: string;
  onSuccess: (token: string) => void;
  onError?: (err: Error) => void;
  onExpired?: () => void;
}

// --- SDK 加载（对齐 captcha/docs/INTEGRATION.md 方式 D + TIER2_IMPLEMENTATION.md）---
//
// 决策树：
//   1. 拉 GET /sdk/manifest.json（3s 超时，cache: no-store）
//   2. 若配置了 manifestPubKey：
//      a. 响应必须带 X-Portcullis-Signature header，否则 reject（防去头绕过）
//      b. Ed25519 verify(sig, raw response bytes, pubKey)；失败 reject
//   3. 解析 manifest.artifacts['pow-captcha.js'] → {url, integrity}
//   4. 注入 <script integrity=... crossorigin=anonymous src=...>
//   5. manifest 不可用（网络 / 超时 / 非 2xx / 解析失败）→ 降级到旧路径 ${endpoint}/sdk/pow-captcha.js
//      - 降级路径不带 integrity，依赖 HTTPS + HSTS
//      - 若本地配置了 pubKey 但 manifest 拉不到，**不降级**——视为强校验失败（未来 Portcullis 故障不应变成静默不验签）

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
  /** 标记本次 plan 是否已通过 Ed25519 签名校验（配置了 pubKey 时必须 true） */
  signatureVerified: boolean;
}

/** 缓存 key：endpoint + "|" + pubKey（不同 pubKey 不复用） */
const manifestCache = new Map<string, Promise<SdkLoadPlan>>();

/** base64 (标准 / URL-safe / padding 变体) → Uint8Array */
function decodeBase64(s: string): Uint8Array | null {
  const trimmed = s.trim();
  // 先尝试标准 base64（含 padding）；不行再试 URL-safe
  try {
    const normalized = trimmed.replace(/-/g, '+').replace(/_/g, '/');
    const padded = normalized + '='.repeat((4 - (normalized.length % 4)) % 4);
    const binary = atob(padded);
    const out = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
    return out;
  } catch {
    return null;
  }
}

async function verifyManifestSignature(
  rawBody: Uint8Array,
  sigB64: string,
  pubKeyB64: string,
): Promise<boolean> {
  const sig = decodeBase64(sigB64);
  const pub = decodeBase64(pubKeyB64);
  if (!sig || sig.length !== 64) return false;
  if (!pub || pub.length !== 32) return false;
  try {
    return await ed.verifyAsync(sig, rawBody, pub);
  } catch {
    return false;
  }
}

function resolveSdkPlan(endpoint: string, manifestPubKey?: string): Promise<SdkLoadPlan> {
  const cacheKey = `${endpoint}|${manifestPubKey ?? ''}`;
  const cached = manifestCache.get(cacheKey);
  if (cached) return cached;

  const legacy: SdkLoadPlan = {
    scriptSrc: `${endpoint}/sdk/pow-captcha.js`,
    wasmBase: `${endpoint}/sdk/`,
    fromManifest: false,
    signatureVerified: false,
  };

  const requireSignature = !!manifestPubKey && manifestPubKey.trim() !== '';

  const promise = (async (): Promise<SdkLoadPlan> => {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), 3000);
    try {
      const resp = await fetch(`${endpoint}/sdk/manifest.json`, {
        cache: 'no-store',
        signal: ctl.signal,
      });
      if (!resp.ok) {
        if (requireSignature) {
          throw new Error(`manifest HTTP ${resp.status}（已配置公钥，禁止降级）`);
        }
        return legacy;
      }
      // 必须先拿 raw bytes（ArrayBuffer），再 parse；签名对象是原始字节，不是规范化 JSON
      const rawBuf = await resp.arrayBuffer();
      const rawBytes = new Uint8Array(rawBuf);
      if (requireSignature) {
        const sigHeader = resp.headers.get('X-Portcullis-Signature');
        if (!sigHeader) {
          throw new Error('Portcullis 已配置签名但响应缺少 X-Portcullis-Signature header');
        }
        const ok = await verifyManifestSignature(rawBytes, sigHeader, manifestPubKey!);
        if (!ok) {
          throw new Error('Portcullis manifest 签名校验失败');
        }
      }
      const manifest = JSON.parse(new TextDecoder().decode(rawBytes)) as PortcullisManifest;
      const sdk = manifest?.artifacts?.['pow-captcha.js'];
      if (!sdk || !sdk.url) {
        if (requireSignature) {
          throw new Error('manifest 缺少 pow-captcha.js artifact');
        }
        return legacy;
      }
      const scriptSrc = `${endpoint}${sdk.url}`;
      return {
        scriptSrc,
        integrity: sdk.integrity,
        wasmBase: scriptSrc.replace(/[^/]+$/, ''),
        fromManifest: true,
        signatureVerified: requireSignature,
      };
    } catch (err) {
      if (requireSignature) throw err;
      return legacy;
    } finally {
      clearTimeout(timer);
    }
  })();

  manifestCache.set(cacheKey, promise);
  // 降级 / 拒绝后允许下次重试（部署升级后无感恢复）
  promise
    .then((plan) => {
      if (!plan.fromManifest) manifestCache.delete(cacheKey);
    })
    .catch(() => manifestCache.delete(cacheKey));
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
    // SRI 校验要求同时设置 integrity + crossorigin=anonymous
    if (attrs?.integrity) script.integrity = attrs.integrity;
    script.crossOrigin = 'anonymous';
    if (attrs?.siteKey) script.setAttribute('data-site-key', attrs.siteKey);
    if (attrs?.wasmBase) script.setAttribute('data-wasm-base', attrs.wasmBase);
    attach(script);
    document.head.appendChild(script);
  });
  loadPromises.set(src, promise);
  return promise;
}

export function CaptchaWidget({
  endpoint,
  siteKey,
  manifestPubKey,
  onSuccess,
  onError,
  onExpired,
}: CaptchaWidgetProps) {
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
      let plan: SdkLoadPlan;
      try {
        plan = await resolveSdkPlan(base, manifestPubKey);
      } catch (err) {
        if (!cancelled) stableOnError.current?.(err as Error);
        return;
      }
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
  }, [endpoint, siteKey, manifestPubKey]);

  return <div ref={containerRef} className="w-full [&>div]:w-full" />;
}

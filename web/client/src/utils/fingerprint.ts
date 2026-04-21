// utils/fingerprint.ts
// 设备指纹采集：Canvas + WebGL + 屏幕 + UA + 时区 + 语言 → SHA-256 hex。
//
// 用途：混入 HKDF salt，让 AES key 与设备绑定。逆向者换设备/换浏览器 →
// 指纹变 → AES key 不同 → 后端解密失败 → 400。
//
// 指纹不是秘密（逆向者能读到计算逻辑），但增加了"换环境复现"的成本。
// 计算逻辑被 obfuscator 混淆，进一步提高阅读门槛。
//
// 同一会话内指纹不变（缓存在模块级变量），不会因重复计算产生抖动。

let cached: string | null = null;

export async function getDeviceFingerprint(): Promise<string> {
  if (cached) return cached;
  const parts: string[] = [];

  parts.push(navigator.userAgent || '');
  parts.push(navigator.language || '');
  parts.push(navigator.platform || '');
  parts.push(String(screen.width) + 'x' + String(screen.height));
  parts.push(String(screen.colorDepth));
  parts.push(String(window.devicePixelRatio || 1));

  try {
    parts.push(Intl.DateTimeFormat().resolvedOptions().timeZone || '');
  } catch {
    parts.push('');
  }

  parts.push(getCanvasFingerprint());
  parts.push(getWebGLFingerprint());

  const raw = parts.join('|');
  const hash = await sha256hex(raw);
  cached = hash;
  return hash;
}

function getCanvasFingerprint(): string {
  try {
    const canvas = document.createElement('canvas');
    canvas.width = 200;
    canvas.height = 50;
    const ctx = canvas.getContext('2d');
    if (!ctx) return '';
    ctx.textBaseline = 'top';
    ctx.font = '14px Arial';
    ctx.fillStyle = '#f60';
    ctx.fillRect(50, 0, 80, 30);
    ctx.fillStyle = '#069';
    ctx.fillText('m3u8fp', 2, 15);
    ctx.fillStyle = 'rgba(102,204,0,0.7)';
    ctx.fillText('m3u8fp', 4, 17);
    return canvas.toDataURL();
  } catch {
    return '';
  }
}

function getWebGLFingerprint(): string {
  try {
    const canvas = document.createElement('canvas');
    const gl = canvas.getContext('webgl') || canvas.getContext('experimental-webgl');
    if (!gl || !(gl instanceof WebGLRenderingContext)) return '';
    const debugInfo = gl.getExtension('WEBGL_debug_renderer_info');
    if (!debugInfo) return '';
    const vendor = gl.getParameter(debugInfo.UNMASKED_VENDOR_WEBGL) || '';
    const renderer = gl.getParameter(debugInfo.UNMASKED_RENDERER_WEBGL) || '';
    return vendor + '~' + renderer;
  } catch {
    return '';
  }
}

async function sha256hex(input: string): Promise<string> {
  const data = new TextEncoder().encode(input);
  const buf = await crypto.subtle.digest('SHA-256', data);
  return Array.from(new Uint8Array(buf))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

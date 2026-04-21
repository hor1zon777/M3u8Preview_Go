// utils/crypto.ts
// 登录载荷加密工具：浏览器 WebCrypto ECDH P-256 + HKDF-SHA256 + AES-256-GCM。
//
// 协议（与后端 internal/util/ecdh.go 一一对齐）：
//   1. GET /auth/challenge → {serverPub, challenge, ttl}（base64url 无 padding）
//   2. 生成一次性客户端 ECDH 密钥对。
//   3. deriveBits(ECDH) → 32B 共享密钥 ss。
//   4. HKDF-SHA256(ss, salt=challengeBytes, info="m3u8preview-auth-v1") → 32B AES key。
//   5. AES-GCM(iv=12B随机, aad=端点常量, plaintext=JSON.stringify(payload+ts)) → ct(含 16B tag)。
//   6. POST {challenge, clientPub, iv, ct}。
//
// 设计点：
//   - 每次都拉新的 challenge；即使用户快速连续登录，每一次 AES 密钥都不同。
//   - ts 随明文传，后端占两重防重放防线（challenge 单次 + ts 60s 窗）。
//   - AAD 用端点常量，防止 login 的密文被原样投到 register/change-password。

import axios from 'axios';

const HKDF_INFO = new TextEncoder().encode('m3u8preview-auth-v1');
const CHALLENGE_URL = '/api/v1/auth/challenge';

/** 端点常量，与后端 internal/handler/auth.go 的 aad* 常量相同。 */
export const AuthAAD = {
  login: 'auth:login:v1',
  register: 'auth:register:v1',
  changePassword: 'auth:change-password:v1',
} as const;
export type AuthAADKey = keyof typeof AuthAAD;

interface ChallengeResponse {
  serverPub: string;
  challenge: string;
  ttl: number;
}

export interface EncryptedEnvelope {
  challenge: string;
  clientPub: string;
  iv: string;
  ct: string;
}

/** base64url 无 padding 解码。与 Go encoding/base64.RawURLEncoding 对齐。 */
function b64urlDecode(s: string): Uint8Array {
  const pad = s.length % 4 === 0 ? '' : '='.repeat(4 - (s.length % 4));
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/') + pad;
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function b64urlEncode(bytes: ArrayBuffer | Uint8Array): string {
  const view = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  let bin = '';
  for (let i = 0; i < view.length; i++) bin += String.fromCharCode(view[i]);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

async function fetchChallenge(): Promise<{
  serverPubRaw: Uint8Array;
  challengeId: string;
  challengeSalt: Uint8Array;
}> {
  // 用原始 axios 跳过 api 拦截器：本接口未认证，也不应触发 refresh 逻辑。
  const { data } = await axios.get<{ success: boolean; data?: ChallengeResponse; error?: string }>(
    CHALLENGE_URL,
    { timeout: 10000 },
  );
  if (!data.success || !data.data) {
    throw new Error(data.error || '无法获取登录挑战');
  }
  const serverPubRaw = b64urlDecode(data.data.serverPub);
  const challengeSalt = b64urlDecode(data.data.challenge);
  return {
    serverPubRaw,
    challengeId: data.data.challenge,
    challengeSalt,
  };
}

/**
 * 把待加密载荷转成 EncryptedEnvelope。调用方不需关心 ts 字段，内部会自动注入。
 * @param aadKey 端点常量键，决定 AES-GCM AAD。
 * @param payload 业务字段（将被 JSON.stringify）。
 */
export async function encryptAuthPayload(
  aadKey: AuthAADKey,
  payload: Record<string, unknown>,
): Promise<EncryptedEnvelope> {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) {
    // WebCrypto 要求 HTTPS 或 localhost；非 HTTPS 的 IP 访问会命中此分支
    throw new Error('当前环境不支持 WebCrypto（需 HTTPS 或 localhost）');
  }

  const { serverPubRaw, challengeId, challengeSalt } = await fetchChallenge();

  // 客户端一次性 ECDH 密钥对
  const clientKey = await subtle.generateKey(
    { name: 'ECDH', namedCurve: 'P-256' },
    true,
    ['deriveBits'],
  );
  const serverKey = await subtle.importKey(
    'raw',
    serverPubRaw.buffer as ArrayBuffer,
    { name: 'ECDH', namedCurve: 'P-256' },
    false,
    [],
  );

  // ECDH 协商 32B 共享密钥
  const sharedBits = await subtle.deriveBits(
    { name: 'ECDH', public: serverKey },
    clientKey.privateKey,
    256,
  );

  // HKDF-SHA256 派生 AES key（与后端 hkdfInfo / salt=challenge 完全对齐）
  const hkdfMaterial = await subtle.importKey('raw', sharedBits, 'HKDF', false, ['deriveKey']);
  const aesKey = await subtle.deriveKey(
    {
      name: 'HKDF',
      hash: 'SHA-256',
      salt: challengeSalt.buffer as ArrayBuffer,
      info: HKDF_INFO.buffer as ArrayBuffer,
    },
    hkdfMaterial,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt'],
  );

  const iv = globalThis.crypto.getRandomValues(new Uint8Array(12));
  const aad = new TextEncoder().encode(AuthAAD[aadKey]);
  const plaintext = new TextEncoder().encode(
    JSON.stringify({ ...payload, ts: Date.now() }),
  );

  const ct = await subtle.encrypt(
    { name: 'AES-GCM', iv: iv.buffer as ArrayBuffer, additionalData: aad.buffer as ArrayBuffer },
    aesKey,
    plaintext.buffer as ArrayBuffer,
  );

  // 导出客户端公钥 raw(65B uncompressed)
  const clientPubRaw = new Uint8Array(await subtle.exportKey('raw', clientKey.publicKey));

  return {
    challenge: challengeId,
    clientPub: b64urlEncode(clientPubRaw),
    iv: b64urlEncode(iv),
    ct: b64urlEncode(ct),
  };
}

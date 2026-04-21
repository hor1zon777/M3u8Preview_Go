// utils/crypto.ts
// 登录载荷加密工具：WASM 加密核心（Rust 编译，位于 src/wasm/）。
//
// 协议（与后端 internal/util/ecdh.go 一一对齐）：
//   1. GET /auth/challenge → {serverPub, challenge, ttl}（base64url 无 padding）
//   2. 加密核心由 Rust 编译的 WASM 模块（src/wasm/）执行：
//      - 生成一次性 ECDH P-256 密钥对
//      - ECDH 协商 → HKDF-SHA256 派生 AES-256 key
//      - AES-GCM 加密 {payload, ts}，IV=12B 随机，AAD=端点常量
//   3. POST {challenge, clientPub, iv, ct}（全 base64url）
//
// 相比 T1（纯 JS WebCrypto）：
//   - 加密核心从 JS 迁移到 WASM（逆向需 wasm-decompile/Ghidra）
//   - HKDF info 等关键常量在 WASM 内 XOR 混淆，运行时解码
//
// 历史注记：曾实现过"检测 DevTools → 拒绝加密请求"的软反调试，但用户正当
// 使用 F12 调试时会被登录流程误杀，ROI 为负，已移除。需要反调试时应上报给
// 后端做风控，而不是阻断当前请求。

import axios from 'axios';
import initWasm, { encrypt_auth_payload, type EncryptResult } from '../wasm/crypto_wasm.js';
import { getDeviceFingerprint } from './fingerprint.js';

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

/** 懒加载 WASM：首次调用 encryptAuthPayload 时初始化，后续复用同一个 Promise。 */
let wasmReady: Promise<void> | null = null;
function ensureWasm(): Promise<void> {
  if (!wasmReady) {
    // wasm-pack --target web 产物会通过 new URL('crypto_wasm_bg.wasm', import.meta.url) 自行定位 .wasm。
    // Vite 在 build 时会把 .wasm 作为 asset 输出并重写 URL。
    wasmReady = initWasm().then(() => undefined);
  }
  return wasmReady;
}

async function fetchChallenge(fingerprint: string): Promise<ChallengeResponse> {
  // POST 改 GET：T2.5 起 challenge 绑定设备指纹，body 传 fingerprint。
  const { data } = await axios.post<{ success: boolean; data?: ChallengeResponse; error?: string }>(
    CHALLENGE_URL,
    { fingerprint },
    { timeout: 10000 },
  );
  if (!data.success || !data.data) {
    throw new Error(data.error || '无法获取登录挑战');
  }
  return data.data;
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
  await ensureWasm();

  const fingerprint = await getDeviceFingerprint();
  const challengeResp = await fetchChallenge(fingerprint);
  const plaintextJson = JSON.stringify({ ...payload, ts: Date.now() });

  let result: EncryptResult | null = null;
  try {
    result = encrypt_auth_payload(
      AuthAAD[aadKey],
      challengeResp.serverPub,
      challengeResp.challenge,
      fingerprint,
      plaintextJson,
    );
    return {
      challenge: challengeResp.challenge,
      clientPub: result.clientPub,
      iv: result.iv,
      ct: result.ct,
    };
  } finally {
    // WASM 分配的 EncryptResult 需手动 free，避免 WASM 堆内存泄漏
    result?.free?.();
  }
}


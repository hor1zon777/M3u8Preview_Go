// utils/crypto.ts
// 登录载荷加密工具：WASM 加密核心（Rust 编译，位于 src/wasm/）。
//
// 协议（与后端 internal/util/ecdh.go 一一对齐）：
//   1. POST /auth/challenge { fingerprint } → {serverPub, challenge, ttl}（base64url 无 padding）
//   2. 加密核心由 Rust 编译的 WASM 模块（src/wasm/）执行：
//      - 生成一次性 ECDH P-256 密钥对
//      - ECDH 协商 → HKDF-SHA256(salt=challenge) 派生 AES-256 key
//      - AES-GCM 加密 {payload, ts}，IV=12B 随机，AAD=端点常量
//   3. POST {challenge, clientPub, iv, ct}（全 base64url）
//
// 历史注记（2026-04）:
//   - fingerprint 仍继续上报到 /auth/challenge 供服务端记录（Phase 2 会做
//     新设备检测 / 风控告警），但 **不再参与** HKDF salt 派生——避免浏览器
//     升级 / 隐身切换 / 硬件变化导致的假阳性登录失败。详见
//     docs/FINGERPRINT_REDESIGN.md。
//   - 曾实现过"检测 DevTools → 拒绝加密请求"的软反调试，正当 F12 调试会误杀已移除。

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
  // fingerprint 继续上报：服务端存 challenge_store 供 Phase 2 风控读取，
  // 但不再参与 HKDF salt 派生（H8 Phase 1）。
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

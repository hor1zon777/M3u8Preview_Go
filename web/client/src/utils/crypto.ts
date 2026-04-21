// utils/crypto.ts
// 登录载荷加密工具（T2 档）：WASM 加密核心 + 反调试检查。
//
// 协议（与后端 internal/util/ecdh.go 一一对齐）：
//   1. GET /auth/challenge → {serverPub, challenge, ttl}（base64url 无 padding）
//   2. 加密核心由 Rust 编译的 WASM 模块（src/wasm/）执行：
//      - 生成一次性 ECDH P-256 密钥对
//      - ECDH 协商 → HKDF-SHA256 派生 AES-256 key
//      - AES-GCM 加密 {payload, ts}，IV=12B 随机，AAD=端点常量
//   3. POST {challenge, clientPub, iv, ct}（全 base64url）
//
// T2 相比 T1 的增强：
//   - 加密核心从 JS WebCrypto 改为 WASM（逆向需 wasm-decompile/Ghidra）
//   - HKDF info 等关键常量在 WASM 内 XOR 混淆，运行时解码
//   - 加密前查 isDebugging()，命中就抛错中断请求（软反调试）

import axios from 'axios';
import initWasm, { encrypt_auth_payload, type EncryptResult } from '../wasm/crypto_wasm.js';
import { isDebugging } from './antidebug.js';

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

async function fetchChallenge(): Promise<ChallengeResponse> {
  // 用原始 axios 跳过 api 拦截器：本接口未认证，也不应触发 refresh 逻辑。
  const { data } = await axios.get<{ success: boolean; data?: ChallengeResponse; error?: string }>(
    CHALLENGE_URL,
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
  // 软反调试：检测到 DevTools/单步调试就中断当前登录请求。
  // 设计上不主动破坏用户体验——用户关闭 DevTools 重试即可。
  if (isDebugging()) {
    throw new Error('检测到调试环境，已中断本次请求');
  }

  await ensureWasm();

  const challengeResp = await fetchChallenge();
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

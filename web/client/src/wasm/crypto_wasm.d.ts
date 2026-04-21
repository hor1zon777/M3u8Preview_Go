/* tslint:disable */
/* eslint-disable */

/**
 * 加密结果，结构字段 base64url 无 padding，与后端 handler 对齐。
 */
export class EncryptResult {
    private constructor();
    free(): void;
    [Symbol.dispose](): void;
    readonly clientPub: string;
    readonly ct: string;
    readonly iv: string;
}

/**
 * 执行 ECDH + HKDF + AES-GCM 加密，返回 (clientPub, iv, ct)。
 * 参数均为明文/base64url 字符串，JS 层保持与文档约定一致的编码。
 *
 * * aad             AES-GCM AAD，端点绑定常量，如 "auth:login:v1"。
 * * server_pub_b64  服务端公钥 65B uncompressed 的 base64url。
 * * challenge_b64   服务端下发的 challenge（既是 ID 也是 HKDF salt）的 base64url。
 * * plaintext_json  明文 JSON，如 '{"username":"a","password":"b","ts":1234567890}'。
 */
export function encrypt_auth_payload(aad: string, server_pub_b64: string, challenge_b64: string, plaintext_json: string): EncryptResult;

export type InitInput = RequestInfo | URL | Response | BufferSource | WebAssembly.Module;

export interface InitOutput {
    readonly memory: WebAssembly.Memory;
    readonly __wbg_encryptresult_free: (a: number, b: number) => void;
    readonly encrypt_auth_payload: (a: number, b: number, c: number, d: number, e: number, f: number, g: number, h: number, i: number) => void;
    readonly encryptresult_clientPub: (a: number, b: number) => void;
    readonly encryptresult_ct: (a: number, b: number) => void;
    readonly encryptresult_iv: (a: number, b: number) => void;
    readonly __wbindgen_export: (a: number) => void;
    readonly __wbindgen_add_to_stack_pointer: (a: number) => number;
    readonly __wbindgen_export2: (a: number, b: number) => number;
    readonly __wbindgen_export3: (a: number, b: number, c: number, d: number) => number;
    readonly __wbindgen_export4: (a: number, b: number, c: number) => void;
}

export type SyncInitInput = BufferSource | WebAssembly.Module;

/**
 * Instantiates the given `module`, which can either be bytes or
 * a precompiled `WebAssembly.Module`.
 *
 * @param {{ module: SyncInitInput }} module - Passing `SyncInitInput` directly is deprecated.
 *
 * @returns {InitOutput}
 */
export function initSync(module: { module: SyncInitInput } | SyncInitInput): InitOutput;

/**
 * If `module_or_path` is {RequestInfo} or {URL}, makes a request and
 * for everything else, calls `WebAssembly.instantiate` directly.
 *
 * @param {{ module_or_path: InitInput | Promise<InitInput> }} module_or_path - Passing `InitInput` directly is deprecated.
 *
 * @returns {Promise<InitOutput>}
 */
export default function __wbg_init (module_or_path?: { module_or_path: InitInput | Promise<InitInput> } | InitInput | Promise<InitInput>): Promise<InitOutput>;

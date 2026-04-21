//! crypto-wasm
//!
//! 登录加密核心的 WASM 实现。与 TypeScript 实现（utils/crypto.ts 的降级路径）
//! 协议上一致：ECDH P-256 + HKDF-SHA256(info="m3u8preview-auth-v1") + AES-256-GCM。
//!
//! WASM 层只负责密码学：导入 server_pub / challenge 与明文 JSON，导出 {clientPub, iv, ct}。
//! fetch challenge 、解 JSON 响应 等 I/O 由 JS 层负责。
//!
//! 混淆策略：
//!   - HKDF info 常量 用 XOR(0xA7) 存储，运行时解码。
//!   - wasm-decompile 看到的是 XOR 还原代码 和 一串乱码字节，不是明文 "m3u8preview-auth-v1"。
//!   - AAD 由调用方传入，本模块不持有（JS 手里已有，没必要再藏）。

use aes_gcm::aead::{Aead, Payload};
use aes_gcm::{Aes256Gcm, KeyInit, Nonce};
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use hkdf::Hkdf;
use p256::ecdh::EphemeralSecret;
use p256::elliptic_curve::sec1::ToEncodedPoint;
use p256::PublicKey;
use sha2::Sha256;
use wasm_bindgen::prelude::*;

// --- 常量混淆 ---

const XOR_KEY: u8 = 0xA7;

/// HKDF info 的 XOR 编码形式。明文 = "m3u8preview-auth-v1"。
/// 每字节 = 明文 ^ 0xA7。wasm-decompile 反向时看到的是这串数字不是有意义字符串。
const HKDF_INFO_ENC: [u8; 19] = [
    0xCA, 0x94, 0xD2, 0x9F, 0xD7, 0xD5, 0xC2, 0xD1, 0xCE, 0xC2, 0xD0, 0x8A, 0xC6, 0xD2, 0xD3, 0xCF,
    0x8A, 0xD1, 0x96,
];

#[inline(never)]
fn xor_decode(enc: &[u8]) -> Vec<u8> {
    enc.iter().map(|b| b ^ XOR_KEY).collect()
}

// --- 导出结构 ---

/// 加密结果，结构字段 base64url 无 padding，与后端 handler 对齐。
#[wasm_bindgen]
pub struct EncryptResult {
    client_pub: String,
    iv: String,
    ct: String,
}

#[wasm_bindgen]
impl EncryptResult {
    #[wasm_bindgen(getter, js_name = clientPub)]
    pub fn client_pub(&self) -> String {
        self.client_pub.clone()
    }
    #[wasm_bindgen(getter)]
    pub fn iv(&self) -> String {
        self.iv.clone()
    }
    #[wasm_bindgen(getter)]
    pub fn ct(&self) -> String {
        self.ct.clone()
    }
}

// --- 导出 API ---

/// 执行 ECDH + HKDF + AES-GCM 加密，返回 (clientPub, iv, ct)。
/// 参数均为明文/base64url 字符串，JS 层保持与文档约定一致的编码。
///
/// * aad             AES-GCM AAD，端点绑定常量，如 "auth:login:v1"。
/// * server_pub_b64  服务端公钥 65B uncompressed 的 base64url。
/// * challenge_b64   服务端下发的 challenge（既是 ID 也是 HKDF salt）的 base64url。
/// * plaintext_json  明文 JSON，如 '{"username":"a","password":"b","ts":1234567890}'。
#[wasm_bindgen]
pub fn encrypt_auth_payload(
    aad: &str,
    server_pub_b64: &str,
    challenge_b64: &str,
    plaintext_json: &str,
) -> Result<EncryptResult, JsError> {
    // 1. 解 base64url
    let server_pub_raw = URL_SAFE_NO_PAD
        .decode(server_pub_b64)
        .map_err(|_| JsError::new("invalid server_pub"))?;
    let challenge = URL_SAFE_NO_PAD
        .decode(challenge_b64)
        .map_err(|_| JsError::new("invalid challenge"))?;

    let server_pub = PublicKey::from_sec1_bytes(&server_pub_raw)
        .map_err(|_| JsError::new("invalid curve point"))?;

    // 2. 客户端一次性 ECDH 密钥对
    let mut rng = rand_core::OsRng;
    let client_priv = EphemeralSecret::random(&mut rng);
    let client_pub = client_priv.public_key();

    // 3. ECDH 协商。shared 是 32B X 坐标（SharedSecret 换成 raw bytes）。
    let shared = client_priv.diffie_hellman(&server_pub);

    // 4. HKDF-SHA256(info="m3u8preview-auth-v1", salt=challenge, ikm=shared) → 32B AES key
    let info = xor_decode(&HKDF_INFO_ENC);
    let hk = Hkdf::<Sha256>::new(Some(&challenge), shared.raw_secret_bytes());
    let mut aes_key = [0u8; 32];
    hk.expand(&info, &mut aes_key)
        .map_err(|_| JsError::new("hkdf expand"))?;

    // 5. AES-256-GCM 加密
    let cipher = Aes256Gcm::new_from_slice(&aes_key).map_err(|_| JsError::new("aes key"))?;
    let mut iv_bytes = [0u8; 12];
    getrandom::getrandom(&mut iv_bytes).map_err(|_| JsError::new("rand iv"))?;
    let nonce = Nonce::from_slice(&iv_bytes);

    let ct_bytes = cipher
        .encrypt(
            nonce,
            Payload {
                msg: plaintext_json.as_bytes(),
                aad: aad.as_bytes(),
            },
        )
        .map_err(|_| JsError::new("aes encrypt"))?;

    // 6. 客户端公钥导出为 SEC1 uncompressed 65B（与 Go crypto/ecdh P256 PublicKey.Bytes() 对齐）
    let client_pub_point = client_pub.to_encoded_point(false);
    let client_pub_bytes = client_pub_point.as_bytes();

    Ok(EncryptResult {
        client_pub: URL_SAFE_NO_PAD.encode(client_pub_bytes),
        iv: URL_SAFE_NO_PAD.encode(iv_bytes),
        ct: URL_SAFE_NO_PAD.encode(&ct_bytes),
    })
}

// --- 单测仅验证混淆还原正确性；加密端到端由 Go 侧 handler test 覆盖 ---

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hkdf_info_xor_roundtrips() {
        let decoded = xor_decode(&HKDF_INFO_ENC);
        assert_eq!(decoded, b"m3u8preview-auth-v1");
    }
}

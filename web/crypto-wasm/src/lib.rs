//! crypto-wasm
//!
//! 登录加密核心的 WASM 实现。协议与后端 `internal/util/ecdh.go` 一一对齐：
//! ECDH P-256 + HKDF-SHA256(info=XOR混淆, salt=SHA256(challenge||fp)) + AES-256-GCM。
//!
//! WASM 层只负责密码学：导入 server_pub / challenge / fingerprint 与明文 JSON，
//! 导出 {clientPub, iv, ct}。fetch challenge、解 JSON 等 I/O 由 JS 层负责。
//!
//! 混淆策略：
//!   - HKDF info 常量用 XOR(0xA7) 存储，运行时解码
//!   - HKDF salt = SHA256(challenge_bytes || fingerprint_bytes)：
//!     设备指纹混入 salt，换设备 → fp 变 → AES key 不同 → 后端解密失败
//!   - AAD 由调用方传入
//!
//! T2.5 加固：
//!   - 增加 dummy 函数和 match dispatch 让 wasm-decompile 输出更混乱
//!   - 外部 wasm-opt --flatten --rse 进一步打散控制流

use aes_gcm::aead::{Aead, Payload};
use aes_gcm::{Aes256Gcm, KeyInit, Nonce};
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use hkdf::Hkdf;
use p256::ecdh::EphemeralSecret;
use p256::elliptic_curve::sec1::ToEncodedPoint;
use p256::PublicKey;
use sha2::{Digest, Sha256};
use wasm_bindgen::prelude::*;

// --- 常量混淆 ---

const XOR_KEY: u8 = 0xA7;

const HKDF_INFO_ENC: [u8; 19] = [
    0xCA, 0x94, 0xD2, 0x9F, 0xD7, 0xD5, 0xC2, 0xD1, 0xCE, 0xC2, 0xD0, 0x8A, 0xC6, 0xD2, 0xD3,
    0xCF, 0x8A, 0xD1, 0x96,
];

#[inline(never)]
fn xor_decode(enc: &[u8]) -> Vec<u8> {
    enc.iter().map(|b| b ^ XOR_KEY).collect()
}

// --- T2.5 控制流混淆辅助 ---

// dummy 函数：编译进 WASM 但运行时不执行。
// wasm-decompile 看到这些函数会增加分析复杂度。
#[inline(never)]
#[allow(dead_code)]
fn dummy_transform_a(data: &[u8]) -> Vec<u8> {
    data.iter().rev().map(|b| b.wrapping_add(0x5A)).collect()
}

#[inline(never)]
#[allow(dead_code)]
fn dummy_transform_b(data: &[u8]) -> Vec<u8> {
    data.iter()
        .enumerate()
        .map(|(i, b)| b ^ (i as u8).wrapping_mul(0x37))
        .collect()
}

// match dispatch：用数值选择器调用真实路径。
// 编译器无法静态确定分支，wasm-decompile 看到 br_table 指令。
#[inline(never)]
fn dispatch_hkdf(
    selector: u32,
    shared: &[u8],
    salt: &[u8],
    info: &[u8],
) -> Result<[u8; 32], &'static str> {
    match selector % 4 {
        0 => {
            let _ = dummy_transform_a(salt);
            compute_hkdf(shared, salt, info)
        }
        1 => compute_hkdf(shared, salt, info),
        2 => {
            let _ = dummy_transform_b(shared);
            compute_hkdf(shared, salt, info)
        }
        _ => compute_hkdf(shared, salt, info),
    }
}

#[inline(never)]
fn compute_hkdf(shared: &[u8], salt: &[u8], info: &[u8]) -> Result<[u8; 32], &'static str> {
    let hk = Hkdf::<Sha256>::new(Some(salt), shared);
    let mut out = [0u8; 32];
    hk.expand(info, &mut out).map_err(|_| "hkdf expand")?;
    Ok(out)
}

/// 混合 HKDF salt：SHA256(challenge_bytes || fingerprint_bytes)。
/// 设备指纹参与 salt 派生 → 换设备后 AES key 不同 → 后端解密失败。
#[inline(never)]
fn blend_salt(challenge: &[u8], fingerprint: &[u8]) -> [u8; 32] {
    let mut hasher = Sha256::new();
    hasher.update(challenge);
    hasher.update(fingerprint);
    let result = hasher.finalize();
    let mut out = [0u8; 32];
    out.copy_from_slice(&result);
    out
}

// --- 导出结构 ---

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
///
/// * aad              AES-GCM AAD，端点绑定常量，如 "auth:login:v1"
/// * server_pub_b64   服务端公钥 65B uncompressed 的 base64url
/// * challenge_b64    challenge salt 的 base64url
/// * fingerprint_hex  设备指纹 SHA-256 hex（64 字符），混入 HKDF salt
/// * plaintext_json   明文 JSON
#[wasm_bindgen]
pub fn encrypt_auth_payload(
    aad: &str,
    server_pub_b64: &str,
    challenge_b64: &str,
    fingerprint_hex: &str,
    plaintext_json: &str,
) -> Result<EncryptResult, JsError> {
    let server_pub_raw = URL_SAFE_NO_PAD
        .decode(server_pub_b64)
        .map_err(|_| JsError::new("invalid server_pub"))?;
    let challenge = URL_SAFE_NO_PAD
        .decode(challenge_b64)
        .map_err(|_| JsError::new("invalid challenge"))?;
    let fp_bytes = hex_decode(fingerprint_hex).map_err(|_| JsError::new("invalid fingerprint"))?;

    let server_pub =
        PublicKey::from_sec1_bytes(&server_pub_raw).map_err(|_| JsError::new("bad point"))?;

    let mut rng = rand_core::OsRng;
    let client_priv = EphemeralSecret::random(&mut rng);
    let client_pub = client_priv.public_key();
    let shared = client_priv.diffie_hellman(&server_pub);

    // salt = SHA256(challenge || fingerprint)
    let blended_salt = blend_salt(&challenge, &fp_bytes);

    let info = xor_decode(&HKDF_INFO_ENC);
    // dispatch selector 用 challenge 首字节做伪随机选择
    let selector = challenge.first().copied().unwrap_or(0) as u32;
    let aes_key = dispatch_hkdf(selector, shared.raw_secret_bytes(), &blended_salt, &info)
        .map_err(|e| JsError::new(e))?;

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

    let client_pub_point = client_pub.to_encoded_point(false);
    let client_pub_bytes = client_pub_point.as_bytes();

    Ok(EncryptResult {
        client_pub: URL_SAFE_NO_PAD.encode(client_pub_bytes),
        iv: URL_SAFE_NO_PAD.encode(iv_bytes),
        ct: URL_SAFE_NO_PAD.encode(&ct_bytes),
    })
}

fn hex_decode(s: &str) -> Result<Vec<u8>, &'static str> {
    if s.len() % 2 != 0 {
        return Err("odd hex length");
    }
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16).map_err(|_| "bad hex"))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hkdf_info_xor_roundtrips() {
        let decoded = xor_decode(&HKDF_INFO_ENC);
        assert_eq!(decoded, b"m3u8preview-auth-v1");
    }

    #[test]
    fn blend_salt_deterministic() {
        let s1 = blend_salt(b"challenge", b"fingerprint");
        let s2 = blend_salt(b"challenge", b"fingerprint");
        assert_eq!(s1, s2);
    }

    #[test]
    fn blend_salt_different_fp_different_result() {
        let s1 = blend_salt(b"challenge", b"fp_a");
        let s2 = blend_salt(b"challenge", b"fp_b");
        assert_ne!(s1, s2);
    }

    #[test]
    fn hex_decode_roundtrip() {
        let h = "deadbeef01";
        let b = hex_decode(h).unwrap();
        assert_eq!(b, vec![0xDE, 0xAD, 0xBE, 0xEF, 0x01]);
    }
}

//! crypto-wasm
//!
//! 登录加密核心的 WASM 实现。协议与后端 `internal/util/ecdh.go` 一一对齐：
//! ECDH P-256 + HKDF-SHA256(info="m3u8preview-auth-v1", salt=SHA256(challenge||fp)) + AES-256-GCM。
//!
//! WASM 层只负责密码学：导入 server_pub / challenge / fingerprint 与明文 JSON，
//! 导出 {clientPub, iv, ct}。fetch challenge、解 JSON 等 I/O 由 JS 层负责。
//!
//! 历史：之前存在一层 XOR(0xA7) 混淆常量 + dispatch match 分派 + dummy_transform_a/b。
//! 审查发现：
//!   - dummy 分支的 Vec::collect 有堆分配副作用，Rust 不会 DCE（每次登录多 2 次无用分配）
//!   - XOR(0xA7) 一字节被 wasm-decompile 一眼识破
//!   - 整体"混淆"对真实逆向无增益，对 CPU / 包体积反而负收益
//! 现在统一删除，回归直白实现；外部 wasm-opt / strip 仍可做名称/DWARF 清理。

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

const HKDF_INFO: &[u8] = b"m3u8preview-auth-v1";

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

    let aes_key = compute_hkdf(shared.raw_secret_bytes(), &blended_salt, HKDF_INFO)
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

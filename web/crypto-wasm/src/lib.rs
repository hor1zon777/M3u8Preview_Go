//! crypto-wasm
//!
//! 登录加密核心的 WASM 实现。协议与后端 `internal/util/ecdh.go` 一一对齐：
//! ECDH P-256 + HKDF-SHA256(info="m3u8preview-auth-v1", salt=challenge) + AES-256-GCM。
//!
//! WASM 层只负责密码学：导入 server_pub / challenge 与明文 JSON，
//! 导出 {clientPub, iv, ct}。fetch challenge、解 JSON 等 I/O 由 JS 层负责。
//!
//! 历史注记（2026-04）：
//!   - 早期版本有一层 XOR(0xA7) 常量混淆 + dispatch match 分派 + dummy_transform_a/b，
//!     对逆向无实际阻力但浪费 CPU/包体积 → 已删除。
//!   - 更早版本把设备指纹混入 HKDF salt（blend_salt = SHA256(challenge||fp)），
//!     对攻击者约 10 行代码即可伪造合法 fp，对合法用户（浏览器升级 / 隐身切换 /
//!     硬件变化）造成假阳性登录失败，ROI 负值 → 已删除（H8 Phase 1）。
//!     详见 docs/FINGERPRINT_REDESIGN.md。

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

const HKDF_INFO: &[u8] = b"m3u8preview-auth-v1";

#[inline(never)]
fn compute_hkdf(shared: &[u8], salt: &[u8], info: &[u8]) -> Result<[u8; 32], &'static str> {
    let hk = Hkdf::<Sha256>::new(Some(salt), shared);
    let mut out = [0u8; 32];
    hk.expand(info, &mut out).map_err(|_| "hkdf expand")?;
    Ok(out)
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
/// * challenge_b64    challenge salt 的 base64url（直接作为 HKDF salt，不再 blend）
/// * plaintext_json   明文 JSON
#[wasm_bindgen]
pub fn encrypt_auth_payload(
    aad: &str,
    server_pub_b64: &str,
    challenge_b64: &str,
    plaintext_json: &str,
) -> Result<EncryptResult, JsError> {
    let server_pub_raw = URL_SAFE_NO_PAD
        .decode(server_pub_b64)
        .map_err(|_| JsError::new("invalid server_pub"))?;
    let challenge = URL_SAFE_NO_PAD
        .decode(challenge_b64)
        .map_err(|_| JsError::new("invalid challenge"))?;

    let server_pub =
        PublicKey::from_sec1_bytes(&server_pub_raw).map_err(|_| JsError::new("bad point"))?;

    let mut rng = rand_core::OsRng;
    let client_priv = EphemeralSecret::random(&mut rng);
    let client_pub = client_priv.public_key();
    let shared = client_priv.diffie_hellman(&server_pub);

    let aes_key = compute_hkdf(shared.raw_secret_bytes(), &challenge, HKDF_INFO)
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn compute_hkdf_deterministic() {
        let k1 = compute_hkdf(b"shared", b"salt", HKDF_INFO).unwrap();
        let k2 = compute_hkdf(b"shared", b"salt", HKDF_INFO).unwrap();
        assert_eq!(k1, k2);
    }

    #[test]
    fn compute_hkdf_different_salt_different_key() {
        let k1 = compute_hkdf(b"shared", b"salt1", HKDF_INFO).unwrap();
        let k2 = compute_hkdf(b"shared", b"salt2", HKDF_INFO).unwrap();
        assert_ne!(k1, k2);
    }
}

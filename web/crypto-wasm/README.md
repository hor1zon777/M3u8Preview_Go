# crypto-wasm

登录加密核心，编译为 WebAssembly 供前端调用。与 `web/client/src/utils/crypto.ts`
搭配使用：JS 层做 IO（拉 challenge、打包 base64 envelope），WASM 层做密码学
（ECDH P-256 + HKDF-SHA256 + AES-256-GCM）。

## 协议

与后端 `internal/util/ecdh.go` 一一对齐：

1. JS 拉 `GET /auth/challenge` 得到 `{serverPub, challenge, ttl=60s}`
2. WASM 生成一次性 ECDH P-256 密钥对，与 `serverPub` 协商共享密钥
3. HKDF-SHA256(shared, salt=challenge, info="m3u8preview-auth-v1") 派生 32B AES key
4. AES-256-GCM(iv=12B 随机, aad=端点常量, plaintext=JSON) → ct(含 16B tag)
5. JS 打包 `{challenge, clientPub, iv, ct}`（全 base64url）POST

## 混淆

- HKDF info 在 Rust 里以 XOR(0xA7) 存储，运行时解码
- `wasm-decompile` 看到的是解码函数 + 乱码字节，不是明文 `"m3u8preview-auth-v1"`

## 构建

仅当修改 `src/lib.rs` 或 `Cargo.toml` 时才需要重新构建；日常开发直接使用
`web/client/src/wasm/` 下的 vendored 产物即可，**无需安装 Rust**。

### 一次性准备

```bash
# 安装 Rust + WASM target
rustup target add wasm32-unknown-unknown

# 安装 wasm-pack
cargo install wasm-pack --locked
```

### 重新构建 WASM 产物

在 `web/crypto-wasm/` 目录下：

```bash
# 单测
cargo test --lib

# 生成 vendored 产物到 web/client/src/wasm/
wasm-pack build --target web --release --out-dir ../client/src/wasm --out-name crypto_wasm

# wasm-pack 会自动生成一个 .gitignore，需删除以让产物可入库
rm ../client/src/wasm/.gitignore
```

产物：
- `crypto_wasm.js` — wasm-bindgen JS glue（动态加载 .wasm）
- `crypto_wasm_bg.wasm` — 编译后的 WebAssembly 模块
- `crypto_wasm.d.ts` — TypeScript 类型声明

### 已知限制

`wasm-pack` 自带的 `wasm-opt` 版本较旧，不支持 bulk memory 操作。本 crate
通过 `[package.metadata.wasm-pack.profile.release] wasm-opt = false` 禁用它。
体积优化靠 Rust 的 `opt-level="z" + lto + strip`，gzip 后 ~35KB。

## 代码布局

```
src/
  lib.rs       # 唯一入口：encrypt_auth_payload() 导出到 JS
```

测试覆盖 XOR 混淆还原正确性；协议端到端正确性由 Go 侧 handler test 覆盖。

# crypto-wasm

登录加密核心，编译为 WebAssembly 供前端调用。与 `web/client/src/utils/crypto.ts`
搭配使用：JS 层做 IO（拉 challenge、打包 base64 envelope），WASM 层做密码学
（ECDH P-256 + HKDF-SHA256 + AES-256-GCM）。

## 协议

与后端 `internal/util/ecdh.go` 一一对齐：

1. JS 拉 `GET /auth/challenge` 得到 `{serverPub, challenge, ttl=60s}`
2. WASM 生成一次性 ECDH P-256 密钥对，与 `serverPub` 协商共享密钥
3. HKDF-SHA256(shared, salt=SHA256(challenge || fingerprint), info="m3u8preview-auth-v1") 派生 32B AES key
4. AES-256-GCM(iv=12B 随机, aad=端点常量, plaintext=JSON) → ct(含 16B tag)
5. JS 打包 `{challenge, clientPub, iv, ct}`（全 base64url）POST

## 关于混淆（历史）

早期版本在 WASM 里加了一层 XOR(0xA7) 常量混淆 + `dispatch_hkdf` 分派 + `dummy_transform_a/b`
作为抗逆向辅助。2026-04 审查结论：
- dummy 分支里的 `Vec::collect` 有堆分配副作用，Rust 不会 DCE，每次登录都在白白分配
- 一字节 XOR 对 `wasm-decompile` 毫无拖延
- 整体对真实逆向收益为 0，对 CPU / 包体积反而负收益

因此现在直接使用字面量 `b"m3u8preview-auth-v1"`，不再做常量混淆。
真正的抗逆向靠外部 `wasm-opt --strip` 做 DWARF / 名称表清理，以及上线后的协议层监控。

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

测试覆盖 `blend_salt` 确定性与 `hex_decode` 往返；协议端到端正确性由 Go 侧 handler test 覆盖。

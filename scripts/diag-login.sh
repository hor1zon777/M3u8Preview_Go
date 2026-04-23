#!/bin/bash
# diag-login.sh — H8 Phase 1 上线后的登录链路诊断脚本
#
# 用途：不依赖前端、不依赖 WASM，直接打后端 API 看每一跳的状态码与耗时
# 必须先启动 air 或 go run，然后 bash scripts/diag-login.sh
#
# 诊断假设：
#   - 后端监听 127.0.0.1:3000（本地 air 默认）
#   - air / go run 打在 foreground，stdout 能看到日志
#
# 环境变量：BASE_URL 覆盖默认 http://127.0.0.1:3000
set -u

BASE="${BASE_URL:-http://127.0.0.1:3000}"
TS=$(date +%s)
FP=$(printf '%064d' 0)  # 64 位合法 hex

echo "=== 诊断目标：$BASE ==="
echo

# 1) healthcheck
echo "--- 1. /api/health ---"
curl -sS -o /dev/null -w "HTTP %{http_code}  time=%{time_total}s\n" "$BASE/api/health"
echo

# 2) captcha-config（登录页首屏第一条请求）
echo "--- 2. /api/v1/auth/captcha-config ---"
curl -sS -w "\nHTTP %{http_code}  time=%{time_total}s\n" "$BASE/api/v1/auth/captcha-config"
echo

# 3) register-status（登录页首屏第二条请求）
echo "--- 3. /api/v1/auth/register-status ---"
curl -sS -w "\nHTTP %{http_code}  time=%{time_total}s\n" "$BASE/api/v1/auth/register-status"
echo

# 4) challenge 签发（Phase 1 已不参与 key 派生，但仍需要 hex 64 字符）
echo "--- 4. /api/v1/auth/challenge ---"
RESP=$(curl -sS -X POST "$BASE/api/v1/auth/challenge" \
  -H 'Content-Type: application/json' \
  -d "{\"fingerprint\":\"$FP\"}" \
  -w "\n__HTTP_%{http_code}__time=%{time_total}s")
echo "$RESP"
echo

# 5) 带非法 fingerprint 试错，应返 400
echo "--- 5. /api/v1/auth/challenge（非 hex，应 400） ---"
curl -sS -X POST "$BASE/api/v1/auth/challenge" \
  -H 'Content-Type: application/json' \
  -d '{"fingerprint":"not-hex-garbage-but-64-chars-long-so-length-ok-in-theory-xxxx"}' \
  -w "\nHTTP %{http_code}  time=%{time_total}s\n"
echo

# 6) 登录端点 reachability（不送密文，只看路由是否在）
echo "--- 6. /api/v1/auth/login POST 空 body（应 400） ---"
curl -sS -X POST "$BASE/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{}' \
  -w "\nHTTP %{http_code}  time=%{time_total}s\n"
echo

echo "=== 诊断完成 ==="
echo
echo "判读:"
echo "  - 1/2/3 全 200 → 后端活着"
echo "  - 4 返回 {serverPub, challenge, ttl} 且 HTTP 200 → 加密协议 Phase 1 后端正常"
echo "  - 4 time > 1s → air 还没热重载完 或 DB 查询卡住，去看 go 日志"
echo "  - 4 返回 503/500 → challenge store 异常，看 go 日志"
echo "  - 5 返回 400 → DTO hex 校验在生效"
echo "  - 6 返回 400 '请求无效' → 路由正常"
echo "  - 2 time > 3s → captcha 服务不可达，widget 会卡在 SRI manifest 拉取"

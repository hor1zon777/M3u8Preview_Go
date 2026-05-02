# Worker Protocol — m3u8-preview-go 远程字幕 Worker 契约

本文档描述 `m3u8-preview-go`（服务器）与远程字幕 Worker（如 `m3u8-preview-worker` Tauri 桌面端）之间的 HTTP API 契约。

- **协议版本**：v1（路径前缀 `/api/v1`）
- **传输**：HTTPS（生产环境必须；开发环境允许 HTTP）
- **鉴权**：所有 worker 端点需要 `Authorization: Bearer mwt_xxx`
- **数据格式**：除 `complete` 端点为 multipart 外，其余均为 `application/json`

---

## 1. 鉴权

### 1.1 Token 获取

Token 由服务器管理员在 admin 面板「字幕管理 → Worker Token 管理」中生成。

- **格式**：`mwt_<base32 32 字符>`，例如 `mwt_abc12345defghi67890jklmnopqrstuv`
- **明文仅在创建时返回一次**，服务器存储 bcrypt(token) + 前 12 位前缀
- 吊销走 soft delete（`revoked_at`），保留审计记录

### 1.2 请求头

所有 `/api/v1/worker/*` 端点必须包含：

```
Authorization: Bearer mwt_xxx
```

### 1.3 鉴权失败响应

| 场景 | HTTP 状态 | 响应体 |
|------|---------|--------|
| 缺失 Authorization 头 | 401 | `{"success":false,"message":"missing authorization header"}` |
| 格式不对（非 `Bearer mwt_xxx`） | 401 | `{"success":false,"message":"invalid authorization format"}` |
| token 已被吊销 | 401 | `{"success":false,"message":"token revoked"}` |
| token 不存在或不匹配 | 401 | `{"success":false,"message":"invalid token"}` |

---

## 2. Worker 生命周期

```
启动
  ↓
POST /worker/register        ← 上报 workerId / name / gpu
  ↓
循环：
  ↓
POST /worker/claim           ← 拉一个 PENDING；无任务返 204
  ↓ (有任务)
POST /worker/jobs/:id/heartbeat  ← 每 stage 切换或 ≥10s 上报
  ↓ (重复)
POST /worker/jobs/:id/complete   ← 上传 VTT
  ↓ 或
POST /worker/jobs/:id/fail       ← 失败上报
```

**关键约束**：

- `workerId` 由 worker 客户端首次启动时本地生成（UUID v4），重启复用，**不要**每次启动改
- 心跳间隔应 < 服务器返回的 `workerStaleThreshold`（默认 10 分钟），推荐 30 秒
- 同一时刻一个 worker 只能持有一个任务（`claim` 成功 → 必须 `complete`/`fail` 后才能再 `claim`）

---

## 3. 端点详解

### 3.1 POST /api/v1/worker/register

Worker 启动时调用，把自身信息 upsert 到服务器。同一 `workerId` 多次注册会更新 `name/version/gpu/last_seen_at`。

**请求体**：

```json
{
  "workerId": "550e8400-e29b-41d4-a716-446655440000",
  "name": "DESKTOP-GPU01",
  "version": "0.1.0",
  "gpu": "NVIDIA GeForce RTX 4070 (CUDA 12.4)"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `workerId` | string | 是 | 客户端本地生成的 UUID v4，重启复用 |
| `name` | string | 是 | 主机名 / 用户自定义名（最多 64 字符） |
| `version` | string | 否 | worker 版本号（用于诊断兼容性问题） |
| `gpu` | string | 否 | 显卡型号 + CUDA 版本 |

**响应**（200）：

```json
{
  "success": true,
  "data": {
    "workerId": "550e8400-e29b-41d4-a716-446655440000",
    "serverTime": 1731234567890,
    "workerStaleThreshold": 600
  }
}
```

| 字段 | 说明 |
|------|------|
| `serverTime` | unix 毫秒，worker 用来对时（处理时钟漂移） |
| `workerStaleThreshold` | 服务器容忍的心跳间隔（秒），worker 心跳应 < 此值 |

---

### 3.2 POST /api/v1/worker/claim

原子认领一条 PENDING 任务。服务器用 `UPDATE WHERE status='PENDING' AND claimed_by=''` (CAS) 保证多 worker 并发下只有一个会拿到。

**请求体**：

```json
{
  "workerId": "550e8400-e29b-41d4-a716-446655440000"
}
```

**响应 A：有任务（200）**：

```json
{
  "success": true,
  "data": {
    "jobId": "01HVA7BKD1...",
    "mediaId": "abc123",
    "mediaTitle": "示例媒体",
    "m3u8Url": "https://cdn.example.com/path/index.m3u8",
    "sourceLang": "ja",
    "targetLang": "zh"
  }
}
```

**响应 B：无任务（204 No Content）**：

无响应体。worker 应 sleep 5~10 秒后重试。

**worker 行为约定**：

- 收到 `ClaimedJob` 后，**必须**最终调用 `complete` 或 `fail` 之一，否则任务在 `workerStaleThreshold` 后会被服务器自动重置回 PENDING
- `m3u8Url` 已含必要的查询参数和 token（如有）；worker 直接用 N_m3u8DL-RE 或 ffmpeg 拉即可
- `sourceLang` / `targetLang` 可能为 `"auto"`，worker 端做对应处理

---

### 3.3 POST /api/v1/worker/jobs/:jobId/heartbeat

上报阶段 + 进度。服务器根据此更新 `subtitle_jobs.stage` / `progress` / `last_heartbeat_at`，admin 面板和播放页通过此值显示进度。

**请求体**：

```json
{
  "workerId": "550e8400-e29b-41d4-a716-446655440000",
  "stage": "asr",
  "progress": 65
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `workerId` | string | 是 | 当前持有任务的 worker id（必须与 claim 时一致） |
| `stage` | string | 是 | 当前阶段，见下表 |
| `progress` | int | 是 | 整体进度 0..99（100 由 complete 隐式置位） |

**Stage 取值**：

| 值 | 含义 | 推荐进度区间 |
|------|------|------|
| `queued` | 已认领待开始 | 0~5 |
| `extracting` | 下载 + 抽音频 | 5~40 |
| `asr` | Whisper 识别 | 40~80 |
| `translate` | LLM 翻译 | 80~95 |
| `writing` | 拼 WebVTT | 95~99 |
| `done` | 完成（仅 complete 内部使用，worker 不直接报） | 100 |

**响应（200）**：

```json
{ "success": true }
```

**错误**：

| HTTP | 场景 | 响应 |
|------|------|------|
| 410 Gone | 任务不再属于此 worker（被 stale 回收 / 被别的 worker 接走） | worker 应放弃此任务 |
| 400 | stage 取值不合法 / progress 越界 | worker 应修复后重试 |
| 404 | jobId 不存在 | 任务可能已被 admin 删除 |

---

### 3.4 POST /api/v1/worker/jobs/:jobId/complete

上传 VTT 文件并标记任务 DONE。

**请求**：`multipart/form-data`

| 字段 | 类型 | 说明 |
|------|------|------|
| `meta` | string (JSON) | `WorkerCompleteMeta` JSON 字符串 |
| `vtt` | file | WebVTT 文件，UTF-8 编码，<= 10MB |

`meta` 结构：

```json
{
  "workerId": "550e8400-e29b-41d4-a716-446655440000",
  "segmentCount": 156,
  "asrModel": "whisper-large-v3",
  "mtModel": "deepseek-chat"
}
```

**curl 示例**：

```bash
curl -X POST "$SERVER/api/v1/worker/jobs/$JOB_ID/complete" \
  -H "Authorization: Bearer mwt_xxx" \
  -F "meta={\"workerId\":\"$WORKER_ID\",\"segmentCount\":156,\"asrModel\":\"whisper-large-v3\",\"mtModel\":\"deepseek-chat\"}" \
  -F "vtt=@output.vtt"
```

**响应（200）**：

```json
{ "success": true }
```

**错误**：

| HTTP | 场景 |
|------|------|
| 400 | meta 字段缺失 / JSON 格式错 / vtt 文件缺失 |
| 410 Gone | 任务不再属于此 worker |
| 409 Conflict | 任务不在 RUNNING 状态（已被取消 / 已完成） |
| 413 | VTT 文件超过 10MB |

**WebVTT 格式要求**：

- 第一行必须为 `WEBVTT`
- 时间格式 `HH:MM:SS.mmm`（毫秒部分必填）
- 双语字幕推荐用 `<v 原文>` 标签包裹原文行：

```
WEBVTT

00:00:01.000 --> 00:00:03.500
<v 原文>こんにちは、世界
你好，世界

00:00:04.000 --> 00:00:06.200
<v 原文>これはテストです
这是测试
```

---

### 3.5 POST /api/v1/worker/jobs/:jobId/fail

上报失败，任务状态改为 FAILED，清空 claimed_by。

**请求体**：

```json
{
  "workerId": "550e8400-e29b-41d4-a716-446655440000",
  "errorMsg": "ffmpeg start: ffmpeg not found in PATH"
}
```

`errorMsg` 最多 2000 字符，超出会被截断。建议包含：失败的阶段、底层错误原因、关键参数（不含密钥）。

**响应（200）**：

```json
{ "success": true }
```

**错误**：

| HTTP | 场景 |
|------|------|
| 410 Gone | 任务不再属于此 worker |

---

## 4. 错误响应统一格式

所有非 2xx 响应均为：

```json
{
  "success": false,
  "message": "human-readable error",
  "code": "OPTIONAL_ERROR_CODE"
}
```

`code` 字段在以下场景出现：

| code | 含义 |
|------|------|
| `WORKER_TOKEN_REVOKED` | token 已吊销 |
| `WORKER_TOKEN_INVALID` | token 不存在或不匹配 |
| `WORKER_JOB_NOT_OWNED` | 任务不属于此 worker |
| `WORKER_JOB_NOT_RUNNING` | 任务不在 RUNNING |

---

## 5. Worker 实现要点（推荐）

### 5.1 配置项（worker 端）

| 配置 | 默认值 | 说明 |
|------|------|------|
| `server_url` | — | 服务器 base URL，例如 `https://m3u8.example.com` |
| `worker_token` | — | `mwt_xxx` |
| `worker_id` | UUID v4（首次启动生成） | 持久化到本地配置 |
| `worker_name` | 主机名 | UI 可改 |
| `poll_interval` | 5s | claim 间隔 |
| `heartbeat_interval` | 30s | 心跳间隔（必须 < 服务器返回的 `workerStaleThreshold`） |
| `claim_backoff_on_error` | 15s | claim 失败时的重试间隔 |

### 5.2 错误恢复

- **网络断开**：claim/heartbeat 失败时不放弃当前 job，本地保留状态；网络恢复后继续上报
- **服务器 5xx**：使用指数退避重试（1s → 2s → 4s → 最多 60s）
- **410 Gone**：立即放弃当前 job，回到 claim 循环
- **进程重启**：`workerId` 不变；如有未完成任务，由服务器 stale 回收机制处理（不在客户端尝试恢复）

### 5.3 安全建议

- **TLS 证书校验**：生产环境必须开启
- **token 存储**：使用系统密钥环（Windows Credential Manager / macOS Keychain），不要明文写到配置文件
- **日志脱敏**：日志中绝不输出完整 token；前 12 位前缀可用于排查

---

## 6. 演进策略

- 新增字段使用 `omitempty`，旧 worker 可继续工作
- 删除字段需提前 1 个版本 deprecated 标记
- Stage 新增不破坏：worker 收到不识别的 stage 时按 `running` 处理即可
- 协议大版本变更（v2）时使用 `/api/v2/worker/*` 路径，与 v1 共存一段时间

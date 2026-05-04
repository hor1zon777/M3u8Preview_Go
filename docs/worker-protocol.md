# Worker Protocol — m3u8-preview-go 远程 Worker 契约

本文档描述 `m3u8-preview-go`（服务器）与远程 Worker（`m3u8-preview-worker` 字幕 Worker、`m3u8-preview-audio-worker` 音频 Worker）之间的 HTTP API 契约。

- **协议版本**：v3（路径前缀 `/api/v1`）
- **传输**：HTTPS（生产环境必须；开发环境允许 HTTP）
- **鉴权**：所有 worker 端点需要 `Authorization: Bearer mwt_xxx`
- **数据格式**：除 `complete` 与 `audio-stream` 外均为 `application/json`

> v3 关键变更（相对 v1/v2）：
> - **Capabilities**：worker 注册时声明能力（`audio_extract` / `asr_subtitle`），服务端按 capability 派活
> - **零落盘 broker**：FLAC 留在 audio worker 本地，subtitle worker 拉取时由服务端实时桥接 audio worker 的上传流（io.Pipe），服务端不持久化音频文件
> - **新协议端点**：`audio-ready`（仅元数据）/ `audio-fetch-poll`（long-poll）/ `audio-stream`（流式上传到 broker）
> - **删除**：v2 的 `audio-complete`（multipart 上传）端点已下线

---

## 1. 鉴权

### 1.1 Token 获取

Token 由服务器管理员在 admin 面板「字幕管理 → Worker Token 管理」中生成。

- **格式**：`mwt_<base32 32 字符>`，例如 `mwt_abc12345defghi67890jklmnopqrstuv`
- **明文仅在创建时返回一次**，服务器存储 bcrypt(token) + 前 12 位前缀
- 吊销走 soft delete（`revoked_at`），保留审计记录
- v3 新增：每个 token 可分别配置 `maxConcurrency`（兜底）/ `maxAudioConcurrency`（默认 2）/ `maxSubtitleConcurrency`（默认 1）

### 1.2 请求头

所有 `/api/v1/worker/*` 端点必须包含：

```
Authorization: Bearer mwt_xxx
```

### 1.3 鉴权失败响应

| 场景 | HTTP 状态 | 响应体 |
|------|---------|--------|
| 缺失 Authorization 头 | 401 | `{"success":false,"error":"Worker authentication required"}` |
| 格式不对（非 `Bearer mwt_xxx`） | 401 | `{"success":false,"error":"Invalid worker token format"}` |
| token 已被吊销 | 401 | `{"success":false,"error":"Invalid worker token"}` |
| token 不存在或不匹配 | 401 | `{"success":false,"error":"Invalid worker token"}` |

---

## 2. Worker 生命周期

### 2.1 audio_extract worker（机 A）

```
启动
  ↓
[启动时] 扫 audio_storage_dir 本地遗留 FLAC → 调 audio-ready 重新注册
  ↓
POST /worker/register (capabilities=["audio_extract"])
  ↓
并行运行两个循环：
  ┌── claim 循环 ────────────────┐    ┌── fetch poll 循环 ────────────┐
  │ POST /worker/claim           │    │ POST /worker/audio-fetch-poll │
  │   ↓ (audio_extract 任务)     │    │   ↓ (无任务则 204 立即重 poll)│
  │ heartbeat 流                 │    │ ↓ (action=fetch)              │
  │ ↓                            │    │ POST /worker/jobs/:id/audio-  │
  │ POST /worker/jobs/:id/audio- │    │      stream（流式上传）        │
  │      ready（仅元数据）        │    │ ↓ (action=cleanup)            │
  │ → 任务进 audio_uploaded      │    │ 删除本地 FLAC + 索引          │
  └──────────────────────────────┘    └───────────────────────────────┘
```

### 2.2 asr_subtitle worker（机 B）

```
启动
  ↓
POST /worker/register (capabilities=["asr_subtitle"])
  ↓
循环：
POST /worker/claim          ← 拉 audio_uploaded 任务
  ↓
GET /worker/jobs/:id/audio  ← broker 流式拉 FLAC（服务端 30s 等 audio worker 上传）
  ↓
ASR + 翻译 + 写 VTT
  ↓
POST /worker/jobs/:id/heartbeat / complete / fail
```

**关键约束**：

- `workerId` 由 worker 客户端首次启动时本地生成（UUID v4），重启复用
- 心跳间隔应 < 服务器返回的 `workerStaleThreshold`（默认 10 分钟），推荐 30 秒
- audio worker 必须维护 long-poll 才能接收 fetch / cleanup 通知（**离线意味着持有的 FLAC 暂时不可达**）
- subtitle worker 拉 FLAC 的客户端 timeout 建议 ≥ 90s（broker 上限 30s + 上传裕量）

---

## 3. 端点详解

### 3.1 POST /api/v1/worker/register

Worker 启动时调用，把自身信息 upsert 到服务器。同一 `workerId` 多次注册会更新 `name/version/gpu/capabilities/last_seen_at`。

**请求体**：

```json
{
  "workerId": "550e8400-e29b-41d4-a716-446655440000",
  "name": "DESKTOP-GPU01",
  "version": "0.1.0",
  "gpu": "NVIDIA GeForce RTX 4070 (CUDA 12.4)",
  "capabilities": ["audio_extract"]
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `workerId` | string | 是 | 客户端本地生成的 UUID v4，重启复用 |
| `name` | string | 是 | 主机名 / 用户自定义名（最多 64 字符） |
| `version` | string | 否 | worker 版本号 |
| `gpu` | string | 否 | 显卡型号 + CUDA 版本（仅 subtitle worker 有意义） |
| `capabilities` | string[] | 否 | v3 新增；缺省时按 `["audio_extract","asr_subtitle"]` 兼容旧 client |

合法 capability：`audio_extract` / `asr_subtitle`。未识别值会被静默丢弃。

**响应**（200）：

```json
{
  "success": true,
  "data": {
    "workerId": "550e8400-e29b-41d4-a716-446655440000",
    "serverTime": 1731234567890,
    "workerStaleThreshold": 600,
    "maxConcurrentTasks": 0,
    "acceptedCapabilities": ["audio_extract"]
  }
}
```

| 字段 | 说明 |
|------|------|
| `serverTime` | unix 毫秒，worker 用来对时 |
| `workerStaleThreshold` | 服务器容忍的心跳间隔（秒） |
| `maxConcurrentTasks` | 服务端配置的最大并发；0 = 服务端没强制，client 用本地配置 |
| `acceptedCapabilities` | v3 新增；服务端实际接受的能力集合，client 应据此 sanity check |

---

### 3.2 POST /api/v1/worker/claim

按 worker capabilities 派活。优先级：subtitle 任务（GPU 稀缺先满负荷）> audio 任务。

**请求体**：

```json
{ "workerId": "550e8400-e29b-41d4-a716-446655440000" }
```

**响应（有任务，audio_extract）**（200）：

```json
{
  "success": true,
  "data": {
    "jobId": "abc-123",
    "mediaId": "media-789",
    "mediaTitle": "示例视频",
    "stage": "audio_extract",
    "m3u8Url": "https://source.example.com/video.m3u8",
    "headers": {
      "Referer": "https://source.example.com/",
      "User-Agent": "Mozilla/5.0..."
    },
    "sourceLang": "ja",
    "targetLang": "zh"
  }
}
```

**响应（有任务，asr_subtitle）**（200）：

```json
{
  "success": true,
  "data": {
    "jobId": "abc-123",
    "mediaId": "media-789",
    "mediaTitle": "示例视频",
    "stage": "asr_subtitle",
    "audioArtifactUrl": "https://media.example.com/api/v1/worker/jobs/abc-123/audio",
    "audioArtifactSize": 52428800,
    "audioArtifactSha256": "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
    "audioArtifactFormat": "flac",
    "audioArtifactDurationMs": 3600000,
    "sourceLang": "ja",
    "targetLang": "zh"
  }
}
```

**响应（无任务）**：HTTP 204 No Content（无 body）

字段使用约定：

- `stage="audio_extract"`：返回 `m3u8Url` + `headers`，audio worker 自行下载 + 抽音 + 编码 FLAC
- `stage="asr_subtitle"`：返回 `audioArtifactUrl` + 完整性元数据；subtitle worker 用 `?workerId=<id>` 访问该 URL，服务端 broker 桥接 audio worker 实时上传

---

### 3.3 POST /api/v1/worker/jobs/:jobId/heartbeat

上报阶段进度。服务端校验 `claimed_by == workerId AND status=RUNNING`，校验失败返回 410（说明任务已被 stale 回收，worker 应放弃）。

**请求体**：

```json
{
  "workerId": "550e8400-...",
  "stage": "encoding_intermediate",
  "progress": 80
}
```

| 字段 | 取值 |
|------|------|
| `stage` | `queued` / `downloading` / `extracting` / `encoding_intermediate` / `audio_uploaded` / `asr` / `translate` / `writing` / `done`（v3 完整集合）|
| `progress` | 0..99（complete 时服务端写 100，worker 不应 ≥100） |

合法 stage 集合（v3）：

| Stage | 用途 | 持有者 |
|-------|------|--------|
| `queued` | 待 audio_extract worker 抢占 | — |
| `downloading` | audio worker 拉 m3u8 切片 | audio worker |
| `extracting` | ffmpeg 抽音 PCM WAV | audio worker |
| `encoding_intermediate` | WAV → FLAC | audio worker |
| `audio_uploaded` | FLAC 已注册元数据，等 subtitle worker | — |
| `asr` | whisper-cli ASR | subtitle worker |
| `translate` | LLM 翻译 | subtitle worker |
| `writing` | 写 VTT | subtitle worker |
| `done` | 完成 | — |

非法 stage 返回 400。

---

### 3.4 POST /api/v1/worker/jobs/:jobId/audio-ready 🆕 v3

**audio worker 完成本地 FLAC 编码后调用**。仅注册元数据，不上传文件 body。

服务端校验：`claimed_by == workerId AND stage ∈ {downloading, extracting, encoding_intermediate}`，校验通过后切到 `stage=audio_uploaded`。

> **v3.1 放宽**：之前要求 `stage == encoding_intermediate`，但 audio worker 心跳间隔默认 30s，
> 流水线从 `extracting` → `encoding_intermediate` → FLAC 编码 → `audio_ready` 整段通常 < 30s，
> 心跳容易在 stage 切换之间被读到旧值，导致 `audio_ready` 被 409 拒绝。
> v3.1 起 stage 仅作进度展示，audio_ready 接受任意 audio 阶段子状态作为来源。
> Worker 仍建议在调用 `audio_ready` 前同步发一次 `heartbeat(stage=encoding_intermediate)`
> 让心跳与最终状态对齐。

**请求体**：

```json
{
  "workerId": "550e8400-...",
  "size": 52428800,
  "sha256": "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
  "format": "flac",
  "durationMs": 3600000
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `size` | int64 | FLAC 字节数 |
| `sha256` | string | hex 小写，64 字符 |
| `format` | string | `flac` / `opus_24k` / `wav`（当前仅实现 flac）|
| `durationMs` | int64 | 音频时长（毫秒） |

**响应**：`{"success":true}` (200) / `409 WORKER_AUDIO_NOT_READY`（stage 不允许）/ `410 WORKER_JOB_NOT_OWNED`

---

### 3.5 POST /api/v1/worker/jobs/:jobId/audio-lost 🆕 v3.1

**audio worker 在收到 broker fetch 通知后发现本地 FLAC 已丢失时调用**。让服务端把任务回滚到 `queued`，避免 broker 反复派发同一个 fetch 死循环。

服务端校验：

| 项 | 期望 |
|---|---|
| `audio_worker_id` | 必须 == 请求体的 `workerId`（FLAC owner） |
| `stage` | 必须 ∈ `{audio_uploaded, asr, translate, writing}` |

校验通过后：清空 `audio_artifact_*`、`audio_worker_id`、`subtitle_worker_id`、`claimed_by`，状态回到 `PENDING/queued`。

> 为何 stage 不止 `audio_uploaded`：fetch 任务是 `subtitle worker GET /audio` 触发 broker `EnqueueFetch` 后才派给 audio worker 的，而 subtitle worker `claim` 时已把 stage 从 `audio_uploaded` 推进到 `asr`。所以 fetch 派发瞬间 stage 通常是 `asr` 而非 `audio_uploaded`，必须放宽到全部 subtitle 阶段才能让 audio-lost 真正走通。
>
> 与 `fail` 的区别：`fail` 校验 `claimed_by == workerId`，但 `audio_ready` 之后 `claimed_by` 短暂为空（随即被 subtitle worker 重新占用）；audio worker 此时是通过 `audio_worker_id` 标识 owner，必须用本端点反馈丢失。

**副作用**：subtitle worker 那边的 `GET /audio` 因 broker 30s 超时返回 503；其后续 `fail` 调用会因 `claimed_by` 已清空被 410 拒绝（subtitle worker runner 抛 `WorkerJobLost` 优雅退出，不致命）。

**请求体**：

```json
{
  "workerId": "550e8400-...",
  "errorMsg": "no local FLAC for job=... (storage_dir=C:\\..., entries=0)"
}
```

**响应**：`{"success":true}` (200) / `404 not found` / `409 WORKER_AUDIO_NOT_READY`（stage 不在允许集合中）/ `410 WORKER_AUDIO_LOST_NOT_OWNED`

---

### 3.6 POST /api/v1/worker/audio-fetch-poll 🆕 v3

**audio worker 长轮询**：等待服务端下发 fetch / cleanup 指令。这是 v3 broker 模式的核心通知通道。

**请求体**：

```json
{
  "workerId": "550e8400-...",
  "timeoutSec": 25
}
```

| 字段 | 取值 | 说明 |
|------|------|------|
| `timeoutSec` | 5..60 | 客户端可接受的最长 hold 时长，服务端会 clamp |

**响应（有任务）**（200）：

```json
{ "success": true, "data": { "action": "fetch", "jobId": "abc-123" } }
```

**响应（无任务）**：HTTP 204 No Content（hold 满 timeoutSec 后），audio worker 应立即重新 poll。

| `action` | 含义 | audio worker 应做什么 |
|----------|------|------------------------|
| `fetch` | 有 subtitle worker 在等这个 job 的 FLAC | 调 audio-stream 把本地 FLAC 流式上传 |
| `cleanup` | 任务已 DONE / stale 回收 | 删除本地 jobId.flac + 索引项 |

---

### 3.6 POST /api/v1/worker/jobs/:jobId/audio-stream 🆕 v3

**audio worker 收到 fetch 通知后，把本地 FLAC 流式推送到服务端 broker**。服务端把 body 实时 io.Copy 到等待中的 subtitle worker GET response。

**请求**：

- Content-Type: `audio/flac`
- Content-Length: FLAC 字节数（必填，让服务端做 LimitReader）
- X-Worker-Id: 上传方 audio worker id（用于 ownership 校验）
- Body: 裸 FLAC 字节流（**不是** multipart）

**响应**：

| HTTP | Code | 含义 |
|------|------|------|
| 200 | — | 上传完成（service.io.Copy 到 broker pipe writer 完成）|
| 400 | — | 缺 X-Worker-Id |
| 403 | `WORKER_JOB_NOT_OWNED` | 不是该任务的 owner audio worker |
| 410 | `WORKER_AUDIO_GONE` | 没人在等这个 jobId（subtitle worker 已超时离开）|
| 413 | — | body > 1 GB |

---

### 3.7 GET /api/v1/worker/jobs/:jobId/audio?workerId=... 🆕 v3

**subtitle worker 拉 FLAC**。服务端 broker 协调：通知 audio worker 上传 → 用 io.Pipe 把 audio worker 的 audio-stream POST body 实时转发到本响应。

**请求**：

- Query: `workerId=<subtitle worker id>`（必填，做 ownership 校验）
- Method: GET
- 不支持 Range（broker 模式一次性流式）

**响应**：

- 200：Content-Type: `audio/flac`，Transfer-Encoding: chunked，body 是 FLAC 数据流
- 503 `WORKER_AUDIO_OWNER_OFFLINE`：audio worker 30s 内未响应 broker 通知
- 410 `WORKER_AUDIO_GONE`：任务的 audio_worker_id 字段为空（任务还未到 audio_uploaded）
- 403 `WORKER_JOB_NOT_OWNED`：subtitle worker 没 claim 这个 job

**客户端建议**：

- HTTP timeout 至少 90 秒（30s broker 等 + 上传裕量）
- 流式 chunked 边读边算 SHA256，与 ClaimedJob 中 `audioArtifactSha256` 比对
- 收到 503 → 上报 fail（任务回 audio_uploaded，等其它 audio worker 上线 / 同 audio worker 重新登场后重试）

---

### 3.8 POST /api/v1/worker/jobs/:jobId/complete

subtitle worker 上传 VTT 文件。

**请求**（multipart/form-data）：

| Part | Content-Type | 内容 |
|------|--------------|------|
| `meta` | text/plain | JSON：`{"workerId":"...","segmentCount":120,"asrModel":"medium","mtModel":"deepseek"}` |
| `vtt` | text/vtt | VTT 文件（≤ 10 MB）|

**响应**：`{"success":true}` (200)

成功后服务端：
1. 写 VTT 到 `<UploadsDir>/subtitles/<mediaId>.vtt`
2. 任务进 status=DONE / stage=done
3. **v3 新行为**：通过 broker long-poll 通道下发 `cleanup` 指令给 owner audio worker（让它删本地 FLAC）

---

### 3.9 POST /api/v1/worker/jobs/:jobId/fail

worker 上报失败。服务端按当前 stage 决定回滚目标（**v3 不再依赖 FLAC 文件存在性**）：

| 失败时 stage | 回滚到 |
|--------------|--------|
| audio 阶段（downloading / extracting / encoding_intermediate）| status=PENDING / stage=queued，audio_worker_id 清空 |
| subtitle 阶段（asr / translate / writing），audio_worker_id 仍存在 | stage=audio_uploaded（让其它 subtitle worker 重试）|
| subtitle 阶段，audio_worker_id 已丢失 | status=PENDING / stage=queued，整条任务回到起点 |
| 未知 stage | status=FAILED（终态）|

中间态回滚不计 `failed_jobs`；只有终态 FAILED 才计入。

**请求体**：

```json
{ "workerId": "550e8400-...", "errorMsg": "ffmpeg exit 1: ..." }
```

---

### 3.10 POST /api/v1/worker/media/:mediaId/retry

让服务端把指定 mediaId 的字幕任务重置为 PENDING；不存在则按 EnsureJob 创建。

worker token 是高权限凭据，允许触发重试（与 admin Retry 等价）。

---

## 4. 错误码

服务端在响应 `code` 字段中可能返回的机器可读错误码：

| Code | HTTP | 含义 |
|------|------|------|
| `WORKER_JOB_NOT_OWNED` | 403/410 | claimed_by / audio_worker_id 不匹配请求方 |
| `WORKER_AUDIO_LOST_NOT_OWNED` | 410 | audio-lost 调用方不是当前 audio_worker_id |
| `WORKER_AUDIO_NOT_READY` | 409 | audio-ready 时 stage 不允许（v3.1 起允许 downloading / extracting / encoding_intermediate）|
| `WORKER_AUDIO_GONE` | 410 | audio_worker_id 为空 / 没人在等 fetch |
| `WORKER_AUDIO_SHA256_MISMATCH` | 412 | 保留（v3 不再校验，但常量保留） |
| `WORKER_AUDIO_OWNER_OFFLINE` | 503 | broker 30s 内未收到 audio worker 上传 |
| `WORKER_AUDIO_STREAM_STUCK` | 504 | 保留（broker 内部），表示 audio worker 通知后仍未上传 |
| `WORKER_CAPABILITY_MISMATCH` | 403 | worker 上报 capability 超出 token 允许（暂未触发，预留） |

---

## 5. 完整时序示例

### 5.1 单台 audio worker + 单台 subtitle worker，跑通一条任务

```
T=0    audio worker  POST /register caps=[audio_extract]                → 200
T=0    subtitle wrkr POST /register caps=[asr_subtitle]                  → 200
T=0    audio worker  POST /audio-fetch-poll (hold)                       → 等待
T=0    subtitle wrkr POST /claim                                          → 204 (没 audio_uploaded 任务)
T=1    admin 触发新任务 → DB 写入 status=PENDING / stage=queued
T=2    audio worker  POST /claim                                          → 200 stage=audio_extract
T=2~30 audio worker  POST /heartbeat (downloading 5%→50%)
T=30   audio worker  POST /heartbeat (extracting 50%→70%)
T=35   audio worker  POST /heartbeat (encoding_intermediate 70%→90%)
T=40   audio worker  写本地 FLAC → POST /audio-ready                      → 200, stage=audio_uploaded
T=42   subtitle wrkr POST /claim                                          → 200 stage=asr_subtitle
T=42   subtitle wrkr GET  /jobs/abc/audio (hold by broker)
T=42   服务端 broker push fetch task 到 audio worker poll 通道
T=42   audio worker  POST /audio-fetch-poll                              → 200 {action=fetch, jobId=abc}
T=42   audio worker  POST /jobs/abc/audio-stream (流式 50MB FLAC)
T=42~47 服务端 broker io.Copy: stream body → GET response (subtitle worker 实时收)
T=48   subtitle wrkr 收到完整 50MB + sha256 校验通过 → 解码 → ASR → 翻译 → VTT
T=120  subtitle wrkr POST /complete                                       → 200, stage=done
T=120  服务端 broker push cleanup task 到 audio worker poll 通道
T=120  audio worker  POST /audio-fetch-poll                              → 200 {action=cleanup, jobId=abc}
T=120  audio worker  删本地 FLAC + 索引项
```

### 5.2 audio worker 重启场景

```
T=0    audio worker 完成 FLAC + audio-ready → 任务 stage=audio_uploaded
T=10   audio worker 重启
T=11   audio worker 启动时扫 audio_storage_dir/*.flac+*.json
T=11   audio worker 对每个本地 FLAC 调 POST /audio-ready
        - 服务端任务仍在 encoding_intermediate（rare race）→ 200
        - 服务端任务已 audio_uploaded → 410 WORKER_JOB_NOT_OWNED
          → audio worker 删本地（任务可能已被新的 audio worker 接手）
        - 服务端 stale recovery 把任务回 queued → 410（同上，删本地）
T=12   audio worker 进入正常 long-poll 循环
```

### 5.3 audio worker 离线时 subtitle worker 拉取

```
T=0    audio worker  POST /audio-fetch-poll (hold)
T=0    audio worker  网络中断
T=10   subtitle wrkr GET /jobs/abc/audio?workerId=...
T=10   服务端 broker push fetch task → audio worker 的通道（buffered，不阻塞）
T=10   服务端 broker hold GET 30s 等 audio worker 上传
T=40   服务端 broker firstByteTimeout 触发
T=40   subtitle wrkr 收到 503 + WORKER_AUDIO_OWNER_OFFLINE
T=40   subtitle wrkr POST /fail
T=40   服务端 WorkerFail 看 stage（asr）→ audio_worker_id 仍在 → stage=audio_uploaded（让下一个 subtitle worker 重试）
T=300  audio worker 网络恢复 → 继续 long-poll
```

---

## 6. v2 → v3 迁移说明

如果你在维护 v1/v2 的 worker 客户端，升级到 v3 时需要：

| 项 | v2（已废弃） | v3 |
|----|--------------|-----|
| 上传 FLAC | `POST /audio-complete` multipart | `POST /audio-ready` 仅 JSON 元数据 |
| audio worker 后台任务 | 无 | 新增 long-poll 循环 + 本地 FLAC 仓库 |
| FLAC 存储 | 服务端 `<UploadsDir>/intermediate/` | audio worker 本地 `<audio_storage_dir>/<jobId>.flac` |
| subtitle worker 拉 FLAC | `GET /audio` ServeFile | `GET /audio` broker 实时桥接（必须实现 90s timeout）|
| 失败 stage 回滚判定 | 看 FLAC 文件是否存在 | 看 audio_worker_id 是否非空 |

**v2 端点已下线**：访问 `/audio-complete` 会返回 404（gin 路由未注册）。

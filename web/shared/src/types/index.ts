// ========== Enums ==========
export enum UserRole {
  ADMIN = 'ADMIN',
  USER = 'USER',
}

export enum MediaStatus {
  ACTIVE = 'ACTIVE',
  INACTIVE = 'INACTIVE',
  ERROR = 'ERROR',
}

export enum ImportFormat {
  TEXT = 'TEXT',
  CSV = 'CSV',
  EXCEL = 'EXCEL',
  JSON = 'JSON',
}

export enum ImportStatus {
  PENDING = 'PENDING',
  SUCCESS = 'SUCCESS',
  PARTIAL = 'PARTIAL',
  FAILED = 'FAILED',
}

// ========== User ==========
export interface User {
  id: string;
  username: string;
  role: UserRole;
  avatar?: string | null;
  isActive: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface UserWithStats extends User {
  _count?: {
    favorites: number;
    playlists: number;
    watchHistory: number;
  };
}

// ========== Auth ==========
export interface LoginRequest {
  username: string;
  password: string;
}

export interface RegisterRequest {
  username: string;
  password: string;
}

export interface AuthResponse {
  user: User;
  accessToken: string;
}

export interface TokenPayload {
  userId: string;
  role: UserRole;
}

// ========== Media ==========
export interface Media {
  id: string;
  title: string;
  m3u8Url: string;
  posterUrl?: string | null;
  description?: string | null;
  year?: number | null;
  rating?: number | null;
  duration?: number | null;
  artist?: string | null;
  views: number;
  status: MediaStatus;
  categoryId?: string | null;
  category?: Category | null;
  tags?: Tag[];
  createdAt: string;
  updatedAt: string;
}

export interface MediaCreateRequest {
  title: string;
  m3u8Url: string;
  posterUrl?: string;
  description?: string;
  year?: number;
  rating?: number;
  duration?: number;
  artist?: string;
  categoryId?: string;
  tagIds?: string[];
}

export interface MediaUpdateRequest extends Partial<MediaCreateRequest> {}

export interface MediaQueryParams {
  page?: number;
  limit?: number;
  search?: string;
  categoryId?: string;
  tagId?: string;
  artist?: string;
  status?: MediaStatus;
  sortBy?: 'title' | 'createdAt' | 'year' | 'rating' | 'views';
  sortOrder?: 'asc' | 'desc';
}

// ========== Category ==========
export interface Category {
  id: string;
  name: string;
  slug: string;
  posterUrl?: string | null;
  _count?: {
    media: number;
  };
  createdAt: string;
  updatedAt: string;
}

export interface CategoryCreateRequest {
  name: string;
  slug: string;
  posterUrl?: string;
}

// ========== Tag ==========
export interface Tag {
  id: string;
  name: string;
  _count?: {
    media: number;
  };
  createdAt: string;
  updatedAt: string;
}

export interface TagCreateRequest {
  name: string;
}

// ========== Favorite ==========
export interface Favorite {
  id: string;
  userId: string;
  mediaId: string;
  media?: Media;
  createdAt: string;
}

// ========== Playlist ==========
export interface Playlist {
  id: string;
  name: string;
  description?: string | null;
  posterUrl?: string | null;
  userId: string;
  isPublic: boolean;
  items?: PlaylistItem[];
  _count?: {
    items: number;
  };
  createdAt: string;
  updatedAt: string;
}

export interface PlaylistItem {
  id: string;
  playlistId: string;
  mediaId: string;
  position: number;
  media?: Media;
  createdAt: string;
}

export interface PlaylistCreateRequest {
  name: string;
  description?: string;
  posterUrl?: string;
  isPublic?: boolean;
}

export interface PlaylistUpdateRequest extends Partial<PlaylistCreateRequest> {}

// ========== Watch History ==========
export interface WatchHistory {
  id: string;
  userId: string;
  mediaId: string;
  progress: number;      // seconds watched
  duration: number;       // total duration in seconds
  percentage: number;     // 0-100
  completed: boolean;
  media?: Media;
  updatedAt: string;
}

export interface WatchProgressUpdate {
  mediaId: string;
  progress: number;
  duration: number;
}

// ========== Import ==========
export interface ImportItem {
  title: string;
  m3u8Url: string;
  posterUrl?: string;
  description?: string;
  year?: number;
  artist?: string;
  categoryName?: string;
  tagNames?: string[];
}

export interface ImportPreviewResponse {
  items: ImportItem[];
  totalCount: number;
  validCount: number;
  invalidCount: number;
  errors: ImportError[];
}

export interface ImportError {
  row: number;
  field: string;
  message: string;
}

export interface ImportResult {
  totalCount: number;
  successCount: number;
  failedCount: number;
  errors: ImportError[];
}

export interface ImportLog {
  id: string;
  userId: string;
  format: ImportFormat;
  fileName?: string | null;
  totalCount: number;
  successCount: number;
  failedCount: number;
  status: ImportStatus;
  createdAt: string;
}

// ========== System Settings ==========
export interface SystemSetting {
  key: string;
  value: string;
}

// ========== API Response ==========
export interface ApiResponse<T = unknown> {
  success: boolean;
  data?: T;
  message?: string;
  error?: string;
  /**
   * 机器可读错误码（服务端 dto.APIResponse.Code）。
   * 例如主备切换期间只读副本拒绝写请求时为 'NODE_READ_ONLY'。
   */
  code?: string;
}

export interface PaginatedResponse<T> {
  items: T[];
  total: number;
  page: number;
  limit: number;
  totalPages: number;
}

// ========== Artist ==========
export interface ArtistInfo {
  name: string;
  videoCount: number;
}

// ========== Dashboard Stats ==========
export interface DashboardStats {
  totalMedia: number;
  totalUsers: number;
  totalCategories: number;
  totalViews: number;
  recentMedia: Media[];
  topMedia: Media[];
}

// ========== Backup ==========
export interface RestoreResult {
  tablesRestored: number;
  totalRecords: number;
  uploadsRestored: number;
  duration: number;
}

export type ExportPhase = 'db' | 'files' | 'finalize' | 'complete' | 'error';

export type BackupPhase = 'upload' | 'parse' | 'db' | 'delete' | 'write' | 'files' | 'finalize' | 'complete' | 'error';

export interface ExportProgress {
  phase: ExportPhase;
  message: string;
  current: number;
  total: number;
  percentage: number;
  downloadId?: string;
}

export interface BackupProgress {
  phase: BackupPhase;
  message: string;
  current: number;
  total: number;
  percentage: number;
  downloadId?: string;
  result?: RestoreResult;
}

// ========== Batch Operations ==========
export interface BatchOperationResult {
  affectedCount: number;
}

// ========== Login Record ==========
export interface LoginRecord {
  id: string;
  userId: string;
  ip: string | null;
  userAgent: string | null;
  browser: string | null;
  os: string | null;
  device: string | null;
  createdAt: string;
}

// ========== User Activity Summary ==========
export interface UserActivitySummary {
  user: {
    username: string;
    role: string;
    isActive: boolean;
    createdAt: string;
  } | null;
  totalLogins: number;
  lastLogin: {
    createdAt: string;
    ip: string | null;
    browser: string | null;
    os: string | null;
    device: string | null;
  } | null;
  totalWatched: number;
  totalCompleted: number;
}

// ========== User Activity Aggregate (all users) ==========
export interface UserActivityAggregate {
  loginStats: {
    totalLogins: number;
    uniqueUsers: number;
    todayLogins: number;
    yesterdayLogins: number;
    last7DaysLogins: number;
  };
  watchStats: {
    totalWatchRecords: number;
    totalCompleted: number;
    totalWatchTime: number; // seconds
  };
  recentLogins: Array<{
    id: string;
    userId: string;
    username: string | null;
    ip: string | null;
    browser: string | null;
    os: string | null;
    device: string | null;
    createdAt: string;
  }>;
  topWatchedMedia: Array<{
    mediaId: string;
    title: string;
    watchCount: number;
    completedCount: number;
  }>;
  topActiveUsers: Array<{
    userId: string;
    username: string;
    loginCount: number;
    watchCount: number;
  }>;
  recentWatchRecords: Array<{
    id: string;
    userId: string;
    username: string | null;
    mediaId: string;
    mediaTitle: string;
    progress: number;
    duration: number;
    percentage: number;
    completed: boolean;
    updatedAt: string;
  }>;
}

// ========== Subtitle ==========
export type SubtitleStatus = 'PENDING' | 'RUNNING' | 'DONE' | 'FAILED' | 'DISABLED' | 'MISSING';

/**
 * v2 分布式 worker 拆分后 stage 集合：
 *   - queued                 → 待 audio_extract worker 抢占
 *   - downloading            → audio worker 在拉 m3u8
 *   - extracting             → audio worker 在抽音（旧值兼容：单机模式仍然用此值表示整段抽音过程）
 *   - encoding_intermediate  → audio worker 在编 FLAC
 *   - audio_uploaded         → FLAC 已上传到中转池，等 asr_subtitle worker 抢占
 *   - asr / translate / writing / done 与 v1 一致
 */
export type SubtitleStage =
  | 'queued'
  | 'downloading'
  | 'extracting'
  | 'encoding_intermediate'
  | 'audio_uploaded'
  | 'asr'
  | 'translate'
  | 'writing'
  | 'done';

/** v2 worker 自报的 capability 字符串。 */
export type WorkerCapability = 'audio_extract' | 'asr_subtitle';

export interface SubtitleStatusResponse {
  mediaId: string;
  status: SubtitleStatus;
  stage: SubtitleStage | '';
  progress: number;
  sourceLang: string;
  targetLang: string;
  vttUrl?: string;
  errorMsg?: string;
}

export interface SubtitleJob {
  id: string;
  mediaId: string;
  mediaTitle?: string;
  categoryId?: string;
  categoryName?: string;
  status: SubtitleStatus;
  stage: SubtitleStage;
  progress: number;
  sourceLang: string;
  targetLang: string;
  asrModel?: string;
  mtModel?: string;
  segmentCount: number;
  errorMsg?: string;
  startedAt?: string | null;
  finishedAt?: string | null;
  createdAt: string;
  updatedAt: string;
  // v2 分布式 worker 协作字段（仅在拆分流水线下有值；单机模式留空）
  audioWorkerId?: string;
  subtitleWorkerId?: string;
  audioArtifactSize?: number;
  audioArtifactFormat?: string;
  audioArtifactDurationMs?: number;
  audioUploadedAt?: string | null;
}

export interface SubtitleSettings {
  enabled: boolean;
  whisperBin: string;
  whisperModel: string;
  whisperLanguage: string;
  whisperThreads: number;
  translateBaseUrl: string;
  translateModel: string;
  translateApiKey: string;
  targetLang: string;
  batchSize: number;
}

/**
 * SubtitleSettingsUpdate admin 字幕配置 patch。
 *
 * 全部字段可选：
 *   - undefined / 缺失 = 不修改
 *   - 字符串字段允许传空串表示"清除/恢复默认"
 *   - translateApiKey 若包含 "***"（脱敏占位）会被服务端忽略，
 *     避免前端展示脱敏值后误覆盖真实 key
 *
 * LocalWorkerEnabled / WorkerStaleThreshold / GlobalMaxConcurrency 等
 * 部署相关字段不在此处修改，仍由 .env 控制。
 */
export interface SubtitleSettingsUpdate {
  enabled?: boolean;
  whisperBin?: string;
  whisperModel?: string;
  whisperLanguage?: string;
  whisperThreads?: number;
  translateBaseUrl?: string;
  translateModel?: string;
  translateApiKey?: string;
  targetLang?: string;
  batchSize?: number;
}

export interface SubtitleQueueStatus {
  pending: number;
  running: number;
  done: number;
  failed: number;
  disabled: number;
  /** 全局并发上限：0=不限 */
  globalMaxConcurrency: number;
}

export interface SubtitleBatchRegenerateRequest {
  mediaIds?: string[];
  all?: boolean;
  onlyFailed?: boolean;
  /** 按分类批量重新生成；"_none" 表示未分类媒体 */
  categoryId?: string;
}

export interface SubtitleBatchRegenerateResponse {
  enqueued: number;
  skipped: number;
}

/** 批量禁用 / 取消 / 删除等仅以 mediaIds 为入参的操作请求体。 */
export interface SubtitleBatchMediaIDsRequest {
  mediaIds: string[];
}

/** 批量启用 / 禁用切换。disabled=true 切到 DISABLED；false 还原为 PENDING 并入队。 */
export interface SubtitleBatchSetDisabledRequest {
  mediaIds: string[];
  disabled: boolean;
}

/** 批量操作的统一返回：affected = 实际被改动 / 删除条数；skipped = 状态不允许或行不存在等被跳过的条数。 */
export interface SubtitleBatchOpResponse {
  affected: number;
  skipped: number;
}

// 远程 GPU worker 在线列表项
export interface SubtitleWorker {
  id: string;
  name: string;
  version?: string;
  gpu?: string;
  currentJobId?: string;
  lastSeenAt: string;
  registeredAt: string;
  completedJobs: number;
  failedJobs: number;
  online: boolean;
  /** v2 worker 自报的能力集合（旧 client 兜底为 ["audio_extract","asr_subtitle"]） */
  capabilities: WorkerCapability[];
}

// admin 面板生成的 worker 凭证（不含明文）
export interface SubtitleWorkerToken {
  id: string;
  name: string;
  tokenPrefix: string;
  /** 该 token 名下 worker 集合允许同时持有的 RUNNING 任务上限（不分能力的兜底）*/
  maxConcurrency: number;
  /** v2：audio_extract 维度并发上限（默认 2，0 = 不限） */
  maxAudioConcurrency: number;
  /** v2：asr_subtitle 维度并发上限（默认 1，0 = 不限） */
  maxSubtitleConcurrency: number;
  /** 该 token 当前正在运行的任务数（实时） */
  currentRunning: number;
  /** v2：当前 audio 阶段（downloading/extracting/encoding_intermediate）任务数 */
  currentAudioRunning: number;
  /** v2：当前 subtitle 阶段（asr/translate/writing）任务数 */
  currentSubtitleRunning: number;
  createdAt: string;
  lastUsedAt?: string | null;
  revokedAt?: string | null;
}

// 创建 token 时的一次性返回（含明文）
export interface SubtitleWorkerTokenCreateResponse {
  token: string;
  record: SubtitleWorkerToken;
}

/** 创建 worker token 的请求体。 */
export interface SubtitleWorkerTokenCreateRequest {
  name: string;
  /** 0 / undefined → 走服务端默认 1 */
  maxConcurrency?: number;
  /** 0 / undefined → 服务端默认 2 */
  maxAudioConcurrency?: number;
  /** 0 / undefined → 服务端默认 1 */
  maxSubtitleConcurrency?: number;
}

/** v2 admin 中转池监控统计。 */
export interface IntermediateAudioStats {
  fileCount: number;
  totalBytes: number;
  oldestUploadedAt?: string | null;
  quotaBytes: number;
}

/** v2 admin 顶部告警条 item。 */
export interface AdminAlert {
  level: 'info' | 'warn' | 'error';
  message: string;
}

// ============ 插件中心 ============

/** 插件卡片上的一行运行时指标（如「在线 Worker: 2 / 3」）。 */
export interface PluginStatusItem {
  label: string;
  value: string;
  /** 前端着色提示；缺省为中性色。 */
  tone?: 'ok' | 'warn' | 'error';
}

/** GET /admin/plugins 列表项：静态元数据 + 启用状态 + 运行时状态快照。 */
export interface PluginInfo {
  /** kebab-case 唯一 ID，同时是详情管理页的前端路由映射键。 */
  id: string;
  name: string;
  description: string;
  version: string;
  /** lucide 图标名提示；前端可按 id 覆盖映射。 */
  icon: string;
  category: string;
  enabled: boolean;
  healthy: boolean;
  status: PluginStatusItem[];
  /** 管理员导入的声明式外部插件（可删除）；内置插件为 false。 */
  external?: boolean;
  /** 外部插件的可选主页外链（仅展示）。 */
  homepage?: string;
}

/** 外部插件 manifest（schemaVersion 1），导入弹窗本地预览用。 */
export interface ExternalPluginManifest {
  schemaVersion: number;
  id: string;
  name: string;
  description?: string;
  version: string;
  /** lucide 图标名（小写）。 */
  icon?: string;
  category?: string;
  /** 可选：外部服务健康检查地址，插件卡片按它显示健康状态。 */
  healthUrl?: string;
  /** 可选：仅展示用主页外链。 */
  homepage?: string;
}

// ============ 高可用管理 ============

/** HA 部署档位：单机 / 仅 LiteFS 角色感知 / 完整租约仲裁。 */
export type HaMode = 'standalone' | 'role-aware' | 'full';

/** 交接进度阶段（对齐 internal/ha/status.go 的 Phase* 常量）。 */
export type HaSwitchPhase =
  | 'idle'
  | 'requested'
  | 'waiting-streams'
  | 'draining'
  | 'switching'
  | 'aborted';

/** 本机节点状态。 */
export interface HaNodeInfo {
  role: string;
  nodeId?: string;
  preferred: boolean;
  txid?: string;
  draining: boolean;
  busyStreams: number;
  epoch: number;
}

/** 对端探测缓存（来自本机 Agent 的 Prober，非实时请求）。 */
export interface HaPeerInfo {
  nodeId?: string;
  reachable: boolean;
  role?: string;
  txid?: string;
  draining: boolean;
  busyStreams: number;
  /** 对端自报的应用版本（滚动升级时对照双端版本）。 */
  version?: string;
  consecutiveFailures: number;
  /** RFC3339；缺省表示还没探测过。 */
  lastProbeAt?: string;
  error?: string;
}

/** Cloudflare 租约缓存。 */
export interface HaLeaseInfo {
  owner: string;
  epoch: number;
  expiresAt: string;
  state: string;
  /** 本机最近一次成功读到租约的时刻。 */
  readAt: string;
}

/** 交接进度。 */
export interface HaSwitchInfo {
  phase: HaSwitchPhase;
  manual: boolean;
  force: boolean;
  since?: string;
  drainSince?: string;
  /** 仅 phase=aborted 时非空。 */
  lastError?: string;
}

/** GET /admin/ha/status 响应。 */
export interface HaStatus {
  mode: HaMode;
  setupDismissed: boolean;
  autoFailback: boolean;
  local: HaNodeInfo;
  peer?: HaPeerInfo;
  lease?: HaLeaseInfo;
  switch: HaSwitchInfo;
}

// ============ 应用内自更新 ============

/** 更新状态机取值（对齐 internal/update/manager.go 的 State* 常量）。 */
export type UpdateState =
  | 'idle'
  | 'checking'
  | 'update-available'
  | 'downloading'
  | 'verifying'
  | 'staged'
  | 'restarting'
  | 'failed';

/** 最新 Release 摘要。 */
export interface UpdateReleaseInfo {
  version: string;
  /** 发布说明（服务端已截断，纯文本渲染）。 */
  notes?: string;
  publishedAt?: string;
  assetSize: number;
}

/** 下载进度（字节）。 */
export interface UpdateProgress {
  downloadedBytes: number;
  totalBytes: number;
}

/** 已暂存待重启生效的版本。 */
export interface UpdateStagedInfo {
  version: string;
  stagedAt: string;
}

/** 更新失败信息（code 机器可读）。 */
export interface UpdateErrorInfo {
  code: string;
  message: string;
}

/** GET /admin/update/status 响应。 */
export interface UpdateStatus {
  currentVersion: string;
  commit?: string;
  enabled: boolean;
  /** enabled=false 的原因："dev-build"（开发构建）/ "env-disabled"（UPDATE_DISABLED）。 */
  disabledReason?: 'dev-build' | 'env-disabled';
  state: UpdateState;
  lastCheckedAt?: string;
  latest?: UpdateReleaseInfo;
  progress?: UpdateProgress;
  staged?: UpdateStagedInfo;
  error?: UpdateErrorInfo;
}

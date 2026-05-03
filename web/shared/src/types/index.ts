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

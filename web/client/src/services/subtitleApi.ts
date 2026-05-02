import api from './api.js';
import type {
  ApiResponse,
  PaginatedResponse,
  SubtitleStatusResponse,
  SubtitleJob,
  SubtitleSettings,
  SubtitleQueueStatus,
  SubtitleBatchRegenerateRequest,
  SubtitleBatchRegenerateResponse,
  SubtitleWorker,
  SubtitleWorkerToken,
  SubtitleWorkerTokenCreateResponse,
} from '@m3u8-preview/shared';

/**
 * 字幕模块 API 客户端。
 * 普通端点：登录用户即可调用，用于播放页拉取状态 + 实际拉取 VTT。
 * Admin 端点：仅管理员可调用，对应字幕管理面板的全部功能。
 */
export const subtitleApi = {
  /** 查询字幕生成状态（播放页加载时调用）。 */
  async getStatus(mediaId: string): Promise<SubtitleStatusResponse> {
    const { data } = await api.get<ApiResponse<SubtitleStatusResponse>>(
      `/subtitle/${encodeURIComponent(mediaId)}/status`,
    );
    return data.data!;
  },

  // ---- Admin ----

  async listJobs(params: { page?: number; limit?: number; status?: string; search?: string; categoryId?: string } = {}) {
    const { data } = await api.get<ApiResponse<PaginatedResponse<SubtitleJob>>>(
      '/admin/subtitle/jobs',
      { params },
    );
    return data.data!;
  },

  async getJob(mediaId: string): Promise<SubtitleJob> {
    const { data } = await api.get<ApiResponse<SubtitleJob>>(
      `/admin/subtitle/jobs/${encodeURIComponent(mediaId)}`,
    );
    return data.data!;
  },

  async retry(mediaId: string): Promise<void> {
    await api.post(`/admin/subtitle/jobs/${encodeURIComponent(mediaId)}/retry`);
  },

  async deleteJob(mediaId: string): Promise<void> {
    await api.delete(`/admin/subtitle/jobs/${encodeURIComponent(mediaId)}`);
  },

  async setDisabled(mediaId: string, disabled: boolean): Promise<void> {
    await api.put(`/admin/subtitle/jobs/${encodeURIComponent(mediaId)}/disabled`, null, {
      params: { value: String(disabled) },
    });
  },

  async batchRegenerate(req: SubtitleBatchRegenerateRequest): Promise<SubtitleBatchRegenerateResponse> {
    const { data } = await api.post<ApiResponse<SubtitleBatchRegenerateResponse>>(
      '/admin/subtitle/jobs/batch-regenerate',
      req,
    );
    return data.data!;
  },

  async queueStatus(): Promise<SubtitleQueueStatus> {
    const { data } = await api.get<ApiResponse<SubtitleQueueStatus>>('/admin/subtitle/queue');
    return data.data!;
  },

  async settings(): Promise<SubtitleSettings> {
    const { data } = await api.get<ApiResponse<SubtitleSettings>>('/admin/subtitle/settings');
    return data.data!;
  },

  // ---- 远程 worker 管理 ----

  /** 列出 5 分钟内有心跳的 worker。 */
  async listWorkers(): Promise<SubtitleWorker[]> {
    const { data } = await api.get<ApiResponse<SubtitleWorker[]>>('/admin/subtitle/workers');
    return data.data ?? [];
  },

  /** 列出所有 worker token（不含明文）。 */
  async listWorkerTokens(): Promise<SubtitleWorkerToken[]> {
    const { data } = await api.get<ApiResponse<SubtitleWorkerToken[]>>(
      '/admin/subtitle/worker-tokens',
    );
    return data.data ?? [];
  },

  /** 生成新 token，仅本次返回明文。maxConcurrency 不传走服务端默认 1。 */
  async createWorkerToken(name: string, maxConcurrency?: number): Promise<SubtitleWorkerTokenCreateResponse> {
    const { data } = await api.post<ApiResponse<SubtitleWorkerTokenCreateResponse>>(
      '/admin/subtitle/worker-tokens',
      { name, maxConcurrency },
    );
    return data.data!;
  },

  /** 编辑 token（目前仅 maxConcurrency）。 */
  async updateWorkerToken(id: string, payload: { maxConcurrency?: number }): Promise<SubtitleWorkerToken> {
    const { data } = await api.put<ApiResponse<SubtitleWorkerToken>>(
      `/admin/subtitle/worker-tokens/${encodeURIComponent(id)}`,
      payload,
    );
    return data.data!;
  },

  /** 吊销 token（soft revoke）。 */
  async revokeWorkerToken(id: string): Promise<void> {
    await api.delete(`/admin/subtitle/worker-tokens/${encodeURIComponent(id)}`);
  },
};

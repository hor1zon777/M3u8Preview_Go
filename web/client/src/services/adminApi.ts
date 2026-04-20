import api from './api.js';
import type { ApiResponse, DashboardStats, PaginatedResponse, RestoreResult, BatchOperationResult, LoginRecord, UserActivitySummary, UserActivityAggregate, WatchHistory, ExportProgress, BackupProgress } from '@m3u8-preview/shared';

async function getSseTicket(): Promise<string> {
  const { data } = await api.post<ApiResponse<{ ticket: string }>>('/auth/sse-ticket');
  return data.data!.ticket;
}

export const adminApi = {
  async getDashboard() {
    const { data } = await api.get<ApiResponse<DashboardStats>>('/admin/dashboard');
    return data.data!;
  },

  async getUsers(page: number = 1, limit: number = 20, search?: string) {
    const { data } = await api.get<ApiResponse<PaginatedResponse<any>>>('/admin/users', {
      params: { page, limit, search },
    });
    return data.data!;
  },

  async updateUser(id: string, payload: { role?: string; isActive?: boolean }) {
    const { data } = await api.put<ApiResponse<any>>(`/admin/users/${id}`, payload);
    return data.data!;
  },

  async deleteUser(id: string) {
    await api.delete(`/admin/users/${id}`);
  },

  async getSettings() {
    const { data } = await api.get<ApiResponse<Array<{ key: string; value: string }>>>('/admin/settings');
    return data.data!;
  },

  async updateSetting(key: string, value: string) {
    await api.put('/admin/settings', { key, value });
  },

  async exportBackup(options?: { includePosters?: boolean }) {
    const params = new URLSearchParams();
    if (options?.includePosters === false) {
      params.set('includePosters', 'false');
    }
    const query = params.toString();
    const url = `/admin/backup/export${query ? `?${query}` : ''}`;

    const response = await api.get(url, {
      responseType: 'blob',
      timeout: 300000,
    });
    const blob = response.data as Blob;

    const disposition = response.headers['content-disposition'];
    const match = disposition?.match(/filename="?([^";\n]+)"?/);
    const filename = match?.[1] || `backup-${new Date().toISOString().slice(0, 19)}.zip`;

    const blobUrl = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = blobUrl;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(blobUrl);
  },

  exportBackupWithProgress(
    options: { includePosters?: boolean },
    onProgress: (progress: ExportProgress) => void,
  ): { abort: () => void } {
    const params = new URLSearchParams();
    if (options.includePosters === false) {
      params.set('includePosters', 'false');
    }

    let eventSource: EventSource | null = null;
    let aborted = false;

    const run = async () => {
      try {
        const ticket = await getSseTicket();
        if (aborted) return;
        params.set('ticket', ticket);
        const query = params.toString();
        const url = `/api/v1/admin/backup/export/stream${query ? `?${query}` : ''}`;

        eventSource = new EventSource(url);

        eventSource.onmessage = (event) => {
          try {
            const data = JSON.parse(event.data) as ExportProgress;
            onProgress(data);

            if (data.phase === 'complete' && data.downloadId) {
              eventSource?.close();
              adminApi.downloadBackupFile(data.downloadId);
            }
            if (data.phase === 'error') {
              eventSource?.close();
            }
          } catch { /* ignore parse errors */ }
        };

        eventSource.onerror = () => {
          eventSource?.close();
          onProgress({
            phase: 'error',
            message: '连接中断',
            current: 0,
            total: 0,
            percentage: 0,
          });
        };
      } catch {
        onProgress({
          phase: 'error',
          message: '获取认证凭据失败',
          current: 0,
          total: 0,
          percentage: 0,
        });
      }
    };

    run();

    return {
      abort: () => {
        aborted = true;
        eventSource?.close();
      },
    };
  },

  async downloadBackupFile(downloadId: string) {
    const response = await api.get(`/admin/backup/download/${downloadId}`, {
      responseType: 'blob',
      timeout: 300000,
    });
    const blob = response.data as Blob;

    const disposition = response.headers['content-disposition'];
    const match = disposition?.match(/filename="?([^";\n]+)"?/);
    const filename = match?.[1] || `backup-${new Date().toISOString().slice(0, 19)}.zip`;

    const blobUrl = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = blobUrl;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(blobUrl);
  },

  async importBackup(file: File): Promise<RestoreResult> {
    const formData = new FormData();
    formData.append('file', file);
    const { data } = await api.post<ApiResponse<RestoreResult>>('/admin/backup/import', formData, {
      timeout: 300000,
    });
    return data.data!;
  },

  importBackupWithProgress(
    file: File,
    onProgress: (progress: BackupProgress) => void,
  ): { abort: () => void } {
    const abortController = new AbortController();
    let eventSource: EventSource | null = null;

    const run = async () => {
      try {
        // 阶段 1：上传文件（0-20%）
        onProgress({ phase: 'upload', message: '正在上传文件...', current: 0, total: 100, percentage: 0 });

        const formData = new FormData();
        formData.append('file', file);

        const { data } = await api.post<ApiResponse<{ restoreId: string }>>(
          '/admin/backup/import/upload',
          formData,
          {
            timeout: 300000,
            signal: abortController.signal,
            onUploadProgress: (e) => {
              const pct = e.total ? Math.round((e.loaded / e.total) * 20) : 0;
              onProgress({
                phase: 'upload',
                message: `正在上传文件...`,
                current: e.loaded,
                total: e.total || 0,
                percentage: pct,
              });
            },
          },
        );

        const restoreId = data.data!.restoreId;

        // 阶段 2：SSE 恢复进度（20-100%）
        const ticket = await getSseTicket();
        if (abortController.signal.aborted) return;
        const params = new URLSearchParams();
        params.set('ticket', ticket);
        const query = params.toString();
        const url = `/api/v1/admin/backup/import/stream/${restoreId}${query ? `?${query}` : ''}`;

        eventSource = new EventSource(url);

        eventSource.onmessage = (event) => {
          try {
            const p = JSON.parse(event.data) as BackupProgress;
            onProgress(p);
            if (p.phase === 'complete' || p.phase === 'error') {
              eventSource?.close();
              eventSource = null;
            }
          } catch { /* ignore */ }
        };

        eventSource.onerror = () => {
          eventSource?.close();
          eventSource = null;
          onProgress({ phase: 'error', message: '连接中断', current: 0, total: 0, percentage: 0 });
        };
      } catch (err: any) {
        if (err.name !== 'CanceledError' && err.name !== 'AbortError') {
          const msg = err.response?.data?.message || err.message || '上传失败';
          onProgress({ phase: 'error', message: msg, current: 0, total: 0, percentage: 0 });
        }
      }
    };

    run();

    return {
      abort: () => {
        abortController.abort();
        eventSource?.close();
        eventSource = null;
      },
    };
  },

  async batchDeleteMedia(ids: string[]): Promise<BatchOperationResult> {
    const { data } = await api.post<ApiResponse<BatchOperationResult>>('/admin/media/batch-delete', { ids });
    return data.data!;
  },

  async batchUpdateMediaStatus(ids: string[], status: 'ACTIVE' | 'INACTIVE'): Promise<BatchOperationResult> {
    const { data } = await api.put<ApiResponse<BatchOperationResult>>('/admin/media/batch-status', { ids, status });
    return data.data!;
  },

  async batchUpdateMediaCategory(ids: string[], categoryId: string | null): Promise<BatchOperationResult> {
    const { data } = await api.put<ApiResponse<BatchOperationResult>>('/admin/media/batch-category', { ids, categoryId });
    return data.data!;
  },

  // ─── 封面管理 ─────────────────────────────────────

  async getPosterStats(): Promise<{ total: number; external: number; local: number; missing: number; totalSizeBytes: number }> {
    const { data } = await api.get<ApiResponse<{ total: number; external: number; local: number; missing: number; totalSizeBytes: number }>>('/admin/posters/stats');
    return data.data!;
  },

  async migratePosterImages(): Promise<{ enqueuedCount: number }> {
    const { data } = await api.post<ApiResponse<{ enqueuedCount: number }>>('/admin/posters/migrate');
    return data.data!;
  },

  async getPosterMigrationStatus() {
    const { data } = await api.get<ApiResponse<{
      pending: number;
      active: number;
      completed: number;
      failed: number;
      skipped: number;
      total: number;
      concurrency: number;
      running: boolean;
    }>>('/admin/posters/status');
    return data.data!;
  },

  async retryFailedPosters(): Promise<{ enqueuedCount: number }> {
    const { data } = await api.post<ApiResponse<{ enqueuedCount: number }>>('/admin/posters/retry');
    return data.data!;
  },

  // ─── 用户行为记录 ─────────────────────────────────────

  async getUserLoginRecords(userId: string, page: number = 1, limit: number = 20) {
    const { data } = await api.get<ApiResponse<PaginatedResponse<LoginRecord>>>(
      `/admin/users/${userId}/login-records`,
      { params: { page, limit } },
    );
    return data.data!;
  },

  async getUserWatchHistory(userId: string, page: number = 1, limit: number = 20) {
    const { data } = await api.get<ApiResponse<PaginatedResponse<WatchHistory>>>(
      `/admin/users/${userId}/watch-history`,
      { params: { page, limit } },
    );
    return data.data!;
  },

  async getUserActivitySummary(userId: string) {
    const { data } = await api.get<ApiResponse<UserActivitySummary>>(
      `/admin/users/${userId}/activity-summary`,
    );
    return data.data!;
  },

  async getActivityAggregate() {
    const { data } = await api.get<ApiResponse<UserActivityAggregate>>('/admin/activity');
    return data.data!;
  },
};

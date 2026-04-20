import api from './api.js';
import { getAccessToken } from './api.js';
import type { ApiResponse, WatchHistory, PaginatedResponse, WatchProgressUpdate } from '@m3u8-preview/shared';

export const historyApi = {
  async updateProgress(data: WatchProgressUpdate) {
    await api.post('/history/progress', data);
  },

  async getAll(page: number = 1, limit: number = 20) {
    const { data } = await api.get<ApiResponse<PaginatedResponse<WatchHistory>>>('/history', { params: { page, limit } });
    return data.data!;
  },

  async getContinueWatching(limit: number = 10) {
    const { data } = await api.get<ApiResponse<WatchHistory[]>>('/history/continue', { params: { limit } });
    return data.data!;
  },

  async getProgress(mediaId: string) {
    const { data } = await api.get<ApiResponse<WatchHistory | null>>(`/history/${mediaId}`);
    return data.data ?? null;
  },

  async deleteOne(id: string) {
    await api.delete(`/history/${id}`);
  },

  async clearAll() {
    await api.delete('/history/clear');
  },

  // keepalive fetch for unload events (sendBeacon doesn't support auth headers)
  sendBeacon(data: WatchProgressUpdate) {
    const token = getAccessToken();
    const baseURL = api.defaults.baseURL || '/api/v1';
    fetch(`${baseURL}/history/progress`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify(data),
      keepalive: true,
    }).catch(() => {});
  },

  async getProgressMap() {
    const { data } = await api.get<ApiResponse<Record<string, { percentage: number; completed: boolean }>>>('/history/progress-map');
    return data.data!;
  },
};

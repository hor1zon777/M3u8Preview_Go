import api from './api.js';
import type { ApiResponse, Media, PaginatedResponse } from '@m3u8-preview/shared';

export const favoriteApi = {
  async toggle(mediaId: string) {
    const { data } = await api.post<ApiResponse<{ isFavorited: boolean }>>(`/favorites/${mediaId}`);
    return data.data!;
  },

  async check(mediaId: string) {
    const { data } = await api.get<ApiResponse<{ isFavorited: boolean }>>(`/favorites/${mediaId}/check`);
    return data.data!;
  },

  async getAll(page: number = 1, limit: number = 20) {
    const { data } = await api.get<ApiResponse<PaginatedResponse<{ id: string; media: Media; createdAt: string }>>>('/favorites', { params: { page, limit } });
    return data.data!;
  },
};

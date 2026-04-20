import api from './api.js';
import type { ApiResponse, Media, PaginatedResponse, MediaQueryParams, MediaCreateRequest, ArtistInfo } from '@m3u8-preview/shared';

export const mediaApi = {
  async getAll(params?: MediaQueryParams) {
    const { data } = await api.get<ApiResponse<PaginatedResponse<Media>>>('/media', { params });
    return data.data!;
  },

  async getById(id: string) {
    const { data } = await api.get<ApiResponse<Media>>(`/media/${id}`);
    return data.data!;
  },

  async create(payload: MediaCreateRequest) {
    const { data } = await api.post<ApiResponse<Media>>('/media', payload);
    return data.data!;
  },

  async update(id: string, payload: Partial<MediaCreateRequest>) {
    const { data } = await api.put<ApiResponse<Media>>(`/media/${id}`, payload);
    return data.data!;
  },

  async delete(id: string) {
    await api.delete(`/media/${id}`);
  },

  async getRecent(count: number = 10) {
    const { data } = await api.get<ApiResponse<Media[]>>('/media/recent', { params: { count } });
    return data.data!;
  },

  async getRandom(count: number = 10) {
    const { data } = await api.get<ApiResponse<Media[]>>('/media/random', { params: { count } });
    return data.data!;
  },

  async incrementViews(id: string) {
    await api.post(`/media/${id}/views`);
  },

  async getArtists() {
    const { data } = await api.get<ApiResponse<ArtistInfo[]>>('/media/artists');
    return data.data!;
  },

  async regenerateThumbnail(id: string) {
    const { data } = await api.post<ApiResponse<Media>>(`/media/${id}/thumbnail`);
    return data.data!;
  },
};

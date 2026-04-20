import api from './api.js';
import type { ApiResponse, Playlist, PlaylistItem, PaginatedResponse } from '@m3u8-preview/shared';

export const playlistApi = {
  async getAll() {
    const { data } = await api.get<ApiResponse<Playlist[]>>('/playlists');
    return data.data!;
  },

  async getPublicPlaylists(params?: { page?: number; limit?: number; search?: string; sortBy?: string; sortOrder?: string }) {
    const { data } = await api.get<ApiResponse<PaginatedResponse<Playlist>>>('/playlists/public', { params });
    return data.data!;
  },

  async getById(id: string) {
    const { data } = await api.get<ApiResponse<Playlist>>(`/playlists/${id}`);
    return data.data!;
  },

  async create(name: string, description?: string, isPublic?: boolean) {
    const { data } = await api.post<ApiResponse<Playlist>>('/playlists', { name, description, isPublic });
    return data.data!;
  },

  async update(id: string, payload: { name?: string; description?: string; posterUrl?: string; isPublic?: boolean }) {
    const { data } = await api.put<ApiResponse<Playlist>>(`/playlists/${id}`, payload);
    return data.data!;
  },

  async delete(id: string) {
    await api.delete(`/playlists/${id}`);
  },

  async addItem(playlistId: string, mediaId: string) {
    await api.post(`/playlists/${playlistId}/items`, { mediaId });
  },

  async removeItem(playlistId: string, mediaId: string) {
    await api.delete(`/playlists/${playlistId}/items/${mediaId}`);
  },

  async reorder(playlistId: string, itemIds: string[]) {
    await api.put(`/playlists/${playlistId}/reorder`, { itemIds });
  },

  async getPlaylistItems(
    playlistId: string,
    params?: { page?: number; limit?: number; search?: string; sortBy?: string; sortOrder?: string },
  ) {
    const { data } = await api.get<ApiResponse<PaginatedResponse<PlaylistItem>>>(
      `/playlists/${playlistId}/items`,
      { params },
    );
    return data.data!;
  },
};

import api from './api.js';
import type { ApiResponse, Tag, TagCreateRequest } from '@m3u8-preview/shared';

export const tagApi = {
  async getAll() {
    const { data } = await api.get<ApiResponse<Tag[]>>('/tags');
    return data.data!;
  },

  async getById(id: string) {
    const { data } = await api.get<ApiResponse<Tag>>(`/tags/${id}`);
    return data.data!;
  },

  async create(payload: TagCreateRequest) {
    const { data } = await api.post<ApiResponse<Tag>>('/tags', payload);
    return data.data!;
  },

  async update(id: string, payload: Partial<TagCreateRequest>) {
    const { data } = await api.put<ApiResponse<Tag>>(`/tags/${id}`, payload);
    return data.data!;
  },

  async delete(id: string) {
    await api.delete(`/tags/${id}`);
  },
};

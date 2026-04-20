import api from './api.js';
import type { ApiResponse, Category, CategoryCreateRequest } from '@m3u8-preview/shared';

export const categoryApi = {
  async getAll() {
    const { data } = await api.get<ApiResponse<Category[]>>('/categories');
    return data.data!;
  },

  async getById(id: string) {
    const { data } = await api.get<ApiResponse<Category>>(`/categories/${id}`);
    return data.data!;
  },

  async create(payload: CategoryCreateRequest) {
    const { data } = await api.post<ApiResponse<Category>>('/categories', payload);
    return data.data!;
  },

  async update(id: string, payload: Partial<CategoryCreateRequest>) {
    const { data } = await api.put<ApiResponse<Category>>(`/categories/${id}`, payload);
    return data.data!;
  },

  async delete(id: string) {
    await api.delete(`/categories/${id}`);
  },
};

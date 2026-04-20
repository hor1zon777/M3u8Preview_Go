import api from './api.js';
import type { ApiResponse, ImportPreviewResponse, ImportResult, ImportLog } from '@m3u8-preview/shared';

export const importApi = {
  async previewText(content: string) {
    const { data } = await api.post<ApiResponse<ImportPreviewResponse>>('/import/preview', { content, format: 'TEXT' });
    return data.data!;
  },

  async previewFile(file: File) {
    const formData = new FormData();
    formData.append('file', file);
    const { data } = await api.post<ApiResponse<ImportPreviewResponse>>('/import/preview', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
    });
    return data.data!;
  },

  async execute(items: any[], format: string, fileName?: string) {
    const { data } = await api.post<ApiResponse<ImportResult>>('/import/execute', { items, format, fileName });
    return data.data!;
  },

  getTemplateUrl(format: 'csv' | 'json') {
    return `/api/v1/import/template/${format}`;
  },

  async getLogs() {
    const { data } = await api.get<ApiResponse<ImportLog[]>>('/import/logs');
    return data.data!;
  },
};

import api from './api.js';
import type { ApiResponse, PluginInfo } from '@m3u8-preview/shared';

/**
 * 插件中心 API 客户端（仅管理员可调用）。
 * 插件的领域端点仍归各插件自有（如字幕插件继续走 subtitleApi 的 /admin/subtitle/*），
 * 这里只覆盖注册表层：列表 + 启用开关。
 */
export const pluginApi = {
  /** 列出全部插件（注册顺序即展示顺序）。 */
  async list(): Promise<PluginInfo[]> {
    const { data } = await api.get<ApiResponse<PluginInfo[]>>('/admin/plugins');
    return data.data ?? [];
  },

  /** 切换插件启用开关，返回该插件最新快照。 */
  async setEnabled(id: string, enabled: boolean): Promise<PluginInfo> {
    const { data } = await api.put<ApiResponse<PluginInfo>>(
      `/admin/plugins/${encodeURIComponent(id)}/enabled`,
      { enabled },
    );
    return data.data!;
  },
};

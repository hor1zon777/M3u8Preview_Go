import api from './api.js';
import type { ApiResponse, UpdateStatus } from '@m3u8-preview/shared';

/**
 * 应用内自更新 API（/api/v1/admin/update/*）。
 *
 * 服务端把这组路由挂在写闸门之外（与 /admin/ha 同理）：滚动升级要求
 * 先在备节点上执行更新，且更新请求不能被 NODE_READ_ONLY 自动重试重放。
 * 重启窗口的探活复用 haApi.pollHealth（裸 axios 打无鉴权 /api/health）。
 */
export const updateApi = {
  async getStatus(): Promise<UpdateStatus> {
    const { data } = await api.get<ApiResponse<UpdateStatus>>('/admin/update/status');
    return data.data!;
  },

  /** 强制检查一次更新（服务端 10s 节流），返回最新快照。 */
  async check(): Promise<UpdateStatus> {
    const { data } = await api.post<ApiResponse<UpdateStatus>>('/admin/update/check');
    return data.data!;
  },

  /** 开始下载并暂存指定版本（须等于最近检查到的最新版本）；全程异步，进度轮询 status。 */
  async apply(version: string): Promise<UpdateStatus> {
    const { data } = await api.post<ApiResponse<UpdateStatus>>('/admin/update/apply', { version });
    return data.data!;
  },
};

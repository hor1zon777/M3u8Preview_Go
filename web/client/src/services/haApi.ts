import axios from 'axios';
import api from './api.js';
import type { ApiResponse, HaStatus } from '@m3u8-preview/shared';

/**
 * 高可用管理 API（/api/v1/admin/ha/*）。
 *
 * 服务端把这组路由挂在写闸门之外：切换请求必须能在 replica 上发出，
 * 且不能被 api.ts 对 NODE_READ_ONLY 503 的自动重试静默重放。
 */
export const haApi = {
  async getStatus(): Promise<HaStatus> {
    const { data } = await api.get<ApiResponse<HaStatus>>('/admin/ha/status');
    return data.data!;
  },

  /** 发起手动切换。graceful=平滑交接（等音频流结束）；force=跳过等流直接停写交接。 */
  async requestSwitch(mode: 'graceful' | 'force'): Promise<void> {
    await api.post('/admin/ha/switch', { mode });
  },

  /** 撤销进行中的手动切换（幂等）。 */
  async cancelSwitch(): Promise<void> {
    await api.post('/admin/ha/switch/cancel');
  },

  /**
   * 重启窗口探活：切换会让节点进程退出重启，期间带鉴权的 status 请求全部失败。
   * 用裸 axios 轮询无鉴权的 /api/health，避开拦截器的 401 刷新与 HA 重试逻辑；
   * 返回 null 表示节点还没起来。
   */
  async pollHealth(): Promise<{ role?: string; nodeId?: string } | null> {
    try {
      const { data } = await axios.get('/api/health', { timeout: 3000 });
      return data && typeof data === 'object' ? data : null;
    } catch {
      return null;
    }
  },
};

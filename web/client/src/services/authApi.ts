import axios from 'axios';
import api from './api.js';
import type { ApiResponse, AuthResponse, User } from '@m3u8-preview/shared';
import { encryptAuthPayload } from '../utils/crypto.js';

export interface CaptchaPublicConfig {
  enabled: boolean;
  endpoint?: string;
  siteKey?: string;
}

export const authApi = {
  async register(username: string, password: string, captchaToken?: string) {
    const envelope = await encryptAuthPayload('register', { username, password });
    const { data } = await api.post<ApiResponse<AuthResponse>>('/auth/register', {
      ...envelope,
      ...(captchaToken ? { captchaToken } : {}),
    });
    return data.data!;
  },

  async login(username: string, password: string, captchaToken?: string) {
    const envelope = await encryptAuthPayload('login', { username, password });
    const { data } = await api.post<ApiResponse<AuthResponse>>('/auth/login', {
      ...envelope,
      ...(captchaToken ? { captchaToken } : {}),
    });
    return data.data!;
  },

  /**
   * Refresh access token via httpOnly cookie.
   * IMPORTANT: Uses raw axios (not the `api` instance) to bypass the
   * 401 response interceptor, which would otherwise re-trigger refresh
   * and cause request amplification / rate-limit exhaustion.
   */
  async refresh() {
    const { data } = await axios.post<ApiResponse<{ accessToken: string; user: User }>>(
      '/api/v1/auth/refresh',
      {},
      { withCredentials: true, timeout: 10000 },
    );
    if (!data.success || !data.data) {
      throw new Error('Refresh failed');
    }
    return data.data;
  },

  async logout() {
    await api.post('/auth/logout');
  },

  async getMe() {
    const { data } = await api.get<ApiResponse<User>>('/auth/me');
    return data.data!;
  },

  /** 查询注册是否开放（公开接口，使用 axios 避免 401 拦截器干扰） */
  async getRegisterStatus() {
    const { data } = await axios.get<ApiResponse<{ allowRegistration: boolean }>>(
      '/api/v1/auth/register-status',
      { timeout: 10000 },
    );
    return data.data!;
  },

  async getCaptchaConfig() {
    const { data } = await axios.get<ApiResponse<CaptchaPublicConfig>>(
      '/api/v1/auth/captcha-config',
      { timeout: 10000 },
    );
    return data.data!;
  },

  async changePassword(oldPassword: string, newPassword: string) {
    const envelope = await encryptAuthPayload('changePassword', { oldPassword, newPassword });
    const { data } = await api.post<ApiResponse<null>>('/auth/change-password', envelope);
    return data;
  },
};

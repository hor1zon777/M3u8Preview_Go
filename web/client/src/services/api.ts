import axios from 'axios';
import type { ApiResponse, User } from '@m3u8-preview/shared';

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 15000,
  withCredentials: true,
});

// Module-level in-memory token storage (not persisted to localStorage)
let accessToken: string | null = null;

export function getAccessToken(): string | null {
  return accessToken;
}

export function setAccessToken(token: string | null): void {
  accessToken = token;
}

let isRefreshing = false;
let failedQueue: Array<{
  resolve: (value: unknown) => void;
  reject: (reason?: unknown) => void;
}> = [];

function processQueue(error: unknown, token: string | null = null) {
  failedQueue.forEach(({ resolve, reject }) => {
    if (error) {
      reject(error);
    } else {
      resolve(token);
    }
  });
  failedQueue = [];
}

// Request interceptor - attach access token from memory
api.interceptors.request.use((config) => {
  if (accessToken) {
    config.headers.Authorization = `Bearer ${accessToken}`;
  }
  return config;
});

// Auth endpoints where 401 means "wrong credentials", not "token expired"
const AUTH_ENDPOINTS = ['/auth/login', '/auth/register', '/auth/refresh'];

// Response interceptor - auto refresh token on 401
api.interceptors.response.use(
  (response) => response,
  async (error) => {
    const originalRequest = error.config;

    // Skip refresh logic for auth endpoints — their 401 is a business error
    const isAuthEndpoint = AUTH_ENDPOINTS.some(ep => originalRequest.url?.includes(ep));

    if (error.response?.status === 401 && !originalRequest._retry && !isAuthEndpoint) {
      if (isRefreshing) {
        return new Promise((resolve, reject) => {
          failedQueue.push({ resolve, reject });
        }).then((token) => {
          originalRequest.headers.Authorization = `Bearer ${token}`;
          return api(originalRequest);
        });
      }

      originalRequest._retry = true;
      isRefreshing = true;

      try {
        const { data } = await axios.post<ApiResponse<{ accessToken: string; user: User }>>(
          '/api/v1/auth/refresh',
          {},
          { withCredentials: true, timeout: 10000 }
        );

        if (data.success && data.data) {
          const newToken = data.data.accessToken;
          accessToken = newToken;
          processQueue(null, newToken);

          // Update authStore user info if available
          if (data.data.user) {
            const { useAuthStore } = await import('../stores/authStore.js');
            useAuthStore.getState().setUser(data.data.user);
          }

          originalRequest.headers.Authorization = `Bearer ${newToken}`;
          return api(originalRequest);
        }
      } catch (refreshError) {
        processQueue(refreshError, null);
        accessToken = null;
        // 通过 store 清除用户状态，让 ProtectedRoute 自然重定向
        const { useAuthStore } = await import('../stores/authStore.js');
        useAuthStore.getState().setUser(null);
        return Promise.reject(refreshError);
      } finally {
        isRefreshing = false;
      }
    }

    return Promise.reject(error);
  }
);

export default api;

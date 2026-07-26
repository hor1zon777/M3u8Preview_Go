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

/**
 * 主备切换期间，只读副本会用 503 + 这个 code 拒绝写请求（服务端 middleware.RequirePrimary）。
 * 详见 docs/ha-failover.md。
 */
const NODE_READ_ONLY = 'NODE_READ_ONLY';

/** 最多自动重试几次；配合 Retry-After（默认 5s）大约覆盖 15 秒的切换窗口。 */
const HA_MAX_RETRIES = 3;

/** 服务端未给 Retry-After 时的默认退避秒数。 */
const HA_FALLBACK_RETRY_SEC = 5;

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * 主备切换窗口内的写请求自动重试。
 *
 * 之所以敢自动重试非幂等的 POST/PUT/DELETE：这个 503 是由路由层中间件在
 * **业务 handler 执行之前**返回的，请求对服务端没有产生任何副作用，重放是安全的。
 * 若换成在业务层判断，就没有这个保证了。
 */
function haRetryDelayMs(error: any): number | null {
  const res = error?.response;
  if (res?.status !== 503 || res?.data?.code !== NODE_READ_ONLY) return null;

  const cfg = error.config;
  if (!cfg) return null;
  cfg._haRetryCount = (cfg._haRetryCount ?? 0) + 1;
  if (cfg._haRetryCount > HA_MAX_RETRIES) return null;

  const header = Number(res.headers?.['retry-after']);
  const sec = Number.isFinite(header) && header > 0 ? header : HA_FALLBACK_RETRY_SEC;
  return sec * 1000;
}

// Response interceptor - auto refresh token on 401
api.interceptors.response.use(
  (response) => {
    // 服务端约定的统一信封：HTTP 200 + {success: false, error: "..."} 也算业务失败。
    // 如果不在这里转成 reject，调用方会拿到一个看似成功但 data 为 undefined 的响应，
    // 接着 `result.xxx` 直接抛 "Cannot read properties of undefined" 之类的运行时错误，
    // 错误信息泄露到 UI 上极不友好。
    const body = response.data as ApiResponse<unknown> | undefined;
    if (body && typeof body === 'object' && 'success' in body && body.success === false) {
      const synthetic = new axios.AxiosError(
        body.error || 'Request failed',
        'ERR_BUSINESS',
        response.config,
        response.request,
        response,
      );
      return Promise.reject(synthetic);
    }
    return response;
  },
  async (error) => {
    const originalRequest = error.config;

    // 主备切换：当前节点是只读副本（或正在交接领导权）。这是秒级的瞬态状态，
    // 退避重试比把错误抛给用户合适——切换完成后（DNS 也会随之移动）请求自然成功。
    const haDelay = haRetryDelayMs(error);
    if (haDelay !== null) {
      window.dispatchEvent(new CustomEvent('ha:switching'));
      await sleep(haDelay);
      return api(originalRequest);
    }

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

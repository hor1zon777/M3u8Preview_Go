import { create } from 'zustand';
import type { User } from '@m3u8-preview/shared';
import { authApi } from '../services/authApi.js';
import { setAccessToken, getAccessToken } from '../services/api.js';

interface AuthState {
  user: User | null;
  isAuthenticated: boolean;
  isLoading: boolean;

  login: (username: string, password: string) => Promise<void>;
  register: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  checkAuth: () => Promise<void>;
  setUser: (user: User | null) => void;
}

// Module-level guard to prevent concurrent/duplicate checkAuth calls
// (e.g., React StrictMode double-invokes useEffect)
let checkAuthPromise: Promise<void> | null = null;

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  isAuthenticated: false,
  isLoading: true,

  login: async (username, password) => {
    const result = await authApi.login(username, password);
    setAccessToken(result.accessToken);
    set({ user: result.user, isAuthenticated: true, isLoading: false });
  },

  register: async (username, password) => {
    const result = await authApi.register(username, password);
    setAccessToken(result.accessToken);
    set({ user: result.user, isAuthenticated: true, isLoading: false });
  },

  logout: async () => {
    try {
      await authApi.logout();
    } catch (e) {
      // Log warning instead of silently ignoring
      console.warn('Logout request failed, token may still be active on server:', e);
    }
    setAccessToken(null);
    set({ user: null, isAuthenticated: false });
  },

  checkAuth: async () => {
    // Deduplicate concurrent calls (StrictMode, multiple component mounts)
    if (checkAuthPromise) return checkAuthPromise;

    checkAuthPromise = (async () => {
      try {
        // Try to refresh token via httpOnly cookie (page reload scenario)
        if (!getAccessToken()) {
          try {
            const refreshResult = await authApi.refresh();
            setAccessToken(refreshResult.accessToken);
            // If refresh returned user info, use it directly
            if (refreshResult.user) {
              set({ user: refreshResult.user, isAuthenticated: true, isLoading: false });
              return;
            }
          } catch {
            set({ isLoading: false });
            return;
          }
        }
        const user = await authApi.getMe();
        set({ user, isAuthenticated: true, isLoading: false });
      } catch {
        setAccessToken(null);
        set({ user: null, isAuthenticated: false, isLoading: false });
      } finally {
        checkAuthPromise = null;
      }
    })();

    return checkAuthPromise;
  },

  setUser: (user) => set({ user, isAuthenticated: !!user }),
}));

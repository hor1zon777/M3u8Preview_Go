import { useEffect } from 'react';
import { useAuthStore } from '../stores/authStore.js';

export function useAuth() {
  const user = useAuthStore((s) => s.user);
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const isLoading = useAuthStore((s) => s.isLoading);
  const login = useAuthStore((s) => s.login);
  const register = useAuthStore((s) => s.register);
  const logout = useAuthStore((s) => s.logout);
  const checkAuth = useAuthStore((s) => s.checkAuth);
  const setUser = useAuthStore((s) => s.setUser);

  useEffect(() => {
    checkAuth();
  }, [checkAuth]);

  return { user, isAuthenticated, isLoading, login, register, logout, checkAuth, setUser };
}

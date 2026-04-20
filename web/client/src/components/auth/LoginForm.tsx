import { useState, useEffect } from 'react';
import { useNavigate, Link, useLocation, Navigate } from 'react-router-dom';
import { Clapperboard } from 'lucide-react';
import { useAuthStore } from '../../stores/authStore.js';
import { authApi } from '../../services/authApi.js';

export function LoginForm() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [allowRegistration, setAllowRegistration] = useState<boolean | null>(null);
  const login = useAuthStore((s) => s.login);
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    authApi.getRegisterStatus().then((res) => setAllowRegistration(res.allowRegistration)).catch(() => {});
  }, []);

  // 登录后重定向到来源页，过滤登录/注册页防止循环
  const rawFrom = (location.state as { from?: { pathname: string } })?.from?.pathname || '/';
  const from = rawFrom === '/login' || rawFrom === '/register' ? '/' : rawFrom;

  // 声明式重定向后备：已认证用户自动跳转
  if (isAuthenticated) return <Navigate to={from} replace />;

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      await login(username, password);
      navigate(from, { replace: true });
    } catch (err: any) {
      setError(err.response?.data?.error || '登录失败，请重试');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen bg-emby-bg-base flex items-center justify-center px-4">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <Clapperboard className="w-12 h-12 text-emby-green mx-auto mb-3" />
          <h1 className="text-3xl font-bold text-white mb-2">M3u8 Preview</h1>
          <p className="text-emby-text-secondary">登录以继续</p>
        </div>

        <form onSubmit={handleSubmit} className="bg-emby-bg-dialog rounded-md p-6 space-y-4 border border-emby-border-subtle">
          {error && (
            <div className="bg-red-500/10 border border-red-500/20 text-red-400 px-4 py-3 rounded-md text-sm">
              {error}
            </div>
          )}

          <div>
            <label className="block text-sm font-medium text-emby-text-primary mb-1.5">用户名</label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
              placeholder="请输入用户名"
              required
              autoFocus
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-emby-text-primary mb-1.5">密码</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
              placeholder="请输入密码"
              required
            />
          </div>

          <button
            type="submit"
            disabled={loading}
            className="w-full py-2.5 bg-emby-green hover:bg-emby-green-dark disabled:opacity-50 text-white font-medium rounded-md transition-colors"
          >
            {loading ? '登录中...' : '登录'}
          </button>

          {allowRegistration && (
            <p className="text-center text-sm text-emby-text-secondary">
              还没有账号？{' '}
              <Link to="/register" className="text-emby-green-light hover:text-emby-green-hover">
                注册
              </Link>
            </p>
          )}
        </form>
      </div>
    </div>
  );
}

import { useState, useEffect } from 'react';
import { useNavigate, Link, Navigate } from 'react-router-dom';
import { Clapperboard } from 'lucide-react';
import { useAuthStore } from '../../stores/authStore.js';
import { authApi } from '../../services/authApi.js';

export function RegisterForm() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [allowRegistration, setAllowRegistration] = useState<boolean | null>(null);
  const register = useAuthStore((s) => s.register);
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const navigate = useNavigate();

  useEffect(() => {
    authApi.getRegisterStatus().then((res) => setAllowRegistration(res.allowRegistration)).catch(() => setAllowRegistration(true));
  }, []);

  // 声明式重定向后备：已认证用户自动跳转
  if (isAuthenticated) return <Navigate to="/" replace />;

  // 加载中
  if (allowRegistration === null) {
    return (
      <div className="min-h-screen bg-emby-bg-base flex items-center justify-center px-4">
        <div className="w-full max-w-md text-center">
          <Clapperboard className="w-12 h-12 text-emby-green mx-auto mb-3" />
          <p className="text-emby-text-secondary">加载中...</p>
        </div>
      </div>
    );
  }

  // 注册已关闭
  if (!allowRegistration) {
    return (
      <div className="min-h-screen bg-emby-bg-base flex items-center justify-center px-4">
        <div className="w-full max-w-md">
          <div className="text-center mb-8">
            <Clapperboard className="w-12 h-12 text-emby-green mx-auto mb-3" />
            <h1 className="text-3xl font-bold text-white mb-2">M3u8 Preview</h1>
          </div>
          <div className="bg-emby-bg-dialog rounded-md p-6 border border-emby-border-subtle text-center">
            <p className="text-emby-text-primary mb-4">注册功能已关闭</p>
            <p className="text-emby-text-muted text-sm mb-6">管理员已暂停新用户注册，如需账号请联系管理员。</p>
            <Link
              to="/login"
              className="inline-block px-6 py-2.5 bg-emby-green hover:bg-emby-green-dark text-white font-medium rounded-md transition-colors"
            >
              返回登录
            </Link>
          </div>
        </div>
      </div>
    );
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError('');

    if (password !== confirmPassword) {
      setError('两次密码输入不一致');
      return;
    }

    setLoading(true);
    try {
      await register(username, password);
      navigate('/');
    } catch (err: any) {
      setError(err.response?.data?.error || '注册失败，请重试');
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
          <p className="text-emby-text-secondary">创建新账号</p>
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
              placeholder="3-50个字符，字母数字下划线"
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
              placeholder="至少8个字符，含大小写和数字"
              required
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-emby-text-primary mb-1.5">确认密码</label>
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
              placeholder="再次输入密码"
              required
            />
          </div>

          <button
            type="submit"
            disabled={loading}
            className="w-full py-2.5 bg-emby-green hover:bg-emby-green-dark disabled:opacity-50 text-white font-medium rounded-md transition-colors"
          >
            {loading ? '注册中...' : '注册'}
          </button>

          <p className="text-center text-sm text-emby-text-secondary">
            已有账号？{' '}
            <Link to="/login" className="text-emby-green-light hover:text-emby-green-hover">
              登录
            </Link>
          </p>
        </form>
      </div>
    </div>
  );
}

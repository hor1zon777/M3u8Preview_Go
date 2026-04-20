import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowLeft, Lock } from 'lucide-react';
import { authApi } from '../services/authApi.js';
import { useAuthStore } from '../stores/authStore.js';

export function ChangePasswordPage() {
  const [oldPassword, setOldPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();
  const logout = useAuthStore(s => s.logout);

  function validate(): string | null {
    if (!oldPassword) return '请输入旧密码';
    if (newPassword.length < 8) return '新密码至少8个字符';
    if (!/(?=.*[a-z])(?=.*[A-Z])(?=.*\d)/.test(newPassword)) {
      return '新密码须包含大写字母、小写字母和数字';
    }
    if (newPassword !== confirmPassword) return '两次输入的新密码不一致';
    if (oldPassword === newPassword) return '新密码不能与旧密码相同';
    return null;
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const validationError = validate();
    if (validationError) {
      setError(validationError);
      return;
    }

    setError('');
    setLoading(true);
    try {
      await authApi.changePassword(oldPassword, newPassword);
      await logout();
      navigate('/login', { replace: true });
    } catch (err: any) {
      setError(err.response?.data?.error || '密码修改失败，请重试');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="max-w-md mx-auto">
      <button
        onClick={() => navigate(-1)}
        className="flex items-center gap-1.5 text-sm text-emby-text-secondary hover:text-white mb-6 transition-colors"
      >
        <ArrowLeft className="w-4 h-4" />
        返回
      </button>

      <div className="flex items-center gap-3 mb-6">
        <Lock className="w-6 h-6 text-emby-text-secondary" />
        <h1 className="text-2xl font-bold text-white">修改密码</h1>
      </div>

      <form onSubmit={handleSubmit} className="bg-emby-bg-dialog rounded-md p-6 space-y-4 border border-emby-border-subtle">
        {error && (
          <div className="bg-red-500/10 border border-red-500/20 text-red-400 px-4 py-3 rounded-md text-sm">
            {error}
          </div>
        )}

        <div>
          <label className="block text-sm font-medium text-emby-text-primary mb-1.5">旧密码</label>
          <input
            type="password"
            value={oldPassword}
            onChange={e => setOldPassword(e.target.value)}
            className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
            placeholder="请输入当前密码"
            required
            autoFocus
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-emby-text-primary mb-1.5">新密码</label>
          <input
            type="password"
            value={newPassword}
            onChange={e => setNewPassword(e.target.value)}
            className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
            placeholder="至少8位，含大小写字母和数字"
            required
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-emby-text-primary mb-1.5">确认新密码</label>
          <input
            type="password"
            value={confirmPassword}
            onChange={e => setConfirmPassword(e.target.value)}
            className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent"
            placeholder="再次输入新密码"
            required
          />
        </div>

        <button
          type="submit"
          disabled={loading}
          className="w-full py-2.5 bg-emby-green hover:bg-emby-green-dark disabled:opacity-50 text-white font-medium rounded-md transition-colors"
        >
          {loading ? '提交中...' : '确认修改'}
        </button>

        <p className="text-xs text-emby-text-muted text-center">
          修改密码后将自动退出登录，需要使用新密码重新登录
        </p>
      </form>
    </div>
  );
}

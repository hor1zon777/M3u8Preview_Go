import { useAuthStore } from '../../stores/authStore.js';
import { useNavigate, NavLink, Link } from 'react-router-dom';
import { useState, useRef, useEffect } from 'react';
import {
  Clapperboard, Search, Film, ListVideo, Heart, Clock,
  Settings, Users, MonitorPlay, Download, Home, LogOut, ChevronDown, Lock, User, FolderTree,
} from 'lucide-react';

export function Header() {
  const user = useAuthStore(s => s.user);
  const logout = useAuthStore(s => s.logout);
  const navigate = useNavigate();
  const [showDropdown, setShowDropdown] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [showMobileSearch, setShowMobileSearch] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  const isAdmin = user?.role === 'ADMIN';

  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setShowDropdown(false);
      }
    }
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  function handleSearch(e: React.FormEvent) {
    e.preventDefault();
    if (searchQuery.trim()) {
      navigate(`/library?search=${encodeURIComponent(searchQuery.trim())}`);
      setSearchQuery('');
      setShowMobileSearch(false);
    }
  }

  async function handleLogout() {
    setShowDropdown(false);
    await logout();
    navigate('/login');
  }

  function handleDropdownNav(to: string) {
    setShowDropdown(false);
    navigate(to);
  }

  return (
    <header className="h-14 bg-gradient-to-b from-black/80 to-transparent flex items-center px-4 lg:px-6 gap-2 lg:gap-4 sticky top-0 z-30">
      {/* Logo */}
      <Link to="/" className="flex items-center gap-2 flex-shrink-0 mr-2 lg:mr-4">
        <Clapperboard className="w-6 h-6 text-emby-green" />
        <span className="text-lg font-bold text-emby-green hidden lg:block">M3u8 Preview</span>
      </Link>

      {/* Desktop nav links */}
      <nav className="hidden lg:flex items-center gap-1">
        <NavLink
          to="/library"
          className={({ isActive }) =>
            `flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-md transition-colors relative ${
              isActive
                ? 'text-emby-green font-medium'
                : 'text-emby-text-secondary hover:text-white'
            }`
          }
        >
          {({ isActive }) => (
            <>
              <Film className="w-4 h-4" />
              <span>我的媒体</span>
              {isActive && (
                <span className="absolute bottom-0 left-3 right-3 h-0.5 bg-emby-green rounded-full" />
              )}
            </>
          )}
        </NavLink>
        <NavLink
          to="/playlists"
          className={({ isActive }) =>
            `flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-md transition-colors relative ${
              isActive
                ? 'text-emby-green font-medium'
                : 'text-emby-text-secondary hover:text-white'
            }`
          }
        >
          {({ isActive }) => (
            <>
              <ListVideo className="w-4 h-4" />
              <span>合集</span>
              {isActive && (
                <span className="absolute bottom-0 left-3 right-3 h-0.5 bg-emby-green rounded-full" />
              )}
            </>
          )}
        </NavLink>
        <NavLink
          to="/artists"
          className={({ isActive }) =>
            `flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-md transition-colors relative ${
              isActive
                ? 'text-emby-green font-medium'
                : 'text-emby-text-secondary hover:text-white'
            }`
          }
        >
          {({ isActive }) => (
            <>
              <User className="w-4 h-4" />
              <span>作者</span>
              {isActive && (
                <span className="absolute bottom-0 left-3 right-3 h-0.5 bg-emby-green rounded-full" />
              )}
            </>
          )}
        </NavLink>
        <NavLink
          to="/categories"
          className={({ isActive }) =>
            `flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-md transition-colors relative ${
              isActive
                ? 'text-emby-green font-medium'
                : 'text-emby-text-secondary hover:text-white'
            }`
          }
        >
          {({ isActive }) => (
            <>
              <FolderTree className="w-4 h-4" />
              <span>分类</span>
              {isActive && (
                <span className="absolute bottom-0 left-3 right-3 h-0.5 bg-emby-green rounded-full" />
              )}
            </>
          )}
        </NavLink>
      </nav>

      {/* Spacer */}
      <div className="flex-1" />

      {/* Desktop search */}
      <form onSubmit={handleSearch} className="hidden lg:block max-w-xs w-64">
        <div className="relative">
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="搜索媒体..."
            className="w-full pl-10 pr-4 py-1.5 bg-emby-bg-input border border-emby-border rounded-lg text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent text-sm"
          />
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-emby-text-muted" />
        </div>
      </form>

      {/* Mobile search toggle */}
      <button
        onClick={() => setShowMobileSearch(!showMobileSearch)}
        className="lg:hidden p-2 text-emby-text-secondary hover:text-white transition-colors"
      >
        <Search className="w-5 h-5" />
      </button>

      {/* User dropdown */}
      <div className="relative" ref={dropdownRef}>
        <button
          onClick={() => setShowDropdown(!showDropdown)}
          className="flex items-center gap-1.5 px-2 py-1.5 rounded-lg hover:bg-white/5 transition-colors"
        >
          <div className="w-8 h-8 bg-emby-green rounded-full flex items-center justify-center text-white text-sm font-medium">
            {user?.username?.charAt(0).toUpperCase()}
          </div>
          <ChevronDown className={`w-4 h-4 text-emby-text-secondary transition-transform ${showDropdown ? 'rotate-180' : ''}`} />
        </button>

        {showDropdown && (
          <div className="absolute right-0 top-full mt-2 w-52 bg-emby-bg-dialog border border-emby-border rounded-lg shadow-xl py-1 animate-dropdown">
            {/* User info */}
            <div className="px-4 py-2.5 border-b border-emby-border">
              <p className="text-sm font-medium text-white">{user?.username}</p>
              <p className="text-xs text-emby-text-secondary">{user?.role === 'ADMIN' ? '管理员' : '用户'}</p>
            </div>

            {/* Mobile-only nav items */}
            <div className="lg:hidden border-b border-emby-border py-1">
              <DropdownItem icon={Home} label="首页" onClick={() => handleDropdownNav('/')} />
              <DropdownItem icon={Film} label="我的媒体" onClick={() => handleDropdownNav('/library')} />
              <DropdownItem icon={ListVideo} label="合集" onClick={() => handleDropdownNav('/playlists')} />
              <DropdownItem icon={User} label="作者" onClick={() => handleDropdownNav('/artists')} />
              <DropdownItem icon={FolderTree} label="分类" onClick={() => handleDropdownNav('/categories')} />
            </div>

            {/* Common nav items */}
            <div className="border-b border-emby-border py-1">
              <DropdownItem icon={Heart} label="收藏" onClick={() => handleDropdownNav('/favorites')} />
              <DropdownItem icon={Clock} label="观看历史" onClick={() => handleDropdownNav('/history')} />
            </div>

            {/* Admin items */}
            {isAdmin && (
              <div className="border-b border-emby-border py-1">
                <DropdownItem icon={Settings} label="管理面板" onClick={() => handleDropdownNav('/admin')} />
                <DropdownItem icon={Users} label="用户管理" onClick={() => handleDropdownNav('/admin/users')} />
                <DropdownItem icon={MonitorPlay} label="媒体管理" onClick={() => handleDropdownNav('/admin/media')} />
                <DropdownItem icon={FolderTree} label="分类管理" onClick={() => handleDropdownNav('/admin/categories')} />
                <DropdownItem icon={Download} label="导入媒体" onClick={() => handleDropdownNav('/import')} />
              </div>
            )}

            {/* Logout */}
            <div className="py-1">
              <DropdownItem icon={Lock} label="修改密码" onClick={() => handleDropdownNav('/change-password')} />
              <button
                onClick={handleLogout}
                className="w-full flex items-center gap-3 px-4 py-2 text-sm text-red-400 hover:bg-emby-bg-elevated transition-colors"
              >
                <LogOut className="w-4 h-4" />
                退出登录
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Mobile search bar (expandable) */}
      {showMobileSearch && (
        <div className="absolute top-14 left-0 right-0 bg-emby-bg-card border-b border-emby-border px-4 py-2 lg:hidden animate-dropdown">
          <form onSubmit={handleSearch}>
            <div className="relative">
              <input
                type="text"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="搜索媒体..."
                autoFocus
                className="w-full pl-10 pr-4 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent text-sm"
              />
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-emby-text-muted" />
            </div>
          </form>
        </div>
      )}
    </header>
  );
}

/** 下拉菜单通用项 */
function DropdownItem({ icon: Icon, label, onClick }: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className="w-full flex items-center gap-3 px-4 py-2 text-sm text-emby-text-primary hover:bg-emby-bg-elevated transition-colors"
    >
      <Icon className="w-4 h-4 text-emby-text-secondary" />
      {label}
    </button>
  );
}

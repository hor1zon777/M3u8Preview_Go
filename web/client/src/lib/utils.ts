import { type ClassValue, clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';

const SCROLL_POSITIONS_KEY = 'route-scroll-positions';
const PENDING_SCROLL_RESTORE_KEY = 'pending-scroll-restore-route';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatDuration(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}:${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`;
  return `${m}:${s.toString().padStart(2, '0')}`;
}

export function formatDate(dateStr: string): string {
  return new Date(dateStr).toLocaleDateString('zh-CN', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
}

export function truncate(str: string, maxLen: number): string {
  if (str.length <= maxLen) return str;
  return str.slice(0, maxLen) + '...';
}

export function buildRouteKey(pathname: string, search = ''): string {
  return `${pathname}${search}`;
}

export function saveCurrentRouteScrollPosition(routeKey: string): void {
  try {
    const stored = sessionStorage.getItem(SCROLL_POSITIONS_KEY);
    const positions = stored ? JSON.parse(stored) as Record<string, number> : {};
    sessionStorage.setItem(SCROLL_POSITIONS_KEY, JSON.stringify({
      ...positions,
      [routeKey]: window.scrollY,
    }));
  } catch {
    // sessionStorage 不可用时忽略
  }
}

export function getSavedRouteScrollPosition(routeKey: string): number | null {
  try {
    const stored = sessionStorage.getItem(SCROLL_POSITIONS_KEY);
    if (!stored) return null;
    const positions = JSON.parse(stored) as Record<string, number>;
    return typeof positions[routeKey] === 'number' ? positions[routeKey] : null;
  } catch {
    return null;
  }
}

export function setPendingScrollRestore(routeKey: string): void {
  try {
    sessionStorage.setItem(PENDING_SCROLL_RESTORE_KEY, routeKey);
  } catch {
    // sessionStorage 不可用时忽略
  }
}

export function getPendingScrollRestore(): string | null {
  try {
    return sessionStorage.getItem(PENDING_SCROLL_RESTORE_KEY);
  } catch {
    return null;
  }
}

export function clearPendingScrollRestore(): void {
  try {
    sessionStorage.removeItem(PENDING_SCROLL_RESTORE_KEY);
  } catch {
    // sessionStorage 不可用时忽略
  }
}

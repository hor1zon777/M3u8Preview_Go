import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { authApi } from '../services/authApi.js';

/** 默认站点名，与后端 migrate.go / GetSiteName 回退值保持一致。 */
export const DEFAULT_SITE_NAME = 'M3u8 Preview';

/**
 * useSiteName 通过公开 /auth/site-info 接口拉取站点显示名称，
 * 并写回 document.title。
 *
 * - queryKey 全局共享，登录前/后任意组件复用同一份缓存
 * - admin 修改 siteName 后通过 queryClient.invalidateQueries(['site-info']) 触发刷新
 * - 网络/接口异常时回退到 DEFAULT_SITE_NAME，避免 UI 空字符串
 */
export function useSiteName(): string {
  const { data } = useQuery({
    queryKey: ['site-info'],
    queryFn: () => authApi.getSiteInfo(),
    staleTime: 1000 * 60 * 5,
    retry: 1,
  });

  const siteName = (data?.siteName && data.siteName.trim()) || DEFAULT_SITE_NAME;

  useEffect(() => {
    if (typeof document !== 'undefined') {
      document.title = siteName;
    }
  }, [siteName]);

  return siteName;
}

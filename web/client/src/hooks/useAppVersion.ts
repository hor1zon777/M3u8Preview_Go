import { useQuery } from '@tanstack/react-query';
import axios from 'axios';

/** /api/health 的响应。前后端同镜像发布，所以这一个版本号覆盖两端。 */
export interface AppVersionInfo {
  /** 语义化版本号，取自仓库根目录 VERSION 文件；未注入时为 'dev'。 */
  version: string;
  /** git 提交短哈希；未注入时为 'unknown'。 */
  commit: string;
  /** 构建时刻（RFC3339，UTC）；未注入时为 'unknown'。 */
  buildTime: string;
  /** 主备角色，'primary' | 'replica'。单机部署恒为 primary。 */
  role?: string;
  /** 节点标识；未启用主备时为空。 */
  nodeId?: string;
}

/** 后端未注入构建信息时的占位值，展示层据此隐藏对应字段。 */
const UNKNOWN = 'unknown';

/**
 * useAppVersion 读取 /api/health 暴露的版本与节点信息。
 *
 * 走无鉴权的 /api/health 而不是 /api/v1 下的接口，是为了让登录页页脚在
 * 未登录状态下也能显示版本——排查线上问题时不必先登录就能确认版本。
 * 因此这里直接用 axios，绕开 api.ts 那套 token 刷新/业务信封拦截器。
 *
 * 版本在一次页面生命周期内不会变（换版本必然伴随刷新），故 staleTime 设为
 * Infinity，多处调用共享同一份缓存，不会重复请求。
 */
export function useAppVersion(): AppVersionInfo | undefined {
  const { data } = useQuery({
    queryKey: ['app-version'],
    queryFn: async (): Promise<AppVersionInfo> => {
      const { data } = await axios.get<AppVersionInfo>('/api/health', { timeout: 5000 });
      return data;
    },
    staleTime: Infinity,
    retry: 1,
  });
  return data;
}

/** 格式化为 "v0.1.0 · 877c77a"；commit 未注入时只返回版本号。 */
export function formatVersion(info?: AppVersionInfo): string {
  if (!info?.version) return '';
  const v = `v${info.version}`;
  if (!info.commit || info.commit === UNKNOWN) return v;
  return `${v} · ${info.commit}`;
}

/** 格式化构建时间为本地时区可读串；未注入或无法解析时返回空串。 */
export function formatBuildTime(info?: AppVersionInfo): string {
  if (!info?.buildTime || info.buildTime === UNKNOWN) return '';
  const t = new Date(info.buildTime);
  if (Number.isNaN(t.getTime())) return '';
  return t.toLocaleString();
}

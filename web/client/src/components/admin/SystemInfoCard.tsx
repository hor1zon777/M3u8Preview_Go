import { Info } from 'lucide-react';
import { useAppVersion, formatBuildTime } from '../../hooks/useAppVersion.js';

/**
 * 系统信息卡片：版本号、git commit、构建时间，以及主备节点角色。
 *
 * 节点信息只在启用主备高可用时才有（见 docs/ha-failover.md）——单机部署下
 * role 恒为 primary、nodeId 为空，此时不展示节点那一行，避免给单机用户
 * 增加无意义的概念。
 */
const UNKNOWN = 'unknown';

function Row({ label, value, mono = true }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5">
      <span className="text-sm text-emby-text-secondary flex-shrink-0">{label}</span>
      <span className={`text-sm text-white truncate ${mono ? 'font-mono' : ''}`}>{value}</span>
    </div>
  );
}

export function SystemInfoCard() {
  const info = useAppVersion();
  if (!info) return null;

  const buildTime = formatBuildTime(info);
  const hasCommit = Boolean(info.commit) && info.commit !== UNKNOWN;
  // nodeId 非空即代表配置了主备；单机部署不显示节点行。
  const isHA = Boolean(info.nodeId);

  return (
    <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5">
      <div className="flex items-center gap-2 mb-3">
        <Info className="w-5 h-5 text-emby-text-secondary" />
        <h3 className="text-white font-semibold">系统信息</h3>
      </div>

      <div className="divide-y divide-emby-border-subtle">
        <Row label="版本" value={`v${info.version}`} />
        {hasCommit && <Row label="提交" value={info.commit} />}
        {buildTime && <Row label="构建时间" value={buildTime} mono={false} />}
        {isHA && (
          <div className="flex items-baseline justify-between gap-4 py-1.5">
            <span className="text-sm text-emby-text-secondary flex-shrink-0">当前节点</span>
            <span className="text-sm text-white truncate font-mono">
              {info.nodeId}
              <span
                className={`ml-2 px-1.5 py-0.5 rounded text-xs ${
                  info.role === 'primary'
                    ? 'bg-emby-green/20 text-emby-green-light'
                    : 'bg-amber-500/20 text-amber-300'
                }`}
              >
                {info.role === 'primary' ? '主' : '备'}
              </span>
            </span>
          </div>
        )}
      </div>
    </div>
  );
}

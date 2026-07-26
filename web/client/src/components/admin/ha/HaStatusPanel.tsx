import { ArrowLeftRight, CircleCheck, CircleX, Clock, Crown, Server, TriangleAlert } from 'lucide-react';
import type { HaStatus } from '@m3u8-preview/shared';

/**
 * HA 状态面板：本机 / 对端 / 租约三卡 + 手动切换入口 + 自动回切开关。
 * 数据全部来自 GET /admin/ha/status 的缓存快照（对端与租约非实时）。
 */
export function HaStatusPanel({
  status,
  onSwitchClick,
  onToggleAutoFailback,
  autoFailbackPending,
}: {
  status: HaStatus;
  onSwitchClick: () => void;
  onToggleAutoFailback: (next: boolean) => void;
  autoFailbackPending: boolean;
}) {
  const { local, peer, lease } = status;
  const isPrimary = local.role === 'primary';
  const aborted = status.switch.phase === 'aborted';

  return (
    <div className="space-y-4">
      {/* 最近一次手动切换被中止的提示 */}
      {aborted && (
        <div className="flex items-start gap-2 rounded-md border border-yellow-500/40 bg-yellow-500/10 px-4 py-3">
          <TriangleAlert className="w-4 h-4 text-yellow-300 mt-0.5 flex-shrink-0" />
          <div className="text-sm text-yellow-200">
            <p className="font-medium">上次手动切换未完成</p>
            <p className="text-xs mt-0.5 text-yellow-200/80">{status.switch.lastError}</p>
          </div>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        {/* 本机 */}
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-4">
          <div className="flex items-center gap-2 mb-3">
            <Server className="w-4 h-4 text-emby-text-secondary" />
            <h3 className="text-white font-semibold text-sm">本机节点</h3>
            <RoleBadge role={local.role} />
            {local.preferred && (
              <span className="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded bg-emby-bg-elevated border border-emby-border text-emby-text-secondary">
                <Crown className="w-3 h-3" /> 首选主
              </span>
            )}
          </div>
          <InfoRow label="节点 ID" value={local.nodeId || '-'} mono />
          <InfoRow label="复制位点 (TXID)" value={local.txid || '-'} mono />
          <InfoRow label="租约世代" value={String(local.epoch)} mono />
          <InfoRow label="音频流" value={`${local.busyStreams} 条进行中`} />
          {local.draining && (
            <p className="text-xs text-yellow-300 mt-2">停写中（正在交接领导权）</p>
          )}
        </div>

        {/* 对端 */}
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-4">
          <div className="flex items-center gap-2 mb-3">
            <Server className="w-4 h-4 text-emby-text-secondary" />
            <h3 className="text-white font-semibold text-sm">对端节点</h3>
            {peer ? (
              peer.reachable ? (
                <span className="inline-flex items-center gap-1 text-[11px] text-emby-green">
                  <CircleCheck className="w-3.5 h-3.5" /> 可达
                </span>
              ) : (
                <span className="inline-flex items-center gap-1 text-[11px] text-red-400">
                  <CircleX className="w-3.5 h-3.5" /> 不可达
                </span>
              )
            ) : (
              <span className="text-[11px] text-emby-text-muted">尚未探测</span>
            )}
          </div>
          {peer ? (
            <>
              <InfoRow label="节点 ID" value={peer.nodeId || '-'} mono />
              {peer.reachable ? (
                <>
                  <InfoRow label="角色" value={peer.role || '-'} />
                  <InfoRow label="复制位点 (TXID)" value={peer.txid || '-'} mono />
                  <InfoRow label="音频流" value={`${peer.busyStreams} 条进行中`} />
                </>
              ) : (
                <>
                  <InfoRow label="连续失败" value={`${peer.consecutiveFailures} 次`} />
                  {peer.error && (
                    <p className="text-xs text-red-400/80 mt-1 break-all">{peer.error}</p>
                  )}
                  {isPrimary && (
                    <p className="text-xs text-emby-text-muted mt-2">
                      对端宕机时无需手动操作：租约过期后备节点会自动接管。
                    </p>
                  )}
                </>
              )}
              {peer.lastProbeAt && (
                <p className="text-[11px] text-emby-text-muted mt-2 flex items-center gap-1">
                  <Clock className="w-3 h-3" /> 探测于 {formatAgo(peer.lastProbeAt)}
                </p>
              )}
            </>
          ) : (
            <p className="text-xs text-emby-text-muted">Agent 刚启动，等待第一轮探测…</p>
          )}
        </div>

        {/* 租约 */}
        <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-4">
          <div className="flex items-center gap-2 mb-3">
            <Crown className="w-4 h-4 text-emby-text-secondary" />
            <h3 className="text-white font-semibold text-sm">Cloudflare 租约</h3>
          </div>
          {lease ? (
            <>
              <InfoRow label="持有者" value={lease.owner} mono />
              <InfoRow label="世代 (epoch)" value={String(lease.epoch)} mono />
              <InfoRow label="状态" value={lease.state === 'draining' ? '交接停写中' : '正常持有'} />
              <InfoRow label="到期时刻" value={formatTime(lease.expiresAt)} />
              <p className="text-[11px] text-emby-text-muted mt-2 flex items-center gap-1">
                <Clock className="w-3 h-3" /> 读取于 {formatAgo(lease.readAt)}
              </p>
            </>
          ) : (
            <p className="text-xs text-emby-text-muted">尚未读到租约（Agent 刚启动或 Cloudflare 暂不可达）。</p>
          )}
        </div>
      </div>

      {/* 操作区 */}
      <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-4 space-y-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-white text-sm font-medium">手动主/备切换</p>
            <p className="text-emby-text-muted text-xs mt-0.5">
              {isPrimary
                ? '把领导权交给对端节点，本机将重启为备用节点'
                : '请求对端交还领导权，本机将重启为主节点'}
            </p>
          </div>
          <button
            onClick={onSwitchClick}
            className="inline-flex items-center gap-2 px-4 py-2 bg-emby-green text-white text-sm font-medium rounded-md hover:bg-emby-green-dark transition-colors"
          >
            <ArrowLeftRight className="w-4 h-4" />
            {isPrimary ? '切换到备用节点' : '将本机升为主节点'}
          </button>
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-emby-border-subtle pt-4">
          <div className="flex-1 min-w-0">
            <p className="text-white text-sm">
              自动回切
              {!status.autoFailback && (
                <span className="ml-2 px-1.5 py-0.5 text-[10px] rounded bg-yellow-500/15 border border-yellow-500/40 text-yellow-300">
                  已禁用
                </span>
              )}
            </p>
            <p className="text-emby-text-muted text-xs mt-0.5">
              开启时首选主节点恢复后会自动收回领导权；手动切换会自动关闭它，避免刚切过去又被切回来。
              {!isPrimary && '（该开关需在当前主节点上修改）'}
            </p>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={status.autoFailback}
            disabled={autoFailbackPending || !isPrimary}
            onClick={() => onToggleAutoFailback(!status.autoFailback)}
            className={`shrink-0 relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-emby-green focus:ring-offset-2 focus:ring-offset-emby-bg-card disabled:opacity-50 ${
              status.autoFailback ? 'bg-emby-green' : 'bg-emby-bg-input'
            }`}
          >
            <span
              className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                status.autoFailback ? 'translate-x-6' : 'translate-x-1'
              }`}
            />
          </button>
        </div>
      </div>
    </div>
  );
}

export function RoleBadge({ role }: { role: string }) {
  const primary = role === 'primary';
  return (
    <span
      className={`px-1.5 py-0.5 text-[10px] font-medium rounded border ${
        primary
          ? 'bg-emby-green/15 border-emby-green/40 text-emby-green'
          : 'bg-blue-500/10 border-blue-500/30 text-blue-400'
      }`}
    >
      {primary ? '主节点' : '备用节点'}
    </span>
  );
}

function InfoRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between text-xs py-0.5">
      <span className="text-emby-text-muted">{label}</span>
      <span className={`text-emby-text-primary ${mono ? 'font-mono' : ''} truncate max-w-[60%]`} title={value}>
        {value}
      </span>
    </div>
  );
}

function formatTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

function formatAgo(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const sec = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (sec < 60) return `${sec} 秒前`;
  if (sec < 3600) return `${Math.floor(sec / 60)} 分钟前`;
  return new Date(iso).toLocaleString();
}

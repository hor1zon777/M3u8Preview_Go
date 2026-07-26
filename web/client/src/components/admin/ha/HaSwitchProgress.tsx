import { CircleCheck, Loader2, TriangleAlert } from 'lucide-react';
import type { HaStatus, HaSwitchPhase } from '@m3u8-preview/shared';

const STEPS: Array<{ key: string; label: string }> = [
  { key: 'requested', label: '请求受理' },
  { key: 'waiting-streams', label: '等待音频流结束' },
  { key: 'draining', label: '停写 · 对端追平' },
  { key: 'switching', label: '交接 · 进程重启' },
];

/** phase → 当前处于第几步（-1 表示不在进行中）。 */
function stepIndex(phase: HaSwitchPhase): number {
  switch (phase) {
    case 'requested':
      return 0;
    case 'waiting-streams':
      return 1;
    case 'draining':
      return 2;
    case 'switching':
      return 3;
    default:
      return -1;
  }
}

/**
 * 交接进行中的进度视图。自动交接（回切）也会走到这里，
 * 只是"升级为强制 / 取消"按钮仅对手动发起的切换显示。
 */
export function HaSwitchProgress({
  status,
  onForceUpgrade,
  onCancel,
  actionPending,
}: {
  status: HaStatus;
  onForceUpgrade: () => void;
  onCancel: () => void;
  actionPending: boolean;
}) {
  const phase = status.switch.phase;
  const active = stepIndex(phase);
  const isPrimary = status.local.role === 'primary';
  const busy = isPrimary ? status.local.busyStreams : status.peer?.busyStreams ?? 0;

  return (
    <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5 space-y-5">
      <div>
        <h3 className="text-white font-semibold flex items-center gap-2">
          <Loader2 className="w-4 h-4 animate-spin text-emby-green" />
          {status.switch.manual ? '手动切换进行中' : '计划内交接进行中'}
          {status.switch.force && (
            <span className="px-1.5 py-0.5 text-[10px] rounded bg-red-500/15 border border-red-500/40 text-red-300">
              强制模式
            </span>
          )}
        </h3>
        <p className="text-xs text-emby-text-muted mt-1">
          {isPrimary
            ? '本机正在把领导权交给对端，完成后进程会退出并以备用节点身份重启。'
            : '已向主节点发出交还请求，对端完成停写交接后本机会重启为主节点。'}
        </p>
      </div>

      {/* 步骤条 */}
      <ol className="space-y-3">
        {STEPS.map((s, i) => {
          const done = active > i;
          const current = active === i;
          return (
            <li key={s.key} className="flex items-center gap-3">
              {done ? (
                <CircleCheck className="w-5 h-5 text-emby-green flex-shrink-0" />
              ) : current ? (
                <Loader2 className="w-5 h-5 animate-spin text-emby-green flex-shrink-0" />
              ) : (
                <span className="w-5 h-5 rounded-full border border-emby-border flex-shrink-0" />
              )}
              <div className="min-w-0">
                <span className={`text-sm ${current ? 'text-white' : done ? 'text-emby-text-secondary' : 'text-emby-text-muted'}`}>
                  {s.label}
                </span>
                {current && s.key === 'waiting-streams' && (
                  <span className="ml-2 text-xs text-yellow-300">仍有 {busy} 条音频流进行中</span>
                )}
                {current && s.key === 'draining' && (
                  <span className="ml-2 text-xs text-emby-text-muted">
                    停写上限 90 秒，追不平会自动中止并恢复写入
                  </span>
                )}
              </div>
            </li>
          );
        })}
      </ol>

      {phase === 'switching' && (
        <p className="text-xs text-emby-text-muted flex items-start gap-1.5">
          <TriangleAlert className="w-3.5 h-3.5 mt-0.5 flex-shrink-0 text-yellow-300" />
          节点进程即将退出重启，本页面会短暂失去连接，恢复后自动刷新状态。
        </p>
      )}

      {/* 手动切换才提供的操作 */}
      {status.switch.manual && phase !== 'switching' && (
        <div className="flex items-center gap-2 border-t border-emby-border-subtle pt-4">
          {!status.switch.force && (phase === 'requested' || phase === 'waiting-streams') && (
            <button
              onClick={onForceUpgrade}
              disabled={actionPending}
              className="px-3 py-1.5 text-sm rounded-md bg-red-600 text-white hover:bg-red-700 transition-colors disabled:opacity-50"
            >
              升级为强制切换
            </button>
          )}
          <button
            onClick={onCancel}
            disabled={actionPending}
            className="px-3 py-1.5 text-sm rounded-md bg-emby-bg-elevated border border-emby-border text-emby-text-primary hover:bg-emby-bg-input transition-colors disabled:opacity-50"
          >
            取消切换
          </button>
        </div>
      )}
    </div>
  );
}

import { useState } from 'react';
import { ArrowLeftRight, Loader2, TriangleAlert, X } from 'lucide-react';
import type { HaStatus } from '@m3u8-preview/shared';

/**
 * 手动切换确认弹窗：选择平滑/强制模式，展示当前音频流数量与复制位点差距。
 * 从首选主节点切走时会自动禁用自动回切（服务端在进入停写前落库），这里只做提示。
 */
export function HaSwitchDialog({
  status,
  pending,
  error,
  onConfirm,
  onClose,
}: {
  status: HaStatus;
  pending: boolean;
  error: string;
  onConfirm: (mode: 'graceful' | 'force') => void;
  onClose: () => void;
}) {
  const [mode, setMode] = useState<'graceful' | 'force'>('graceful');
  const { local, peer } = status;
  const isPrimary = local.role === 'primary';
  // 音频流数量：交接由当前主节点执行，看主节点侧的数字。
  const busy = isPrimary ? local.busyStreams : peer?.busyStreams ?? 0;
  const txidLagged =
    !!local.txid && !!peer?.txid && (isPrimary ? peer.txid < local.txid : local.txid < peer.txid);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={onClose}>
      <div
        className="w-full max-w-lg bg-emby-bg-dialog border border-emby-border rounded-lg shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 py-4 border-b border-emby-border">
          <h3 className="text-white font-semibold flex items-center gap-2">
            <ArrowLeftRight className="w-4 h-4 text-emby-green" />
            {isPrimary ? '切换到备用节点' : '将本机升为主节点'}
          </h3>
          <button onClick={onClose} className="text-emby-text-muted hover:text-white transition-colors">
            <X className="w-4 h-4" />
          </button>
        </div>

        <div className="px-5 py-4 space-y-4">
          <p className="text-sm text-emby-text-secondary">
            {isPrimary
              ? '本机将把领导权（Cloudflare 租约 + 主域名 A 记录）交给对端，随后重启为备用节点。'
              : '将向当前主节点发出交还领导权请求，对端完成停写交接后本机重启为主节点。'}
          </p>

          {/* 模式选择 */}
          <div className="space-y-2">
            <ModeOption
              checked={mode === 'graceful'}
              onSelect={() => setMode('graceful')}
              title="平滑交接（推荐）"
              desc="等正在传输的音频流结束 → 停写 → 对端追平数据 → 让位。音频流较长时最多等待 30 分钟。"
            />
            <ModeOption
              checked={mode === 'force'}
              onSelect={() => setMode('force')}
              title="强制切换"
              desc="跳过等待音频流，立即停写交接（停写上限 90 秒）。数据零丢失流程不受影响。"
              warn={
                busy > 0
                  ? `当前有 ${busy} 条音频流进行中，强制切换会将其截断，相关任务需要重跑。`
                  : undefined
              }
            />
          </div>

          {/* 上下文信息 */}
          <div className="rounded-md bg-emby-bg-elevated/40 border border-emby-border px-3 py-2 space-y-1.5 text-xs">
            <div className="flex items-center justify-between">
              <span className="text-emby-text-muted">进行中的音频流</span>
              <span className={busy > 0 ? 'text-yellow-300' : 'text-emby-text-primary'}>{busy} 条</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-emby-text-muted">本机 / 对端复制位点</span>
              <span className="font-mono text-emby-text-primary">
                {(local.txid || '-') + ' / ' + (peer?.txid || '-')}
              </span>
            </div>
            {txidLagged && (
              <p className="text-yellow-300/90">
                接收方复制位点落后，交接会在停写后等它追平（上限 90 秒），追不平则自动中止并恢复写入。
              </p>
            )}
          </div>

          <ul className="text-xs text-emby-text-muted space-y-1 list-disc pl-4">
            <li>切换会重启节点进程，页面短暂不可用属正常现象。</li>
            {(isPrimary ? local.preferred : peer?.role === 'primary' && !local.preferred) && (
              <li>从首选主节点切走会自动关闭"自动回切"，可在状态面板重新开启。</li>
            )}
            <li>切换期间请勿进行备份导出/恢复操作。</li>
            <li>主域名解析随之切换，浏览器 DNS 缓存约 1 分钟内生效。</li>
          </ul>

          {error && (
            <div className="flex items-start gap-2 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2">
              <TriangleAlert className="w-4 h-4 text-red-400 mt-0.5 flex-shrink-0" />
              <p className="text-xs text-red-300">{error}</p>
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 px-5 py-4 border-t border-emby-border">
          <button
            onClick={onClose}
            disabled={pending}
            className="px-4 py-2 text-sm rounded-md bg-emby-bg-elevated border border-emby-border text-emby-text-primary hover:bg-emby-bg-input transition-colors disabled:opacity-50"
          >
            取消
          </button>
          <button
            onClick={() => onConfirm(mode)}
            disabled={pending}
            className={`inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-md text-white transition-colors disabled:opacity-50 ${
              mode === 'force' ? 'bg-red-600 hover:bg-red-700' : 'bg-emby-green hover:bg-emby-green-dark'
            }`}
          >
            {pending && <Loader2 className="w-4 h-4 animate-spin" />}
            {mode === 'force' ? '确认强制切换' : '确认平滑交接'}
          </button>
        </div>
      </div>
    </div>
  );
}

function ModeOption({
  checked,
  onSelect,
  title,
  desc,
  warn,
}: {
  checked: boolean;
  onSelect: () => void;
  title: string;
  desc: string;
  warn?: string;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`w-full text-left rounded-md border px-3 py-2.5 transition-colors ${
        checked
          ? 'border-emby-green/60 bg-emby-green/10'
          : 'border-emby-border bg-emby-bg-card hover:border-emby-border-light'
      }`}
    >
      <div className="flex items-center gap-2">
        <span
          className={`w-3.5 h-3.5 rounded-full border flex-shrink-0 ${
            checked ? 'border-emby-green bg-emby-green' : 'border-emby-text-muted'
          }`}
        />
        <span className="text-sm text-white font-medium">{title}</span>
      </div>
      <p className="text-xs text-emby-text-secondary mt-1 ml-5 pl-0.5">{desc}</p>
      {warn && (
        <p className="text-xs text-yellow-300 mt-1 pl-0.5 flex items-start gap-1">
          <TriangleAlert className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" /> {warn}
        </p>
      )}
    </button>
  );
}

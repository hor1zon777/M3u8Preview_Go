import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Cpu,
  Plus,
  Trash2,
  Copy,
  Check,
  ChevronDown,
  ChevronRight,
  Server,
  Loader2,
  Clock,
  CircleDot,
  Activity,
  Pencil,
  Gauge,
  Mic,
  Download,
  HardDrive,
  AlertTriangle,
  Info,
} from 'lucide-react';
import { subtitleApi } from '../../services/subtitleApi.js';
import type {
  SubtitleWorker,
  SubtitleWorkerToken,
  WorkerCapability,
} from '@m3u8-preview/shared';

/**
 * 字幕远程 GPU Worker 面板。
 *
 * 三段式：
 *   - 顶部 admin 告警条（无 worker 在线 / 中转池满）
 *   - 中部：在线 worker 列表（5 秒轮询，显示 capability badge）
 *   - 底部：Worker Token 管理（默认折叠）
 *   - 嵌入：中转池监控小卡（5s 轮询）
 *
 * 上半区：在线 worker 列表（5 秒轮询）
 *   - last_seen 在 staleThreshold（默认 10min）内视为在线
 *   - 显示 GPU / 当前任务 / 累计完成/失败数 / capability badge
 *
 * 下半区：Worker Token 管理（默认折叠）
 *   - 列出 admin 生成的所有 token（不含明文）
 *   - 创建：弹窗输入名称 → 生成 → 一次性显示明文 + 复制按钮
 *   - 吊销：soft revoke，不删除审计记录
 */
export function SubtitleWorkersPanel() {
  const [tokenAreaOpen, setTokenAreaOpen] = useState(false);

  return (
    <div className="mb-6 space-y-3">
      <AlertsBar />
      <WorkersOnlineCard />
      <IntermediatePoolCard />
      <TokensCard open={tokenAreaOpen} onToggle={() => setTokenAreaOpen((v) => !v)} />
    </div>
  );
}

// ---- 顶部告警条（M8.4） ----

function AlertsBar() {
  const { data: alerts = [] } = useQuery({
    queryKey: ['admin', 'subtitle', 'alerts'],
    queryFn: () => subtitleApi.alerts(),
    refetchInterval: 30_000,
  });
  if (alerts.length === 0) return null;
  return (
    <div className="space-y-2">
      {alerts.map((a, idx) => (
        <div
          key={idx}
          className={`flex items-start gap-2 px-3 py-2 rounded-md border text-xs ${
            a.level === 'error'
              ? 'bg-red-900/30 border-red-700/40 text-red-200'
              : a.level === 'warn'
              ? 'bg-yellow-900/25 border-yellow-700/40 text-yellow-200'
              : 'bg-blue-900/25 border-blue-700/40 text-blue-200'
          }`}
        >
          {a.level === 'info' ? (
            <Info className="w-4 h-4 mt-0.5 shrink-0" />
          ) : (
            <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
          )}
          <span className="leading-relaxed">{a.message}</span>
        </div>
      ))}
    </div>
  );
}

// ---- 中转池监控小卡（M8.3，v3 broker 模式适配） ----

/**
 * v3 broker 模式：服务端不再持有 FLAC 文件，"中转池"语义改为
 * 当前等待 subtitle worker 拉取的任务集合（DB 中 stage=audio_uploaded）。
 *
 * 显示：
 *   - FLAC 待拉数 = stats.fileCount（即 audio_uploaded 任务数）
 *   - 估算总字节 = sum(audio_artifact_size)（保留在 audio worker 本地）
 *   - 最早 audio_ready 时间（看是不是有 FLAC 长时间没人来取）
 */
function IntermediatePoolCard() {
  const { data: stats } = useQuery({
    queryKey: ['admin', 'subtitle', 'intermediate-stats'],
    queryFn: () => subtitleApi.intermediateStats(),
    refetchInterval: 5000,
  });
  if (!stats) return null;
  const totalMB = stats.totalBytes / 1024 / 1024;
  return (
    <div className="bg-emby-bg-card border border-emby-border rounded-lg px-4 py-3">
      <div className="flex items-center justify-between mb-1">
        <div className="flex items-center gap-2">
          <HardDrive className="w-4 h-4 text-emby-text-secondary" />
          <h3 className="text-sm font-medium text-white">FLAC 转交队列</h3>
        </div>
        <span className="text-xs text-emby-text-muted tabular-nums">
          {stats.fileCount} 个待 ASR · 估算 {totalMB.toFixed(1)} MB
        </span>
      </div>
      <div className="text-[11px] text-emby-text-muted">
        v3 broker 模式：FLAC 留在 audio worker 本地，subtitle worker 拉取时由服务端实时桥接（服务端 0 落盘）
      </div>
      {stats.oldestUploadedAt && (
        <div className="mt-1 text-[11px] text-emby-text-muted">
          最早 audio_ready：{new Date(stats.oldestUploadedAt).toLocaleString('zh-CN')}（{formatRelativeTime(stats.oldestUploadedAt)}）
        </div>
      )}
    </div>
  );
}

// ---- 在线 worker 列表 ----

function WorkersOnlineCard() {
  const { data: workers = [], isLoading } = useQuery({
    queryKey: ['admin', 'subtitle', 'workers'],
    queryFn: () => subtitleApi.listWorkers(),
    refetchInterval: 5000,
  });

  const onlineCount = workers.filter((w) => w.online).length;

  return (
    <div className="bg-emby-bg-card border border-emby-border rounded-lg overflow-hidden">
      <div className="px-4 py-3 border-b border-emby-border flex items-center gap-2">
        <Cpu className="w-4 h-4 text-emby-green" />
        <h3 className="text-sm font-medium text-white">远程 GPU Worker</h3>
        <span className="text-xs text-emby-text-secondary">
          在线 <span className="text-emby-green tabular-nums">{onlineCount}</span> / {workers.length}
        </span>
      </div>

      {isLoading ? (
        <div className="px-4 py-6 text-center text-emby-text-secondary text-sm">
          <Loader2 className="w-4 h-4 inline animate-spin mr-2" />
          加载中...
        </div>
      ) : workers.length === 0 ? (
        <div className="px-4 py-6 text-center text-emby-text-secondary text-sm">
          暂无注册 worker。生成 token 并启动 m3u8-preview-worker 桌面端开始使用。
        </div>
      ) : (
        <div className="divide-y divide-emby-border">
          {workers.map((w) => (
            <WorkerRow key={w.id} worker={w} />
          ))}
        </div>
      )}
    </div>
  );
}

function WorkerRow({ worker }: { worker: SubtitleWorker }) {
  return (
    <div className="px-4 py-3 flex items-center gap-4 text-sm">
      <CircleDot
        className={`w-3 h-3 flex-shrink-0 ${worker.online ? 'text-emby-green' : 'text-emby-text-muted'}`}
        aria-label={worker.online ? '在线' : '离线'}
      />
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-white font-medium truncate" title={worker.name}>
            {worker.name}
          </span>
          {worker.version && (
            <span className="text-xs text-emby-text-muted font-normal">v{worker.version}</span>
          )}
          {(worker.capabilities ?? []).map((c) => (
            <CapabilityBadge key={c} cap={c} />
          ))}
        </div>
        <div className="text-xs text-emby-text-muted truncate flex items-center gap-3 mt-0.5">
          {worker.gpu && (
            <span className="flex items-center gap-1">
              <Server className="w-3 h-3" />
              {worker.gpu}
            </span>
          )}
          {worker.currentJobId && (
            <span className="flex items-center gap-1 text-blue-400">
              <Activity className="w-3 h-3" />
              当前任务 <code className="font-mono">{worker.currentJobId.slice(0, 8)}</code>
            </span>
          )}
          <span className="flex items-center gap-1">
            <Clock className="w-3 h-3" />
            {formatRelativeTime(worker.lastSeenAt)}
          </span>
        </div>
      </div>
      <div className="text-right text-xs flex-shrink-0">
        <div className="text-emby-green tabular-nums">完成 {worker.completedJobs}</div>
        <div className="text-red-400 tabular-nums">失败 {worker.failedJobs}</div>
      </div>
    </div>
  );
}

/** 能力徽章：audio_extract = 蓝色（带宽角色）；asr_subtitle = 紫色（GPU 角色）。 */
function CapabilityBadge({ cap }: { cap: WorkerCapability }) {
  if (cap === 'audio_extract') {
    return (
      <span
        className="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded bg-blue-900/40 text-blue-200 border border-blue-700/40"
        title="audio_extract：负责下载 m3u8 + 抽音 + FLAC 编码"
      >
        <Download className="w-3 h-3" /> 下载抽音
      </span>
    );
  }
  if (cap === 'asr_subtitle') {
    return (
      <span
        className="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded bg-purple-900/40 text-purple-200 border border-purple-700/40"
        title="asr_subtitle：负责 ASR + 翻译 + VTT"
      >
        <Mic className="w-3 h-3" /> ASR 字幕
      </span>
    );
  }
  return null;
}

// ---- Token 管理 ----

function TokensCard({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  return (
    <div className="bg-emby-bg-card border border-emby-border rounded-lg overflow-hidden">
      <button
        onClick={onToggle}
        className="w-full px-4 py-3 flex items-center justify-between hover:bg-emby-bg-elevated/50 transition-colors"
      >
        <div className="flex items-center gap-2">
          {open ? (
            <ChevronDown className="w-4 h-4 text-emby-text-secondary" />
          ) : (
            <ChevronRight className="w-4 h-4 text-emby-text-secondary" />
          )}
          <h3 className="text-sm font-medium text-white">Worker Token 管理</h3>
        </div>
        <span className="text-xs text-emby-text-muted">点击展开</span>
      </button>
      {open && <TokensList />}
    </div>
  );
}

function TokensList() {
  const queryClient = useQueryClient();
  const [showCreate, setShowCreate] = useState(false);
  const [editing, setEditing] = useState<SubtitleWorkerToken | null>(null);
  const [newToken, setNewToken] = useState<{ token: string; name: string } | null>(null);

  const { data: tokens = [], isLoading } = useQuery({
    queryKey: ['admin', 'subtitle', 'worker-tokens'],
    queryFn: () => subtitleApi.listWorkerTokens(),
    refetchInterval: 5000,
  });

  const createMutation = useMutation({
    mutationFn: (payload: {
      name: string;
      maxConcurrency: number;
      maxAudioConcurrency: number;
      maxSubtitleConcurrency: number;
    }) =>
      subtitleApi.createWorkerToken({
        name: payload.name,
        maxConcurrency: payload.maxConcurrency,
        maxAudioConcurrency: payload.maxAudioConcurrency,
        maxSubtitleConcurrency: payload.maxSubtitleConcurrency,
      }),
    onSuccess: (resp) => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'subtitle', 'worker-tokens'] });
      setNewToken({ token: resp.token, name: resp.record.name });
      setShowCreate(false);
    },
    onError: (err: Error) => {
      alert(`生成失败：${err.message}`);
    },
  });

  const revokeMutation = useMutation({
    mutationFn: (id: string) => subtitleApi.revokeWorkerToken(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'subtitle', 'worker-tokens'] });
    },
    onError: (err: Error) => {
      alert(`吊销失败：${err.message}`);
    },
  });

  const updateMutation = useMutation({
    mutationFn: (payload: {
      id: string;
      maxConcurrency: number;
      maxAudioConcurrency: number;
      maxSubtitleConcurrency: number;
    }) =>
      subtitleApi.updateWorkerToken(payload.id, {
        maxConcurrency: payload.maxConcurrency,
        maxAudioConcurrency: payload.maxAudioConcurrency,
        maxSubtitleConcurrency: payload.maxSubtitleConcurrency,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'subtitle', 'worker-tokens'] });
      setEditing(null);
    },
    onError: (err: Error) => {
      alert(`更新失败：${err.message}`);
    },
  });

  return (
    <div className="border-t border-emby-border">
      <div className="px-4 py-3 flex items-center justify-between bg-emby-bg-elevated/30">
        <div className="text-xs text-emby-text-secondary">
          共 {tokens.length} 个 token{' '}
          <span className="text-emby-text-muted">（明文仅在创建时显示一次，请妥善保存）</span>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="px-3 py-1.5 text-xs rounded-md bg-emby-green text-white hover:bg-emby-green-dark transition-colors flex items-center gap-1.5"
        >
          <Plus className="w-3.5 h-3.5" /> 生成新 token
        </button>
      </div>

      {isLoading ? (
        <div className="px-4 py-6 text-center text-emby-text-secondary text-sm">
          <Loader2 className="w-4 h-4 inline animate-spin mr-2" />
          加载中...
        </div>
      ) : tokens.length === 0 ? (
        <div className="px-4 py-6 text-center text-emby-text-secondary text-sm">
          暂无 token，点击右上角生成。
        </div>
      ) : (
        <table className="w-full text-sm">
          <thead className="bg-emby-bg-elevated text-emby-text-secondary text-xs uppercase">
            <tr>
              <th className="text-left px-4 py-2.5">名称</th>
              <th className="text-left px-4 py-2.5">前缀</th>
              <th className="text-left px-4 py-2.5">
                <span className="inline-flex items-center gap-1">
                  <Gauge className="w-3 h-3" /> 并发
                </span>
              </th>
              <th className="text-left px-4 py-2.5">创建时间</th>
              <th className="text-left px-4 py-2.5">最近使用</th>
              <th className="text-left px-4 py-2.5">状态</th>
              <th className="text-right px-4 py-2.5">操作</th>
            </tr>
          </thead>
          <tbody>
            {tokens.map((t) => (
              <TokenRow
                key={t.id}
                token={t}
                onEdit={() => setEditing(t)}
                onRevoke={() => {
                  if (confirm(`确认吊销 token "${t.name}"？此操作不可撤销。`)) {
                    revokeMutation.mutate(t.id);
                  }
                }}
              />
            ))}
          </tbody>
        </table>
      )}

      {showCreate && (
        <CreateTokenModal
          onClose={() => setShowCreate(false)}
          onSubmit={(name, maxConcurrency, maxAudioConcurrency, maxSubtitleConcurrency) =>
            createMutation.mutate({ name, maxConcurrency, maxAudioConcurrency, maxSubtitleConcurrency })
          }
          submitting={createMutation.isPending}
        />
      )}

      {editing && (
        <EditTokenModal
          token={editing}
          submitting={updateMutation.isPending}
          onClose={() => setEditing(null)}
          onSubmit={(maxConcurrency, maxAudioConcurrency, maxSubtitleConcurrency) =>
            updateMutation.mutate({
              id: editing.id,
              maxConcurrency,
              maxAudioConcurrency,
              maxSubtitleConcurrency,
            })
          }
        />
      )}

      {newToken && <NewTokenModal token={newToken.token} name={newToken.name} onClose={() => setNewToken(null)} />}
    </div>
  );
}

function TokenRow({
  token,
  onEdit,
  onRevoke,
}: {
  token: SubtitleWorkerToken;
  onEdit: () => void;
  onRevoke: () => void;
}) {
  const revoked = !!token.revokedAt;
  return (
    <tr className="border-t border-emby-border">
      <td className="px-4 py-2.5 text-white">{token.name}</td>
      <td className="px-4 py-2.5">
        <code className="text-xs font-mono text-emby-text-secondary">{token.tokenPrefix}…</code>
      </td>
      <td className="px-4 py-2.5">
        <div className="space-y-1.5 min-w-[160px]">
          <ConcurrencyBar
            label="audio"
            cur={token.currentAudioRunning}
            max={token.maxAudioConcurrency}
            color="blue"
          />
          <ConcurrencyBar
            label="subtitle"
            cur={token.currentSubtitleRunning}
            max={token.maxSubtitleConcurrency}
            color="purple"
          />
          {token.maxConcurrency > 0 && (
            <div className="text-[10px] text-emby-text-muted">
              总上限 {token.currentRunning} / {token.maxConcurrency}
            </div>
          )}
        </div>
      </td>
      <td className="px-4 py-2.5 text-xs text-emby-text-secondary">
        {new Date(token.createdAt).toLocaleString('zh-CN')}
      </td>
      <td className="px-4 py-2.5 text-xs text-emby-text-secondary">
        {token.lastUsedAt ? new Date(token.lastUsedAt).toLocaleString('zh-CN') : '—'}
      </td>
      <td className="px-4 py-2.5">
        {revoked ? (
          <span className="px-2 py-0.5 text-xs rounded bg-zinc-800 text-emby-text-muted border border-emby-border">
            已吊销
          </span>
        ) : (
          <span className="px-2 py-0.5 text-xs rounded bg-emerald-900/40 text-emerald-300 border border-emerald-700/40">
            活跃
          </span>
        )}
      </td>
      <td className="px-4 py-2.5 text-right">
        {!revoked && (
          <div className="inline-flex items-center gap-1">
            <button
              onClick={onEdit}
              title="编辑并发上限"
              className="p-1.5 rounded hover:bg-emby-bg-elevated text-emby-text-secondary transition-colors"
            >
              <Pencil className="w-4 h-4" />
            </button>
            <button
              onClick={onRevoke}
              title="吊销"
              className="p-1.5 rounded hover:bg-red-900/40 text-red-400 transition-colors"
            >
              <Trash2 className="w-4 h-4" />
            </button>
          </div>
        )}
      </td>
    </tr>
  );
}

function ConcurrencyBar({
  label,
  cur,
  max,
  color,
}: {
  label: string;
  cur: number;
  max: number;
  color: 'blue' | 'purple';
}) {
  const ratio = max > 0 ? cur / max : 0;
  const fillColor =
    max <= 0
      ? 'bg-emby-text-muted'
      : ratio >= 1
      ? 'bg-red-500'
      : ratio >= 0.7
      ? 'bg-yellow-500'
      : color === 'blue'
      ? 'bg-blue-500'
      : 'bg-purple-500';
  return (
    <div className="flex items-center gap-2">
      <span className="text-[10px] text-emby-text-muted w-12 shrink-0">{label}</span>
      <div className="flex-1 h-1.5 rounded-full bg-emby-bg-elevated overflow-hidden">
        <div
          className={`h-full ${fillColor} transition-all`}
          style={{ width: `${max > 0 ? Math.min(100, ratio * 100) : 100}%` }}
        />
      </div>
      <span className="text-[10px] tabular-nums text-emby-text-secondary whitespace-nowrap">
        {cur} / {max <= 0 ? '∞' : max}
      </span>
    </div>
  );
}

function CreateTokenModal({
  onClose,
  onSubmit,
  submitting,
}: {
  onClose: () => void;
  onSubmit: (
    name: string,
    maxConcurrency: number,
    maxAudioConcurrency: number,
    maxSubtitleConcurrency: number,
  ) => void;
  submitting: boolean;
}) {
  const [name, setName] = useState('');
  const [maxConcurrency, setMaxConcurrency] = useState(0);
  const [maxAudioConcurrency, setMaxAudioConcurrency] = useState(2);
  const [maxSubtitleConcurrency, setMaxSubtitleConcurrency] = useState(1);
  return (
    <Modal title="生成 Worker Token" onClose={onClose}>
      <div className="space-y-3">
        <div>
          <label className="block text-xs text-emby-text-secondary mb-1">
            名称（用来识别这台 worker，例如「家里 GPU 机」）
          </label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={64}
            placeholder="家里 GPU 机"
            autoFocus
            className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green text-sm"
          />
        </div>
        <div className="grid grid-cols-3 gap-2">
          <NumberField
            label="audio 并发"
            hint="audio_extract（下载抽音）维度上限。机 A 带宽足时可调到 2~3。0 = 不限。"
            min={0}
            max={64}
            value={maxAudioConcurrency}
            onChange={setMaxAudioConcurrency}
          />
          <NumberField
            label="subtitle 并发"
            hint="asr_subtitle（GPU ASR）维度上限。单卡通常保持 1。0 = 不限。"
            min={0}
            max={64}
            value={maxSubtitleConcurrency}
            onChange={setMaxSubtitleConcurrency}
          />
          <NumberField
            label="总上限（兜底）"
            hint="不区分能力的总并发上限。0 = 不限，仅由两条维度上限管控。"
            min={0}
            max={64}
            value={maxConcurrency}
            onChange={setMaxConcurrency}
          />
        </div>
        <div className="flex justify-end gap-2 pt-2">
          <button
            onClick={onClose}
            className="px-3 py-1.5 rounded bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated text-sm"
          >
            取消
          </button>
          <button
            disabled={!name.trim() || submitting}
            onClick={() =>
              onSubmit(name.trim(), maxConcurrency, maxAudioConcurrency, maxSubtitleConcurrency)
            }
            className="px-3 py-1.5 rounded bg-emby-green text-white hover:bg-emby-green-dark text-sm disabled:opacity-40 disabled:cursor-not-allowed flex items-center gap-1.5"
          >
            {submitting ? <Loader2 className="w-4 h-4 animate-spin" /> : <Plus className="w-4 h-4" />}
            生成
          </button>
        </div>
      </div>
    </Modal>
  );
}

function EditTokenModal({
  token,
  submitting,
  onSubmit,
  onClose,
}: {
  token: SubtitleWorkerToken;
  submitting: boolean;
  onSubmit: (
    maxConcurrency: number,
    maxAudioConcurrency: number,
    maxSubtitleConcurrency: number,
  ) => void;
  onClose: () => void;
}) {
  const [maxConcurrency, setMaxConcurrency] = useState(token.maxConcurrency);
  const [maxAudioConcurrency, setMaxAudioConcurrency] = useState(token.maxAudioConcurrency);
  const [maxSubtitleConcurrency, setMaxSubtitleConcurrency] = useState(token.maxSubtitleConcurrency);
  const dirty =
    maxConcurrency !== token.maxConcurrency ||
    maxAudioConcurrency !== token.maxAudioConcurrency ||
    maxSubtitleConcurrency !== token.maxSubtitleConcurrency;
  return (
    <Modal title={`编辑 Token：${token.name}`} onClose={onClose}>
      <div className="space-y-3">
        <div className="grid grid-cols-3 gap-2">
          <NumberField
            label="audio 并发"
            hint={`当前 ${token.currentAudioRunning} 条 audio 阶段任务。0 = 不限。`}
            min={0}
            max={64}
            value={maxAudioConcurrency}
            onChange={setMaxAudioConcurrency}
          />
          <NumberField
            label="subtitle 并发"
            hint={`当前 ${token.currentSubtitleRunning} 条 subtitle 阶段任务。0 = 不限。`}
            min={0}
            max={64}
            value={maxSubtitleConcurrency}
            onChange={setMaxSubtitleConcurrency}
          />
          <NumberField
            label="总上限（兜底）"
            hint={`当前 RUNNING ${token.currentRunning} 条。0 = 不限。`}
            min={0}
            max={64}
            value={maxConcurrency}
            onChange={setMaxConcurrency}
          />
        </div>
        <p className="text-[11px] text-emby-text-muted">
          降低上限不会中断已经在跑的任务，仅影响后续 claim。
        </p>
        <div className="flex justify-end gap-2 pt-2">
          <button
            onClick={onClose}
            className="px-3 py-1.5 rounded bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated text-sm"
          >
            取消
          </button>
          <button
            disabled={submitting || !dirty}
            onClick={() => onSubmit(maxConcurrency, maxAudioConcurrency, maxSubtitleConcurrency)}
            className="px-3 py-1.5 rounded bg-emby-green text-white hover:bg-emby-green-dark text-sm disabled:opacity-40 disabled:cursor-not-allowed flex items-center gap-1.5"
          >
            {submitting ? <Loader2 className="w-4 h-4 animate-spin" /> : <Check className="w-4 h-4" />}
            保存
          </button>
        </div>
      </div>
    </Modal>
  );
}

function NumberField({
  label,
  hint,
  min,
  max,
  value,
  onChange,
}: {
  label: string;
  hint?: string;
  min: number;
  max: number;
  value: number;
  onChange: (v: number) => void;
}) {
  return (
    <div>
      <label className="block text-xs text-emby-text-secondary mb-1">{label}</label>
      <input
        type="number"
        min={min}
        max={max}
        value={value}
        onChange={(e) => onChange(Math.max(min, Math.min(max, Number(e.target.value) || 0)))}
        className="w-full px-3 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white text-sm focus:outline-none focus:ring-2 focus:ring-emby-green"
      />
      {hint && <p className="mt-1 text-[10px] text-emby-text-muted leading-snug">{hint}</p>}
    </div>
  );
}

function NewTokenModal({ token, name, onClose }: { token: string; name: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(token);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      alert('复制失败，请手动选择文本复制');
    }
  };
  return (
    <Modal title={`Token 已生成：${name}`} onClose={onClose}>
      <div className="space-y-3">
        <div className="px-3 py-2 rounded bg-yellow-900/30 border border-yellow-700/50 text-xs text-yellow-300">
          ⚠️ 此 token 仅显示一次。关闭后无法再次查看，请立即复制并保存到 worker 配置中。
        </div>
        <div>
          <label className="block text-xs text-emby-text-secondary mb-1">Token（明文）</label>
          <div className="flex gap-2">
            <input
              type="text"
              readOnly
              value={token}
              onFocus={(e) => e.currentTarget.select()}
              className="flex-1 px-3 py-2 bg-emby-bg-input border border-emby-border rounded-lg text-white font-mono text-xs focus:outline-none focus:ring-2 focus:ring-emby-green"
            />
            <button
              onClick={handleCopy}
              className="px-3 py-2 rounded bg-emby-green text-white hover:bg-emby-green-dark text-sm flex items-center gap-1.5"
            >
              {copied ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
              {copied ? '已复制' : '复制'}
            </button>
          </div>
        </div>
        <div className="flex justify-end pt-2">
          <button
            onClick={onClose}
            className="px-3 py-1.5 rounded bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated text-sm"
          >
            我已保存
          </button>
        </div>
      </div>
    </Modal>
  );
}

function Modal({ children, onClose, title }: { children: React.ReactNode; onClose: () => void; title: string }) {
  return (
    <div
      className="fixed inset-0 z-50 bg-black/60 flex items-center justify-center px-4"
      onClick={onClose}
    >
      <div
        className="bg-emby-bg-dialog border border-emby-border rounded-lg shadow-xl max-w-lg w-full"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="px-5 py-3 border-b border-emby-border">
          <h3 className="text-white font-medium">{title}</h3>
        </div>
        <div className="px-5 py-4">{children}</div>
      </div>
    </div>
  );
}

// 把 ISO 时间格式化成相对时间（"3 秒前" / "2 分钟前" / "1 小时前"）
function formatRelativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return '';
  const diffMs = Date.now() - t;
  const sec = Math.floor(diffMs / 1000);
  if (sec < 5) return '刚刚';
  if (sec < 60) return `${sec} 秒前`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  const day = Math.floor(hr / 24);
  return `${day} 天前`;
}

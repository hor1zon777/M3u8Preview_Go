import { useMemo, useState } from 'react';
import { Check, ChevronLeft, ChevronRight, ClipboardCopy, Info, ShieldCheck } from 'lucide-react';
import type { HaMode } from '@m3u8-preview/shared';
import {
  buildChecklist,
  buildEnvSnippet,
  deriveRecordNames,
  type HaWizardForm,
} from './envTemplates.js';

/**
 * 首次部署配置向导：询问本机角色 → 收集域名/Cloudflare/节点信息 →
 * 生成两台节点的 .env 片段与操作清单，由用户复制到服务器手工应用并重启。
 *
 * 纯前端：所有输入（含 CF Token）只在浏览器内存中拼模板，绝不发送到服务器。
 */
export function HaSetupWizard({ mode, initialRole }: { mode: HaMode; initialRole?: string }) {
  const [step, setStep] = useState(0);
  const [form, setForm] = useState<HaWizardForm>({
    localRole: initialRole === 'replica' ? 'replica' : 'primary',
    domain: '',
    leaseRecord: '',
    handoffRecord: '',
    cfApiToken: '',
    cfZoneId: '',
    primaryId: 'node-a',
    primaryIp: '',
    primaryHost: 'node-a.internal',
    replicaId: 'node-b',
    replicaIp: '',
    replicaHost: 'node-b.internal',
    channelPort: '20203',
  });

  const set = (patch: Partial<HaWizardForm>) => setForm((f) => ({ ...f, ...patch }));

  const steps = ['本机角色', '域名与 Cloudflare', '节点信息', '生成配置'];
  const canNext =
    step === 0
      ? true
      : step === 1
        ? !!form.domain && !!form.leaseRecord && !!form.handoffRecord && !!form.cfZoneId
        : step === 2
          ? !!form.primaryId && !!form.replicaId && form.primaryId !== form.replicaId &&
            !!form.primaryIp && !!form.replicaIp && !!form.primaryHost && !!form.replicaHost
          : false;

  return (
    <div className="space-y-4">
      {mode === 'role-aware' && (
        <div className="flex items-start gap-2 rounded-md border border-blue-500/40 bg-blue-500/10 px-4 py-3">
          <Info className="w-4 h-4 text-blue-400 mt-0.5 flex-shrink-0" />
          <p className="text-sm text-blue-200">
            检测到 LiteFS 已启用但 Cloudflare 租约仲裁未配置（仅角色感知档位）。
            补齐下面第 2 步的 Cloudflare 相关变量即可升级到完整高可用。
          </p>
        </div>
      )}

      {/* 步骤指示 */}
      <div className="flex items-center gap-1 text-xs">
        {steps.map((s, i) => (
          <div key={s} className="flex items-center gap-1">
            {i > 0 && <span className="w-6 h-px bg-emby-border mx-1" />}
            <span
              className={`w-5 h-5 rounded-full flex items-center justify-center border text-[10px] ${
                i < step
                  ? 'bg-emby-green border-emby-green text-white'
                  : i === step
                    ? 'border-emby-green text-emby-green'
                    : 'border-emby-border text-emby-text-muted'
              }`}
            >
              {i < step ? <Check className="w-3 h-3" /> : i + 1}
            </span>
            <span className={i === step ? 'text-white' : 'text-emby-text-muted'}>{s}</span>
          </div>
        ))}
      </div>

      <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5">
        {step === 0 && <StepRole form={form} set={set} />}
        {step === 1 && <StepCloudflare form={form} set={set} />}
        {step === 2 && <StepNodes form={form} set={set} />}
        {step === 3 && <StepResult form={form} />}
      </div>

      {/* 导航 */}
      <div className="flex items-center justify-between">
        <button
          onClick={() => setStep((s) => Math.max(0, s - 1))}
          disabled={step === 0}
          className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded-md bg-emby-bg-card border border-emby-border text-emby-text-primary hover:bg-emby-bg-elevated transition-colors disabled:opacity-40"
        >
          <ChevronLeft className="w-4 h-4" /> 上一步
        </button>
        {step < steps.length - 1 && (
          <button
            onClick={() => setStep((s) => s + 1)}
            disabled={!canNext}
            className="inline-flex items-center gap-1 px-4 py-1.5 text-sm font-medium rounded-md bg-emby-green text-white hover:bg-emby-green-dark transition-colors disabled:opacity-40"
          >
            下一步 <ChevronRight className="w-4 h-4" />
          </button>
        )}
      </div>
    </div>
  );
}

function StepRole({ form, set }: { form: HaWizardForm; set: (p: Partial<HaWizardForm>) => void }) {
  return (
    <div className="space-y-3">
      <p className="text-sm text-emby-text-secondary">
        本机（当前访问的这台服务器）将作为哪种节点？两台里只有一台是"主节点（首选）"——
        它平时承载全部流量，故障恢复后会自动收回领导权。
      </p>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <RoleCard
          checked={form.localRole === 'primary'}
          onSelect={() => set({ localRole: 'primary' })}
          title="主节点（首选）"
          desc="平时对外服务的这台。数据从这里复制到备用节点。"
        />
        <RoleCard
          checked={form.localRole === 'replica'}
          onSelect={() => set({ localRole: 'replica' })}
          title="备用节点"
          desc="热备：实时接收复制数据，主节点故障时自动接管。"
        />
      </div>
      <p className="text-xs text-emby-text-muted">
        向导会一次性生成两台节点各自的配置——在哪台机器上操作都可以，选好本机身份即可。
      </p>
    </div>
  );
}

function RoleCard({
  checked,
  onSelect,
  title,
  desc,
}: {
  checked: boolean;
  onSelect: () => void;
  title: string;
  desc: string;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`text-left rounded-md border p-4 transition-colors ${
        checked
          ? 'border-emby-green/60 bg-emby-green/10'
          : 'border-emby-border bg-emby-bg-elevated/40 hover:border-emby-border-light'
      }`}
    >
      <p className="text-white text-sm font-medium">{title}</p>
      <p className="text-xs text-emby-text-secondary mt-1">{desc}</p>
    </button>
  );
}

function StepCloudflare({ form, set }: { form: HaWizardForm; set: (p: Partial<HaWizardForm>) => void }) {
  return (
    <div className="space-y-4">
      <div className="flex items-start gap-2 rounded-md border border-emby-border bg-emby-bg-elevated/40 px-3 py-2">
        <ShieldCheck className="w-4 h-4 text-emby-green mt-0.5 flex-shrink-0" />
        <p className="text-xs text-emby-text-secondary">
          这里填写的所有内容（包括 API Token）只用于在你的浏览器里生成配置文本，
          <span className="text-white">不会发送到服务器</span>，也不会被保存。
        </p>
      </div>
      <Field
        label="用户流量域名（A 记录全名）"
        hint="当前主节点会把它指向自己；需已托管在 Cloudflare 且开启橙云代理"
        value={form.domain}
        placeholder="media.example.com"
        onChange={(v) => {
          const derived = deriveRecordNames(v);
          set({
            domain: v,
            // 用户没手动改过时跟随域名联动
            leaseRecord: form.leaseRecord === '' || form.leaseRecord.startsWith('_ha-lease.') ? derived.lease : form.leaseRecord,
            handoffRecord: form.handoffRecord === '' || form.handoffRecord.startsWith('_ha-handoff.') ? derived.handoff : form.handoffRecord,
          });
        }}
      />
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <Field
          label="租约 TXT 记录全名"
          hint="需先在 Cloudflare 手工创建"
          value={form.leaseRecord}
          placeholder="_ha-lease.example.com"
          onChange={(v) => set({ leaseRecord: v })}
        />
        <Field
          label="交还请求 TXT 记录全名"
          hint="需先在 Cloudflare 手工创建"
          value={form.handoffRecord}
          placeholder="_ha-handoff.example.com"
          onChange={(v) => set({ handoffRecord: v })}
        />
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <Field
          label="Cloudflare API Token（可留空稍后自行填入）"
          hint="权限收窄到 Zone → DNS → Edit，且只授权这一个 zone"
          value={form.cfApiToken}
          placeholder="留空则生成的片段中该项为空"
          onChange={(v) => set({ cfApiToken: v })}
          password
        />
        <Field
          label="Zone ID"
          hint="Cloudflare 域名概览页右下角"
          value={form.cfZoneId}
          placeholder="023e105f4ecef8ad9ca31a8372d0c353"
          onChange={(v) => set({ cfZoneId: v })}
          mono
        />
      </div>
    </div>
  );
}

function StepNodes({ form, set }: { form: HaWizardForm; set: (p: Partial<HaWizardForm>) => void }) {
  return (
    <div className="space-y-5">
      <p className="text-sm text-emby-text-secondary">
        两台节点的身份信息。内部主机名用于节点间 TLS 通道的证书校验（不需要公开 DNS 记录，
        由 docker compose 的 extra_hosts 映射到对端 IP）。
      </p>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
        <fieldset className="space-y-3 rounded-md border border-emby-border p-4">
          <legend className="px-1 text-sm text-white font-medium">
            主节点（首选）{form.localRole === 'primary' && <span className="text-emby-green text-xs ml-1">← 本机</span>}
          </legend>
          <Field label="节点 ID" value={form.primaryId} onChange={(v) => set({ primaryId: v })} mono />
          <Field label="公网 IP" value={form.primaryIp} placeholder="203.0.113.10" onChange={(v) => set({ primaryIp: v })} mono />
          <Field label="内部主机名" value={form.primaryHost} onChange={(v) => set({ primaryHost: v })} mono />
        </fieldset>
        <fieldset className="space-y-3 rounded-md border border-emby-border p-4">
          <legend className="px-1 text-sm text-white font-medium">
            备用节点{form.localRole === 'replica' && <span className="text-emby-green text-xs ml-1">← 本机</span>}
          </legend>
          <Field label="节点 ID" value={form.replicaId} onChange={(v) => set({ replicaId: v })} mono />
          <Field label="公网 IP" value={form.replicaIp} placeholder="203.0.113.20" onChange={(v) => set({ replicaIp: v })} mono />
          <Field label="内部主机名" value={form.replicaHost} onChange={(v) => set({ replicaHost: v })} mono />
        </fieldset>
      </div>
      <div className="max-w-xs">
        <Field
          label="LiteFS 通道端口"
          hint="nginx 为节点间复制/探测做 TLS 终结的端口"
          value={form.channelPort}
          onChange={(v) => set({ channelPort: v })}
          mono
        />
      </div>
    </div>
  );
}

function StepResult({ form }: { form: HaWizardForm }) {
  const [tab, setTab] = useState<'primary' | 'replica'>(form.localRole);
  const envPrimary = useMemo(() => buildEnvSnippet(form, true), [form]);
  const envReplica = useMemo(() => buildEnvSnippet(form, false), [form]);
  const checklist = useMemo(() => buildChecklist(form), [form]);

  return (
    <div className="space-y-5">
      {/* env 片段 */}
      <div>
        <div className="flex items-center gap-2 mb-2">
          <TabButton active={tab === 'primary'} onClick={() => setTab('primary')}>
            {form.primaryId}（主）{form.localRole === 'primary' && ' · 本机'}
          </TabButton>
          <TabButton active={tab === 'replica'} onClick={() => setTab('replica')}>
            {form.replicaId}（备）{form.localRole === 'replica' && ' · 本机'}
          </TabButton>
        </div>
        <CodeBlock content={tab === 'primary' ? envPrimary : envReplica} />
      </div>

      {/* 操作清单 */}
      <div>
        <h4 className="text-white text-sm font-semibold mb-2">部署操作清单（按顺序执行）</h4>
        <ol className="space-y-3">
          {checklist.map((item, i) => (
            <li key={i} className="rounded-md border border-emby-border bg-emby-bg-elevated/30 p-3">
              <p className="text-sm text-white">
                <span className="text-emby-green font-mono mr-1.5">{i + 1}.</span>
                {item.title}
              </p>
              <p className="text-xs text-emby-text-secondary mt-1">{item.detail}</p>
              {item.command && <CodeBlock content={item.command} compact />}
            </li>
          ))}
        </ol>
      </div>
      <p className="text-xs text-emby-text-muted">
        两台节点都启动后回到本页面，即可看到状态面板与手动切换入口。
      </p>
    </div>
  );
}

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`px-3 py-1.5 text-xs rounded-md border transition-colors ${
        active
          ? 'bg-emby-green/15 border-emby-green/50 text-emby-green'
          : 'bg-emby-bg-card border-emby-border text-emby-text-secondary hover:text-white'
      }`}
    >
      {children}
    </button>
  );
}

function CodeBlock({ content, compact }: { content: string; compact?: boolean }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* http 环境无 clipboard 权限时忽略，用户可手动选择复制 */
    }
  };
  return (
    <div className={`relative group ${compact ? 'mt-2' : ''}`}>
      <pre className="bg-emby-bg-input border border-emby-border rounded-md p-3 pr-10 text-[11px] leading-relaxed text-emby-text-primary font-mono overflow-x-auto whitespace-pre">
        {content}
      </pre>
      <button
        onClick={copy}
        title="复制"
        className="absolute top-2 right-2 p-1.5 rounded bg-emby-bg-elevated border border-emby-border text-emby-text-muted hover:text-white transition-colors"
      >
        {copied ? <Check className="w-3.5 h-3.5 text-emby-green" /> : <ClipboardCopy className="w-3.5 h-3.5" />}
      </button>
    </div>
  );
}

function Field({
  label,
  hint,
  value,
  placeholder,
  onChange,
  mono,
  password,
}: {
  label: string;
  hint?: string;
  value: string;
  placeholder?: string;
  onChange: (v: string) => void;
  mono?: boolean;
  password?: boolean;
}) {
  return (
    <div>
      <label className="block text-sm text-white">{label}</label>
      {hint && <p className="text-xs text-emby-text-muted mt-0.5">{hint}</p>}
      <input
        type={password ? 'password' : 'text'}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        autoComplete="off"
        className={`mt-1.5 w-full px-3 py-1.5 bg-emby-bg-input border border-emby-border rounded-md text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green focus:border-transparent ${mono ? 'font-mono' : ''}`}
      />
    </div>
  );
}

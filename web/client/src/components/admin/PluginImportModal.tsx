import { useRef, useState } from 'react';
import { FileJson, Loader2, TriangleAlert, Upload, X } from 'lucide-react';
import type { ExternalPluginManifest, PluginInfo } from '@m3u8-preview/shared';
import { pluginApi } from '../../services/pluginApi.js';

/**
 * 插件导入弹窗：选择 manifest.json → 本地解析预览 → 确认导入。
 * manifest 是纯声明式元数据（服务端不执行任何导入内容），
 * 前端用 File.text() 直接解析做预览，无需服务端 preview 端点。
 */
export function PluginImportModal({
  onImported,
  onClose,
}: {
  onImported: (plugin: PluginInfo) => void;
  onClose: () => void;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [manifest, setManifest] = useState<ExternalPluginManifest | null>(null);
  const [parseError, setParseError] = useState('');
  const [submitError, setSubmitError] = useState('');
  const [askOverwrite, setAskOverwrite] = useState(false);
  const [pending, setPending] = useState(false);

  async function handleFile(f: File) {
    setFile(f);
    setManifest(null);
    setParseError('');
    setSubmitError('');
    setAskOverwrite(false);
    if (f.size > 64 * 1024) {
      setParseError('manifest 超过 64KB 上限');
      return;
    }
    try {
      const parsed = JSON.parse(await f.text()) as ExternalPluginManifest;
      if (!parsed || typeof parsed !== 'object' || !parsed.id || !parsed.name) {
        setParseError('不是有效的插件 manifest（缺少 id / name 字段）');
        return;
      }
      setManifest(parsed);
    } catch {
      setParseError('文件不是合法的 JSON');
    }
  }

  async function submit(overwrite: boolean) {
    if (!file || pending) return;
    setPending(true);
    setSubmitError('');
    try {
      const plugin = await pluginApi.importManifest(file, overwrite);
      onImported(plugin);
    } catch (err: unknown) {
      const resp = (err as { response?: { data?: { error?: string; code?: string } } })?.response?.data;
      if (resp?.code === 'PLUGIN_EXISTS') {
        // 同 id 外部插件已存在：转为"是否覆盖升级"的二次确认
        setAskOverwrite(true);
        setSubmitError('');
      } else {
        setSubmitError(resp?.error ?? (err as { message?: string })?.message ?? '导入失败');
      }
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={onClose}>
      <div
        className="w-full max-w-md bg-emby-bg-dialog border border-emby-border rounded-lg shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 py-4 border-b border-emby-border">
          <h3 className="text-white font-semibold flex items-center gap-2">
            <Upload className="w-4 h-4 text-emby-green" /> 导入插件
          </h3>
          <button onClick={onClose} className="text-emby-text-muted hover:text-white transition-colors">
            <X className="w-4 h-4" />
          </button>
        </div>

        <div className="px-5 py-4 space-y-4">
          <p className="text-xs text-emby-text-secondary">
            选择插件的 <code className="text-emby-text-primary">manifest.json</code>（声明式外部插件包，
            仅包含名称/图标/说明与可选的健康检查地址，<span className="text-white">不含也不会执行任何代码</span>）。
            格式说明见 docs/external-plugin-manifest.md。
          </p>

          {/* 选择文件 */}
          <input
            ref={fileRef}
            type="file"
            accept=".json,application/json"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) void handleFile(f);
            }}
          />
          <button
            onClick={() => fileRef.current?.click()}
            className="w-full flex items-center justify-center gap-2 px-4 py-3 rounded-md border border-dashed border-emby-border text-sm text-emby-text-secondary hover:border-emby-green/50 hover:text-white transition-colors"
          >
            <FileJson className="w-4 h-4" />
            {file ? file.name : '点击选择 manifest.json'}
          </button>

          {parseError && (
            <p className="text-xs text-red-400 flex items-start gap-1.5">
              <TriangleAlert className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" /> {parseError}
            </p>
          )}

          {/* 预览 */}
          {manifest && (
            <div className="rounded-md bg-emby-bg-elevated/40 border border-emby-border px-3 py-2.5 space-y-1 text-xs">
              <PreviewRow label="ID" value={manifest.id} mono />
              <PreviewRow label="名称" value={manifest.name} />
              <PreviewRow label="版本" value={manifest.version || '-'} mono />
              <PreviewRow label="分类" value={manifest.category || '外部服务'} />
              {manifest.description && <PreviewRow label="描述" value={manifest.description} />}
              {manifest.healthUrl && <PreviewRow label="健康检查" value={manifest.healthUrl} mono />}
              {manifest.homepage && <PreviewRow label="主页" value={manifest.homepage} mono />}
            </div>
          )}

          {askOverwrite && (
            <div className="rounded-md border border-yellow-500/40 bg-yellow-500/10 px-3 py-2.5 text-xs text-yellow-200">
              同 ID 的外部插件已存在。确认按此 manifest <span className="font-medium">覆盖升级</span>
              （保留当前启用状态）？
            </div>
          )}

          {submitError && (
            <p className="text-xs text-red-400 flex items-start gap-1.5">
              <TriangleAlert className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" /> {submitError}
            </p>
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
            onClick={() => submit(askOverwrite)}
            disabled={!manifest || pending}
            className="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-md bg-emby-green text-white hover:bg-emby-green-dark transition-colors disabled:opacity-50"
          >
            {pending && <Loader2 className="w-4 h-4 animate-spin" />}
            {askOverwrite ? '覆盖升级' : '导入'}
          </button>
        </div>
      </div>
    </div>
  );
}

function PreviewRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <span className="text-emby-text-muted flex-shrink-0">{label}</span>
      <span className={`text-emby-text-primary text-right break-all ${mono ? 'font-mono' : ''}`}>{value}</span>
    </div>
  );
}

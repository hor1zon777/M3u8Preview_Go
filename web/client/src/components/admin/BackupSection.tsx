import { useState, useRef, useCallback, useEffect } from 'react';
import { Archive, Download, Upload, AlertTriangle, CheckCircle, Database, FolderArchive, PackageCheck, Loader2, Trash2, HardDriveUpload, PenLine } from 'lucide-react';
import { adminApi } from '../../services/adminApi.js';
import type { RestoreResult, ExportProgress, BackupProgress } from '@m3u8-preview/shared';

// ── 平滑进度 hook：确保每个阶段至少展示 MIN_PHASE_MS ──
function useSmoothedProgress<T extends { phase: string; percentage: number }>(minPhaseMs = 500) {
  const [display, setDisplay] = useState<T | null>(null);
  const queueRef = useRef<T[]>([]);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const displayPhaseRef = useRef<string | null>(null);

  const drainNext = useCallback(() => {
    timerRef.current = null;
    if (queueRef.current.length === 0) return;

    const next = queueRef.current.shift()!;
    displayPhaseRef.current = next.phase;
    setDisplay(next);

    if (queueRef.current.length > 0) {
      timerRef.current = setTimeout(drainNext, minPhaseMs);
    }
  }, [minPhaseMs]);

  const push = useCallback((p: T) => {
    if (p.phase === displayPhaseRef.current) {
      setDisplay(p);
      return;
    }

    const idx = queueRef.current.findIndex(q => q.phase === p.phase);
    if (idx >= 0) {
      queueRef.current[idx] = p;
    } else {
      queueRef.current.push(p);
    }

    if (!timerRef.current) {
      if (displayPhaseRef.current === null) {
        drainNext();
      } else {
        timerRef.current = setTimeout(drainNext, minPhaseMs);
      }
    }
  }, [drainNext, minPhaseMs]);

  const reset = useCallback(() => {
    if (timerRef.current) clearTimeout(timerRef.current);
    timerRef.current = null;
    queueRef.current = [];
    displayPhaseRef.current = null;
    setDisplay(null);
  }, []);

  useEffect(() => () => {
    if (timerRef.current) clearTimeout(timerRef.current);
  }, []);

  return { display, push, reset };
}

const PHASE_LABELS: Record<string, string> = {
  db: '查询数据库',
  files: '打包文件',
  finalize: '压缩写入',
  complete: '打包完成',
  error: '操作失败',
  upload: '上传文件',
  parse: '解析校验',
  delete: '清空数据',
  write: '写入数据',
};

const PHASE_ICONS: Record<string, typeof Database> = {
  db: Database,
  files: FolderArchive,
  finalize: PackageCheck,
  complete: CheckCircle,
  upload: HardDriveUpload,
  parse: PenLine,
  delete: Trash2,
  write: Database,
};

export function BackupSection() {
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [showConfirm, setShowConfirm] = useState(false);
  const [restoreResult, setRestoreResult] = useState<RestoreResult | null>(null);
  const [includePosters, setIncludePosters] = useState(true);
  const [isExporting, setIsExporting] = useState(false);
  const [isRestoring, setIsRestoring] = useState(false);
  const [restoreError, setRestoreError] = useState<string | null>(null);
  const abortRef = useRef<(() => void) | null>(null);
  const restoreAbortRef = useRef<(() => void) | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const pendingRestoreResultRef = useRef<RestoreResult | null>(null);

  const exportSmooth = useSmoothedProgress<ExportProgress>(500);
  const restoreSmooth = useSmoothedProgress<BackupProgress>(500);

  // 导出完成时：触发下载已在 SSE 回调中处理，这里处理 UI 收尾
  useEffect(() => {
    if (exportSmooth.display?.phase === 'complete') {
      setIsExporting(false);
      abortRef.current = null;
      const t = setTimeout(() => exportSmooth.reset(), 3000);
      return () => clearTimeout(t);
    }
    if (exportSmooth.display?.phase === 'error') {
      setIsExporting(false);
      abortRef.current = null;
    }
  }, [exportSmooth.display?.phase]);

  // 恢复完成时：处理 UI 收尾
  useEffect(() => {
    if (restoreSmooth.display?.phase === 'complete') {
      setIsRestoring(false);
      restoreAbortRef.current = null;
      if (pendingRestoreResultRef.current) {
        setRestoreResult(pendingRestoreResultRef.current);
        pendingRestoreResultRef.current = null;
      }
      setSelectedFile(null);
      if (fileInputRef.current) fileInputRef.current.value = '';
      const t = setTimeout(() => restoreSmooth.reset(), 3000);
      return () => clearTimeout(t);
    }
    if (restoreSmooth.display?.phase === 'error') {
      setIsRestoring(false);
      restoreAbortRef.current = null;
      setRestoreError(restoreSmooth.display.message);
    }
  }, [restoreSmooth.display?.phase]);

  const startExport = useCallback(() => {
    setIsExporting(true);
    exportSmooth.reset();
    exportSmooth.push({ phase: 'db', message: '准备中...', current: 0, total: 0, percentage: 0 });

    const { abort } = adminApi.exportBackupWithProgress(
      { includePosters },
      (progress) => {
        exportSmooth.push(progress);
      },
    );
    abortRef.current = abort;
  }, [includePosters, exportSmooth]);

  const cancelExport = useCallback(() => {
    abortRef.current?.();
    abortRef.current = null;
    setIsExporting(false);
    exportSmooth.reset();
  }, [exportSmooth]);

  const startRestore = useCallback(() => {
    if (!selectedFile) return;
    setIsRestoring(true);
    setRestoreResult(null);
    setRestoreError(null);
    pendingRestoreResultRef.current = null;
    restoreSmooth.reset();
    restoreSmooth.push({ phase: 'upload', message: '准备上传...', current: 0, total: 0, percentage: 0 });
    setShowConfirm(false);

    const { abort } = adminApi.importBackupWithProgress(
      selectedFile,
      (progress) => {
        if (progress.phase === 'complete' && progress.result) {
          pendingRestoreResultRef.current = progress.result;
        }
        restoreSmooth.push(progress);
      },
    );
    restoreAbortRef.current = abort;
  }, [selectedFile, restoreSmooth]);

  const cancelRestore = useCallback(() => {
    restoreAbortRef.current?.();
    restoreAbortRef.current = null;
    setIsRestoring(false);
    restoreSmooth.reset();
  }, [restoreSmooth]);

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0] || null;
    setSelectedFile(file);
    setRestoreResult(null);
    setRestoreError(null);
    restoreSmooth.reset();
  };

  const formatFileSize = (bytes: number) => {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
  };

  const exportProgress = exportSmooth.display;
  const restoreProgress = restoreSmooth.display;
  const ExportIcon = exportProgress ? (PHASE_ICONS[exportProgress.phase] || Loader2) : null;
  const RestoreIcon = restoreProgress ? (PHASE_ICONS[restoreProgress.phase] || Loader2) : null;

  return (
    <div className="bg-emby-bg-card border border-emby-border-subtle rounded-md p-5">
      <div className="flex items-center gap-2 mb-4">
        <Archive className="w-5 h-5 text-emby-text-secondary" />
        <h3 className="text-white font-semibold">数据备份与恢复</h3>
      </div>

      {/* 导出备份 */}
      <div>
        <p className="text-white text-sm">导出备份</p>
        <p className="text-emby-text-muted text-xs mt-0.5">
          将所有数据和上传文件打包为 ZIP 备份文件
        </p>
        <label className="mt-3 flex items-center gap-2 cursor-pointer select-none">
          <input
            type="checkbox"
            checked={includePosters}
            onChange={(e) => setIncludePosters(e.target.checked)}
            disabled={isExporting}
            className="w-4 h-4 rounded border-emby-border-subtle bg-emby-bg-input text-emby-green focus:ring-emby-green focus:ring-offset-0"
          />
          <span className="text-emby-text-secondary text-sm">包含封面图片</span>
          <span className="text-emby-text-muted text-xs">（不勾选可显著减小备份体积）</span>
        </label>

        <div className="mt-3 flex items-center gap-2">
          <button
            onClick={startExport}
            disabled={isExporting}
            className="inline-flex items-center gap-2 px-4 py-2 bg-emby-green hover:bg-emby-green-light text-white text-sm font-medium rounded transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <Download className="w-4 h-4" />
            {isExporting ? '正在打包...' : '下载备份文件'}
          </button>
          {isExporting && (
            <button
              onClick={cancelExport}
              className="px-3 py-2 text-emby-text-muted hover:text-white text-sm transition-colors"
            >
              取消
            </button>
          )}
        </div>

        {/* 进度条 */}
        {exportProgress && exportProgress.phase !== 'error' && (
          <div className="mt-3 space-y-2">
            <div className="flex items-center gap-2 text-sm">
              {ExportIcon && (
                <ExportIcon className={`w-4 h-4 ${
                  exportProgress.phase === 'complete' ? 'text-green-400' : 'text-emby-green animate-pulse'
                }`} />
              )}
              <span className="text-emby-text-secondary">
                {PHASE_LABELS[exportProgress.phase] || exportProgress.phase}
              </span>
              <span className="text-emby-text-muted text-xs">
                {exportProgress.message}
              </span>
            </div>
            <div className="relative h-2 bg-emby-bg-input rounded-full overflow-hidden">
              <div
                className={`absolute inset-y-0 left-0 rounded-full transition-all duration-300 ease-out ${
                  exportProgress.phase === 'complete'
                    ? 'bg-green-500'
                    : 'bg-emby-green'
                }`}
                style={{ width: `${exportProgress.percentage}%` }}
              />
            </div>
            <div className="flex justify-between text-xs text-emby-text-muted">
              <span>
                {exportProgress.total > 0
                  ? `${exportProgress.current} / ${exportProgress.total}`
                  : ''}
              </span>
              <span>{exportProgress.percentage}%</span>
            </div>
          </div>
        )}

        {/* 导出错误 */}
        {exportProgress?.phase === 'error' && (
          <p className="text-red-400 text-xs mt-2">
            导出失败: {exportProgress.message}
          </p>
        )}
      </div>

      <div className="border-t border-emby-border-subtle my-5" />

      {/* 恢复备份 */}
      <div>
        <p className="text-white text-sm">恢复备份</p>
        <p className="text-emby-text-muted text-xs mt-0.5">
          上传 ZIP 备份文件恢复所有数据（将覆盖当前数据）
        </p>

        <div className="mt-3 flex items-center gap-3">
          <label className={`inline-flex items-center gap-2 px-4 py-2 bg-emby-bg-input hover:bg-emby-border-subtle text-emby-text-secondary text-sm font-medium rounded transition-colors ${isRestoring ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}`}>
            <Upload className="w-4 h-4" />
            选择备份文件
            <input
              ref={fileInputRef}
              type="file"
              accept=".zip"
              className="hidden"
              disabled={isRestoring}
              onChange={handleFileChange}
            />
          </label>
          {selectedFile && !isRestoring && (
            <span className="text-emby-text-muted text-xs">
              已选: {selectedFile.name} ({formatFileSize(selectedFile.size)})
            </span>
          )}
        </div>

        {selectedFile && !showConfirm && !isRestoring && (
          <button
            onClick={() => setShowConfirm(true)}
            className="mt-3 inline-flex items-center gap-2 px-4 py-2 bg-amber-600 hover:bg-amber-500 text-white text-sm font-medium rounded transition-colors"
          >
            开始恢复
          </button>
        )}

        {/* 确认警告框 */}
        {showConfirm && !isRestoring && (
          <div className="mt-3 p-4 bg-amber-900/30 border border-amber-700/50 rounded">
            <div className="flex items-start gap-2">
              <AlertTriangle className="w-5 h-5 text-amber-400 flex-shrink-0 mt-0.5" />
              <div>
                <p className="text-amber-200 text-sm font-medium">
                  此操作将清空所有数据并替换为备份内容，不可撤销！
                </p>
                <div className="flex gap-2 mt-3">
                  <button
                    onClick={() => setShowConfirm(false)}
                    className="px-3 py-1.5 bg-emby-bg-input hover:bg-emby-border-subtle text-emby-text-secondary text-sm rounded transition-colors"
                  >
                    取消
                  </button>
                  <button
                    onClick={startRestore}
                    className="px-3 py-1.5 bg-red-600 hover:bg-red-500 text-white text-sm font-medium rounded transition-colors"
                  >
                    确认恢复
                  </button>
                </div>
              </div>
            </div>
          </div>
        )}

        {/* 恢复进度条 */}
        {restoreProgress && restoreProgress.phase !== 'error' && (
          <div className="mt-3 space-y-2">
            <div className="flex items-center gap-2 text-sm">
              {RestoreIcon && (
                <RestoreIcon className={`w-4 h-4 ${
                  restoreProgress.phase === 'complete' ? 'text-green-400' : 'text-amber-400 animate-pulse'
                }`} />
              )}
              <span className="text-emby-text-secondary">
                {PHASE_LABELS[restoreProgress.phase] || restoreProgress.phase}
              </span>
              <span className="text-emby-text-muted text-xs">
                {restoreProgress.message}
              </span>
            </div>
            <div className="relative h-2 bg-emby-bg-input rounded-full overflow-hidden">
              <div
                className={`absolute inset-y-0 left-0 rounded-full transition-all duration-300 ease-out ${
                  restoreProgress.phase === 'complete'
                    ? 'bg-green-500'
                    : 'bg-amber-500'
                }`}
                style={{ width: `${restoreProgress.percentage}%` }}
              />
            </div>
            <div className="flex justify-between text-xs text-emby-text-muted">
              <span>
                {restoreProgress.total > 0 && restoreProgress.phase !== 'upload'
                  ? `${restoreProgress.current} / ${restoreProgress.total}`
                  : ''}
              </span>
              <span>{restoreProgress.percentage}%</span>
            </div>
            {isRestoring && (
              <button
                onClick={cancelRestore}
                className="text-emby-text-muted hover:text-white text-xs transition-colors"
              >
                取消
              </button>
            )}
          </div>
        )}

        {/* 恢复错误 */}
        {(restoreError || restoreProgress?.phase === 'error') && (
          <div className="mt-3 p-3 bg-red-900/30 border border-red-700/50 rounded">
            <p className="text-red-400 text-sm">
              恢复失败: {restoreError || restoreProgress?.message || '未知错误'}
            </p>
          </div>
        )}

        {/* 恢复结果 */}
        {restoreResult && (
          <div className="mt-3 p-4 bg-green-900/30 border border-green-700/50 rounded">
            <div className="flex items-center gap-2">
              <CheckCircle className="w-5 h-5 text-green-400" />
              <p className="text-green-200 text-sm font-medium">恢复完成</p>
            </div>
            <div className="mt-2 grid grid-cols-4 gap-3 text-xs text-emby-text-muted">
              <div>
                <span className="text-white font-medium">{restoreResult.tablesRestored}</span> 张表
              </div>
              <div>
                <span className="text-white font-medium">{restoreResult.totalRecords.toLocaleString()}</span> 条记录
              </div>
              <div>
                <span className="text-white font-medium">{restoreResult.uploadsRestored}</span> 个文件
              </div>
              <div>
                耗时 <span className="text-white font-medium">{restoreResult.duration}</span>s
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

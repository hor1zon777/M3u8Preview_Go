import { useState, useRef } from 'react';
import { useMutation } from '@tanstack/react-query';
import { importApi } from '../services/importApi.js';
import type { ImportPreviewResponse, ImportResult, ImportItem } from '@m3u8-preview/shared';

type Step = 'input' | 'preview' | 'result';

export function ImportPage() {
  const [step, setStep] = useState<Step>('input');
  const [format, setFormat] = useState<'text' | 'file'>('text');
  const [textContent, setTextContent] = useState('');
  const [preview, setPreview] = useState<ImportPreviewResponse | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [fileName, setFileName] = useState('');
  const [selectedFileName, setSelectedFileName] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);

  const previewMutation = useMutation({
    mutationFn: async () => {
      if (format === 'text') {
        return importApi.previewText(textContent);
      } else {
        const file = fileRef.current?.files?.[0];
        if (!file) throw new Error('请选择文件');
        setFileName(file.name);
        return importApi.previewFile(file);
      }
    },
    onSuccess: (data) => {
      setPreview(data);
      setStep('preview');
    },
  });

  const executeMutation = useMutation({
    mutationFn: () => {
      if (!preview) throw new Error('No preview data');
      const validItems = preview.items.filter((_: ImportItem, i: number) =>
        !preview.errors.some(e => e.row === i + 1)
      );
      return importApi.execute(validItems, format === 'text' ? 'TEXT' : 'FILE', fileName);
    },
    onSuccess: (data) => {
      setResult(data);
      setStep('result');
    },
  });

  function reset() {
    setStep('input');
    setFormat('text');
    setTextContent('');
    setPreview(null);
    setResult(null);
    setFileName('');
    setSelectedFileName('');
  }

  const stepNames = ['input', 'preview', 'result'] as Step[];
  const stepLabels: Record<Step, string> = {
    input: '输入数据',
    preview: '预览确认',
    result: '导入结果',
  };

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      <h1 className="text-2xl font-bold text-white">批量导入</h1>

      {/* Steps indicator */}
      <div className="flex items-center gap-4 text-sm">
        {stepNames.map((s, i) => (
          <div key={s} className="flex items-center gap-2">
            <div className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-medium ${
              step === s ? 'bg-emby-green text-white' : i < stepNames.indexOf(step) ? 'bg-emby-green text-white' : 'bg-emby-bg-input text-emby-text-muted'
            }`}>
              {i + 1}
            </div>
            <span className={step === s ? 'text-white' : 'text-emby-text-muted'}>
              {stepLabels[s]}
            </span>
            {i < 2 && <span className="text-emby-text-muted mx-2">&rarr;</span>}
          </div>
        ))}
      </div>

      {/* Step 1: Input */}
      {step === 'input' && (
        <div className="space-y-4">
          <div className="flex gap-3">
            <button onClick={() => setFormat('text')} className={`px-4 py-2 rounded-lg text-sm ${format === 'text' ? 'bg-emby-green text-white' : 'bg-emby-bg-input text-emby-text-secondary hover:bg-emby-bg-elevated'}`}>
              文本输入
            </button>
            <button onClick={() => setFormat('file')} className={`px-4 py-2 rounded-lg text-sm ${format === 'file' ? 'bg-emby-green text-white' : 'bg-emby-bg-input text-emby-text-secondary hover:bg-emby-bg-elevated'}`}>
              文件上传
            </button>
          </div>

          {format === 'text' ? (
            <div className="space-y-2">
              <p className="text-sm text-emby-text-secondary">每行一条记录。格式：纯URL 或 标题|URL|作者 或 标题|URL|分类|标签|作者</p>
              <textarea
                value={textContent}
                onChange={e => setTextContent(e.target.value)}
                rows={12}
                placeholder={"https://example.com/video1.m3u8\n示例视频|https://example.com/video2.m3u8|张三\n示例视频|https://example.com/video3.m3u8|电影|动作,科幻|张三"}
                className="w-full px-4 py-3 bg-emby-bg-card border border-emby-border rounded-lg text-white placeholder-emby-text-muted text-sm font-mono focus:outline-none focus:ring-2 focus:ring-emby-green resize-y"
              />
            </div>
          ) : (
            <div className="space-y-2">
              <p className="text-sm text-emby-text-secondary">支持 CSV、Excel (.xlsx)、JSON 格式</p>
              <div className="border-2 border-dashed border-emby-border rounded-lg p-8 text-center hover:border-emby-border-light transition-colors">
                <input
                  ref={fileRef}
                  type="file"
                  accept=".csv,.xlsx,.json"
                  className="hidden"
                  onChange={() => {
                    const name = fileRef.current?.files?.[0]?.name || '';
                    setSelectedFileName(name);
                  }}
                />
                <button
                  onClick={() => fileRef.current?.click()}
                  className="px-4 py-2 bg-emby-bg-input text-white rounded-lg hover:bg-emby-bg-elevated text-sm"
                >
                  选择文件
                </button>
                <p className="text-emby-text-muted text-xs mt-2">{selectedFileName || '支持 .csv, .xlsx, .json'}</p>
              </div>
              <div className="flex gap-2">
                <a href={importApi.getTemplateUrl('csv')} download className="text-emby-green-light text-sm hover:underline">下载 CSV 模板</a>
                <span className="text-emby-text-muted">|</span>
                <a href={importApi.getTemplateUrl('json')} download className="text-emby-green-light text-sm hover:underline">下载 JSON 模板</a>
              </div>
            </div>
          )}

          <button
            onClick={() => previewMutation.mutate()}
            disabled={previewMutation.isPending || (format === 'text' && !textContent.trim())}
            className="px-6 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark disabled:opacity-50 text-sm"
          >
            {previewMutation.isPending ? '解析中...' : '预览'}
          </button>

          {previewMutation.error && (
            <div className="bg-red-500/10 border border-red-500/20 text-red-400 px-4 py-3 rounded-md text-sm">
              {(previewMutation.error as Error).message}
            </div>
          )}
        </div>
      )}

      {/* Step 2: Preview */}
      {step === 'preview' && preview && (
        <div className="space-y-4">
          <div className="flex gap-4 text-sm">
            <span className="text-emby-text-secondary">总计: <span className="text-white">{preview.totalCount}</span></span>
            <span className="text-green-400">有效: {preview.validCount}</span>
            <span className="text-red-400">无效: {preview.invalidCount}</span>
          </div>

          {/* Preview table */}
          <div className="bg-emby-bg-card border border-emby-border-subtle rounded-lg overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-emby-border-subtle">
                  <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">#</th>
                  <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">标题</th>
                  <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">URL</th>
                  <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">作者</th>
                  <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">分类</th>
                  <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">标签</th>
                  <th className="px-4 py-3 text-left text-emby-text-secondary font-medium">状态</th>
                </tr>
              </thead>
              <tbody>
                {preview.items.slice(0, 50).map((item: ImportItem, i: number) => {
                  const hasError = preview.errors.some(e => e.row === i + 1);
                  return (
                    <tr key={i} className={`border-b border-emby-border-subtle/50 ${hasError ? 'bg-red-500/5' : ''}`}>
                      <td className="px-4 py-2 text-emby-text-muted">{i + 1}</td>
                      <td className="px-4 py-2 text-white max-w-48 truncate">{item.title || '-'}</td>
                      <td className="px-4 py-2 text-emby-text-secondary max-w-48 truncate">{item.m3u8Url || '-'}</td>
                      <td className="px-4 py-2 text-emby-text-secondary">{item.artist || '-'}</td>
                      <td className="px-4 py-2 text-emby-text-secondary">{item.categoryName || '-'}</td>
                      <td className="px-4 py-2 text-emby-text-secondary">{item.tagNames?.join(', ') || '-'}</td>
                      <td className="px-4 py-2">
                        {hasError
                          ? <span className="text-red-400 text-xs">! 错误</span>
                          : <span className="text-green-400 text-xs">有效</span>
                        }
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {preview.items.length > 50 && <p className="px-4 py-2 text-emby-text-muted text-xs">仅显示前 50 条...</p>}
          </div>

          {/* Errors */}
          {preview.errors.length > 0 && (
            <div className="bg-red-500/10 border border-red-500/20 rounded-lg p-4">
              <p className="text-red-400 text-sm font-medium mb-2">错误详情：</p>
              <ul className="space-y-1">
                {preview.errors.slice(0, 10).map((err, i) => (
                  <li key={i} className="text-red-300 text-xs">行 {err.row}: {err.field} - {err.message}</li>
                ))}
              </ul>
            </div>
          )}

          <div className="flex gap-3">
            <button onClick={() => setStep('input')} className="px-4 py-2 bg-emby-bg-input text-emby-text-primary rounded-lg hover:bg-emby-bg-elevated text-sm">返回修改</button>
            <button
              onClick={() => executeMutation.mutate()}
              disabled={executeMutation.isPending || preview.validCount === 0}
              className="px-6 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark disabled:opacity-50 text-sm"
            >
              {executeMutation.isPending ? '导入中...' : `确认导入 (${preview.validCount} 条)`}
            </button>
          </div>

          {executeMutation.error && (
            <div className="bg-red-500/10 border border-red-500/20 text-red-400 px-4 py-3 rounded-md text-sm">
              {(executeMutation.error as Error).message}
            </div>
          )}
        </div>
      )}

      {/* Step 3: Result */}
      {step === 'result' && result && (
        <div className="space-y-4">
          <div className={`p-6 rounded-lg border ${result.failedCount === 0 ? 'bg-green-500/10 border-green-500/20' : 'bg-yellow-500/10 border-yellow-500/20'}`}>
            <h3 className={`text-lg font-semibold ${result.failedCount === 0 ? 'text-green-400' : 'text-yellow-400'}`}>
              {result.failedCount === 0 ? '导入完成' : '部分导入成功'}
            </h3>
            <div className="mt-3 flex gap-6 text-sm">
              <span className="text-emby-text-secondary">总计: <span className="text-white">{result.totalCount}</span></span>
              <span className="text-green-400">成功: {result.successCount}</span>
              {result.failedCount > 0 && <span className="text-red-400">失败: {result.failedCount}</span>}
            </div>
          </div>

          {result.errors && result.errors.length > 0 && (
            <div className="bg-red-500/10 border border-red-500/20 rounded-lg p-4">
              <p className="text-red-400 text-sm font-medium mb-2">失败详情：</p>
              <ul className="space-y-1">
                {result.errors.slice(0, 20).map((err, i) => (
                  <li key={i} className="text-red-300 text-xs">行 {err.row}: {err.field} - {err.message}</li>
                ))}
              </ul>
            </div>
          )}

          <button onClick={reset} className="px-6 py-2 bg-emby-green text-white rounded-lg hover:bg-emby-green-dark text-sm">
            继续导入
          </button>
        </div>
      )}
    </div>
  );
}

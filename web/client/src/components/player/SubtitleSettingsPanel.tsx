import { useEffect, useRef, useState } from 'react';
import { Captions, CaptionsOff, RotateCcw } from 'lucide-react';
import {
  useSubtitleSettingsStore,
  type SubtitleEdgeStyle,
  type SubtitleFontWeight,
} from '../../stores/subtitleSettingsStore.js';

/** 内置可选文本颜色 */
const TEXT_COLORS: Array<{ value: string; label: string }> = [
  { value: '#FFFFFF', label: '白色' },
  { value: '#FFEB3B', label: '黄色' },
  { value: '#A5D6A7', label: '浅绿' },
  { value: '#90CAF9', label: '浅蓝' },
  { value: '#FFAB91', label: '橙色' },
];

/** 内置可选背景色 */
const BG_COLORS: Array<{ value: string; label: string }> = [
  { value: '#000000', label: '黑色' },
  { value: '#1F2937', label: '深灰' },
  { value: '#3B0764', label: '深紫' },
  { value: '#FFFFFF', label: '白色' },
];

const EDGE_OPTIONS: Array<{ value: SubtitleEdgeStyle; label: string }> = [
  { value: 'none', label: '无' },
  { value: 'shadow', label: '阴影' },
  { value: 'outline', label: '描边' },
  { value: 'glow', label: '发光' },
];

const WEIGHT_OPTIONS: Array<{ value: SubtitleFontWeight; label: string }> = [
  { value: 'normal', label: '常规' },
  { value: 'bold', label: '加粗' },
];

interface SubtitleSettingsPanelProps {
  /** 字幕生成是否就绪，false 时仅展示提示文案 */
  available?: boolean;
}

/**
 * 字幕设置入口按钮 + 弹出面板。
 * 控件风格与倍速菜单保持一致：黑色半透明卡片、emby-green 高亮选中态。
 */
export function SubtitleSettingsPanel({ available = true }: SubtitleSettingsPanelProps) {
  const settings = useSubtitleSettingsStore();
  const [open, setOpen] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);

  // 点击面板外部时关闭
  useEffect(() => {
    if (!open) return;
    const handleClick = (e: MouseEvent) => {
      if (panelRef.current && !panelRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('click', handleClick, true);
    return () => document.removeEventListener('click', handleClick, true);
  }, [open]);

  const Icon = settings.enabled ? Captions : CaptionsOff;

  return (
    <div className="relative" ref={panelRef}>
      <button
        onClick={() => setOpen((v) => !v)}
        className="p-1.5 hover:bg-white/10 rounded-full transition-colors relative"
        aria-label="字幕设置"
        title="字幕设置"
      >
        <Icon className={`w-5 h-5 ${settings.enabled ? 'text-white' : 'text-white/60'}`} />
      </button>

      {open && (
        <div className="absolute bottom-full mb-2 right-0 bg-black/95 backdrop-blur-sm rounded-lg shadow-lg w-72 max-w-[calc(100vw-2rem)] p-3 text-white text-sm">
          {!available && (
            <div className="text-xs text-white/60 mb-2 px-1">
              当前媒体暂无可用字幕；以下设置会在字幕生成完成后生效。
            </div>
          )}

          {/* 字幕开关 */}
          <div className="flex items-center justify-between mb-3">
            <span className="text-xs text-white/80">显示字幕</span>
            <button
              onClick={() => settings.setEnabled(!settings.enabled)}
              className={`relative inline-flex items-center w-9 h-5 rounded-full transition-colors ${
                settings.enabled ? 'bg-emby-green' : 'bg-white/25'
              }`}
              role="switch"
              aria-checked={settings.enabled}
            >
              <span
                className={`absolute top-0.5 w-4 h-4 bg-white rounded-full transition-all ${
                  settings.enabled ? 'left-[18px]' : 'left-0.5'
                }`}
              />
            </button>
          </div>

          {/* 显示原文（双语模式）开关 */}
          <div className="flex items-center justify-between mb-3">
            <div className="flex flex-col">
              <span className="text-xs text-white/80">显示原文</span>
              <span className="text-[10px] text-white/40 leading-tight mt-0.5">
                同时显示译文与原文（仅对包含原文的字幕生效）
              </span>
            </div>
            <button
              onClick={() => settings.setShowOriginal(!settings.showOriginal)}
              className={`relative inline-flex items-center w-9 h-5 rounded-full transition-colors flex-shrink-0 ml-2 ${
                settings.showOriginal ? 'bg-emby-green' : 'bg-white/25'
              } ${settings.enabled ? '' : 'opacity-50 cursor-not-allowed'}`}
              role="switch"
              aria-checked={settings.showOriginal}
              disabled={!settings.enabled}
            >
              <span
                className={`absolute top-0.5 w-4 h-4 bg-white rounded-full transition-all ${
                  settings.showOriginal ? 'left-[18px]' : 'left-0.5'
                }`}
              />
            </button>
          </div>

          {/* 字号 */}
          <div className="mb-3">
            <div className="flex items-center justify-between mb-1">
              <span className="text-xs text-white/80">字号</span>
              <span className="text-xs tabular-nums text-white/60">{settings.fontSize}%</span>
            </div>
            <input
              type="range"
              min={50}
              max={250}
              step={5}
              value={settings.fontSize}
              onChange={(e) => settings.setFontSize(Number(e.target.value))}
              className="w-full h-1 cursor-pointer accent-emby-green"
              aria-label="字幕字号"
            />
          </div>

          {/* 文字颜色 */}
          <div className="mb-3">
            <div className="text-xs text-white/80 mb-1">文字颜色</div>
            <div className="flex gap-1.5 flex-wrap">
              {TEXT_COLORS.map((c) => (
                <button
                  key={c.value}
                  onClick={() => settings.setTextColor(c.value)}
                  className={`w-6 h-6 rounded-full border-2 transition-all ${
                    settings.textColor.toLowerCase() === c.value.toLowerCase()
                      ? 'border-emby-green scale-110'
                      : 'border-white/30 hover:border-white/60'
                  }`}
                  style={{ backgroundColor: c.value }}
                  aria-label={c.label}
                  title={c.label}
                />
              ))}
            </div>
          </div>

          {/* 文字不透明度 */}
          <div className="mb-3">
            <div className="flex items-center justify-between mb-1">
              <span className="text-xs text-white/80">文字不透明度</span>
              <span className="text-xs tabular-nums text-white/60">{settings.textOpacity}%</span>
            </div>
            <input
              type="range"
              min={20}
              max={100}
              step={5}
              value={settings.textOpacity}
              onChange={(e) => settings.setTextOpacity(Number(e.target.value))}
              className="w-full h-1 cursor-pointer accent-emby-green"
              aria-label="字幕文字不透明度"
            />
          </div>

          {/* 背景色 */}
          <div className="mb-3">
            <div className="text-xs text-white/80 mb-1">背景颜色</div>
            <div className="flex gap-1.5 flex-wrap">
              {BG_COLORS.map((c) => (
                <button
                  key={c.value}
                  onClick={() => settings.setBgColor(c.value)}
                  className={`w-6 h-6 rounded border-2 transition-all ${
                    settings.bgColor.toLowerCase() === c.value.toLowerCase()
                      ? 'border-emby-green scale-110'
                      : 'border-white/30 hover:border-white/60'
                  }`}
                  style={{ backgroundColor: c.value }}
                  aria-label={c.label}
                  title={c.label}
                />
              ))}
            </div>
          </div>

          {/* 背景不透明度 */}
          <div className="mb-3">
            <div className="flex items-center justify-between mb-1">
              <span className="text-xs text-white/80">背景不透明度</span>
              <span className="text-xs tabular-nums text-white/60">{settings.bgOpacity}%</span>
            </div>
            <input
              type="range"
              min={0}
              max={100}
              step={5}
              value={settings.bgOpacity}
              onChange={(e) => settings.setBgOpacity(Number(e.target.value))}
              className="w-full h-1 cursor-pointer accent-emby-green"
              aria-label="字幕背景不透明度"
            />
          </div>

          {/* 边缘修饰 */}
          <div className="mb-3">
            <div className="text-xs text-white/80 mb-1">边缘修饰</div>
            <div className="grid grid-cols-4 gap-1">
              {EDGE_OPTIONS.map((o) => (
                <button
                  key={o.value}
                  onClick={() => settings.setEdgeStyle(o.value)}
                  className={`px-2 py-1 text-xs rounded transition-colors ${
                    settings.edgeStyle === o.value
                      ? 'bg-emby-green text-white'
                      : 'bg-white/10 hover:bg-white/20 text-white/80'
                  }`}
                >
                  {o.label}
                </button>
              ))}
            </div>
          </div>

          {/* 字重 */}
          <div className="mb-3">
            <div className="text-xs text-white/80 mb-1">字重</div>
            <div className="grid grid-cols-2 gap-1">
              {WEIGHT_OPTIONS.map((o) => (
                <button
                  key={o.value}
                  onClick={() => settings.setFontWeight(o.value)}
                  className={`px-2 py-1 text-xs rounded transition-colors ${
                    settings.fontWeight === o.value
                      ? 'bg-emby-green text-white'
                      : 'bg-white/10 hover:bg-white/20 text-white/80'
                  }`}
                >
                  {o.label}
                </button>
              ))}
            </div>
          </div>

          {/* 垂直位置 */}
          <div className="mb-1">
            <div className="flex items-center justify-between mb-1">
              <span className="text-xs text-white/80">距底部</span>
              <span className="text-xs tabular-nums text-white/60">{settings.bottomOffset}%</span>
            </div>
            <input
              type="range"
              min={0}
              max={40}
              step={1}
              value={settings.bottomOffset}
              onChange={(e) => settings.setBottomOffset(Number(e.target.value))}
              className="w-full h-1 cursor-pointer accent-emby-green"
              aria-label="字幕距视频底部位置"
            />
          </div>

          {/* 重置 */}
          <div className="mt-3 pt-2 border-t border-white/10 flex justify-end">
            <button
              onClick={() => settings.resetToDefault()}
              className="flex items-center gap-1 px-2 py-1 text-xs text-white/70 hover:text-white hover:bg-white/10 rounded transition-colors"
              title="恢复默认设置"
            >
              <RotateCcw className="w-3 h-3" />
              恢复默认
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

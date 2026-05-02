import { create } from 'zustand';

/** 字幕边缘修饰风格 */
export type SubtitleEdgeStyle = 'none' | 'shadow' | 'outline' | 'glow';

/** 字幕字体粗细 */
export type SubtitleFontWeight = 'normal' | 'bold';

/** 用户可调字幕外观设置；持久化到 localStorage */
export interface SubtitleSettings {
  /** 是否显示字幕（开关） */
  enabled: boolean;
  /** 是否在主字幕之外同时显示 ASR 原文（双语模式） */
  showOriginal: boolean;
  /** 字号百分比，相对默认值 100；范围 50-250 */
  fontSize: number;
  /** 文本颜色，CSS 颜色字符串 */
  textColor: string;
  /** 文本不透明度 0-100 */
  textOpacity: number;
  /** 背景色，CSS 颜色字符串 */
  bgColor: string;
  /** 背景不透明度 0-100；为 0 时视为无背景 */
  bgOpacity: number;
  /** 边缘修饰，描边/阴影/发光 */
  edgeStyle: SubtitleEdgeStyle;
  /** 字重 */
  fontWeight: SubtitleFontWeight;
  /** 距视频底部的偏移百分比，5-40 */
  bottomOffset: number;
}

interface SubtitleSettingsState extends SubtitleSettings {
  setEnabled: (enabled: boolean) => void;
  setShowOriginal: (show: boolean) => void;
  setFontSize: (size: number) => void;
  setTextColor: (color: string) => void;
  setTextOpacity: (opacity: number) => void;
  setBgColor: (color: string) => void;
  setBgOpacity: (opacity: number) => void;
  setEdgeStyle: (style: SubtitleEdgeStyle) => void;
  setFontWeight: (weight: SubtitleFontWeight) => void;
  setBottomOffset: (offset: number) => void;
  resetToDefault: () => void;
}

const STORAGE_KEY = 'subtitle-settings-v1';

const DEFAULT_SETTINGS: SubtitleSettings = {
  enabled: true,
  showOriginal: false,
  fontSize: 100,
  textColor: '#FFFFFF',
  textOpacity: 100,
  bgColor: '#000000',
  bgOpacity: 50,
  edgeStyle: 'shadow',
  fontWeight: 'normal',
  bottomOffset: 8,
};

/** 限制数值范围，防止持久化数据被篡改导致溢出 */
function clamp(value: number, min: number, max: number): number {
  if (Number.isNaN(value)) return min;
  return Math.max(min, Math.min(max, value));
}

/** 校验并修正持久化字段，避免历史版本字段缺失或越界 */
function sanitize(input: Partial<SubtitleSettings>): SubtitleSettings {
  return {
    enabled: typeof input.enabled === 'boolean' ? input.enabled : DEFAULT_SETTINGS.enabled,
    showOriginal:
      typeof input.showOriginal === 'boolean' ? input.showOriginal : DEFAULT_SETTINGS.showOriginal,
    fontSize: clamp(Number(input.fontSize) || DEFAULT_SETTINGS.fontSize, 50, 250),
    textColor: typeof input.textColor === 'string' ? input.textColor : DEFAULT_SETTINGS.textColor,
    textOpacity: clamp(Number(input.textOpacity ?? DEFAULT_SETTINGS.textOpacity), 0, 100),
    bgColor: typeof input.bgColor === 'string' ? input.bgColor : DEFAULT_SETTINGS.bgColor,
    bgOpacity: clamp(Number(input.bgOpacity ?? DEFAULT_SETTINGS.bgOpacity), 0, 100),
    edgeStyle: (['none', 'shadow', 'outline', 'glow'] as SubtitleEdgeStyle[]).includes(
      input.edgeStyle as SubtitleEdgeStyle,
    )
      ? (input.edgeStyle as SubtitleEdgeStyle)
      : DEFAULT_SETTINGS.edgeStyle,
    fontWeight: input.fontWeight === 'bold' ? 'bold' : 'normal',
    bottomOffset: clamp(Number(input.bottomOffset ?? DEFAULT_SETTINGS.bottomOffset), 0, 40),
  };
}

function loadFromStorage(): SubtitleSettings {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULT_SETTINGS;
    return sanitize(JSON.parse(raw) as Partial<SubtitleSettings>);
  } catch {
    return DEFAULT_SETTINGS;
  }
}

function persist(settings: SubtitleSettings): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(settings));
  } catch {
    // 隐私模式或配额满：静默忽略，不影响播放
  }
}

export const useSubtitleSettingsStore = create<SubtitleSettingsState>((set, get) => {
  const initial = loadFromStorage();

  /** 写入并持久化的通用辅助 */
  const update = (patch: Partial<SubtitleSettings>) => {
    set(patch);
    const current: SubtitleSettings = {
      enabled: get().enabled,
      showOriginal: get().showOriginal,
      fontSize: get().fontSize,
      textColor: get().textColor,
      textOpacity: get().textOpacity,
      bgColor: get().bgColor,
      bgOpacity: get().bgOpacity,
      edgeStyle: get().edgeStyle,
      fontWeight: get().fontWeight,
      bottomOffset: get().bottomOffset,
    };
    persist(current);
  };

  return {
    ...initial,
    setEnabled: (enabled) => update({ enabled }),
    setShowOriginal: (showOriginal) => update({ showOriginal }),
    setFontSize: (fontSize) => update({ fontSize: clamp(fontSize, 50, 250) }),
    setTextColor: (textColor) => update({ textColor }),
    setTextOpacity: (textOpacity) => update({ textOpacity: clamp(textOpacity, 0, 100) }),
    setBgColor: (bgColor) => update({ bgColor }),
    setBgOpacity: (bgOpacity) => update({ bgOpacity: clamp(bgOpacity, 0, 100) }),
    setEdgeStyle: (edgeStyle) => update({ edgeStyle }),
    setFontWeight: (fontWeight) => update({ fontWeight }),
    setBottomOffset: (bottomOffset) => update({ bottomOffset: clamp(bottomOffset, 0, 40) }),
    resetToDefault: () => {
      set(DEFAULT_SETTINGS);
      persist(DEFAULT_SETTINGS);
    },
  };
});

/** 工具：将 hex 颜色 + 不透明度（0-100）合成 rgba 字符串 */
export function colorWithOpacity(hex: string, opacity: number): string {
  const normalized = hex.startsWith('#') ? hex.slice(1) : hex;
  if (normalized.length !== 6) return hex;
  const r = parseInt(normalized.slice(0, 2), 16);
  const g = parseInt(normalized.slice(2, 4), 16);
  const b = parseInt(normalized.slice(4, 6), 16);
  const a = clamp(opacity, 0, 100) / 100;
  return `rgba(${r}, ${g}, ${b}, ${a})`;
}

/** 根据 edgeStyle 生成 text-shadow 值 */
export function edgeStyleToTextShadow(style: SubtitleEdgeStyle): string {
  switch (style) {
    case 'shadow':
      return '2px 2px 4px rgba(0, 0, 0, 0.85)';
    case 'outline':
      return '-1px -1px 0 #000, 1px -1px 0 #000, -1px 1px 0 #000, 1px 1px 0 #000, 0 0 2px #000';
    case 'glow':
      return '0 0 6px rgba(255, 255, 255, 0.85), 0 0 12px rgba(255, 255, 255, 0.6)';
    case 'none':
    default:
      return 'none';
  }
}

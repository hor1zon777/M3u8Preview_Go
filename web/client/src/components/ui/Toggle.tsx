import { Loader2 } from 'lucide-react';

/**
 * 开关控件（emby 色板）。项目此前没有 Switch 组件，
 * 插件中心卡片与插件详情页头共用这一个实现。
 */
export function Toggle({
  checked,
  pending = false,
  onToggle,
  label,
}: {
  checked: boolean;
  pending?: boolean;
  onToggle: () => void;
  label: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      title={label}
      disabled={pending}
      onClick={onToggle}
      className={`relative w-10 h-[22px] rounded-full transition-colors flex-shrink-0 ${
        checked ? 'bg-emby-green' : 'bg-emby-bg-elevated border border-emby-border'
      } ${pending ? 'opacity-60 cursor-wait' : 'cursor-pointer'}`}
    >
      <span
        className={`absolute top-[2px] w-[18px] h-[18px] rounded-full bg-white shadow transition-all ${
          checked ? 'left-[20px]' : 'left-[2px]'
        }`}
      />
      {pending && <Loader2 className="absolute inset-0 m-auto w-3 h-3 animate-spin text-white/80" />}
    </button>
  );
}

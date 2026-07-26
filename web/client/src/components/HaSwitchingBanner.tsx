import { useEffect, useState } from 'react';

/**
 * 主备切换提示条。
 *
 * 触发时机：写请求打到只读副本时服务端返回 503 + code=NODE_READ_ONLY，
 * api.ts 的响应拦截器会退避重试并派发 `ha:switching` 事件（见 docs/ha-failover.md）。
 *
 * 之所以做成"事件 + 自动消失"而不是模态框：切换是秒级的瞬态状态，请求会自己重试成功，
 * 用户无需做任何操作。挡住界面反而会让人以为出了故障。
 */
const HIDE_DELAY_MS = 8000;

export function HaSwitchingBanner() {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;

    const onSwitching = () => {
      setVisible(true);
      // 每次收到事件都把消失时间往后推：切换窗口内会连续重试多次。
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => setVisible(false), HIDE_DELAY_MS);
    };

    window.addEventListener('ha:switching', onSwitching);
    return () => {
      window.removeEventListener('ha:switching', onSwitching);
      if (timer) clearTimeout(timer);
    };
  }, []);

  if (!visible) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      className="fixed top-4 left-1/2 z-50 -translate-x-1/2 rounded-md bg-amber-500/95 px-4 py-2 text-sm font-medium text-black shadow-lg"
    >
      服务节点切换中，正在自动重试…
    </div>
  );
}

import { useRef, useEffect, type ReactNode } from 'react';
import { useLocation } from 'react-router-dom';

/**
 * 轻量页面过渡动画包裹组件（零依赖）。
 * 每次路由路径变化时触发 fade-in + slide-up 动画。
 */
export function PageTransition({ children }: { children: ReactNode }) {
  const location = useLocation();
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    el.classList.remove('page-enter-active');
    // 触发 reflow 重置动画
    void el.offsetHeight;
    el.classList.add('page-enter-active');
  }, [location.pathname]);

  return (
    <div ref={containerRef} className="page-enter">
      {children}
    </div>
  );
}

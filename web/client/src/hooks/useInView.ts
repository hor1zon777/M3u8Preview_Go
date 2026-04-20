import { useRef, useState, useEffect } from 'react';

/**
 * 利用 IntersectionObserver 检测元素是否进入可视区域。
 * 一旦可见后设置 isInView = true 并断开观察（不再重置为 false）。
 * rootMargin 200px 用于预加载即将滚入视口的卡片。
 */
export function useInView<T extends HTMLElement = HTMLElement>() {
  const ref = useRef<T | null>(null);
  const [isInView, setIsInView] = useState(false);

  useEffect(() => {
    const element = ref.current;
    if (!element || isInView) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setIsInView(true);
          observer.disconnect();
        }
      },
      { rootMargin: '200px' },
    );

    observer.observe(element);
    return () => observer.disconnect();
  }, [isInView]);

  return { ref, isInView };
}

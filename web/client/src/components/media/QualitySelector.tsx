import { useState, useRef, useEffect } from 'react';
import { Settings } from 'lucide-react';
import { usePlayerStore } from '../../stores/playerStore.js';

interface QualitySelectorProps {
  dropDirection?: 'up' | 'down';
}

export function QualitySelector({ dropDirection = 'up' }: QualitySelectorProps) {
  const { qualities, quality, setQuality } = usePlayerStore();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, []);

  if (qualities.length <= 1) return null;

  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => setOpen(!open)}
        className="px-3 py-1.5 bg-emby-bg-input text-emby-text-primary rounded-md hover:bg-emby-bg-elevated text-sm flex items-center gap-1"
      >
        <Settings className="w-4 h-4" />
        {quality === -1 ? '自动' : `${qualities.find(q => q.index === quality)?.height}p`}
      </button>

      {open && (
        <div className={`absolute ${dropDirection === 'down' ? 'top-full mt-2' : 'bottom-full mb-2'} right-0 bg-emby-bg-dialog border border-emby-border rounded-md shadow-xl py-1 min-w-[120px]`}>
          <button
            onClick={() => { setQuality(-1); setOpen(false); }}
            className={`w-full text-left px-4 py-2 text-sm hover:bg-emby-bg-elevated ${quality === -1 ? 'text-emby-green-light' : 'text-emby-text-primary'}`}
          >
            自动
          </button>
          {[...qualities].sort((a, b) => b.height - a.height).map((q) => (
            <button
              key={q.index}
              onClick={() => { setQuality(q.index); setOpen(false); }}
              className={`w-full text-left px-4 py-2 text-sm hover:bg-emby-bg-elevated ${quality === q.index ? 'text-emby-green-light' : 'text-emby-text-primary'}`}
            >
              {q.height}p
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

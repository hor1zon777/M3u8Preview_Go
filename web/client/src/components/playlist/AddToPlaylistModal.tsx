import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { X, Check } from 'lucide-react';
import { playlistApi } from '../../services/playlistApi.js';
import type { Playlist } from '@m3u8-preview/shared';

interface Props {
  mediaId: string;
  isOpen: boolean;
  onClose: () => void;
}

export function AddToPlaylistModal({ mediaId, isOpen, onClose }: Props) {
  const [newName, setNewName] = useState('');
  const queryClient = useQueryClient();

  const { data: playlists } = useQuery({
    queryKey: ['playlists'],
    queryFn: () => playlistApi.getAll(),
    enabled: isOpen,
  });

  const [addedId, setAddedId] = useState<string | null>(null);

  const addMutation = useMutation({
    mutationFn: (playlistId: string) => playlistApi.addItem(playlistId, mediaId),
    onSuccess: (_data, playlistId) => {
      queryClient.invalidateQueries({ queryKey: ['playlists'] });
      // H4: 添加成功后给反馈并自动关闭
      setAddedId(playlistId);
      setTimeout(() => {
        setAddedId(null);
        onClose();
      }, 800);
    },
  });

  const createAndAdd = useMutation({
    mutationFn: async () => {
      const playlist = await playlistApi.create(newName);
      await playlistApi.addItem(playlist.id, mediaId);
      return playlist;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['playlists'] });
      setNewName('');
    },
  });

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 bg-black/70 z-50 flex items-center justify-center p-4 transition-opacity" onClick={onClose}>
      <div className="bg-emby-bg-dialog border border-emby-border rounded-md w-full max-w-md animate-modal" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between p-4 border-b border-emby-border-subtle">
          <h3 className="text-lg font-semibold text-white">添加到播放列表</h3>
          <button onClick={onClose} className="text-emby-text-secondary hover:text-white">
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="p-4 space-y-3 max-h-64 overflow-y-auto">
          {playlists?.map((pl: Playlist) => (
            <button
              key={pl.id}
              onClick={() => addMutation.mutate(pl.id)}
              disabled={addMutation.isPending}
              className="w-full text-left px-4 py-3 bg-emby-bg-input hover:bg-emby-bg-elevated rounded-md transition-colors"
            >
              <div className="flex items-center justify-between">
                <div>
                  <p className="text-white text-sm font-medium">{pl.name}</p>
                  <p className="text-emby-text-muted text-xs">{pl._count?.items || 0} 个视频</p>
                </div>
                {addedId === pl.id && <Check className="w-4 h-4 text-emby-green" />}
              </div>
            </button>
          ))}
          {(!playlists || playlists.length === 0) && (
            <p className="text-emby-text-muted text-sm text-center py-2">暂无播放列表</p>
          )}
        </div>

        <div className="p-4 border-t border-emby-border-subtle">
          <p className="text-sm text-emby-text-secondary mb-2">或创建新列表</p>
          <div className="flex gap-2">
            <input
              type="text"
              value={newName}
              onChange={e => setNewName(e.target.value)}
              placeholder="列表名称"
              className="flex-1 px-3 py-2 bg-emby-bg-input border border-emby-border rounded-md text-white text-sm placeholder-emby-text-muted focus:outline-none focus:ring-2 focus:ring-emby-green"
            />
            <button
              onClick={() => createAndAdd.mutate()}
              disabled={!newName.trim() || createAndAdd.isPending}
              className="px-4 py-2 bg-emby-green text-white rounded-md text-sm hover:bg-emby-green-dark disabled:opacity-50"
            >
              创建并添加
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

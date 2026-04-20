import { create } from 'zustand';
import type { Media } from '@m3u8-preview/shared';

interface PlayerState {
  currentMedia: Media | null;
  isPlaying: boolean;
  isBuffering: boolean;
  currentTime: number;
  duration: number;
  volume: number;
  isMuted: boolean;
  isFullscreen: boolean;
  quality: number; // -1 = auto
  qualities: Array<{ index: number; height: number; bitrate: number }>;

  setMedia: (media: Media | null) => void;
  setPlaying: (playing: boolean) => void;
  setBuffering: (buffering: boolean) => void;
  setCurrentTime: (time: number) => void;
  setDuration: (duration: number) => void;
  setAudioState: (audio: { volume: number; isMuted: boolean }) => void;
  setFullscreen: (fullscreen: boolean) => void;
  setQuality: (quality: number) => void;
  setQualities: (qualities: Array<{ index: number; height: number; bitrate: number }>) => void;
  reset: () => void;
}

export const usePlayerStore = create<PlayerState>((set) => ({
  currentMedia: null,
  isPlaying: false,
  isBuffering: false,
  currentTime: 0,
  duration: 0,
  volume: 1,
  isMuted: false,
  isFullscreen: false,
  quality: -1,
  qualities: [],

  setMedia: (media) => set({ currentMedia: media }),
  setPlaying: (isPlaying) => set({ isPlaying }),
  setBuffering: (isBuffering) => set({ isBuffering }),
  setCurrentTime: (currentTime) => set({ currentTime }),
  setDuration: (duration) => set({ duration }),
  setAudioState: ({ volume, isMuted }) => {
    const normalizedVolume = Math.max(0, Math.min(1, volume));
    return set({ volume: normalizedVolume, isMuted });
  },
  setFullscreen: (isFullscreen) => set({ isFullscreen }),
  setQuality: (quality) => set({ quality }),
  setQualities: (qualities) => set({ qualities }),
  reset: () => set({
    currentMedia: null,
    isPlaying: false,
    isBuffering: false,
    currentTime: 0,
    duration: 0,
    volume: 1,
    isMuted: false,
    quality: -1,
    qualities: [],
  }),
}));

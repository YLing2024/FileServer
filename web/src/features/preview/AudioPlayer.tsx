/**
 * 音频预览：原生 `<audio controls>`，与视频共用按 path+size 的进度记忆。
 *
 * 音频一律直链（`/api/file`），不参与 video-info/HLS 决策。
 */
import { useEffect, useRef, useState } from 'react';
import { ensureTicket, fileUrl, withTicket } from '../../api/media';
import { clearProgress, readProgress, saveProgress } from './progress';
import styles from './AudioPlayer.module.css';

interface AudioPlayerProps {
  path: string;
  name: string;
  size: number;
}

/** 原生音频播放器；恢复/保存进度不改变浏览器原生控制条行为。 */
export function AudioPlayer({ path, name, size }: AudioPlayerProps) {
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const restoredRef = useRef(false);
  const [src, setSrc] = useState('');

  useEffect(() => {
    let cancelled = false;
    restoredRef.current = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换音频时先清空旧源
    setSrc('');
    void ensureTicket().then((ticket) => {
      if (!cancelled) setSrc(withTicket(fileUrl(path), ticket));
    });
    return () => {
      cancelled = true;
    };
  }, [path]);

  useEffect(() => {
    const persist = (): void => {
      const audio = audioRef.current;
      if (!audio) return;
      saveProgress(path, size, audio.currentTime, audio.duration);
    };
    const timer = window.setInterval(persist, 5000);
    return () => {
      window.clearInterval(timer);
      persist();
    };
  }, [path, size]);

  const restore = (): void => {
    const audio = audioRef.current;
    if (!audio || restoredRef.current) return;
    restoredRef.current = true;
    const saved = readProgress(path, size);
    if (saved > 1 && (!Number.isFinite(audio.duration) || saved < audio.duration - 1)) {
      audio.currentTime = saved;
    }
  };

  return (
    <div className={styles.wrap}>
      <div className={styles.icon} aria-hidden="true">
        ♪
      </div>
      <audio
        ref={audioRef}
        className={styles.audio}
        src={src}
        controls
        preload="metadata"
        aria-label={`音频：${name}`}
        data-testid="audio-player"
        onLoadedMetadata={restore}
        onPause={() => {
          const audio = audioRef.current;
          if (audio) saveProgress(path, size, audio.currentTime, audio.duration);
        }}
        onEnded={() => clearProgress(path, size)}
      />
    </div>
  );
}

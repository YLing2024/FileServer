/**
 * 视频播放器：/api/video-info 决策 + 直链/HLS 双路径 + 自绘控制条与移动端手势。
 *
 * 行为对齐旧前端：不自动播放、直链先行、HLS 8 秒轮询、超前 seek 收敛、
 * 离开预览请求 abandon=1 并销毁 hls 实例；同时按 path+size 记忆播放进度。
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import { request } from '../../api/client';
import { ensureTicket, fileUrl, hlsUrl, withTicket } from '../../api/media';
import { fetchVideoInfo } from '../../api/videoInfo';
import { formatDuration } from '../../domain/format';
import { useToast } from '../../components/Toast';
import { attachHls, type HlsAttachment } from './attachHls';
import { clearProgress, readProgress, saveProgress } from './progress';
import { usePlayerGestures } from './usePlayerGestures';
import styles from './VideoPlayer.module.css';

interface VideoPlayerProps {
  path: string;
  name: string;
  size: number;
}

interface WebkitVideo extends HTMLVideoElement {
  webkitSupportsPresentationMode?: (mode: string) => boolean;
  webkitSetPresentationMode?: (mode: string) => void;
  webkitPresentationMode?: string;
  webkitEnterFullscreen?: () => void;
}

const RATES = [0.5, 0.75, 1, 1.25, 1.5, 2] as const;

function PlayIcon({ paused }: { paused: boolean }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      {paused ? (
        <path className={styles.filled} d="M7 5v14l12-7z" />
      ) : (
        <path className={styles.filled} d="M7 5h3.5v14H7zM13.5 5H17v14h-3.5z" />
      )}
    </svg>
  );
}

function VolumeIcon({ muted }: { muted: boolean }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M4 9v6h4l5 4V5L8 9H4z" />
      {muted ? <path d="M16 9l5 6M21 9l-5 6" /> : null}
    </svg>
  );
}

function PipIcon({ active }: { active: boolean }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <rect x="3" y="5" width="18" height="14" rx="2" />
      <rect x="12" y="11" width="7" height="6" rx="1" fill={active ? 'currentColor' : 'none'} />
    </svg>
  );
}

function FullscreenIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" />
    </svg>
  );
}

function isInteractiveTarget(target: EventTarget | null): boolean {
  return (
    target instanceof Element &&
    Boolean(target.closest('button, input, select, textarea, a, [contenteditable="true"]'))
  );
}

/** 自绘视频播放器。 */
export function VideoPlayer({ path, name, size }: VideoPlayerProps) {
  const { showToast } = useToast();
  const playerRef = useRef<HTMLDivElement | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const knownDurationRef = useRef(0);
  const wantPlayRef = useRef(false);
  const restoredRef = useRef(false);
  const switchedToHlsRef = useRef(false);
  const hlsRef = useRef<HlsAttachment | null>(null);
  const hlsPathRef = useRef<string | null>(null);
  const lastSavedRef = useRef(0);

  const [mode, setMode] = useState<'direct' | 'hls'>('direct');
  const [playing, setPlaying] = useState(false);
  const [current, setCurrent] = useState(0);
  const [duration, setDuration] = useState(0);
  const [buffered, setBuffered] = useState(0);
  const [volume, setVolume] = useState(1);
  const [muted, setMuted] = useState(false);
  const [rate, setRate] = useState(1);
  const [buffering, setBuffering] = useState(true);
  const [fullscreen, setFullscreen] = useState(false);
  const [pipActive, setPipActive] = useState(false);
  const [pipSupported, setPipSupported] = useState(false);
  const [hint, setHint] = useState('');
  const [error, setError] = useState('');
  const [reloadToken, setReloadToken] = useState(0);
  const hintTimerRef = useRef<number | null>(null);
  // 键盘闭包读取最新音量，避免每次音量变化重绑 document 监听。
  const volumeRef = useRef(volume);
  useEffect(() => {
    volumeRef.current = volume;
  }, [volume]);

  const currentDuration = useCallback((): number => {
    const video = videoRef.current;
    const media = video && Number.isFinite(video.duration) ? video.duration : 0;
    return knownDurationRef.current > 0 ? knownDurationRef.current : media;
  }, []);

  const syncTime = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    setCurrent(video.currentTime || 0);
    const total = currentDuration();
    if (total > 0) setDuration(total);
    try {
      if (video.buffered.length > 0) {
        setBuffered(video.buffered.end(video.buffered.length - 1));
      }
    } catch {
      // 某些浏览器在 src 切换瞬间访问 buffered 会抛错：忽略这一帧。
    }
  }, [currentDuration]);

  const persist = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    saveProgress(path, size, video.currentTime, currentDuration());
    lastSavedRef.current = Date.now();
  }, [path, size, currentDuration]);

  const showHint = useCallback((text: string, sticky: boolean) => {
    setHint(text);
    if (hintTimerRef.current !== null) window.clearTimeout(hintTimerRef.current);
    hintTimerRef.current = window.setTimeout(
      () => {
        setHint('');
        hintTimerRef.current = null;
      },
      sticky ? 1200 : 700,
    );
  }, []);

  const hideHint = useCallback(() => {
    if (hintTimerRef.current !== null) {
      window.clearTimeout(hintTimerRef.current);
      hintTimerRef.current = null;
    }
    setHint('');
  }, []);

  const safePlay = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    if (!video.src && !video.currentSrc) {
      wantPlayRef.current = true;
      return;
    }
    void video.play().catch((playError: unknown) => {
      if (playError instanceof DOMException && playError.name === 'AbortError') {
        window.setTimeout(() => {
          if (videoRef.current === video) {
            void video.play().catch(() => showToast('播放失败', 'error'));
          }
        }, 300);
        return;
      }
      showToast('播放失败', 'error');
    });
  }, [showToast]);

  const requestPlay = useCallback(() => {
    const video = videoRef.current;
    if (!video || (!video.src && !video.currentSrc)) {
      wantPlayRef.current = true;
      return;
    }
    safePlay();
  }, [safePlay]);

  const togglePlay = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) requestPlay();
    else video.pause();
  }, [requestPlay]);

  const restoreProgress = useCallback(() => {
    const video = videoRef.current;
    if (!video || restoredRef.current) return;
    restoredRef.current = true;
    const saved = readProgress(path, size);
    if (saved <= 1) return;
    const total = currentDuration();
    if (total > 0 && saved >= total - 2) return;
    try {
      video.currentTime = saved;
    } catch {
      // 元数据尚未就绪：保持从头播放。
    }
  }, [path, size, currentDuration]);

  const toggleFullscreen = useCallback(() => {
    const player = playerRef.current;
    const video = videoRef.current as WebkitVideo | null;
    if (document.fullscreenElement) {
      void document.exitFullscreen().catch(() => showToast('退出全屏失败', 'error'));
      return;
    }
    if (video && /iPhone|iPad|iPod/i.test(navigator.userAgent) && video.webkitEnterFullscreen) {
      video.webkitEnterFullscreen();
      return;
    }
    if (!player) return;
    void player.requestFullscreen().catch(() => showToast('全屏不可用', 'error'));
  }, [showToast]);

  const togglePip = useCallback(() => {
    const video = videoRef.current as WebkitVideo | null;
    if (!video) return;
    try {
      if (document.pictureInPictureElement) {
        void document.exitPictureInPicture().catch(() => showToast('退出画中画失败', 'error'));
        return;
      }
      if (document.pictureInPictureEnabled && typeof video.requestPictureInPicture === 'function') {
        void video.requestPictureInPicture().catch(() => showToast('画中画不可用', 'error'));
        return;
      }
      if (video.webkitSupportsPresentationMode?.('picture-in-picture')) {
        video.webkitSetPresentationMode?.(
          video.webkitPresentationMode === 'picture-in-picture' ? 'inline' : 'picture-in-picture',
        );
      }
    } catch {
      showToast('画中画不可用', 'error');
    }
  }, [showToast]);

  const seekTo = useCallback(
    (seconds: number) => {
      const video = videoRef.current;
      if (!video) return;
      const total = currentDuration();
      const target = total > 0 ? Math.min(total, Math.max(0, seconds)) : Math.max(0, seconds);
      video.currentTime = target;
      setCurrent(target);
    },
    [currentDuration],
  );

  const setVolumeValue = useCallback((value: number) => {
    const video = videoRef.current;
    if (!video) return;
    const next = Math.min(1, Math.max(0, value));
    video.volume = next;
    video.muted = false;
    setVolume(next);
    setMuted(false);
  }, []);

  // 媒体事件 → React 状态。
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    const onPlay = (): void => setPlaying(true);
    const onPause = (): void => {
      setPlaying(false);
      persist();
    };
    const onEnded = (): void => {
      setPlaying(false);
      clearProgress(path, size);
    };
    const onWaiting = (): void => setBuffering(true);
    const onCanPlay = (): void => {
      setBuffering(false);
      setPlaying(!video.paused);
    };
    const onVolume = (): void => {
      setVolume(video.volume);
      setMuted(video.muted);
    };
    const onRate = (): void => setRate(video.playbackRate);
    const onTime = (): void => {
      syncTime();
      if (Date.now() - lastSavedRef.current > 2000) persist();
    };
    const onLoaded = (): void => {
      syncTime();
      restoreProgress();
    };
    const onError = (): void => {
      setBuffering(false);
      window.setTimeout(() => {
        if (switchedToHlsRef.current) return;
        setError('视频加载失败（可能是编码不支持或服务端转码出错）');
      }, 0);
    };
    const onFullscreen = (): void => setFullscreen(Boolean(document.fullscreenElement));
    const onEnterPip = (): void => setPipActive(true);
    const onLeavePip = (): void => setPipActive(false);
    const onWebkitPip = (): void =>
      setPipActive((video as WebkitVideo).webkitPresentationMode === 'picture-in-picture');

    video.addEventListener('play', onPlay);
    video.addEventListener('pause', onPause);
    video.addEventListener('ended', onEnded);
    video.addEventListener('waiting', onWaiting);
    video.addEventListener('canplay', onCanPlay);
    video.addEventListener('playing', onCanPlay);
    video.addEventListener('timeupdate', onTime);
    video.addEventListener('progress', onTime);
    video.addEventListener('durationchange', syncTime);
    video.addEventListener('loadedmetadata', onLoaded);
    video.addEventListener('volumechange', onVolume);
    video.addEventListener('ratechange', onRate);
    video.addEventListener('error', onError);
    video.addEventListener('enterpictureinpicture', onEnterPip);
    video.addEventListener('leavepictureinpicture', onLeavePip);
    video.addEventListener('webkitpresentationmodechanged', onWebkitPip);
    document.addEventListener('fullscreenchange', onFullscreen);

    return () => {
      video.removeEventListener('play', onPlay);
      video.removeEventListener('pause', onPause);
      video.removeEventListener('ended', onEnded);
      video.removeEventListener('waiting', onWaiting);
      video.removeEventListener('canplay', onCanPlay);
      video.removeEventListener('playing', onCanPlay);
      video.removeEventListener('timeupdate', onTime);
      video.removeEventListener('progress', onTime);
      video.removeEventListener('durationchange', syncTime);
      video.removeEventListener('loadedmetadata', onLoaded);
      video.removeEventListener('volumechange', onVolume);
      video.removeEventListener('ratechange', onRate);
      video.removeEventListener('error', onError);
      video.removeEventListener('enterpictureinpicture', onEnterPip);
      video.removeEventListener('leavepictureinpicture', onLeavePip);
      video.removeEventListener('webkitpresentationmodechanged', onWebkitPip);
      document.removeEventListener('fullscreenchange', onFullscreen);
    };
  }, [path, size, persist, restoreProgress, syncTime]);

  // 画中画能力探测：两种实现都没有时按钮不渲染。
  useEffect(() => {
    const video = videoRef.current as WebkitVideo | null;
    if (!video) return;
    setPipSupported(
      Boolean(
        (document.pictureInPictureEnabled && typeof video.requestPictureInPicture === 'function') ||
        video.webkitSupportsPresentationMode?.('picture-in-picture'),
      ),
    );
  }, []);

  // 键盘快捷键：焦点在交互控件上时让原生行为优先。
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent): void => {
      if (isInteractiveTarget(event.target)) return;
      switch (event.key) {
        case ' ':
          event.preventDefault();
          togglePlay();
          break;
        case 'ArrowLeft':
          event.preventDefault();
          seekTo((videoRef.current?.currentTime ?? 0) - 5);
          break;
        case 'ArrowRight':
          event.preventDefault();
          seekTo((videoRef.current?.currentTime ?? 0) + 5);
          break;
        case 'ArrowUp':
          event.preventDefault();
          setVolumeValue(volumeRef.current + 0.1);
          break;
        case 'ArrowDown':
          event.preventDefault();
          setVolumeValue(volumeRef.current - 0.1);
          break;
        case 'm':
        case 'M': {
          const video = videoRef.current;
          if (video) video.muted = !video.muted;
          break;
        }
        case 'p':
        case 'P':
          if (pipSupported) togglePip();
          break;
        case 'f':
        case 'F':
          toggleFullscreen();
          break;
        default:
          break;
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [pipSupported, seekTo, setVolumeValue, toggleFullscreen, togglePip, togglePlay]);

  // 播放源：直链先行，video-info 异步补时长或切换到 HLS。
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    let cancelled = false;
    knownDurationRef.current = 0;
    restoredRef.current = false;
    switchedToHlsRef.current = false;
    setMode('direct');
    setError('');
    setBuffering(true);
    setCurrent(0);
    setDuration(0);
    setBuffered(0);

    const start = async (): Promise<void> => {
      const ticket = await ensureTicket();
      if (cancelled) return;
      video.src = withTicket(fileUrl(path), ticket);
      if (wantPlayRef.current) {
        wantPlayRef.current = false;
        safePlay();
      }
    };
    void start();

    void fetchVideoInfo(path).then((info) => {
      if (cancelled) return;
      if (info.duration && info.duration > 0) {
        knownDurationRef.current = info.duration;
        setDuration(info.duration);
      }
      if (info.mode === 'hls') {
        switchedToHlsRef.current = true;
        restoredRef.current = false;
        setMode('hls');
        video.removeAttribute('src');
        video.load();
        const attachment = attachHls(video, path, {
          onReady: () => {
            if (cancelled) return;
            setBuffering(false);
            restoreProgress();
            if (wantPlayRef.current) {
              wantPlayRef.current = false;
              safePlay();
            }
          },
          onSeekClamped: (message) => showToast(message),
          onError: (message) => {
            if (cancelled) return;
            setError(message);
            showToast(message, 'error');
          },
        });
        hlsRef.current = attachment;
        hlsPathRef.current = path;
        return;
      }
      if (wantPlayRef.current) {
        wantPlayRef.current = false;
        safePlay();
      }
    });

    return () => {
      cancelled = true;
      hlsRef.current?.destroy();
      hlsRef.current = null;
      const hlsPath = hlsPathRef.current;
      hlsPathRef.current = null;
      if (hlsPath) {
        void request(hlsUrl(hlsPath, true)).catch(() => {
          // 离开页面时终止转码失败：不影响本地资源释放。
        });
      }
      try {
        if (document.pictureInPictureElement) void document.exitPictureInPicture().catch(() => {});
      } catch {
        // 浏览器不支持画中画：无需退出。
      }
      const webkit = video as WebkitVideo;
      if (webkit.webkitPresentationMode === 'picture-in-picture') {
        try {
          webkit.webkitSetPresentationMode?.('inline');
        } catch {
          // 忽略退出失败。
        }
      }
      try {
        video.pause();
        video.removeAttribute('src');
        video.load();
      } catch {
        // 元素已卸载：忽略。
      }
    };
  }, [path, size, reloadToken, restoreProgress, safePlay, showToast]);

  usePlayerGestures(playerRef, videoRef, {
    getDuration: currentDuration,
    formatTime: formatDuration,
    onSeek: seekTo,
    onTogglePlay: togglePlay,
    onVolume: setVolumeValue,
    onRate: setRate,
    showHint,
    hideHint,
  });

  const progress = duration > 0 ? Math.min(100, (current / duration) * 100) : 0;
  const bufferedPercent = duration > 0 ? Math.min(100, (buffered / duration) * 100) : 0;
  const pipTitle = pipActive ? '退出画中画 (P)' : '画中画 (P)';

  return (
    <div ref={playerRef} className={styles.player} data-testid="video-player" data-mode={mode}>
      <video
        ref={videoRef}
        className={styles.video}
        preload="auto"
        playsInline
        aria-label={`视频：${name}`}
        data-testid="video-element"
      />

      {buffering && !error ? (
        <div className={styles.spinner} role="status" aria-label="缓冲中" />
      ) : null}

      {!playing && !error ? (
        <button
          type="button"
          className={styles.bigPlay}
          data-big-play
          aria-label="播放"
          onClick={requestPlay}
        >
          <PlayIcon paused />
        </button>
      ) : null}

      {hint ? (
        <div className={styles.hint} aria-live="polite" data-testid="gesture-hint">
          {hint}
        </div>
      ) : null}

      {error ? (
        <div className={styles.error} role="alert">
          <p>{error}</p>
          <button
            type="button"
            className={styles.retry}
            onClick={() => {
              setError('');
              setReloadToken((value) => value + 1);
            }}
          >
            重试
          </button>
        </div>
      ) : null}

      <div className={styles.controls} data-player-controls>
        <button
          type="button"
          className={styles.button}
          aria-label={playing ? '暂停' : '播放'}
          title={playing ? '暂停 (空格)' : '播放 (空格)'}
          onClick={togglePlay}
        >
          <PlayIcon paused={!playing} />
        </button>

        <div className={styles.progress}>
          <div className={styles.track} aria-hidden="true">
            <div className={styles.buffered} style={{ width: `${bufferedPercent}%` }} />
            <div className={styles.played} style={{ width: `${progress}%` }} />
          </div>
          <input
            className={styles.progressInput}
            type="range"
            min={0}
            max={duration > 0 ? duration : 0}
            step={0.1}
            value={duration > 0 ? Math.min(current, duration) : 0}
            aria-label="播放进度"
            onChange={(event) => seekTo(Number(event.target.value))}
          />
        </div>

        <span className={styles.time} data-testid="player-time">
          {formatDuration(current)} / {formatDuration(duration)}
        </span>

        <div className={styles.volume}>
          <button
            type="button"
            className={styles.button}
            aria-label={muted ? '取消静音 (M)' : '静音 (M)'}
            title={muted ? '取消静音 (M)' : '静音 (M)'}
            onClick={() => {
              const video = videoRef.current;
              if (video) video.muted = !video.muted;
            }}
          >
            <VolumeIcon muted={muted || volume === 0} />
          </button>
          <input
            className={styles.volumeInput}
            type="range"
            min={0}
            max={100}
            step={1}
            value={Math.round((muted ? 0 : volume) * 100)}
            aria-label="音量"
            onChange={(event) => setVolumeValue(Number(event.target.value) / 100)}
          />
        </div>

        <select
          className={styles.rate}
          aria-label="播放倍速"
          title="播放倍速"
          value={String(rate)}
          onChange={(event) => {
            const next = Number(event.target.value);
            const video = videoRef.current;
            if (video) video.playbackRate = next;
            setRate(next);
          }}
        >
          {RATES.map((item) => (
            <option value={item} key={item}>
              {item}x
            </option>
          ))}
        </select>

        {pipSupported ? (
          <button
            type="button"
            className={`${styles.button} ${pipActive ? styles.active : ''}`}
            aria-label={pipTitle}
            aria-pressed={pipActive}
            title={pipTitle}
            onClick={togglePip}
          >
            <PipIcon active={pipActive} />
          </button>
        ) : null}

        <button
          type="button"
          className={styles.button}
          aria-label={fullscreen ? '退出全屏 (F)' : '全屏 (F)'}
          aria-pressed={fullscreen}
          title={fullscreen ? '退出全屏 (F)' : '全屏 (F)'}
          onClick={toggleFullscreen}
        >
          <FullscreenIcon />
        </button>
      </div>
    </div>
  );
}

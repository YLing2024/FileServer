/**
 * 播放器触摸手势：只在触摸设备注册，桌面鼠标环境完全不受影响。
 *
 * | 手势 | 区域 | 行为 |
 * |---|---|---|
 * | 双击 | 左/右 1/3 | ∓10 秒 |
 * | 双击 | 中间 1/3 | 播放/暂停 |
 * | 长按 350ms | 画面任意处 | 临时 2x，松手恢复原速率 |
 * | 横滑 | 画面任意处 | 松手时提交 seek |
 * | 竖滑 | 右侧 1/3 | 调音量 |
 *
 * 滑动过程中只更新提示，不连续 seek；转码分片按需生成，连续 seek 会拖垮服务端。
 */
import { useEffect, useRef } from 'react';

/** 手势需要的外部能力；由播放器注入，避免手势模块自己维护第二份播放状态。 */
export interface GestureOptions {
  getDuration: () => number;
  formatTime: (seconds: number) => string;
  onSeek: (seconds: number) => void;
  onTogglePlay: () => void;
  onVolume: (volume: number) => void;
  onRate: (rate: number) => void;
  showHint: (text: string, sticky: boolean) => void;
  hideHint: () => void;
}

const TAP_MAX_MS = 300;
const LONG_PRESS_MS = 350;
const MOVE_THRESHOLD = 10;

function isTouchDevice(): boolean {
  const coarse =
    typeof window.matchMedia === 'function' && window.matchMedia('(pointer: coarse)').matches;
  return coarse || 'ontouchstart' in window;
}

interface StartState {
  x: number;
  y: number;
  time: number;
  volume: number;
  fromBigPlay: boolean;
}

/** 给播放器挂手势；返回清理函数。 */
export function usePlayerGestures(
  playerRef: React.RefObject<HTMLDivElement | null>,
  videoRef: React.RefObject<HTMLVideoElement | null>,
  options: GestureOptions,
): void {
  const optionsRef = useRef(options);
  useEffect(() => {
    optionsRef.current = options;
  }, [options]);

  useEffect(() => {
    const player = playerRef.current;
    const video = videoRef.current;
    if (!player || !video || !isTouchDevice()) return;

    let active = false;
    let moved = false;
    let mode: '' | 'seek' | 'volume' | 'long' | 'cancel' = '';
    let start: StartState | null = null;
    let seekTarget = 0;
    let originalRate = 1;
    let lastTapAt = 0;
    let lastTapX = 0;
    let lastTapY = 0;
    let suppressTapUntil = 0;
    let longPressTimer: number | null = null;

    const duration = (): number => {
      const value = optionsRef.current.getDuration();
      return Number.isFinite(value) && value > 0 ? value : 0;
    };

    const clearLongPress = (): void => {
      if (longPressTimer !== null) {
        window.clearTimeout(longPressTimer);
        longPressTimer = null;
      }
    };

    const endLongPress = (): void => {
      if (mode !== 'long') return;
      video.playbackRate = originalRate;
      optionsRef.current.onRate(originalRate);
      mode = 'cancel';
      suppressTapUntil = Date.now() + TAP_MAX_MS;
      optionsRef.current.hideHint();
    };

    const onStart = (event: TouchEvent): void => {
      if (event.touches.length !== 1) return;
      const target = event.target;
      if (!(target instanceof Element)) return;
      if (target.closest('[data-player-controls], input, select, textarea, a')) return;
      const button = target.closest('button');
      if (button && !button.hasAttribute('data-big-play')) return;

      const touch = event.touches[0];
      if (!touch) return;
      active = true;
      moved = false;
      mode = '';
      start = {
        x: touch.clientX,
        y: touch.clientY,
        time: video.currentTime || 0,
        volume: video.volume,
        fromBigPlay: Boolean(button?.hasAttribute('data-big-play')),
      };
      clearLongPress();
      longPressTimer = window.setTimeout(() => {
        longPressTimer = null;
        if (!active || moved || mode) return;
        mode = 'long';
        lastTapAt = 0;
        originalRate = video.playbackRate;
        video.playbackRate = 2;
        optionsRef.current.onRate(2);
        optionsRef.current.showHint('2x 倍速播放中', true);
      }, LONG_PRESS_MS);
    };

    const onMove = (event: TouchEvent): void => {
      if (!active || !start || event.touches.length !== 1) return;
      const touch = event.touches[0];
      if (!touch) return;
      const dx = touch.clientX - start.x;
      const dy = touch.clientY - start.y;
      if (!moved && (Math.abs(dx) > MOVE_THRESHOLD || Math.abs(dy) > MOVE_THRESHOLD)) {
        moved = true;
        clearLongPress();
        if (mode === 'long') {
          event.preventDefault();
          endLongPress();
          return;
        }
      }
      if (!moved || mode === 'long' || mode === 'cancel') return;

      const rect = player.getBoundingClientRect();
      if (!mode) {
        if (Math.abs(dx) > Math.abs(dy)) {
          mode = 'seek';
        } else if (start.x > rect.left + rect.width * (2 / 3)) {
          mode = 'volume';
        } else {
          mode = 'cancel';
          return;
        }
      }

      event.preventDefault();
      if (mode === 'seek') {
        const total = duration();
        const span = total > 0 ? Math.max(15, Math.min(120, total * 0.5)) : 60;
        const raw = start.time + (dx / (rect.width || 1)) * span;
        seekTarget = total > 0 ? Math.min(total, Math.max(0, raw)) : Math.max(0, raw);
        optionsRef.current.showHint(
          `${optionsRef.current.formatTime(seekTarget)} / ${optionsRef.current.formatTime(total)}`,
          true,
        );
      } else if (mode === 'volume') {
        const next = Math.min(1, Math.max(0, start.volume - (dy / (rect.height || 1)) * 1.2));
        optionsRef.current.onVolume(next);
        optionsRef.current.showHint(`音量 ${Math.round(next * 100)}%`, true);
      }
    };

    const commitDoubleTap = (clientX: number): void => {
      const rect = player.getBoundingClientRect();
      const x = clientX - rect.left;
      if (x < rect.width / 3) {
        optionsRef.current.onSeek(Math.max(0, (video.currentTime || 0) - 10));
        optionsRef.current.showHint('-10s', false);
      } else if (x > (rect.width * 2) / 3) {
        const total = duration();
        const target = (video.currentTime || 0) + 10;
        optionsRef.current.onSeek(total > 0 ? Math.min(total, target) : target);
        optionsRef.current.showHint('+10s', false);
      } else {
        optionsRef.current.onTogglePlay();
      }
    };

    const onEnd = (event: TouchEvent): void => {
      if (!active) return;
      active = false;
      const wasBigPlay = start?.fromBigPlay ?? false;
      start = null;
      clearLongPress();

      if (mode === 'long') {
        event.preventDefault();
        endLongPress();
        return;
      }
      if (mode === 'seek') {
        event.preventDefault();
        optionsRef.current.onSeek(seekTarget);
        optionsRef.current.showHint(
          `${optionsRef.current.formatTime(seekTarget)} / ${optionsRef.current.formatTime(duration())}`,
          false,
        );
        mode = '';
        return;
      }
      if (mode === 'volume') {
        event.preventDefault();
        optionsRef.current.hideHint();
        mode = '';
        return;
      }
      if (mode === 'cancel' || moved) {
        mode = '';
        return;
      }
      if (wasBigPlay) {
        lastTapAt = 0;
        mode = '';
        return;
      }

      const now = Date.now();
      if (now < suppressTapUntil) {
        lastTapAt = 0;
        mode = '';
        return;
      }
      const touch = event.changedTouches[0];
      if (!touch) return;
      const isDouble =
        now - lastTapAt < TAP_MAX_MS &&
        Math.abs(touch.clientX - lastTapX) < 40 &&
        Math.abs(touch.clientY - lastTapY) < 40;
      if (isDouble) {
        lastTapAt = 0;
        event.preventDefault();
        commitDoubleTap(touch.clientX);
      } else {
        lastTapAt = now;
        lastTapX = touch.clientX;
        lastTapY = touch.clientY;
      }
      mode = '';
    };

    const onCancel = (): void => {
      active = false;
      clearLongPress();
      if (mode === 'long') endLongPress();
      else optionsRef.current.hideHint();
      mode = '';
      moved = false;
      start = null;
    };

    const onContextMenu = (event: Event): void => {
      const target = event.target;
      if (target instanceof Element && target.closest('[data-player-controls], input, select, a')) {
        return;
      }
      event.preventDefault();
    };

    player.addEventListener('touchstart', onStart, { passive: false });
    player.addEventListener('touchmove', onMove, { passive: false });
    player.addEventListener('touchend', onEnd, { passive: false });
    player.addEventListener('touchcancel', onCancel, { passive: false });
    player.addEventListener('contextmenu', onContextMenu);

    return () => {
      clearLongPress();
      if (mode === 'long') {
        try {
          video.playbackRate = originalRate;
        } catch {
          // 元素已卸载：无需恢复。
        }
      }
      player.removeEventListener('touchstart', onStart);
      player.removeEventListener('touchmove', onMove);
      player.removeEventListener('touchend', onEnd);
      player.removeEventListener('touchcancel', onCancel);
      player.removeEventListener('contextmenu', onContextMenu);
    };
  }, [playerRef, videoRef]);
}

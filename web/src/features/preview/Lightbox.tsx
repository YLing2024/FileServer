/**
 * 图片灯箱：原图优先、骨架加载、缩放/旋转、前后切换。
 *
 * 缩放在桌面用滚轮、触屏用双指；左右切换支持按钮、键盘 ←/→ 与触屏横滑。
 * 灯箱固定深色背景，不跟随主题，避免亮色主题下图片边缘失真。
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { downloadFile, ensureTicket, fileUrl, withTicket } from '../../api/media';
import styles from './Lightbox.module.css';

interface LightboxProps {
  path: string;
  /** 当前目录内的图片路径（含当前图片），顺序即切换顺序。 */
  paths: string[];
  onClose: () => void;
  onChange: (path: string) => void;
  onToast: (message: string, tone?: 'info' | 'error') => void;
}

const MIN_ZOOM = 0.25;
const MAX_ZOOM = 8;
const ZOOM_STEP = 1.5;
const SWIPE_THRESHOLD = 48;

function clampZoom(value: number): number {
  return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, value));
}

function distance(a: Touch, b: Touch): number {
  return Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY);
}

function fileName(path: string): string {
  const parts = path.split('/');
  return parts[parts.length - 1] ?? path;
}

/** 图片灯箱；切图只 replace 当前 URL，不新增浏览器历史。 */
export function Lightbox({ path, paths, onClose, onChange, onToast }: LightboxProps) {
  const images = useMemo(() => (paths.includes(path) ? paths : [path, ...paths]), [path, paths]);
  const found = images.indexOf(path);
  const [localIndex, setLocalIndex] = useState(0);
  const index = found >= 0 ? found : Math.min(localIndex, images.length - 1);
  const current = images[index] ?? path;
  const [src, setSrc] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);
  const [zoom, setZoom] = useState(1);
  const [rotate, setRotate] = useState(0);
  const stageRef = useRef<HTMLDivElement | null>(null);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const zoomRef = useRef(1);
  const touchStartRef = useRef<{ x: number; y: number } | null>(null);
  const pinchRef = useRef<{ distance: number; zoom: number } | null>(null);

  useEffect(() => {
    zoomRef.current = zoom;
  }, [zoom]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换图片时重置缩放/旋转
    setZoom(1);
    setRotate(0);
  }, [current]);

  useEffect(() => {
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换图片时重置加载态
    setLoaded(false);
    setFailed(false);
    setSrc('');
    void ensureTicket().then((ticket) => {
      if (!cancelled) setSrc(withTicket(fileUrl(current), ticket));
    });
    return () => {
      cancelled = true;
    };
  }, [current, reloadToken]);

  // 相邻图片预加载，切换方向几乎零等待。
  useEffect(() => {
    const neighbors = [images[index + 1], images[index - 1]].filter((item): item is string =>
      Boolean(item),
    );
    let cancelled = false;
    void ensureTicket().then((ticket) => {
      if (cancelled) return;
      for (const neighbor of neighbors) {
        const preload = new Image();
        preload.src = withTicket(fileUrl(neighbor), ticket);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [images, index]);

  useEffect(() => {
    const previous = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    closeRef.current?.focus();
    return () => {
      document.body.style.overflow = previous;
    };
  }, []);

  const go = useCallback(
    (delta: number) => {
      if (images.length <= 1) return;
      const next = (index + delta + images.length) % images.length;
      const nextPath = images[next];
      if (!nextPath) return;
      setLocalIndex(next);
      onChange(nextPath);
    },
    [images, index, onChange],
  );

  const zoomBy = useCallback((factor: number) => {
    setZoom((value) => clampZoom(value * factor));
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent): void => {
      const target = event.target;
      if (
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        target instanceof HTMLSelectElement
      ) {
        return;
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
      } else if (event.key === 'ArrowLeft') {
        event.preventDefault();
        go(-1);
      } else if (event.key === 'ArrowRight') {
        event.preventDefault();
        go(1);
      } else if (event.key === '+' || event.key === '=') {
        event.preventDefault();
        zoomBy(ZOOM_STEP);
      } else if (event.key === '-') {
        event.preventDefault();
        zoomBy(1 / ZOOM_STEP);
      } else if (event.key === 'r' || event.key === 'R') {
        setRotate((value) => (value + 90) % 360);
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [go, onClose, zoomBy]);

  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;

    const onWheel = (event: WheelEvent): void => {
      event.preventDefault();
      setZoom((value) => clampZoom(value * (event.deltaY < 0 ? 1.2 : 1 / 1.2)));
    };

    const onTouchStart = (event: TouchEvent): void => {
      if (event.touches.length === 2) {
        const first = event.touches[0];
        const second = event.touches[1];
        if (first && second) {
          pinchRef.current = { distance: distance(first, second), zoom: zoomRef.current };
        }
        touchStartRef.current = null;
        return;
      }
      const touch = event.touches[0];
      if (touch) touchStartRef.current = { x: touch.clientX, y: touch.clientY };
      pinchRef.current = null;
    };

    const onTouchMove = (event: TouchEvent): void => {
      if (event.touches.length === 2) {
        const first = event.touches[0];
        const second = event.touches[1];
        const pinch = pinchRef.current;
        if (first && second && pinch && pinch.distance > 0) {
          event.preventDefault();
          setZoom(clampZoom(pinch.zoom * (distance(first, second) / pinch.distance)));
        }
      }
    };

    const onTouchEnd = (event: TouchEvent): void => {
      if (pinchRef.current) {
        if (event.touches.length < 2) pinchRef.current = null;
        return;
      }
      const start = touchStartRef.current;
      touchStartRef.current = null;
      if (!start || zoomRef.current > 1) return;
      const touch = event.changedTouches[0];
      if (!touch) return;
      const dx = touch.clientX - start.x;
      if (Math.abs(dx) > SWIPE_THRESHOLD) go(dx < 0 ? 1 : -1);
    };

    stage.addEventListener('wheel', onWheel, { passive: false });
    stage.addEventListener('touchstart', onTouchStart, { passive: true });
    stage.addEventListener('touchmove', onTouchMove, { passive: false });
    stage.addEventListener('touchend', onTouchEnd, { passive: true });
    return () => {
      stage.removeEventListener('wheel', onWheel);
      stage.removeEventListener('touchstart', onTouchStart);
      stage.removeEventListener('touchmove', onTouchMove);
      stage.removeEventListener('touchend', onTouchEnd);
    };
  }, [go]);

  const name = fileName(current);
  const zoomed = zoom > 1;

  return (
    <div
      className={styles.overlay}
      role="dialog"
      aria-modal="true"
      aria-label={`图片预览：${name}`}
      data-testid="lightbox"
    >
      <header className={styles.top}>
        <div className={styles.titles}>
          <span className={styles.name} title={name}>
            {name}
          </span>
          <span className={styles.count}>
            {index + 1} / {images.length}
          </span>
        </div>
        <div className={styles.actions}>
          <button
            type="button"
            className={styles.action}
            aria-label="缩小"
            title="缩小 (-)"
            onClick={() => zoomBy(1 / ZOOM_STEP)}
          >
            −
          </button>
          <span className={styles.zoom} aria-label={`缩放 ${Math.round(zoom * 100)}%`}>
            {Math.round(zoom * 100)}%
          </span>
          <button
            type="button"
            className={styles.action}
            aria-label="放大"
            title="放大 (+)"
            onClick={() => zoomBy(ZOOM_STEP)}
          >
            +
          </button>
          <button
            type="button"
            className={styles.action}
            aria-label="旋转 90 度"
            title="旋转 (R)"
            onClick={() => setRotate((value) => (value + 90) % 360)}
          >
            ⟳
          </button>
          <button
            type="button"
            className={styles.action}
            aria-label="下载原图"
            title="下载原图"
            onClick={() => {
              void downloadFile(current, name).catch(() => onToast('下载失败', 'error'));
            }}
          >
            ↓
          </button>
          <button
            ref={closeRef}
            type="button"
            className={`${styles.action} ${styles.close}`}
            aria-label="关闭预览"
            title="关闭 (Esc)"
            onClick={onClose}
          >
            ×
          </button>
        </div>
      </header>

      <div className={styles.stage} ref={stageRef} data-testid="lightbox-stage">
        {!loaded && !failed ? <div className={styles.skeleton} aria-label="图片加载中" /> : null}
        {failed ? (
          <div className={styles.failed} role="alert">
            <p>图片加载失败</p>
            <button
              type="button"
              className={styles.retry}
              onClick={() => setReloadToken((value) => value + 1)}
            >
              重试
            </button>
          </div>
        ) : null}
        {src ? (
          <img
            className={styles.image}
            src={src}
            alt={name}
            draggable={false}
            data-testid="lightbox-image"
            style={{
              transform: `rotate(${rotate}deg) scale(${zoom})`,
              cursor: zoomed ? 'zoom-out' : 'zoom-in',
              visibility: loaded ? 'visible' : 'hidden',
            }}
            onLoad={() => setLoaded(true)}
            onError={() => {
              setLoaded(false);
              setFailed(true);
            }}
            onClick={() => setZoom((value) => (value > 1 ? 1 : 2))}
          />
        ) : null}
        {images.length > 1 ? (
          <>
            <button
              type="button"
              className={`${styles.nav} ${styles.prev}`}
              aria-label="上一张"
              title="上一张 (←)"
              onClick={() => go(-1)}
            >
              ‹
            </button>
            <button
              type="button"
              className={`${styles.nav} ${styles.next}`}
              aria-label="下一张"
              title="下一张 (→)"
              onClick={() => go(1)}
            >
              ›
            </button>
          </>
        ) : null}
      </div>
    </div>
  );
}

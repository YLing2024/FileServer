/** 视频缩略图 hook：进入视口后入队抽帧，卸载时取消任务。 */
import { useEffect, useRef, useState, type RefObject } from 'react';
import { enqueueVideoThumb, getCachedVideoThumb } from './videoThumb';

interface VideoThumbState {
  ref: RefObject<HTMLDivElement | null>;
  src: string | null;
  failed: boolean;
}

/**
 * 浏览器抽帧缩略图。
 *
 * @param path 文件相对路径，作为缓存键
 * @param enabled 仅 MP4/WebM 家族启用；冷门格式由服务端缩略图或图标兜底
 */
export function useVideoThumbnail(path: string, enabled: boolean): VideoThumbState {
  const ref = useRef<HTMLDivElement | null>(null);
  // jsdom 等没有 IntersectionObserver 的环境按“已进入视口”处理；真实浏览器走懒加载。
  const [visible, setVisible] = useState(() => typeof IntersectionObserver === 'undefined');
  const [src, setSrc] = useState<string | null>(() => getCachedVideoThumb(path) ?? null);
  const [failed, setFailed] = useState(() => getCachedVideoThumb(path) === null);

  useEffect(() => {
    if (!enabled) return;
    const element = ref.current;
    if (!element) return;
    if (typeof IntersectionObserver === 'undefined') return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { rootMargin: '200px' },
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [enabled, path]);

  useEffect(() => {
    if (!enabled || !visible) return;
    if (getCachedVideoThumb(path) !== undefined) return;

    const job = enqueueVideoThumb(path);
    let cancelled = false;
    job.promise
      .then((value) => {
        if (cancelled) return;
        if (value) {
          setSrc(value);
          setFailed(false);
        } else {
          setFailed(true);
        }
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });

    return () => {
      cancelled = true;
      job.cancel();
    };
  }, [enabled, visible, path]);

  return { ref, src, failed };
}

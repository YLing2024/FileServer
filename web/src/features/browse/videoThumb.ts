/**
 * 视频缩略图浏览器抽帧队列。
 *
 * 旧前端的策略：卡片进入视口才入队，最多 3 路并发；抽帧源走 /api/thumb-src
 * （服务端返回截短的合法 MP4），seek 到 10% 处画到 300×300 canvas。
 * 这里把同一策略收敛成模块级队列，供 React 组件按需入队/取消。
 */
import { ensureTicket, thumbSrcUrl, withTicket } from '../../api/media';

const CACHE_MAX = 200;
const MAX_ACTIVE = 3;
const TIMEOUT_MS = 20_000;
const FRAME_SIZE = 300;

/** 缓存值：dataURL 表示成功，null 表示本会话内已知失败（不再重试）。 */
type CachedThumb = string | null;

interface Job {
  path: string;
  cancelled: boolean;
  done: boolean;
  resolve: (value: string | null) => void;
}

/** 抽帧任务句柄：调用方卸载时 cancel，避免离开视图后继续占磁盘/连接。 */
export interface VideoThumbJob {
  promise: Promise<string | null>;
  cancel: () => void;
}

const cache = new Map<string, CachedThumb>();
const queue: Job[] = [];
const active = new Map<Job, HTMLVideoElement>();

function cacheSet(path: string, value: CachedThumb): void {
  cache.delete(path);
  cache.set(path, value);
  while (cache.size > CACHE_MAX) {
    const oldest = cache.keys().next().value;
    if (oldest === undefined) break;
    cache.delete(oldest);
  }
}

/** 读取缓存：undefined 未缓存，null 已知失败，字符串为 dataURL。 */
export function getCachedVideoThumb(path: string): CachedThumb | undefined {
  return cache.get(path);
}

/** 清空缓存并取消所有在途/排队任务（冷门格式开关切换后缩略图策略会变）。 */
export function clearVideoThumbCache(): void {
  cache.clear();
  for (const job of [...queue, ...active.keys()]) cancelJob(job);
}

function cleanupVideo(video: HTMLVideoElement): void {
  try {
    video.pause();
  } catch {
    // 元素可能已脱离文档；暂停失败不影响释放。
  }
  try {
    video.removeAttribute('src');
    video.load();
  } catch {
    // 释放失败时浏览器仍会在 GC 后回收。
  }
}

function cancelJob(job: Job): void {
  job.cancelled = true;
  const index = queue.indexOf(job);
  if (index >= 0) queue.splice(index, 1);

  const video = active.get(job);
  if (video) {
    active.delete(job);
    cleanupVideo(video);
  }
  if (!job.done) {
    job.done = true;
    job.resolve(null);
  }
  pump();
}

function pump(): void {
  while (active.size < MAX_ACTIVE && queue.length > 0) {
    const job = queue.shift();
    if (!job) break;
    if (job.cancelled) {
      job.resolve(null);
      continue;
    }
    runJob(job);
  }
}

function runJob(job: Job): void {
  const video = document.createElement('video');
  active.set(job, video);
  video.preload = 'metadata';
  video.muted = true;
  video.playsInline = true;
  video.dataset.fsThumb = '1';

  const timer = window.setTimeout(() => finish(null), TIMEOUT_MS);

  const finish = (value: CachedThumb): void => {
    if (job.done) return;
    job.done = true;
    window.clearTimeout(timer);
    active.delete(job);
    cleanupVideo(video);
    if (!job.cancelled) cacheSet(job.path, value);
    job.resolve(value);
    pump();
  };

  video.addEventListener('loadedmetadata', () => {
    const duration = video.duration;
    const seekTo = Number.isFinite(duration) && duration > 0 ? Math.min(1, duration * 0.1) : 0.1;
    try {
      video.currentTime = seekTo;
    } catch {
      finish(null);
    }
  });

  video.addEventListener('seeked', () => {
    try {
      const canvas = document.createElement('canvas');
      canvas.width = FRAME_SIZE;
      canvas.height = FRAME_SIZE;
      const context = canvas.getContext('2d');
      if (!context) {
        finish(null);
        return;
      }
      const width = video.videoWidth;
      const height = video.videoHeight;
      if (!width || !height) {
        finish(null);
        return;
      }
      const scale = Math.max(FRAME_SIZE / width, FRAME_SIZE / height);
      const drawWidth = width * scale;
      const drawHeight = height * scale;
      context.drawImage(
        video,
        (FRAME_SIZE - drawWidth) / 2,
        (FRAME_SIZE - drawHeight) / 2,
        drawWidth,
        drawHeight,
      );
      finish(canvas.toDataURL('image/jpeg', 0.72));
    } catch {
      finish(null);
    }
  });

  video.addEventListener('error', () => finish(null));

  void ensureTicket().then((ticket) => {
    if (job.cancelled || job.done) return;
    video.src = withTicket(thumbSrcUrl(job.path), ticket);
  });
}

/** 入队一个视频抽帧任务；缓存命中时立即返回。 */
export function enqueueVideoThumb(path: string): VideoThumbJob {
  const cached = cache.get(path);
  if (cached !== undefined) {
    return { promise: Promise.resolve(cached), cancel: () => undefined };
  }

  let job: Job | null = null;
  const promise = new Promise<string | null>((resolve) => {
    job = { path, cancelled: false, done: false, resolve };
    queue.push(job);
    pump();
  });

  return {
    promise,
    cancel: () => {
      if (job) cancelJob(job);
    },
  };
}

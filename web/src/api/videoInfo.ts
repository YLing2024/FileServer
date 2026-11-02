/**
 * /api/video-info 播放决策，带会话级缓存。
 *
 * 决策本身是毫秒级的（扩展名 + MP4 头探测），但重复点开同一视频仍会多一次往返；
 * 与旧前端一样缓存有效结果（mode=hls 或带 duration），失败结果不缓存以便重试。
 */
import { requestJson } from './client';
import { videoInfoUrl } from './media';

/** /api/video-info 响应中前端使用的字段。 */
export interface VideoInfo {
  mode: 'direct' | 'hls';
  mime?: string;
  duration?: number;
  width?: number;
  height?: number;
}

const CACHE_MAX = 200;
const cache = new Map<string, Promise<VideoInfo>>();

function isVideoInfo(value: unknown): value is VideoInfo {
  if (typeof value !== 'object' || value === null) return false;
  const mode = (value as { mode?: unknown }).mode;
  return mode === 'direct' || mode === 'hls';
}

/** 读取播放决策；网络失败按 direct 处理，避免卡住直链播放。 */
export function fetchVideoInfo(path: string): Promise<VideoInfo> {
  const cached = cache.get(path);
  if (cached) return cached;

  const promise = requestJson<VideoInfo>(videoInfoUrl(path))
    .then((info) => {
      if (!isVideoInfo(info) || (info.mode !== 'hls' && !info.duration)) {
        cache.delete(path);
      }
      return isVideoInfo(info) ? info : { mode: 'direct' as const };
    })
    .catch(() => {
      cache.delete(path);
      return { mode: 'direct' as const };
    });

  cache.set(path, promise);
  while (cache.size > CACHE_MAX) {
    const oldest = cache.keys().next().value;
    if (oldest === undefined) break;
    cache.delete(oldest);
  }
  return promise;
}

/** 清空缓存（切换冷门格式开关后下次重新探测）。 */
export function clearVideoInfoCache(): void {
  cache.clear();
}

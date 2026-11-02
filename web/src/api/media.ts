/**
 * 浏览器原生媒体 URL 的凭据处理。
 *
 * `<img>` / `<video>` / `<a download>` 无法携带 Authorization 头，旧前端通过
 * `POST /api/auth/ticket` 换一张只对 GET 生效的短票，再以 `?t=` 附加到 URL。
 * 新前端沿用同一约定；认证关闭时 ensureTicket 直接返回空串，不产生额外请求。
 */
import { requestJson } from './client';
import { readToken } from './token';

interface TicketState {
  value: string;
  expires: number;
  inflight: Promise<string> | null;
}

const state: TicketState = { value: '', expires: 0, inflight: null };

/** 短票剩余寿命是否仍大于 60 秒；不足则重新签发，避免长页面中途失效。 */
function ticketFresh(): boolean {
  return state.value !== '' && state.expires * 1000 - Date.now() > 60_000;
}

/** 获取当前会话短票；未登录或认证关闭时返回空串。 */
export function ensureTicket(): Promise<string> {
  if (!readToken()) return Promise.resolve('');
  if (ticketFresh()) return Promise.resolve(state.value);
  if (state.inflight) return state.inflight;

  state.inflight = requestJson<{ ticket?: string; expires?: number }>('/api/auth/ticket', {
    method: 'POST',
  })
    .then((body) => {
      if (typeof body.ticket === 'string') state.value = body.ticket;
      state.expires = typeof body.expires === 'number' ? body.expires : 0;
      return state.value;
    })
    .catch(() => '')
    .finally(() => {
      state.inflight = null;
    });
  return state.inflight;
}

/**
 * 同步读取已缓存的短票，供 hls.js 的 xhrSetup 在每次请求前重写 `?t=`。
 * 尚未签发时返回空串；调用方应先 await ensureTicket()。
 */
export function cachedTicket(): string {
  return state.value;
}

/** 清除缓存的短票（401 或主动登出后调用）。 */
export function clearTicket(): void {
  state.value = '';
  state.expires = 0;
  state.inflight = null;
}

/** 给 URL 追加短票；无票时原样返回。 */
export function withTicket(url: string, ticket: string): string {
  if (!ticket) return url;
  return `${url}${url.includes('?') ? '&' : '?'}t=${encodeURIComponent(ticket)}`;
}

/** 图片缩略图 URL（服务端 Go 解码缩放）。 */
export function thumbUrl(path: string, width = 300, height = 300): string {
  const params = new URLSearchParams({ path, w: String(width), h: String(height) });
  return `/api/thumb?${params.toString()}`;
}

/** 视频抽帧专用源 URL（服务端返回截短的合法 MP4）。 */
export function thumbSrcUrl(path: string): string {
  return `/api/thumb-src?${new URLSearchParams({ path }).toString()}`;
}

/** 原始文件下载 URL。 */
export function fileDownloadUrl(path: string): string {
  return `/api/file?${new URLSearchParams({ path, dl: '1' }).toString()}`;
}

/** 目录打包下载 URL（流式 zip，含子目录）。 */
export function zipDownloadUrl(path: string): string {
  return `/api/zip?${new URLSearchParams({ path }).toString()}`;
}

/** 原始文件直链 URL（不强制附件下载）。 */
export function fileUrl(path: string): string {
  return `/api/file?${new URLSearchParams({ path }).toString()}`;
}

/** HLS 播放列表或分片 URL；`abandon=1` 用于离开预览时终止服务端转码。 */
export function hlsUrl(path: string, abandon = false): string {
  const params = new URLSearchParams({ path, f: 'index.m3u8' });
  if (abandon) params.set('abandon', '1');
  return `/api/hls?${params.toString()}`;
}

/** 视频播放决策 URL。 */
export function videoInfoUrl(path: string): string {
  return `/api/video-info?${new URLSearchParams({ path }).toString()}`;
}

/**
 * 触发浏览器下载。原生 `<a download>` 不能带 Authorization，
 * 因此先换短票再拼到 URL 上。
 */
export async function downloadFile(path: string, name: string): Promise<void> {
  const ticket = await ensureTicket();
  const link = document.createElement('a');
  link.href = withTicket(fileDownloadUrl(path), ticket);
  link.download = name;
  document.body.appendChild(link);
  link.click();
  link.remove();
}

/** 目录打包下载；文件名形如 `目录名.zip`。 */
export async function downloadZip(path: string, name: string): Promise<void> {
  const ticket = await ensureTicket();
  const link = document.createElement('a');
  link.href = withTicket(zipDownloadUrl(path), ticket);
  link.download = `${name}.zip`;
  document.body.appendChild(link);
  link.click();
  link.remove();
}

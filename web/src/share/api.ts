/**
 * 公开分享页的数据访问：只调用 `/api/public/share/<token>/*`。
 *
 * 独立于管理端 fetch 封装：分享页不发送登录凭据，也不能因为公开接口
 * 返回 401/403 就清理同浏览器里的登录态。错误码由服务端稳定给出，
 * 页面据此展示原因。
 */

/** 公开页 info 响应；未通过提取码时只有展示所需的最小信息。 */
export interface PublicShareInfo {
  note: string;
  has_code: boolean;
  verified: boolean;
  allow_preview: boolean;
  created: number;
  expires: number;
  max_downloads: number;
  downloads: number;
  status: string;
  kind?: string;
  name?: string;
  size?: number;
  mtime?: number;
}

/** 公开目录条目，不含任何绝对路径。 */
export interface PublicEntry {
  name: string;
  is_dir: boolean;
  size: number;
  mtime: number;
  ext: string;
  kind: string;
}

/** 公开目录列表响应。 */
export interface PublicListResponse {
  path: string;
  entries: PublicEntry[];
  total: number;
  truncated: boolean;
}

/** 公开接口错误：带服务端稳定 code，页面据此选择文案。 */
export class ShareError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ShareError';
    this.status = status;
    this.code = code;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

async function getJson<T>(url: string, signal?: AbortSignal): Promise<T> {
  let response: Response;
  try {
    response = await fetch(url, { cache: 'no-store', credentials: 'same-origin', signal });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new ShareError(0, 'network', '网络请求失败，请检查连接');
  }
  const text = await response.text();
  let body: unknown = null;
  if (text) {
    try {
      body = JSON.parse(text) as unknown;
    } catch {
      body = null;
    }
  }
  if (!response.ok) {
    const code = isRecord(body) && typeof body.code === 'string' ? body.code : '';
    const message =
      isRecord(body) && typeof body.error === 'string' ? body.error : `请求失败（HTTP ${response.status}）`;
    throw new ShareError(response.status, code, message);
  }
  return body as T;
}

/** 公开接口根路径；token 可能包含 URL 保留字符，统一编码。 */
export function shareBase(token: string): string {
  return `/api/public/share/${encodeURIComponent(token)}`;
}

/** 读取分享元信息。 */
export function fetchShareInfo(token: string, signal?: AbortSignal): Promise<PublicShareInfo> {
  return getJson<PublicShareInfo>(`${shareBase(token)}/info`, signal);
}

/** 提交提取码；成功后服务端下发 30 分钟 HttpOnly cookie。 */
export async function verifyShareCode(token: string, code: string): Promise<void> {
  await postJson<{ ok: boolean }>(`${shareBase(token)}/verify`, { code });
}

/** 目录列表。 */
export function fetchShareList(
  token: string,
  path: string,
  signal?: AbortSignal,
): Promise<PublicListResponse> {
  const params = new URLSearchParams({ path, limit: '2000' });
  return getJson<PublicListResponse>(`${shareBase(token)}/list?${params.toString()}`, signal);
}

/** 单文件/目录内文件的预览 URL；`path` 为空表示分享根单文件。 */
export function sharePreviewUrl(token: string, path?: string): string {
  const base = `${shareBase(token)}/preview`;
  return path ? `${base}?${new URLSearchParams({ path }).toString()}` : base;
}

/** 下载 URL；`path` 为空表示分享根单文件。 */
export function shareDownloadUrl(token: string, path?: string): string {
  const base = `${shareBase(token)}/download`;
  return path ? `${base}?${new URLSearchParams({ path }).toString()}` : base;
}

/** 目录打包下载 URL。 */
export function shareZipUrl(token: string, path: string): string {
  return `${shareBase(token)}/zip?${new URLSearchParams({ path }).toString()}`;
}

/** POST 请求（仅提取码校验使用）。 */
export async function postJson<T>(url: string, body: unknown): Promise<T> {
  let response: Response;
  try {
    response = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      cache: 'no-store',
      credentials: 'same-origin',
    });
  } catch {
    throw new ShareError(0, 'network', '网络请求失败，请检查连接');
  }
  const text = await response.text();
  let parsed: unknown = null;
  if (text) {
    try {
      parsed = JSON.parse(text) as unknown;
    } catch {
      parsed = null;
    }
  }
  if (!response.ok) {
    const code = isRecord(parsed) && typeof parsed.code === 'string' ? parsed.code : '';
    const message =
      isRecord(parsed) && typeof parsed.error === 'string'
        ? parsed.error
        : `请求失败（HTTP ${response.status}）`;
    throw new ShareError(response.status, code, message);
  }
  return parsed as T;
}

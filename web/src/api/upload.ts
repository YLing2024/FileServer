/**
 * 上传接口封装：单请求、分片、断点查询、合并、秒传与目录预创建。
 *
 * 仅单请求 `/api/upload` 用 XHR（需要上传进度）；其余短 JSON 请求复用
 * `requestJson`，这样 401 清凭据、错误归一与其它 API 完全一致。
 */
import { ApiError } from './errors';
import { reportUnauthorized, requestJson } from './client';
import { readToken } from './token';

/** 同名冲突策略；ask 由前端弹窗询问后改用 overwrite/rename 重试。 */
export type UploadMode = 'ask' | 'overwrite' | 'rename';

/** 上传成功条目（服务端返回的最终落盘名可能与请求名不同）。 */
export interface UploadedFile {
  name: string;
  size: number;
  mtime: number;
}

/** `/api/upload`、`/api/upload/complete`、`/api/upload/instant` 的公共响应。 */
export interface UploadResponse {
  uploaded?: UploadedFile[];
  instant?: boolean;
}

/** `/api/upload/status` 响应。会话不存在时 received 为空、total 为 0。 */
export interface UploadStatus {
  upload_id: string;
  received: number[];
  total: number;
  size: number;
  complete: boolean;
  created_at: number;
  expires_at: number;
}

/** `/api/upload/mkdir` 响应。 */
export interface MkdirResponse {
  ok: boolean;
  name: string;
  path: string;
  created: boolean;
}

/** 单请求上传结果：完成或同名冲突（其余错误抛 ApiError）。 */
export type SmallUploadResult =
  | { kind: 'done'; uploaded: UploadedFile[] }
  | { kind: 'conflict'; existing: readonly string[]; message: string };

function queryString(values: Record<string, string>): string {
  return new URLSearchParams(values).toString();
}

/** 单请求上传 URL；sha256 只在已计算时附带。 */
export function uploadUrl(dir: string, mode: UploadMode, sha256 = ''): string {
  const params: Record<string, string> = { dir, mode };
  if (sha256) params.sha256 = sha256;
  return `/api/upload?${queryString(params)}`;
}

function responseMessage(text: string, status: number): string {
  if (text) {
    try {
      const body: unknown = JSON.parse(text);
      if (typeof body === 'object' && body !== null) {
        const message = (body as { error?: unknown }).error;
        if (typeof message === 'string' && message) return message;
      }
    } catch {
      // 非 JSON 响应沿用状态码文案。
    }
  }
  return `上传失败（HTTP ${status}）`;
}

function conflictFromBody(text: string, message: string): SmallUploadResult {
  let existing: readonly string[] = [];
  try {
    const body: unknown = JSON.parse(text);
    if (typeof body === 'object' && body !== null) {
      const value = (body as { existing?: unknown }).existing;
      if (Array.isArray(value)) {
        existing = value.filter((item): item is string => typeof item === 'string');
      }
    }
  } catch {
    // 冲突响应缺少 JSON 体时仍按冲突处理，由调用方决定策略。
  }
  return { kind: 'conflict', existing, message };
}

/**
 * XHR 单请求上传（multipart），提供上传进度与取消。
 * 401 会触发全局登出回调；其余失败抛出归一后的 ApiError。
 */
export function uploadSmall(
  file: File,
  name: string,
  dir: string,
  mode: UploadMode,
  sha256: string,
  onProgress: (loaded: number, total: number) => void,
  signal: AbortSignal,
): Promise<SmallUploadResult> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const abort = (): void => xhr.abort();
    if (signal.aborted) {
      reject(new DOMException('已取消', 'AbortError'));
      return;
    }
    signal.addEventListener('abort', abort, { once: true });

    const cleanup = (): void => signal.removeEventListener('abort', abort);

    xhr.open('POST', uploadUrl(dir, mode, sha256));
    const token = readToken();
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`);
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress(event.loaded, event.total);
    };
    xhr.onload = () => {
      cleanup();
      if (xhr.status === 401) {
        reportUnauthorized();
        reject(new ApiError(401, '登录状态已失效'));
        return;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        let uploaded: UploadedFile[] = [];
        try {
          const body = JSON.parse(xhr.responseText) as UploadResponse;
          if (Array.isArray(body.uploaded)) uploaded = body.uploaded;
        } catch {
          // 成功响应体异常时仍以 HTTP 状态为准。
        }
        resolve({ kind: 'done', uploaded });
        return;
      }
      const message = responseMessage(xhr.responseText, xhr.status);
      if (xhr.status === 409) {
        resolve(conflictFromBody(xhr.responseText, message));
        return;
      }
      reject(new ApiError(xhr.status, message));
    };
    xhr.onerror = () => {
      cleanup();
      reject(new ApiError(0, '网络错误'));
    };
    xhr.onabort = () => {
      cleanup();
      reject(new DOMException('已取消', 'AbortError'));
    };

    const form = new FormData();
    form.append('file', file, name);
    xhr.send(form);
  });
}

/** 查询分片会话进度；会话不存在也返回 200（received=[]）。 */
export function fetchUploadStatus(
  uploadId: string,
  dir: string,
  name: string,
  size: number,
  signal: AbortSignal,
): Promise<UploadStatus> {
  const params = queryString({ upload_id: uploadId, size: String(size), name, dir });
  return requestJson<UploadStatus>(`/api/upload/status?${params}`, { signal });
}

/**
 * 上传一个分片（application/octet-stream），返回本次已收分片数。
 * 与旧前端一致使用 XHR：分片进度用于逐文件进度条。
 */
export function uploadChunk(
  uploadId: string,
  index: number,
  total: number,
  size: number,
  name: string,
  dir: string,
  blob: Blob,
  onProgress: (loaded: number) => void,
  signal: AbortSignal,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const params = queryString({
      upload_id: uploadId,
      index: String(index),
      total: String(total),
      size: String(size),
      name,
      dir,
    });
    const xhr = new XMLHttpRequest();
    const abort = (): void => xhr.abort();
    if (signal.aborted) {
      reject(new DOMException('已取消', 'AbortError'));
      return;
    }
    signal.addEventListener('abort', abort, { once: true });
    const cleanup = (): void => signal.removeEventListener('abort', abort);

    xhr.open('POST', `/api/upload/chunk?${params}`);
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    const token = readToken();
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`);
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress(event.loaded);
    };
    xhr.onload = () => {
      cleanup();
      if (xhr.status === 401) {
        reportUnauthorized();
        reject(new ApiError(401, '登录状态已失效'));
        return;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
        return;
      }
      reject(new ApiError(xhr.status, responseMessage(xhr.responseText, xhr.status)));
    };
    xhr.onerror = () => {
      cleanup();
      reject(new ApiError(0, '网络错误'));
    };
    xhr.onabort = () => {
      cleanup();
      reject(new DOMException('已取消', 'AbortError'));
    };
    xhr.send(blob);
  });
}

/** 合并分片并落位；409 冲突会以 ApiError（含 existing）抛给调用方。 */
export function completeUpload(
  uploadId: string,
  dir: string,
  name: string,
  mode: UploadMode,
  sha256: string,
  signal: AbortSignal,
): Promise<UploadResponse> {
  return requestJson<UploadResponse>('/api/upload/complete', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ upload_id: uploadId, dir, name, mode, sha256 }),
    signal,
  });
}

/** 秒传探测；命中时服务端直接创建目标文件。 */
export function instantUpload(
  sha256: string,
  name: string,
  dir: string,
  size: number,
  mode: UploadMode,
  signal: AbortSignal,
): Promise<UploadResponse> {
  return requestJson<UploadResponse>('/api/upload/instant', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ sha256, name, dir, size, mode }),
    signal,
  });
}

/** 文件夹上传的目录预创建（单层，不递归）。 */
export function mkdirUpload(
  dir: string,
  name: string,
  mode: UploadMode,
  signal: AbortSignal,
): Promise<MkdirResponse> {
  return requestJson<MkdirResponse>('/api/upload/mkdir', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ dir, name, mode }),
    signal,
  });
}

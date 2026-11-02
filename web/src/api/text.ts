/**
 * 文本文件读取与保存接口。
 *
 * 只读预览走 `/api/file`，编辑态才调用这里：`GET /api/text` 返回内容、版本与
 * 可编辑原因，`POST /api/text` 带读取时版本做并发校验（409 表示他人已修改）。
 */
import { requestJson } from './client';

/** `GET /api/text` 响应。`reason` 为不可编辑原因：ext / too_large。 */
export interface TextDocument {
  path: string;
  content: string;
  size: number;
  mtime: number;
  version: string;
  editable: boolean;
  max_bytes: number;
  reason?: string;
}

/** 读取可编辑文本内容与版本标识。 */
export function fetchText(path: string, signal?: AbortSignal): Promise<TextDocument> {
  const query = new URLSearchParams({ path }).toString();
  return requestJson<TextDocument>(`/api/text?${query}`, { signal });
}

/** 保存文本；版本不一致时服务端返回 409，由 UI 提示重新载入。 */
export function saveText(
  path: string,
  content: string,
  version: string,
): Promise<{ path: string; size: number; mtime: number; version: string }> {
  return requestJson('/api/text', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path, content, version }),
  });
}

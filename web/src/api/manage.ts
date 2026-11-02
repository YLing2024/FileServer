/**
 * 文件管理接口：重命名、移动、复制、删除（移入回收站）。
 *
 * 全部是短 JSON 请求；批量操作即使部分失败也返回 200，失败项在 failed 中
 * 逐条给出路径与中文原因，调用方负责提示而不是把整批当失败。
 */
import { requestJson } from './client';

/** 冲突策略：ask 先返回 409 询问，overwrite/rename 由用户选择后重试。 */
export type ManageMode = 'ask' | 'overwrite' | 'rename';

/** 批量操作中的单项失败。 */
export interface ManageFailure {
  path: string;
  error: string;
}

/** 移动/复制成功项。 */
export interface TransferPair {
  from: string;
  to: string;
}

/** `/api/move`、`/api/copy` 响应。 */
export interface TransferResponse {
  ok: boolean;
  moved?: TransferPair[];
  copied?: TransferPair[];
  failed?: ManageFailure[];
}

/** `/api/delete` 的单个回收站条目。 */
export interface TrashMove {
  path: string;
  id: string;
}

/** `/api/delete` 响应。 */
export interface DeleteResponse {
  ok: boolean;
  moved?: TrashMove[];
  failed?: ManageFailure[];
}

/** `/api/rename` 响应。 */
export interface RenameResponse {
  ok: boolean;
  path: string;
  name: string;
}

/** 回收站列表项。id 是字符串（时间戳 + 随机后缀）。 */
export interface TrashItem {
  id: string;
  path: string;
  name: string;
  kind: string;
  is_dir: boolean;
  size: number;
  deleted_at: number;
}

/** `/api/trash` 响应。 */
export interface TrashListResponse {
  items: TrashItem[];
  count: number;
  total_size: number;
}

/** `/api/trash/restore` 响应。 */
export interface RestoreResponse {
  ok: boolean;
  restored?: { id: string; path: string; from: string }[];
  failed?: ManageFailure[];
}

/** `/api/trash/empty` 响应。 */
export interface EmptyTrashResponse {
  ok: boolean;
  deleted?: string[];
  failed?: ManageFailure[];
}

/** 重命名当前目录内的单个条目；同名冲突返回 409。 */
export function renameEntry(path: string, name: string): Promise<RenameResponse> {
  return requestJson<RenameResponse>('/api/rename', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path, name }),
  });
}

function transfer(
  kind: 'move' | 'copy',
  paths: string[],
  dir: string,
  mode: ManageMode,
): Promise<TransferResponse> {
  return requestJson<TransferResponse>(`/api/${kind}?mode=${encodeURIComponent(mode)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ paths, dir }),
  });
}

/** 移动条目到目标目录；目标不能是条目自身或其子目录。 */
export function moveEntries(
  paths: string[],
  dir: string,
  mode: ManageMode = 'ask',
): Promise<TransferResponse> {
  return transfer('move', paths, dir, mode);
}

/** 复制条目到目标目录（目录递归复制）。 */
export function copyEntries(
  paths: string[],
  dir: string,
  mode: ManageMode = 'ask',
): Promise<TransferResponse> {
  return transfer('copy', paths, dir, mode);
}

/** 把条目移入回收站（可恢复，不是物理删除）。 */
export function deleteEntries(paths: string[]): Promise<DeleteResponse> {
  return requestJson<DeleteResponse>('/api/delete', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ paths }),
  });
}

/** 列出回收站条目（按删除时间倒序）。 */
export function fetchTrash(signal?: AbortSignal): Promise<TrashListResponse> {
  return requestJson<TrashListResponse>('/api/trash', { signal });
}

/** 恢复回收站条目；原位置冲突且 mode=ask 时返回 409。 */
export function restoreTrash(
  ids: string[],
  mode: 'ask' | 'rename' = 'ask',
): Promise<RestoreResponse> {
  return requestJson<RestoreResponse>('/api/trash/restore', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids, mode }),
  });
}

/** 彻底删除指定回收站条目，或清空整个回收站。 */
export function emptyTrash(
  options: { ids: string[] } | { all: true },
): Promise<EmptyTrashResponse> {
  return requestJson<EmptyTrashResponse>('/api/trash/empty', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(options),
  });
}

/** 在指定目录下新建单层文件夹（目录选择器里的“新建文件夹”）。 */
export function createFolder(dir: string, name: string): Promise<{ ok: boolean; path: string }> {
  return requestJson<{ ok: boolean; path: string }>('/api/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ dir, kind: 'dir', name }),
  });
}

/** 目录列表相关的类型与请求函数。字段与服务端 internal/server/list.go 保持一致。 */
import { requestJson } from './client';

export const DEFAULT_LIST_LIMIT = 300;

export type SortKey = 'name' | 'size' | 'mtime';
export type SortOrder = 'asc' | 'desc';

/** /api/list 的单条记录。mtime 为 Unix 秒，dir 的 size 无意义。 */
export interface ListEntry {
  name: string;
  is_dir: boolean;
  size: number;
  mtime: number;
  ext: string;
  kind: string;
}

/** /api/list 响应。total/truncated 在服务端分页时才出现。 */
export interface ListResponse {
  path: string;
  entries: ListEntry[];
  total?: number;
  truncated?: boolean;
}

/** 构建 /api/list 查询串；path 用 encodeURIComponent，空串表示根目录。 */
export function buildListUrl(
  path: string,
  options: { sort?: SortKey; order?: SortOrder; limit?: number; offset?: number } = {},
): string {
  const { sort = 'name', order = 'asc', limit = DEFAULT_LIST_LIMIT, offset } = options;
  const params = new URLSearchParams({
    path,
    sort,
    order,
    limit: String(limit),
  });
  if (offset !== undefined) params.set('offset', String(offset));
  return `/api/list?${params.toString()}`;
}

/** 拉取目录列表。signal 用于切换目录时取消过期请求。 */
export function fetchDirectory(
  path: string,
  options: { sort?: SortKey; order?: SortOrder; limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<ListResponse> {
  return requestJson<ListResponse>(buildListUrl(path, options), { signal });
}

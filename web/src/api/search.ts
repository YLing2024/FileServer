/** 搜索结果类型与请求函数。字段与服务端 internal/server/search.go 保持一致。 */
import { requestJson } from './client';

/** /api/search 的单条结果；path 为相对服务根目录的路径（带前导斜杠）。 */
export interface SearchResult {
  path: string;
  name: string;
  is_dir: boolean;
  size: number;
  mtime: number;
  kind: string;
}

/** /api/search 响应。 */
export interface SearchResponse {
  query: string;
  results: SearchResult[];
  truncated?: boolean;
}

/** 搜索上限：服务端 /api/info 的 search_limit 会覆盖此默认值。 */
export const DEFAULT_SEARCH_LIMIT = 1000;

/** 构建 /api/search 查询串；q 与 path 都经过 URL 编码。 */
export function buildSearchUrl(query: string, path: string, limit = DEFAULT_SEARCH_LIMIT): string {
  const params = new URLSearchParams({ q: query, path, limit: String(limit) });
  return `/api/search?${params.toString()}`;
}

/** 在指定目录下递归搜索；signal 用于输入变化时取消过期请求。 */
export function fetchSearch(
  query: string,
  path: string,
  limit = DEFAULT_SEARCH_LIMIT,
  signal?: AbortSignal,
): Promise<SearchResponse> {
  return requestJson<SearchResponse>(buildSearchUrl(query, path, limit), { signal });
}

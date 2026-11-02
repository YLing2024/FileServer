/** 搜索数据 hook：防抖后的关键字变化才发请求，路径变化时重新搜索。 */
import { useCallback, useEffect, useState } from 'react';
import { errorMessage } from '../../api/errors';
import type { ListEntry } from '../../api/list';
import { DEFAULT_SEARCH_LIMIT, fetchSearch, type SearchResult } from '../../api/search';
import { normalizePath, parentPath } from '../../domain/path';

/** 搜索结果在列表/网格里复用的条目类型。 */
export interface SearchEntry extends ListEntry {
  /** 相对服务根目录的真实路径（不带前导斜杠）。 */
  searchPath: string;
  /** 文件所在目录，点击目录结果时跳转到这里。 */
  searchDir: string;
}

interface SearchState {
  entries: SearchEntry[];
  loading: boolean;
  error: string;
  truncated: boolean;
}

const IDLE_STATE: SearchState = { entries: [], loading: false, error: '', truncated: false };

function toEntry(result: SearchResult): SearchEntry {
  const searchPath = normalizePath(result.path);
  return {
    name: result.name,
    is_dir: result.is_dir,
    size: result.size,
    mtime: result.mtime,
    ext: '',
    kind: result.kind,
    searchPath,
    searchDir: parentPath(searchPath),
  };
}

/** 在 path 下递归搜索 query；query 为空时返回空闲状态。 */
export function useSearch(query: string, path: string, limit = DEFAULT_SEARCH_LIMIT) {
  const [state, setState] = useState<SearchState>(IDLE_STATE);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    if (!query) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- 清空搜索必须同步回到空闲态
      setState(IDLE_STATE);
      return;
    }

    const controller = new AbortController();
    setState({ entries: [], loading: true, error: '', truncated: false });

    fetchSearch(query, path, limit, controller.signal)
      .then((response) => {
        if (controller.signal.aborted) return;
        setState({
          entries: response.results.map(toEntry),
          loading: false,
          error: '',
          truncated: Boolean(response.truncated),
        });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof DOMException && error.name === 'AbortError') return;
        setState({
          entries: [],
          loading: false,
          error: errorMessage(error, '搜索失败'),
          truncated: false,
        });
      });

    return () => controller.abort();
  }, [query, path, limit, reloadToken]);

  const reload = useCallback(() => setReloadToken((value) => value + 1), []);

  return { ...state, reload };
}

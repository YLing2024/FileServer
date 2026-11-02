/** 目录数据 hook：分页拉取 /api/list，切换路径/排序时丢弃过期响应。 */
import { useCallback, useEffect, useRef, useState } from 'react';
import { errorMessage } from '../../api/errors';
import {
  DEFAULT_LIST_LIMIT,
  fetchDirectory,
  type ListEntry,
  type SortKey,
  type SortOrder,
} from '../../api/list';

interface DirectoryState {
  entries: ListEntry[];
  total: number | undefined;
  truncated: boolean;
  loading: boolean;
  loadingMore: boolean;
  error: string;
}

const EMPTY_STATE: DirectoryState = {
  entries: [],
  total: undefined,
  truncated: false,
  loading: true,
  loadingMore: false,
  error: '',
};

/** 目录浏览数据：首页加载、加载更多、错误与刷新。 */
export function useDirectory(
  path: string,
  sort: SortKey,
  order: SortOrder,
  limit = DEFAULT_LIST_LIMIT,
) {
  const [state, setState] = useState<DirectoryState>(EMPTY_STATE);
  const [reloadToken, setReloadToken] = useState(0);
  const seq = useRef(0);
  const loadingMoreRef = useRef(false);

  useEffect(() => {
    const controller = new AbortController();
    const current = ++seq.current;
    loadingMoreRef.current = false;
    // 切换目录/排序立即进入加载态，避免把旧目录条目误显示成新目录内容。
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 有意的加载态切换
    setState(EMPTY_STATE);

    fetchDirectory(path, { sort, order, limit }, controller.signal)
      .then((response) => {
        if (current !== seq.current || controller.signal.aborted) return;
        setState({
          entries: response.entries ?? [],
          total: response.total,
          truncated: Boolean(response.truncated),
          loading: false,
          loadingMore: false,
          error: '',
        });
      })
      .catch((error: unknown) => {
        if (current !== seq.current || controller.signal.aborted) return;
        if (error instanceof DOMException && error.name === 'AbortError') return;
        setState((currentState) => ({
          ...currentState,
          entries: [],
          loading: false,
          loadingMore: false,
          error: errorMessage(error, '加载目录失败'),
        }));
      });

    return () => controller.abort();
  }, [path, sort, order, limit, reloadToken]);

  const reload = useCallback(() => setReloadToken((value) => value + 1), []);

  const loadMore = useCallback(() => {
    if (loadingMoreRef.current || state.loading || !state.truncated) return;
    loadingMoreRef.current = true;
    const current = seq.current;
    const offset = state.entries.length;
    setState((currentState) => ({ ...currentState, loadingMore: true, error: '' }));

    fetchDirectory(path, { sort, order, limit, offset })
      .then((response) => {
        if (current !== seq.current) return;
        setState((currentState) => ({
          ...currentState,
          entries: [...currentState.entries, ...(response.entries ?? [])],
          total: response.total ?? currentState.total,
          truncated: Boolean(response.truncated),
          loadingMore: false,
        }));
      })
      .catch((error: unknown) => {
        if (current !== seq.current) return;
        setState((currentState) => ({
          ...currentState,
          loadingMore: false,
          error: errorMessage(error, '加载更多失败'),
        }));
      })
      .finally(() => {
        loadingMoreRef.current = false;
      });
  }, [path, sort, order, limit, state.entries.length, state.loading, state.truncated]);

  return { ...state, reload, loadMore };
}

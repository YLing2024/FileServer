/** 目录数据 hook：按 path 拉取 /api/list，切换路径时取消过期请求。 */
import { useCallback, useEffect, useState } from 'react';
import { ApiError } from '../../api/errors';
import { fetchDirectory, type ListEntry } from '../../api/list';

interface DirectoryState {
  entries: ListEntry[];
  total: number | undefined;
  truncated: boolean;
  loading: boolean;
  error: string;
}

const EMPTY_STATE: DirectoryState = {
  entries: [],
  total: undefined,
  truncated: false,
  loading: true,
  error: '',
};

/** 返回目录条目、加载状态、错误文案与手动刷新函数。 */
export function useDirectory(path: string) {
  const [state, setState] = useState<DirectoryState>(EMPTY_STATE);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    // 切换目录立即进入加载态，避免把旧目录条目误显示成新目录内容。
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 该同步 setState 是有意的加载态切换
    setState((current) => ({ ...current, loading: true, error: '' }));

    fetchDirectory(path, {}, controller.signal)
      .then((response) => {
        if (controller.signal.aborted) return;
        setState({
          entries: response.entries ?? [],
          total: response.total,
          truncated: Boolean(response.truncated),
          loading: false,
          error: '',
        });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof DOMException && error.name === 'AbortError') return;
        const message = error instanceof ApiError ? error.message : '加载目录失败';
        setState((current) => ({ ...current, loading: false, error: message }));
      });

    return () => controller.abort();
  }, [path, reloadToken]);

  const reload = useCallback(() => setReloadToken((value) => value + 1), []);

  return { ...state, reload };
}

/** 从当前 URL 解析 ?path=；非法值按空路径处理。 */
export function readPathFromUrl(search = window.location.search): string {
  const raw = new URLSearchParams(search).get('path') ?? '';
  return raw.replace(/^\/+|\/+$/g, '');
}

/** 让 ?path= 与状态双向同步；pushState 保留浏览器前进/后退。 */
export function useUrlPath(): [string, (next: string) => void] {
  const [path, setPath] = useState(() => readPathFromUrl());

  useEffect(() => {
    const onPopState = (): void => setPath(readPathFromUrl());
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

  const navigate = useCallback((next: string) => {
    const normalized = next.replace(/^\/+|\/+$/g, '');
    const url = new URL(window.location.href);
    url.searchParams.set('path', normalized);
    window.history.pushState({ path: normalized }, '', url);
    setPath(normalized);
  }, []);

  return [path, navigate];
}

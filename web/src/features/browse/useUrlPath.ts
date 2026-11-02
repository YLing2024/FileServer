/** URL 路径同步：?path= 是浏览位置的唯一真源，浏览器前进/后退可逐级回退。 */
import { useCallback, useEffect, useState } from 'react';
import { normalizePath } from '../../domain/path';

/** 从 URL 查询串解析 ?path=；非法值按空路径处理。 */
export function readPathFromUrl(search = window.location.search): string {
  return normalizePath(new URLSearchParams(search).get('path') ?? '');
}

/** 让 ?path= 与状态双向同步；pushState 保留浏览器历史。 */
export function useUrlPath(): [string, (next: string) => void] {
  const [path, setPath] = useState(() => readPathFromUrl());

  useEffect(() => {
    const onPopState = (): void => setPath(readPathFromUrl());
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

  const navigate = useCallback((next: string) => {
    const normalized = normalizePath(next);
    const url = new URL(window.location.href);
    if (normalized) url.searchParams.set('path', normalized);
    else url.searchParams.delete('path');
    window.history.pushState({ path: normalized }, '', url);
    setPath(normalized);
  }, []);

  return [path, navigate];
}

/**
 * 浏览/预览路由：`?path=` 是目录位置，`?view=` 是预览目标。
 *
 * 两者共用浏览器历史：
 * - 进入预览时 push 一条只带 `?view=` 的历史记录（与旧前端一致）；
 * - 返回/前进/后退由 popstate 恢复，刷新时按 URL 重建；
 * - 直接打开深链（没有站内历史）时 Esc 原地 replaceState 回目录，而不是离开站点。
 */
import { useCallback, useEffect, useState } from 'react';
import { dirname, normalizePath } from '../../domain/path';

/** 当前路由：目录路径（不带前导斜杠）与预览目标（可能为 null）。 */
export interface BrowseRoute {
  path: string;
  view: string | null;
}

interface HistoryState {
  fsView?: string;
  path?: string;
}

/** 从查询串解析路由；只有 view 时目录取文件所在目录，保证面包屑/灯箱有上下文。 */
export function readBrowseRoute(search = window.location.search): BrowseRoute {
  const params = new URLSearchParams(search);
  const rawView = params.get('view');
  const view = rawView ? normalizePath(rawView) : null;
  const rawPath = params.get('path');
  const path = normalizePath(rawPath ?? (view ? dirname(view) : ''));
  return { path, view };
}

function currentState(): HistoryState | null {
  const state: unknown = window.history.state;
  if (typeof state !== 'object' || state === null) return null;
  return state as HistoryState;
}

function buildUrl(options: { path?: string; view?: string | null }): URL {
  const url = new URL(window.location.href);
  url.search = '';
  if (options.path) url.searchParams.set('path', options.path);
  if (options.view) url.searchParams.set('view', options.view);
  return url;
}

/** 路由 hook：目录导航、进入/替换/退出预览。 */
export function useBrowseRoute(): {
  path: string;
  view: string | null;
  navigate: (next: string) => void;
  openView: (target: string) => void;
  replaceView: (target: string) => void;
  closeView: () => void;
} {
  const [route, setRoute] = useState<BrowseRoute>(() => readBrowseRoute());

  useEffect(() => {
    const onPopState = (): void => setRoute(readBrowseRoute());
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

  const navigate = useCallback((next: string) => {
    const path = normalizePath(next);
    window.history.pushState({ path }, '', buildUrl({ path }));
    setRoute({ path, view: null });
  }, []);

  const openView = useCallback((target: string) => {
    const view = normalizePath(target);
    const path = readBrowseRoute().path;
    window.history.pushState({ path, fsView: view }, '', buildUrl({ view }));
    setRoute({ path, view });
  }, []);

  const replaceView = useCallback((target: string) => {
    const view = normalizePath(target);
    const state = currentState();
    // 深链进入时 history.state 为空：保持为空，退出时才会走 replace 而不是离开站点。
    const nextState: HistoryState | null = state ? { ...state, fsView: view } : null;
    window.history.replaceState(nextState, '', buildUrl({ view }));
    setRoute((current) => ({ ...current, view }));
  }, []);

  const closeView = useCallback(() => {
    const path = readBrowseRoute().path;
    if (currentState()?.fsView) {
      window.history.back();
      return;
    }
    window.history.replaceState({ path }, '', buildUrl({ path }));
    setRoute({ path, view: null });
  }, []);

  return { ...route, navigate, openView, replaceView, closeView };
}

/**
 * 浏览页：工具栏 + 网格/列表 + 搜索 + 分页。
 *
 * 数据获取全部在 hooks（useDirectory / useSearch / useCapabilities），组件只负责
 * 展示与事件分发。可预览的文件进入 `?view=`，关闭时由路由恢复目录与滚动位置；
 * 其余类型直接下载。
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { errorMessage } from '../../api/errors';
import type { SortKey, SortOrder } from '../../api/list';
import { downloadFile } from '../../api/media';
import { useToast } from '../../components/Toast';
import { kindOf, kindOfEntry } from '../../domain/fileType';
import { basename, joinPath, normalizePath } from '../../domain/path';
import { isSortKey, isSortOrder, sortEntries } from '../../domain/sort';
import { PreviewPage, type PreviewFile } from '../preview/PreviewPage';
import { GridSkeleton, GridView } from './GridView';
import { ListSkeleton, ListView } from './ListView';
import { Toolbar, type ViewMode } from './Toolbar';
import { useCapabilities } from './useCapabilities';
import { useDebouncedValue } from './useDebouncedValue';
import { useDirectory } from './useDirectory';
import { usePersistentChoice } from './usePersistentChoice';
import { useSearch } from './useSearch';
import { useBrowseRoute } from './useBrowseRoute';
import { clearVideoThumbCache } from './videoThumb';
import type { BrowseEntry } from './types';
import styles from './BrowsePage.module.css';

const SEARCH_DEBOUNCE_MS = 250;
const SCROLL_MEMORY_MAX = 100;

function isViewMode(value: string | null): value is ViewMode {
  return value === 'grid' || value === 'list';
}

function EmptyState({ searching }: { searching: boolean }) {
  return (
    <div className={styles.empty} data-testid="empty">
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z" />
      </svg>
      <p>{searching ? '没有匹配的文件' : '此文件夹为空'}</p>
    </div>
  );
}

/** 目录浏览页；路径由 ?path= 驱动，视图/排序偏好持久化到 localStorage。 */
export function BrowsePage() {
  const { showToast } = useToast();
  const {
    path,
    view: previewView,
    navigate: navigateRaw,
    openView,
    replaceView,
    closeView,
  } = useBrowseRoute();
  const [query, setQuery] = useState('');
  const [view, setView] = usePersistentChoice<ViewMode>('fs.view', 'grid', isViewMode);
  const [sort, setSort] = usePersistentChoice<SortKey>('fs.sort', 'name', isSortKey);
  const [order, setOrder] = usePersistentChoice<SortOrder>('fs.order', 'asc', isSortOrder);

  const trimmedQuery = query.trim();
  const debouncedQuery = useDebouncedValue(trimmedQuery, SEARCH_DEBOUNCE_MS);
  // 清空输入立即回到浏览态，输入过程中才等待防抖。
  const activeQuery = trimmedQuery === '' ? '' : debouncedQuery;
  const searching = activeQuery !== '';

  const capabilities = useCapabilities();
  // 单页固定 300（旧前端 PAGE），服务端的 list_limit 是上限而不是页大小。
  const directory = useDirectory(path, sort, order);
  const search = useSearch(activeQuery, path, capabilities.searchLimit);
  const scrollPositions = useRef(new Map<string, number>());
  const loadMoreRef = useRef<HTMLDivElement | null>(null);

  const browseEntries = useMemo<BrowseEntry[]>(
    () =>
      sortEntries(directory.entries, sort, order).map((entry) => ({
        ...entry,
        fullPath: joinPath(path, entry.name),
      })),
    [directory.entries, sort, order, path],
  );

  const searchEntries = useMemo<BrowseEntry[]>(
    () =>
      sortEntries(search.entries, sort, order).map((entry) => ({
        ...entry,
        fullPath: entry.searchPath,
      })),
    [search.entries, sort, order],
  );

  const entries = searching ? searchEntries : browseEntries;
  const loading = searching ? search.loading : directory.loading;
  const error = searching ? search.error : directory.error;
  const retry = searching ? search.reload : directory.reload;

  const navigate = useCallback(
    (next: string) => {
      scrollPositions.current.set(path, window.scrollY);
      if (scrollPositions.current.size > SCROLL_MEMORY_MAX) {
        const oldest = scrollPositions.current.keys().next().value;
        if (oldest !== undefined) scrollPositions.current.delete(oldest);
      }
      setQuery('');
      navigateRaw(normalizePath(next));
    },
    [navigateRaw, path],
  );

  const handleOpen = useCallback(
    (entry: BrowseEntry) => {
      if (entry.is_dir) {
        navigate(entry.searchDir ?? entry.fullPath);
        return;
      }
      const kind = kindOfEntry(entry);
      if (
        kind === 'image' ||
        kind === 'video' ||
        kind === 'audio' ||
        kind === 'pdf' ||
        kind === 'text' ||
        kind === 'code'
      ) {
        // 记录列表滚动位置必须在切换视图前完成；预览关闭后由此恢复。
        scrollPositions.current.set(path, window.scrollY);
        openView(entry.fullPath);
        return;
      }
      void downloadFile(entry.fullPath, entry.name).catch(() => {
        showToast('下载失败', 'error');
      });
    },
    [navigate, openView, path, showToast],
  );

  const handleRefresh = useCallback(() => {
    if (searching) search.reload();
    else directory.reload();
  }, [searching, search, directory]);

  const handleClearSearch = useCallback(() => setQuery(''), []);

  const handleToggleHls = useCallback(async () => {
    const turningOn = !capabilities.hls;
    try {
      await capabilities.toggleHls();
      clearVideoThumbCache();
      directory.reload();
      if (searching) search.reload();
      showToast(turningOn ? '冷门格式支持已开启' : '冷门格式支持已关闭');
    } catch (toggleError: unknown) {
      showToast(errorMessage(toggleError, '切换冷门格式失败'), 'error');
    }
  }, [capabilities, directory, search, searching, showToast]);

  useEffect(() => {
    if (previewView || loading || searching) return;
    const saved = scrollPositions.current.get(path) ?? 0;
    const frame = window.requestAnimationFrame(() => window.scrollTo(0, saved));
    return () => window.cancelAnimationFrame(frame);
  }, [path, loading, searching, previewView]);

  useEffect(() => {
    if (!searching) return;
    window.scrollTo(0, 0);
  }, [searching]);

  const { loadMore } = directory;

  useEffect(() => {
    if (searching || !directory.truncated || directory.loading || directory.loadingMore) return;
    const element = loadMoreRef.current;
    if (!element || typeof IntersectionObserver === 'undefined') return;
    const observer = new IntersectionObserver(
      (items) => {
        if (items.some((item) => item.isIntersecting)) loadMore();
      },
      { rootMargin: '300px' },
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [
    searching,
    directory.truncated,
    directory.loading,
    directory.loadingMore,
    directory.entries.length,
    loadMore,
  ]);

  const previewFile = useMemo<PreviewFile | null>(() => {
    if (!previewView) return null;
    const match =
      browseEntries.find((entry) => entry.fullPath === previewView) ??
      searchEntries.find((entry) => entry.fullPath === previewView);
    return {
      path: previewView,
      name: match?.name ?? basename(previewView),
      size: match?.size ?? -1,
      mtime: match?.mtime ?? 0,
      kind: match ? kindOfEntry(match) : kindOf(basename(previewView), false),
    };
  }, [previewView, browseEntries, searchEntries]);

  const previewImages = useMemo(() => {
    const seen = new Set<string>();
    const paths: string[] = [];
    for (const entry of [...browseEntries, ...searchEntries]) {
      if (entry.is_dir || kindOfEntry(entry) !== 'image' || seen.has(entry.fullPath)) continue;
      seen.add(entry.fullPath);
      paths.push(entry.fullPath);
    }
    return paths;
  }, [browseEntries, searchEntries]);

  if (previewView && previewFile) {
    return (
      <PreviewPage
        file={previewFile}
        images={previewImages}
        onClose={closeView}
        onChangeImage={replaceView}
      />
    );
  }

  const meta = loading
    ? '正在加载…'
    : searching
      ? `搜索“${activeQuery}”：${entries.length} 项${search.truncated ? '（结果过多，仅显示前若干项）' : ''}`
      : `共 ${directory.total ?? entries.length} 项${
          directory.truncated ? `（已加载 ${entries.length} 项，还有更多）` : ''
        }`;

  const currentLabel = path ? `/${path}` : '/';
  const retryLabel = searching ? '重新搜索' : '重试';

  return (
    <>
      <Toolbar
        path={path}
        view={view}
        sort={sort}
        order={order}
        query={query}
        searching={searching}
        hls={capabilities.hls}
        ffmpeg={capabilities.ffmpeg}
        hlsBusy={capabilities.toggling}
        onHome={() => navigate('')}
        onNavigate={navigate}
        onViewChange={setView}
        onSortChange={setSort}
        onOrderToggle={() => setOrder(order === 'asc' ? 'desc' : 'asc')}
        onQueryChange={setQuery}
        onClearSearch={handleClearSearch}
        onRefresh={handleRefresh}
        onToggleHls={() => {
          void handleToggleHls();
        }}
      />

      <main className={styles.main}>
        <div className={styles.head}>
          <h1 className={styles.title}>{currentLabel}</h1>
          <p className={styles.meta}>{meta}</p>
        </div>

        {error ? (
          <div className={styles.error} role="alert">
            <p className={styles.errorText}>{error}</p>
            <button type="button" className={styles.action} onClick={retry}>
              {retryLabel}
            </button>
          </div>
        ) : null}

        {loading ? view === 'grid' ? <GridSkeleton /> : <ListSkeleton /> : null}

        {!loading && !error && entries.length === 0 ? <EmptyState searching={searching} /> : null}

        {!loading && entries.length > 0 ? (
          view === 'grid' ? (
            <GridView
              key={capabilities.hls ? 'hls' : 'no-hls'}
              entries={entries}
              query={searching ? activeQuery : ''}
              serverTranscode={capabilities.hls}
              onOpen={handleOpen}
            />
          ) : (
            <ListView entries={entries} query={searching ? activeQuery : ''} onOpen={handleOpen} />
          )
        ) : null}

        {!searching && directory.truncated ? (
          <div className={styles.loadMore}>
            <button type="button" onClick={directory.loadMore} disabled={directory.loadingMore}>
              {directory.loadingMore ? '正在加载…' : '加载更多'}
            </button>
            <span>
              已加载 {entries.length} 项
              {directory.total !== undefined ? ` / 共 ${directory.total} 项` : ''}
            </span>
            <div ref={loadMoreRef} className={styles.sentinel} aria-hidden="true" />
          </div>
        ) : null}
      </main>
    </>
  );
}

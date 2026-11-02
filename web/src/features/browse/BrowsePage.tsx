/**
 * 浏览页：工具栏 + 网格/列表 + 搜索 + 分页。
 *
 * 数据获取全部在 hooks（useDirectory / useSearch / useCapabilities），组件只负责
 * 展示与事件分发。可预览的文件进入 `?view=`，关闭时由路由恢复目录与滚动位置；
 * 其余类型直接下载。
 */
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type MouseEvent as ReactMouseEvent,
} from 'react';
import { errorMessage } from '../../api/errors';
import type { SortKey, SortOrder } from '../../api/list';
import { deleteEntries, type ManageFailure } from '../../api/manage';
import { downloadFile, downloadZip } from '../../api/media';
import { useToast } from '../../components/Toast';
import { kindOf, kindOfEntry } from '../../domain/fileType';
import { basename, joinPath, normalizePath } from '../../domain/path';
import { isSortKey, isSortOrder, sortEntries } from '../../domain/sort';
import { FailuresDialog } from '../manage/FailuresDialog';
import { ConfirmDialog } from '../manage/ConfirmDialog';
import { RenameDialog } from '../manage/RenameDialog';
import { TransferDialog } from '../manage/TransferDialog';
import { TrashDialog } from '../manage/TrashDialog';
import { manageErrorMessage } from '../manage/messages';
import { PreviewPage, type PreviewFile } from '../preview/PreviewPage';
import { collectFolderFiles, supportsDirectoryPicker } from '../upload/collect';
import { DropOverlay } from '../upload/DropOverlay';
import { UploadConflictDialog } from '../upload/UploadConflictDialog';
import { UploadPanel } from '../upload/UploadPanel';
import { usePageDrop, usePasteUpload } from '../upload/pageDrop';
import { useUploadQueue } from '../upload/useUploadQueue';
import { GridSkeleton, GridView } from './GridView';
import { ListSkeleton, ListView } from './ListView';
import { SelectionBar } from './SelectionBar';
import { Toolbar, type ViewMode } from './Toolbar';
import { useCapabilities } from './useCapabilities';
import { useDebouncedValue } from './useDebouncedValue';
import { useDirectory } from './useDirectory';
import { usePersistentChoice } from './usePersistentChoice';
import { useSearch } from './useSearch';
import { useBrowseRoute } from './useBrowseRoute';
import { useSelection } from './useSelection';
import { clearVideoThumbCache } from './videoThumb';
import type { BrowseEntry, EntryInteraction } from './types';
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

  const pathRef = useRef(path);
  useEffect(() => {
    pathRef.current = path;
  }, [path]);
  const [renameTarget, setRenameTarget] = useState<BrowseEntry | null>(null);
  const [transfer, setTransfer] = useState<{ kind: 'move' | 'copy'; paths: string[] } | null>(null);
  const [trashOpen, setTrashOpen] = useState(false);
  const [deleteTargets, setDeleteTargets] = useState<string[] | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [failures, setFailures] = useState<{ title: string; failed: ManageFailure[] } | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const folderInputRef = useRef<HTMLInputElement | null>(null);
  const folderPickerSupported = useMemo(() => supportsDirectoryPicker(), []);

  const refreshCurrent = useCallback(() => {
    if (searching) search.reload();
    else directory.reload();
  }, [directory, search, searching]);

  const selection = useSelection(entries, capabilities.manage, path);

  const upload = useUploadQueue({
    settings: {
      manage: capabilities.manage,
      maxBytes: capabilities.uploadMaxBytes,
      chunkThreshold: capabilities.chunkThreshold,
      chunkMax: capabilities.chunkMax,
      maxFiles: 2000,
    },
    onFinished: (target, names) => {
      if (!names.length) return;
      if (target === pathRef.current) {
        refreshCurrent();
        showToast(`已上传 ${names.length} 项`);
      } else {
        showToast(`已上传 ${names.length} 项到 /${target}`);
      }
    },
    onToast: (message, tone) => showToast(message, tone === 'error' ? 'error' : 'info'),
    onUnauthorized: () => {
      // 401 的清理与跳转已由 api/client 的全局回调完成。
    },
  });

  const dropEnabled =
    capabilities.uploadAllowed && !previewView && upload.snapshot.conflict === null;
  usePasteUpload(dropEnabled, (files) => upload.enqueuePlain(files, pathRef.current));
  const dragging = usePageDrop({
    enabled: dropEnabled,
    onCollected: (collected) => upload.enqueue(collected, pathRef.current),
    onError: (message) => showToast(message, 'error'),
  });


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

  const selectedEntries = useMemo(
    () => entries.filter((entry) => selection.selected.has(entry.fullPath)),
    [entries, selection.selected],
  );

  const handleEntryClick = useCallback(
    (entry: BrowseEntry, event: ReactMouseEvent<HTMLElement>) => {
      if (selection.handleClick(entry, event) === 'open') handleOpen(entry);
    },
    [handleOpen, selection],
  );

  const handleEntryDoubleClick = useCallback(
    (entry: BrowseEntry) => {
      if (selection.handleDoubleClick(entry) === 'open') handleOpen(entry);
    },
    [handleOpen, selection],
  );

  const interaction: EntryInteraction = {
    selected: selection.selected,
    onClick: handleEntryClick,
    onDoubleClick: handleEntryDoubleClick,
    onPointerDown: selection.handlePointerDown,
    onPointerMove: selection.handlePointerMove,
    onPointerUp: selection.handlePointerUp,
  };

  const handleDownload = useCallback(async () => {
    const targets = entries.filter((entry) => selection.selected.has(entry.fullPath));
    if (!targets.length) return;
    for (const entry of targets) {
      try {
        if (entry.is_dir) await downloadZip(entry.fullPath, entry.name);
        else await downloadFile(entry.fullPath, entry.name);
      } catch {
        showToast('下载失败', 'error');
      }
    }
  }, [entries, selection.selected, showToast]);

  const handleRenamed = useCallback(
    (newPath: string) => {
      setRenameTarget(null);
      selection.setOnly(newPath);
      refreshCurrent();
      showToast('已重命名');
    },
    [refreshCurrent, selection, showToast],
  );

  const confirmDelete = useCallback(async () => {
    if (!deleteTargets?.length || deleting) return;
    setDeleting(true);
    try {
      const result = await deleteEntries(deleteTargets);
      setDeleteTargets(null);
      selection.clear();
      refreshCurrent();
      const failed = result.failed ?? [];
      if (failed.length) setFailures({ title: '部分项目未能删除', failed });
      showToast(
        failed.length
          ? `已删除 ${result.moved?.length ?? 0} 项，${failed.length} 项失败`
          : `已移入回收站 ${result.moved?.length ?? 0} 项`,
        failed.length ? 'error' : 'info',
      );
    } catch (deleteError) {
      showToast(manageErrorMessage(deleteError, '删除失败'), 'error');
    } finally {
      setDeleting(false);
    }
  }, [deleteTargets, deleting, refreshCurrent, selection, showToast]);

  const handleTransferDone = useCallback(
    (result: {
      kind: 'move' | 'copy';
      done: { from: string; to: string }[];
      failed: ManageFailure[];
    }) => {
      setTransfer(null);
      selection.clear();
      refreshCurrent();
      if (result.failed.length) {
        setFailures({ title: '部分项目未能完成', failed: result.failed });
      }
      const verb = result.kind === 'move' ? '已移动' : '已复制';
      showToast(
        result.failed.length
          ? `完成 ${result.done.length} 项，${result.failed.length} 项失败`
          : `${verb} ${result.done.length} 项`,
        result.failed.length ? 'error' : 'info',
      );
    },
    [refreshCurrent, selection, showToast],
  );

  const handleFilePick = useCallback(
    (event: ChangeEvent<HTMLInputElement>) => {
      const files = Array.from(event.target.files ?? []);
      if (files.length) upload.enqueuePlain(files, pathRef.current);
      event.target.value = '';
    },
    [upload],
  );

  const handleFolderPick = useCallback(
    (event: ChangeEvent<HTMLInputElement>) => {
      const files = Array.from(event.target.files ?? []);
      if (files.length) upload.enqueue(collectFolderFiles(files), pathRef.current);
      event.target.value = '';
    },
    [upload],
  );

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent): void => {
      const target = event.target;
      if (
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        target instanceof HTMLSelectElement ||
        (target instanceof HTMLElement && target.isContentEditable)
      ) {
        return;
      }
      if (!capabilities.manage || previewView) return;
      if (event.key === 'Escape' && selection.count > 0) {
        selection.clear();
        return;
      }
      if ((event.ctrlKey || event.metaKey) && (event.key === 'a' || event.key === 'A')) {
        event.preventDefault();
        selection.selectAll();
        return;
      }
      if (event.key === 'Delete' && selection.count > 0) {
        event.preventDefault();
        setDeleteTargets([...selection.selected]);
        return;
      }
      if (event.key === 'F2' && selection.count === 1) {
        const only = [...selection.selected][0];
        const match = entries.find((entry) => entry.fullPath === only);
        if (match) {
          event.preventDefault();
          setRenameTarget(match);
        }
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [capabilities.manage, entries, previewView, selection]);

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
      <>
        <PreviewPage
          file={previewFile}
          images={previewImages}
          onClose={closeView}
          onChangeImage={replaceView}
        />
        <UploadPanel
          snapshot={upload.snapshot}
          onClose={upload.close}
          onPause={upload.pauseItem}
          onResume={upload.resumeItem}
          onCancel={upload.cancelItem}
          onRetry={upload.retryItem}
          onCancelAll={upload.cancelAll}
        />
        {upload.snapshot.conflict ? (
          <UploadConflictDialog
            conflict={upload.snapshot.conflict}
            onResolve={upload.resolveConflict}
          />
        ) : null}
      </>
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
        uploadAllowed={capabilities.uploadAllowed}
        folderUploadAllowed={capabilities.manage && folderPickerSupported}
        manageEnabled={capabilities.manage}
        uploadBusy={upload.snapshot.busy}
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
        onUpload={() => fileInputRef.current?.click()}
        onUploadFolder={() => folderInputRef.current?.click()}
        onTrash={() => setTrashOpen(true)}
      />

      <input
        ref={fileInputRef}
        type="file"
        multiple
        hidden
        aria-hidden="true"
        tabIndex={-1}
        onChange={handleFilePick}
      />
      <input
        ref={(element) => {
          folderInputRef.current = element;
          if (element) {
            element.setAttribute('webkitdirectory', '');
            element.setAttribute('directory', '');
          }
        }}
        type="file"
        multiple
        hidden
        aria-hidden="true"
        tabIndex={-1}
        onChange={handleFolderPick}
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
              interaction={capabilities.manage ? interaction : undefined}
            />
          ) : (
            <ListView
              entries={entries}
              query={searching ? activeQuery : ''}
              onOpen={handleOpen}
              interaction={capabilities.manage ? interaction : undefined}
            />
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

      <SelectionBar
        count={selection.count}
        onDownload={() => {
          void handleDownload();
        }}
        onRename={() => {
          if (selectedEntries.length === 1 && selectedEntries[0]) {
            setRenameTarget(selectedEntries[0]);
          }
        }}
        onMove={() => setTransfer({ kind: 'move', paths: [...selection.selected] })}
        onCopy={() => setTransfer({ kind: 'copy', paths: [...selection.selected] })}
        onDelete={() => setDeleteTargets([...selection.selected])}
        onExit={selection.clear}
      />

      {renameTarget ? (
        <RenameDialog
          path={renameTarget.fullPath}
          name={renameTarget.name}
          isDir={renameTarget.is_dir}
          siblings={entries
            .filter((entry) => entry.fullPath !== renameTarget.fullPath)
            .map((entry) => entry.name)}
          onClose={() => setRenameTarget(null)}
          onRenamed={handleRenamed}
        />
      ) : null}

      {transfer ? (
        <TransferDialog
          kind={transfer.kind}
          paths={transfer.paths}
          currentPath={path}
          onClose={() => setTransfer(null)}
          onDone={handleTransferDone}
        />
      ) : null}

      {trashOpen ? (
        <TrashDialog
          onClose={() => setTrashOpen(false)}
          onRestored={refreshCurrent}
          onFailures={(title, failed) => setFailures({ title, failed })}
        />
      ) : null}

      {deleteTargets ? (
        <ConfirmDialog
          title="删除"
          message={`将 ${deleteTargets.length} 项移入回收站，可恢复。`}
          confirmLabel={deleting ? '处理中…' : '移入回收站'}
          danger
          onCancel={() => {
            if (!deleting) setDeleteTargets(null);
          }}
          onConfirm={() => {
            void confirmDelete();
          }}
        />
      ) : null}

      {failures ? (
        <FailuresDialog
          title={failures.title}
          failed={failures.failed}
          onClose={() => setFailures(null)}
        />
      ) : null}

      <UploadPanel
        snapshot={upload.snapshot}
        onClose={upload.close}
        onPause={upload.pauseItem}
        onResume={upload.resumeItem}
        onCancel={upload.cancelItem}
        onRetry={upload.retryItem}
        onCancelAll={upload.cancelAll}
      />

      {upload.snapshot.conflict ? (
        <UploadConflictDialog
          conflict={upload.snapshot.conflict}
          onResolve={upload.resolveConflict}
        />
      ) : null}

      {dragging ? <DropOverlay /> : null}
    </>
  );
}

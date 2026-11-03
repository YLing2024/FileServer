/** 浏览页工具栏：品牌/回根、面包屑、搜索、刷新、视图、排序、主题、冷门格式。 */
import { useEffect, useRef } from 'react';
import type { SortKey, SortOrder } from '../../api/list';
import { ThemeToggle } from '../../components/ThemeToggle';
import { SORT_LABELS } from '../../domain/sort';
import { pathSegments } from '../../domain/path';
import styles from './Toolbar.module.css';

export type ViewMode = 'grid' | 'list';

interface ToolbarProps {
  path: string;
  view: ViewMode;
  sort: SortKey;
  order: SortOrder;
  query: string;
  searching: boolean;
  hls: boolean;
  ffmpeg: boolean;
  hlsBusy: boolean;
  uploadAllowed: boolean;
  folderUploadAllowed: boolean;
  manageEnabled: boolean;
  uploadBusy: boolean;
  shareAllowed: boolean;
  onHome: () => void;
  onNavigate: (path: string) => void;
  onViewChange: (view: ViewMode) => void;
  onSortChange: (sort: SortKey) => void;
  onOrderToggle: () => void;
  onQueryChange: (value: string) => void;
  onClearSearch: () => void;
  onRefresh: () => void;
  onToggleHls: () => void;
  onUpload: () => void;
  onUploadFolder: () => void;
  /** 新建文件 / 文件夹（仅登录后可用）。 */
  onCreate: () => void;
  onTrash: () => void;
  onOpenShare: () => void;
  onOpenSettings: () => void;
}

function NewEntryIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M6 3h8l4 4v14H6z" />
      <path d="M14 3v4h4" />
      <path d="M12 11v6M9 14h6" />
    </svg>
  );
}

function BrandMark() {
  return (
    <svg className={styles.brandMark} viewBox="0 0 512 512" aria-hidden="true">
      <rect x="128" y="112" width="256" height="288" rx="20" />
      <line x1="128" y1="208" x2="384" y2="208" />
      <line x1="128" y1="304" x2="384" y2="304" />
      <rect x="232" y="148" width="48" height="22" rx="11" className={styles.slot} />
      <rect x="232" y="244" width="48" height="22" rx="11" className={styles.slotActive} />
      <rect x="232" y="340" width="48" height="22" rx="11" className={styles.slot} />
    </svg>
  );
}

function Breadcrumbs({ path, onNavigate }: { path: string; onNavigate: (path: string) => void }) {
  const segments = pathSegments(path);
  return (
    <nav className={styles.crumbs} aria-label="面包屑">
      <button
        type="button"
        className={styles.crumb}
        aria-current={segments.length === 0 ? 'page' : undefined}
        onClick={() => onNavigate('')}
      >
        根目录
      </button>
      {segments.map((segment, index) => {
        const target = segments.slice(0, index + 1).join('/');
        const current = index === segments.length - 1;
        return (
          <span className={styles.crumbGroup} key={target}>
            <span className={styles.sep} aria-hidden="true">
              ›
            </span>
            <button
              type="button"
              className={styles.crumb}
              title={segment}
              aria-current={current ? 'page' : undefined}
              onClick={() => onNavigate(target)}
            >
              {segment}
            </button>
          </span>
        );
      })}
    </nav>
  );
}

function SearchBox({
  query,
  onQueryChange,
  onClearSearch,
}: {
  query: string;
  onQueryChange: (value: string) => void;
  onClearSearch: () => void;
}) {
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent): void => {
      const target = event.target;
      if (event.key !== '/' || event.metaKey || event.ctrlKey || event.altKey) return;
      if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement) return;
      event.preventDefault();
      inputRef.current?.focus();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  return (
    <div className={styles.searchBox}>
      <svg className={styles.searchIcon} viewBox="0 0 24 24" aria-hidden="true">
        <circle cx="11" cy="11" r="7" />
        <path d="M20 20l-3.5-3.5" />
      </svg>
      <input
        ref={inputRef}
        className={styles.searchInput}
        type="text"
        value={query}
        placeholder="搜索文件…"
        aria-label="搜索文件"
        autoComplete="off"
        spellCheck={false}
        onChange={(event) => onQueryChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Escape') {
            event.preventDefault();
            onClearSearch();
          }
        }}
      />
      {query ? (
        <button
          type="button"
          className={styles.searchClear}
          aria-label="清除搜索"
          title="清除搜索"
          onClick={onClearSearch}
        >
          ×
        </button>
      ) : (
        <kbd className={styles.kbd}>/</kbd>
      )}
    </div>
  );
}

function GridIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <rect x="4" y="4" width="7" height="7" rx="1.5" />
      <rect x="13" y="4" width="7" height="7" rx="1.5" />
      <rect x="4" y="13" width="7" height="7" rx="1.5" />
      <rect x="13" y="13" width="7" height="7" rx="1.5" />
    </svg>
  );
}

function ListIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M4 6h16M4 12h16M4 18h16" />
    </svg>
  );
}

function RefreshIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M20 12a8 8 0 1 1-2.34-5.66M20 4v4h-4" />
    </svg>
  );
}

function HlsIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M8 5.5v13l11-6.5z" />
      <path d="M3 8h3M5 6v4M16 3v3M14.5 4.5h3" />
    </svg>
  );
}

function OrderIcon({ order }: { order: SortOrder }) {
  return (
    <svg
      viewBox="0 0 24 24"
      aria-hidden="true"
      className={order === 'desc' ? styles.orderDesc : undefined}
    >
      <path d="M6 20V4m0 0L2.5 7.5M6 4l3.5 3.5M12 6h8M12 12h8M12 18h5" />
    </svg>
  );
}

function UploadIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 20V9m0 0l-4 4m4-4l4 4" />
      <path d="M4 5h16" />
    </svg>
  );
}

function FolderUploadIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z" />
      <path d="M12 16v-4m0 0l-2.5 2.5M12 12l2.5 2.5" />
    </svg>
  );
}

function TrashIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M4 7h16M9 7V5h6v2M6 7l1 13h10l1-13" />
      <path d="M10 11v5M14 11v5" />
    </svg>
  );
}

function ShareIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1.4 1.4" />
      <path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1.4-1.4" />
    </svg>
  );
}

function SettingsIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.9 1.7 1.7 0 0 0-1.6-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.6-1.1 1.7 1.7 0 0 0-.3-1.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.9.3H9a1.7 1.7 0 0 0 1-1.6V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.9V9a1.7 1.7 0 0 0 1.6 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" />
    </svg>
  );
}

/** 工具栏；所有控件都是原生 button/select/input，键盘可达。 */
export function Toolbar({
  path,
  view,
  sort,
  order,
  query,
  searching,
  hls,
  ffmpeg,
  hlsBusy,
  uploadAllowed,
  folderUploadAllowed,
  manageEnabled,
  uploadBusy,
  shareAllowed,
  onHome,
  onNavigate,
  onViewChange,
  onSortChange,
  onOrderToggle,
  onQueryChange,
  onClearSearch,
  onRefresh,
  onToggleHls,
  onUpload,
  onUploadFolder,
  onCreate,
  onTrash,
  onOpenShare,
  onOpenSettings,
}: ToolbarProps) {
  const hlsTitle = !ffmpeg
    ? '冷门格式支持不可用（服务端无 ffmpeg）'
    : hls
      ? '冷门格式支持已开启（MKV/RMVB/HEVC 等可在线播放）— 点击关闭'
      : '冷门格式支持已关闭 — 点击开启';

  return (
    <header className={styles.toolbar}>
      <div className={styles.main}>
        <button
          type="button"
          className={styles.brand}
          aria-label="回到根目录"
          title="回到根目录"
          onClick={onHome}
        >
          <BrandMark />
          <span className={styles.brandName}>文件服务器</span>
        </button>
        <Breadcrumbs path={path} onNavigate={onNavigate} />
      </div>

      <div className={styles.actions}>
        <SearchBox query={query} onQueryChange={onQueryChange} onClearSearch={onClearSearch} />

        <button
          type="button"
          className={styles.iconButton}
          aria-label="刷新"
          title="刷新"
          onClick={onRefresh}
        >
          <RefreshIcon />
        </button>

        {uploadAllowed ? (
          <button
            type="button"
            className={styles.iconButton}
            aria-label="上传文件"
            title={uploadBusy ? '上传进行中，可继续添加到当前目录' : '上传文件到当前目录'}
            onClick={onUpload}
            data-testid="toolbar-upload"
          >
            <UploadIcon />
          </button>
        ) : null}

        {uploadAllowed && folderUploadAllowed ? (
          <button
            type="button"
            className={styles.iconButton}
            aria-label="上传文件夹"
            title="上传文件夹到当前目录"
            onClick={onUploadFolder}
            data-testid="toolbar-upload-folder"
          >
            <FolderUploadIcon />
          </button>
        ) : null}

        {manageEnabled ? (
          <button
            type="button"
            className={styles.iconButton}
            aria-label="新建文件或文件夹"
            title="新建文件 / 文件夹"
            onClick={onCreate}
            data-testid="toolbar-create"
          >
            <NewEntryIcon />
          </button>
        ) : null}

        {manageEnabled ? (
          <button
            type="button"
            className={styles.iconButton}
            aria-label="回收站"
            title="回收站"
            onClick={onTrash}
            data-testid="toolbar-trash"
          >
            <TrashIcon />
          </button>
        ) : null}

        {shareAllowed ? (
          <button
            type="button"
            className={styles.iconButton}
            aria-label="分享链接"
            title="分享链接管理"
            onClick={onOpenShare}
            data-testid="toolbar-share"
          >
            <ShareIcon />
          </button>
        ) : null}

        <button
          type="button"
          className={`${styles.iconButton} ${hls ? styles.active : ''}`}
          aria-label="冷门格式开关"
          aria-pressed={hls}
          title={hlsTitle}
          disabled={!ffmpeg || hlsBusy}
          onClick={onToggleHls}
        >
          <HlsIcon />
        </button>

        <div className={styles.seg} role="group" aria-label="视图切换">
          <button
            type="button"
            className={`${styles.segButton} ${view === 'grid' ? styles.active : ''}`}
            aria-label="网格视图"
            aria-pressed={view === 'grid'}
            title="网格视图"
            onClick={() => onViewChange('grid')}
          >
            <GridIcon />
          </button>
          <button
            type="button"
            className={`${styles.segButton} ${view === 'list' ? styles.active : ''}`}
            aria-label="列表视图"
            aria-pressed={view === 'list'}
            title="列表视图"
            onClick={() => onViewChange('list')}
          >
            <ListIcon />
          </button>
        </div>

        <select
          className={styles.sortSelect}
          aria-label="排序方式"
          title="排序方式"
          value={sort}
          onChange={(event) => onSortChange(event.target.value as SortKey)}
        >
          <option value="name">{SORT_LABELS.name}</option>
          <option value="time">{SORT_LABELS.time}</option>
          <option value="size">{SORT_LABELS.size}</option>
        </select>

        <button
          type="button"
          className={styles.iconButton}
          aria-label={order === 'asc' ? '升序，点击切换为降序' : '降序，点击切换为升序'}
          title={order === 'asc' ? '升序' : '降序'}
          onClick={onOrderToggle}
        >
          <OrderIcon order={order} />
        </button>

        <ThemeToggle />
        <button
          type="button"
          className={styles.iconButton}
          aria-label="设置"
          title="设置与关于"
          onClick={onOpenSettings}
          data-testid="toolbar-settings"
        >
          <SettingsIcon />
        </button>
        {searching ? <span className={styles.srOnly}>搜索中</span> : null}
      </div>
    </header>
  );
}

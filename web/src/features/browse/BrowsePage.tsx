/**
 * Phase 0 的最小目录浏览页。
 *
 * 功能边界：只读列出 /api/list 的名称/大小/修改时间；目录可点击进入，
 * ?path= 与 URL 同步。预览、排序、搜索、分页留给后续阶段，避免在骨架期
 * 引入半成品交互。
 */
import { useMemo } from 'react';
import { formatBytes, formatTime } from '../../domain/format';
import { joinPath, parentPath, pathSegments } from '../../domain/path';
import { useDirectory, useUrlPath } from './useDirectory';
import styles from './BrowsePage.module.css';

function FolderIcon() {
  return (
    <svg className={styles.icon} viewBox="0 0 24 24" aria-hidden="true">
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z" />
    </svg>
  );
}

function FileIcon() {
  return (
    <svg className={styles.icon} viewBox="0 0 24 24" aria-hidden="true">
      <path d="M7 3.5h7L19.5 9v11a1.5 1.5 0 0 1-1.5 1.5H7A1.5 1.5 0 0 1 5.5 20V5A1.5 1.5 0 0 1 7 3.5z" />
      <path d="M14 3.5V9h5.5" />
    </svg>
  );
}

/** 目录浏览页；当前路径完全由 ?path= 驱动。 */
export function BrowsePage() {
  const [path, navigate] = useUrlPath();
  const { entries, total, truncated, loading, error, reload } = useDirectory(path);

  const segments = useMemo(() => pathSegments(path), [path]);
  const currentLabel = path ? `/${path}` : '/';

  return (
    <div className={styles.page}>
      <nav className={styles.crumbs} aria-label="面包屑">
        <button
          type="button"
          className={styles.crumb}
          onClick={() => navigate('')}
          aria-current={path ? undefined : 'page'}
        >
          根目录
        </button>
        {segments.map((segment, index) => {
          const target = segments.slice(0, index + 1).join('/');
          const isCurrent = index === segments.length - 1;
          return (
            <span className={styles.crumbGroup} key={target}>
              <span className={styles.sep} aria-hidden="true">
                /
              </span>
              <button
                type="button"
                className={styles.crumb}
                onClick={() => navigate(target)}
                aria-current={isCurrent ? 'page' : undefined}
              >
                {segment}
              </button>
            </span>
          );
        })}
      </nav>

      <div className={styles.head}>
        <div>
          <h1 className={styles.title}>{currentLabel}</h1>
          <p className={styles.meta}>
            {loading ? '正在加载…' : `共 ${total ?? entries.length} 项`}
            {truncated ? '（已截断，后续阶段支持继续加载）' : ''}
          </p>
        </div>
        <div className={styles.actions}>
          {path ? (
            <button
              type="button"
              className={styles.action}
              onClick={() => navigate(parentPath(path))}
            >
              返回上级
            </button>
          ) : null}
          <button type="button" className={styles.action} onClick={reload} disabled={loading}>
            刷新
          </button>
        </div>
      </div>

      {error ? (
        <div className={styles.error} role="alert">
          <p>{error}</p>
          <button type="button" className={styles.action} onClick={reload}>
            重试
          </button>
        </div>
      ) : null}

      {!error && !loading && entries.length === 0 ? (
        <p className={styles.empty}>这个目录是空的</p>
      ) : null}

      {!error && entries.length > 0 ? (
        <div className={styles.tableWrap} aria-busy={loading}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th scope="col">名称</th>
                <th scope="col" className={styles.sizeCol}>
                  大小
                </th>
                <th scope="col" className={styles.timeCol}>
                  修改时间
                </th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry) => {
                const isDir = entry.is_dir;
                const content = (
                  <>
                    {isDir ? <FolderIcon /> : <FileIcon />}
                    <span className={styles.name}>{entry.name}</span>
                  </>
                );
                return (
                  <tr key={entry.name} className={isDir ? styles.dirRow : undefined}>
                    <td>
                      {isDir ? (
                        <button
                          type="button"
                          className={styles.nameButton}
                          onClick={() => navigate(joinPath(path, entry.name))}
                        >
                          {content}
                        </button>
                      ) : (
                        <span className={styles.nameText}>{content}</span>
                      )}
                    </td>
                    <td className={styles.sizeCol}>{isDir ? '—' : formatBytes(entry.size)}</td>
                    <td className={styles.timeCol}>{formatTime(entry.mtime)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}
    </div>
  );
}

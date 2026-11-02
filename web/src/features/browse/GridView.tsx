/** 网格视图：方形缩略图 + 名称/大小/时间。 */
import { formatBytes, formatTime } from '../../domain/format';
import { kindOfEntry } from '../../domain/fileType';
import { FileThumb } from './FileThumb';
import { Highlight } from './Highlight';
import type { BrowseEntry } from './types';
import styles from './GridView.module.css';

interface GridViewProps {
  entries: BrowseEntry[];
  query: string;
  serverTranscode: boolean;
  onOpen: (entry: BrowseEntry) => void;
}

/** 网格卡片；目录/文件都由原生 button 承载，Tab/Enter/空格可直接操作。 */
export function GridView({ entries, query, serverTranscode, onOpen }: GridViewProps) {
  return (
    <div className={styles.grid} data-testid="grid">
      {entries.map((entry) => {
        const kind = kindOfEntry(entry);
        return (
          <button
            type="button"
            key={entry.fullPath}
            className={styles.card}
            aria-label={entry.name}
            title={entry.name}
            data-testid="entry-card"
            data-entry-name={entry.name}
            data-kind={kind}
            onClick={() => onOpen(entry)}
          >
            <FileThumb entry={entry} path={entry.fullPath} serverTranscode={serverTranscode} />
            <span className={styles.info}>
              <span className={styles.name}>
                <Highlight text={entry.name} query={query} />
              </span>
              <span className={styles.meta}>
                {entry.is_dir ? '文件夹' : formatBytes(entry.size)}
                {entry.mtime ? ` · ${formatTime(entry.mtime)}` : ''}
              </span>
            </span>
          </button>
        );
      })}
    </div>
  );
}

/** 加载骨架：保持与网格相同的列宽，避免加载完成时跳动。 */
export function GridSkeleton({ count = 12 }: { count?: number }) {
  return (
    <div className={styles.grid} aria-hidden="true" data-testid="grid-skeleton">
      {Array.from({ length: count }, (_, index) => (
        <span className={styles.skCard} key={index} />
      ))}
    </div>
  );
}

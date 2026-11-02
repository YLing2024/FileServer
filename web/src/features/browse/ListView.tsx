/** 列表视图：名称/大小/修改时间三列，名称可点进入。 */
import { formatBytes, formatTime } from '../../domain/format';
import { kindOfEntry } from '../../domain/fileType';
import { Highlight } from './Highlight';
import { KindIcon } from './icons';
import type { BrowseEntry } from './types';
import styles from './ListView.module.css';

interface ListViewProps {
  entries: BrowseEntry[];
  query: string;
  onOpen: (entry: BrowseEntry) => void;
}

/** 列表行；目录优先由调用方排序保证。 */
export function ListView({ entries, query, onOpen }: ListViewProps) {
  return (
    <div className={styles.wrap} data-testid="list">
      <table className={styles.table}>
        <colgroup>
          <col />
          <col className={styles.sizeCol} />
          <col className={styles.timeCol} />
        </colgroup>
        <thead>
          <tr>
            <th scope="col">名称</th>
            <th scope="col" className={styles.sizeCell}>
              大小
            </th>
            <th scope="col" className={styles.timeCell}>
              修改时间
            </th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry) => (
            <tr key={entry.fullPath} data-testid="entry-row" data-entry-name={entry.name}>
              <td>
                <button
                  type="button"
                  className={styles.nameButton}
                  aria-label={entry.name}
                  title={entry.name}
                  onClick={() => onOpen(entry)}
                >
                  <KindIcon kind={kindOfEntry(entry)} className={styles.icon} />
                  <span className={styles.name}>
                    <Highlight text={entry.name} query={query} />
                  </span>
                </button>
              </td>
              <td className={styles.sizeCell}>{entry.is_dir ? '—' : formatBytes(entry.size)}</td>
              <td className={styles.timeCell}>{formatTime(entry.mtime)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** 列表加载骨架：三列占位，行高与真实行一致。 */
export function ListSkeleton({ count = 8 }: { count?: number }) {
  return (
    <div className={styles.wrap} aria-hidden="true" data-testid="list-skeleton">
      {Array.from({ length: count }, (_, index) => (
        <span className={styles.skRow} key={index} />
      ))}
    </div>
  );
}

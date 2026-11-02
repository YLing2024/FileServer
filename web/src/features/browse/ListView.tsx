/** 列表视图：名称/大小/修改时间三列，名称可点进入。 */
import { formatBytes, formatTime } from '../../domain/format';
import { kindOfEntry } from '../../domain/fileType';
import { Highlight } from './Highlight';
import { KindIcon } from './icons';
import type { BrowseEntry, EntryInteraction } from './types';
import styles from './ListView.module.css';

interface ListViewProps {
  entries: BrowseEntry[];
  query: string;
  onOpen: (entry: BrowseEntry) => void;
  interaction?: EntryInteraction;
}

/** 列表行；目录优先由调用方排序保证。行本身承担选择，名称按钮保证键盘可达。 */
export function ListView({ entries, query, onOpen, interaction }: ListViewProps) {
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
          {entries.map((entry) => {
            const selected = interaction?.selected.has(entry.fullPath) ?? false;
            return (
              <tr
                key={entry.fullPath}
                className={selected ? styles.selected : undefined}
                data-testid="entry-row"
                data-entry-name={entry.name}
                data-selected={interaction ? selected : undefined}
                aria-selected={interaction ? selected : undefined}
                onClick={interaction ? (event) => interaction.onClick(entry, event) : undefined}
                onDoubleClick={interaction ? () => interaction.onDoubleClick(entry) : undefined}
                onPointerDown={
                  interaction ? (event) => interaction.onPointerDown(entry, event) : undefined
                }
                onPointerMove={interaction?.onPointerMove}
                onPointerUp={interaction?.onPointerUp}
                onPointerCancel={interaction?.onPointerUp}
              >
                <td>
                  <button
                    type="button"
                    className={styles.nameButton}
                    aria-label={entry.name}
                    title={entry.name}
                    onClick={
                      interaction
                        ? undefined
                        : () => {
                            onOpen(entry);
                          }
                    }
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
            );
          })}
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

/**
 * 批量操作栏：显示已选数量，并提供下载、重命名、移动、复制、删除与退出。
 *
 * 只有 `count > 0` 时渲染；重命名仅单选可用。按钮全部是原生 button，
 * 窄屏下换行而不是横向溢出。
 */
import buttonStyles from '../../styles/buttons.module.css';
import styles from './SelectionBar.module.css';

interface SelectionBarProps {
  count: number;
  onDownload: () => void;
  onRename: () => void;
  onMove: () => void;
  onCopy: () => void;
  onDelete: () => void;
  onExit: () => void;
}

export function SelectionBar({
  count,
  onDownload,
  onRename,
  onMove,
  onCopy,
  onDelete,
  onExit,
}: SelectionBarProps) {
  if (count <= 0) return null;
  return (
    <div className={styles.bar} role="region" aria-label="批量操作" data-testid="selection-bar">
      <span className={styles.count} data-testid="selection-count">
        已选 {count} 项
      </span>
      <div className={styles.actions}>
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small}`}
          onClick={onDownload}
        >
          下载
        </button>
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small}`}
          onClick={onRename}
          disabled={count !== 1}
        >
          重命名
        </button>
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small}`}
          onClick={onMove}
        >
          移动
        </button>
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small}`}
          onClick={onCopy}
        >
          复制
        </button>
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.danger}`}
          onClick={onDelete}
        >
          删除
        </button>
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.ghost}`}
          onClick={onExit}
          data-testid="selection-exit"
        >
          退出
        </button>
      </div>
    </div>
  );
}

/**
 * 上传队列面板：总进度、逐文件进度、暂停/继续、重试、取消与关闭。
 *
 * 只渲染 UploadSnapshot；所有操作通过回调交给队列状态机。
 * 面板在上传完成后由队列自动关闭（约 1.8 秒），progress 归零即卸载。
 */
import buttonStyles from '../../styles/buttons.module.css';
import { formatBytes } from '../../domain/format';
import type { UploadItemView, UploadSnapshot, UploadStatus } from './types';
import styles from './UploadPanel.module.css';

interface UploadPanelProps {
  snapshot: UploadSnapshot;
  onClose: () => void;
  onPause: (id: number) => void;
  onResume: (id: number) => void;
  onCancel: (id: number) => void;
  onRetry: (id: number) => void;
  onCancelAll: () => void;
}

function statusText(item: UploadItemView): string {
  switch (item.status) {
    case 'waiting':
      return '等待中';
    case 'hashing':
      return item.progress > 0 ? `校验 ${Math.round(item.progress)}%` : '校验中…';
    case 'instant':
      return '秒传检查…';
    case 'uploading':
      return item.progress > 0 ? `${Math.round(item.progress)}%` : '上传中…';
    case 'paused':
      return item.error || '已暂停';
    case 'done':
      return '已完成';
    case 'error':
      return item.error || '失败';
    case 'canceled':
      return item.error || '已取消';
    case 'skipped':
      return item.error || '已跳过';
    default:
      return '';
  }
}

function isActive(status: UploadStatus): boolean {
  return status === 'waiting' || status === 'hashing' || status === 'instant' || status === 'uploading';
}

function ItemActions({
  item,
  onPause,
  onResume,
  onCancel,
  onRetry,
}: {
  item: UploadItemView;
  onPause: (id: number) => void;
  onResume: (id: number) => void;
  onCancel: (id: number) => void;
  onRetry: (id: number) => void;
}) {
  if (item.status === 'done') return null;
  if (item.status === 'paused') {
    return (
      <div className={styles.actions}>
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small}`}
          onClick={() => onResume(item.id)}
        >
          继续
        </button>
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.ghost}`}
          onClick={() => onCancel(item.id)}
        >
          取消
        </button>
      </div>
    );
  }
  if (item.status === 'error' || item.status === 'canceled' || item.status === 'skipped') {
    return (
      <div className={styles.actions}>
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small}`}
          onClick={() => onRetry(item.id)}
        >
          重试
        </button>
      </div>
    );
  }
  return (
    <div className={styles.actions}>
      {isActive(item.status) ? (
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.ghost}`}
          onClick={() => onPause(item.id)}
        >
          暂停
        </button>
      ) : null}
      <button
        type="button"
        className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.ghost}`}
        onClick={() => onCancel(item.id)}
      >
        取消
      </button>
    </div>
  );
}

/** 上传队列面板；`panelOpen=false` 时不渲染。 */
export function UploadPanel({
  snapshot,
  onClose,
  onPause,
  onResume,
  onCancel,
  onRetry,
  onCancelAll,
}: UploadPanelProps) {
  if (!snapshot.panelOpen) return null;

  return (
    <section
      className={styles.panel}
      role="region"
      aria-label="上传队列"
      data-testid="upload-panel"
    >
      <header className={styles.head}>
        <div>
          <span className={styles.title} data-testid="upload-title">
            {snapshot.title}
          </span>
          <span className={styles.total} data-testid="upload-total">
            总进度 {snapshot.totalProgress}%
          </span>
        </div>
        <div className={styles.headActions}>
          {snapshot.busy ? (
            <button
              type="button"
              className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.ghost}`}
              onClick={onCancelAll}
            >
              全部取消
            </button>
          ) : null}
          <button
            type="button"
            className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.icon}`}
            aria-label="关闭上传队列"
            title={snapshot.busy ? '上传进行中，暂不能关闭' : '关闭上传队列'}
            disabled={snapshot.busy}
            onClick={onClose}
          >
            ×
          </button>
        </div>
      </header>

      <div
        className={styles.totalBar}
        role="progressbar"
        aria-label="总上传进度"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={snapshot.totalProgress}
      >
        <i
          className={styles.totalFill}
          style={{ width: `${snapshot.totalProgress}%` }}
          data-testid="upload-overall-fill"
          data-progress={snapshot.totalProgress}
        />
      </div>

      <div className={styles.list}>
        {snapshot.items.map((item, index) => {
          const previous = index > 0 ? snapshot.items[index - 1] : undefined;
          const groupChanged =
            item.groupRoot !== '' && item.groupRoot !== (previous?.groupRoot ?? '');
          return (
            <div key={item.id}>
              {groupChanged ? (
                <div className={styles.group} data-testid="upload-group">
                  <span className={styles.groupName} title={item.groupRoot}>
                    {item.groupRoot}
                  </span>
                </div>
              ) : null}
              <div
                className={`${styles.item} ${item.status === 'error' || item.status === 'canceled' ? styles.failed : ''}`}
                data-testid="upload-item"
                data-name={item.name}
                data-status={item.status}
              >
                <div className={styles.itemHead}>
                  <span className={styles.name} title={item.name}>
                    {item.name}
                  </span>
                  <span className={styles.status} title={statusText(item)}>
                    {statusText(item)}
                  </span>
                </div>
                <div className={styles.bar}>
                  <i
                    className={styles.fill}
                    style={{ width: `${item.status === 'done' ? 100 : item.progress}%` }}
                    data-testid="upload-item-fill"
                    data-progress={Math.round(item.progress)}
                  />
                </div>
                <div className={styles.foot}>
                  <span className={styles.size}>{formatBytes(item.size)}</span>
                  <ItemActions
                    item={item}
                    onPause={onPause}
                    onResume={onResume}
                    onCancel={onCancel}
                    onRetry={onRetry}
                  />
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

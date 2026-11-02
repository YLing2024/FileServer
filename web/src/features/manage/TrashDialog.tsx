/**
 * 回收站对话框：列表、全选、恢复、彻底删除与清空。
 *
 * 恢复冲突（原位置已有同名项）在对话框内提示，可自动改名重试或取消；
 * 彻底删除与清空需要第二次确认，删除不可恢复。
 */
import { useCallback, useEffect, useState } from 'react';
import { ApiError } from '../../api/errors';
import {
  emptyTrash,
  fetchTrash,
  restoreTrash,
  type ManageFailure,
  type TrashItem,
} from '../../api/manage';
import { Modal } from '../../components/Modal';
import { useToast } from '../../components/Toast';
import { formatBytes, formatTime } from '../../domain/format';
import buttonStyles from '../../styles/buttons.module.css';
import { ConfirmDialog } from './ConfirmDialog';
import { manageErrorMessage } from './messages';
import styles from './TrashDialog.module.css';

interface TrashDialogProps {
  onClose: () => void;
  onRestored: () => void;
  onFailures: (title: string, failed: ManageFailure[]) => void;
}

type RemoveRequest = { mode: 'ids'; ids: string[] } | { mode: 'all' };

const KIND_LABELS: Record<string, string> = {
  image: '图片',
  video: '视频',
  audio: '音频',
  pdf: 'PDF',
  archive: '压缩包',
  text: '文本',
  doc: '文档',
  code: '代码',
  other: '文件',
};

function kindLabel(item: TrashItem): string {
  if (item.is_dir) return '文件夹';
  return KIND_LABELS[item.kind] ?? '文件';
}

export function TrashDialog({ onClose, onRestored, onFailures }: TrashDialogProps) {
  const { showToast } = useToast();
  const [items, setItems] = useState<TrashItem[]>([]);
  const [totalSize, setTotalSize] = useState(0);
  const [selected, setSelected] = useState<ReadonlySet<string>>(() => new Set());
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [removeRequest, setRemoveRequest] = useState<RemoveRequest | null>(null);
  const [conflict, setConflict] = useState<{ ids: string[]; names: string[] } | null>(null);

  const load = useCallback(async (): Promise<void> => {
    setLoading(true);
    setError('');
    try {
      const data = await fetchTrash();
      const next = data.items ?? [];
      setItems(next);
      setTotalSize(data.total_size ?? 0);
      const valid = new Set(next.map((item) => item.id));
      setSelected((current) => new Set([...current].filter((id) => valid.has(id))));
    } catch (loadError) {
      setError(manageErrorMessage(loadError, '回收站加载失败'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 打开对话框时加载回收站是外部数据同步
    void load();
  }, [load]);

  const restore = async (ids: string[], mode: 'ask' | 'rename'): Promise<void> => {
    if (!ids.length || busy) return;
    setBusy(true);
    setError('');
    setConflict(null);
    try {
      const result = await restoreTrash(ids, mode);
      const failed = result.failed ?? [];
      showToast(
        failed.length
          ? `恢复 ${result.restored?.length ?? 0} 项，${failed.length} 项失败`
          : '已恢复',
      );
      setSelected(new Set());
      await load();
      if (result.restored?.length) onRestored();
      if (failed.length) onFailures('部分项目未能恢复', failed);
    } catch (restoreError) {
      if (
        restoreError instanceof ApiError &&
        restoreError.status === 409 &&
        restoreError.code === 'conflict'
      ) {
        setConflict({
          ids,
          names: restoreError.existing.length ? [...restoreError.existing] : ids,
        });
      } else {
        setError(manageErrorMessage(restoreError, '恢复失败'));
      }
    } finally {
      setBusy(false);
    }
  };

  const remove = async (request: RemoveRequest): Promise<void> => {
    setRemoveRequest(null);
    setBusy(true);
    setError('');
    try {
      const result = await emptyTrash(
        request.mode === 'all' ? { all: true } : { ids: request.ids },
      );
      const failed = result.failed ?? [];
      const count = result.deleted?.length ?? 0;
      showToast(failed.length ? `删除 ${count} 项，${failed.length} 项失败` : '已彻底删除');
      setSelected(new Set());
      await load();
      if (failed.length) onFailures('部分项目未能删除', failed);
    } catch (removeError) {
      setError(manageErrorMessage(removeError, '删除失败'));
    } finally {
      setBusy(false);
    }
  };

  const selectedIds = [...selected];
  const allChecked = items.length > 0 && selectedIds.length === items.length;

  return (
    <>
      <Modal
        title="回收站"
        onClose={busy ? () => undefined : onClose}
        wide
        testId="trash-dialog"
        footer={
          <>
            <button
              type="button"
              className={`${buttonStyles.button} ${buttonStyles.ghost}`}
              onClick={onClose}
              disabled={busy}
            >
              关闭
            </button>
            <button
              type="button"
              className={`${buttonStyles.button} ${buttonStyles.danger}`}
              disabled={busy || items.length === 0}
              onClick={() => setRemoveRequest({ mode: 'all' })}
              data-testid="trash-empty"
            >
              清空
            </button>
            <button
              type="button"
              className={`${buttonStyles.button} ${buttonStyles.danger}`}
              disabled={busy || selectedIds.length === 0}
              onClick={() => setRemoveRequest({ mode: 'ids', ids: selectedIds })}
              data-testid="trash-delete"
            >
              彻底删除
            </button>
            <button
              type="button"
              className={`${buttonStyles.button} ${buttonStyles.primary}`}
              disabled={busy || selectedIds.length === 0}
              onClick={() => {
                void restore(selectedIds, 'ask');
              }}
              data-testid="trash-restore"
            >
              恢复
            </button>
          </>
        }
      >
        <div className={styles.head}>
          <label className={styles.all}>
            <input
              type="checkbox"
              checked={allChecked}
              disabled={items.length === 0}
              ref={(element) => {
                if (element) element.indeterminate = selectedIds.length > 0 && !allChecked;
              }}
              onChange={(event) => {
                setSelected(
                  event.target.checked ? new Set(items.map((item) => item.id)) : new Set(),
                );
              }}
              aria-label="全选回收站条目"
            />
            全选
          </label>
          <span className={styles.usage}>
            {items.length ? `${items.length} 项，共 ${formatBytes(totalSize)}` : '回收站为空'}
          </span>
        </div>

        {loading ? <p className={styles.hint}>加载中…</p> : null}
        {!loading && items.length === 0 ? <p className={styles.hint}>暂无可恢复的项目</p> : null}

        <div className={styles.list}>
          {items.map((item) => (
            <label
              key={item.id}
              className={styles.row}
              data-testid="trash-item"
              data-path={item.path}
            >
              <input
                type="checkbox"
                checked={selected.has(item.id)}
                aria-label={`选择 ${item.path}`}
                onChange={(event) => {
                  setSelected((current) => {
                    const next = new Set(current);
                    if (event.target.checked) next.add(item.id);
                    else next.delete(item.id);
                    return next;
                  });
                }}
              />
              <span className={styles.info}>
                <span className={styles.name} title={item.path}>
                  {item.path}
                </span>
                <span className={styles.meta}>
                  {kindLabel(item)} · {formatBytes(item.size)} · {formatTime(item.deleted_at)}
                </span>
              </span>
            </label>
          ))}
        </div>

        {error ? (
          <p className={styles.error} role="alert">
            {error}
          </p>
        ) : null}

        {conflict ? (
          <div className={styles.conflict} role="alert" data-testid="trash-conflict">
            <p>原位置已有同名项：{conflict.names.join('、')}</p>
            <div className={styles.conflictActions}>
              <button
                type="button"
                className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.ghost}`}
                onClick={() => setConflict(null)}
              >
                取消
              </button>
              <button
                type="button"
                className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.primary}`}
                onClick={() => {
                  void restore(conflict.ids, 'rename');
                }}
              >
                自动改名
              </button>
            </div>
          </div>
        ) : null}

        {busy ? <p className={styles.hint}>处理中…</p> : null}
      </Modal>

      {removeRequest ? (
        <ConfirmDialog
          title={removeRequest.mode === 'all' ? '清空回收站' : '彻底删除'}
          message={
            removeRequest.mode === 'all'
              ? `将彻底删除回收站中的 ${items.length} 项，不可恢复。`
              : `将彻底删除 ${removeRequest.ids.length} 项，不可恢复。`
          }
          confirmLabel={removeRequest.mode === 'all' ? '清空回收站' : '彻底删除'}
          danger
          onCancel={() => setRemoveRequest(null)}
          onConfirm={() => {
            void remove(removeRequest);
          }}
        />
      ) : null}
    </>
  );
}

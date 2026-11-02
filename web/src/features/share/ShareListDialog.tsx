/**
 * 分享管理对话框：列出全部分享，复制链接、取消分享、删除记录。
 *
 * 过期/撤销/超次项保留展示但标灰；取消分享只让链接立即失效，删除记录
 * 才从 shares.json 移除，两者都不影响源文件。
 */
import { useCallback, useEffect, useState } from 'react';
import { errorMessage } from '../../api/errors';
import {
  absoluteShareUrl,
  deleteShareRecord,
  listShares,
  revokeShare,
  SHARE_STATUS_LABELS,
  type ShareListItem,
  type ShareStatus,
} from '../../api/share';
import { Modal } from '../../components/Modal';
import { useToast } from '../../components/Toast';
import { formatTime } from '../../domain/format';
import buttonStyles from '../../styles/buttons.module.css';
import { ConfirmDialog } from '../manage/ConfirmDialog';
import { copyText } from './clipboard';
import styles from './ShareDialogs.module.css';

interface ShareListDialogProps {
  onClose: () => void;
}

type PendingAction = { kind: 'revoke' | 'delete'; item: ShareListItem } | null;

function statusLabel(status: ShareStatus): string {
  return SHARE_STATUS_LABELS[status] ?? status;
}

/** 分享管理对话框；打开时自动拉取列表。 */
export function ShareListDialog({ onClose }: ShareListDialogProps) {
  const { showToast } = useToast();
  const [items, setItems] = useState<ShareListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [pending, setPending] = useState<PendingAction>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError('');
    try {
      setItems(await listShares(signal));
    } catch (loadError) {
      if (signal?.aborted) return;
      setError(errorMessage(loadError, '加载分享列表失败'));
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 打开对话框立即拉取分享列表
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const copy = async (text: string): Promise<void> => {
    if (await copyText(text)) showToast('已复制');
    else showToast('复制失败，请手动选择', 'error');
  };

  const confirmAction = async (): Promise<void> => {
    if (!pending || busy) return;
    setBusy(true);
    try {
      if (pending.kind === 'revoke') await revokeShare(pending.item.token);
      else await deleteShareRecord(pending.item.token);
      setPending(null);
      showToast(pending.kind === 'revoke' ? '分享已取消' : '记录已删除');
      await load();
    } catch (actionError) {
      showToast(errorMessage(actionError, '操作失败'), 'error');
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Modal
        title="分享链接"
        onClose={onClose}
        testId="share-list-dialog"
        wide
        footer={
          <button type="button" className={buttonStyles.button} onClick={onClose}>
            关闭
          </button>
        }
      >
        <div className={styles.form}>
          {loading ? <p className={styles.hint}>正在加载…</p> : null}
          {error ? (
            <p className={styles.error} role="alert">
              {error}
            </p>
          ) : null}
          {!loading && !error && items.length === 0 ? (
            <p className={styles.empty}>暂无分享记录</p>
          ) : null}
          <div className={styles.list}>
            {items.map((item) => {
              const link = absoluteShareUrl(item.url);
              const active = item.status === 'active';
              return (
                <div
                  key={item.token}
                  className={`${styles.item} ${active ? '' : styles.itemInactive}`}
                  data-testid="share-item"
                  data-token={item.token}
                  data-status={item.status}
                >
                  <div className={styles.itemHead}>
                    <span className={styles.itemName}>{item.name || '（已删除）'}</span>
                    <span
                      className={`${styles.badge} ${
                        active ? styles.badgeActive : item.status === 'exhausted' ? styles.badgeDanger : ''
                      }`}
                    >
                      {statusLabel(item.status)}
                    </span>
                    {item.is_dir ? <span className={styles.badge}>文件夹</span> : null}
                    {item.code_set ? <span className={styles.badge}>提取码</span> : null}
                  </div>
                  <span className={styles.itemLink} data-testid="share-item-link">
                    {link}
                  </span>
                  <div className={styles.itemMeta}>
                    <span>{item.expires ? `有效期至 ${formatTime(item.expires)}` : '永久有效'}</span>
                    <span>
                      已下载 {item.downloads}
                      {item.max_downloads ? ` / ${item.max_downloads}` : ' / 不限'}
                    </span>
                    <span>创建于 {formatTime(item.created)}</span>
                    {item.note && item.note !== '共享文件' ? <span>备注：{item.note}</span> : null}
                  </div>
                  <div className={styles.itemActions}>
                    <button
                      type="button"
                      className={`${buttonStyles.button} ${buttonStyles.small}`}
                      onClick={() => {
                        void copy(link);
                      }}
                    >
                      复制链接
                    </button>
                    {active ? (
                      <button
                        type="button"
                        className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.danger}`}
                        onClick={() => setPending({ kind: 'revoke', item })}
                        data-testid="share-revoke"
                      >
                        取消分享
                      </button>
                    ) : null}
                    <button
                      type="button"
                      className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.ghost}`}
                      onClick={() => setPending({ kind: 'delete', item })}
                      data-testid="share-delete"
                    >
                      删除记录
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </Modal>

      {pending ? (
        <ConfirmDialog
          title={pending.kind === 'revoke' ? '取消分享' : '删除记录'}
          message={
            pending.kind === 'revoke'
              ? '取消后链接立即失效，确定取消？'
              : '删除记录不会删除源文件，确定删除？'
          }
          confirmLabel={busy ? '处理中…' : pending.kind === 'revoke' ? '取消分享' : '删除'}
          danger={pending.kind === 'revoke'}
          onCancel={() => {
            if (!busy) setPending(null);
          }}
          onConfirm={() => {
            void confirmAction();
          }}
        />
      ) : null}
    </>
  );
}

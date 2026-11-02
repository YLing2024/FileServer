/**
 * 已登录设备/会话对话框：列出设备与时间，支持下线其他会话。
 *
 * 当前会话只显示“当前”标记（服务端拒绝下线当前会话）；下线其他设备
 * 后立即刷新列表，不显示 token 明文。
 */
import { useCallback, useEffect, useState } from 'react';
import { errorMessage } from '../../api/errors';
import { fetchSessions, revokeSession, type SessionEntry } from '../../api/sessions';
import { Modal } from '../../components/Modal';
import { useToast } from '../../components/Toast';
import { formatTime } from '../../domain/format';
import buttonStyles from '../../styles/buttons.module.css';
import styles from './SessionsDialog.module.css';

interface SessionsDialogProps {
  onClose: () => void;
}

/** 会话列表对话框；打开时拉取，下线后刷新。 */
export function SessionsDialog({ onClose }: SessionsDialogProps) {
  const { showToast } = useToast();
  const [sessions, setSessions] = useState<SessionEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busyId, setBusyId] = useState('');

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError('');
    try {
      setSessions(await fetchSessions(signal));
    } catch (loadError) {
      if (signal?.aborted) return;
      setError(errorMessage(loadError, '加载会话列表失败'));
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 打开对话框立即拉取会话列表
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const revoke = async (session: SessionEntry): Promise<void> => {
    if (busyId) return;
    setBusyId(session.id);
    try {
      await revokeSession(session.id);
      showToast('已下线该设备');
      await load();
    } catch (revokeError) {
      showToast(errorMessage(revokeError, '下线失败'), 'error');
    } finally {
      setBusyId('');
    }
  };

  return (
    <Modal
      title="已登录设备/会话"
      onClose={onClose}
      testId="sessions-dialog"
      wide
      footer={
        <button type="button" className={buttonStyles.button} onClick={onClose}>
          关闭
        </button>
      }
    >
      <div className={styles.list}>
        {loading ? <p className={styles.hint}>正在加载…</p> : null}
        {error ? (
          <p className={styles.error} role="alert">
            {error}
          </p>
        ) : null}
        {!loading && !error && sessions.length === 0 ? (
          <p className={styles.hint}>暂无会话</p>
        ) : null}
        {sessions.map((session) => (
          <div className={styles.row} key={session.id} data-testid="session-row">
            <div className={styles.meta}>
              <span className={styles.device}>
                {session.ua || '未知设备'}
                {session.current ? (
                  <span className={styles.current} data-testid="session-current">
                    当前
                  </span>
                ) : null}
              </span>
              <span className={styles.detail}>
                登录于 {formatTime(session.created)}
                {session.remember ? ' · 记住我' : ''}
                {session.ip ? ` · ${session.ip}` : ''}
              </span>
            </div>
            {session.current ? null : (
              <button
                type="button"
                className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.danger}`}
                disabled={busyId === session.id}
                onClick={() => {
                  void revoke(session);
                }}
                data-testid="session-revoke"
              >
                {busyId === session.id ? '下线中…' : '下线'}
              </button>
            )}
          </div>
        ))}
      </div>
    </Modal>
  );
}

/**
 * 文本/代码只读预览：等宽字体、保留空白、超长文件只取前 2 MiB 并明确提示。
 *
 * 编辑能力属于 Phase 3；本组件不调用 /api/text，避免只读预览触碰写接口。
 */
import { useEffect, useState } from 'react';
import { request } from '../../api/client';
import { errorMessage } from '../../api/errors';
import { ensureTicket, fileUrl, withTicket } from '../../api/media';
import styles from './TextPreview.module.css';

interface TextPreviewProps {
  path: string;
  size: number;
  mtime: number;
  onToast: (message: string, tone?: 'info' | 'error') => void;
}

/** 只读预览上限：与服务端文本编辑上限一致，超出部分不读入内存。 */
export const TEXT_PREVIEW_LIMIT = 2 * 1024 * 1024;

interface TextState {
  content: string;
  loading: boolean;
  error: string;
  truncated: boolean;
}

const INITIAL: TextState = { content: '', loading: true, error: '', truncated: false };

function formatSize(bytes: number): string {
  if (bytes <= 0) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** 文本预览：超限时用 Range 只取前 2 MiB。 */
export function TextPreview({ path, size, mtime, onToast }: TextPreviewProps) {
  const [state, setState] = useState<TextState>(INITIAL);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换文件时重置读取状态
    setState(INITIAL);
    const limit = TEXT_PREVIEW_LIMIT;
    // 深链进入时目录尚未加载，size 未知（-1）：等 BrowsePage 拿到真实条目后再读取。
    if (size < 0) return () => controller.abort();

    void ensureTicket()
      .then(async (ticket) => {
        const headers = size > limit ? { Range: `bytes=0-${limit - 1}` } : undefined;
        const response = await request(withTicket(fileUrl(path), ticket), {
          headers,
          signal: controller.signal,
        });
        if (!response.ok && response.status !== 206) {
          throw new Error(response.status === 416 ? '文件为空或范围不可读' : '文本读取失败');
        }
        const text = await response.text();
        if (controller.signal.aborted) return;
        setState({
          content: text.length > limit ? text.slice(0, limit) : text,
          loading: false,
          error: '',
          truncated: size > limit || response.status === 206 || text.length > limit,
        });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof DOMException && error.name === 'AbortError') return;
        setState({
          content: '',
          loading: false,
          error: errorMessage(error, '文本加载失败'),
          truncated: false,
        });
      });

    return () => controller.abort();
  }, [path, size, reloadToken]);

  const meta = [formatSize(size), mtime ? new Date(mtime * 1000).toLocaleString('zh-CN') : '']
    .filter(Boolean)
    .join(' · ');

  return (
    <div className={styles.wrap}>
      <div className={styles.bar}>
        <span className={styles.meta}>{meta}</span>
        <button
          type="button"
          className={styles.reload}
          onClick={() => {
            setReloadToken((value) => value + 1);
            onToast('正在重新读取', 'info');
          }}
        >
          重新读取
        </button>
      </div>

      {state.truncated ? (
        <p className={styles.notice} data-testid="text-truncated">
          文件较大（超过 2 MB），仅显示前 2 MB。
        </p>
      ) : null}

      {state.loading ? <div className={styles.loading}>正在加载…</div> : null}

      {state.error ? (
        <div className={styles.error} role="alert">
          <p>{state.error}</p>
          <button
            type="button"
            className={styles.reload}
            onClick={() => setReloadToken((value) => value + 1)}
          >
            重试
          </button>
        </div>
      ) : null}

      {!state.loading && !state.error ? (
        <pre className={styles.pre} tabIndex={0} aria-label="文本内容" data-testid="text-preview">
          {state.content}
        </pre>
      ) : null}
    </div>
  );
}

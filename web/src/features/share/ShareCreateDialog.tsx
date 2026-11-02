/**
 * 创建分享对话框：选择有效期、提取码、下载次数、预览开关与备注。
 *
 * 创建成功后原地切换为结果视图，展示绝对链接与提取码；提取码只在创建
 * 响应中可见，关闭后无法找回。目录分享会包含之后新增的内容。
 */
import { useState, type FormEvent } from 'react';
import { errorMessage } from '../../api/errors';
import { absoluteShareUrl, createShare, type ShareCreateResult } from '../../api/share';
import { Modal } from '../../components/Modal';
import { useToast } from '../../components/Toast';
import buttonStyles from '../../styles/buttons.module.css';
import { copyText } from './clipboard';
import { generateShareCode, isValidShareCode, normalizeShareCode } from './shareCode';
import styles from './ShareDialogs.module.css';

/** 分享目标：单个文件或目录。 */
export interface ShareTarget {
  path: string;
  name: string;
  isDir: boolean;
}

interface ShareCreateDialogProps {
  target: ShareTarget;
  onClose: () => void;
  onCreated?: (result: ShareCreateResult) => void;
}

const DAY_OPTIONS = [
  { value: 7, label: '7 天' },
  { value: 1, label: '1 天' },
  { value: 30, label: '30 天' },
  { value: 0, label: '永久' },
] as const;

const MAX_DOWNLOAD_OPTIONS = [
  { value: 0, label: '不限' },
  { value: 10, label: '最多 10 次' },
  { value: 100, label: '最多 100 次' },
] as const;

/** 创建分享对话框，创建成功后切换为结果视图。 */
export function ShareCreateDialog({ target, onClose, onCreated }: ShareCreateDialogProps) {
  const { showToast } = useToast();
  const [days, setDays] = useState(7);
  const [codeEnabled, setCodeEnabled] = useState(false);
  const [code, setCode] = useState('');
  const [maxDownloads, setMaxDownloads] = useState(0);
  const [allowPreview, setAllowPreview] = useState(true);
  const [note, setNote] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<ShareCreateResult | null>(null);

  const toggleCode = (enabled: boolean): void => {
    setCodeEnabled(enabled);
    if (enabled && !code) setCode(generateShareCode());
    if (!enabled) setCode('');
    setError('');
  };

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy) return;
    if (codeEnabled && !isValidShareCode(code)) {
      setError('提取码必须是 4 位字母或数字');
      return;
    }
    setBusy(true);
    setError('');
    try {
      const created = await createShare({
        path: target.path,
        days,
        max_downloads: maxDownloads,
        allow_preview: allowPreview,
        note: note.trim(),
        ...(codeEnabled ? { code } : {}),
      });
      setResult(created);
      onCreated?.(created);
    } catch (submitError) {
      setError(errorMessage(submitError, '创建分享失败'));
    } finally {
      setBusy(false);
    }
  };

  const copy = async (text: string): Promise<void> => {
    if (await copyText(text)) showToast('已复制');
    else showToast('复制失败，请手动选择', 'error');
  };

  if (result) {
    const link = absoluteShareUrl(result.url);
    const withCode = result.code ? `${link}\n提取码：${result.code}` : link;
    return (
      <Modal
        title="分享已创建"
        onClose={onClose}
        testId="share-result-dialog"
        footer={
          <>
            <button
              type="button"
              className={`${buttonStyles.button} ${buttonStyles.primary}`}
              onClick={() => {
                void copy(withCode);
              }}
              data-testid="share-copy-all"
            >
              {result.code ? '复制链接+提取码' : '复制链接'}
            </button>
            <button
              type="button"
              className={buttonStyles.button}
              onClick={onClose}
              data-testid="share-result-close"
            >
              关闭
            </button>
          </>
        }
      >
        <div className={styles.form}>
          <label className={styles.label}>
            链接
            <span className={styles.linkRow}>
              <input
                className={`${styles.input} ${styles.mono}`}
                type="text"
                readOnly
                value={link}
                data-testid="share-result-link"
                onFocus={(event) => event.currentTarget.select()}
              />
              <button
                type="button"
                className={buttonStyles.button}
                onClick={() => {
                  void copy(link);
                }}
              >
                复制
              </button>
            </span>
          </label>
          {result.code ? (
            <p className={styles.hint} data-testid="share-result-code-row">
              提取码：
              <span className={styles.code} data-testid="share-result-code">
                {result.code}
              </span>
            </p>
          ) : null}
          <p className={styles.hint}>目录分享会包含之后新增的内容；撤销或过期后链接立即失效。</p>
          <p className={styles.hint}>提取码只在创建时显示，关闭后无法找回。</p>
        </div>
      </Modal>
    );
  }

  return (
    <Modal
      title="创建分享"
      onClose={busy ? () => undefined : onClose}
      testId="share-create-dialog"
      footer={
        <>
          <button
            type="button"
            className={`${buttonStyles.button} ${buttonStyles.ghost}`}
            onClick={onClose}
            disabled={busy}
          >
            取消
          </button>
          <button
            type="submit"
            form="share-create-form"
            className={`${buttonStyles.button} ${buttonStyles.primary}`}
            disabled={busy}
            data-testid="share-submit"
          >
            {busy ? '创建中…' : '创建链接'}
          </button>
        </>
      }
    >
      <form id="share-create-form" className={styles.form} onSubmit={(event) => void submit(event)}>
        <p className={styles.target} data-testid="share-target">
          {target.name}
          {target.isDir ? '（文件夹）' : ''}
        </p>
        {target.isDir ? (
          <p className={styles.hint}>目录分享会包含生成时刻之后新增的内容。</p>
        ) : null}

        <div className={styles.row}>
          <label className={styles.rowLabel} htmlFor="share-days">
            有效期
          </label>
          <select
            id="share-days"
            className={styles.select}
            value={days}
            onChange={(event) => setDays(Number(event.target.value))}
            data-testid="share-days"
          >
            {DAY_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </div>

        <div className={styles.row}>
          <label className={styles.check}>
            <input
              type="checkbox"
              checked={codeEnabled}
              onChange={(event) => toggleCode(event.target.checked)}
              data-testid="share-code-enable"
            />
            使用提取码
          </label>
          <input
            className={`${styles.input} ${styles.codeInput}`}
            type="text"
            value={code}
            maxLength={4}
            placeholder="4 位字母或数字"
            autoComplete="off"
            spellCheck={false}
            disabled={!codeEnabled}
            aria-label="提取码"
            onChange={(event) => {
              setCode(normalizeShareCode(event.target.value));
              setError('');
            }}
            data-testid="share-code"
          />
          {codeEnabled ? (
            <button
              type="button"
              className={`${buttonStyles.button} ${buttonStyles.small}`}
              onClick={() => setCode(generateShareCode())}
            >
              随机
            </button>
          ) : null}
        </div>

        <div className={styles.row}>
          <label className={styles.rowLabel} htmlFor="share-max">
            下载次数
          </label>
          <select
            id="share-max"
            className={styles.select}
            value={maxDownloads}
            onChange={(event) => setMaxDownloads(Number(event.target.value))}
            data-testid="share-max"
          >
            {MAX_DOWNLOAD_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </div>

        <label className={styles.check}>
          <input
            type="checkbox"
            checked={allowPreview}
            onChange={(event) => setAllowPreview(event.target.checked)}
            data-testid="share-preview"
          />
          允许在线预览（图片 / 视频 / 文本 / PDF）
        </label>

        <label className={styles.label}>
          分享者
          <input
            className={styles.input}
            type="text"
            value={note}
            maxLength={60}
            placeholder="共享文件"
            autoComplete="off"
            onChange={(event) => setNote(event.target.value)}
            data-testid="share-note"
          />
        </label>

        {error ? (
          <p className={styles.error} role="alert">
            {error}
          </p>
        ) : null}
      </form>
    </Modal>
  );
}

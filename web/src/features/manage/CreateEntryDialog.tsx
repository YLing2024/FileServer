/**
 * 「新建」对话框：在指定目录下新建空文件或文件夹。
 *
 * 与旧版一致：
 * - 类型切换（文件 / 文件夹）改变名称提示与「初始内容」的显隐；
 * - 只有文本白名单内的扩展名才显示「初始内容」，其余一律建空文件；
 * - 名称合法性与服务端规则一致（隐藏名、路径分隔符、Windows 保留名等由服务端最终裁决）。
 */
import { useMemo, useRef, useState } from 'react';
import { createEntry } from '../../api/manage';
import { Modal } from '../../components/Modal';
import buttonStyles from '../../styles/buttons.module.css';
import { manageErrorMessage } from './messages';
import styles from './CreateEntryDialog.module.css';

interface CreateEntryDialogProps {
  dir: string;
  textExts: readonly string[];
  onClose: () => void;
  onCreated: (name: string, kind: 'file' | 'dir') => void;
}

const RESERVED = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$/i;

function validate(value: string): string {
  if (!value) return '名称不能为空';
  if (value !== value.trim()) return '名称首尾不能有空白';
  if (value === '.' || value === '..') return '名称不合法';
  if (/[/\\]/.test(value)) return '名称不能包含路径分隔符';
  if (value.startsWith('.')) return '不能使用隐藏名称（点开头）';
  if (/[\u0000-\u001f\u007f]/.test(value)) return '名称包含控制字符';
  if (/[<>:"|?*]/.test(value)) return '名称不能包含 < > : " | ? * 等字符';
  if (value.endsWith('.') || value.endsWith(' ')) return '名称不能以点或空格结尾';
  if (RESERVED.test(value)) return '名称是系统保留名';
  if (value.toLowerCase() === '.fileserver') return '不能使用该名称';
  return '';
}

function extOf(name: string): string {
  const index = name.lastIndexOf('.');
  return index <= 0 ? '' : name.slice(index).toLowerCase();
}

export function CreateEntryDialog({ dir, textExts, onClose, onCreated }: CreateEntryDialogProps) {
  const [kind, setKind] = useState<'file' | 'dir'>('file');
  const [name, setName] = useState('');
  const [content, setContent] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const isFile = kind === 'file';
  const trimmed = name.trim();
  const editable = isFile && textExts.includes(extOf(trimmed));
  const localError = useMemo(() => (trimmed ? validate(trimmed) : ''), [trimmed]);

  const submit = async () => {
    const reason = validate(trimmed);
    if (reason) {
      setError(reason);
      return;
    }
    setBusy(true);
    setError('');
    try {
      await createEntry(dir, kind, trimmed, editable ? content : undefined);
      onCreated(trimmed, kind);
    } catch (exc) {
      setError(manageErrorMessage(exc, '创建失败'));
      setBusy(false);
    }
  };

  const footer = (
    <>
      <button type="button" className={buttonStyles.ghost} onClick={onClose} disabled={busy}>
        取消
      </button>
      <button
        type="button"
        className={buttonStyles.primary}
        onClick={() => void submit()}
        disabled={busy || !trimmed || Boolean(localError)}
      >
        {busy ? '创建中…' : '创建'}
      </button>
    </>
  );

  return (
    <Modal title="新建" onClose={onClose} footer={footer} testId="create-entry-dialog">
      <div className={styles.form}>
        <div className={styles.kinds} role="group" aria-label="新建类型">
          <button
            type="button"
            className={`${styles.kind}${isFile ? ` ${styles.kindActive}` : ''}`}
            aria-pressed={isFile}
            onClick={() => setKind('file')}
          >
            文件
          </button>
          <button
            type="button"
            className={`${styles.kind}${isFile ? '' : ` ${styles.kindActive}`}`}
            aria-pressed={!isFile}
            onClick={() => setKind('dir')}
          >
            文件夹
          </button>
        </div>

        <label className={styles.label}>
          <span>{isFile ? '文件名（含扩展名）' : '文件夹名'}</span>
          <input
            ref={inputRef}
            className={styles.input}
            value={name}
            placeholder={isFile ? 'example.md' : '新建文件夹'}
            autoComplete="off"
            spellCheck={false}
            aria-label={isFile ? '文件名' : '文件夹名'}
            onChange={(event) => {
              setName(event.target.value);
              setError('');
            }}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                void submit();
              }
            }}
          />
        </label>

        {editable ? (
          <label className={styles.label}>
            <span>初始内容（可留空）</span>
            <textarea
              className={styles.textarea}
              value={content}
              spellCheck={false}
              onChange={(event) => setContent(event.target.value)}
            />
          </label>
        ) : isFile ? (
          <p className={styles.hint}>该类型只创建空文件</p>
        ) : null}

        {error || localError ? <p className={styles.error}>{error || localError}</p> : null}
        <p className={styles.path}>创建于 /{dir}</p>
      </div>
    </Modal>
  );
}

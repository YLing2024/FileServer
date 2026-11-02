/**
 * 重命名对话框：实时校验名称并调用 `POST /api/rename`。
 *
 * 默认只选中主名（扩展名不参与），目录则全选。非法名称禁用确定；
 * 服务端 409（同目录同名）会原样显示中文原因。
 */
import { useEffect, useRef, useState } from 'react';
import { ApiError } from '../../api/errors';
import { renameEntry } from '../../api/manage';
import { Modal } from '../../components/Modal';
import buttonStyles from '../../styles/buttons.module.css';
import { manageErrorMessage } from './messages';
import styles from './RenameDialog.module.css';

interface RenameDialogProps {
  path: string;
  name: string;
  isDir: boolean;
  siblings: string[];
  onClose: () => void;
  onRenamed: (newPath: string) => void;
}

const RESERVED = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$/i;

function validate(value: string, current: string, siblings: string[]): string {
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
  if (value !== current) {
    const lower = value.toLowerCase();
    if (siblings.some((item) => item.toLowerCase() === lower)) return '同目录已存在同名项';
  }
  return '';
}

export function RenameDialog({
  path,
  name,
  isDir,
  siblings,
  onClose,
  onRenamed,
}: RenameDialogProps) {
  const [value, setValue] = useState(name);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.focus();
    let end = name.length;
    if (!isDir) {
      const dot = name.lastIndexOf('.');
      if (dot > 0) end = dot;
    }
    try {
      input.setSelectionRange(0, end);
    } catch {
      // 某些输入类型不支持选区时忽略，不影响改名。
    }
  }, [isDir, name]);

  const message = validate(value, name, siblings);
  const submit = async (): Promise<void> => {
    if (busy || message || value === name) return;
    setBusy(true);
    setError('');
    try {
      const result = await renameEntry(path, value);
      onRenamed(result.path);
    } catch (submitError) {
      if (submitError instanceof ApiError && submitError.status === 409) {
        setError('同名文件或文件夹已存在');
      } else {
        setError(manageErrorMessage(submitError, '重命名失败'));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title="重命名"
      onClose={busy ? () => undefined : onClose}
      testId="rename-dialog"
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
            type="button"
            className={`${buttonStyles.button} ${buttonStyles.primary}`}
            disabled={busy || Boolean(message) || value === name}
            onClick={() => {
              void submit();
            }}
            data-testid="rename-submit"
          >
            {busy ? '保存中…' : '确定'}
          </button>
        </>
      }
    >
      <form
        className={styles.form}
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <label className={styles.label}>
          新名称
          <input
            ref={inputRef}
            className={styles.input}
            type="text"
            value={value}
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => {
              setValue(event.target.value);
              setError('');
            }}
          />
        </label>
        <p className={styles.hint}>扩展名不参与默认选中，可直接修改主名。</p>
        {message && value !== name ? (
          <p className={styles.error} role="alert">
            {message}
          </p>
        ) : null}
        {error ? (
          <p className={styles.error} role="alert">
            {error}
          </p>
        ) : null}
      </form>
    </Modal>
  );
}

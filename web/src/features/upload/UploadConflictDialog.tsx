/**
 * 上传同名冲突对话框：覆盖 / 自动改名 / 跳过。
 *
 * 覆盖与自动改名统一设置本批次模式；跳过只处理当前文件或当前目录分组，
 * 其余文件继续上传。对话框不可通过遮罩/Esc 关闭，必须明确选择。
 */
import { Modal } from '../../components/Modal';
import buttonStyles from '../../styles/buttons.module.css';
import type { UploadConflictView } from './types';
import styles from './UploadConflictDialog.module.css';

interface UploadConflictDialogProps {
  conflict: UploadConflictView;
  onResolve: (choice: 'overwrite' | 'rename' | 'skip') => void;
}

export function UploadConflictDialog({ conflict, onResolve }: UploadConflictDialogProps) {
  const subject =
    conflict.kind === 'group' ? `文件夹「${conflict.subject}」` : `文件「${conflict.subject}」`;
  return (
    <Modal
      title={conflict.kind === 'group' ? '文件夹已存在' : '文件已存在'}
      onClose={() => onResolve('skip')}
      dismissible={false}
      testId="upload-conflict"
      footer={
        <>
          <button
            type="button"
            className={`${buttonStyles.button} ${buttonStyles.ghost}`}
            onClick={() => onResolve('skip')}
            data-testid="conflict-skip"
          >
            跳过
          </button>
          <button
            type="button"
            className={buttonStyles.button}
            onClick={() => onResolve('rename')}
            data-testid="conflict-rename"
          >
            自动改名
          </button>
          <button
            type="button"
            className={`${buttonStyles.button} ${buttonStyles.primary}`}
            onClick={() => onResolve('overwrite')}
            data-testid="conflict-overwrite"
          >
            覆盖
          </button>
        </>
      }
    >
      <p className={styles.hint}>
        {subject}在目标目录中已存在。覆盖与自动改名对本次上传生效；跳过只处理当前项。
      </p>
      <ul className={styles.list} aria-label="已存在的名称">
        {conflict.names.map((name) => (
          <li key={name} className={styles.name}>
            {name}
          </li>
        ))}
      </ul>
    </Modal>
  );
}

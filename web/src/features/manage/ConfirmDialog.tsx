/**
 * 通用确认对话框：危险操作二次确认，可选第三个动作（冲突时的“自动改名”）。
 *
 * Props：`onConfirm`/`onCancel` 至少一个会被调用；`thirdLabel` 存在时显示
 * 第三个按钮并回调 `onThird`。按钮顺序固定为 取消 / 第三动作 / 确认。
 */
import { Modal } from '../../components/Modal';
import buttonStyles from '../../styles/buttons.module.css';
import styles from './ConfirmDialog.module.css';

interface ConfirmDialogProps {
  title: string;
  message: string;
  confirmLabel: string;
  cancelLabel?: string;
  danger?: boolean;
  thirdLabel?: string;
  onConfirm: () => void;
  onThird?: () => void;
  onCancel: () => void;
  testId?: string;
}

export function ConfirmDialog({
  title,
  message,
  confirmLabel,
  cancelLabel = '取消',
  danger = false,
  thirdLabel,
  onConfirm,
  onThird,
  onCancel,
  testId = 'confirm-dialog',
}: ConfirmDialogProps) {
  return (
    <Modal
      title={title}
      onClose={onCancel}
      testId={testId}
      footer={
        <>
          <button type="button" className={`${buttonStyles.button} ${buttonStyles.ghost}`} onClick={onCancel}>
            {cancelLabel}
          </button>
          {thirdLabel && onThird ? (
            <button type="button" className={buttonStyles.button} onClick={onThird}>
              {thirdLabel}
            </button>
          ) : null}
          <button
            type="button"
            className={`${buttonStyles.button} ${danger ? buttonStyles.danger : buttonStyles.primary}`}
            onClick={onConfirm}
            data-testid="confirm-ok"
          >
            {confirmLabel}
          </button>
        </>
      }
    >
      <p className={styles.message}>{message}</p>
    </Modal>
  );
}

/** 批量操作失败明细：逐条列出路径与中文原因。 */
import type { ManageFailure } from '../../api/manage';
import { Modal } from '../../components/Modal';
import buttonStyles from '../../styles/buttons.module.css';
import styles from './FailuresDialog.module.css';

interface FailuresDialogProps {
  title: string;
  failed: ManageFailure[];
  onClose: () => void;
}

export function FailuresDialog({ title, failed, onClose }: FailuresDialogProps) {
  return (
    <Modal
      title={title}
      onClose={onClose}
      testId="failures-dialog"
      footer={
        <button type="button" className={buttonStyles.button} onClick={onClose}>
          知道了
        </button>
      }
    >
      <ul className={styles.list}>
        {failed.map((item, index) => (
          <li key={`${item.path}-${index}`} className={styles.item}>
            <span className={styles.path}>{item.path}</span>
            <span className={styles.reason}>{item.error || '操作失败'}</span>
          </li>
        ))}
      </ul>
    </Modal>
  );
}

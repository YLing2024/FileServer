/** 整页拖拽投放遮罩：仅提示“松开即可上传”，不拦截实际数据读取。 */
import styles from './DropOverlay.module.css';

export function DropOverlay() {
  return (
    <div className={styles.overlay} data-testid="drop-overlay" aria-hidden="true">
      <div className={styles.card}>
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 16V4m0 0L7 9m5-5l5 5" />
          <path d="M4 16v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3" />
        </svg>
        <strong>松开即可上传</strong>
        <span>上传到当前目录</span>
      </div>
    </div>
  );
}

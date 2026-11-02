/**
 * 通用模态框：遮罩、`role="dialog"`、Esc 关闭与初始焦点。
 *
 * Props：
 * - `title`：对话框标题，用于 aria-labelledby；
 * - `onClose`：关闭回调（Esc、遮罩点击或右上角关闭按钮）；
 * - `dismissible`：默认 true；冲突确认类对话框可关闭遮罩/Esc 关闭；
 * - `footer`：底部操作区，通常放原生 button；
 * - `testId`：供端到端测试定位。
 *
 * 注意：打开时把焦点移到第一个可聚焦控件；关闭后恢复到打开前的元素。
 * 不使用 portal，固定定位由样式层保证。
 */
import { useEffect, useId, useRef, type ReactNode } from 'react';
import styles from './Modal.module.css';

interface ModalProps {
  title: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  dismissible?: boolean;
  testId?: string;
  wide?: boolean;
}

export function Modal({
  title,
  onClose,
  children,
  footer,
  dismissible = true,
  testId,
  wide = false,
}: ModalProps) {
  const titleId = useId();
  const boxRef = useRef<HTMLDivElement | null>(null);
  const closeRef = useRef(onClose);

  useEffect(() => {
    closeRef.current = onClose;
  });

  useEffect(() => {
    const previous = document.activeElement;
    const box = boxRef.current;
    const first = box?.querySelector<HTMLElement>(
      'input:not([type="hidden"]), textarea, select, button',
    );
    first?.focus();
    const onKeyDown = (event: KeyboardEvent): void => {
      if (!dismissible) return;
      if (event.key === 'Escape') {
        event.preventDefault();
        closeRef.current();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      if (previous instanceof HTMLElement) previous.focus();
    };
  }, [dismissible]);

  return (
    <div
      className={styles.backdrop}
      onMouseDown={(event) => {
        if (dismissible && event.target === event.currentTarget) onClose();
      }}
    >
      <div
        ref={boxRef}
        className={`${styles.box} ${wide ? styles.wide : ''}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        data-testid={testId}
      >
        <div className={styles.head}>
          <h2 id={titleId} className={styles.title}>
            {title}
          </h2>
          {dismissible ? (
            <button
              type="button"
              className={styles.close}
              aria-label="关闭"
              title="关闭"
              onClick={onClose}
            >
              ×
            </button>
          ) : null}
        </div>
        <div className={styles.body}>{children}</div>
        {footer ? <div className={styles.footer}>{footer}</div> : null}
      </div>
    </div>
  );
}

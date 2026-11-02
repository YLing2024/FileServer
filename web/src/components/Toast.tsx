/**
 * 全局轻提示。
 *
 * 用途：异步操作失败/成功的统一反馈，避免每个页面各写一份。
 * Props：ToastProvider 只接收 children；useToast() 返回 showToast(text, tone?)，
 * tone 默认 info，error 仅改变强调色，不改变自动消失时间（3 秒）。
 * 可访问性：容器 aria-live="polite"，每条提示带“关闭”按钮，键盘可操作。
 * 注意：不要在渲染期间调用 showToast，放到事件回调或 effect 里。
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import styles from './Toast.module.css';

export type ToastTone = 'info' | 'error';

interface ToastItem {
  id: number;
  text: string;
  tone: ToastTone;
}

interface ToastApi {
  showToast: (text: string, tone?: ToastTone) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

/** 读取当前 Toast 上下文；必须位于 ToastProvider 内。 */
export function useToast(): ToastApi {
  const context = useContext(ToastContext);
  if (!context) throw new Error('useToast 必须在 ToastProvider 内使用');
  return context;
}

const TOAST_TTL_MS = 3000;

/** ToastProvider：挂载一次即可，内部维护提示队列与自动消失定时器。 */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const seq = useRef(0);
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>());

  const dismiss = useCallback((id: number) => {
    setItems((current) => current.filter((item) => item.id !== id));
    const timer = timers.current.get(id);
    if (timer !== undefined) {
      clearTimeout(timer);
      timers.current.delete(id);
    }
  }, []);

  const showToast = useCallback(
    (text: string, tone: ToastTone = 'info') => {
      seq.current += 1;
      const id = seq.current;
      setItems((current) => [...current.slice(-2), { id, text, tone }]);
      timers.current.set(
        id,
        setTimeout(() => dismiss(id), TOAST_TTL_MS),
      );
    },
    [dismiss],
  );

  useEffect(() => {
    const pending = timers.current;
    return () => {
      for (const timer of pending.values()) clearTimeout(timer);
      pending.clear();
    };
  }, []);

  const api = useMemo(() => ({ showToast }), [showToast]);

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className={styles.viewport} aria-live="polite" aria-atomic="false">
        {items.map((item) => (
          <div
            key={item.id}
            className={`${styles.toast} ${item.tone === 'error' ? styles.error : ''}`}
            role={item.tone === 'error' ? 'alert' : 'status'}
          >
            <span className={styles.text}>{item.text}</span>
            <button
              type="button"
              className={styles.close}
              aria-label="关闭提示"
              onClick={() => dismiss(item.id)}
            >
              ×
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

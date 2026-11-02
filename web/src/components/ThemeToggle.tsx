/**
 * 深浅主题切换。
 *
 * 沿用旧前端约定：localStorage 键 fs.theme，值为 dark/light；没有记录时跟随
 * prefers-color-scheme。主题落在 <html data-theme> 上，颜色全部来自既有
 * --bg/--text/--accent 等变量，不新增设计令牌。
 *
 * 可访问性：原生 button，aria-label 固定为“切换主题”，title 给出目标主题。
 */
import { useCallback, useEffect, useState } from 'react';
import styles from './ThemeToggle.module.css';

export type Theme = 'dark' | 'light';

export const THEME_KEY = 'fs.theme';

function isTheme(value: string | null): value is Theme {
  return value === 'dark' || value === 'light';
}

/** 计算应使用的主题：已保存值优先，否则跟随系统。 */
export function preferredTheme(): Theme {
  try {
    const saved = localStorage.getItem(THEME_KEY);
    if (isTheme(saved)) return saved;
  } catch {
    // 存储不可用时仍要能渲染页面，回退系统偏好。
  }
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

/** 应用主题并持久化；供启动时初始化与组件切换共用。 */
export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  try {
    localStorage.setItem(THEME_KEY, theme);
  } catch {
    // 隐私模式下写入失败不影响本次会话的视觉状态。
  }
}

/** 启动时调用：只应用、不写回 localStorage（避免把系统偏好固化成用户选择）。 */
export function initTheme(): Theme {
  const theme = preferredTheme();
  document.documentElement.dataset.theme = theme;
  return theme;
}

/** 主题切换按钮；同一浏览器多标签页切换时通过 storage 事件同步。 */
export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(() =>
    document.documentElement.dataset.theme === 'light' ? 'light' : 'dark',
  );

  useEffect(() => {
    const onStorage = (event: StorageEvent): void => {
      if (event.key === THEME_KEY && isTheme(event.newValue)) {
        document.documentElement.dataset.theme = event.newValue;
        setTheme(event.newValue);
      }
    };
    window.addEventListener('storage', onStorage);
    return () => window.removeEventListener('storage', onStorage);
  }, []);

  const toggle = useCallback(() => {
    setTheme((current) => {
      const next: Theme = current === 'dark' ? 'light' : 'dark';
      applyTheme(next);
      return next;
    });
  }, []);

  const target = theme === 'dark' ? '浅色' : '深色';

  return (
    <button
      type="button"
      className={styles.button}
      aria-label="切换主题"
      title={`切换到${target}主题`}
      onClick={toggle}
    >
      {theme === 'dark' ? (
        <svg className={styles.icon} viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
        </svg>
      ) : (
        <svg className={styles.icon} viewBox="0 0 24 24" aria-hidden="true">
          <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5z" />
        </svg>
      )}
    </button>
  );
}

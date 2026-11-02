/**
 * V2 应用外壳：顶栏 + 主题切换 + 目录浏览。
 *
 * Phase 0 只挂最小浏览视图；后续视图（预览/播放/上传/管理）按 features 逐步接入，
 * 不在这里预埋假入口。401 通过 API 层的全局回调统一提示，不在各页面重复处理。
 */
import { useEffect } from 'react';
import { onUnauthorized } from './api/client';
import { ErrorBoundary } from './components/ErrorBoundary';
import { ThemeToggle } from './components/ThemeToggle';
import { ToastProvider, useToast } from './components/Toast';
import { BrowsePage } from './features/browse/BrowsePage';
import styles from './App.module.css';

function Shell() {
  const { showToast } = useToast();

  useEffect(
    () => onUnauthorized(() => showToast('登录状态已失效，请重新登录', 'error')),
    [showToast],
  );

  return (
    <div className={styles.app}>
      <header className={styles.toolbar}>
        <div className={styles.brand}>
          <svg className={styles.brandMark} viewBox="0 0 512 512" aria-hidden="true">
            <rect x="128" y="112" width="256" height="288" rx="20" />
            <line x1="128" y1="208" x2="384" y2="208" />
            <line x1="128" y1="304" x2="384" y2="304" />
            <rect x="232" y="148" width="48" height="22" rx="11" className={styles.slot} />
            <rect x="232" y="244" width="48" height="22" rx="11" className={styles.slotActive} />
            <rect x="232" y="340" width="48" height="22" rx="11" className={styles.slot} />
          </svg>
          <span className={styles.brandName}>文件服务器</span>
          <span className={styles.badge}>V2 预览</span>
        </div>
        <ThemeToggle />
      </header>
      <main>
        <BrowsePage />
      </main>
    </div>
  );
}

/** 根组件：错误边界 + Toast 上下文，再渲染应用外壳。 */
export default function App() {
  return (
    <ErrorBoundary>
      <ToastProvider>
        <Shell />
      </ToastProvider>
    </ErrorBoundary>
  );
}

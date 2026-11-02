/**
 * V2 应用外壳：错误边界 + Toast + 浏览页。
 *
 * 401 由 API 层统一清凭据并回调；V2 的登录页属于 Phase 4，当前跳回旧前端 `/`
 * （认证开启时旧前端会显示登录覆盖层）。浏览视图本身不重复处理鉴权。
 */
import { useEffect } from 'react';
import { onUnauthorized } from './api/client';
import { ErrorBoundary } from './components/ErrorBoundary';
import { ToastProvider } from './components/Toast';
import { BrowsePage } from './features/browse/BrowsePage';
import styles from './App.module.css';

function Shell() {
  useEffect(
    () =>
      onUnauthorized(() => {
        window.location.assign('/');
      }),
    [],
  );

  return (
    <div className={styles.app}>
      <BrowsePage />
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

/**
 * V2 应用外壳：错误边界 + Toast + 认证门禁 + 浏览页。
 *
 * 401 由 API 层统一清凭据并回调；这里清掉短票并让 AuthGate 重新挂载，
 * 用户留在 V2 直接重新登录（不再跳回旧前端 `/`）。
 * 首次 TOTP 绑定仍由旧前端完成，AuthGate 会给出明确入口。
 */
import { useEffect, useState } from 'react';
import { onUnauthorized } from './api/client';
import { clearTicket } from './api/media';
import { ErrorBoundary } from './components/ErrorBoundary';
import { ToastProvider } from './components/Toast';
import { AuthGate } from './features/auth/AuthGate';
import { BrowsePage } from './features/browse/BrowsePage';
import styles from './App.module.css';

function Shell() {
  const [notice, setNotice] = useState('');
  const [epoch, setEpoch] = useState(0);

  useEffect(
    () =>
      onUnauthorized(() => {
        clearTicket();
        setNotice('登录状态已失效，请重新登录');
        setEpoch((current) => current + 1);
      }),
    [],
  );

  return (
    <div className={styles.app}>
      <AuthGate key={epoch} notice={notice}>
        <BrowsePage />
      </AuthGate>
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

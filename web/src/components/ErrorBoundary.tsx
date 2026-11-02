/**
 * 错误边界：捕获子树渲染异常，避免整页白屏。
 *
 * Props：children（必填）；fallback 可替换默认提示。默认提供“重试”按钮，
 * 重置边界状态并重新挂载子树。仅捕获渲染/生命周期错误，不捕获事件回调与
 * 异步错误（那些交给 Toast 或调用方）。
 */
import { Component, type ErrorInfo, type ReactNode } from 'react';
import styles from './ErrorBoundary.module.css';

interface ErrorBoundaryProps {
  children: ReactNode;
  fallback?: ReactNode;
}

interface ErrorBoundaryState {
  error: Error | null;
}

/** 渲染错误兜底组件；用法：<ErrorBoundary><App /></ErrorBoundary>。 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // 生产环境没有集中日志，保留控制台错误便于用户反馈时定位组件栈。
    console.error('前端渲染失败', error, info.componentStack);
  }

  private reset = (): void => {
    this.setState({ error: null });
  };

  render(): ReactNode {
    const { error } = this.state;
    if (!error) return this.props.children;
    if (this.props.fallback) return this.props.fallback;

    return (
      <div className={styles.wrap} role="alert">
        <h1 className={styles.title}>页面暂时无法显示</h1>
        <p className={styles.detail}>{error.message}</p>
        <button type="button" className={styles.retry} onClick={this.reset}>
          重试
        </button>
      </div>
    );
  }
}

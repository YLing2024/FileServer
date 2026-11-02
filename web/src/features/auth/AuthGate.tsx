/**
 * 认证门禁：认证开启时先探测 `/api/auth/status`，未登录则内嵌登录表单。
 *
 * 与旧前端保持一致：勾选“记住我”写 localStorage，否则写 sessionStorage；
 * 首次设置（口令 / TOTP 绑定）也由默认入口承接，不再依赖已删除的旧前端。
 */
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { authModeLabel, fetchAuthStatus, login, type AuthStatus } from '../../api/auth';
import { ApiError, errorMessage } from '../../api/errors';
import buttonStyles from '../../styles/buttons.module.css';
import { SetupForm } from './SetupForm';
import styles from './AuthGate.module.css';

interface AuthGateProps {
  children: ReactNode;
  /** 登录态失效等提示；为空时不显示提示条。 */
  notice?: string;
}

type Phase = 'loading' | 'error' | 'ready';

/** 认证门禁；认证关闭或已登录时直接渲染 children。 */
export function AuthGate({ children, notice = '' }: AuthGateProps) {
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [phase, setPhase] = useState<Phase>('loading');
  const [error, setError] = useState('');
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [remember, setRemember] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    setPhase('loading');
    setError('');
    try {
      const next = await fetchAuthStatus(signal);
      if (signal?.aborted) return;
      setStatus(next);
      setPhase('ready');
    } catch (loadError) {
      if (signal?.aborted) return;
      setError(errorMessage(loadError, '无法读取认证状态'));
      setPhase('error');
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 挂载后立即探测登录状态
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy || !status) return;
    setBusy(true);
    setError('');
    try {
      await login({ password, code: code.trim(), remember });
      setStatus({ ...status, authenticated: true });
    } catch (loginError) {
      if (loginError instanceof ApiError && loginError.status === 429) {
        setError('尝试过于频繁，请稍后再试');
      } else {
        setError(errorMessage(loginError, '登录失败'));
      }
    } finally {
      setBusy(false);
    }
  };

  if (phase === 'loading') {
    return <p className={styles.center}>正在检查登录状态…</p>;
  }

  if (phase === 'error') {
    return (
      <div className={styles.center}>
        <p className={styles.error} role="alert">
          {error}
        </p>
        <button
          type="button"
          className={`${buttonStyles.button} ${buttonStyles.small}`}
          onClick={() => {
            void load();
          }}
        >
          重试
        </button>
      </div>
    );
  }

  if (!status || !status.enabled || status.authenticated) return <>{children}</>;

  if (status.unavailable) {
    return (
      <div className={styles.page}>
        <div className={styles.card}>
          <h1 className={styles.title}>认证不可用</h1>
          <p className={styles.hint}>服务端认证初始化失败，请检查 .FileServer/auth.json。</p>
        </div>
      </div>
    );
  }

  if (status.needs_setup) {
    return <SetupForm onDone={() => void load()} />;
  }

  const needPassword =
    status.password_set === true || status.mode === 'password' || status.mode === 'password+totp';
  const needCode = status.totp_bound === true;

  return (
    <div className={styles.page}>
      <form className={styles.card} onSubmit={(event) => void submit(event)}>
        <h1 className={styles.title}>登录</h1>
        <p className={styles.hint}>{authModeLabel(status.mode)}</p>
        {notice ? (
          <p className={styles.notice} role="status">
            {notice}
          </p>
        ) : null}

        {needPassword ? (
          <label className={styles.label}>
            访问口令
            <input
              className={styles.input}
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoFocus
              data-testid="login-password"
            />
          </label>
        ) : null}

        {needCode ? (
          <label className={styles.label}>
            6 位验证码
            <input
              className={styles.input}
              type="text"
              inputMode="numeric"
              maxLength={6}
              autoComplete="one-time-code"
              value={code}
              onChange={(event) => setCode(event.target.value)}
              data-testid="login-code"
            />
          </label>
        ) : null}

        <label className={styles.remember}>
          <input
            type="checkbox"
            checked={remember}
            onChange={(event) => setRemember(event.target.checked)}
            data-testid="login-remember"
          />
          记住我（7 天内免登录）
        </label>
        <p className={styles.hint}>不勾选：关闭浏览器后需要重新登录</p>

        {error ? (
          <p className={styles.error} role="alert" data-testid="login-error">
            {error}
          </p>
        ) : null}

        <button
          type="submit"
          className={`${buttonStyles.button} ${buttonStyles.primary}`}
          disabled={busy}
          data-testid="login-submit"
        >
          {busy ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  );
}

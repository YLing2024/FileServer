/**
 * 首次设置：认证开启且尚未配置时，在新前端内完成口令设置与 TOTP 绑定。
 *
 * 旧前端删除后，引导流程必须由默认入口承接；二维码与提交仍走
 * `/api/auth/setup` 与 `/api/auth/totp-qr`，不新增服务端契约。
 */
import { useEffect, useState, type FormEvent } from 'react';
import { completeSetup, fetchSetup, type SetupStatus } from '../../api/auth';
import { errorMessage } from '../../api/errors';
import buttonStyles from '../../styles/buttons.module.css';
import styles from './AuthGate.module.css';

interface SetupFormProps {
  /** 配置完成后由父组件重新探测登录状态。 */
  onDone: () => void;
}

/** 首次设置表单：按认证模式显示口令、二维码与验证码。 */
export function SetupForm({ onDone }: SetupFormProps) {
  const [info, setInfo] = useState<SetupStatus | null>(null);
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const controller = new AbortController();
    fetchSetup(controller.signal)
      .then((next) => {
        if (controller.signal.aborted) return;
        setInfo(next);
        setLoading(false);
      })
      .catch((loadError: unknown) => {
        if (controller.signal.aborted) return;
        setError(errorMessage(loadError, '无法读取设置信息'));
        setLoading(false);
      });
    return () => controller.abort();
  }, []);

  const submit = async (skipTOTP: boolean): Promise<void> => {
    if (busy || !info) return;
    setBusy(true);
    setError('');
    try {
      await completeSetup({
        password,
        code: code.trim(),
        skipTOTP,
      });
      onDone();
    } catch (setupError) {
      setError(errorMessage(setupError, '设置失败'));
    } finally {
      setBusy(false);
    }
  };

  const onSubmit = (event: FormEvent): void => {
    event.preventDefault();
    void submit(false);
  };

  if (loading) return <p className={styles.center}>正在读取设置信息…</p>;

  return (
    <div className={styles.page}>
      <form className={styles.card} onSubmit={onSubmit}>
        <h1 className={styles.title}>首次设置</h1>
        <p className={styles.hint}>{info ? `认证模式：${info.mode}` : '认证尚未配置'}</p>

        {info?.need_password ? (
          <label className={styles.label}>
            访问口令（至少 8 位）
            <input
              className={styles.input}
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoFocus
              data-testid="setup-password"
            />
          </label>
        ) : null}

        {info?.need_totp ? (
          <>
            <img
              className={styles.qr}
              src="/api/auth/totp-qr"
              alt="TOTP 绑定二维码"
              width={220}
              height={220}
            />
            {info.totp_secret ? (
              <p className={styles.secret}>
                无法扫码时手输密钥：<code>{info.totp_secret}</code>
              </p>
            ) : null}
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
                autoFocus={!info.need_password}
                data-testid="setup-code"
              />
            </label>
          </>
        ) : null}

        {error ? (
          <p className={styles.error} role="alert" data-testid="setup-error">
            {error}
          </p>
        ) : null}

        <button
          type="submit"
          className={`${buttonStyles.button} ${buttonStyles.primary}`}
          disabled={busy}
          data-testid="setup-submit"
        >
          {busy ? '正在保存…' : '完成设置'}
        </button>
        {info?.need_totp && info.mode === 'password+totp' ? (
          <button
            type="button"
            className={`${buttonStyles.button} ${buttonStyles.ghost}`}
            disabled={busy}
            onClick={() => void submit(true)}
            data-testid="setup-skip"
          >
            暂不绑定 TOTP
          </button>
        ) : null}
      </form>
    </div>
  );
}

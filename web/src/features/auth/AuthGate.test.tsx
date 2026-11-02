import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TOKEN_KEY } from '../../api/token';
import { AuthGate } from './AuthGate';

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function stubFetch(handlers: Record<string, () => Response>): ReturnType<typeof vi.fn> {
  const mock = vi.fn((input: RequestInfo | URL) => {
    const url = String(input);
    const handler = handlers[url];
    if (!handler) return Promise.resolve(json({}, 404));
    return Promise.resolve(handler());
  });
  vi.stubGlobal('fetch', mock);
  return mock;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('AuthGate', () => {
  it('认证关闭时直接渲染应用', async () => {
    stubFetch({
      '/api/auth/status': () => json({ enabled: false, mode: 'none', authenticated: true }),
    });
    render(
      <AuthGate>
        <div>APP</div>
      </AuthGate>,
    );
    expect(await screen.findByText('APP')).toBeTruthy();
  });

  it('未登录时显示登录表单，成功后渲染应用并写入 sessionStorage', async () => {
    stubFetch({
      '/api/auth/status': () =>
        json({
          enabled: true,
          mode: 'password',
          authenticated: false,
          needs_setup: false,
          password_set: true,
        }),
      '/api/auth/login': () => json({ ok: true, token: 'new-token', expires: 123 }),
    });

    render(
      <AuthGate>
        <div>APP</div>
      </AuthGate>,
    );

    const password = await screen.findByTestId('login-password');
    fireEvent.change(password, { target: { value: 'secret' } });
    fireEvent.click(screen.getByTestId('login-submit'));

    expect(await screen.findByText('APP')).toBeTruthy();
    expect(sessionStorage.getItem(TOKEN_KEY)).toBe('new-token');
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
  });

  it('登录失败显示服务端文案并停留在表单', async () => {
    stubFetch({
      '/api/auth/status': () =>
        json({
          enabled: true,
          mode: 'password',
          authenticated: false,
          needs_setup: false,
          password_set: true,
        }),
      '/api/auth/login': () => json({ error: '访问凭据无效' }, 401),
    });

    render(
      <AuthGate>
        <div>APP</div>
      </AuthGate>,
    );

    await screen.findByTestId('login-password');
    fireEvent.click(screen.getByTestId('login-submit'));
    await waitFor(() => {
      expect(screen.getByTestId('login-error').textContent).toContain('访问凭据无效');
    });
    expect(screen.queryByText('APP')).toBeNull();
  });

  it('首次设置在新前端内显示口令与 TOTP 表单', async () => {
    stubFetch({
      '/api/auth/status': () =>
        json({
          enabled: true,
          mode: 'password+totp',
          authenticated: false,
          needs_setup: true,
        }),
      '/api/auth/setup': () =>
        json({
          mode: 'password+totp',
          need_password: true,
          need_totp: true,
          totp_secret: 'JBSWY3DPEHPK3PXP',
          otpauth_url: 'otpauth://totp/FileServer?secret=JBSWY3DPEHPK3PXP',
        }),
    });
    render(
      <AuthGate>
        <div>APP</div>
      </AuthGate>,
    );
    expect(await screen.findByText('首次设置')).toBeTruthy();
    expect(await screen.findByTestId('setup-password')).toBeTruthy();
    expect(screen.getByTestId('setup-code')).toBeTruthy();
    expect(screen.getByTestId('setup-submit')).toBeTruthy();
  });

  it('登录失效提示显示在表单顶部', async () => {
    stubFetch({
      '/api/auth/status': () =>
        json({
          enabled: true,
          mode: 'password',
          authenticated: false,
          needs_setup: false,
          password_set: true,
        }),
    });
    render(
      <AuthGate notice="登录状态已失效，请重新登录">
        <div>APP</div>
      </AuthGate>,
    );
    expect(await screen.findByText('登录状态已失效，请重新登录')).toBeTruthy();
  });
});

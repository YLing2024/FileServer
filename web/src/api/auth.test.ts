import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './errors';
import { fetchAuthStatus, login } from './auth';
import { TOKEN_KEY } from './token';

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('auth api', () => {
  it('解析认证状态的可选字段', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          json({
            enabled: true,
            mode: 'password+totp',
            authenticated: false,
            needs_setup: false,
            password_set: true,
            totp_bound: true,
          }),
        ),
      ),
    );

    await expect(fetchAuthStatus()).resolves.toMatchObject({
      enabled: true,
      mode: 'password+totp',
      authenticated: false,
      password_set: true,
      totp_bound: true,
    });
  });

  it('记住我写入 localStorage，不勾选写入 sessionStorage', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ token: 'long-token', expires: 100 }))
      .mockResolvedValueOnce(json({ token: 'short-token', expires: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await login({ password: 'pw', code: '', remember: true });
    expect(localStorage.getItem(TOKEN_KEY)).toBe('long-token');
    expect(sessionStorage.getItem(TOKEN_KEY)).toBeNull();

    await login({ password: 'pw', code: '', remember: false });
    expect(sessionStorage.getItem(TOKEN_KEY)).toBe('short-token');
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
  });

  it('登录失败抛出 ApiError 且不写入凭据', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ error: '访问凭据无效' }, 401))));

    await expect(login({ password: 'x', code: '', remember: true })).rejects.toMatchObject({
      status: 401,
      message: '访问凭据无效',
    });
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(sessionStorage.getItem(TOKEN_KEY)).toBeNull();
  });

  it('响应缺少 token 时视为失败', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ ok: true }))));

    await expect(login({ password: 'x', code: '', remember: false })).rejects.toBeInstanceOf(
      ApiError,
    );
    expect(sessionStorage.getItem(TOKEN_KEY)).toBeNull();
  });
});

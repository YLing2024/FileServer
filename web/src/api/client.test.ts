import { describe, expect, it, vi } from 'vitest';
import { ApiError } from './errors';
import { onUnauthorized, requestJson } from './client';
import { TOKEN_KEY } from './token';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function headersOf(call: unknown[]): Headers {
  const init = call[1] as RequestInit;
  return init.headers as Headers;
}

describe('api client', () => {
  it('有 token 时同时发送 X-Auth 与 Authorization，且无 token 时不带鉴权头', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse({ ok: true })));
    vi.stubGlobal('fetch', fetchMock);

    await requestJson('/api/list?path=');
    expect(headersOf(fetchMock.mock.calls[0] ?? []).has('Authorization')).toBe(false);
    expect(headersOf(fetchMock.mock.calls[0] ?? []).has('X-Auth')).toBe(false);

    localStorage.setItem(TOKEN_KEY, 'token-123');
    await requestJson('/api/list?path=');
    const headers = headersOf(fetchMock.mock.calls[1] ?? []);
    expect(headers.get('Authorization')).toBe('Bearer token-123');
    expect(headers.get('X-Auth')).toBe('token-123');
  });

  it('401 清空两处凭据并触发全局回调', async () => {
    localStorage.setItem(TOKEN_KEY, 'expired');
    sessionStorage.setItem(TOKEN_KEY, 'expired');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ error: '未认证' }, 401)));
    const handler = vi.fn();
    const unsubscribe = onUnauthorized(handler);

    await expect(requestJson('/api/list?path=')).rejects.toMatchObject({
      status: 401,
      message: '登录状态已失效',
    });
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(sessionStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(handler).toHaveBeenCalledTimes(1);
    unsubscribe();
  });

  it('非 JSON 错误与业务 error 字段都归一为 ApiError', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: '目录不存在', code: 'not_found' }, 404))
      .mockResolvedValueOnce(new Response('bad gateway', { status: 502 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(requestJson('/api/list')).rejects.toMatchObject({
      status: 404,
      message: '目录不存在',
      code: 'not_found',
    });
    await expect(requestJson('/api/list')).rejects.toMatchObject({
      status: 502,
      message: 'bad gateway',
    });
  });

  it('网络失败归一为 status=0 的 ApiError', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('failed')));
    const error = await requestJson('/api/list').catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 0, message: '网络请求失败，请检查连接' });
  });
});

/**
 * 认证接口：状态探测与登录。
 *
 * 登录必须用裸 fetch：登录失败本身就是 401，若走 `requestJson` 会被全局
 * “登录失效”逻辑清凭据并触发回调，用户只会看到笼统的跳转/提示。
 * 登录成功后按“记住我”写入 sessionStorage 或 localStorage。
 */
import { ApiError } from './errors';
import { readToken, writeToken } from './token';

/** `/api/auth/status` 响应。字段按认证模式可选。 */
export interface AuthStatus {
  enabled: boolean;
  mode: string;
  authenticated: boolean;
  needs_setup: boolean;
  unavailable?: boolean;
  configured?: boolean;
  need_password?: boolean;
  need_totp?: boolean;
  password_set?: boolean;
  totp_bound?: boolean;
}

/** 登录表单输入。 */
export interface LoginInput {
  password: string;
  code: string;
  remember: boolean;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

async function parseBody(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return null;
  }
}

/** 读取认证状态（免鉴权接口）。带上本地凭据，服务端据此返回 authenticated。 */
export async function fetchAuthStatus(signal?: AbortSignal): Promise<AuthStatus> {
  const headers = new Headers();
  const token = readToken();
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
    headers.set('X-Auth', token);
  }
  const response = await fetch('/api/auth/status', {
    signal,
    cache: 'no-store',
    headers,
  });
  const body = await parseBody(response);
  if (!response.ok || !isRecord(body)) {
    throw new ApiError(response.status, '认证状态读取失败');
  }
  return {
    enabled: body.enabled === true,
    mode: typeof body.mode === 'string' ? body.mode : 'none',
    authenticated: body.authenticated === true,
    needs_setup: body.needs_setup === true,
    unavailable: body.unavailable === true,
    configured: body.configured === true,
    need_password: body.need_password === true,
    need_totp: body.need_totp === true,
    password_set: body.password_set === true,
    totp_bound: body.totp_bound === true,
  };
}

/**
 * 登录并写入凭据。失败抛出 ApiError，UI 只需要展示 message。
 * 429 由服务端统一文案给出，不在前端猜测锁定阈值。
 */
export async function login(input: LoginInput, signal?: AbortSignal): Promise<void> {
  let response: Response;
  try {
    response = await fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
      signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new ApiError(0, '网络请求失败，请检查连接');
  }

  const body = await parseBody(response);
  if (!response.ok) {
    const message = isRecord(body) && typeof body.error === 'string' ? body.error : '登录失败';
    throw new ApiError(response.status, message);
  }
  if (!isRecord(body) || typeof body.token !== 'string' || !body.token) {
    throw new ApiError(response.status, '服务端未返回登录凭据');
  }
  const expires = typeof body.expires === 'number' ? body.expires : 0;
  writeToken(body.token, expires, input.remember);
}

/** 认证模式的中文说明，与旧前端保持一致。 */
export function authModeLabel(mode: string): string {
  if (mode === 'totp') return '仅验证码（TOTP）';
  if (mode === 'password') return '仅访问口令';
  return '访问口令 + 验证码（双因素）';
}

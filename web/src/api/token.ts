/**
 * 会话 token 的读写收口。
 *
 * 沿用旧前端的存储约定：勾选“记住我”时写 localStorage，否则写 sessionStorage；
 * 键名固定为 fs.token / fs.token.expires。新前端不另造键，保证与旧前端、分享页
 * 在同一浏览器里共享登录态。
 */

export const TOKEN_KEY = 'fs.token';
export const TOKEN_EXP_KEY = 'fs.token.expires';

/** 一次读取到的凭据状态；remember 表示来源是否为 localStorage。 */
export interface TokenState {
  token: string;
  expires: number;
  remember: boolean;
}

function readFrom(store: Storage): TokenState | null {
  const token = store.getItem(TOKEN_KEY);
  if (!token) return null;
  return {
    token,
    expires: Number(store.getItem(TOKEN_EXP_KEY)) || 0,
    remember: store === localStorage,
  };
}

/** 读取当前 token 状态：sessionStorage 优先，其次 localStorage。 */
export function readTokenState(): TokenState | null {
  try {
    return readFrom(sessionStorage) ?? readFrom(localStorage);
  } catch {
    // 隐私模式或禁用存储时按未登录处理，页面仍可匿名浏览。
    return null;
  }
}

/** 读取当前 token；无凭据时返回空串。 */
export function readToken(): string {
  return readTokenState()?.token ?? '';
}

/** 清除两处存储中的凭据（401 或主动退出时调用）。 */
export function clearToken(): void {
  try {
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(TOKEN_EXP_KEY);
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(TOKEN_EXP_KEY);
  } catch {
    // 存储不可用时无需额外处理；调用方本来就要把用户当作未登录。
  }
}

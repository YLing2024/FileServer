/**
 * 类型化 fetch 封装：统一注入凭据、处理 401、归一错误。
 *
 * 鉴权约定：读取旧前端写入的 fs.token，同时发送 X-Auth（供后续网关/新后端使用）
 * 与 Authorization: Bearer（当前 Go 服务实际校验的通道）。无 token 时不发鉴权头，
 * 保持匿名访问语义（认证关闭时服务端本就敞开）。
 */
import { ApiError } from './errors';
import { clearToken, readToken } from './token';

type UnauthorizedHandler = () => void;

let unauthorizedHandler: UnauthorizedHandler | null = null;

/**
 * 注册 401 统一回调（清凭据后的提示/跳转由 UI 决定）。
 * 返回取消注册函数，避免组件卸载后仍触发旧回调。
 */
export function onUnauthorized(handler: UnauthorizedHandler): () => void {
  unauthorizedHandler = handler;
  return () => {
    if (unauthorizedHandler === handler) unauthorizedHandler = null;
  };
}

function authHeaders(init: RequestInit): Headers {
  const headers = new Headers(init.headers);
  const token = readToken();
  if (token) {
    if (!headers.has('X-Auth')) headers.set('X-Auth', token);
    if (!headers.has('Authorization')) headers.set('Authorization', `Bearer ${token}`);
  }
  return headers;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function errorFromResponse(status: number, body: unknown, text: string): ApiError {
  if (isRecord(body)) {
    const message = typeof body.error === 'string' ? body.error : '';
    const code = typeof body.code === 'string' ? body.code : undefined;
    if (message) return new ApiError(status, message, code);
  }
  return new ApiError(status, text.trim() || `请求失败（HTTP ${status}）`);
}

/** 发送请求并返回原始 Response；401 会清凭据、触发全局回调并抛出 ApiError。 */
export async function request(path: string, init: RequestInit = {}): Promise<Response> {
  let response: Response;
  try {
    response = await fetch(path, { ...init, headers: authHeaders(init) });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new ApiError(0, '网络请求失败，请检查连接');
  }

  if (response.status === 401) {
    clearToken();
    unauthorizedHandler?.();
    throw new ApiError(401, '登录状态已失效');
  }
  return response;
}

/** 发送请求并解析 JSON；非 2xx 或非法 JSON 统一抛出 ApiError。 */
export async function requestJson<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await request(path, init);
  const text = await response.text();

  let body: unknown = null;
  if (text) {
    try {
      body = JSON.parse(text) as unknown;
    } catch {
      if (!response.ok) throw errorFromResponse(response.status, null, text);
      throw new ApiError(response.status, '服务端返回了无法解析的响应');
    }
  }

  if (!response.ok) throw errorFromResponse(response.status, body, text);
  return body as T;
}

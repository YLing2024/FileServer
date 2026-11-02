/**
 * 会话管理接口：已登录设备列表、下线指定会话、退出当前会话。
 *
 * 服务端只返回会话元信息，不返回 token 明文；当前会话用 `current` 标记。
 */
import { requestJson } from './client';

/** 一个已登录会话。时间字段均为 Unix 秒。 */
export interface SessionEntry {
  id: string;
  created: number;
  last_seen: number;
  expires: number;
  remember: boolean;
  current: boolean;
  ua: string;
  ip: string;
}

interface SessionsResponse {
  sessions?: SessionEntry[];
}

/** 读取当前账号的全部会话。 */
export async function fetchSessions(signal?: AbortSignal): Promise<SessionEntry[]> {
  const body = await requestJson<SessionsResponse>('/api/auth/sessions', { signal });
  return Array.isArray(body.sessions) ? body.sessions : [];
}

/** 下线指定会话（不能下线当前会话，服务端会拒绝）。 */
export function revokeSession(id: string): Promise<{ ok: boolean }> {
  return requestJson<{ ok: boolean }>('/api/auth/sessions/revoke', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id }),
  });
}

/** 退出当前会话；服务端返回 204。 */
export async function logout(): Promise<void> {
  await requestJson<unknown>('/api/auth/logout', { method: 'POST' });
}

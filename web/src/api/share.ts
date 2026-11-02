/**
 * 分享管理接口：创建、列表、撤销、删除记录。
 *
 * 公开访问走 `/api/public/share/*`（见 `features/share` 公开页入口），
 * 这里只封装登录后管理接口。字段与 internal/server/share.go 保持一致。
 */
import { requestJson } from './client';

/** 分享状态：由服务端按记录字段与当前时间计算。 */
export type ShareStatus = 'active' | 'expired' | 'revoked' | 'exhausted';

/** 创建分享的请求体；`code` 为空表示不设提取码。 */
export interface ShareCreateInput {
  path: string;
  /** 有效期天数：0 永久，仅允许 1/7/30。 */
  days: number;
  code?: string;
  /** 下载次数上限：0 不限，仅允许 10/100。 */
  max_downloads: number;
  allow_preview: boolean;
  note: string;
}

/** `/api/share` 列表项（不包含源文件相对路径）。 */
export interface ShareListItem {
  token: string;
  /** 相对链接，形如 `/s/<token>`；展示时用 location.origin 拼绝对地址。 */
  url: string;
  name: string;
  is_dir: boolean;
  note: string;
  created: number;
  /** Unix 秒；0 表示永久有效。 */
  expires: number;
  downloads: number;
  max_downloads: number;
  allow_preview: boolean;
  code_set: boolean;
  status: ShareStatus;
}

/** 创建成功的响应，`code` 只在该次响应里返回。 */
export interface ShareCreateResult extends ShareListItem {
  code?: string;
}

interface ShareListResponse {
  shares?: ShareListItem[];
}

/** 创建分享。 */
export function createShare(input: ShareCreateInput): Promise<ShareCreateResult> {
  return requestJson<ShareCreateResult>('/api/share', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
}

/** 读取分享列表；服务端异常字段按空列表处理。 */
export async function listShares(signal?: AbortSignal): Promise<ShareListItem[]> {
  const body = await requestJson<ShareListResponse>('/api/share', { signal });
  return Array.isArray(body.shares) ? body.shares : [];
}

/** 撤销分享：记录保留，公开链接立即失效。 */
export function revokeShare(token: string): Promise<{ ok: boolean }> {
  return requestJson<{ ok: boolean }>('/api/share/revoke', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  });
}

/** 删除分享记录（不影响源文件）。 */
export function deleteShareRecord(token: string): Promise<{ ok: boolean }> {
  return requestJson<{ ok: boolean }>('/api/share/record', {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  });
}

/** 展示用绝对链接；服务端只返回相对路径，域名一律取运行时 origin。 */
export function absoluteShareUrl(url: string): string {
  if (/^https?:\/\//i.test(url)) return url;
  return `${window.location.origin}${url.startsWith('/') ? '' : '/'}${url}`;
}

/** 状态中文标签；未知状态原样展示，避免吞掉服务端新状态。 */
export const SHARE_STATUS_LABELS: Readonly<Record<ShareStatus, string>> = {
  active: '有效',
  expired: '已过期',
  revoked: '已撤销',
  exhausted: '次数用完',
};

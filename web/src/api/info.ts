/** 服务端能力探测：/api/info 与 /api/health。 */
import { requestJson } from './client';

/** /api/info 响应中前端用到的字段。 */
export interface ServerInfo {
  ffmpeg?: boolean;
  hls?: boolean;
  search_limit?: number;
  list_limit?: number;
}

/**
 * /api/health 响应。
 *
 * 当前后端只承诺进程/版本字段；`hls` 为可选字段，存在时优先作为冷门格式
 * 开关的初始状态，否则回退到 /api/info。
 */
export interface HealthInfo {
  ok?: boolean;
  hls?: boolean;
}

/** 读取服务端能力（扩展名映射、搜索/列表上限、ffmpeg/HLS 开关）。 */
export function fetchServerInfo(signal?: AbortSignal): Promise<ServerInfo> {
  return requestJson<ServerInfo>('/api/info', { signal });
}

/** 读取健康检查；认证开启时该端点仍匿名可用。 */
export function fetchHealth(signal?: AbortSignal): Promise<HealthInfo> {
  return requestJson<HealthInfo>('/api/health', { signal });
}

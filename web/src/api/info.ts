/** 服务端能力探测：/api/info 与 /api/health。 */
import { requestJson } from './client';

/** /api/info 响应中前端用到的字段。 */
export interface ServerInfo {
  /** 服务标识与版本（与 /api/health 同源，用于“关于”）。 */
  name?: string;
  version?: string;
  ffmpeg?: boolean;
  hls?: boolean;
  search_limit?: number;
  list_limit?: number;
  /** 上传总开关（`--allow-upload`）。关闭时工具栏不渲染上传入口。 */
  allow_upload?: boolean;
  /** 单文件上传上限（字节）。 */
  upload_max_bytes?: number;
  /** 超过该大小走分片上传（字节）。 */
  upload_chunk_threshold?: number;
  /** 单个分片上限（字节）。 */
  upload_chunk_max?: number;
  /** 文件管理是否可用；仅登录会话为 true。 */
  manage?: boolean;
  /** 分享功能是否可用（当前服务端恒为 true，字段留给未来开关）。 */
  share?: boolean;
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
  /** 构建注入的版本、commit、Go 版本与平台，用于“关于”。 */
  version?: string;
  commit?: string;
  go?: string;
  platform?: string;
}

/** 读取服务端能力（扩展名映射、搜索/列表上限、ffmpeg/HLS 开关）。 */
export function fetchServerInfo(signal?: AbortSignal): Promise<ServerInfo> {
  return requestJson<ServerInfo>('/api/info', { signal });
}

/** 读取健康检查；认证开启时该端点仍匿名可用。 */
export function fetchHealth(signal?: AbortSignal): Promise<HealthInfo> {
  return requestJson<HealthInfo>('/api/health', { signal });
}

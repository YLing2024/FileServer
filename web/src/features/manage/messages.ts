/** 文件管理失败文案：按 HTTP 状态区分权限、冲突、越界与网络错误。 */
import { ApiError } from '../../api/errors';

export function manageErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    switch (error.status) {
      case 0:
        return '网络错误，请检查连接';
      case 400:
        return error.message || '路径或名称不合法';
      case 401:
        return '登录状态已失效，请重新登录';
      case 403:
        return error.message || '没有权限（目录只读或不可操作）';
      case 404:
        return error.message || '目标不存在';
      case 409:
        return error.message || '同名文件或文件夹已存在';
      case 413:
        return '请求内容过大';
      default:
        return error.message || fallback;
    }
  }
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}

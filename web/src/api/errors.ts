/**
 * API 错误归一：所有请求失败最终都变成 ApiError，UI 只需展示 message。
 *
 * status=0 表示请求没有到达服务端（断网、DNS、被拦截）。业务错误码放在 code，
 * 例如上传冲突的 conflict；没有则为 undefined。
 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string | undefined;

  constructor(status: number, message: string, code?: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

/** 把未知异常收窄成可展示文案；非 ApiError 一律用调用方给的兜底文案。 */
export function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError && error.message) return error.message;
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}

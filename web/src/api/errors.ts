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

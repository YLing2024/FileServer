/**
 * 公开分享页的状态文案。
 *
 * 服务端错误码固定（not_found/expired/revoked/exhausted/...），页面只展示
 * 友好原因，不暴露路径或服务器信息；未知错误回退到服务端 message。
 */

/** 错误码 → [标题, 说明]。 */
export const SHARE_STATUS_TEXT: Readonly<Record<string, readonly [string, string]>> = {
  not_found: ['分享链接无效', '该链接不存在或分享记录已被删除。'],
  expired: ['分享已过期', '分享者设置的有效期已结束，请联系分享者重新分享。'],
  revoked: ['分享已撤销', '分享者已撤销此链接，无法继续访问。'],
  exhausted: ['下载次数已用完', '该分享的下载次数已达上限。'],
  missing: ['分享内容不存在', '分享的文件或目录已不可用。'],
  bad_path: ['无法访问', '请求的路径不合法。'],
  rate_limited: ['尝试过于频繁', '请稍后再试。'],
  need_code: ['需要提取码', '请输入分享者提供的 4 位提取码。'],
  bad_code: ['提取码不正确', '请重新输入分享者提供的 4 位提取码。'],
  preview_disabled: ['未开启预览', '分享者未开启在线预览，请下载后查看。'],
  preview_unsupported: ['不支持预览', '该类型不支持在线预览，请下载后查看。'],
  not_a_dir: ['无法访问', '该分享不是目录。'],
  network: ['网络错误', '无法连接服务器，请检查网络后重试。'],
};

/** 把错误码/后端文案归一为可展示的标题与说明。 */
export function shareStatusText(
  code: string,
  fallback: string,
): { title: string; text: string } {
  const pair = SHARE_STATUS_TEXT[code];
  if (pair) return { title: pair[0], text: pair[1] };
  return { title: '无法访问', text: fallback || '分享暂时不可用。' };
}

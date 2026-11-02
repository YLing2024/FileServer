/**
 * 复制文本到剪贴板。
 *
 * 优先 `navigator.clipboard`（需要安全上下文），局域网 HTTP 下回退到
 * 临时 textarea + `execCommand('copy')`，与旧前端行为一致。
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // 继续走 execCommand 回退。
  }
  try {
    const area = document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', '');
    area.style.position = 'fixed';
    area.style.opacity = '0';
    document.body.appendChild(area);
    area.select();
    const ok = document.execCommand('copy');
    area.remove();
    return ok;
  } catch {
    return false;
  }
}

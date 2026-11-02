/**
 * 上传名称校验与相对路径工具。
 *
 * 规则与服务端 validEntryName 对齐：单段名称不含分隔符/控制字符、不以点开头
 * 或点/空格结尾、不是 Windows 保留设备名、UTF-8 不超过 255 字节。
 * 前端提前拦截只是为了少一次请求，最终仍以服务端校验为准。
 */
const RESERVED = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$/i;

/** 单段名称是否可作为上传落盘名。 */
export function validNamePart(name: string): boolean {
  if (!name || name === '.' || name === '..') return false;
  if (/[/\\\u0000]/.test(name)) return false;
  if (/[\u0000-\u001f\u007f]/.test(name)) return false;
  if (/[<>:"|?*]/.test(name)) return false;
  if (name.startsWith('.') || name.endsWith('.') || name.endsWith(' ')) return false;
  if (RESERVED.test(name)) return false;
  if (name.toLowerCase() === '.fileserver') return false;
  try {
    if (new TextEncoder().encode(name).length > 255) return false;
  } catch {
    return false;
  }
  return true;
}

/** 拆分相对路径（忽略空段与首尾斜杠）。 */
export function splitRelPath(rel: string): string[] {
  return rel.split('/').filter((part) => part !== '');
}

/** 相对路径中每一段都必须是合法名称。 */
export function validRelPath(rel: string): boolean {
  const parts = splitRelPath(rel);
  return parts.length > 0 && parts.every(validNamePart);
}

/** 递归列出一个目录分组需要的全部子目录（不含根目录本身）。 */
export function groupSubdirs(relPath: string): string[] {
  const parts = splitRelPath(relPath);
  const out: string[] = [];
  for (let i = 1; i < parts.length; i += 1) out.push(parts.slice(0, i).join('/'));
  return out;
}

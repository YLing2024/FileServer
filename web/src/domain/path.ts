/** 浏览器路径工具：URL 参数里的 path 一律是相对服务根目录的 POSIX 路径。 */

/** 去掉首尾斜杠，折叠重复斜杠；根目录返回空串。 */
export function normalizePath(path: string): string {
  const parts = path.split('/').filter((part) => part !== '' && part !== '.');
  return parts.join('/');
}

/** 拼接目录与子项名。 */
export function joinPath(base: string, name: string): string {
  const normalized = normalizePath(base);
  return normalized ? `${normalized}/${name}` : name;
}

/** 返回上级路径；已在根目录时返回空串。 */
export function parentPath(path: string): string {
  const parts = normalizePath(path).split('/').filter(Boolean);
  parts.pop();
  return parts.join('/');
}

/** 文件所在目录；根目录文件返回空串。与 `/` 分隔符约定一致。 */
export function dirname(path: string): string {
  return parentPath(path);
}

/** 路径最后一段；空路径返回空串。 */
export function basename(path: string): string {
  const parts = normalizePath(path).split('/').filter(Boolean);
  return parts[parts.length - 1] ?? '';
}

/** 拆成面包屑片段（含空白根节点）。 */
export function pathSegments(path: string): string[] {
  return normalizePath(path).split('/').filter(Boolean);
}

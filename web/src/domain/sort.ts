/** 浏览视图排序：与 internal/server/list.go 的 sortEntries 保持同一语义。 */
import type { ListEntry, SortKey, SortOrder } from '../api/list';

/** 排序键的展示文案，顺序与旧前端下拉框一致。 */
export const SORT_LABELS: Readonly<Record<SortKey, string>> = {
  name: '名称',
  size: '大小',
  time: '时间',
};

/** 运行时收窄 localStorage 里的排序键。 */
export function isSortKey(value: string | null): value is SortKey {
  return value === 'name' || value === 'size' || value === 'time';
}

/** 运行时收窄 localStorage 里的排序方向。 */
export function isSortOrder(value: string | null): value is SortOrder {
  return value === 'asc' || value === 'desc';
}

function compareText(a: string, b: string): number {
  const left = a.toLowerCase();
  const right = b.toLowerCase();
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

/**
 * 条目比较器：目录永远在最前（不随升降序反转），其余按排序键比较，
 * 同值回退到名称，与服务端行为一致。
 */
export function compareEntries(
  a: ListEntry,
  b: ListEntry,
  sort: SortKey,
  order: SortOrder,
): number {
  if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;

  let result = 0;
  if (sort === 'size' && a.size !== b.size) {
    result = a.size < b.size ? -1 : 1;
  } else if (sort === 'time' && a.mtime !== b.mtime) {
    result = a.mtime < b.mtime ? -1 : 1;
  } else {
    result = compareText(a.name, b.name);
  }

  return order === 'desc' ? -result : result;
}

/** 返回排序后的新数组，不修改调用方数据。 */
export function sortEntries<T extends ListEntry>(
  entries: readonly T[],
  sort: SortKey,
  order: SortOrder,
): T[] {
  return [...entries].sort((a, b) => compareEntries(a, b, sort, order));
}

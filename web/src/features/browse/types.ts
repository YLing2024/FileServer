/** 浏览视图的条目：目录条目拼接当前路径，搜索条目携带真实路径。 */
import type { MouseEvent, PointerEvent } from 'react';
import type { ListEntry } from '../../api/list';

export interface BrowseEntry extends ListEntry {
  /** 相对服务根目录的完整路径，不带前导斜杠。 */
  fullPath: string;
  /** 搜索结果的所在目录（仅搜索模式存在）。 */
  searchDir?: string;
}

/** 条目交互：选中/打开/长按由 BrowsePage 组合，视图只负责转发事件。 */
export interface EntryInteraction {
  selected: ReadonlySet<string>;
  onClick: (entry: BrowseEntry, event: MouseEvent<HTMLElement>) => void;
  onDoubleClick: (entry: BrowseEntry) => void;
  onPointerDown: (entry: BrowseEntry, event: PointerEvent<HTMLElement>) => void;
  onPointerMove: (event: PointerEvent<HTMLElement>) => void;
  onPointerUp: () => void;
}

/**
 * 多选状态：桌面单击选中、双击打开；Ctrl/Cmd 切换、Shift 连选、全选/退出。
 *
 * 与旧前端一致：触屏未进入选择模式时轻点直接打开，长按 500ms 进入选择；
 * 选择模式中轻点切换。键盘激活（detail=0）始终按“打开”处理，避免
 * Enter 只能选中而无法进入目录。
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { MouseEvent, PointerEvent } from 'react';
import type { BrowseEntry } from './types';

interface UseSelectionResult {
  selected: ReadonlySet<string>;
  count: number;
  isSelected: (entry: BrowseEntry) => boolean;
  handleClick: (entry: BrowseEntry, event: MouseEvent<HTMLElement>) => 'open' | 'select';
  handleDoubleClick: (entry: BrowseEntry) => 'open' | 'none';
  handlePointerDown: (entry: BrowseEntry, event: PointerEvent<HTMLElement>) => void;
  handlePointerMove: (event: PointerEvent<HTMLElement>) => void;
  handlePointerUp: () => void;
  selectAll: () => void;
  clear: () => void;
  setOnly: (path: string) => void;
}

const LONG_PRESS_MS = 500;
const LONG_PRESS_TOLERANCE = 10;
const EMPTY_SELECTION: ReadonlySet<string> = new Set<string>();

function isCoarsePointer(): boolean {
  return window.matchMedia?.('(pointer: coarse)').matches ?? false;
}

export function useSelection(
  entries: BrowseEntry[],
  enabled: boolean,
  path: string,
): UseSelectionResult {
  const [state, setState] = useState<{ path: string; selected: Set<string> }>(() => ({
    path,
    selected: new Set(),
  }));
  const anchor = useRef<string | null>(null);
  const suppressClickUntil = useRef(0);
  const pressTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pressOrigin = useRef({ x: 0, y: 0, path: '' });

  // 路径变化后旧选择自动失效；不写 effect，避免一次多余渲染。
  const rawSelected = state.path === path ? state.selected : EMPTY_SELECTION;
  const validPaths = useMemo(() => new Set(entries.map((entry) => entry.fullPath)), [entries]);
  const selected = useMemo(
    () =>
      enabled ? new Set([...rawSelected].filter((item) => validPaths.has(item))) : EMPTY_SELECTION,
    [enabled, rawSelected, validPaths],
  );

  const update = useCallback(
    (mutate: (base: Set<string>) => Set<string>) => {
      setState((current) => {
        const base = current.path === path ? current.selected : new Set<string>();
        return { path, selected: mutate(new Set(base)) };
      });
    },
    [path],
  );

  const clear = useCallback(() => {
    anchor.current = null;
    setState({ path, selected: new Set() });
  }, [path]);

  useEffect(
    () => () => {
      if (pressTimer.current) clearTimeout(pressTimer.current);
    },
    [],
  );

  const toggle = useCallback(
    (entry: BrowseEntry) => {
      update((next) => {
        if (next.has(entry.fullPath)) next.delete(entry.fullPath);
        else next.add(entry.fullPath);
        return next;
      });
      anchor.current = entry.fullPath;
    },
    [update],
  );

  const selectRange = useCallback(
    (entry: BrowseEntry) => {
      const all = entries.map((item) => item.fullPath);
      const to = all.indexOf(entry.fullPath);
      if (to < 0) return;
      const from = anchor.current ? all.indexOf(anchor.current) : to;
      const start = from < 0 ? to : Math.min(from, to);
      const end = from < 0 ? to : Math.max(from, to);
      update((next) => {
        for (let index = start; index <= end; index += 1) {
          const item = all[index];
          if (item !== undefined) next.add(item);
        }
        return next;
      });
    },
    [entries, update],
  );

  const handleClick = useCallback(
    (entry: BrowseEntry, event: MouseEvent<HTMLElement>): 'open' | 'select' => {
      if (!enabled) return 'open';
      // 键盘触发的 click（detail=0）没有鼠标修饰键语义，按打开处理。
      if (event.detail === 0) return 'open';
      if (isCoarsePointer()) {
        if (Date.now() < suppressClickUntil.current) return 'select';
        if (selected.size > 0) {
          toggle(entry);
          return 'select';
        }
        return 'open';
      }
      if (event.ctrlKey || event.metaKey) {
        toggle(entry);
        return 'select';
      }
      if (event.shiftKey) {
        selectRange(entry);
        return 'select';
      }
      anchor.current = entry.fullPath;
      update((next) => {
        next.clear();
        next.add(entry.fullPath);
        return next;
      });
      return 'select';
    },
    [enabled, selectRange, selected.size, toggle, update],
  );

  const handleDoubleClick = useCallback(
    (entry: BrowseEntry): 'open' | 'none' => {
      if (!enabled || isCoarsePointer()) return 'none';
      // 双击前的那次单击已经把选择设到该项，这里只负责打开。
      anchor.current = entry.fullPath;
      update((next) => {
        next.clear();
        next.add(entry.fullPath);
        return next;
      });
      return 'open';
    },
    [enabled, update],
  );

  const cancelPress = useCallback(() => {
    if (pressTimer.current) {
      clearTimeout(pressTimer.current);
      pressTimer.current = null;
    }
  }, []);

  const handlePointerDown = useCallback(
    (entry: BrowseEntry, event: PointerEvent<HTMLElement>) => {
      if (!enabled || !isCoarsePointer() || event.button !== 0) return;
      if (selected.size > 0) return;
      pressOrigin.current = { x: event.clientX, y: event.clientY, path: entry.fullPath };
      cancelPress();
      pressTimer.current = setTimeout(() => {
        pressTimer.current = null;
        suppressClickUntil.current = Date.now() + 900;
        anchor.current = entry.fullPath;
        update((next) => {
          next.add(entry.fullPath);
          return next;
        });
      }, LONG_PRESS_MS);
    },
    [cancelPress, enabled, selected.size, update],
  );

  const handlePointerMove = useCallback(
    (event: PointerEvent<HTMLElement>) => {
      if (!pressTimer.current) return;
      if (
        Math.abs(event.clientX - pressOrigin.current.x) > LONG_PRESS_TOLERANCE ||
        Math.abs(event.clientY - pressOrigin.current.y) > LONG_PRESS_TOLERANCE
      ) {
        cancelPress();
      }
    },
    [cancelPress],
  );

  const handlePointerUp = useCallback(() => {
    cancelPress();
    if (suppressClickUntil.current > Date.now()) suppressClickUntil.current = Date.now() + 300;
  }, [cancelPress]);

  const selectAll = useCallback(() => {
    if (!enabled) return;
    const all = entries.map((entry) => entry.fullPath);
    anchor.current = all[0] ?? null;
    update((next) => {
      next.clear();
      for (const item of all) next.add(item);
      return next;
    });
  }, [enabled, entries, update]);

  const setOnly = useCallback(
    (target: string) => {
      anchor.current = target;
      update((next) => {
        next.clear();
        next.add(target);
        return next;
      });
    },
    [update],
  );

  return {
    selected,
    count: selected.size,
    isSelected: (entry) => selected.has(entry.fullPath),
    handleClick,
    handleDoubleClick,
    handlePointerDown,
    handlePointerMove,
    handlePointerUp,
    selectAll,
    clear,
    setOnly,
  };
}

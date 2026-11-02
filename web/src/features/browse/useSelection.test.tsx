import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { MouseEvent } from 'react';
import type { BrowseEntry } from './types';
import { useSelection } from './useSelection';

function entry(name: string): BrowseEntry {
  return {
    name,
    fullPath: name,
    is_dir: false,
    size: 1,
    mtime: 1,
    ext: '',
    kind: 'other',
  };
}

function mouse(detail: number, modifiers: { ctrl?: boolean; shift?: boolean } = {}): MouseEvent<HTMLElement> {
  return {
    detail,
    ctrlKey: Boolean(modifiers.ctrl),
    metaKey: false,
    shiftKey: Boolean(modifiers.shift),
  } as unknown as MouseEvent<HTMLElement>;
}

describe('useSelection', () => {
  it('单击选中、Ctrl 追加、Shift 连选、全选与清空', () => {
    const entries = [entry('a'), entry('b'), entry('c')];
    const { result } = renderHook(() => useSelection(entries, true, ''));

    act(() => {
      expect(result.current.handleClick(entries[0]!, mouse(1))).toBe('select');
    });
    expect(result.current.count).toBe(1);

    act(() => {
      result.current.handleClick(entries[1]!, mouse(1, { ctrl: true }));
    });
    expect(result.current.count).toBe(2);

    act(() => {
      result.current.handleClick(entries[2]!, mouse(1, { shift: true }));
    });
    expect(result.current.count).toBe(3);

    act(() => result.current.clear());
    expect(result.current.count).toBe(0);

    act(() => result.current.selectAll());
    expect([...result.current.selected].sort()).toEqual(['a', 'b', 'c']);

    act(() => result.current.clear());
    expect(result.current.count).toBe(0);
  });

  it('键盘激活（detail=0）返回 open，不进入选择模式', () => {
    const entries = [entry('a')];
    const { result } = renderHook(() => useSelection(entries, true, ''));
    act(() => {
      expect(result.current.handleClick(entries[0]!, mouse(0))).toBe('open');
    });
    expect(result.current.count).toBe(0);
  });

  it('管理能力关闭时不允许选中', () => {
    const entries = [entry('a')];
    const { result } = renderHook(() => useSelection(entries, false, ''));
    act(() => {
      expect(result.current.handleClick(entries[0]!, mouse(1))).toBe('open');
    });
    expect(result.current.count).toBe(0);
  });
});

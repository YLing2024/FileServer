import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ListEntry, ListResponse } from '../../api/list';
import { useDirectory } from './useDirectory';

function entry(name: string): ListEntry {
  return { name, is_dir: false, size: 1, mtime: 1, ext: '', kind: 'other' };
}

function json(body: ListResponse): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('useDirectory', () => {
  it('按 offset 追加下一页并更新 truncated', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      const offset = Number(new URL(url, 'http://localhost').searchParams.get('offset') ?? 0);
      const entries = offset === 0 ? [entry('a.txt'), entry('b.txt')] : [entry('c.txt')];
      return Promise.resolve(json({ path: '', entries, total: 3, truncated: offset === 0 }));
    });
    vi.stubGlobal('fetch', fetchMock);

    const { result } = renderHook(() => useDirectory('', 'name', 'asc', 2));
    await waitFor(() => expect(result.current.entries).toHaveLength(2));
    expect(result.current.truncated).toBe(true);

    act(() => result.current.loadMore());
    await waitFor(() => expect(result.current.entries).toHaveLength(3));
    expect(result.current.truncated).toBe(false);
    expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('offset=2'), expect.anything());
  });
});

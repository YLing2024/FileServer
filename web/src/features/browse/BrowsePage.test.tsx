import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ListEntry, ListResponse } from '../../api/list';
import { BrowsePage } from './BrowsePage';

function entry(name: string, isDir: boolean, size = 0): ListEntry {
  return {
    name,
    is_dir: isDir,
    size,
    mtime: 1_700_000_000,
    ext: '',
    kind: isDir ? 'dir' : 'other',
  };
}

function response(path: string, entries: ListEntry[]): Response {
  const body: ListResponse = { path, entries, total: entries.length };
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  window.history.replaceState(null, '', '/v2/');
});

describe('BrowsePage', () => {
  it('渲染目录条目，点击目录后更新 ?path= 并加载子目录', async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.includes('path=videos')) {
        return Promise.resolve(response('videos', [entry('sample.mp4', false, 2048)]));
      }
      return Promise.resolve(response('', [entry('videos', true), entry('readme.txt', false, 12)]));
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<BrowsePage />);

    const dirButton = await screen.findByRole('button', { name: /videos/ });
    expect(screen.getByText('readme.txt')).toBeTruthy();
    fireEvent.click(dirButton);

    await waitFor(() => {
      expect(window.location.search).toBe('?path=videos');
    });
    expect(await screen.findByText('sample.mp4')).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/list?path=&sort=name&order=asc&limit=300'),
      expect.anything(),
    );
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/list?path=videos&sort=name&order=asc&limit=300'),
      expect.anything(),
    );
  });

  it('接口失败时显示错误与重试按钮', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ error: '路径不存在' }), { status: 404 }));
    vi.stubGlobal('fetch', fetchMock);

    render(<BrowsePage />);

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('路径不存在');
    expect(screen.getByRole('button', { name: '重试' })).toBeTruthy();
  });
});

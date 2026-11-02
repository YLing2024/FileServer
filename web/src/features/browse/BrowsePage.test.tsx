import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ListEntry, ListResponse } from '../../api/list';
import type { SearchResponse } from '../../api/search';
import { ToastProvider } from '../../components/Toast';
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

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function listResponse(path: string, entries: ListEntry[], truncated = false): Response {
  const body: ListResponse = { path, entries, total: entries.length, truncated };
  return json(body);
}

function renderPage() {
  return render(
    <ToastProvider>
      <BrowsePage />
    </ToastProvider>,
  );
}

afterEach(() => {
  window.history.replaceState(null, '', '/v2/');
});

describe('BrowsePage', () => {
  it('渲染目录条目，点击目录后更新 ?path= 并加载子目录', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('/api/info')) {
        return Promise.resolve(json({ ffmpeg: false, hls: false, list_limit: 300 }));
      }
      if (url.startsWith('/api/health')) return Promise.resolve(json({ ok: true }));
      if (url.includes('path=videos')) {
        return Promise.resolve(listResponse('videos', [entry('sample.mp4', false, 2048)]));
      }
      return Promise.resolve(
        listResponse('', [entry('videos', true), entry('readme.txt', false, 12)]),
      );
    });
    vi.stubGlobal('fetch', fetchMock);

    renderPage();

    const dirButton = await screen.findByRole('button', { name: 'videos' });
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
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('/api/info')) return Promise.resolve(json({ ffmpeg: false, hls: false }));
      if (url.startsWith('/api/health')) return Promise.resolve(json({ ok: true }));
      return Promise.resolve(json({ error: '路径不存在' }, 404));
    });
    vi.stubGlobal('fetch', fetchMock);

    renderPage();

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('路径不存在');
    expect(screen.getByRole('button', { name: '重试' })).toBeTruthy();
  });

  it('网格/列表切换后保留目录条目', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.startsWith('/api/info'))
          return Promise.resolve(json({ ffmpeg: false, hls: false }));
        if (url.startsWith('/api/health')) return Promise.resolve(json({ ok: true }));
        return Promise.resolve(
          listResponse('', [entry('photos', true), entry('readme.txt', false, 12)]),
        );
      }),
    );

    renderPage();
    await screen.findByTestId('grid');

    fireEvent.click(screen.getByRole('button', { name: '列表视图' }));
    expect(await screen.findByTestId('list')).toBeTruthy();
    expect(screen.getByText('readme.txt')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: '网格视图' }));
    expect(await screen.findByTestId('grid')).toBeTruthy();
  });

  it('搜索 250ms 防抖后请求 /api/search 并高亮命中', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('/api/info')) {
        return Promise.resolve(json({ ffmpeg: false, hls: false, search_limit: 2000 }));
      }
      if (url.startsWith('/api/health')) return Promise.resolve(json({ ok: true }));
      if (url.startsWith('/api/search')) {
        const body: SearchResponse = {
          query: 'mp4',
          results: [
            {
              path: '/videos/sample.mp4',
              name: 'sample.mp4',
              is_dir: false,
              size: 2048,
              mtime: 1_700_000_000,
              kind: 'video',
            },
          ],
        };
        return Promise.resolve(json(body));
      }
      return Promise.resolve(listResponse('', [entry('readme.txt', false, 12)]));
    });
    vi.stubGlobal('fetch', fetchMock);

    renderPage();
    await screen.findByTestId('grid');

    fireEvent.change(screen.getByRole('textbox', { name: '搜索文件' }), {
      target: { value: 'mp4' },
    });

    expect(await screen.findByRole('button', { name: 'sample.mp4' })).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/search?q=mp4&path=&limit=2000'),
      expect.anything(),
    );
    expect(document.querySelector('mark')?.textContent).toBe('mp4');
  });
});

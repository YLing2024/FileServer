import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TEXT_PREVIEW_LIMIT, TextPreview } from './TextPreview';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('TextPreview', () => {
  it('等宽只读预览文本内容', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response('第一行\n第二行', {
            status: 200,
            headers: { 'Content-Type': 'text/plain' },
          }),
        ),
      ),
    );

    render(<TextPreview path="README.txt" size={20} mtime={0} onToast={vi.fn()} />);
    const pre = await screen.findByTestId('text-preview');
    expect(pre.textContent).toBe('第一行\n第二行');
    expect(screen.queryByTestId('text-truncated')).toBeNull();
  });

  it('超过 2 MiB 时用 Range 只读前 2 MiB 并提示截断', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      const headers = new Headers(init?.headers);
      expect(headers.get('Range')).toBe(`bytes=0-${TEXT_PREVIEW_LIMIT - 1}`);
      return Promise.resolve(
        new Response('截断内容', { status: 206, headers: { 'Content-Type': 'text/plain' } }),
      );
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <TextPreview path="big.log" size={TEXT_PREVIEW_LIMIT + 10} mtime={0} onToast={vi.fn()} />,
    );
    expect(await screen.findByTestId('text-truncated')).toBeTruthy();
    await waitFor(() => expect(screen.getByTestId('text-preview').textContent).toBe('截断内容'));
  });

  it('文件大小未知时先等待目录元数据，不发请求', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(<TextPreview path="unknown.txt" size={-1} mtime={0} onToast={vi.fn()} />);
    expect(screen.getByText('正在加载…')).toBeTruthy();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

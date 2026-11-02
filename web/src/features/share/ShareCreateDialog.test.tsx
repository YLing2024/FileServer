import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components/Toast';
import { ShareCreateDialog } from './ShareCreateDialog';

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('ShareCreateDialog', () => {
  it('开启提取码后自动生成 4 位，创建成功后展示链接与提取码', async () => {
    let requestBody: Record<string, unknown> = {};
    vi.stubGlobal(
      'fetch',
      vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
        requestBody = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>;
        return Promise.resolve(
          json({
            token: 'tok',
            url: '/s/tok',
            name: 'a.bin',
            is_dir: false,
            note: '共享文件',
            created: 1,
            expires: 2,
            downloads: 0,
            max_downloads: 0,
            allow_preview: true,
            code_set: true,
            status: 'active',
            code: requestBody.code,
          }),
        );
      }),
    );

    render(
      <ToastProvider>
        <ShareCreateDialog
          target={{ path: 'upload/a.bin', name: 'a.bin', isDir: false }}
          onClose={() => undefined}
        />
      </ToastProvider>,
    );

    const codeInput = screen.getByTestId('share-code') as HTMLInputElement;
    expect(codeInput.disabled).toBe(true);
    fireEvent.click(screen.getByTestId('share-code-enable'));
    expect(codeInput.disabled).toBe(false);
    expect(codeInput.value).toHaveLength(4);

    fireEvent.change(screen.getByTestId('share-note'), { target: { value: '给同事' } });
    fireEvent.submit(document.getElementById('share-create-form') as HTMLFormElement);

    await waitFor(() => {
      expect(screen.getByTestId('share-result-link')).toBeTruthy();
    });
    expect((screen.getByTestId('share-result-link') as HTMLInputElement).value).toBe(
      `${window.location.origin}/s/tok`,
    );
    expect(screen.getByTestId('share-result-code').textContent).toBe(String(requestBody.code));
    expect(requestBody).toMatchObject({
      path: 'upload/a.bin',
      days: 7,
      max_downloads: 0,
      allow_preview: true,
      note: '给同事',
    });
  });

  it('提取码不合法时阻止提交并提示', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(json({})));
    vi.stubGlobal('fetch', fetchMock);

    render(
      <ToastProvider>
        <ShareCreateDialog
          target={{ path: 'a.bin', name: 'a.bin', isDir: false }}
          onClose={() => undefined}
        />
      </ToastProvider>,
    );

    fireEvent.click(screen.getByTestId('share-code-enable'));
    fireEvent.change(screen.getByTestId('share-code'), { target: { value: 'A' } });
    fireEvent.submit(document.getElementById('share-create-form') as HTMLFormElement);

    expect(await screen.findByText('提取码必须是 4 位字母或数字')).toBeTruthy();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components/Toast';
import { SessionsDialog } from './SessionsDialog';

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('SessionsDialog', () => {
  it('标出当前设备，只允许下线其他会话', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/auth/sessions/revoke') return Promise.resolve(json({ ok: true }));
      return Promise.resolve(
        json({
          sessions: [
            {
              id: 's1',
              created: 1_700_000_000,
              last_seen: 1_700_000_100,
              expires: 1_700_100_000,
              remember: true,
              current: true,
              ua: 'Current Browser',
              ip: '127.0.0.1',
            },
            {
              id: 's2',
              created: 1_700_000_000,
              last_seen: 1_700_000_100,
              expires: 1_700_100_000,
              remember: false,
              current: false,
              ua: 'Other Device',
              ip: '192.0.2.10',
            },
          ],
        }),
      );
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <ToastProvider>
        <SessionsDialog onClose={() => undefined} />
      </ToastProvider>,
    );

    expect(await screen.findByText('Current Browser')).toBeTruthy();
    expect(screen.getByTestId('session-current').textContent).toBe('当前');
    const revokeButtons = screen.getAllByTestId('session-revoke');
    expect(revokeButtons).toHaveLength(1);

    fireEvent.click(revokeButtons[0] as HTMLElement);
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/revoke'));
      expect(call).toBeTruthy();
    });
    const init = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/revoke'))?.[1] as
      | RequestInit
      | undefined;
    expect(JSON.parse(String(init?.body))).toEqual({ id: 's2' });
  });
});

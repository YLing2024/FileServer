import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  absoluteShareUrl,
  createShare,
  deleteShareRecord,
  listShares,
  revokeShare,
} from './share';

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('share api', () => {
  it('创建分享发送完整请求体并返回结果', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(
        json({
          token: 't1',
          url: '/s/t1',
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
          code: 'AB12',
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    const result = await createShare({
      path: 'upload/a.bin',
      days: 7,
      code: 'AB12',
      max_downloads: 10,
      allow_preview: true,
      note: '备注',
    });

    expect(result.token).toBe('t1');
    expect(result.code).toBe('AB12');
    const init = fetchMock.mock.calls[0]?.[1] as RequestInit;
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toMatchObject({
      path: 'upload/a.bin',
      days: 7,
      code: 'AB12',
      max_downloads: 10,
      allow_preview: true,
      note: '备注',
    });
  });

  it('列表缺失 shares 时按空数组处理', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((_input: RequestInfo | URL, _init?: RequestInit) => Promise.resolve(json({}))),
    );
    await expect(listShares()).resolves.toEqual([]);
  });

  it('撤销/删除记录使用约定的方法与路径', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(json({ ok: true })),
    );
    vi.stubGlobal('fetch', fetchMock);

    await revokeShare('tok');
    await deleteShareRecord('tok');

    expect(String(fetchMock.mock.calls[0]?.[0])).toBe('/api/share/revoke');
    expect((fetchMock.mock.calls[0]?.[1] as RequestInit).method).toBe('POST');
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe('/api/share/record');
    expect((fetchMock.mock.calls[1]?.[1] as RequestInit).method).toBe('DELETE');
  });

  it('相对链接按运行时 origin 拼成绝对地址', () => {
    expect(absoluteShareUrl('/s/abc')).toBe(`${window.location.origin}/s/abc`);
    expect(absoluteShareUrl('https://example.test/s/abc')).toBe('https://example.test/s/abc');
  });
});

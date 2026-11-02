import { describe, expect, it, vi } from 'vitest';
import { UploadQueue } from './uploadQueue';
import type { UploadSettings } from './types';

const SETTINGS: UploadSettings = {
  manage: false,
  maxBytes: 2,
  chunkThreshold: 8,
  chunkMax: 16,
  maxFiles: 10,
};

function queue(): UploadQueue {
  return new UploadQueue({
    getSettings: () => SETTINGS,
    onFinished: vi.fn(),
    onToast: vi.fn(),
    onUnauthorized: vi.fn(),
  });
}

describe('UploadQueue', () => {
  it('超过单文件上限时直接标记错误并给出上限文案', () => {
    const q = queue();
    q.enqueuePlain([new File(['abc'], 'too-big.txt')], '');
    const item = q.getSnapshot().items[0];
    expect(item?.status).toBe('error');
    expect(item?.error).toContain('超过上限');
    q.destroy();
  });

  it('名称不合法（隐藏名）会进入 failed 列表', () => {
    const q = queue();
    q.enqueuePlain([new File(['a'], '.hidden.txt')], '');
    const item = q.getSnapshot().items[0];
    expect(item?.status).toBe('error');
    expect(item?.error).toContain('名称不合法');
    q.destroy();
  });
});

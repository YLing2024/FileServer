import { describe, expect, it } from 'vitest';
import { formatBytes, formatTime } from './format';

describe('formatBytes', () => {
  it('按 1024 进位并保留合适精度', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(1023)).toBe('1023 B');
    expect(formatBytes(1024)).toBe('1.00 KB');
    expect(formatBytes(10 * 1024)).toBe('10.0 KB');
    expect(formatBytes(150 * 1024)).toBe('150 KB');
    expect(formatBytes(1024 ** 3)).toBe('1.00 GB');
  });

  it('非法值返回空串（目录/未知大小不显示 0 B）', () => {
    expect(formatBytes(-1)).toBe('');
    expect(formatBytes(Number.NaN)).toBe('');
    expect(formatBytes(Number.POSITIVE_INFINITY)).toBe('');
  });
});

describe('formatTime', () => {
  it('Unix 秒转本地时间；0 或非法值返回空串', () => {
    expect(formatTime(0)).toBe('');
    expect(formatTime(-1)).toBe('');
    expect(formatTime(Number.NaN)).toBe('');
    expect(formatTime(1_700_000_000)).toMatch(/\d{4}\/\d{2}\/\d{2}/);
  });
});

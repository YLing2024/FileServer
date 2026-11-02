import { describe, expect, it } from 'vitest';
import { joinPath, normalizePath, parentPath, pathSegments } from './path';

describe('browser path helpers', () => {
  it('normalizePath 去掉首尾与重复斜杠', () => {
    expect(normalizePath('/a//b/')).toBe('a/b');
    expect(normalizePath('')).toBe('');
    expect(normalizePath('/')).toBe('');
    expect(normalizePath('./a/./b')).toBe('a/b');
  });

  it('joinPath / parentPath / pathSegments 与 URL 参数一致', () => {
    expect(joinPath('', 'videos')).toBe('videos');
    expect(joinPath('a/b', 'c')).toBe('a/b/c');
    expect(parentPath('a/b/c')).toBe('a/b');
    expect(parentPath('a')).toBe('');
    expect(parentPath('')).toBe('');
    expect(pathSegments('/a/b/')).toEqual(['a', 'b']);
  });
});

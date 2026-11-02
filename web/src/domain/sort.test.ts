import { describe, expect, it } from 'vitest';
import type { ListEntry } from '../api/list';
import { sortEntries } from './sort';

function entry(name: string, isDir: boolean, size = 0, mtime = 0): ListEntry {
  return { name, is_dir: isDir, size, mtime, ext: '', kind: isDir ? 'dir' : 'other' };
}

describe('sortEntries', () => {
  it('目录永远在最前，升降序只影响文件/目录内部顺序', () => {
    const entries = [
      entry('b.txt', false, 2, 20),
      entry('a-dir', true, 0, 10),
      entry('a.txt', false, 1, 30),
    ];
    expect(sortEntries(entries, 'name', 'asc').map((item) => item.name)).toEqual([
      'a-dir',
      'a.txt',
      'b.txt',
    ]);
    expect(sortEntries(entries, 'name', 'desc').map((item) => item.name)).toEqual([
      'a-dir',
      'b.txt',
      'a.txt',
    ]);
  });

  it('按大小与修改时间排序', () => {
    const entries = [entry('b.txt', false, 3, 10), entry('a.txt', false, 1, 30)];
    expect(sortEntries(entries, 'size', 'asc').map((item) => item.name)).toEqual([
      'a.txt',
      'b.txt',
    ]);
    expect(sortEntries(entries, 'time', 'desc').map((item) => item.name)).toEqual([
      'a.txt',
      'b.txt',
    ]);
  });
});

import { describe, expect, it } from 'vitest';
import { clearProgress, progressKey, readProgress, saveProgress } from './progress';

describe('播放进度记忆', () => {
  it('按 path + size 隔离，保存后可恢复', () => {
    saveProgress('videos/a.mp4', 100, 42.34, 600);
    expect(readProgress('videos/a.mp4', 100)).toBe(42.3);
    expect(readProgress('videos/a.mp4', 101)).toBe(0);
    expect(readProgress('videos/b.mp4', 100)).toBe(0);
    expect(progressKey('videos/a.mp4', 100)).toContain('videos/a.mp4:100');
  });

  it('片头、结尾与非法时长不写入，短片中段仍可记忆', () => {
    saveProgress('a.mp4', 1, 0.4, 100);
    saveProgress('a.mp4', 1, 99.5, 100);
    saveProgress('a.mp4', 1, 30, 0);
    expect(readProgress('a.mp4', 1)).toBe(0);
    saveProgress('short.mp4', 2, 3, 6);
    expect(readProgress('short.mp4', 2)).toBe(3);
  });

  it('清除后不再恢复', () => {
    saveProgress('a.mp4', 1, 30, 100);
    clearProgress('a.mp4', 1);
    expect(readProgress('a.mp4', 1)).toBe(0);
  });
});

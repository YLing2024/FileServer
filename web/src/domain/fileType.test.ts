import { describe, expect, it } from 'vitest';
import { extensionOf, kindOf, kindOfEntry } from './fileType';

describe('file type', () => {
  it('目录优先于扩展名', () => {
    expect(kindOf('photos.mp4', true)).toBe('dir');
  });

  it('大小写不敏感地按扩展名分类', () => {
    expect(kindOf('A.MKV', false)).toBe('video');
    expect(kindOf('photo.JPEG', false)).toBe('image');
    expect(kindOf('notes.md', false)).toBe('text');
    expect(kindOf('main.go', false)).toBe('code');
    expect(kindOf('unknown.bin', false)).toBe('other');
  });

  it('无扩展名 / 隐藏文件名不被当作扩展名', () => {
    expect(extensionOf('README')).toBe('');
    expect(extensionOf('.bashrc')).toBe('');
    expect(kindOf('README', false)).toBe('other');
  });

  it('优先使用服务端 kind，异常时回退本地判定', () => {
    expect(kindOfEntry({ name: 'a.bin', is_dir: false, kind: 'video' })).toBe('video');
    expect(kindOfEntry({ name: 'a.png', is_dir: false, kind: 'bogus' })).toBe('image');
    expect(kindOfEntry({ name: 'a.png', is_dir: true, kind: 'image' })).toBe('dir');
  });
});

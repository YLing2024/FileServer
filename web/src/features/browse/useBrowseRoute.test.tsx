import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { readBrowseRoute, useBrowseRoute } from './useBrowseRoute';

afterEach(() => {
  window.history.replaceState(null, '', '/v2/');
});

describe('readBrowseRoute', () => {
  it('解析 path 与 view，view 深链自动补出所在目录', () => {
    expect(readBrowseRoute('?path=photos&view=photos/sample.jpg')).toEqual({
      path: 'photos',
      view: 'photos/sample.jpg',
    });
    expect(readBrowseRoute('?view=videos/sample.mp4')).toEqual({
      path: 'videos',
      view: 'videos/sample.mp4',
    });
    expect(readBrowseRoute('')).toEqual({ path: '', view: null });
  });
});

describe('useBrowseRoute', () => {
  it('进入预览只写 ?view=，浏览器返回后恢复原目录', () => {
    window.history.replaceState({ path: 'photos' }, '', '/v2/?path=photos');
    const { result } = renderHook(() => useBrowseRoute());

    act(() => result.current.openView('photos/sample.jpg'));
    expect(window.location.search).toBe('?view=photos%2Fsample.jpg');
    expect(result.current).toMatchObject({ path: 'photos', view: 'photos/sample.jpg' });

    vi.spyOn(window.history, 'back').mockImplementation(() => {});
    act(() => result.current.closeView());
    expect(window.history.back).toHaveBeenCalled();
  });

  it('深链 Esc 退出时 replace 回目录，不离开站点', () => {
    window.history.replaceState(null, '', '/v2/?view=docs/readme.md');
    const { result } = renderHook(() => useBrowseRoute());

    act(() => result.current.closeView());
    expect(window.location.search).toBe('?path=docs');
    expect(result.current).toMatchObject({ path: 'docs', view: null });
  });

  it('灯箱切图只 replace 当前记录，不新增历史', () => {
    window.history.replaceState(
      { path: 'photos', fsView: 'photos/a.jpg' },
      '',
      '/v2/?view=photos%2Fa.jpg',
    );
    const { result } = renderHook(() => useBrowseRoute());
    const length = window.history.length;

    act(() => result.current.replaceView('photos/b.jpg'));
    expect(window.location.search).toBe('?view=photos%2Fb.jpg');
    expect(window.history.length).toBe(length);
    expect(result.current.view).toBe('photos/b.jpg');
  });
});

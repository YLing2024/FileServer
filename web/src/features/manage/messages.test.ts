import { describe, expect, it } from 'vitest';
import { ApiError } from '../../api/errors';
import { manageErrorMessage } from './messages';
import { uploadErrorMessage } from '../upload/uploadQueue';
import { validNamePart, validRelPath } from '../upload/uploadName';

describe('上传/管理错误文案', () => {
  it('上传 413/403/409 分别给出明确文案', () => {
    expect(uploadErrorMessage(new ApiError(413, '请求体过大'), 1024)).toContain('超过大小上限');
    expect(uploadErrorMessage(new ApiError(403, ''), 1024)).toContain('没有上传权限');
    expect(uploadErrorMessage(new ApiError(409, '同名文件已存在'), 1024)).toBe('同名文件已存在');
    expect(uploadErrorMessage(new ApiError(0, ''), 1024)).toContain('网络错误');
  });

  it('管理 403/404/409/413 映射到对应提示', () => {
    expect(manageErrorMessage(new ApiError(403, ''), '失败')).toContain('没有权限');
    expect(manageErrorMessage(new ApiError(404, ''), '失败')).toBe('目标不存在');
    expect(manageErrorMessage(new ApiError(409, ''), '失败')).toContain('同名');
    expect(manageErrorMessage(new ApiError(413, ''), '失败')).toBe('请求内容过大');
  });
});

describe('上传名称校验', () => {
  it('拒绝隐藏名、保留名、分隔符与控制字符', () => {
    expect(validNamePart('a.txt')).toBe(true);
    expect(validNamePart('.hidden')).toBe(false);
    expect(validNamePart('CON')).toBe(false);
    expect(validNamePart('a/b')).toBe(false);
    expect(validNamePart('a\u0000b')).toBe(false);
    expect(validNamePart('a.')).toBe(false);
  });

  it('相对路径每一段都必须是合法名称', () => {
    expect(validRelPath('folder/a.txt')).toBe(true);
    expect(validRelPath('.folder/a.txt')).toBe(false);
    expect(validRelPath('')).toBe(false);
  });
});

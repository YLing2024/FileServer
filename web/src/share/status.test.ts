import { describe, expect, it } from 'vitest';
import { shareStatusText } from './status';

describe('shareStatusText', () => {
  it('已知错误码使用固定标题与说明', () => {
    expect(shareStatusText('not_found', 'x')).toEqual({
      title: '分享链接无效',
      text: '该链接不存在或分享记录已被删除。',
    });
    expect(shareStatusText('revoked', 'x').title).toBe('分享已撤销');
    expect(shareStatusText('rate_limited', 'x').title).toBe('尝试过于频繁');
  });

  it('未知错误码回退服务端文案', () => {
    expect(shareStatusText('weird', '后端说明')).toEqual({
      title: '无法访问',
      text: '后端说明',
    });
    expect(shareStatusText('', '').text).toBe('分享暂时不可用。');
  });
});

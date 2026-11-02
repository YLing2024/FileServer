import { describe, expect, it } from 'vitest';
import {
  generateShareCode,
  isValidShareCode,
  normalizeShareCode,
  SHARE_CODE_ALPHABET,
} from './shareCode';

describe('shareCode', () => {
  it('按提供的随机字节生成 4 位可读提取码', () => {
    const code = generateShareCode((bytes) => {
      bytes.set([0, 1, 2, 3]);
      return bytes;
    });
    expect(code).toHaveLength(4);
    expect(code).toBe(
      [0, 1, 2, 3].map((value) => SHARE_CODE_ALPHABET[value]).join(''),
    );
  });

  it('规范化只保留字母数字并转大写、截断到 4 位', () => {
    expect(normalizeShareCode('ab-12x')).toBe('AB12');
    expect(normalizeShareCode(' 中 A b 9 ')).toBe('AB9');
    expect(normalizeShareCode('!!!!')).toBe('');
  });

  it('校验规则与服务端一致', () => {
    expect(isValidShareCode('AB12')).toBe(true);
    expect(isValidShareCode('ab12')).toBe(true);
    expect(isValidShareCode('AB1')).toBe(false);
    expect(isValidShareCode('AB-2')).toBe(false);
  });
});

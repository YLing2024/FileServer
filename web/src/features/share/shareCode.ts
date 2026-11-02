/**
 * 4 位提取码的生成与规范化。
 *
 * 去掉了 0/O/1/I 等易混字符，自动生成的提取码更好读；服务端只要求
 * 4 位 ASCII 字母或数字，因此用户自定义时也接受小写（统一转大写展示）。
 */

export const SHARE_CODE_ALPHABET = 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789';
export const SHARE_CODE_LENGTH = 4;

/** 生成 4 位可读提取码；`random` 仅供测试注入，默认用 crypto。 */
export function generateShareCode(random: (bytes: Uint8Array) => Uint8Array = secureRandom): string {
  const bytes = random(new Uint8Array(SHARE_CODE_LENGTH));
  let code = '';
  for (let index = 0; index < SHARE_CODE_LENGTH; index += 1) {
    const value = bytes[index] ?? 0;
    code += SHARE_CODE_ALPHABET.charAt(value % SHARE_CODE_ALPHABET.length);
  }
  return code;
}

/** 过滤非法字符并截断到 4 位；输入框与提交前都调用同一函数。 */
export function normalizeShareCode(value: string): string {
  return value.replace(/[^A-Za-z0-9]/g, '').toUpperCase().slice(0, SHARE_CODE_LENGTH);
}

/** 服务端校验规则：恰好 4 位 ASCII 字母或数字（区分大小写，前端统一大写）。 */
export function isValidShareCode(value: string): boolean {
  return /^[A-Za-z0-9]{4}$/.test(value);
}

function secureRandom(bytes: Uint8Array): Uint8Array {
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    crypto.getRandomValues(bytes);
    return bytes;
  }
  for (let index = 0; index < bytes.length; index += 1) {
    bytes[index] = Math.floor(Math.random() * 256);
  }
  return bytes;
}

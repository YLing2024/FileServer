/**
 * `upload_id` 派生与分片长度计算。
 *
 * 公式必须与服务端 `deriveUploadID` 完全一致：
 * `sha256(文件前 min(1 MiB, size) 字节 || 0x00 || 十进制 size || 0x00 || 文件名)`。
 * 相同文件中断后重新选择会得到同一 id，从而复用已上传分片。
 */
import { sha256Hex } from './sha256';

const HEAD_LIMIT = 1 << 20;

/** 计算与服务端一致的 upload_id（64 位小写十六进制）。 */
export async function computeUploadId(file: File, name: string): Promise<string> {
  const headLength = Math.min(HEAD_LIMIT, file.size);
  const head = new Uint8Array(await file.slice(0, headLength).arrayBuffer());
  const meta = new TextEncoder().encode(`\0${file.size}\0${name}`);
  const all = new Uint8Array(head.length + meta.length);
  all.set(head, 0);
  all.set(meta, head.length);
  return sha256Hex(all);
}

/** 第 index 片的字节数；最后一片可能不足 chunkSize。 */
export function chunkLength(size: number, total: number, index: number, chunkSize: number): number {
  if (index < total - 1) return chunkSize;
  return size - chunkSize * (total - 1);
}

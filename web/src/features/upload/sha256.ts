/**
 * 纯 TypeScript 增量 SHA-256。
 *
 * 为什么不用 Web Crypto：`crypto.subtle.digest` 只能一次性吃完整缓冲区，
 * 大文件会占满内存；而且它只在安全上下文（HTTPS/localhost）可用，局域网
 * HTTP 部署拿不到。这里沿用旧前端 sha256.js 的思路，按 4 MiB 分片流式
 * 读取，既能放进 Worker，也能在主线程回退。
 *
 * 仅用于秒传内容指纹与 `upload_id` 派生，不承载任何密钥。
 */

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

function rotr(value: number, bits: number): number {
  return (value >>> bits) | (value << (32 - bits));
}

function toHex(bytes: Uint8Array): string {
  let out = '';
  for (const byte of bytes) out += byte.toString(16).padStart(2, '0');
  return out;
}

/** 增量 SHA-256；update 可多次调用，digest 只能调用一次。 */
export class Sha256 {
  private readonly h = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ]);

  private readonly buffer = new Uint8Array(64);
  private bufferLength = 0;
  private byteLength = 0;
  private finalized = false;

  /** 追加一段数据；数据会被立即消费，调用方可以复用缓冲区。 */
  update(data: Uint8Array): void {
    if (this.finalized) throw new Error('SHA-256 已结束');
    this.byteLength += data.length;
    let offset = 0;

    if (this.bufferLength > 0) {
      const take = Math.min(64 - this.bufferLength, data.length);
      this.buffer.set(data.subarray(0, take), this.bufferLength);
      this.bufferLength += take;
      offset = take;
      if (this.bufferLength === 64) {
        this.compress(this.buffer, 0);
        this.bufferLength = 0;
      }
    }

    while (offset + 64 <= data.length) {
      this.compress(data, offset);
      offset += 64;
    }
    if (offset < data.length) {
      this.buffer.set(data.subarray(offset));
      this.bufferLength = data.length - offset;
    }
  }

  /** 结束并返回 32 字节摘要；再次调用会抛错，避免重复 finalize 污染状态。 */
  digest(): Uint8Array {
    if (this.finalized) throw new Error('SHA-256 已结束');
    this.finalized = true;

    const rem = this.bufferLength;
    this.buffer[rem] = 0x80;
    if (rem >= 56) {
      this.buffer.fill(0, rem + 1);
      this.compress(this.buffer, 0);
      this.buffer.fill(0);
    } else {
      this.buffer.fill(0, rem + 1, 56);
    }

    // 64 位大端比特长度：高位 = bytes / 2^29，低位 = (bytes * 8) mod 2^32。
    const high = Math.floor(this.byteLength / 0x20000000);
    const low = (this.byteLength * 8) >>> 0;
    this.buffer[56] = (high >>> 24) & 0xff;
    this.buffer[57] = (high >>> 16) & 0xff;
    this.buffer[58] = (high >>> 8) & 0xff;
    this.buffer[59] = high & 0xff;
    this.buffer[60] = (low >>> 24) & 0xff;
    this.buffer[61] = (low >>> 16) & 0xff;
    this.buffer[62] = (low >>> 8) & 0xff;
    this.buffer[63] = low & 0xff;
    this.compress(this.buffer, 0);

    const out = new Uint8Array(32);
    for (let i = 0; i < 8; i += 1) {
      const word = this.h[i] ?? 0;
      out[i * 4] = (word >>> 24) & 0xff;
      out[i * 4 + 1] = (word >>> 16) & 0xff;
      out[i * 4 + 2] = (word >>> 8) & 0xff;
      out[i * 4 + 3] = word & 0xff;
    }
    return out;
  }

  private readonly w = new Uint32Array(64);

  private compress(block: Uint8Array, start: number): void {
    const w = this.w;
    for (let i = 0; i < 16; i += 1) {
      const at = start + i * 4;
      w[i] =
        (((block[at] ?? 0) << 24) |
          ((block[at + 1] ?? 0) << 16) |
          ((block[at + 2] ?? 0) << 8) |
          (block[at + 3] ?? 0)) >>>
        0;
    }
    for (let i = 16; i < 64; i += 1) {
      const x = w[i - 15] ?? 0;
      const y = w[i - 2] ?? 0;
      const s0 = rotr(x, 7) ^ rotr(x, 18) ^ (x >>> 3);
      const s1 = rotr(y, 17) ^ rotr(y, 19) ^ (y >>> 10);
      w[i] = ((w[i - 16] ?? 0) + s0 + (w[i - 7] ?? 0) + s1) >>> 0;
    }

    let a = this.h[0] ?? 0;
    let b = this.h[1] ?? 0;
    let c = this.h[2] ?? 0;
    let d = this.h[3] ?? 0;
    let e = this.h[4] ?? 0;
    let f = this.h[5] ?? 0;
    let g = this.h[6] ?? 0;
    let h = this.h[7] ?? 0;

    for (let i = 0; i < 64; i += 1) {
      const s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const ch = (e & f) ^ (~e & g);
      const temp1 = (h + s1 + ch + (K[i] ?? 0) + (w[i] ?? 0)) >>> 0;
      const s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const temp2 = (s0 + maj) >>> 0;
      h = g;
      g = f;
      f = e;
      e = (d + temp1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + temp2) >>> 0;
    }

    this.h[0] = ((this.h[0] ?? 0) + a) >>> 0;
    this.h[1] = ((this.h[1] ?? 0) + b) >>> 0;
    this.h[2] = ((this.h[2] ?? 0) + c) >>> 0;
    this.h[3] = ((this.h[3] ?? 0) + d) >>> 0;
    this.h[4] = ((this.h[4] ?? 0) + e) >>> 0;
    this.h[5] = ((this.h[5] ?? 0) + f) >>> 0;
    this.h[6] = ((this.h[6] ?? 0) + g) >>> 0;
    this.h[7] = ((this.h[7] ?? 0) + h) >>> 0;
  }
}

/** 一次性计算小段数据的 SHA-256（十六进制）。 */
export function sha256Hex(data: Uint8Array): string {
  const hash = new Sha256();
  hash.update(data);
  return toHex(hash.digest());
}

const HASH_CHUNK_DEFAULT = 4 * 1024 * 1024;

function abortError(): DOMException {
  return new DOMException('已取消', 'AbortError');
}

/**
 * 流式读取 Blob 并计算 SHA-256；每片 4 MiB，进度为 0..1。
 * signal 被触发时在分片边界退出，Worker 侧由调用方直接 terminate。
 */
export async function hashBlob(
  blob: Blob,
  onProgress?: (progress: number) => void,
  signal?: AbortSignal,
  chunkSize = HASH_CHUNK_DEFAULT,
): Promise<string> {
  const hash = new Sha256();
  const total = blob.size;
  let offset = 0;
  while (offset < total) {
    if (signal?.aborted) throw abortError();
    const end = Math.min(offset + chunkSize, total);
    const buffer = await blob.slice(offset, end).arrayBuffer();
    if (signal?.aborted) throw abortError();
    hash.update(new Uint8Array(buffer));
    offset = end;
    onProgress?.(total === 0 ? 1 : offset / total);
  }
  if (total === 0) onProgress?.(1);
  return toHex(hash.digest());
}

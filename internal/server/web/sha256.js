/* ============================================================
   SHA-256（原生 JS，无依赖）
   ------------------------------------------------------------
   Web Crypto 的 digest() 只能一次性吃完整缓冲区，大文件会占满内存且阻塞主线程。
   这里实现可增量更新的 SHA-256，供 app.js 与 hash-worker.js 分片读取文件时复用。
   仅用于秒传内容指纹与 upload_id 派生，不承载任何密钥。
   ============================================================ */
(function (global) {
  'use strict';

  const K = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ];

  function rotr(x, n) { return (x >>> n) | (x << (32 - n)); }

  class SHA256 {
    constructor() {
      this.h = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19];
      this.buffer = new Uint8Array(64);
      this.bufferLen = 0;
      this.bytes = 0;
      this.w = new Uint32Array(64);
    }

    update(data) {
      let offset = 0;
      this.bytes += data.length;
      if (this.bufferLen > 0) {
        const need = 64 - this.bufferLen;
        const take = Math.min(need, data.length);
        this.buffer.set(data.subarray(0, take), this.bufferLen);
        this.bufferLen += take;
        offset += take;
        if (this.bufferLen === 64) {
          this._block(this.buffer, 0);
          this.bufferLen = 0;
        }
      }
      while (offset + 64 <= data.length) {
        this._block(data, offset);
        offset += 64;
      }
      if (offset < data.length) {
        this.buffer.set(data.subarray(offset), 0);
        this.bufferLen = data.length - offset;
      }
      return this;
    }

    _block(data, off) {
      const w = this.w;
      for (let i = 0; i < 16; i++) {
        const j = off + i * 4;
        w[i] = ((data[j] << 24) | (data[j + 1] << 16) | (data[j + 2] << 8) | data[j + 3]) >>> 0;
      }
      for (let i = 16; i < 64; i++) {
        const s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >>> 3);
        const s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >>> 10);
        w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
      }
      let a = this.h[0], b = this.h[1], c = this.h[2], d = this.h[3];
      let e = this.h[4], f = this.h[5], g = this.h[6], h = this.h[7];
      for (let i = 0; i < 64; i++) {
        const S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
        const ch = (e & f) ^ (~e & g);
        const t1 = (h + S1 + ch + K[i] + w[i]) >>> 0;
        const S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
        const maj = (a & b) ^ (a & c) ^ (b & c);
        const t2 = (S0 + maj) >>> 0;
        h = g; g = f; f = e; e = (d + t1) >>> 0;
        d = c; c = b; b = a; a = (t1 + t2) >>> 0;
      }
      this.h[0] = (this.h[0] + a) >>> 0;
      this.h[1] = (this.h[1] + b) >>> 0;
      this.h[2] = (this.h[2] + c) >>> 0;
      this.h[3] = (this.h[3] + d) >>> 0;
      this.h[4] = (this.h[4] + e) >>> 0;
      this.h[5] = (this.h[5] + f) >>> 0;
      this.h[6] = (this.h[6] + g) >>> 0;
      this.h[7] = (this.h[7] + h) >>> 0;
    }

    digest() {
      const bitLenHi = Math.floor((this.bytes * 8) / 0x100000000);
      const bitLenLo = (this.bytes * 8) >>> 0;
      const pad = new Uint8Array(72);
      let padLen = 0;
      pad[padLen++] = 0x80;
      const remain = (this.bufferLen + 1) % 64;
      const zeros = remain <= 56 ? 56 - remain : 120 - remain;
      padLen += zeros;
      for (let i = 0; i < 8; i++) pad[padLen++] = i < 4 ? (bitLenHi >>> (24 - i * 8)) & 0xff : (bitLenLo >>> (24 - (i - 4) * 8)) & 0xff;
      // update() 会改变 this.bytes，但 digest 之后对象不再复用；保存长度已提前算出。
      this.update(pad.subarray(0, padLen));
      const out = new Uint8Array(32);
      for (let i = 0; i < 8; i++) {
        out[i * 4] = (this.h[i] >>> 24) & 0xff;
        out[i * 4 + 1] = (this.h[i] >>> 16) & 0xff;
        out[i * 4 + 2] = (this.h[i] >>> 8) & 0xff;
        out[i * 4 + 3] = this.h[i] & 0xff;
      }
      return out;
    }
  }

  function hex(bytes) {
    let s = '';
    for (let i = 0; i < bytes.length; i++) s += bytes[i].toString(16).padStart(2, '0');
    return s;
  }

  function hashBytes(bytes) {
    return new SHA256().update(bytes).digest();
  }

  // hashBlob 分块读取 Blob/File，避免整文件进内存；onProgress 为 0..1。
  async function hashBlob(blob, onProgress, chunkSize) {
    const step = chunkSize || (4 << 20);
    const hasher = new SHA256();
    let offset = 0;
    while (offset < blob.size) {
      const end = Math.min(offset + step, blob.size);
      const buf = await blob.slice(offset, end).arrayBuffer();
      hasher.update(new Uint8Array(buf));
      offset = end;
      if (onProgress) onProgress(blob.size ? offset / blob.size : 1);
    }
    if (blob.size === 0 && onProgress) onProgress(1);
    return hex(hasher.digest());
  }

  global.FSSHA256 = { hashBlob, hashBytes, hex };
})(typeof self !== 'undefined' ? self : this);

import { describe, expect, it } from 'vitest';
import { Sha256, hashBlob, sha256Hex } from './sha256';
import { chunkLength, computeUploadId } from './uploadId';

const encoder = new TextEncoder();

describe('SHA-256', () => {
  it('匹配标准向量（空串、abc、长文本）', () => {
    expect(sha256Hex(new Uint8Array())).toBe(
      'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    );
    expect(sha256Hex(encoder.encode('abc'))).toBe(
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
    );
    expect(sha256Hex(encoder.encode('The quick brown fox jumps over the lazy dog'))).toBe(
      'd7a8fbb307d7809469ca9abcb0082e4f8d5651e46d3cdb762d02d0bf37c9e592',
    );
  });

  it('分片 update 与一次性计算结果一致', () => {
    const text = 'FileServer 分片哈希一致性 '.repeat(64);
    const data = encoder.encode(text);
    const oneShot = new Sha256();
    oneShot.update(data);

    const chunked = new Sha256();
    for (let offset = 0; offset < data.length; offset += 37) {
      chunked.update(data.subarray(offset, offset + 37));
    }
    expect(Array.from(chunked.digest())).toEqual(Array.from(oneShot.digest()));
  });

  it('hashBlob 流式读取 Blob 并上报进度', async () => {
    const blob = new Blob([encoder.encode('abc')]);
    const progress: number[] = [];
    await expect(hashBlob(blob, (value) => progress.push(value), undefined, 2)).resolves.toBe(
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
    );
    expect(progress).toEqual([2 / 3, 1]);
  });

  it('取消后抛出 AbortError', async () => {
    const controller = new AbortController();
    controller.abort();
    await expect(hashBlob(new Blob(['abc']), undefined, controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    });
  });
});

describe('uploadId', () => {
  it('最后一片长度按剩余字节计算', () => {
    expect(chunkLength(10 * 1024 * 1024, 2, 0, 8 * 1024 * 1024)).toBe(8 * 1024 * 1024);
    expect(chunkLength(10 * 1024 * 1024, 2, 1, 8 * 1024 * 1024)).toBe(2 * 1024 * 1024);
  });

  it('upload_id 与“前 1 MiB + 0 + 大小 + 0 + 名称”公式一致', async () => {
    const file = new File(['hello'], 'a.txt');
    const head = await file.slice(0, Math.min(1 << 20, file.size)).arrayBuffer();
    const meta = encoder.encode(`\0${file.size}\0a.txt`);
    const all = new Uint8Array(head.byteLength + meta.length);
    all.set(new Uint8Array(head), 0);
    all.set(meta, head.byteLength);
    await expect(computeUploadId(file, 'a.txt')).resolves.toBe(sha256Hex(all));
  });
});

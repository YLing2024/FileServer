/* ============================================================
   大文件 SHA-256 计算 Worker
   ------------------------------------------------------------
   仅在文件 > 200 MiB 且浏览器支持 Worker 时由 app.js 启用，避免校验大文件时
   卡住主线程。计算逻辑来自同源 sha256.js；不支持 Worker 时前端回退主线程分片。
   ============================================================ */
'use strict';

importScripts('/sha256.js');

self.onmessage = async (e) => {
  const file = e.data && e.data.file;
  if (!file) return;
  try {
    const hex = await self.FSSHA256.hashBlob(file, (progress) => {
      self.postMessage({ type: 'progress', progress });
    }, 4 << 20);
    self.postMessage({ type: 'done', hex });
  } catch (err) {
    self.postMessage({ type: 'error', error: String(err && err.message ? err.message : err) });
  }
};

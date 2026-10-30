/* ============================================================
   公开分享页逻辑 — 原生 JS，独立于管理端 app.js。
   只读取 /api/public/share/<token>/*；token 从 URL 路径解析，
   不写入页面其他位置（避免 Referer 之外再扩散）。
   ============================================================ */
'use strict';

(() => {
  const $ = (id) => document.getElementById(id);
  const enc = encodeURIComponent;

  const parts = location.pathname.split('/').filter(Boolean);
  const token = parts.length ? decodeURIComponent(parts[parts.length - 1]) : '';
  const base = '/api/public/share/' + enc(token);

  const state = { info: null, dirPath: '/', listSeq: 0 };
  const TEXT_PREVIEW_LIMIT = 1024 * 1024;

  const STATUS_TEXT = {
    not_found: ['分享链接无效', '该链接不存在或分享记录已被删除。'],
    expired: ['分享已过期', '分享者设置的有效期已结束，请联系分享者重新分享。'],
    revoked: ['分享已撤销', '分享者已撤销此链接，无法继续访问。'],
    exhausted: ['下载次数已用完', '该分享的下载次数已达上限。'],
    missing: ['分享内容不存在', '分享的文件或目录已不可用。'],
    bad_path: ['无法访问', '请求的路径不合法。'],
    rate_limited: ['尝试过于频繁', '请稍后再试。'],
  };

  const KIND_ICONS = {
    dir: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/>',
    image: '<rect x="3.5" y="5" width="17" height="14" rx="2.5"/><circle cx="9" cy="10" r="1.6"/><path d="M4 17l4.5-4.5 3.5 3.5 3-3L20 17.5"/>',
    video: '<rect x="3" y="5.5" width="13" height="13" rx="2.5"/><path d="M16 10.5l5-3v9l-5-3"/>',
    audio: '<path d="M9 18V6l10-2v11.5"/><circle cx="6.5" cy="18" r="2.5"/><circle cx="16.5" cy="15.5" r="2.5"/>',
    pdf: '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/>',
    text: '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/><path d="M9 12h6M9 15h6"/>',
    code: '<path d="M8.5 7.5L4 12l4.5 4.5M15.5 7.5L20 12l-4.5 4.5"/><path d="M13.5 5l-3 14"/>',
    other: '<path d="M7 3.5h7L19.5 9v11a1.5 1.5 0 0 1-1.5 1.5H7A1.5 1.5 0 0 1 5.5 20V5A1.5 1.5 0 0 1 7 3.5z"/><path d="M14 3.5V9h5.5"/>',
  };

  function icon(kind) {
    const svg = KIND_ICONS[kind] || KIND_ICONS.other;
    const span = document.createElement('span');
    span.className = 'type-icon';
    span.innerHTML = `<svg viewBox="0 0 24 24">${svg}</svg>`;
    return span;
  }

  function fmtSize(n) {
    if (n == null) return '';
    if (n < 1024) return n + ' B';
    const units = ['KB', 'MB', 'GB', 'TB'];
    let v = n / 1024;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return (v >= 100 ? v.toFixed(0) : v.toFixed(1)) + ' ' + units[i];
  }

  function fmtTime(ts) {
    if (!ts) return '';
    const d = new Date(ts * 1000);
    const pad = (x) => String(x).padStart(2, '0');
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) +
      ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  }

  async function getJSON(path, options) {
    const resp = await fetch(base + path, Object.assign({ cache: 'no-store', credentials: 'same-origin' }, options || {}));
    let body = {};
    try { body = await resp.json(); } catch (_) { /* 非 JSON 响应按空对象处理 */ }
    if (!resp.ok) {
      const err = new Error(body.error || '请求失败');
      err.status = resp.status;
      err.code = body.code || '';
      throw err;
    }
    return body;
  }

  function show(id) {
    ['shareLoading', 'shareCode', 'shareStatus', 'shareFile', 'shareDir'].forEach((x) => {
      const el = $(x);
      if (el) el.classList.toggle('hidden', x !== id);
    });
  }

  function showStatus(err) {
    const pair = STATUS_TEXT[err.code] || ['无法访问', err.message || '分享暂时不可用。'];
    $('statusTitle').textContent = pair[0];
    $('statusText').textContent = pair[1];
    show('shareStatus');
  }

  function renderShell(info) {
    $('shareNote').textContent = info.note || '共享文件';
    $('shareExpiry').textContent = info.expires
      ? '有效期至 ' + fmtTime(info.expires)
      : '永久有效';
    document.title = (info.name ? info.name + ' · ' : '') + '文件分享';
  }

  function showCode() {
    show('shareCode');
    $('codeInput').value = '';
    $('codeError').classList.add('hidden');
    window.setTimeout(() => $('codeInput').focus(), 30);
  }

  async function load() {
    show('shareLoading');
    try {
      const info = await getJSON('/info');
      state.info = info;
      renderShell(info);
      if (info.has_code && !info.verified) { showCode(); return; }
      if (info.kind === 'dir') { await openDir('/'); return; }
      renderFile(info);
    } catch (e) {
      if (e.code === 'need_code') { showCode(); return; }
      showStatus(e);
    }
  }

  /* ---------- 提取码 ---------- */

  $('codeForm').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const code = $('codeInput').value.trim();
    const errEl = $('codeError');
    if (code.length !== 4) {
      errEl.textContent = '请输入 4 位提取码';
      errEl.classList.remove('hidden');
      return;
    }
    const btn = $('codeSubmit');
    btn.disabled = true;
    try {
      await getJSON('/verify', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ code }),
      });
      errEl.classList.add('hidden');
      await load();
    } catch (e) {
      const pair = STATUS_TEXT[e.code];
      errEl.textContent = e.code === 'rate_limited'
        ? '尝试过于频繁，请稍后再试'
        : (pair ? pair[1] : '提取码不正确');
      errEl.classList.remove('hidden');
      $('codeInput').select();
    } finally {
      btn.disabled = false;
    }
  });

  /* ---------- 单文件 ---------- */

  function renderFile(info) {
    state.info = info;
    $('fileTitle').textContent = info.name || '文件';
    $('fileMeta').textContent = [fmtSize(info.size), fmtTime(info.mtime)].filter(Boolean).join(' · ');
    const dl = $('fileDownload');
    dl.href = base + '/download';
    dl.setAttribute('download', info.name || '');
    const box = $('filePreview');
    box.innerHTML = '';
    box.classList.remove('hidden');

    if (!info.allow_preview) {
      const p = document.createElement('p');
      p.className = 'share-hint';
      p.textContent = '分享者未开启在线预览，请下载后查看。';
      box.appendChild(p);
      show('shareFile');
      return;
    }

    switch (info.kind) {
      case 'image': {
        const img = document.createElement('img');
        img.alt = info.name || '';
        img.loading = 'lazy';
        img.src = base + '/preview';
        box.appendChild(img);
        break;
      }
      case 'video': {
        const video = document.createElement('video');
        video.controls = true;
        video.preload = 'metadata';
        video.src = base + '/preview';
        box.appendChild(video);
        break;
      }
      case 'audio': {
        const audio = document.createElement('audio');
        audio.controls = true;
        audio.preload = 'metadata';
        audio.src = base + '/preview';
        box.appendChild(audio);
        break;
      }
      case 'pdf': {
        const frame = document.createElement('iframe');
        frame.title = info.name || 'PDF 预览';
        frame.src = base + '/preview';
        box.appendChild(frame);
        break;
      }
      case 'text':
      case 'code': {
        const pre = document.createElement('pre');
        pre.textContent = '正在加载…';
        box.appendChild(pre);
        loadTextPreview(pre);
        break;
      }
      default: {
        const p = document.createElement('p');
        p.className = 'share-hint';
        p.textContent = '该类型不支持在线预览，请下载后查看。';
        box.appendChild(p);
      }
    }
    show('shareFile');
  }

  async function loadTextPreview(pre) {
    try {
      const resp = await fetch(base + '/preview', { cache: 'no-store', credentials: 'same-origin' });
      if (!resp.ok) throw new Error('预览加载失败');
      if (!resp.body) {
        const text = await resp.text();
        pre.textContent = text.slice(0, TEXT_PREVIEW_LIMIT);
        return;
      }
      const reader = resp.body.getReader();
      const chunks = [];
      let total = 0;
      let truncated = false;
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        chunks.push(value);
        total += value.length;
        if (total > TEXT_PREVIEW_LIMIT) {
          truncated = true;
          await reader.cancel();
          break;
        }
      }
      const merged = new Uint8Array(Math.min(total, TEXT_PREVIEW_LIMIT));
      let offset = 0;
      for (const chunk of chunks) {
        if (offset >= merged.length) break;
        const take = Math.min(chunk.length, merged.length - offset);
        merged.set(chunk.subarray(0, take), offset);
        offset += take;
      }
      pre.textContent = new TextDecoder().decode(merged) + (truncated ? '\n\n（内容较长，仅显示前 1 MiB，请下载查看完整文件）' : '');
    } catch (_) {
      pre.textContent = '预览加载失败，请下载后查看。';
    }
  }

  /* ---------- 目录 ---------- */

  function joinPath(dir, name) {
    return (dir === '/' ? '' : dir.replace(/\/+$/, '')) + '/' + name;
  }

  function renderCrumbs(path) {
    const nav = $('dirCrumbs');
    nav.innerHTML = '';
    const rootName = (state.info && state.info.name) || '分享目录';
    const segs = path.split('/').filter(Boolean);
    const addCrumb = (label, target, current) => {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.textContent = label;
      if (current) btn.setAttribute('aria-current', 'page');
      if (!current) btn.addEventListener('click', () => openDir(target));
      nav.appendChild(btn);
    };
    addCrumb(rootName, '/', segs.length === 0);
    let acc = '';
    segs.forEach((seg, i) => {
      const sep = document.createElement('span');
      sep.className = 'crumb-sep';
      sep.textContent = '/';
      nav.appendChild(sep);
      acc += '/' + seg;
      addCrumb(seg, acc, i === segs.length - 1);
    });
  }

  async function openDir(path) {
    const seq = ++state.listSeq;
    show('shareDir');
    $('dirTitle').textContent = (state.info && state.info.name) || '分享目录';
    $('dirMeta').textContent = '正在加载…';
    $('dirError').classList.add('hidden');
    $('dirEmpty').classList.add('hidden');
    $('dirBody').innerHTML = '';
    renderCrumbs(path);
    $('dirZip').href = base + '/zip?path=' + enc(path);
    try {
      const data = await getJSON('/list?path=' + enc(path) + '&limit=2000');
      if (seq !== state.listSeq) return;
      state.dirPath = path;
      const entries = data.entries || [];
      $('dirMeta').textContent = entries.length + ' 项';
      renderEntries(entries, path);
    } catch (e) {
      if (seq !== state.listSeq) return;
      $('dirMeta').textContent = '';
      $('dirError').textContent = (STATUS_TEXT[e.code] && STATUS_TEXT[e.code][1]) || e.message || '读取目录失败';
      $('dirError').classList.remove('hidden');
    }
  }

  function renderEntries(entries, dirPath) {
    const body = $('dirBody');
    body.innerHTML = '';
    for (const e of entries) {
      const tr = document.createElement('tr');
      const nameCell = document.createElement('td');
      nameCell.className = 'col-name';
      const row = document.createElement(e.is_dir ? 'button' : 'span');
      row.className = 'share-entry';
      if (e.is_dir) {
        row.type = 'button';
        row.addEventListener('click', () => openDir(joinPath(dirPath, e.name)));
      }
      row.appendChild(icon(e.is_dir ? 'dir' : (e.kind || 'other')));
      const name = document.createElement('span');
      name.className = 'share-name';
      name.textContent = e.name;
      name.title = e.name;
      row.appendChild(name);
      nameCell.appendChild(row);

      const sizeCell = document.createElement('td');
      sizeCell.className = 'col-size';
      sizeCell.textContent = e.is_dir ? '—' : fmtSize(e.size);

      const timeCell = document.createElement('td');
      timeCell.className = 'col-time';
      timeCell.textContent = e.mtime ? fmtTime(e.mtime) : '';

      const actCell = document.createElement('td');
      actCell.className = 'col-act';
      if (!e.is_dir) {
        const dl = document.createElement('a');
        dl.className = 'share-row-dl';
        dl.textContent = '下载';
        dl.href = base + '/download?path=' + enc(joinPath(dirPath, e.name));
        dl.setAttribute('download', e.name);
        actCell.appendChild(dl);
      }

      tr.appendChild(nameCell);
      tr.appendChild(sizeCell);
      tr.appendChild(timeCell);
      tr.appendChild(actCell);
      body.appendChild(tr);
    }
    $('dirEmpty').classList.toggle('hidden', entries.length !== 0);
  }

  load();
})();

/* ============================================================
   FileServer 前端逻辑 — 原生 JS，无任何依赖
   ============================================================ */
'use strict';

/* ---------- 工具函数 ---------- */

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const enc = encodeURIComponent;
const dec = decodeURIComponent;

function fmtSize(n) {
  if (n == null) return '';
  if (n < 1024) return n + ' B';
  const u = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (v >= 100 ? v.toFixed(0) : v.toFixed(1)) + ' ' + u[i];
}

function fmtTime(ts) {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  const now = new Date();
  const pad = (x) => String(x).padStart(2, '0');
  const hm = pad(d.getHours()) + ':' + pad(d.getMinutes());
  const sameYear = d.getFullYear() === now.getFullYear();
  const ymd = d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
  return sameYear ? ymd + ' ' + hm : ymd;
}

function fmtDur(sec) {
  if (!isFinite(sec)) return '0:00';
  sec = Math.round(sec);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  const mm = String(m).padStart(2, '0'), ss = String(s).padStart(2, '0');
  return h > 0 ? `${h}:${mm}:${ss}` : `${m}:${ss}`;
}

const fileURL = (p) => '/api/file?path=' + enc(p);
const thumbURL = (p, w, h) => `/api/thumb?path=${enc(p)}&w=${w || 256}&h=${h || 256}`;
const zipURL = (p) => '/api/zip?path=' + enc(p);

async function api(url) {
  const resp = await fetch(url);
  if (!resp.ok) {
    let msg = '请求失败 (' + resp.status + ')';
    try { const j = await resp.json(); if (j.error) msg = j.error; } catch (_) { /* ignore */ }
    const err = new Error(msg);
    err.status = resp.status;
    throw err;
  }
  return resp.json();
}

let toastTimer = null;
function toast(msg, isError) {
  const el = $('toast');
  el.textContent = msg;
  el.classList.toggle('error', !!isError);
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), 3200);
}

/* ---------- 状态 ---------- */

const state = {
  path: '/',
  entries: [],
  view: localStorage.getItem('fs.view') || 'grid',
  sort: localStorage.getItem('fs.sort') || 'name',
  order: localStorage.getItem('fs.order') || 'asc',
  page: 0,
  pageSize: 300,
  searching: false,
  query: '',
};

const PAGE = 300;

/* ---------- 主题 ---------- */

function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
}
function initTheme() {
  const saved = localStorage.getItem('fs.theme');
  const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  applyTheme(saved || (prefersDark ? 'dark' : 'light'));
}
$('btnTheme').addEventListener('click', () => {
  const next = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
  applyTheme(next);
  localStorage.setItem('fs.theme', next);
});

/* ---------- 类型图标（线性 SVG） ---------- */

const KIND_ICONS = {
  dir: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/>',
  image: '<rect x="3.5" y="5" width="17" height="14" rx="2.5"/><circle cx="9" cy="10" r="1.6"/><path d="M4 17l4.5-4.5 3.5 3.5 3-3L20 17.5"/>',
  video: '<rect x="3" y="5.5" width="13" height="13" rx="2.5"/><path d="M16 10.5l5-3v9l-5-3"/>',
  audio: '<path d="M9 18V6l10-2v11.5"/><circle cx="6.5" cy="18" r="2.5"/><circle cx="16.5" cy="15.5" r="2.5"/>',
  pdf: '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/><path d="M9.5 12.5h5M9.5 15.5h5M9.5 9.5h2"/>',
  archive: '<path d="M4 8.5V20a1.5 1.5 0 0 0 1.5 1.5h13A1.5 1.5 0 0 0 20 20V8.5"/><rect x="3" y="4.5" width="18" height="4" rx="1.5"/><path d="M10 12h4M9 15h6"/>',
  text: '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/><path d="M9 12h6M9 15h6"/>',
  doc: '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/><path d="M9.5 12.5h5M9.5 15.5h5M9.5 9.5h2"/>',
  code: '<path d="M8.5 7.5L4 12l4.5 4.5M15.5 7.5L20 12l-4.5 4.5"/><path d="M13.5 5l-3 14"/>',
  other: '<path d="M7 3.5h7L19.5 9v11a1.5 1.5 0 0 1-1.5 1.5H7A1.5 1.5 0 0 1 5.5 20V5A1.5 1.5 0 0 1 7 3.5z"/><path d="M14 3.5V9h5.5"/>',
};
const KIND_COLOR = {
  dir: '', image: 'kind-image', video: 'kind-video', audio: 'kind-audio', pdf: 'kind-pdf',
  archive: 'kind-archive', text: 'kind-text', doc: 'kind-doc', code: 'kind-code', other: 'kind-other',
};
function kindIcon(kind, cls) {
  const svg = KIND_ICONS[kind] || KIND_ICONS.other;
  return `<span class="type-icon ${cls || ''}"><svg viewBox="0 0 24 24">${svg}</svg></span>`;
}
function fileKind(entry) { return entry.is_dir ? 'dir' : (entry.kind || 'other'); }

/* ---------- 面包屑 ---------- */

function renderCrumbs() {
  const nav = $('crumbs');
  nav.innerHTML = '';
  if (state.searching) {
    const crumb = document.createElement('span');
    crumb.className = 'crumb current';
    crumb.textContent = '搜索：' + state.query;
    nav.appendChild(crumb);
    const clear = document.createElement('button');
    clear.className = 'crumb';
    clear.textContent = '✕ 退出搜索';
    clear.addEventListener('click', exitSearch);
    nav.appendChild(clear);
    return;
  }
  const parts = state.path.split('/').filter(Boolean);
  const root = document.createElement('button');
  root.className = 'crumb' + (parts.length === 0 ? ' current' : '');
  root.textContent = '根目录';
  root.addEventListener('click', () => navigate('/'));
  nav.appendChild(root);
  let acc = '';
  parts.forEach((p, i) => {
    acc += '/' + p;
    const seg = acc; // 快照：闭包捕获当前值，避免循环结束后全部指向最终路径
    const sep = document.createElement('span');
    sep.className = 'crumb-sep';
    sep.textContent = '›';
    nav.appendChild(sep);
    const c = document.createElement('button');
    c.className = 'crumb' + (i === parts.length - 1 ? ' current' : '');
    c.textContent = p;
    c.title = p;
    if (i < parts.length - 1) c.addEventListener('click', () => navigate(seg));
    nav.appendChild(c);
  });
}

/* ---------- 渲染 ---------- */

async function loadList(path) {
  state.path = path || '/';
  state.page = 0;
  state.searching = false;
  $('searchInput').value = '';
  $('searchClear').classList.add('hidden');
  showSkeleton(true);
  try {
    const data = await api(`/api/list?path=${enc(state.path)}&sort=${state.sort}&order=${state.order}`);
    state.entries = data.entries;
    showSkeleton(false);
    render();
  } catch (e) {
    showSkeleton(false);
    toast(e.message, true);
  }
}

function showSkeleton(on) {
  $('skeleton').classList.toggle('hidden', !on);
  $('grid').classList.toggle('hidden', on);
  $('listView').classList.toggle('hidden', true);
  $('empty').classList.add('hidden');
  $('loadMore').classList.add('hidden');
}

function render() {
  renderCrumbs();
  const shown = state.entries.slice(0, (state.page + 1) * PAGE);
  const isGrid = state.view === 'grid';
  $('grid').classList.toggle('hidden', !isGrid);
  $('listView').classList.toggle('hidden', isGrid);
  if (shown.length === 0) {
    $('empty').classList.remove('hidden');
    $('emptyText').textContent = state.searching ? '没有匹配的文件' : '此文件夹为空';
  } else {
    $('empty').classList.add('hidden');
  }
  if (isGrid) renderGrid(shown); else renderList(shown);
  // 加载更多
  const more = state.entries.length > shown.length;
  $('loadMore').classList.toggle('hidden', !more);
  $('loadMoreInfo').textContent = `已显示 ${shown.length} / ${state.entries.length} 项`;
}

// 条目真实路径：搜索模式下用服务端返回的完整路径，浏览模式下拼接当前目录
function entryPath(e) {
  return e._searchPath || joinPath(state.path, e.name);
}

function renderGrid(entries) {
  const grid = $('grid');
  grid.innerHTML = '';
  for (const e of entries) {
    const kind = fileKind(e);
    const p = entryPath(e);
    const card = document.createElement('div');
    card.className = 'card kind-' + kind;
    card.dataset.path = e.name;

    const thumb = document.createElement('div');
    thumb.className = 'card-thumb';

    if (kind === 'dir') {
      thumb.classList.add('dir');
      thumb.innerHTML = `<div class="centered-icon"><div class="dir-icon"><svg viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/></svg></div></div>`;
      // zip 下载按钮
      const dl = document.createElement('button');
      dl.className = 'card-dl';
      dl.title = '打包下载';
      dl.innerHTML = '<svg viewBox="0 0 24 24"><path d="M12 4v11m0 0l-4-4m4 4l4-4M4 19h16"/></svg>';
      dl.addEventListener('click', (ev) => {
        ev.stopPropagation();
        triggerDownload(zipURL(p), e.name + '.zip');
      });
      thumb.appendChild(dl);
    } else if (kind === 'image') {
      const img = document.createElement('img');
      img.loading = 'lazy';
      img.decoding = 'async';
      img.alt = e.name;
      img.src = thumbURL(p, 300, 300);
      img.addEventListener('error', () => { img.remove(); });
      thumb.appendChild(img);
      thumb.appendChild(downloadBtn(e, p));
    } else if (kind === 'video') {
      // 视频：先显示图标，视口内异步抽帧
      const holder = document.createElement('div');
      holder.className = 'centered-icon';
      holder.innerHTML = kindIcon('video');
      thumb.appendChild(holder);
      const img = document.createElement('img');
      img.alt = e.name;
      img.className = 'hidden';
      thumb.appendChild(img);
      thumb.appendChild(downloadBtn(e, p));
      observeVideoThumb(e, img, holder, thumb, p);
    } else {
      const holder = document.createElement('div');
      holder.className = 'centered-icon';
      holder.innerHTML = kindIcon(kind);
      thumb.appendChild(holder);
      thumb.appendChild(downloadBtn(e, p));
    }

    const info = document.createElement('div');
    info.className = 'card-info';
    info.innerHTML = `<div class="card-name" title="${esc(e.name)}">${esc(e.name)}</div>
      <div class="card-meta">${kind === 'dir' ? '文件夹' : fmtSize(e.size)}${e.mtime ? ' · ' + fmtTime(e.mtime) : ''}</div>`;

    card.appendChild(thumb);
    card.appendChild(info);
    card.addEventListener('click', () => openEntry(e));
    grid.appendChild(card);
  }
}

function downloadBtn(e, p) {
  const dl = document.createElement('button');
  dl.className = 'card-dl';
  dl.title = '下载';
  dl.innerHTML = '<svg viewBox="0 0 24 24"><path d="M12 4v11m0 0l-4-4m4 4l4-4M4 19h16"/></svg>';
  dl.addEventListener('click', (ev) => {
    ev.stopPropagation();
    triggerDownload(fileURL(p), e.name);
  });
  return dl;
}

function renderList(entries) {
  const body = $('listBody');
  body.innerHTML = '';
  for (const e of entries) {
    const kind = fileKind(e);
    const p = entryPath(e);
    const tr = document.createElement('tr');
    tr.dataset.path = e.name;
    const nameCell = document.createElement('td');
    nameCell.className = 'col-name';
    nameCell.innerHTML = `<div class="row-name"><span class="${kind === 'dir' ? 'rdir' : 'rkind'}">${kindIcon(kind)}</span><span class="rname" title="${esc(e.name)}">${esc(e.name)}</span></div>`;
    const sizeCell = document.createElement('td');
    sizeCell.className = 'col-size';
    sizeCell.textContent = kind === 'dir' ? '—' : fmtSize(e.size);
    const timeCell = document.createElement('td');
    timeCell.className = 'col-time';
    timeCell.textContent = e.mtime ? fmtTime(e.mtime) : '';
    const actCell = document.createElement('td');
    actCell.className = 'col-act row-act';
    if (kind === 'dir') {
      const zipBtn = document.createElement('button');
      zipBtn.className = 'mini-btn';
      zipBtn.textContent = '打包';
      zipBtn.addEventListener('click', (ev) => {
        ev.stopPropagation();
        triggerDownload(zipURL(p), e.name + '.zip');
      });
      actCell.appendChild(zipBtn);
    } else {
      const dlBtn = document.createElement('button');
      dlBtn.className = 'mini-btn';
      dlBtn.textContent = '下载';
      dlBtn.addEventListener('click', (ev) => {
        ev.stopPropagation();
        triggerDownload(fileURL(p), e.name);
      });
      actCell.appendChild(dlBtn);
    }
    tr.appendChild(nameCell);
    tr.appendChild(sizeCell);
    tr.appendChild(timeCell);
    tr.appendChild(actCell);
    tr.addEventListener('click', () => openEntry(e));
    body.appendChild(tr);
  }
}

function joinPath(dir, name) {
  if (dir === '/') return '/' + name;
  return dir.replace(/\/+$/, '') + '/' + name;
}

// navigate 统一目录跳转：写入浏览器历史（URL 变为 /?path=xxx），
// 使物理返回键/手机返回手势按目录层级逐级回退，而不是退出应用
function navigate(path) {
  path = path || '/';
  if (!state.searching && path === state.path) return;
  const url = new URL(location.href);
  url.search = '';
  if (path !== '/') url.searchParams.set('path', path);
  history.pushState({ path }, '', url);
  loadList(path);
}

function openEntry(e) {
  const p = entryPath(e);
  if (e.is_dir) {
    navigate(p);
    return;
  }
  const kind = fileKind(e);
  if (kind === 'image') {
    openLightbox(p, state.entries.filter((x) => !x.is_dir && fileKind(x) === 'image'));
    return;
  }
  if (kind === 'video' || kind === 'audio' || kind === 'pdf' || kind === 'text' || kind === 'code') {
    openPreview(p, e);
    return;
  }
  // 其余类型直接下载
  triggerDownload(fileURL(p), e.name);
}

function triggerDownload(url, filename) {
  const a = document.createElement('a');
  a.href = url;
  a.download = filename || '';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/* ---------- 视频前端抽帧 ---------- */

const thumbCache = new Map(); // path -> dataURL（会话级）

// 观察者：卡片进入视口（含 400px 预加载区）后，把对应任务推入待处理队列。
// 注意：回调是批量异步的，不能在这里直接消费队列（否则未标记的卡片会被误删）。
const videoObserver = new IntersectionObserver((items) => {
  for (const it of items) {
    if (it.isIntersecting) {
      videoObserver.unobserve(it.target);
      const job = it.target.__videoJob;
      if (job && !job.started) {
        job.started = true;
        videoQueue.push(job);
      }
    }
  }
  pumpVideoThumbs();
}, { rootMargin: '400px' });

const videoQueue = [];
let activeGrabs = 0;
const MAX_ACTIVE = 2;

function observeVideoThumb(e, img, holder, thumbEl, p) {
  const key = p;
  if (thumbCache.has(key)) {
    img.src = thumbCache.get(key);
    img.classList.remove('hidden');
    holder.classList.add('hidden');
    return;
  }
  const job = { key, img, holder, thumbEl, tries: 0, started: false };
  thumbEl.__videoJob = job;
  videoObserver.observe(thumbEl);
}

// 队列中的任务已全部可见，按 2 并发消费
function pumpVideoThumbs() {
  while (activeGrabs < MAX_ACTIVE && videoQueue.length > 0) {
    const job = videoQueue.shift();
    activeGrabs++;
    grabVideoFrame(job).finally(() => {
      activeGrabs--;
      pumpVideoThumbs();
    });
  }
}

function grabVideoFrame(job) {
  return new Promise((resolve) => {
    const video = document.createElement('video');
    video.preload = 'metadata';
    video.muted = true;
    video.playsInline = true;
    const src = fileURL(job.key);
    video.src = src;

    const timeout = setTimeout(() => { video.src = ''; resolve(); }, 8000);

    video.addEventListener('loadedmetadata', () => {
      const dur = video.duration;
      const seekTo = isFinite(dur) && dur > 0 ? Math.min(1, dur * 0.1) : 0.1;
      try { video.currentTime = seekTo; } catch (_) { /* ignore */ }
    });
    video.addEventListener('seeked', () => {
      try {
        const canvas = document.createElement('canvas');
        canvas.width = 300;
        canvas.height = 300;
        const ctx = canvas.getContext('2d');
        const vw = video.videoWidth, vh = video.videoHeight;
        if (!vw || !vh) throw new Error('no size');
        const scale = Math.max(300 / vw, 300 / vh);
        const dw = vw * scale, dh = vh * scale;
        ctx.drawImage(video, (300 - dw) / 2, (300 - dh) / 2, dw, dh);
        const dataURL = canvas.toDataURL('image/jpeg', 0.72);
        thumbCache.set(job.key, dataURL);
        job.img.src = dataURL;
        job.img.classList.remove('hidden');
        job.holder.classList.add('hidden');
      } catch (_) { /* 降级为图标 */ }
      clearTimeout(timeout);
      video.src = '';
      resolve();
    });
    video.addEventListener('error', () => {
      clearTimeout(timeout);
      video.src = '';
      // 网络抖动等偶发失败：重试一次
      if (job.tries < 1) {
        job.tries++;
        videoQueue.push(job);
      }
      resolve();
    });
  });
}

/* ---------- 灯箱 ---------- */

const lb = {
  images: [],   // [{path, name}]
  index: 0,
  zoom: 1,
  rotate: 0,
  touchX: null,
};

function openLightbox(path, entries) {
  lb.images = entries.map((e) => ({ path: entryPath(e), name: e.name }));
  lb.index = lb.images.findIndex((x) => x.path === path);
  if (lb.index < 0) lb.index = 0;
  lb.zoom = 1;
  lb.rotate = 0;
  $('lightbox').classList.remove('hidden');
  document.body.style.overflow = 'hidden';
  lbShow();
}

function lbShow() {
  const img = $('lbImg');
  const it = lb.images[lb.index];
  img.classList.add('loading');
  img.style.transform = '';
  lb.zoom = 1;
  lb.rotate = 0;
  $('lbName').textContent = it.name;
  $('lbCount').textContent = `${lb.index + 1} / ${lb.images.length}`;
  $('lbPrev').classList.toggle('disabled', lb.images.length <= 1);
  $('lbNext').classList.toggle('disabled', lb.images.length <= 1);
  img.src = fileURL(it.path);
  img.onload = () => img.classList.remove('loading');
  img.onerror = () => { img.classList.remove('loading'); toast('图片加载失败', true); };
  lbApplyTransform();
}

function lbApplyTransform() {
  const img = $('lbImg');
  const rot = `rotate(${lb.rotate}deg)`;
  img.style.transform = `${rot} scale(${lb.zoom})`;
  $('lbZoomLabel').textContent = Math.round(lb.zoom * 100) + '%';
  img.classList.toggle('zoomed', lb.zoom > 1);
  img.style.cursor = lb.zoom > 1 ? 'grab' : 'zoom-in';
}

function lbNav(dir) {
  const n = lb.images.length;
  if (n <= 1) return;
  lb.index = (lb.index + dir + n) % n;
  lbShow();
}

function lbClose() {
  $('lightbox').classList.add('hidden');
  document.body.style.overflow = '';
  $('lbImg').src = '';
}

$('lbClose').addEventListener('click', lbClose);
$('lbPrev').addEventListener('click', () => lbNav(-1));
$('lbNext').addEventListener('click', () => lbNav(1));
$('lbZoomIn').addEventListener('click', () => { lb.zoom = Math.min(8, lb.zoom * 1.5); lbApplyTransform(); });
$('lbZoomOut').addEventListener('click', () => { lb.zoom = Math.max(0.25, lb.zoom / 1.5); lbApplyTransform(); });
$('lbRotate').addEventListener('click', () => { lb.rotate = (lb.rotate + 90) % 360; lbApplyTransform(); });
$('lbDownload').addEventListener('click', () => {
  const it = lb.images[lb.index];
  triggerDownload(fileURL(it.path), it.name);
});

$('lbImg').addEventListener('click', (e) => {
  if (lb.zoom > 1) { lb.zoom = 1; lbApplyTransform(); }
  else { lb.zoom = 2; lbApplyTransform(); }
});
$('lbImg').addEventListener('wheel', (e) => {
  e.preventDefault();
  const factor = e.deltaY < 0 ? 1.2 : 1 / 1.2;
  lb.zoom = Math.min(8, Math.max(0.25, lb.zoom * factor));
  lbApplyTransform();
}, { passive: false });

$('lightbox').addEventListener('touchstart', (e) => {
  if (e.target.closest('.lb-actions')) return;
  lb.touchX = e.touches[0].clientX;
}, { passive: true });
$('lightbox').addEventListener('touchend', (e) => {
  if (lb.touchX == null) return;
  const dx = e.changedTouches[0].clientX - lb.touchX;
  if (Math.abs(dx) > 48) lbNav(dx < 0 ? 1 : -1);
  lb.touchX = null;
}, { passive: true });

/* ---------- 预览页 ---------- */

// 预览页播放器资源管理：返回/切换时必须暂停并释放，否则视频和声音会继续播放
let pvVideo = null;      // 当前预览页 video/audio 元素
let pvKeyHandler = null; // 播放器键盘监听（避免重复注册泄漏）

function stopPreview() {
  if (pvVideo) {
    try {
      pvVideo.pause();
      pvVideo.removeAttribute('src');
      pvVideo.load(); // 释放媒体资源（停止下载/解码/声音）
    } catch (_) { /* ignore */ }
    pvVideo = null;
  }
  if (pvKeyHandler) {
    document.removeEventListener('keydown', pvKeyHandler);
    pvKeyHandler = null;
  }
  $('pvMain').innerHTML = ''; // 移除 video/embed 等 DOM 元素
}

function showBrowse() {
  stopPreview();
  $('preview').classList.add('hidden');
  $('browse').classList.remove('hidden');
}

function openPreview(path, entry) {
  // 使用 URL 参数记录预览目标，支持浏览器前进/后退
  const url = new URL(location.href);
  url.search = '';
  url.searchParams.set('view', path);
  history.pushState({ view: path }, '', url);
  renderPreview(path, entry);
}

function renderPreview(path, entry) {
  stopPreview(); // 清理上一个预览的播放资源
  $('browse').classList.add('hidden');
  $('preview').classList.remove('hidden');
  $('pvName').textContent = entry.name;
  $('pvMeta').textContent = fmtSize(entry.size) + (entry.mtime ? ' · ' + fmtTime(entry.mtime) : '');
  $('btnPvDownload').onclick = () => triggerDownload(fileURL(path), entry.name);
  $('btnBack').onclick = () => {
    if (history.length > 1) {
      history.back(); // 由 popstate 统一处理
    } else {
      // 无历史记录（直接打开预览链接）：原地返回根目录列表
      const url = new URL(location.href);
      url.search = '';
      history.replaceState({}, '', url);
      showBrowse();
      loadList('/');
    }
  };

  const main = $('pvMain');
  main.innerHTML = '';
  const kind = fileKind(entry);

  if (kind === 'image') {
    main.innerHTML = `<div class="pv-image"><img src="${esc(fileURL(path))}" alt="${esc(entry.name)}"></div>`;
  } else if (kind === 'video' || kind === 'audio') {
    buildPlayer(main, path, entry, kind);
  } else if (kind === 'pdf') {
    main.innerHTML = `<embed class="pv-embed" src="${esc(fileURL(path))}" type="application/pdf">`;
  } else if (kind === 'text' || kind === 'code') {
    if (entry.size > 2 * 1024 * 1024) {
      pvHint(main, '文件较大（超过 2MB），不进行在线预览', path, entry.name);
    } else {
      main.innerHTML = '<pre class="pv-text">加载中…</pre>';
      fetch(fileURL(path))
        .then((r) => r.text())
        .then((t) => { main.querySelector('.pv-text').textContent = t; })
        .catch(() => { main.innerHTML = '<div class="pv-hint">文本加载失败</div>'; });
    }
  } else {
    pvHint(main, '该文件类型不支持在线预览', path, entry.name);
  }
}

// pvHint 提示 + 下载按钮（动态绑定事件，避免文件名注入 inline onclick）
function pvHint(main, text, path, name) {
  const hint = document.createElement('div');
  hint.className = 'pv-hint';
  hint.textContent = text;
  main.appendChild(hint);
  if (path) {
    const btn = document.createElement('button');
    btn.className = 'btn primary';
    btn.textContent = '下载文件';
    btn.addEventListener('click', () => triggerDownload(fileURL(path), name));
    hint.appendChild(btn);
  }
}

/* ---------- 自定义播放器 ---------- */

function buildPlayer(container, path, entry, kind) {
  const isAudio = kind === 'audio';
  container.innerHTML = `
    <div class="player ${isAudio ? 'audio' : ''}" id="player">
      <video src="${esc(fileURL(path))}" ${isAudio ? '' : 'poster=""'}></video>
      <button class="big-play" id="bigPlay"><svg viewBox="0 0 24 24"><path d="M8 5.5v13l11-6.5z"/></svg></button>
      <div class="player-bar" id="playerBar">
        <button class="pb-btn" id="pbPlay" title="播放/暂停 (空格)"><svg viewBox="0 0 24 24" class="filled"><path d="M7 5v14l12-7z"/></svg></button>
        <div class="pb-progress" id="pbProgress">
          <div class="pb-track">
            <div class="pb-buffered" id="pbBuffered"></div>
            <div class="pb-played" id="pbPlayed"></div>
            <div class="pb-knob" id="pbKnob"></div>
          </div>
          <div class="pb-tooltip" id="pbTip"></div>
        </div>
        <span class="pb-time" id="pbTime">0:00 / 0:00</span>
        <div class="pb-vol">
          <button class="pb-btn" id="pbMute" title="静音 (M)"><svg viewBox="0 0 24 24"><path d="M4 9v6h4l5 4V5L8 9H4z"/></svg></button>
          <input type="range" class="pb-vol-slider" id="pbVol" min="0" max="100" value="100">
        </div>
        ${isAudio ? '' : '<select class="pb-rate" id="pbRate" title="倍速"><option value="0.5">0.5x</option><option value="0.75">0.75x</option><option value="1" selected>1x</option><option value="1.25">1.25x</option><option value="1.5">1.5x</option><option value="2">2x</option></select>'}
        ${isAudio ? '' : '<button class="pb-btn" id="pbFull" title="全屏 (F)"><svg viewBox="0 0 24 24"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg></button>'}
      </div>
    </div>`;

  const player = $('player');
  const video = player.querySelector('video');
  const bar = $('playerBar');
  const bigPlay = $('bigPlay');
  const pbPlay = $('pbPlay');
  const prog = $('pbProgress');
  const played = $('pbPlayed');
  const buffered = $('pbBuffered');
  const knob = $('pbKnob');
  const tip = $('pbTip');
  const timeEl = $('pbTime');
  const volSlider = $('pbVol');
  const muteBtn = $('pbMute');
  const rateSel = $('pbRate');
  const fullBtn = $('pbFull');

  let hideTimer = null;
  let dragging = false;

  const showBar = () => {
    player.classList.add('bar-visible');
    clearTimeout(hideTimer);
    hideTimer = setTimeout(() => {
      if (!video.paused && !dragging) player.classList.remove('bar-visible');
    }, 2500);
  };
  player.addEventListener('mousemove', showBar);
  player.addEventListener('touchstart', showBar, { passive: true });

  const updatePlayIcon = () => {
    pbPlay.innerHTML = video.paused
      ? '<svg viewBox="0 0 24 24" class="filled"><path d="M7 5v14l12-7z"/></svg>'
      : '<svg viewBox="0 0 24 24" class="filled"><path d="M7 5h3.5v14H7zM13.5 5H17v14h-3.5z"/></svg>';
    bigPlay.classList.toggle('hide', !video.paused && video.readyState > 0);
  };

  const fmt = (s) => fmtDur(s);
  const updateTime = () => {
    const d = video.duration || 0;
    const c = video.currentTime || 0;
    timeEl.textContent = `${fmt(c)} / ${fmt(d)}`;
    if (d > 0) {
      const pct = (c / d) * 100;
      played.style.width = pct + '%';
      knob.style.left = pct + '%';
    }
    try {
      if (video.buffered.length > 0) {
        const end = video.buffered.end(video.buffered.length - 1);
        buffered.style.width = (d > 0 ? (end / d) * 100 : 0) + '%';
      }
    } catch (_) { /* ignore */ }
  };

  video.addEventListener('timeupdate', updateTime);
  video.addEventListener('progress', updateTime);
  video.addEventListener('loadedmetadata', () => { updateTime(); showBar(); });
  video.addEventListener('play', updatePlayIcon);
  video.addEventListener('pause', updatePlayIcon);
  video.addEventListener('ended', () => { updatePlayIcon(); showBar(); });

  bigPlay.addEventListener('click', () => { video.play().catch(() => toast('播放失败', true)); });
  pbPlay.addEventListener('click', () => {
    if (video.paused) video.play().catch(() => toast('播放失败', true));
    else video.pause();
  });

  // 进度条交互
  const seekFromEvent = (e) => {
    const rect = prog.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width));
    if (video.duration) video.currentTime = ratio * video.duration;
  };
  prog.addEventListener('pointerdown', (e) => {
    dragging = true;
    prog.classList.add('dragging');
    prog.setPointerCapture(e.pointerId);
    seekFromEvent(e);
  });
  prog.addEventListener('pointermove', (e) => {
    const rect = prog.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width));
    tip.textContent = fmt(ratio * (video.duration || 0));
    tip.style.left = ratio * 100 + '%';
    if (dragging) seekFromEvent(e);
  });
  prog.addEventListener('pointerup', () => {
    dragging = false;
    prog.classList.remove('dragging');
    showBar();
  });

  // 音量
  muteBtn.addEventListener('click', () => {
    video.muted = !video.muted;
    muteBtn.innerHTML = video.muted
      ? '<svg viewBox="0 0 24 24"><path d="M4 9v6h4l5 4V5L8 9H4z"/><path d="M16 9l5 6M21 9l-5 6"/></svg>'
      : '<svg viewBox="0 0 24 24"><path d="M4 9v6h4l5 4V5L8 9H4z"/></svg>';
  });
  volSlider.addEventListener('input', () => {
    video.volume = volSlider.value / 100;
    video.muted = false;
  });

  // 倍速与全屏
  if (rateSel) rateSel.addEventListener('change', () => { video.playbackRate = parseFloat(rateSel.value); });
  if (fullBtn) fullBtn.addEventListener('click', () => {
    if (document.fullscreenElement) document.exitFullscreen();
    else player.requestFullscreen().catch(() => toast('全屏不可用', true));
  });

  // 键盘快捷键（注册为模块级唯一监听，便于返回时移除）
  const keys = (e) => {
    if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT') return;
    switch (e.key) {
      case ' ': e.preventDefault(); pbPlay.click(); break;
      case 'ArrowLeft': video.currentTime = Math.max(0, (video.currentTime || 0) - 5); break;
      case 'ArrowRight': video.currentTime = Math.min(video.duration || 0, (video.currentTime || 0) + 5); break;
      case 'ArrowUp': e.preventDefault(); volSlider.value = Math.min(100, +volSlider.value + 10); volSlider.dispatchEvent(new Event('input')); break;
      case 'ArrowDown': e.preventDefault(); volSlider.value = Math.max(0, +volSlider.value - 10); volSlider.dispatchEvent(new Event('input')); break;
      case 'm': case 'M': muteBtn.click(); break;
      case 'f': case 'F': if (fullBtn) fullBtn.click(); break;
    }
  };
  pvKeyHandler = keys;
  document.addEventListener('keydown', keys);
  pvVideo = video;
}

/* ---------- 搜索 ---------- */

let searchTimer = null;
$('searchInput').addEventListener('input', () => {
  clearTimeout(searchTimer);
  const q = $('searchInput').value.trim();
  $('searchClear').classList.toggle('hidden', !q);
  searchTimer = setTimeout(() => { if (q) doSearch(q); else exitSearch(); }, 320);
});
$('searchClear').addEventListener('click', () => {
  $('searchInput').value = '';
  exitSearch();
});
$('searchInput').addEventListener('keydown', (e) => {
  if (e.key === 'Escape') { $('searchInput').value = ''; exitSearch(); }
  if (e.key === 'Enter' && $('searchInput').value.trim()) {
    clearTimeout(searchTimer);
    doSearch($('searchInput').value.trim());
  }
});

async function doSearch(q) {
  state.searching = true;
  state.query = q;
  state.page = 0;
  showSkeleton(true);
  try {
    const data = await api(`/api/search?q=${enc(q)}&path=${enc(state.path)}&limit=1000`);
    state.entries = data.results.map((r) => ({
      name: r.name, is_dir: r.is_dir, size: r.size, mtime: r.mtime, kind: r.kind,
      _searchPath: r.path, _searchDir: r.path.replace(/\/[^/]*$/, '') || '/',
    }));
    showSkeleton(false);
    render();
    if (data.truncated) toast('结果过多，仅显示前 1000 项');
  } catch (e) {
    showSkeleton(false);
    toast(e.message, true);
    exitSearch();
  }
}

function exitSearch() {
  if (!state.searching) return;
  $('searchInput').value = '';
  $('searchClear').classList.add('hidden');
  loadList(state.path);
}

// 搜索模式下点击条目：目录进入其所在目录（写入历史），文件直接预览/下载
const _openEntry = openEntry;
openEntry = function (e) {
  if (state.searching) {
    if (e.is_dir) { navigate(e._searchDir); return; }
    const p = e._searchPath;
    const kind = fileKind(e);
    if (kind === 'image') openLightbox(p, state.entries.filter((x) => !x.is_dir && fileKind(x) === 'image'));
    else if (kind === 'video' || kind === 'audio' || kind === 'pdf' || kind === 'text' || kind === 'code') openPreview(p, e);
    else triggerDownload(fileURL(p), e.name);
    return;
  }
  _openEntry(e);
};

/* ---------- 工具栏 ---------- */

$('btnHome').addEventListener('click', () => { if (state.searching) exitSearch(); else navigate('/'); });
$('btnRefresh').addEventListener('click', () => {
  if (state.searching) doSearch(state.query); else loadList(state.path);
});
$('btnGrid').addEventListener('click', () => setView('grid'));
$('btnList').addEventListener('click', () => setView('list'));

function setView(v) {
  state.view = v;
  localStorage.setItem('fs.view', v);
  $('btnGrid').classList.toggle('active', v === 'grid');
  $('btnList').classList.toggle('active', v === 'list');
  render();
}

$('sortSelect').addEventListener('change', () => {
  state.sort = $('sortSelect').value;
  localStorage.setItem('fs.sort', state.sort);
  reloadWithSort();
});
$('btnOrder').addEventListener('click', () => {
  state.order = state.order === 'asc' ? 'desc' : 'asc';
  localStorage.setItem('fs.order', state.order);
  document.documentElement.dataset.order = state.order;
  reloadWithSort();
});

function reloadWithSort() {
  if (state.searching) doSearch(state.query);
  else loadList(state.path);
}

$('btnLoadMore').addEventListener('click', () => {
  state.page++;
  render();
});

/* ---------- 快捷键 ---------- */

document.addEventListener('keydown', (e) => {
  if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT') return;
  const lbOpen = !$('lightbox').classList.contains('hidden');
  const pvOpen = !$('preview').classList.contains('hidden');
  if (lbOpen) {
    if (e.key === 'Escape') lbClose();
    else if (e.key === 'ArrowLeft') lbNav(-1);
    else if (e.key === 'ArrowRight') lbNav(1);
    else if (e.key === '+' || e.key === '=') $('lbZoomIn').click();
    else if (e.key === '-') $('lbZoomOut').click();
    else if (e.key === 'r' || e.key === 'R') $('lbRotate').click();
    return;
  }
  if (e.key === 'Escape' && pvOpen) { history.back(); return; }
  if (pvOpen) return;
  if (e.key === '/') {
    e.preventDefault();
    $('searchInput').focus();
  } else if (e.key === 't' || e.key === 'T') {
    $('btnTheme').click();
  } else if (e.key === 'g' || e.key === 'G') {
    setView('grid');
  } else if (e.key === 'l' || e.key === 'L') {
    setView('list');
  } else if (e.key === 'F5') {
    e.preventDefault();
    $('btnRefresh').click();
  }
});

/* ---------- 浏览器历史 ---------- */

// URL 驱动导航：目录路径在 ?path= 参数，预览目标在 ?view= 参数。
// 物理返回键/手机返回手势/前进按钮都会触发 popstate，从这里恢复视图。
window.addEventListener('popstate', () => {
  const params = new URL(location.href).searchParams;
  const view = params.get('view');
  if (view) {
    // 回到预览页
    $('browse').classList.add('hidden');
    $('preview').classList.remove('hidden');
    const name = view.split('/').pop();
    renderPreview(view, { name, size: 0, mtime: 0 });
  } else {
    // 回到列表页：从 URL 恢复目录（同时清理预览播放资源）
    const path = params.get('path') || '/';
    showBrowse();
    loadList(path);
  }
});

/* ---------- 启动 ---------- */

(function init() {
  initTheme();
  document.documentElement.dataset.order = state.order;
  $('sortSelect').value = state.sort;
  setView(state.view);
  // 支持直接打开深层链接（刷新后也能恢复所在目录）
  const params = new URL(location.href).searchParams;
  const view = params.get('view');
  if (view) {
    const name = view.split('/').pop();
    renderPreview(view, { name, size: 0, mtime: 0 });
  } else {
    loadList(params.get('path') || '/');
  }
})();

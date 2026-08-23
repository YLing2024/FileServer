/* ============================================================
   FileServer 前端逻辑 — 原生 JS，无任何依赖
   ============================================================ */
'use strict';

/* ---------- 工具函数 ---------- */

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
/* ============================================================
   路径 → URL 编码（唯一出口）
   文件名可能包含任意字符（# % + & 空格 中文 emoji …），
   凡进入 URL 的路径/关键字必须经 pathParam 编码，禁止手写拼接。
   ============================================================ */
const enc = encodeURIComponent;
const dec = decodeURIComponent;

// pathParam 路径参数编码：query 值的安全编码（含 + 转义为 %2B）
const pathParam = (p) => enc(p);

// 所有 API 端点统一从这里构造 URL
// dl=1：强制附件下载原始文件；fs=1：小文件 faststart 化后的直链播放
const fileURL = (p, dl, fs) => '/api/file?path=' + pathParam(p) + (dl ? '&dl=1' : '') + (fs ? '&fs=1' : '');
const thumbURL = (p, w, h) => `/api/thumb?path=${pathParam(p)}&w=${w || 256}&h=${h || 256}`;
const zipURL = (p) => '/api/zip?path=' + pathParam(p);
const listURL = (p, sort, order, limit, offset) => `/api/list?path=${pathParam(p)}&sort=${sort}&order=${order}&limit=${limit || ''}&offset=${offset || 0}`;
const searchURL = (q, p, limit) => `/api/search?q=${pathParam(q)}&path=${pathParam(p)}&limit=${limit}`;
const videoInfoURL = (p) => '/api/video-info?path=' + pathParam(p);
const hlsURL = (p) => '/api/hls?path=' + pathParam(p) + '&f=index.m3u8';
const normalizeURL = (p) => '/api/normalize?path=' + pathParam(p);
const normalizeStatusURL = '/api/normalize/status';
const backupsURL = '/api/normalize/backups';
const restoreURL = (p) => '/api/normalize/restore?path=' + pathParam(p);
const deleteBackupURL = (p) => '/api/normalize/delete-backup?path=' + pathParam(p);

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
  searching: false,
  query: '',
  scrollMap: {},   // path -> 离开该目录时的滚动位置（返回时恢复，有上限）
  serverThumb: false, // 已废弃：视频缩略图 100% 浏览器抽帧（保留字段避免遗漏引用）
  hls: false,         // 服务端是否支持 HLS 转码（决定 hls.js 预加载）
  kinds: null,        // 服务端下发的扩展名→类型映射（4.1，前端不再各自维护）
  searchLimit: 1000,  // 搜索条数上限（4.5，从 /api/info 取服务端值）
  hasMore: false,     // 目录列表是否还有更多页（服务端分页 3.1）
  listSeq: 0,         // 列表加载序列号（竞态保护）
  weirdPaths: null,   // 当前目录怪封装视频的路径集合（Set，异步加载）
};

// 单次目录列表页大小（唯一来源，取代旧的分页常量）
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
// kindByExt 根据扩展名推断文件类型（刷新恢复预览时 entry 缺 kind 字段，据此兜底）
const KIND_EXT_MAP = [
  [/\.(jpg|jpeg|png|gif|webp|bmp|tiff|tif|svg|ico|avif|jfif)$/i, 'image'],
  [/\.(mp4|mkv|mov|webm|avi|wmv|flv|m4v|m2ts|3gp|rmvb|rm|mpg|mpeg|ogv)$/i, 'video'],
  [/\.(mp3|wav|flac|aac|ogg|m4a|wma|opus|mid|midi|ape|amr)$/i, 'audio'],
  [/\.pdf$/i, 'pdf'],
  [/\.(zip|rar|7z|tar|gz|bz2|xz|iso|zst)$/i, 'archive'],
  [/\.(txt|md|log|json|xml|yaml|yml|ini|conf|cfg|csv|toml|srt|ass|vtt|nfo|rtf|url)$/i, 'text'],
  [/\.(c|cpp|h|hpp|go|rs|py|js|mjs|ts|tsx|jsx|html|htm|css|scss|java|kt|swift|sh|bat|cmd|ps1|sql|php|rb|lua|pl|vue|svelte|dockerfile|gradle|properties)$/i, 'code'],
];
// kindByExt 根据扩展名推断文件类型（刷新恢复预览时 entry 缺 kind 字段，据此兜底）。
// 优先使用服务端 /api/info 下发的统一映射（4.1）；未加载前用本地表兜底。
function kindByExt(name) {
  if (state.kinds) {
    const dot = name.lastIndexOf('.');
    if (dot > 0) {
      const ext = '.' + name.slice(dot + 1).toLowerCase();
      if (state.kinds[ext]) return state.kinds[ext];
    }
    return 'other';
  }
  for (const [re, k] of KIND_EXT_MAP) if (re.test(name)) return k;
  return 'other';
}
function fileKind(entry) { return entry.is_dir ? 'dir' : (entry.kind || kindByExt(entry.name || '')); }

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
  state.searching = false;
  // 列表重新加载：旧网格的抽帧 video 元素随 DOM 销毁，但并发集合可能残留
  // （幽灵任务占满并发槽、导致新缩略图全部不生成）——清空自愈。
  activeGrabsSet.clear();
  window.__thumbGrabs = 0;
  $('searchInput').value = '';
  $('searchClear').classList.add('hidden');
  showSkeleton(true);
  // 请求竞态保护：快速连续导航时丢弃过期响应
  const seq = ++state.listSeq;
  try {
    const data = await api(listURL(state.path, state.sort, state.order, PAGE, 0));
    if (seq !== state.listSeq) return; // 已有更新的导航
    state.entries = data.entries;
    state.hasMore = !!data.truncated;
    state.weirdPaths = new Set(); // 先置空，避免旧的误标
    showSkeleton(false);
    render();
    // 返回本目录时恢复之前记住的滚动位置
    const saved = state.scrollMap[state.path];
    if (saved != null) requestAnimationFrame(() => window.scrollTo(0, saved));
    // 异步补怪封装标记（列表不阻塞判定）：完成后再渲染一次加 badge
    loadWeirdFlag(seq);
  } catch (e) {
    if (seq !== state.listSeq) return;
    showSkeleton(false);
    toast(e.message, true);
  }
}

// loadWeirdFlag 异步获取当前目录的怪封装路径集合（带缓存，服务端已优化顺序读）。
// 注意：不能全量 render()——会重建网格销毁在途抽帧 video 元素，
// 而 activeGrabs 计数不同步清零，导致并发槽永久占满、后续缩略图全部不生成。
// 改为就地给已有卡片补 badge，不打断抽帧。
async function loadWeirdFlag(seq) {
  try {
    const d = await api('/api/weird?path=' + pathParam(state.path));
    if (seq !== state.listSeq) return; // 已导航离开
    state.weirdPaths = new Set(d.weird || []);
    patchWeirdBadges();
  } catch (_) { /* 服务端不可用时静默，列表仍可用 */ }
}

// patchWeirdBadges 就地给已渲染的怪封装视频卡片/列表行补 badge + 规整按钮
function patchWeirdBadges() {
  // 补一个 badge+规整按钮到容器
  const addRow = (container, p) => {
    if (container.querySelector('.card-actions')) return; // 已有 badge
    const row = document.createElement('div');
    row.className = 'card-actions';
    const badge = document.createElement('span');
    badge.className = 'weird-badge';
    badge.textContent = '怪封装';
    badge.title = '该视频 moov 过大或 mdat 碎片化，起播较慢；可规整化使其秒开';
    const btn = document.createElement('button');
    btn.className = 'mini-btn normalize-btn';
    btn.textContent = '规整化';
    btn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      startNormalize(p, btn);
    });
    row.appendChild(badge);
    row.appendChild(btn);
    container.appendChild(row);
  };
  if (state.view === 'grid') {
    document.querySelectorAll('.card.kind-video').forEach((card) => {
      const p = card.dataset.path ? joinPath(state.path, card.dataset.path) : null;
      if (!p || !state.weirdPaths.has(p)) return;
      const info = card.querySelector('.card-info');
      if (info) addRow(info, p);
    });
  } else if (state.view === 'list') {
    document.querySelectorAll('#listBody tr').forEach((tr) => {
      const p = tr.dataset.path ? joinPath(state.path, tr.dataset.path) : null;
      if (!p || !state.weirdPaths.has(p)) return;
      const act = tr.querySelector('.row-act');
      if (act) addRow(act, p);
    });
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
  const shown = state.entries; // 服务端分页：entries 即当前已加载全部
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
  // 加载更多（服务端分页：还有未加载的页）
  $('loadMore').classList.toggle('hidden', !state.hasMore || state.searching);
  $('loadMoreInfo').textContent = state.hasMore
    ? `已加载 ${shown.length} 项，还有更多`
    : (shown.length ? `共 ${shown.length} 项` : '');
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
      // 网络抖动偶发失败：重试一次，再失败才移除（保留图标位）
      let tries = 0;
      img.addEventListener('error', () => {
        tries++;
        if (tries < 2) { img.src = thumbURL(p, 300, 300); }
        else { img.remove(); }
      });
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
    // 怪封装视频：加 badge + 规整按钮（仅 video 且服务端标注 weird）
    if (state.weirdPaths && state.weirdPaths.has(p) && kind === 'video') {
      const row = document.createElement('div');
      row.className = 'card-actions';
      const badge = document.createElement('span');
      badge.className = 'weird-badge';
      badge.textContent = '怪封装';
      badge.title = '该视频 moov 过大或 mdat 碎片化，起播较慢；可规整化使其秒开';
      const btn = document.createElement('button');
      btn.className = 'mini-btn normalize-btn';
      btn.textContent = '规整化';
      btn.addEventListener('click', (ev) => {
        ev.stopPropagation();
        startNormalize(p, btn);
      });
      row.appendChild(badge);
      row.appendChild(btn);
      info.appendChild(row);
    }

    card.appendChild(thumb);
    card.appendChild(info);
    card.addEventListener('click', () => onEntryClick(e));
    grid.appendChild(card);
  }
}

// startNormalize 触发对某个视频的规整化，并反馈到按钮
function startNormalize(p, btn) {
  if (btn) { btn.disabled = true; btn.textContent = '已加入队列…'; }
  fetch(normalizeURL(p), { method: 'POST' })
    .then(async (r) => {
      const j = await r.json().catch(() => ({}));
      if (!r.ok) {
        // 409 已入队/进行中：打开面板看进度（不弹错误，按钮保持"规整中"由列表刷新接管）
        if (r.status === 409) {
          if (btn) { btn.textContent = '规整中…'; }
          showNormalizePanel();
          refreshNormalizeStatus();
          return;
        }
        throw new Error(j.error || ('请求失败 ' + r.status));
      }
      if (btn) { btn.textContent = '规整中…'; }
      showNormalizePanel(); // 打开面板看进度
      refreshNormalizeStatus();
    })
    .catch((e) => {
      if (btn) { btn.disabled = false; btn.textContent = '规整化'; }
      toast(e.message, true);
    });
}

function downloadBtn(e, p) {
  const dl = document.createElement('button');
  dl.className = 'card-dl';
  dl.title = '下载';
  dl.innerHTML = '<svg viewBox="0 0 24 24"><path d="M12 4v11m0 0l-4-4m4 4l4-4M4 19h16"/></svg>';
  dl.addEventListener('click', (ev) => {
    ev.stopPropagation();
    triggerDownload(fileURL(p, true), e.name);
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
        triggerDownload(fileURL(p, true), e.name);
      });
      actCell.appendChild(dlBtn);
    }
    tr.appendChild(nameCell);
    tr.appendChild(sizeCell);
    tr.appendChild(timeCell);
    tr.appendChild(actCell);
    tr.addEventListener('click', () => onEntryClick(e));
    body.appendChild(tr);
  }
}

function joinPath(dir, name) {
  if (dir === '/') return '/' + name;
  return dir.replace(/\/+$/, '') + '/' + name;
}

// navigateOrBack 统一离开前的滚动记录：应用内跳转（navigate/后退/前进）都会调用
function rememberScroll() {
  state.scrollMap[state.path] = window.scrollY;
  // 会话级上限：浏览大量目录后丢弃最早记录，防内存膨胀（3.5）
  const keys = Object.keys(state.scrollMap);
  if (keys.length > 100) delete state.scrollMap[keys[0]];
}

// navigate 统一目录跳转：写入浏览器历史（URL 变为 /?path=xxx），
// 使物理返回键/手机返回手势按目录层级逐级回退，而不是退出应用
function navigate(path) {
  path = path || '/';
  if (!state.searching && path === state.path) return;
  rememberScroll(); // 离开前记住当前位置（返回时可恢复）
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

// 搜索模式下点击条目：目录进入其所在目录（写入历史），文件直接预览/下载
function openSearchEntry(e) {
  if (e.is_dir) { navigate(e._searchDir); return; }
  const p = e._searchPath;
  const kind = fileKind(e);
  if (kind === 'image') openLightbox(p, state.entries.filter((x) => !x.is_dir && fileKind(x) === 'image'));
  else if (kind === 'video' || kind === 'audio' || kind === 'pdf' || kind === 'text' || kind === 'code') {
    if (kind === 'video') pauseThumbs(); // 播放优先
    openPreview(p, e);
  }
  else triggerDownload(fileURL(p), e.name);
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

const thumbCache = new Map(); // path -> dataURL（会话级，带容量上限防无限膨胀）
const THUMB_CACHE_MAX = 200;

// thumbCacheSet 写入并淘汰最旧条目，避免浏览大量视频后内存膨胀（3.5）
function thumbCacheSet(key, val) {
  if (thumbCache.has(key)) thumbCache.delete(key);
  thumbCache.set(key, val);
  while (thumbCache.size > THUMB_CACHE_MAX) {
    const oldest = thumbCache.keys().next().value;
    if (oldest === undefined) break;
    thumbCache.delete(oldest);
  }
}

// 观察者：卡片进入视口（含 200px 预加载区）后，把前端抽帧任务推入待处理队列。
// 注意：回调是批量异步的，不能在这里直接消费队列（否则未标记的卡片会被误删）。
const videoObserver = new IntersectionObserver((items) => {
  for (const it of items) {
    // 目标已脱离 DOM（某个 render 重建了 grid）：解除观察，防止 observer 强引用
    // 旧 DOM 节点导致会话级内存泄漏（每次导航积累视频卡片个数的节点+闭包链）。
    if (!it.target.isConnected) {
      videoObserver.unobserve(it.target);
      continue;
    }
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
}, { rootMargin: '200px' });

const videoQueue = [];
// 前端抽帧并发：3 路在保证内存可控的前提下让大目录首屏缩略图更快铺满。
// 并发控制用 activeGrabsSet.size（真实运行中的 promise 数），而非单独计数器——
// 计数器在异常路径（元素回收/事件纠缠）下会残留，把并发槽占死导致后续缩略图
// 全部饿死（曾因此出现「滚动到后部的视频完全没有缩略图」）。
const MAX_ACTIVE = 3;

/* moov 预读预热已移除：按用户要求，服务端不做任何自动后台任务（不预热/不常驻）。
   怪封装文件的起播优化改为用户手动「规整化」（卡片上的规整按钮 / 规整化面板）。 */

function observeVideoThumb(e, img, holder, thumbEl, p) {
  const key = p;
  if (thumbCache.has(key)) {
    const cached = thumbCache.get(key);
    if (cached) {
      img.src = cached;
      img.classList.remove('hidden');
      holder.classList.add('hidden');
    }
    // cached == null：本会话内已知该视频缩略图生成失败，保持图标，不再重试
    return;
  }
  // 冷门格式（MKV/RMVB/HEVC 等）：
  // - ffmpeg 开启：缩略图由服务端生成（/api/thumb 返回 JPEG），用 <img> 加载；
  // - ffmpeg 关闭：浏览器无法解码这些容器，且服务端不生成——保持图标，绝不触发
  //   浏览器抽帧（否则 <video> 请求 /api/thumb-src 会被服务端整段回源，几百 MB 流量
  //   且浏览器深解只进不出）。注意判断必须在 state.hls 可用时才有服务端缩略图。
  const name = e.name || '';
  if (!/\.(mp4|m4v|mov|webm)$/i.test(name)) {
    if (state.hls) {
      const im = new Image();
      im.onload = () => {
        img.src = im.src;
        img.classList.remove('hidden');
        holder.classList.add('hidden');
        thumbCacheSet(key, im.src);
      };
      im.onerror = () => { thumbCacheSet(key, null); }; // 失败记忆，不重试
      im.src = thumbURL(p, 300, 300);
    } else {
      thumbCacheSet(key, null); // 不生成缩略图，保持图标且本会话不重试
    }
    return;
  }
  // 常规视频（MP4/WebM）：浏览器抽帧。
  // preload=metadata + seek 只做 Range 小读（moov + 目标帧附近），不整段下载，
  // 不占服务端任何资源。懒加载：卡片进入视口才入队（3 路并发）。
  enqueueFrontThumb(e, img, holder, thumbEl, p);
}

// thumbPaused=true（正在播放视频）时不再启动新的抽帧：抽帧的 Range 读与播放
// 抢机械硬盘/连接池，播放优先；返回列表后自动恢复。
let thumbPaused = false;
// 播放开始时中止所有在途抽帧 video 元素：立即释放连接与磁盘 IO 给视频。
// grabVideoFrame 会把自己注册进来，pauseThumbs 统一清空。
const activeGrabsSet = new Set();
window.__thumbGrabs = 0; // 测试钩子：进行中的前端抽帧数
function pauseThumbs() {
  thumbPaused = true;
  for (const v of activeGrabsSet) {
    try { v.src = ''; } catch (_) { /* ignore */ }
  }
  // 中止的在途抽帧立即出账（done() 里按集合成员判定，避免重复扣减）
  window.__thumbGrabs = Math.max(0, window.__thumbGrabs - activeGrabsSet.size);
  activeGrabsSet.clear();
}

function enqueueFrontThumb(e, img, holder, thumbEl, p) {
  const job = { key: p, img, holder, thumbEl, tries: 0, started: false };
  thumbEl.__videoJob = job;
  videoObserver.observe(thumbEl);
}

// 队列中的任务已全部可见，按 MAX_ACTIVE 并发消费（播放中不启动新任务）。
// 消费前检查卡片是否仍在视口附近（±3000px）：滚动经过的卡片若已远离视口，
// 直接跳过（保持图标，滚回时 observer 重新触发）。否则 146 个视频全部排队、
// 每个 16MB 抽帧源在机械盘上很慢，排在队首的几十个会饿死视口内最后入队的卡片。
function pumpVideoThumbs() {
  if (thumbPaused) return;
  while (activeGrabsSet.size < MAX_ACTIVE && videoQueue.length > 0) {
    const job = videoQueue.shift();
    // 卡片已被移除（重新渲染/离开列表）：放弃
    if (job.thumbEl && !job.thumbEl.isConnected) continue;
    // 卡片已滚出视口较远：跳过本次抽帧并重置观察——重新 observe 后
    // 不会立即触发（元素在视口外），用户滚回进入 200px 预加载区时才重新
    // 入队。避免「滚过的卡片排队占满、视口内卡片饿在队尾」。
    if (job.thumbEl) {
      const r = job.thumbEl.getBoundingClientRect();
      const vh = window.innerHeight || 800;
      if (r.bottom < -500 || r.top > vh + 500) {
        job.started = false;
        videoObserver.observe(job.thumbEl);
        continue;
      }
    }
    const p = grabVideoFrame(job);
    p.finally(() => pumpVideoThumbs());
  }
}

function grabVideoFrame(job) {
  return new Promise((resolve) => {
    // 播放已开始（thumbPaused）：不再启动新抽帧（队列任务由返回列表后的
    // 重新渲染接手）
    if (thumbPaused) { resolve(); return; }
    const video = document.createElement('video');
    video.preload = 'metadata';
    video.muted = true;
    video.playsInline = true;
    video.dataset.fsThumb = '1'; // 供端到端测试识别抽帧元素
    // 抽帧源走 /api/thumb-src：服务端只返回截短的合法 MP4（moov + 开头
    // 样本区，≤16MB），浏览器把它当完整文件解析——元数据秒读、seek 命中，
    // 绝不整段下载整个视频（Chromium 对 video 源发开区间 Range，整段回源
    // 会把 1.4GB 文件全传一遍）。
    const src = '/api/thumb-src?path=' + pathParam(job.key);
    video.src = src;
    activeGrabsSet.add(video);
    window.__thumbGrabs++;

    const done = () => {
      if (activeGrabsSet.delete(video) && window.__thumbGrabs > 0) {
        window.__thumbGrabs--;
      }
      // 安全中止后台下载：pause + removeAttribute('src') 不会像 src='' 那样触发
      // error 事件（避免与 error 处理器纠缠）；仅执行一次（died 防重复）。
      if (!video.__died) {
        video.__died = true;
        try { video.pause(); } catch (_) { /* ignore */ }
        try { video.removeAttribute('src'); video.load(); } catch (_) { /* ignore */ }
      }
    };
    // 卡死兜底：任务在 20s 内必须结束（任何状态），并立刻补位消费队列。
    const timeout = setTimeout(() => { done(); resolve(); }, 20000);

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
        thumbCacheSet(job.key, dataURL);
        job.img.src = dataURL;
        job.img.classList.remove('hidden');
        job.holder.classList.add('hidden');
      } catch (_) { /* 降级为图标 */ }
      clearTimeout(timeout);
      done();
      resolve();
    });
    // 加载失败：不重试（避免雪球）。done() 内部对已结束的元素幂等。
    video.addEventListener('error', () => {
      if (video.__died) return; // 已被 done() 清理（removeAttribute 触发的二次 error）
      clearTimeout(timeout);
      done();
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
  // 相邻图片预加载：切换方向几乎零等待
  for (const d of [1, -1]) {
    const n = lb.images[lb.index + d];
    if (n) { const pre = new Image(); pre.src = fileURL(n.path); }
  }
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
  triggerDownload(fileURL(it.path, true), it.name);
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
let pvHls = null;        // 当前 hls.js 实例（如有）
let pvHlsTimer = null;   // HLS manifest 刷新定时器
let pvHlsPath = null;    // 当前 HLS 播放的文件路径（离开时通知服务端终止转码）
let pvKeyHandler = null; // 播放器键盘监听（避免重复注册泄漏）

function stopPreview() {
  // 离开播放页：通知服务端终止该视频的转码会话。
  // 用户已不在这看，转码进程在机械硬盘上的持续读写会拖慢下一个视频的首片产出。
  if (pvHlsPath) {
    const ab = pvHlsPath;
    pvHlsPath = null;
    fetch(hlsURL(ab) + '&abandon=1').catch(() => {});
  }
  if (pvHlsTimer) {
    clearInterval(pvHlsTimer);
    pvHlsTimer = null;
  }
  if (pvHls) {
    try { pvHls.destroy(); } catch (_) { /* ignore */ }
    pvHls = null;
  }
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
  thumbPaused = false; // 回到列表页：恢复缩略图请求（重新渲染的卡片会自动触发）
  document.title = '文件服务器';
  $('preview').classList.add('hidden');
  $('browse').classList.remove('hidden');
}

function openPreview(path, entry) {
  // 记住列表页滚动位置（必须在切换视图前记录，此时 window.scrollY 仍是列表页的）
  rememberScroll();
  // 使用 URL 参数记录预览目标，支持浏览器前进/后退
  const url = new URL(location.href);
  url.search = '';
  url.searchParams.set('view', path);
  history.pushState({ view: path }, '', url);
  renderPreview(path, entry);
}

// exitPreviewOrBack 退出预览：有历史则 back（popstate 统一处理），
// 无历史（直接打开 ?view= 深链）则原地返回根目录列表。供 btnBack 与 Escape 复用。
function exitPreviewOrBack() {
  if (history.length > 1) {
    history.back(); // 由 popstate 统一处理
  } else {
    const url = new URL(location.href);
    url.search = '';
    history.replaceState({}, '', url);
    showBrowse();
    loadList('/');
  }
}

let pvSeq = 0; // 预览序号：renderPreview 每次递增，迟到的 fetch 据此丢弃（防覆盖竞态）

function renderPreview(path, entry) {
  stopPreview(); // 清理上一个预览的播放资源
  pvSeq++; // 新预览：使上一个预览的迟到 fetch 失效
  $('browse').classList.add('hidden');
  $('preview').classList.remove('hidden');
  $('pvName').textContent = entry.name;
  document.title = entry.name + ' - FileServer'; // 浏览器标签页显示文件名
  $('pvMeta').textContent = fmtSize(entry.size) + (entry.mtime ? ' · ' + fmtTime(entry.mtime) : '');
  $('btnPvDownload').onclick = () => triggerDownload(fileURL(path, true), entry.name);
  $('btnBack').onclick = exitPreviewOrBack;

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
    // 深链/刷新时 entry.size 为 0（未知），先经 /api/list 取真实 size 再决定，
    // 避免 >2MB 的大文本被绕过防护全量拉取（2.11）
    const doPreview = (size) => {
      // size<=0（未获取到/目录超限查不到）视为不可预览，走下载提示：
      // 避免「未获取到 → -1 → -1>2MB 为假 → 绕过 2MB 防护全量拉取大文本」。
      if (size <= 0 || size > 2 * 1024 * 1024) {
        pvHint(main, size <= 0 ? '无法确定文件大小，不进行在线预览' : '文件较大（超过 2MB），不进行在线预览', path, entry.name);
      } else {
        main.innerHTML = '<pre class="pv-text">加载中…</pre>';
        const seq = pvSeq; // 预览序号：迟到响应不覆盖新预览内容
        fetch(fileURL(path))
          .then((r) => r.text())
          .then((t) => {
            if (seq !== pvSeq) return; // 已打开其他预览，丢弃
            const el = main.querySelector('.pv-text');
            if (el) el.textContent = t;
          })
          .catch(() => {
            if (seq !== pvSeq) return;
            main.innerHTML = '<div class="pv-hint">文本加载失败</div>';
          });
      }
    };
    if (entry.size > 0) doPreview(entry.size);
    else getEntrySize(path).then(doPreview);
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

// getEntrySize 深链/刷新时经 /api/list 获取文件真实大小（2.11）
async function getEntrySize(path) {
  const dir = path.replace(/\/[^/]*$/, '') || '/';
  const name = path.split('/').pop();
  try {
    const data = await api(listURL(dir, 'name', 'asc', 2000, 0));
    const e = data.entries.find((x) => x.name === name);
    return e ? e.size : -1;
  } catch (_) {
    return -1;
  }
}

/* ---------- 自定义播放器 ---------- */

// video-info 结果会话级缓存（Map，带容量上限）：重复点开同一视频免探测往返
const videoInfoCache = new Map();
const VIDEO_INFO_CACHE_MAX = 200;

// hls.js 动态加载（按需，只在需要转码播放时拉取）
function ensureHls() {
  return new Promise((resolve, reject) => {
    if (window.Hls) return resolve();
    if (window.__hlsPromise) return window.__hlsPromise.then(resolve, reject);
    window.__hlsPromise = new Promise((res, rej) => {
      const s = document.createElement('script');
      s.src = '/hls.min.js';
      s.onload = () => { if (window.Hls) res(); else rej(new Error('hls.js 加载失败')); };
      s.onerror = () => rej(new Error('hls.js 加载失败'));
      document.head.appendChild(s);
    });
    window.__hlsPromise.then(resolve, reject);
  });
}

function buildPlayer(container, path, entry, kind) {
  const isAudio = kind === 'audio';
  container.innerHTML = `
    <div class="player ${isAudio ? 'audio' : ''}" id="player">
      <video preload="auto" playsinline ${isAudio ? '' : 'poster=""'}></video>
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

  // 播放请求统一入口：src 未就绪时记住意图（挂在元素上，供 direct/hls 就绪后触发），
  // 消除「用户在 video-info 探测返回前点播放 → play() 失败 → 再也不会自动播」的竞态
  const safePlay = () => {
    video.play().catch((err) => {
      if (err && err.name === 'AbortError' && document.contains(video) && pvVideo === video) {
        // play() 被一次新的 load 打断（源切换竞态）：元素还在且仍是当前播放器，稍后重试
        setTimeout(() => { if (pvVideo === video) video.play().catch(() => toast('播放失败', true)); }, 300);
      } else {
        toast('播放失败', true);
      }
    });
  };
  const requestPlay = () => {
    if (video.src || video.currentSrc) {
      safePlay();
    } else {
      video.__wantPlay = true;
    }
  };
  const firePendingPlay = () => {
    if (video.__wantPlay) {
      video.__wantPlay = false;
      safePlay();
    }
  };

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
  // 服务端下发的真实时长兜底：HLS EVENT 播放列表转码初期 duration 未知，
  // 进度条按 ffprobe 时长显示，拖拽定位依然可用。
  // 注意：hls.js 对无 ENDLIST 的 EVENT 流按 live 处理，video.duration = 已生成
  // 分片总时长并随转码增长——若用 video.duration，进度百分比会随播放回跳。
  // 因此 realDur 优先用 knownDur（ffprobe 真实总时长），仅当其未知时才回退 video.duration。
  let knownDur = 0;
  let switchedToHls = false; // 已切换到 hls.js（冷门格式直链失败是预期，不弹误导 toast）
  const realDur = () => (knownDur > 0 ? knownDur : (video.duration || 0));
  const updateTime = () => {
    const d = realDur();
    const c = video.currentTime || 0;
    timeEl.textContent = `${fmt(c)} / ${fmt(d)}`;
    if (d > 0) {
      const pct = Math.min(100, (c / d) * 100);
      played.style.width = pct + '%';
      knob.style.left = pct + '%';
    }
    try {
      if (video.buffered.length > 0) {
        const end = video.buffered.end(video.buffered.length - 1);
        buffered.style.width = (d > 0 ? Math.min(100, (end / d) * 100) : 0) + '%';
      }
    } catch (_) { /* ignore */ }
  };

  video.addEventListener('timeupdate', updateTime);
  video.addEventListener('progress', updateTime);
  video.addEventListener('loadedmetadata', () => { updateTime(); showBar(); });
  video.addEventListener('durationchange', updateTime);
  video.addEventListener('play', updatePlayIcon);
  video.addEventListener('pause', updatePlayIcon);
  video.addEventListener('ended', () => { updatePlayIcon(); showBar(); });

  // 缓冲状态：转码首片等待/网络缓冲期间显示转圈，避免误以为卡死。
  // 注意 buffering 必须覆盖 hide（播放中停顿时 bigPlay 是隐藏的），
  // 由 CSS `.big-play.buffering` 强制可见。
  video.addEventListener('waiting', () => bigPlay.classList.add('buffering'));
  video.addEventListener('canplay', () => { bigPlay.classList.remove('buffering'); updatePlayIcon(); });
  video.addEventListener('playing', () => { bigPlay.classList.remove('buffering'); updatePlayIcon(); });
  video.addEventListener('error', () => {
    bigPlay.classList.remove('buffering');
    // 直链失败对冷门格式是预期的（浏览器解不了容器）：即将/已切到 hls.js，
    // 此时不弹误导性 toast（否则每次打开冷门视频都闪「加载失败」）。
    // 延迟一拍再判断：error 可能先于 infoP.then 的 hls 切换执行。
    setTimeout(() => {
      if (pvVideo !== video) return; // 已离开预览
      if (switchedToHls) return;     // 已切 HLS（直链失败是预期）
      // HLS 转码中途失败等场景：给用户明确提示而非永远黑屏
      toast('视频加载失败（可能是编码不支持或服务端转码出错）', true);
    }, 0);
  });

  bigPlay.addEventListener('click', () => requestPlay());
  pbPlay.addEventListener('click', () => {
    if (video.paused) requestPlay();
    else video.pause();
  });

  // 进度条交互
  const seekFromEvent = (e) => {
    const rect = prog.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width));
    const d = realDur();
    if (d) video.currentTime = ratio * d;
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
    const d = realDur();
    tip.textContent = fmt(ratio * d);
    tip.style.left = Math.min(96, Math.max(4, ratio * 100)) + '%'; // 边界内不溢出
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
    // iOS Safari 只支持视频元素级全屏（webkitEnterFullscreen）
    else if (/iPhone|iPad|iPod/i.test(navigator.userAgent) && video.webkitEnterFullscreen) video.webkitEnterFullscreen();
    else player.requestFullscreen().catch(() => toast('全屏不可用', true));
  });

  // 键盘快捷键（注册为模块级唯一监听，便于返回时移除）
  const keys = (e) => {
    // 焦点在交互元素上时不接管（原生行为优先，避免空格/方向键双触发）
    const t = e.target;
    if (t && t.closest && t.closest('button, input, select, textarea, a')) return;
    switch (e.key) {
      case ' ': e.preventDefault(); pbPlay.click(); break;
      case 'ArrowLeft': video.currentTime = Math.max(0, (video.currentTime || 0) - 5); break;
      case 'ArrowRight': video.currentTime = Math.min(video.duration || knownDur || 0, (video.currentTime || 0) + 5); break;
      case 'ArrowUp': e.preventDefault(); volSlider.value = Math.min(100, +volSlider.value + 10); volSlider.dispatchEvent(new Event('input')); break;
      case 'ArrowDown': e.preventDefault(); volSlider.value = Math.max(0, +volSlider.value - 10); volSlider.dispatchEvent(new Event('input')); break;
      case 'm': case 'M': muteBtn.click(); break;
      case 'f': case 'F': if (fullBtn) fullBtn.click(); break;
    }
  };
  pvKeyHandler = keys;
  document.addEventListener('keydown', keys);
  pvVideo = video;

  // ---- 设置播放源 ----
  // 直链优先：立即设置 src 让浏览器立刻开始拉流/解析 moov，不等任何网络探测，
  // 起播不再被「等待 video-info 往返」串联拖慢。video-info 仅异步补时长（进度条）。
  // 因服务端已禁 ffmpeg/HLS，mode 恒为 direct；仅当出现 hls（异常残留）时兜底切换。
  if (isAudio) {
    video.src = fileURL(path); // 音频一律直链（浏览器原生支持）
    return;
  }
  video.src = fileURL(path); // 视频直链：立即拉流，浏览器 GPU 硬解

  // 异步补时长（不阻塞起播）：命中缓存立即返回，未命中则后台探测。
  // 失败结果（{} 且无关键字段）不入缓存，下次仍重试（否则瞬时错误会永久锁死该视频的时长）。
  const infoKey = 'vinfo:' + path;
  let infoP = videoInfoCache.get(infoKey);
  if (!infoP) {
    infoP = fetch(videoInfoURL(path)).then((r) => (r.ok ? r.json() : {})).catch(() => ({}));
    infoP.then((info) => {
      if (info && (info.mode === 'hls' || info.duration)) {
        videoInfoCache.set(infoKey, infoP); // 有效结果才缓存
      } else {
        videoInfoCache.delete(infoKey); // 失败：不留缓存，允许下次重试
      }
    }).catch(() => {});
    while (videoInfoCache.size > VIDEO_INFO_CACHE_MAX) {
      const oldest = videoInfoCache.keys().next().value;
      if (oldest === undefined) break;
      videoInfoCache.delete(oldest);
    }
  }
  infoP.then((info) => {
    if (pvVideo !== video) return; // 用户已离开该预览页
    if (info.mode === 'hls') {
      // 冷门格式（MKV/RMVB/HEVC 等）：服务端转码流（--ffmpeg 开启时）。
      // 先清掉预设置的直链 src（浏览器加载冷门容器会失败/黑屏），再挂 hls.js。
      // knownDur 设为服务端真实总时长：进度条/拖动用真实时长定位，
      // 避免 hls.js 把 EVENT 流的 media duration(已生成量)当总时长导致进度回跳。
      knownDur = info.duration || 0;
      updateTime();
      switchedToHls = true; // 标记已切 HLS，error 处理器据此跳过误导 toast
      try {
        video.removeAttribute('src');
        video.load();
      } catch (_) { /* ignore */ }
      attachHls(video, path);
      return;
    }
    knownDur = info.duration || 0;
    updateTime();
    // 用户已提前点过播放：src 已就绪，立即开播（消除等待 src 的竞态）
    firePendingPlay();
    // duration 可能尚未探测完成：稍后二次查询补真实总时长
    if (!info.duration) {
      setTimeout(() => {
        fetch(videoInfoURL(path))
          .then((r) => (r.ok ? r.json() : null))
          .then((info2) => {
            if (info2 && info2.duration && pvVideo === video) {
              knownDur = info2.duration;
              updateTime();
            }
          })
          .catch(() => {});
      }, 2000);
    }
  });
}

// attachHls 为 video 挂载 HLS 流：真 Safari 用原生 HLS；其他浏览器统一 hls.js
// （部分 Chromium 构建的 canPlayType('mpegurl') 返回 truthy 但原生实现有缺陷，
// 相对 URI 解析错误导致起播失败——只对 Safari 走原生路径）。
function attachHls(video, path) {
  const url = hlsURL(path);
  pvHlsPath = path;
  const isSafari = /^((?!chrome|android).)*safari/i.test(navigator.userAgent);
  if (isSafari && video.canPlayType('application/vnd.apple.mpegurl')) {
    video.src = url; // Safari 原生 HLS
    return;
  }
  ensureHls()
    .then(() => {
      if (pvVideo !== video) return; // 用户已离开该预览页
      if (!window.Hls || !Hls.isSupported()) {
        video.src = url; // 最后兜底：交给浏览器
        return;
      }
      // 服务端下发「VOD 快照」播放列表（带 ENDLIST，随转码增长分片），
      // hls.js 按 VOD 语义处理：首片即播、无直播追边缘问题。
      // fragLoadingTimeOut 与服务端分片等待上限对齐：seek 超前时服务端
      // 阻塞等分片生成（秒级），超时 404 由 hls.js 重试/恢复。
      // 服务端播放列表：转码进行中为 EVENT 流（无 ENDLIST，随转码增长分片），
      // 完成后带 ENDLIST 正常结束。hls.js 对无 ENDLIST 的流按 live 处理：
      // liveSyncDurationCount=999 让它不从「直播边缘」跳播（我们是从头生成的流），
      // 而是从第一个分片顺序播放，配合下方 8s 轮询持续拉取新分片。
      const hls = new Hls({
        enableWorker: true,
        backBufferLength: 30,
        liveSyncDurationCount: 999,
        fragLoadingTimeOut: 30000,
        fragLoadingMaxRetry: 4,
        fragLoadingRetryDelay: 500,
        manifestLoadingTimeOut: 30000,
        manifestLoadingMaxRetry: 4,
      });
      pvHls = hls;
      hls.loadSource(url);
      hls.attachMedia(video);
      // 用户已提前点过播放：媒体就绪后立即开播（attachMedia 后 play() 有效）
      if (video.__wantPlay) {
        video.__wantPlay = false;
        video.play().catch((err) => {
          if (err && err.name === 'AbortError' && document.contains(video) && pvVideo === video) {
            setTimeout(() => { if (pvVideo === video) video.play().catch(() => toast('播放失败', true)); }, 300);
          } else {
            toast('播放失败', true);
          }
        });
      }
      // seek 收敛：分片未生成到位时，超前 seek 会让 hls.js 长时间卡住甚至停播。
      // 维护「当前已生成总时长」，seek 目标超出时收敛到已生成范围（立刻有画面），
      // manifest 轮询持续推进该范围，转码完成后全片可拖。
      let hlsAvail = 0;
      const updateAvail = () => {
        fetch(url, { cache: 'no-store' })
          .then((r) => r.text())
          .then((txt) => {
            let total = 0;
            for (const ln of txt.split('\n')) {
              const m = ln.match(/^#EXTINF:([\d.]+)/);
              if (m) total += parseFloat(m[1]);
            }
            if (total > 0) hlsAvail = total;
          })
          .catch(() => {});
      };
      const onSeeking = () => {
        if (hlsAvail > 4 && video.currentTime > hlsAvail - 1.5) {
          video.currentTime = Math.max(0, hlsAvail - 3);
          // 去抖：seeking 会连续触发，只提示一次直到位置变化
          if (!onSeeking.__notified) { toast('已跳到当前可播位置', false); onSeeking.__notified = true; }
          setTimeout(() => { onSeeking.__notified = false; }, 1500);
        }
      };
      video.addEventListener('seeking', onSeeking);
      updateAvail();
      // 转码进行中：定时重载 manifest 让 hls.js 看到新分片。
      // 检测到 ENDLIST（转码完成）即停止轮询——hls.js 已按 VOD 结束，无需再推。
      pvHlsTimer = setInterval(() => {
        fetch(url, { cache: 'no-store' })
          .then((r) => r.text())
          .then((txt) => {
            updateAvail();
            if (txt.includes('#EXT-X-ENDLIST')) {
              clearInterval(pvHlsTimer);
              pvHlsTimer = null;
              return; // 转码完成，停止轮询
            }
            try { hls.startLoad(); } catch (_) { /* ignore */ }
          })
          .catch(() => {});
      }, 8000);
      hls.on(Hls.Events.ERROR, (_evt, data) => {
        if (data.fatal) {
          if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
            hls.startLoad(); // 网络错误自动恢复
          } else if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
            hls.recoverMediaError();
          } else {
            // 分片 404 等（seek 超前转码进度）：重载 manifest 重试，
            // 转码持续推进，稍后重试大概率成功
            setTimeout(() => { try { hls.startLoad(); } catch (_) { /* ignore */ } }, 2000);
          }
        }
      });
    })
    .catch(() => {
      video.src = url; // hls.js 加载失败：交给浏览器
    });
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
  state.hasMore = false;
  showSkeleton(true);
  const seq = ++state.listSeq;
  try {
    const data = await api(searchURL(q, state.path, state.searchLimit));
    if (seq !== state.listSeq) return;
    state.entries = data.results.map((r) => ({
      name: r.name, is_dir: r.is_dir, size: r.size, mtime: r.mtime, kind: r.kind,
      _searchPath: r.path, _searchDir: r.path.replace(/\/[^/]*$/, '') || '/',
    }));
    showSkeleton(false);
    render();
    if (data.truncated) toast(`结果过多，仅显示前 ${state.searchLimit} 项`);
  } catch (e) {
    if (seq !== state.listSeq) return;
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

// 条目点击统一入口：搜索模式走搜索路径，浏览模式走普通逻辑（4.7 单一函数，无重赋值）
function onEntryClick(e) {
  if (state.searching) { openSearchEntry(e); return; }
  // 点击视频：中止在途缩略图请求，播放优先（不抢机械硬盘）
  if (fileKind(e) === 'video') {
    pauseThumbs(); // 播放优先：中止在途缩略图请求，不抢机械硬盘
  }
  openEntry(e);
}

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

/* ---------- 怪封装规整化面板 ---------- */

let normPollTimer = null;

// showNormalizePanel 打开规整化/备份面板
function showNormalizePanel() {
  $('normalizePanel').classList.remove('hidden');
  refreshNormalizeStatus();
  refreshBackups();
  if (!normPollTimer) {
    normPollTimer = setInterval(() => {
      refreshNormalizeStatus();
      refreshBackups();
    }, 1200);
  }
}

function hideNormalizePanel() {
  $('normalizePanel').classList.add('hidden');
  if (normPollTimer) { clearInterval(normPollTimer); normPollTimer = null; }
}

// refreshNormalizeStatus 拉取任务队列并渲染
let normPrevActive = false; // 上一轮是否有进行中任务（用于"刚完成时刷新备份"）
let normStatusBusy = false; // in-flight 保护：上一轮未完成时跳过本轮，防请求堆积
async function refreshNormalizeStatus() {
  if (normStatusBusy) return;
  normStatusBusy = true;
  try {
    const d = await api(normalizeStatusURL);
    const list = d.tasks || [];
    const hasActive = list.some(x => ['queued', 'normalizing', 'verifying', 'backing_up'].includes(x.state));
    // 任务刚全部结束（之前在进行中、现在空闲/完成）：立即刷新备份列表
    if (normPrevActive && !hasActive) refreshBackups();
    normPrevActive = hasActive;
    $('normState').textContent = hasActive ? '规整化进行中…' : (list.length ? '空闲' : '暂无任务');
    const box = $('normTaskList');
    box.innerHTML = '';
    if (!list.length) {
      box.innerHTML = '<div class="norm-empty">暂无规整任务。在怪封装视频卡片上点「规整化」即可加入。</div>';
    }
    for (const t of list) {
      const row = document.createElement('div');
      row.className = 'norm-task';
      const pct = Math.round(t.percent || 0);
      row.innerHTML = `
        <div class="norm-task-head">
          <span class="norm-task-name" title="${esc(t.path)}">${esc(t.name)}</span>
          <span class="norm-task-state state-${esc(t.state)}">${esc(stateLabel(t.state))}</span>
        </div>
        <div class="norm-bar"><div class="norm-bar-fill" style="width:${pct}%"></div></div>
        ${t.err ? `<div class="norm-err">${esc(t.err)}</div>` : ''}`;
      box.appendChild(row);
    }
  } catch (_) { /* 服务不可用/未启动时静默 */ } finally {
    normStatusBusy = false;
  }
}

function stateLabel(s) {
  return { queued: '排队中', normalizing: '规整中', verifying: '校验中', backing_up: '备份中', done: '完成', failed: '失败' }[s] || s;
}

// refreshBackups 拉取备份列表并渲染
let normBackupsBusy = false; // in-flight 保护：备份列表全量 WalkDir 可能 >1.2s，防堆积
async function refreshBackups() {
  if (normBackupsBusy) return;
  normBackupsBusy = true;
  try {
    const d = await api(backupsURL);
    const list = d.backups || [];
    const box = $('backupList');
    box.innerHTML = '';
    if (!list.length) {
      box.innerHTML = '<div class="norm-empty">暂无备份。规整化会自动备份原文件。</div>';
    }
    for (const b of list) {
      const row = document.createElement('div');
      row.className = 'backup-item';
      row.innerHTML = `
        <div class="backup-info">
          <div class="backup-name" title="${esc(b.path)}">${esc(b.name)}</div>
          <div class="backup-meta">${fmtSize(b.size)} · ${fmtTime(b.mtime)}${b.exists ? '' : '（原文件不在）'}</div>
        </div>
        <div class="backup-actions">
          ${b.exists ? `<button class="mini-btn" data-act="restore" data-path="${esc(b.path)}">恢复</button>` : ''}
          <button class="mini-btn danger" data-act="del" data-path="${esc(b.path)}">删除</button>
        </div>`;
      box.appendChild(row);
    }
    // 绑定事件
    box.querySelectorAll('button[data-act]').forEach((btn) => {
      btn.addEventListener('click', async () => {
        const act = btn.dataset.act, path = btn.dataset.path;
        const url = act === 'restore' ? restoreURL(path) : deleteBackupURL(path);
        btn.disabled = true;
        try {
          const r = await fetch(url, { method: 'POST' });
          const j = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(j.error || '失败');
          toast(act === 'restore' ? '已恢复原文件' : '已删除备份', false);
          refreshBackups();
          refreshNormalizeStatus();
        } catch (e) {
          toast(e.message, true);
        } finally {
          btn.disabled = false;
        }
      });
    });
  } catch (_) { /* 静默 */ } finally {
    normBackupsBusy = false;
  }
}

// 面板按钮
$('btnNormalize').addEventListener('click', () => {
  if ($('normalizePanel').classList.contains('hidden')) showNormalizePanel();
  else hideNormalizePanel();
});
$('btnNormClose').addEventListener('click', hideNormalizePanel);

/* ---------- 冷门格式支持开关（ffmpeg） ---------- */

// setFFmpegUI 根据服务端能力更新按钮外观；refresh=true 时刷新列表（缩略图策略变化）
function setFFmpegUI(ffavail, hls, refresh) {
  const btn = $('btnFFmpeg');
  btn.classList.toggle('active', !!hls);
  btn.title = hls
    ? '冷门格式支持已开启（MKV/RMVB/HEVC 等可在线播放）— 点击关闭'
    : (ffavail ? '冷门格式支持已关闭 — 点击开启' : '冷门格式支持不可用（服务端无 ffmpeg）');
  if (refresh && state.path && !state.searching) {
    loadList(state.path);
  }
}

// 点击切换
$('btnFFmpeg').addEventListener('click', async () => {
  const btn = $('btnFFmpeg');
  const target = !btn.classList.contains('active');
  btn.disabled = true;
  try {
    const r = await fetch('/api/settings/ffmpeg', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled: target }),
    });
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(j.error || '切换失败');
    state.hls = !!j.hls;
    localStorage.setItem('fs.hls', state.hls ? '1' : '0');
    toast(state.hls ? '冷门格式支持已开启' : '冷门格式支持已关闭', false);
    setFFmpegUI(true, state.hls, true);
  } catch (e) {
    toast(e.message, true);
  } finally {
    btn.disabled = false;
  }
});

let loadMoreBusy = false; // 加载更多 in-flight 保护：防双击并发同 offset 重复条目
$('btnLoadMore').addEventListener('click', async () => {
  if (state.searching || loadMoreBusy) return;
  loadMoreBusy = true;
  const offset = state.entries.length;
  const seq = state.listSeq;
  try {
    const data = await api(listURL(state.path, state.sort, state.order, PAGE, offset));
    if (seq !== state.listSeq) return; // 已导航到其他目录，丢弃
    state.entries = state.entries.concat(data.entries);
    state.hasMore = !!data.truncated;
    render();
  } catch (e) {
    toast(e.message, true);
  } finally {
    loadMoreBusy = false;
  }
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
  if (e.key === 'Escape' && pvOpen) { exitPreviewOrBack(); return; }
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
    // 进入预览：不能调 rememberScroll()——当前页面若是预览页（预览→预览导航），
    // window.scrollY 是预览页的，会覆盖列表正确滚动位置。列表位置由
    // openPreview 在首次进入预览前记录；预览→预览不需要更新列表位置。
    $('browse').classList.add('hidden');
    $('preview').classList.remove('hidden');
    const name = view.split('/').pop();
    renderPreview(view, { name, size: 0, mtime: 0 });
  } else {
    // 回到列表页：从 URL 恢复目录。
    // 注意：这里【不能】调 rememberScroll()——popstate 时页面还显示着
    // 预览/子目录内容，window.scrollY 是那个页面的滚动位置，会覆盖
    // 此前正确记录的列表滚动位置（这正是"返回后滚动条丢失"的根因）。
    // 正确的列表滚动位置在进入预览/子目录前已由 openPreview/navigate 记录。
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
  // 探测服务端能力（HLS 转码、扩展名映射、搜索上限），缩略图已 100% 浏览器抽帧。
  const params = new URL(location.href).searchParams;
  fetch('/api/info')
    .then((r) => r.json())
    .then((info) => {
      if (info.kinds) state.kinds = info.kinds; // 统一扩展名映射（4.1）
      if (info.search_limit) state.searchLimit = info.search_limit; // 搜索上限（4.5）
      const ffavail = !!info.ffmpeg;
      // 用户上次的开关选择（localStorage）优先于服务端默认（--ffmpeg 参数）
      const saved = localStorage.getItem('fs.hls');
      let want = !!info.hls;
      if (saved !== null) {
        want = saved === '1';
        if (want !== !!info.hls) {
          // 同步服务端到用户选择（不阻塞初始化）
          fetch('/api/settings/ffmpeg', {
            method: 'POST', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enabled: want }),
          }).catch(() => {});
        }
      }
      state.hls = want && ffavail;
      setFFmpegUI(ffavail, state.hls, false);
    })
    .catch(() => {})
    .finally(() => {
      // 支持直接打开深层链接（刷新后也能恢复所在目录）
      const view = params.get('view');
      if (view) {
        const name = view.split('/').pop();
        renderPreview(view, { name, size: 0, mtime: 0 });
      } else {
        loadList(params.get('path') || '/');
      }
      // 服务端支持 HLS 时预热 hls.js：首个需要转码的视频点开时无需再等 400KB 脚本下载
      if (state.hls) ensureHls().catch(() => {});
    });
})();

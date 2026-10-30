/* ============================================================
   FileServer 文件管理前端 — 原生 JS，无依赖。
   多选 / 右键菜单 / 重命名 / 移动 / 复制 / 回收站。
   本文件由 index.html 在 app.js 之前加载，但所有入口都在
   app.js 调用 FSManage.init() 之后才执行（此时全局状态与工具函数已就绪）。
   ============================================================ */
'use strict';

window.FSManage = (() => {
  const MAX_BATCH = 200;
  const sel = new Set();
  const selState = { anchor: null, mode: false, suppressUntil: 0 };
  let inited = false;
  let confirmResolve = null;

  const transfer = {
    kind: 'move', paths: [], target: '/', expanded: new Set(['/']), children: new Map(), busy: false,
  };
  const trashState = { items: [], selected: new Set(), loading: false };

  const $ = (id) => document.getElementById(id);
  const isCoarse = () => window.matchMedia('(pointer: coarse)').matches;

  function entryOfPath(path) {
    return state.entries.find((e) => entryPath(e) === path) || null;
  }
  function parentPath(path) {
    const i = path.lastIndexOf('/');
    return i <= 0 ? '/' : path.slice(0, i);
  }
  function baseName(path) {
    return path.slice(path.lastIndexOf('/') + 1);
  }
  function selectedEntries() {
    return Array.from(sel).map(entryOfPath).filter(Boolean);
  }
  function selectedPaths() {
    return Array.from(sel);
  }
  function modalOpen() {
    return ['renameModal', 'transferModal', 'trashModal', 'confirmModal', 'resultModal', 'shareCreate', 'shareResult', 'shareList'].some((id) => {
      const el = $(id);
      return el && !el.classList.contains('hidden');
    });
  }
  function showError(id, msg) {
    const el = $(id);
    if (!el) return;
    el.textContent = msg || '';
    el.classList.toggle('hidden', !msg);
  }

  /* ---------- 选择状态 ---------- */

  // canSelect 是否允许进入选择模式：文件管理需要登录；仅分享时也允许单选，
  // 便于认证关闭的部署直接生成分享链接。
  function canSelect() { return !!state.manageAllowed || !!state.shareAllowed; }

  function setSelection(paths) {
    sel.clear();
    paths.forEach((p) => sel.add(p));
    if (!sel.size) selState.mode = false;
    decorate();
  }
  function toggleOne(path) {
    if (sel.has(path)) sel.delete(path);
    else sel.add(path);
    selState.anchor = path;
    if (!sel.size) selState.mode = false;
    decorate();
  }
  function selectRange(path) {
    const all = state.entries.map(entryPath);
    const to = all.indexOf(path);
    if (to < 0) return;
    const from = selState.anchor ? all.indexOf(selState.anchor) : to;
    if (from < 0) { setSelection([path]); selState.anchor = path; return; }
    const [a, b] = from < to ? [from, to] : [to, from];
    for (let i = a; i <= b; i++) sel.add(all[i]);
    decorate();
  }
  function clearSelection() {
    sel.clear();
    selState.anchor = null;
    selState.mode = false;
    decorate();
  }
  function selectAll() {
    setSelection(state.entries.map(entryPath));
    selState.anchor = state.entries.length ? entryPath(state.entries[0]) : null;
  }
  function decorate() {
    const on = canSelect();
    document.querySelectorAll('#grid .card, #listBody tr').forEach((el) => {
      el.classList.toggle('selected', on && sel.has(el.dataset.full));
    });
    const bar = $('selBar');
    if (!bar) return;
    const n = sel.size;
    bar.classList.toggle('hidden', !on || n === 0);
    $('selCount').textContent = `已选 ${n} 项`;
    $('selRename').disabled = n !== 1;
    ['selRename', 'selMove', 'selCopy', 'selDelete'].forEach((id) => {
      if ($(id)) $(id).classList.toggle('hidden', !state.manageAllowed);
    });
    if ($('selShare')) {
      $('selShare').classList.toggle('hidden', !state.shareAllowed);
      $('selShare').disabled = n !== 1;
    }
    document.body.classList.toggle('has-selection', on && n > 0);
  }

  // handleClick 返回 true 表示已处理（阻止默认打开）；false 交给 app.js 打开。
  function handleClick(e, ev) {
    if (!canSelect()) return false;
    const p = entryPath(e);
    if (isCoarse()) {
      if (Date.now() < selState.suppressUntil) return true;
      if (selState.mode) { toggleOne(p); return true; }
      return false; // 移动端未进入选择模式：轻点直接打开
    }
    // 仅分享（认证关闭）时保留原来的单击打开：分享入口走右键菜单/长按，
    // 不能为了分享把最常用的打开动作改成单击选中。
    if (!state.manageAllowed) {
      if (ev && (ev.ctrlKey || ev.metaKey)) { toggleOne(p); return true; }
      if (ev && ev.shiftKey) { selectRange(p); return true; }
      return false;
    }
    if (ev && (ev.ctrlKey || ev.metaKey)) { toggleOne(p); return true; }
    if (ev && ev.shiftKey) { selectRange(p); return true; }
    setSelection([p]);
    selState.anchor = p;
    return true;
  }

  // bindItem 为条目挂右键菜单、双击打开与移动端长按（不接管普通点击）。
  function bindItem(el, e, path) {
    el.dataset.full = path;
    el.addEventListener('dblclick', (ev) => {
      if (isCoarse()) return;
      ev.preventDefault();
      ev.stopPropagation();
      openEntryByEntry(e);
    });
    el.addEventListener('contextmenu', (ev) => {
      if (!canSelect()) return;
      ev.preventDefault();
      if (isCoarse()) return; // 触屏长按已有选择模式，不弹桌面菜单
      if (!sel.has(path)) { setSelection([path]); selState.anchor = path; }
      showContextMenu(ev.clientX, ev.clientY);
    });
    if (!isCoarse()) return;
    let timer = null;
    let longPressed = false;
    let sx = 0, sy = 0;
    const cancel = () => { if (timer) { clearTimeout(timer); timer = null; } };
    el.addEventListener('pointerdown', (ev) => {
      if (!canSelect() || ev.button !== 0) return;
      sx = ev.clientX; sy = ev.clientY;
      longPressed = false;
      cancel();
      timer = setTimeout(() => {
        timer = null;
        longPressed = true;
        selState.suppressUntil = Date.now() + 1200;
        selState.mode = true;
        if (!sel.has(path)) sel.add(path);
        selState.anchor = path;
        decorate();
      }, 500);
    }, { passive: true });
    el.addEventListener('pointermove', (ev) => {
      if (!timer) return;
      if (Math.abs(ev.clientX - sx) > 10 || Math.abs(ev.clientY - sy) > 10) cancel();
    }, { passive: true });
    ['pointerup', 'pointercancel', 'pointerleave'].forEach((name) => el.addEventListener(name, () => {
      cancel();
      if (longPressed) {
        // 长按后松手仍会补一个 click：延后一点继续吞掉，避免刚选中又被切换掉。
        selState.suppressUntil = Date.now() + 800;
        longPressed = false;
      }
    }, { passive: true }));
  }

  function openEntryByEntry(e) {
    if (state.searching) openSearchEntry(e);
    else openEntry(e);
  }

  function onNavigate() {
    clearSelection();
    hideContextMenu();
  }

  /* ---------- 右键菜单 ---------- */

  function menuButton(label, fn, danger) {
    const b = document.createElement('button');
    b.className = 'ctx-item' + (danger ? ' danger' : '');
    b.setAttribute('role', 'menuitem');
    b.textContent = label;
    b.addEventListener('click', () => { hideContextMenu(); fn(); });
    return b;
  }
  function showContextMenu(x, y) {
    const menu = $('ctxMenu');
    menu.innerHTML = '';
    const ents = selectedEntries();
    const n = ents.length;
    if (n === 1) {
      menu.appendChild(menuButton('打开', () => openEntryByEntry(ents[0])));
      menu.appendChild(menuButton('下载', () => downloadSelected()));
      if (window.FSShare && state.shareAllowed) {
        menu.appendChild(menuButton('分享', () => FSShare.openCreate(selectedPaths())));
      }
      if (state.manageAllowed) menu.appendChild(menuButton('重命名', openRename));
    } else {
      menu.appendChild(menuButton(`下载 ${n} 项`, () => downloadSelected()));
    }
    if (state.manageAllowed) {
      menu.appendChild(menuButton('移动到…', () => openTransfer('move')));
      menu.appendChild(menuButton('复制到…', () => openTransfer('copy')));
      menu.appendChild(menuButton('删除（移入回收站）', deleteSelected, true));
    }
    menu.classList.remove('hidden');
    const r = menu.getBoundingClientRect();
    menu.style.left = Math.max(8, Math.min(x, window.innerWidth - r.width - 8)) + 'px';
    menu.style.top = Math.max(8, Math.min(y, window.innerHeight - r.height - 8)) + 'px';
  }
  function hideContextMenu() {
    const menu = $('ctxMenu');
    if (menu) menu.classList.add('hidden');
  }

  /* ---------- 下载 ---------- */

  function downloadSelected() {
    const ents = selectedEntries();
    if (!ents.length) { toast('请先选择文件', true); return; }
    for (const e of ents) {
      const p = entryPath(e);
      if (e.is_dir) triggerDownload(zipURL(p), e.name + '.zip');
      else triggerDownload(fileURL(p, true), e.name);
    }
  }

  /* ---------- 重命名 ---------- */

  let renameTarget = null;

  function openRename() {
    if (!state.manageAllowed) return;
    if (sel.size !== 1) { toast('重命名只能单选', true); return; }
    const path = selectedPaths()[0];
    const e = entryOfPath(path);
    if (!e) return;
    renameTarget = { path, entry: e };
    const input = $('rnName');
    input.value = e.name;
    showError('rnError', '');
    $('rnOk').disabled = false;
    $('renameModal').classList.remove('hidden');
    input.focus();
    // 扩展名不全选：默认选中主名部分（目录则全选）。
    let end = e.name.length;
    if (!e.is_dir) {
      const dot = e.name.lastIndexOf('.');
      if (dot > 0) end = dot;
    }
    try { input.setSelectionRange(0, end); } catch (_) { /* 类型不支持时忽略 */ }
    validateRename();
  }

  function validateRename() {
    const input = $('rnName');
    const v = input.value;
    const target = renameTarget;
    if (!target) return;
    let msg = '';
    if (!v) msg = '名称不能为空';
    else if (v !== v.trim()) msg = '名称首尾不能有空白';
    else if (v === '.' || v === '..') msg = '名称不合法';
    else if (/[\/\\]/.test(v)) msg = '名称不能包含路径分隔符';
    else if (v.startsWith('.')) msg = '不能使用隐藏名称（点开头）';
    else if (/[<>:"|?*]/.test(v)) msg = '名称不能包含 < > : " | ? * 等字符';
    else if (v !== target.entry.name) {
      const lower = v.toLowerCase();
      const conflict = state.entries.some((x) => {
        const xp = entryPath(x);
        return xp !== target.path && x.name.toLowerCase() === lower;
      });
      if (conflict) msg = '同目录已存在同名项';
    }
    showError('rnError', msg);
    $('rnOk').disabled = !!msg || v === target.entry.name;
    return msg;
  }

  async function submitRename() {
    if (!renameTarget || $('rnOk').disabled) return;
    const name = $('rnName').value;
    const btn = $('rnOk');
    btn.disabled = true;
    try {
      const { r, j } = await postManage('/api/rename', { path: renameTarget.path, name });
      if (!r.ok) { showError('rnError', j.error || '重命名失败'); return; }
      const newPath = j.path;
      $('renameModal').classList.add('hidden');
      renameTarget = null;
      toast('已重命名', false);
      await refreshCurrent();
      setSelection([newPath]);
      selState.anchor = newPath;
      decorate();
    } catch (e) {
      if (e && e.message !== '未认证') showError('rnError', e.message);
    } finally {
      btn.disabled = false;
    }
  }

  /* ---------- 删除 ---------- */

  async function deleteSelected() {
    if (!sel.size) return;
    const paths = selectedPaths();
    const choice = await confirmBox({
      title: '删除',
      message: `将 ${paths.length} 项移入回收站，可恢复。`,
      okLabel: '移入回收站',
    });
    if (choice !== 'ok') return;
    try {
      const { r, j } = await postManage('/api/delete', { paths });
      if (!r.ok) throw new Error(j.error || '删除失败');
      const moved = j.moved || [];
      const failed = j.failed || [];
      clearSelection();
      await refreshCurrent();
      if (failed.length) showFailures('部分项目未能删除', failed);
      toast(failed.length ? `已删除 ${moved.length} 项，${failed.length} 项失败` : `已移入回收站 ${moved.length} 项`, !!failed.length);
    } catch (e) {
      if (e.message !== '未认证') toast(e.message, true);
    }
  }

  /* ---------- 移动 / 复制 ---------- */

  async function openTransfer(kind) {
    if (!state.manageAllowed || !sel.size) return;
    transfer.kind = kind;
    transfer.paths = selectedPaths();
    transfer.target = state.path || '/';
    transfer.expanded = new Set(['/']);
    transfer.children.clear();
    transfer.busy = false;
    $('trTitle').textContent = kind === 'move' ? '移动' : '复制';
    $('trOk').textContent = kind === 'move' ? '移动' : '复制';
    $('trNewName').value = '';
    showError('trError', '');
    $('trTree').innerHTML = '<p class="modal-hint">加载中…</p>';
    $('transferModal').classList.remove('hidden');
    try {
      await expandTo(transfer.target);
    } catch (e) {
      showError('trError', e.message || '目录加载失败');
    }
    renderTransferTree();
  }

  async function loadChildren(path) {
    if (transfer.children.has(path)) return transfer.children.get(path);
    const data = await api(listURL(path, 'name', 'asc', 500, 0));
    const dirs = (data.entries || []).filter((e) => e.is_dir);
    transfer.children.set(path, dirs);
    return dirs;
  }

  async function expandTo(path) {
    transfer.expanded.add('/');
    await loadChildren('/');
    let cur = '/';
    for (const part of path.split('/').filter(Boolean)) {
      transfer.expanded.add(cur);
      cur = joinPath(cur, part);
      await loadChildren(cur).catch(() => []);
      transfer.expanded.add(cur);
    }
  }

  function renderTransferTree() {
    const box = $('trTree');
    box.innerHTML = '';
    box.appendChild(treeRow('/', '根目录', 0, true));
    if (transfer.expanded.has('/')) renderTreeChildren(box, '/', 1);
    $('trTarget').textContent = '目标：' + transfer.target;
  }

  function renderTreeChildren(box, path, depth) {
    const kids = transfer.children.get(path) || [];
    if (!kids.length) {
      const empty = document.createElement('p');
      empty.className = 'tree-empty';
      empty.style.paddingLeft = (depth * 18 + 26) + 'px';
      empty.textContent = '（无子文件夹）';
      box.appendChild(empty);
      return;
    }
    for (const e of kids) {
      const p = joinPath(path, e.name);
      box.appendChild(treeRow(p, e.name, depth, false));
      if (transfer.expanded.has(p)) renderTreeChildren(box, p, depth + 1);
    }
  }

  function treeRow(path, label, depth, isRoot) {
    const row = document.createElement('div');
    row.className = 'tree-row' + (path === transfer.target ? ' active' : '');
    row.style.paddingLeft = (depth * 18 + 6) + 'px';
    row.setAttribute('role', 'treeitem');

    const caret = document.createElement('button');
    caret.className = 'tree-caret';
    caret.type = 'button';
    caret.textContent = transfer.expanded.has(path) ? '▾' : '▸';
    caret.setAttribute('aria-label', transfer.expanded.has(path) ? '收起' : '展开');
    caret.addEventListener('click', async (ev) => {
      ev.stopPropagation();
      if (transfer.expanded.has(path)) transfer.expanded.delete(path);
      else {
        transfer.expanded.add(path);
        try { await loadChildren(path); } catch (e) { showError('trError', e.message || '目录加载失败'); }
      }
      renderTransferTree();
    });

    const name = document.createElement('button');
    name.className = 'tree-name';
    name.type = 'button';
    name.textContent = label;
    name.title = path;
    name.addEventListener('click', () => {
      transfer.target = path;
      renderTransferTree();
    });

    row.appendChild(caret);
    row.appendChild(name);
    if (isRoot) row.classList.add('root');
    return row;
  }

  async function createTargetDir() {
    const name = $('trNewName').value.trim();
    if (!name) { showError('trError', '请输入文件夹名'); return; }
    showError('trError', '');
    try {
      const { r, j } = await postManage('/api/create', { dir: transfer.target, kind: 'dir', name });
      if (!r.ok) { showError('trError', j.error || '新建失败'); return; }
      const newPath = j.path;
      transfer.children.delete(transfer.target);
      transfer.expanded.add(transfer.target);
      await loadChildren(transfer.target);
      transfer.expanded.add(newPath);
      transfer.target = newPath;
      await loadChildren(newPath).catch(() => []);
      $('trNewName').value = '';
      renderTransferTree();
      toast('已新建文件夹', false);
    } catch (e) {
      if (e.message !== '未认证') showError('trError', e.message);
    }
  }

  async function runTransfer(mode) {
    if (transfer.busy) return;
    transfer.busy = true;
    const kind = transfer.kind;
    const btn = $('trOk');
    btn.disabled = true;
    try {
      let currentMode = mode;
      // 冲突询问后原地重试：不用递归，避免 finally 提前复位 busy 造成并发提交。
      for (;;) {
        const url = `/api/${kind}?mode=${encodeURIComponent(currentMode)}`;
        const { r, j } = await postManage(url, { paths: transfer.paths, dir: transfer.target });
        if (r.status === 409 && j.code === 'conflict') {
          const existing = j.existing || [];
          const choice = await confirmBox({
            title: '同名冲突',
            message: `目标目录已有同名项：${existing.join('、')}`,
            okLabel: '覆盖',
            thirdLabel: '自动改名',
          });
          if (choice === 'ok') { currentMode = 'overwrite'; continue; }
          if (choice === 'third') { currentMode = 'rename'; continue; }
          return;
        }
        if (!r.ok) { showError('trError', j.error || '操作失败'); return; }
        const done = (kind === 'move' ? j.moved : j.copied) || [];
        const failed = j.failed || [];
        $('transferModal').classList.add('hidden');
        clearSelection();
        await refreshCurrent();
        if (failed.length) showFailures('部分项目未能完成', failed);
        toast(failed.length ? `完成 ${done.length} 项，${failed.length} 项失败` : `${kind === 'move' ? '已移动' : '已复制'} ${done.length} 项`, !!failed.length);
        return;
      }
    } catch (e) {
      if (e.message !== '未认证') showError('trError', e.message);
    } finally {
      transfer.busy = false;
      btn.disabled = false;
    }
  }

  /* ---------- 回收站 ---------- */

  async function openTrash() {
    if (!state.manageAllowed) return;
    trashState.selected.clear();
    $('trashModal').classList.remove('hidden');
    await loadTrash();
  }

  async function loadTrash() {
    trashState.loading = true;
    $('trashList').textContent = '加载中…';
    showError('trashError', '');
    try {
      const data = await api('/api/trash');
      trashState.items = data.items || [];
      const valid = new Set(trashState.items.map((x) => x.id));
      trashState.selected = new Set(Array.from(trashState.selected).filter((id) => valid.has(id)));
      renderTrash(data.total_size || 0);
    } catch (e) {
      if (e.message !== '未认证') {
        trashState.items = [];
        $('trashList').textContent = '加载失败';
        showError('trashError', e.message);
      }
    } finally {
      trashState.loading = false;
    }
  }

  function kindLabel(item) {
    if (item.is_dir) return '文件夹';
    return ({
      image: '图片', video: '视频', audio: '音频', pdf: 'PDF', archive: '压缩包',
      text: '文本', doc: '文档', code: '代码', other: '文件',
    })[item.kind] || '文件';
  }

  function renderTrash(totalSize) {
    const list = $('trashList');
    list.innerHTML = '';
    const items = trashState.items;
    $('trashUsage').textContent = items.length
      ? `${items.length} 项，共 ${fmtSize(totalSize)}`
      : '回收站为空';
    if (!items.length) {
      const empty = document.createElement('p');
      empty.className = 'modal-hint';
      empty.textContent = '暂无可恢复的项目';
      list.appendChild(empty);
    }
    for (const item of items) {
      const row = document.createElement('div');
      row.className = 'trash-row';
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.checked = trashState.selected.has(item.id);
      cb.setAttribute('aria-label', '选择 ' + item.path);
      cb.addEventListener('change', () => {
        if (cb.checked) trashState.selected.add(item.id);
        else trashState.selected.delete(item.id);
        syncTrashAll();
      });
      const meta = document.createElement('div');
      meta.className = 'trash-info';
      const title = document.createElement('div');
      title.className = 'trash-name';
      title.textContent = item.path;
      title.title = item.path;
      const sub = document.createElement('div');
      sub.className = 'trash-meta';
      sub.textContent = `${kindLabel(item)} · ${fmtSize(item.size)} · ${fmtTime(item.deleted_at)}`;
      meta.appendChild(title);
      meta.appendChild(sub);
      row.appendChild(cb);
      row.appendChild(meta);
      list.appendChild(row);
    }
    syncTrashAll();
  }

  function syncTrashAll() {
    const all = $('trashAll');
    const n = trashState.items.length;
    const picked = trashState.selected.size;
    all.checked = n > 0 && picked === n;
    all.indeterminate = picked > 0 && picked < n;
    all.disabled = n === 0;
  }

  function trashSelectedIds() {
    return Array.from(trashState.selected);
  }

  async function restoreTrash(mode) {
    const ids = trashSelectedIds();
    if (!ids.length) { toast('请先选择回收站条目', true); return; }
    try {
      const { r, j } = await postManage('/api/trash/restore', { ids, mode });
      if (r.status === 409 && j.code === 'conflict') {
        const existing = j.existing || [];
        const choice = await confirmBox({
          title: '恢复冲突',
          message: `原位置已有同名项：${existing.join('、')}`,
          okLabel: '自动改名',
          cancelLabel: '取消',
        });
        if (choice === 'ok') return restoreTrash('rename');
        return;
      }
      if (!r.ok) { showError('trashError', j.error || '恢复失败'); return; }
      const restored = j.restored || [];
      const failed = j.failed || [];
      trashState.selected.clear();
      await loadTrash();
      if (restored.length) await refreshCurrent();
      if (failed.length) showFailures('部分项目未能恢复', failed);
      toast(failed.length ? `恢复 ${restored.length} 项，${failed.length} 项失败` : `已恢复 ${restored.length} 项`, !!failed.length);
    } catch (e) {
      if (e.message !== '未认证') showError('trashError', e.message);
    }
  }

  async function emptyTrash(all) {
    const ids = all ? [] : trashSelectedIds();
    if (!all && !ids.length) { toast('请先选择回收站条目', true); return; }
    const count = all ? trashState.items.length : ids.length;
    if (!count) { toast('回收站为空'); return; }
    const choice = await confirmBox({
      title: all ? '清空回收站' : '彻底删除',
      message: `将彻底删除 ${count} 项，不可恢复。`,
      okLabel: all ? '清空回收站' : '彻底删除',
      danger: true,
    });
    if (choice !== 'ok') return;
    try {
      const body = all ? { all: true } : { ids };
      const { r, j } = await postManage('/api/trash/empty', body);
      if (!r.ok) { showError('trashError', j.error || '操作失败'); return; }
      const failed = j.failed || [];
      trashState.selected.clear();
      await loadTrash();
      if (failed.length) showFailures('部分项目未能删除', failed);
      toast(failed.length ? `彻底删除 ${count - failed.length} 项，${failed.length} 项失败` : '已彻底删除', !!failed.length);
    } catch (e) {
      if (e.message !== '未认证') showError('trashError', e.message);
    }
  }

  /* ---------- 通用确认 / 失败明细 ---------- */

  function confirmBox(opts) {
    return new Promise((resolve) => {
      if (confirmResolve) { const old = confirmResolve; confirmResolve = null; old('cancel'); }
      confirmResolve = resolve;
      $('cfTitle').textContent = opts.title || '确认';
      $('cfMessage').textContent = opts.message || '';
      $('cfOk').textContent = opts.okLabel || '确定';
      $('cfOk').classList.toggle('danger', !!opts.danger);
      $('cfCancel').textContent = opts.cancelLabel || '取消';
      const third = $('cfThird');
      if (opts.thirdLabel) {
        third.textContent = opts.thirdLabel;
        third.classList.remove('hidden');
      } else third.classList.add('hidden');
      $('confirmModal').classList.remove('hidden');
      $('cfCancel').focus();
    });
  }
  function finishConfirm(value) {
    const r = confirmResolve;
    confirmResolve = null;
    $('confirmModal').classList.add('hidden');
    if (r) r(value);
  }

  function showFailures(title, failed) {
    if (!failed || !failed.length) return;
    $('rsTitle').textContent = title;
    const list = $('resultList');
    list.innerHTML = '';
    for (const f of failed) {
      const li = document.createElement('li');
      const p = document.createElement('div');
      p.className = 'rf-path';
      p.textContent = f.path || '';
      const err = document.createElement('div');
      err.className = 'rf-error';
      err.textContent = f.error || '操作失败';
      li.appendChild(p);
      li.appendChild(err);
      list.appendChild(li);
    }
    $('resultModal').classList.remove('hidden');
  }

  /* ---------- 网络 / 刷新 ---------- */

  async function postManage(url, body) {
    const r = await authFetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    let j = {};
    try { j = await r.json(); } catch (_) { /* 非 JSON 响应按空对象处理 */ }
    return { r, j };
  }

  function refreshCurrent() {
    return state.searching ? doSearch(state.query) : loadList(state.path);
  }

  /* ---------- 初始化与快捷键 ---------- */

  function applyCapabilities() {
    const btn = $('btnTrash');
    if (btn) btn.classList.toggle('hidden', !state.manageAllowed);
    if (!canSelect()) {
      clearSelection();
      hideContextMenu();
    }
    decorate();
  }

  function shortcut(ev) {
    if (!canSelect()) return false;
    const tag = (ev.target.tagName || '').toLowerCase();
    const typing = tag === 'input' || tag === 'textarea' || tag === 'select' || ev.target.isContentEditable;
    if (ev.key === 'Escape') {
      const ctx = $('ctxMenu');
      if (ctx && !ctx.classList.contains('hidden')) { hideContextMenu(); return true; }
      if ($('confirmModal') && !$('confirmModal').classList.contains('hidden')) { finishConfirm('cancel'); return true; }
      if ($('renameModal') && !$('renameModal').classList.contains('hidden')) {
        $('renameModal').classList.add('hidden');
        renameTarget = null;
        return true;
      }
      if ($('transferModal') && !$('transferModal').classList.contains('hidden')) { $('transferModal').classList.add('hidden'); return true; }
      if ($('trashModal') && !$('trashModal').classList.contains('hidden')) { $('trashModal').classList.add('hidden'); return true; }
      if ($('resultModal') && !$('resultModal').classList.contains('hidden')) { $('resultModal').classList.add('hidden'); return true; }
      if ($('shareCreate') && !$('shareCreate').classList.contains('hidden')) { $('shareCreate').classList.add('hidden'); return true; }
      if ($('shareResult') && !$('shareResult').classList.contains('hidden')) { $('shareResult').classList.add('hidden'); return true; }
      if ($('shareList') && !$('shareList').classList.contains('hidden')) { $('shareList').classList.add('hidden'); return true; }
      if (typing) return false;
      if (sel.size || selState.mode) { clearSelection(); return true; }
      return false;
    }
    if (typing || modalOpen()) return false;
    if ($('browse').classList.contains('hidden')) return false;
    if ((ev.ctrlKey || ev.metaKey) && (ev.key === 'a' || ev.key === 'A')) {
      selectAll();
      return true;
    }
    if (ev.key === 'Delete') {
      if (!state.manageAllowed || !sel.size) return false;
      deleteSelected();
      return true;
    }
    if (ev.key === 'F2') {
      if (!state.manageAllowed || sel.size !== 1) return false;
      openRename();
      return true;
    }
    return false;
  }

  function init() {
    if (inited) return;
    inited = true;

    $('btnTrash').addEventListener('click', openTrash);
    $('selDownload').addEventListener('click', downloadSelected);
    $('selRename').addEventListener('click', openRename);
    $('selMove').addEventListener('click', () => openTransfer('move'));
    $('selCopy').addEventListener('click', () => openTransfer('copy'));
    $('selDelete').addEventListener('click', deleteSelected);
    $('selExit').addEventListener('click', clearSelection);

    $('rnCancel').addEventListener('click', () => { $('renameModal').classList.add('hidden'); renameTarget = null; });
    $('rnOk').addEventListener('click', submitRename);
    $('rnName').addEventListener('input', validateRename);
    $('rnName').addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter') { ev.preventDefault(); submitRename(); }
      if (ev.key === 'Escape') { $('renameModal').classList.add('hidden'); renameTarget = null; }
    });

    $('trCancel').addEventListener('click', () => $('transferModal').classList.add('hidden'));
    $('trOk').addEventListener('click', () => runTransfer('ask'));
    $('trNewBtn').addEventListener('click', createTargetDir);
    $('trNewName').addEventListener('keydown', (ev) => { if (ev.key === 'Enter') { ev.preventDefault(); createTargetDir(); } });
    $('transferModal').addEventListener('keydown', (ev) => { if (ev.key === 'Escape') $('transferModal').classList.add('hidden'); });

    $('trashClose').addEventListener('click', () => $('trashModal').classList.add('hidden'));
    $('trashRestore').addEventListener('click', () => restoreTrash('ask'));
    $('trashDelete').addEventListener('click', () => emptyTrash(false));
    $('trashEmpty').addEventListener('click', () => emptyTrash(true));
    $('trashAll').addEventListener('change', () => {
      if ($('trashAll').checked) trashState.items.forEach((it) => trashState.selected.add(it.id));
      else trashState.selected.clear();
      renderTrash(trashState.items.reduce((sum, it) => sum + (it.size || 0), 0));
    });
    $('trashModal').addEventListener('keydown', (ev) => { if (ev.key === 'Escape') $('trashModal').classList.add('hidden'); });

    $('cfCancel').addEventListener('click', () => finishConfirm('cancel'));
    $('cfOk').addEventListener('click', () => finishConfirm('ok'));
    $('cfThird').addEventListener('click', () => finishConfirm('third'));
    $('confirmModal').addEventListener('keydown', (ev) => {
      if (ev.key === 'Escape') finishConfirm('cancel');
      if (ev.key === 'Enter') finishConfirm('ok');
    });

    $('resultClose').addEventListener('click', () => $('resultModal').classList.add('hidden'));
    $('resultModal').addEventListener('keydown', (ev) => { if (ev.key === 'Escape') $('resultModal').classList.add('hidden'); });

    document.addEventListener('keydown', (ev) => {
      if (shortcut(ev)) {
        ev.preventDefault();
        ev.stopPropagation();
      }
    }, true);

    document.addEventListener('click', (ev) => {
      if (!ev.target.closest || !ev.target.closest('#ctxMenu')) hideContextMenu();
    });

    // 空白区域点击退出选择（条目点击由 app.js / 本条目的处理器负责）。
    $('grid').addEventListener('click', (ev) => { if (ev.target === $('grid')) clearSelection(); });
    $('listBody').addEventListener('click', (ev) => { if (ev.target === $('listBody')) clearSelection(); });
  }

  return {
    init,
    applyCapabilities,
    bindItem,
    handleClick,
    onNavigate,
    decorate,
    selectedPaths,
  };
})();

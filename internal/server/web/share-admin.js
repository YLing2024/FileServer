/* ============================================================
   分享链接管理前端 — 创建弹窗、结果复制、分享列表/撤销/删记录。
   依赖 app.js 的 state / entryPath / toast / authFetch / fmtTime，
   只在管理页加载；公开页使用独立的 share.js。
   ============================================================ */
'use strict';

window.FSShare = (() => {
  const $ = (id) => document.getElementById(id);
  const STATUS_LABEL = {
    active: '有效',
    expired: '已过期',
    revoked: '已撤销',
    exhausted: '次数用完',
  };
  // 去掉了 0/O/1/I 等易混字符，自动生成的提取码更好读。
  const CODE_CHARS = 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789';

  let targetPath = '';
  let inited = false;
  let result = { url: '', code: '' };

  function absolute(url) {
    return /^https?:\/\//i.test(url) ? url : location.origin + url;
  }

  async function copyText(text) {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch (_) { /* 回退到 execCommand */ }
    try {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand('copy');
      ta.remove();
      return ok;
    } catch (_) {
      return false;
    }
  }

  function generatedCode() {
    const out = [];
    if (window.crypto && window.crypto.getRandomValues) {
      const buf = new Uint8Array(4);
      window.crypto.getRandomValues(buf);
      for (const b of buf) out.push(CODE_CHARS[b % CODE_CHARS.length]);
    } else {
      for (let i = 0; i < 4; i++) out.push(CODE_CHARS[Math.floor(Math.random() * CODE_CHARS.length)]);
    }
    return out.join('');
  }

  function showError(id, msg) {
    const el = $(id);
    if (!el) return;
    el.textContent = msg || '';
    el.classList.toggle('hidden', !msg);
  }

  function resetCreateForm() {
    $('scDays').value = '7';
    $('scCodeOn').checked = false;
    $('scCode').value = '';
    $('scCode').disabled = true;
    $('scMax').value = '0';
    $('scPreview').checked = true;
    $('scNote').value = '';
    showError('scError', '');
  }

  function openCreate(paths) {
    if (!state.shareAllowed) return;
    if (!paths || !paths.length) { toast('请先选择文件或文件夹', true); return; }
    if (paths.length > 1) { toast('分享只支持单个文件或文件夹', true); return; }
    targetPath = paths[0];
    const entry = state.entries.find((e) => entryPath(e) === targetPath) || null;
    const name = targetPath.slice(targetPath.lastIndexOf('/') + 1) || targetPath;
    $('scTarget').textContent = name + (entry && entry.is_dir ? '（文件夹）' : '');
    $('scDirHint').classList.toggle('hidden', !(entry && entry.is_dir));
    resetCreateForm();
    $('shareCreate').classList.remove('hidden');
    $('scNote').focus();
  }

  function closeCreate() { $('shareCreate').classList.add('hidden'); }

  async function submitCreate() {
    const days = Number($('scDays').value);
    const maxDownloads = Number($('scMax').value);
    const codeOn = $('scCodeOn').checked;
    const code = $('scCode').value.trim();
    if (codeOn && code.length !== 4) {
      showError('scError', '提取码必须是 4 位字母或数字');
      $('scCode').focus();
      return;
    }
    const btn = $('scCreate');
    btn.disabled = true;
    showError('scError', '');
    try {
      const body = {
        path: targetPath,
        days,
        max_downloads: maxDownloads,
        allow_preview: $('scPreview').checked,
        note: $('scNote').value.trim(),
      };
      if (codeOn) body.code = code;
      const resp = await authFetch('/api/share', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      let j = {};
      try { j = await resp.json(); } catch (_) { /* 非 JSON 响应 */ }
      if (!resp.ok) throw new Error(j.error || '创建失败');
      closeCreate();
      result = { url: absolute(j.url), code: j.code || '' };
      $('srLink').value = result.url;
      $('srCodeRow').classList.toggle('hidden', !result.code);
      $('srCode').textContent = result.code;
      $('srCopyAll').textContent = result.code ? '复制链接+提取码' : '复制链接';
      $('shareResult').classList.remove('hidden');
      $('srLink').select();
      toast('分享链接已创建', false);
    } catch (e) {
      showError('scError', e.message || '创建失败');
    } finally {
      btn.disabled = false;
    }
  }

  function closeResult() { $('shareResult').classList.add('hidden'); }

  async function copyResult(all) {
    const text = all && result.code
      ? result.url + '\n提取码：' + result.code
      : (all ? result.url : result.url);
    if (await copyText(text)) toast('已复制', false);
    else toast('复制失败，请手动选择', true);
  }

  /* ---------- 分享列表 ---------- */

  function statusBadge(status) {
    const span = document.createElement('span');
    span.className = 'share-badge' + (status === 'active' ? ' active' : '') + (status === 'exhausted' ? ' danger' : '');
    span.textContent = STATUS_LABEL[status] || status;
    return span;
  }

  function metaItem(text) {
    const span = document.createElement('span');
    span.textContent = text;
    return span;
  }

  function renderList(items) {
    const box = $('slItems');
    box.innerHTML = '';
    $('slEmpty').classList.toggle('hidden', items.length !== 0);
    for (const it of items) {
      const card = document.createElement('div');
      card.className = 'share-item' + (it.status === 'active' ? '' : ' inactive');
      const head = document.createElement('div');
      head.className = 'share-item-head';
      const name = document.createElement('span');
      name.className = 'share-item-name';
      name.textContent = it.name || '（已删除）';
      head.appendChild(name);
      head.appendChild(statusBadge(it.status));
      if (it.is_dir) {
        const dirBadge = document.createElement('span');
        dirBadge.className = 'share-badge';
        dirBadge.textContent = '文件夹';
        head.appendChild(dirBadge);
      }
      if (it.code_set) {
        const codeBadge = document.createElement('span');
        codeBadge.className = 'share-badge';
        codeBadge.textContent = '提取码';
        head.appendChild(codeBadge);
      }
      card.appendChild(head);

      const link = document.createElement('div');
      link.className = 'share-item-link';
      link.textContent = absolute(it.url);
      card.appendChild(link);

      const meta = document.createElement('div');
      meta.className = 'share-item-meta';
      meta.appendChild(metaItem(it.expires ? '有效期至 ' + fmtTime(it.expires) : '永久有效'));
      meta.appendChild(metaItem('已下载 ' + (it.downloads || 0) + (it.max_downloads ? ' / ' + it.max_downloads : ' / 不限')));
      meta.appendChild(metaItem('创建于 ' + fmtTime(it.created)));
      if (it.note && it.note !== '共享文件') meta.appendChild(metaItem('备注：' + it.note));
      card.appendChild(meta);

      const actions = document.createElement('div');
      actions.className = 'share-item-actions';
      const copyBtn = document.createElement('button');
      copyBtn.className = 'btn';
      copyBtn.textContent = '复制链接';
      copyBtn.addEventListener('click', async () => {
        if (await copyText(absolute(it.url))) toast('已复制', false);
        else toast('复制失败，请手动选择', true);
      });
      actions.appendChild(copyBtn);
      if (it.status === 'active') {
        const revokeBtn = document.createElement('button');
        revokeBtn.className = 'btn danger';
        revokeBtn.textContent = '撤销';
        revokeBtn.addEventListener('click', async () => {
          if (!window.confirm('撤销后链接立即失效，确定撤销？')) return;
          await manageAction('/api/share/revoke', it.token, '撤销失败');
        });
        actions.appendChild(revokeBtn);
      }
      const delBtn = document.createElement('button');
      delBtn.className = 'btn';
      delBtn.textContent = '删除记录';
      delBtn.addEventListener('click', async () => {
        if (!window.confirm('删除记录不会删除源文件，确定删除？')) return;
        await manageAction('/api/share/record', it.token, '删除失败', 'DELETE');
      });
      actions.appendChild(delBtn);
      card.appendChild(actions);
      box.appendChild(card);
    }
  }

  async function manageAction(url, token, failMsg, method) {
    try {
      const resp = await authFetch(url, {
        method: method || 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token }),
      });
      let j = {};
      try { j = await resp.json(); } catch (_) { /* 非 JSON 响应 */ }
      if (!resp.ok) throw new Error(j.error || failMsg);
      toast('已更新', false);
      await loadList();
    } catch (e) {
      showError('slError', e.message || failMsg);
    }
  }

  async function loadList() {
    showError('slError', '');
    try {
      const resp = await authFetch('/api/share');
      const j = await resp.json();
      if (!resp.ok) throw new Error(j.error || '加载失败');
      renderList(j.shares || []);
    } catch (e) {
      showError('slError', e.message || '加载失败');
    }
  }

  async function openList() {
    if (!state.shareAllowed) return;
    $('slItems').innerHTML = '';
    $('slEmpty').classList.add('hidden');
    $('shareList').classList.remove('hidden');
    await loadList();
  }

  function init() {
    if (inited) return;
    inited = true;
    $('btnShares').addEventListener('click', openList);
    $('slClose').addEventListener('click', () => $('shareList').classList.add('hidden'));
    $('shareList').addEventListener('keydown', (e) => { if (e.key === 'Escape') $('shareList').classList.add('hidden'); });

    $('selShare').addEventListener('click', () => {
      if (!window.FSManage) return;
      openCreate(FSManage.selectedPaths());
    });

    $('scCancel').addEventListener('click', closeCreate);
    $('shareCreate').addEventListener('keydown', (e) => { if (e.key === 'Escape') closeCreate(); });
    $('scCreate').addEventListener('click', submitCreate);
    $('scCodeOn').addEventListener('change', () => {
      const on = $('scCodeOn').checked;
      $('scCode').disabled = !on;
      if (on && !$('scCode').value) $('scCode').value = generatedCode();
      if (on) $('scCode').focus();
    });
    $('scCode').addEventListener('input', () => {
      $('scCode').value = $('scCode').value.replace(/[^A-Za-z0-9]/g, '').toUpperCase().slice(0, 4);
    });

    $('srCopy').addEventListener('click', () => copyResult(false));
    $('srCopyAll').addEventListener('click', () => copyResult(!!result.code));
    $('srClose').addEventListener('click', closeResult);
    $('shareResult').addEventListener('keydown', (e) => { if (e.key === 'Escape') closeResult(); });
  }

  function applyCapabilities() {
    const btn = $('btnShares');
    if (btn) btn.classList.toggle('hidden', !state.shareAllowed);
  }

  return { init, applyCapabilities, openCreate, openList };
})();

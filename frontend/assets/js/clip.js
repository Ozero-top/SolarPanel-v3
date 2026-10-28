/* SolarPanel 剪藏落地页：表单 / 结果 两种视图，移动端优先 */
(function () {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const params = new URLSearchParams(location.search);
  const state = { me: null, groups: [], force: false, itemID: 0, previewTimer: null };

  function setMsg(text, type) {
    const el = $('f-msg');
    el.textContent = text || '';
    el.className = 'clip-msg' + (type ? ' ' + type : '');
  }

  function isHTTP(u) {
    return /^https?:\/\//i.test(String(u || '').trim());
  }

  function show(view) {
    $('view-form').classList.toggle('hidden', view !== 'form');
    $('view-result').classList.toggle('hidden', view !== 'result');
  }

  function iconNode(item) {
    const box = $('result-icon');
    box.textContent = '';
    const value = item && item.icon_value ? item.icon_value : '';
    if (value && (value.startsWith('/') || isHTTP(value))) {
      const img = document.createElement('img');
      img.alt = '';
      img.src = value;
      img.onerror = () => { box.textContent = 'S'; };
      box.appendChild(img);
    } else {
      const title = (item && item.title) || 'S';
      box.textContent = title.trim().charAt(0).toUpperCase() || 'S';
    }
  }

  function renderResult(data) {
    show('result');
    const saved = data.status !== 'duplicate' && data.status !== 'error';
    const title = data.title || '';
    const group = data.group || '';

    if (data.status === 'error') {
      const reasons = {
        'no-url': '没有从分享内容中识别到链接，请复制网页链接后重试。',
        'forbidden': '当前账号没有剪藏权限，请使用管理员或编辑账号。',
        'disabled': '站点已关闭剪藏功能，请联系管理员开启。',
        'bad-request': '分享内容格式无法识别，请重试。',
      };
      $('result-title').textContent = '保存失败';
      $('result-desc').textContent = reasons[data.reason] || data.reason || '未知错误';
      $('btn-open').classList.add('hidden');
      $('btn-undo').classList.add('hidden');
      iconNode(null);
      return;
    }

    $('result-title').textContent = saved ? '已保存' : '该链接已存在';
    $('result-desc').textContent = title
      ? (saved ? '「' + title + '」已归入「' + group + '」' : '「' + title + '」已在「' + group + '」中')
      : (saved ? '已归入「' + group + '」' : '相同链接不会重复建卡');
    iconNode(data.item || { title: title });

    const open = $('btn-open');
    if (isHTTP(data.url)) {
      open.href = data.url;
      open.classList.remove('hidden');
    } else {
      open.classList.add('hidden');
    }
    const undo = $('btn-undo');
    state.itemID = data.item_id || (data.item && data.item.id) || 0;
    undo.classList.toggle('hidden', !state.itemID || !saved);
    $('result-msg').textContent = data.warnings && data.warnings.indexOf('fetch_failed') >= 0
      ? '卡片已保存，但站点元数据抓取失败，可在后台补充标题与图标。'
      : '';
  }

  function showSavedResult(res, url) {
    renderResult({
      status: res.duplicate ? 'duplicate' : 'saved',
      item: res.item,
      item_id: res.item && res.item.id,
      title: res.item && res.item.title,
      group: res.group,
      url: (res.item && res.item.url) || url,
      warnings: res.warnings,
    });
  }

  async function undo() {
    if (!state.itemID) return;
    const btn = $('btn-undo');
    btn.disabled = true;
    try {
      await API.post('/api/items.php?action=delete', { id: state.itemID });
      $('result-msg').textContent = '已撤销，卡片已删除。';
      btn.classList.add('hidden');
    } catch (e) {
      $('result-msg').textContent = '撤销失败：' + e.message;
      btn.disabled = false;
    }
  }

  async function loadGroups() {
    const sel = $('f-group');
    sel.textContent = '';
    const auto = document.createElement('option');
    auto.value = '0';
    auto.textContent = '默认分组';
    sel.appendChild(auto);
    try {
      state.groups = await API.get('/api/v1/groups');
    } catch (_) {
      state.groups = [];
    }
    const byParent = new Map();
    (state.groups || []).forEach((g) => {
      const p = g.parent_id || 0;
      if (!byParent.has(p)) byParent.set(p, []);
      byParent.get(p).push(g);
    });
    (byParent.get(0) || []).forEach((top) => {
      const opt = document.createElement('option');
      opt.value = String(top.id);
      opt.textContent = top.title;
      sel.appendChild(opt);
      (byParent.get(top.id) || []).forEach((child) => {
        const c = document.createElement('option');
        c.value = String(child.id);
        c.textContent = '　└ ' + child.title;
        sel.appendChild(c);
      });
    });
  }

  function showDuplicate(id) {
    const box = $('f-dup');
    box.classList.toggle('hidden', !id);
    state.force = false;
    if (id) {
      $('dup-open').href = '/admin.html';
    }
  }

  async function preview() {
    const url = $('f-url').value.trim();
    if (!isHTTP(url)) return;
    try {
      const data = await API.post('/api/v1/preview', { url: url });
      if (!$('f-title').value.trim() && data.title) {
        $('f-title').value = data.title;
      }
      showDuplicate(data.duplicate ? (data.existing_id || -1) : 0);
    } catch (_) {
      /* 预览失败不影响保存 */
    }
  }

  async function save(e) {
    e.preventDefault();
    const url = $('f-url').value.trim();
    if (!isHTTP(url)) {
      setMsg('请填写以 http:// 或 https:// 开头的链接', 'error');
      return;
    }
    const btn = $('btn-save');
    btn.disabled = true;
    setMsg('正在保存，服务端抓取可能需要几秒…');
    try {
      const res = await API.post('/api/v1/clips', {
        url: url,
        title: $('f-title').value.trim(),
        note: $('f-note').value.trim(),
        group_id: parseInt($('f-group').value || '0', 10),
        force: state.force,
      });
      if (res.duplicate && !state.force) {
        showDuplicate(res.item && res.item.id);
        setMsg('该链接已存在，可直接打开已有卡片，或选择「仍要另存」。', 'ok');
        return;
      }
      showSavedResult(res, url);
    } catch (err) {
      const text = err.message || '保存失败';
      if (text.indexOf('已存在') >= 0) showDuplicate(-1);
      setMsg(text, 'error');
    } finally {
      btn.disabled = false;
    }
  }

  async function init() {
    try {
      state.me = await API.get('/api/v1/me');
    } catch (e) {
      if (String(e.message || '').indexOf('登录') >= 0) {
        location.href = '/login.html?redirect=' + encodeURIComponent(location.pathname + location.search);
        return;
      }
      show('result');
      renderResult({ status: 'error', reason: e.message || '无法连接服务器' });
      return;
    }

    if (!state.me.clip_enabled) {
      show('result');
      renderResult({ status: 'error', reason: 'disabled' });
      return;
    }

    if (params.get('status')) {
      renderResult({
        status: params.get('status'),
        reason: params.get('reason') || '',
        item_id: parseInt(params.get('item') || '0', 10),
        title: params.get('title') || '',
        group: params.get('group') || '',
        url: params.get('url') || '',
      });
      return;
    }

    show('form');
    await loadGroups();
    const url = params.get('url') || '';
    const title = params.get('title') || '';
    if (url) $('f-url').value = url;
    if (title) $('f-title').value = title;
    if (url) preview();

    $('clip-form').addEventListener('submit', save);
    $('btn-undo').addEventListener('click', undo);
    $('btn-force').addEventListener('click', () => {
      state.force = true;
      $('f-dup').classList.add('hidden');
      setMsg('将强制另存一张新卡片。');
    });
    $('f-url').addEventListener('input', () => {
      showDuplicate(0);
      clearTimeout(state.previewTimer);
      state.previewTimer = setTimeout(preview, 700);
    });
  }

  init();
})();

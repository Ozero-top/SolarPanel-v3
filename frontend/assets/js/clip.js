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

  /* 只放行 http / https 绝对地址，阻断 javascript:、data: 等伪协议
     与畸形地址，避免把分享内容直接写入 href 造成脚本注入或跳转劫持 */
  function safeHTTPURL(u) {
    const raw = String(u || '').trim();
    if (!/^https?:\/\//i.test(raw)) return '';
    try {
      const parsed = new URL(raw);
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return '';
      if (!parsed.hostname) return '';
      return parsed.href;
    } catch (_) {
      return '';
    }
  }

  function show(view) {
    $('view-form').classList.toggle('hidden', view !== 'form');
    $('view-result').classList.toggle('hidden', view !== 'result');
  }

  /* ================= 离线队列（IndexedDB） =================
     保存失败（断网/服务不可达）时把请求暂存本机，联网或下次打开自动重发。 */
  const QUEUE_DB = 'sp-clip-queue';
  const QUEUE_STORE = 'pending';
  const QUEUE_MAX = 50;
  let flushing = false;

  function openQueueDB() {
    return new Promise((resolve, reject) => {
      if (!('indexedDB' in window)) {
        reject(new Error('IndexedDB 不可用'));
        return;
      }
      const req = indexedDB.open(QUEUE_DB, 1);
      req.onupgradeneeded = () => {
        const idb = req.result;
        if (!idb.objectStoreNames.contains(QUEUE_STORE)) {
          idb.createObjectStore(QUEUE_STORE, { keyPath: 'id', autoIncrement: true });
        }
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error || new Error('IndexedDB 打开失败'));
    });
  }

  async function queueAll() {
    const idb = await openQueueDB();
    return new Promise((resolve, reject) => {
      const tx = idb.transaction(QUEUE_STORE, 'readonly');
      const req = tx.objectStore(QUEUE_STORE).getAll();
      req.onsuccess = () => resolve(req.result || []);
      req.onerror = () => reject(req.error);
    });
  }

  async function queuePut(record) {
    const idb = await openQueueDB();
    return new Promise((resolve, reject) => {
      const tx = idb.transaction(QUEUE_STORE, 'readwrite');
      tx.objectStore(QUEUE_STORE).add(record);
      tx.oncomplete = () => resolve();
      tx.onerror = () => reject(tx.error);
    });
  }

  async function queueDrop(id) {
    const idb = await openQueueDB();
    return new Promise((resolve) => {
      const tx = idb.transaction(QUEUE_STORE, 'readwrite');
      tx.objectStore(QUEUE_STORE).delete(id);
      tx.oncomplete = () => resolve();
      tx.onerror = () => resolve();
    });
  }

  async function updateQueueHint() {
    const el = $('queue-hint');
    if (!el) return;
    try {
      const list = await queueAll();
      el.classList.toggle('hidden', list.length === 0);
      el.textContent = list.length
        ? '本机还有 ' + list.length + ' 条离线剪藏待发送，联网后会自动重试。'
        : '';
    } catch (_) {
      el.classList.add('hidden');
    }
  }

  // 网络类失败才入队；权限/参数类失败直接提示，避免队列被脏数据堵死
  function isNetworkError(err) {
    if (!navigator.onLine) return true;
    const msg = String((err && err.message) || '');
    return msg.indexOf('网络请求失败') >= 0 || msg.indexOf('无法连接') >= 0 || msg.indexOf('Failed to fetch') >= 0;
  }

  async function enqueueClip(payload) {
    try {
      await queuePut({ payload: payload, created_at: Date.now() });
      // 超出上限时丢弃最早的一条
      const list = await queueAll();
      if (list.length > QUEUE_MAX) {
        await queueDrop(list[0].id);
      }
      await updateQueueHint();
      return true;
    } catch (_) {
      return false;
    }
  }

  async function flushQueue() {
    if (flushing || !navigator.onLine) return 0;
    flushing = true;
    let done = 0;
    try {
      const list = await queueAll();
      for (const rec of list) {
        try {
          await API.post('/api/v1/clips', rec.payload);
          await queueDrop(rec.id);
          done++;
        } catch (e) {
          const msg = String((e && e.message) || '');
          const permanent = /登录|权限|参数|已关闭|不能为空|开头|无法识别/.test(msg);
          if (permanent) {
            await queueDrop(rec.id);
            continue;
          }
          break; // 仍是网络问题，保留剩余记录
        }
      }
    } catch (_) {
      /* IndexedDB 不可用时静默 */
    } finally {
      flushing = false;
      await updateQueueHint();
    }
    if (done > 0) setMsg('已自动补发 ' + done + ' 条离线剪藏。', 'ok');
    return done;
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
    const openURL = safeHTTPURL(data.url);
    if (openURL) {
      open.href = openURL;
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
    const payload = {
      url: url,
      title: $('f-title').value.trim(),
      note: $('f-note').value.trim(),
      group_id: parseInt($('f-group').value || '0', 10),
      force: state.force,
    };
    try {
      const res = await API.post('/api/v1/clips', payload);
      if (res.duplicate && !state.force) {
        showDuplicate(res.item && res.item.id);
        setMsg('该链接已存在，可直接打开已有卡片，或选择「仍要另存」。', 'ok');
        return;
      }
      showSavedResult(res, url);
    } catch (err) {
      // 断网 / 服务不可达：写入本机队列，联网后自动重发
      if (isNetworkError(err)) {
        const queued = await enqueueClip(payload);
        if (queued) {
          setMsg('无法连接服务器，已暂存到本机，联网后会自动重发。', 'ok');
          return;
        }
      }
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

    // 已登录才显示后台入口，避免公开页暴露后台地址
    const backAdmin = document.getElementById('backAdmin');
    if (backAdmin) {
      backAdmin.href = '/admin.html';
      backAdmin.hidden = false;
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

    // 离线队列：进页面先补发，网络恢复时再补发一次
    updateQueueHint();
    flushQueue();
    window.addEventListener('online', () => {
      setMsg('网络已恢复，正在补发离线剪藏…');
      flushQueue();
    });
  }

  init();
})();

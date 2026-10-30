/* SolarPanel 剪藏弹窗 */
'use strict';

const $ = (id) => document.getElementById(id);
const state = {
  cfg: null,
  tab: null,
  url: '',
  clientId: crypto.randomUUID(),
  force: false,
  item: null,
  dupItem: null,
  savedText: '',
};

document.addEventListener('DOMContentLoaded', init);

async function init() {
  $('ver').textContent = 'v' + chrome.runtime.getManifest().version;
  $('setupBtn').addEventListener('click', () => chrome.runtime.openOptionsPage());
  $('openOptions').addEventListener('click', (e) => {
    e.preventDefault();
    chrome.runtime.openOptionsPage();
  });

  const data = await chrome.storage.local.get('spClipConfig');
  const cfg = data.spClipConfig || {};
  // 打开主页：跳到设置里填写的服务器地址；还没配置时改为打开设置页
  $('openHome').addEventListener('click', (e) => {
    e.preventDefault();
    if (!cfg.serverUrl) {
      chrome.runtime.openOptionsPage();
      return;
    }
    chrome.tabs.create({ url: String(cfg.serverUrl).replace(/\/+$/, '') + '/' });
  });
  if (cfg.serverUrl) {
    const base = String(cfg.serverUrl).replace(/\/+$/, '');
    $('openHome').href = base + '/';
    $('openClips').href = base + '/clips';
  }
  $('openClips').addEventListener('click', (e) => {
    if (!cfg.serverUrl) {
      e.preventDefault();
      chrome.runtime.openOptionsPage();
    }
  });
  if (!cfg.serverUrl || !cfg.token) {
    $('setupView').classList.remove('hidden');
    return;
  }
  state.cfg = {
    serverUrl: String(cfg.serverUrl).replace(/\/+$/, ''),
    token: String(cfg.token),
    defaultGroup: parseInt(cfg.defaultGroup || '0', 10) || 0,
  };
  $('mainView').classList.remove('hidden');

  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  state.tab = (tabs && tabs[0]) || {};
  state.url = state.tab.url || '';
  $('fUrl').textContent = state.url;
  $('fUrl').title = state.url;
  $('fTitle').value = state.tab.title || '';
  $('fSnapshot').checked = cfg.snapshot === undefined ? true : !!cfg.snapshot;
  $('fShotMode').value = cfg.shotMode === 'viewport' || cfg.shotMode === 'none' ? cfg.shotMode : 'full';
  $('fSnapshot').addEventListener('change', async () => {
    const data = await chrome.storage.local.get('spClipConfig');
    const next = data.spClipConfig || {};
    next.snapshot = $('fSnapshot').checked;
    await chrome.storage.local.set({ spClipConfig: next });
  });
  $('fShotMode').addEventListener('change', async () => {
    const data = await chrome.storage.local.get('spClipConfig');
    const next = data.spClipConfig || {};
    next.shotMode = $('fShotMode').value;
    await chrome.storage.local.set({ spClipConfig: next });
  });

  if (!/^https?:\/\//i.test(state.url)) {
    setStatus('当前页面不支持保存（仅支持 http / https 页面）', 'err');
    $('saveBtn').disabled = true;
  }

  $('saveBtn').addEventListener('click', () => save(false));
  $('retryShotBtn').addEventListener('click', () => {
    if (!state.savedText) return;
    $('retryShotBtn').classList.add('hidden');
    runShot(state.savedText);
  });
  $('forceBtn').addEventListener('click', () => {
    state.force = true;
    $('dupBox').classList.add('hidden');
    save(true);
  });
  $('updateShotBtn').addEventListener('click', async () => {
    const it = state.dupItem;
    if (!it || !it.id) return;
    state.item = it;
    state.savedText = '已更新「' + (it.title || '已有卡片') + '」';
    $('dupBox').classList.add('hidden');
    $('saveBtn').classList.add('hidden');
    showDone(it);
    refreshSavedState(it.id).catch(() => {});
    setStatus(state.savedText + '，正在重新截图…', 'ok');
    await runShot(state.savedText);
  });
  $('undoBtn').addEventListener('click', undo);
  $('starBtn').addEventListener('click', toggleStar);
  $('readBtn').addEventListener('click', toggleRead);
  $('refetchBtn').addEventListener('click', refetchBody);
  $('delSnapBtn').addEventListener('click', deleteSnapshot);

  let libTimer = null;
  $('libQ').addEventListener('input', () => {
    clearTimeout(libTimer);
    libTimer = setTimeout(() => loadLibrary($('libQ').value.trim()), 350);
  });

  loadGroups();
  checkConnection();
  loadLibrary('');
  chrome.runtime.sendMessage({ type: 'flush' }).catch(() => {});
}

function setStatus(text, cls) {
  const el = $('statusBar');
  el.textContent = text || '';
  el.className = 'status' + (cls ? ' ' + cls : '');
}

async function request(path, method, body) {
  const res = await fetch(state.cfg.serverUrl + path, {
    method: method || 'GET',
    headers: {
      Authorization: 'Bearer ' + state.cfg.token,
      ...(body ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  let data = {};
  try { data = await res.json(); } catch (_) { /* 忽略非 JSON */ }
  if (!res.ok) {
    const err = new Error(data.msg || ('HTTP ' + res.status));
    err.status = res.status;
    throw err;
  }
  // 服务端统一响应信封 {code,data,msg}
  if (data && typeof data === 'object' && 'code' in data) {
    if (data.code !== 0) {
      const err = new Error(data.msg || '请求失败');
      err.code = data.code;
      throw err;
    }
    return data.data;
  }
  return data;
}

async function checkConnection() {
  try {
    const me = await request('/api/v1/me');
    if (!me.clip_enabled) {
      setStatus('站点已关闭剪藏功能，请联系管理员', 'err');
      $('saveBtn').disabled = true;
    } else if (me.name || me.username) {
      setStatus('已连接：' + (me.name || me.username));
    }
  } catch (e) {
    if (e.status === 401) {
      setStatus('Token 无效或已过期，请在设置中更新', 'err');
      $('saveBtn').disabled = true;
    } else {
      setStatus('无法连接服务器：' + e.message, 'err');
    }
  }
}

async function loadGroups() {
  const sel = $('fGroup');
  sel.innerHTML = '';
  const auto = document.createElement('option');
  auto.value = '0';
  auto.textContent = '默认分组';
  sel.appendChild(auto);
  try {
    const groups = await request('/api/v1/groups');
    const tops = groups.filter((g) => !g.parent_id);
    tops.forEach((top) => {
      const opt = document.createElement('option');
      opt.value = String(top.id);
      opt.textContent = top.title;
      sel.appendChild(opt);
      groups.filter((c) => c.parent_id === top.id).forEach((child) => {
        const copt = document.createElement('option');
        copt.value = String(child.id);
        copt.textContent = '　└ ' + child.title;
        sel.appendChild(copt);
      });
    });
    sel.value = String(state.cfg.defaultGroup || 0);
  } catch (_) {
    /* 分组读取失败时保留默认分组 */
  }
}

async function save(force) {
  const btn = $('saveBtn');
  btn.disabled = true;
  setStatus('正在保存…');
  try {
    const res = await chrome.runtime.sendMessage({
      type: 'save',
      payload: {
        url: state.url,
        title: $('fTitle').value.trim(),
        note: $('fNote').value.trim(),
        group_id: parseInt($('fGroup').value || '0', 10),
        capture: $('fSnapshot').checked ? 'snapshot' : '',
        tags: parseTags($('fTags').value),
        force: force,
        clientId: state.clientId,
      },
    });
    if (!res || res.error) {
      setStatus((res && res.error) || '保存失败', 'err');
      if (res && res.status === 401) setStatus('Token 无效，请在设置中更新', 'err');
      return;
    }
    if (res.queued) {
      setStatus('网络不可用，已加入待发送队列，下次打开浏览器会自动重试', 'ok');
      return;
    }
    if (res.duplicate && !force) {
      state.dupItem = res.item;
      $('updateShotBtn').classList.toggle('hidden', $('fShotMode').value === 'none');
      $('dupBox').classList.remove('hidden');
      setStatus('该链接已存在，可更新已有卡片截图或仍要另存');
      return;
    }
    state.item = res.item;
    const savedText = '已保存到「' + (res.group || '默认分组') + '」' + (res.snapshot ? '（含正文快照）' : '');
    setStatus(savedText, 'ok');
    showDone(res.item);
    $('saveBtn').classList.add('hidden');
    $('dupBox').classList.add('hidden');
    refreshSavedState(res.item && res.item.id).catch(() => {});
    loadLibrary($('libQ').value.trim());

    state.savedText = savedText;
    // 截图（整页 / 可视区）：失败不影响保存结果，可点「重试截图」
    const shotMode = $('fShotMode').value;
    if (shotMode !== 'none' && state.item && state.item.id) {
      setStatus(savedText + (shotMode === 'viewport' ? '，正在截取可视区…' : '，正在生成整页截图…'), 'ok');
      await runShot(savedText);
    }
  } catch (e) {
    setStatus('保存失败：' + e.message, 'err');
    btn.disabled = false;
  }
}

/** 截图并回填状态；失败时显示「重试截图」按钮 */
async function runShot(savedText) {
  const mode = $('fShotMode').value;
  try {
    const shot = await chrome.runtime.sendMessage({
      type: 'screenshot',
      itemId: state.item && state.item.id,
      tabId: state.tab && state.tab.id,
      windowId: state.tab && state.tab.windowId,
      mode: mode,
    });
    if (shot && shot.ok) {
      setStatus(savedText + (shot.mode === 'viewport' ? '（截图仅可视区）' : '（含整页截图）'), 'ok');
      $('retryShotBtn').classList.add('hidden');
    } else {
      setStatus(savedText + '，截取失败：' + ((shot && shot.error) || '未知原因'), 'ok');
      $('retryShotBtn').classList.remove('hidden');
    }
  } catch (e) {
    setStatus(savedText + '，截取失败：' + e.message, 'ok');
    $('retryShotBtn').classList.remove('hidden');
  }
}

async function undo() {
  if (!state.item) return;
  const btn = $('undoBtn');
  btn.disabled = true;
  try {
    await request('/api/v1/clips', 'DELETE', { id: state.item.id });
    setStatus('已撤销，卡片已删除', 'ok');
    $('doneBox').classList.add('hidden');
    $('retryShotBtn').classList.add('hidden');
    $('saveBtn').classList.remove('hidden');
    $('saveBtn').disabled = false;
    state.item = null;
    loadLibrary($('libQ').value.trim());
  } catch (e) {
    setStatus('撤销失败：' + e.message, 'err');
    btn.disabled = false;
  }
}

/* ---------- 保存后：阅读页入口 / 收藏 / 已读 / 正文 / 快照 ---------- */

function parseTags(s) {
  return String(s || '')
    .split(/[,，;；]/)
    .map((t) => t.trim().replace(/^#/, ''))
    .filter(Boolean)
    .slice(0, 20);
}

function showDone(it) {
  $('doneBox').classList.remove('hidden');
  $('openPage').href = state.url;
  const id = it && it.id;
  $('openRead').classList.toggle('hidden', !id);
  if (id) $('openRead').href = state.cfg.serverUrl + '/read/' + id;
  setStar(it && it.starred);
  setRead(it && it.read_at);
  $('delSnapBtn').disabled = !(it && it.has_snapshot);
}

function setStar(on) {
  $('starBtn').textContent = on ? '★ 已收藏' : '☆ 收藏';
  $('starBtn').classList.toggle('on', !!on);
}

function setRead(on) {
  $('readBtn').textContent = on ? '标记未读' : '标记已读';
  $('readBtn').classList.toggle('on', !!on);
}

/** 拉取卡片最新状态（收藏 / 已读 / 是否有快照） */
async function refreshSavedState(id) {
  if (!id) return;
  const d = await request('/api/v1/clips/' + id);
  if (!d || !d.item) return;
  state.item = Object.assign({}, state.item, d.item, {
    starred: !!d.starred,
    read_at: d.read_at || '',
    has_snapshot: !!d.has_snapshot,
  });
  setStar(state.item.starred);
  setRead(state.item.read_at);
  $('delSnapBtn').disabled = !state.item.has_snapshot;
}

async function toggleStar() {
  if (!state.item || !state.item.id) return;
  const next = !state.item.starred;
  $('starBtn').disabled = true;
  try {
    const r = await request('/api/v1/clips/' + state.item.id, 'PATCH', { starred: next });
    state.item.starred = !!(r && r.starred);
    setStar(state.item.starred);
    setStatus(state.item.starred ? '已收藏' : '已取消收藏', 'ok');
    loadLibrary($('libQ').value.trim());
  } catch (e) {
    setStatus('收藏失败：' + e.message, 'err');
  } finally {
    $('starBtn').disabled = false;
  }
}

async function toggleRead() {
  if (!state.item || !state.item.id) return;
  const next = !state.item.read_at;
  $('readBtn').disabled = true;
  try {
    const r = await request('/api/v1/clips/' + state.item.id, 'PATCH', { read: next });
    state.item.read_at = (r && r.read_at) || '';
    setRead(state.item.read_at);
    setStatus(state.item.read_at ? '已标记为已读' : '已标记为未读', 'ok');
    loadLibrary($('libQ').value.trim());
  } catch (e) {
    setStatus('更新已读状态失败：' + e.message, 'err');
  } finally {
    $('readBtn').disabled = false;
  }
}

async function refetchBody() {
  if (!state.item || !state.item.id) return;
  $('refetchBtn').disabled = true;
  setStatus('正在重新抓取正文…');
  try {
    const r = await request('/api/v1/clips/' + state.item.id + '/refetch', 'POST');
    if (r && r.changed) {
      setStatus('正文已更新（' + (r.word_count || 0) + ' 字）', 'ok');
    } else if (r && r.message) {
      setStatus('正文未变化：' + r.message, 'ok');
    } else {
      setStatus('正文已是最新', 'ok');
    }
    state.item.has_snapshot = true;
    $('delSnapBtn').disabled = false;
    loadLibrary($('libQ').value.trim());
  } catch (e) {
    setStatus('抓取失败：' + e.message, 'err');
  } finally {
    $('refetchBtn').disabled = false;
  }
}

async function deleteSnapshot() {
  if (!state.item || !state.item.id) return;
  if (!state.item.has_snapshot) {
    setStatus('该卡片没有正文快照', 'err');
    return;
  }
  $('delSnapBtn').disabled = true;
  try {
    await request('/api/v1/clips/' + state.item.id + '/content', 'DELETE');
    state.item.has_snapshot = false;
    setStatus('正文快照已删除（卡片保留）', 'ok');
    loadLibrary($('libQ').value.trim());
  } catch (e) {
    setStatus('删除失败：' + e.message, 'err');
    $('delSnapBtn').disabled = false;
  }
}

/* ---------- 最近剪藏 / 搜索 ---------- */

async function loadLibrary(q) {
  const base = state.cfg && state.cfg.serverUrl;
  if (!base) return;
  $('libAll').href = base + '/clips' + (q ? '?q=' + encodeURIComponent(q) : '');
  try {
    const params = new URLSearchParams({ sort: 'newest', limit: '8' });
    if (q) params.set('q', q);
    const d = await request('/api/v1/clips?' + params.toString());
    renderLibrary((d && d.items) || [], q);
  } catch (e) {
    const list = $('libList');
    list.textContent = '';
    list.appendChild(libEmpty('加载失败：' + e.message));
  }
}

function libEmpty(text) {
  const li = document.createElement('li');
  li.className = 'lib-empty';
  li.textContent = text;
  return li;
}

function renderLibrary(items, q) {
  const list = $('libList');
  list.textContent = '';
  if (!items.length) {
    list.appendChild(libEmpty(q ? '没有匹配「' + q + '」的剪藏' : '还没有剪藏，先保存一条试试'));
    return;
  }
  items.forEach((it) => {
    const li = document.createElement('li');
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'lib-item';
    const title = document.createElement('span');
    title.className = 'lib-title';
    title.textContent = (it.starred ? '★ ' : '') + (it.title || it.url || '未命名剪藏');
    const meta = document.createElement('span');
    meta.className = 'lib-meta';
    meta.textContent = [it.group || '未分组', it.read_at ? '已读' : '未读', it.screenshot_path ? '有截图' : '']
      .filter(Boolean).join(' · ');
    btn.appendChild(title);
    btn.appendChild(meta);
    btn.addEventListener('click', () => {
      chrome.tabs.create({ url: state.cfg.serverUrl + '/read/' + it.id });
    });
    li.appendChild(btn);
    list.appendChild(li);
  });
}

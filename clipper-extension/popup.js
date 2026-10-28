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
  $('fSnapshot').checked = !!cfg.snapshot;
  $('fShot').checked = !!cfg.shot;
  $('fSnapshot').addEventListener('change', async () => {
    const data = await chrome.storage.local.get('spClipConfig');
    const next = data.spClipConfig || {};
    next.snapshot = $('fSnapshot').checked;
    await chrome.storage.local.set({ spClipConfig: next });
  });
  $('fShot').addEventListener('change', async () => {
    const data = await chrome.storage.local.get('spClipConfig');
    const next = data.spClipConfig || {};
    next.shot = $('fShot').checked;
    await chrome.storage.local.set({ spClipConfig: next });
  });

  if (!/^https?:\/\//i.test(state.url)) {
    setStatus('当前页面不支持保存（仅支持 http / https 页面）', 'err');
    $('saveBtn').disabled = true;
  }

  $('saveBtn').addEventListener('click', () => save(false));
  $('forceBtn').addEventListener('click', () => {
    state.force = true;
    $('dupBox').classList.add('hidden');
    save(true);
  });
  $('undoBtn').addEventListener('click', undo);

  loadGroups();
  checkConnection();
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
      $('dupBox').classList.remove('hidden');
      setStatus('该链接已存在，可打开已有卡片或仍要另存');
      return;
    }
    state.item = res.item;
    const savedText = '已保存到「' + (res.group || '默认分组') + '」' + (res.snapshot ? '（含正文快照）' : '');
    setStatus(savedText, 'ok');
    $('doneBox').classList.remove('hidden');
    $('openPage').href = state.url;
    $('saveBtn').classList.add('hidden');
    $('dupBox').classList.add('hidden');

    // 整页截图：失败不影响保存结果，仅提示原因
    if ($('fShot').checked && state.item && state.item.id) {
      setStatus(savedText + '，正在生成整页截图…', 'ok');
      try {
        const shot = await chrome.runtime.sendMessage({
          type: 'screenshot',
          itemId: state.item.id,
          tabId: state.tab && state.tab.id,
          windowId: state.tab && state.tab.windowId,
        });
        if (shot && shot.ok) {
          setStatus(savedText + (shot.mode === 'viewport' ? '（截图仅可视区）' : '（含整页截图）'), 'ok');
        } else {
          setStatus(savedText + '，截取失败：' + ((shot && shot.error) || '未知原因'), 'ok');
        }
      } catch (e) {
        setStatus(savedText + '，截取失败：' + e.message, 'ok');
      }
    }
  } catch (e) {
    setStatus('保存失败：' + e.message, 'err');
    btn.disabled = false;
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
    $('saveBtn').classList.remove('hidden');
    $('saveBtn').disabled = false;
    state.item = null;
  } catch (e) {
    setStatus('撤销失败：' + e.message, 'err');
    btn.disabled = false;
  }
}

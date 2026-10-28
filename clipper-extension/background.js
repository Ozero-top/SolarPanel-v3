/* SolarPanel 剪藏扩展：MV3 service worker（按需唤醒） */
'use strict';

const STORAGE_KEY = 'spClipConfig';
const QUEUE_KEY = 'spClipQueue';
const MAX_QUEUE = 50;
const MENU_PAGE = 'sp-save-page';
const MENU_LINK = 'sp-save-link';
const MENU_SELECTION = 'sp-save-selection';

function normalizeBase(url) {
  return String(url || '').trim().replace(/\/+$/, '');
}

async function getConfig() {
  const data = await chrome.storage.local.get(STORAGE_KEY);
  const cfg = data[STORAGE_KEY] || {};
  return {
    serverUrl: normalizeBase(cfg.serverUrl),
    token: String(cfg.token || '').trim(),
    defaultGroup: parseInt(cfg.defaultGroup || '0', 10) || 0,
    defaultGroupName: cfg.defaultGroupName || '稍后读',
  };
}

async function api(cfg, path, method, body) {
  const res = await fetch(cfg.serverUrl + path, {
    method: method || 'GET',
    headers: {
      Authorization: 'Bearer ' + cfg.token,
      ...(body ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  let data = {};
  try { data = await res.json(); } catch (_) { /* 非 JSON 响应 */ }
  if (!res.ok) {
    const err = new Error(data.msg || ('HTTP ' + res.status));
    err.status = res.status;
    err.code = data.code;
    throw err;
  }
  // 服务端统一响应信封 {code,data,msg}：成功时取出 data，失败时抛错
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

function badge(text, color) {
  chrome.action.setBadgeBackgroundColor({ color: color || '#2563eb' });
  chrome.action.setBadgeText({ text: text || '' });
  if (text) {
    setTimeout(() => chrome.action.setBadgeText({ text: '' }), 3000);
  }
}

async function pageTitle(tabId) {
  if (!tabId) return '';
  try {
    const [r] = await chrome.scripting.executeScript({
      target: { tabId },
      func: () => document.title,
    });
    return (r && r.result) || '';
  } catch (_) {
    return '';
  }
}

async function enqueue(payload) {
  const data = await chrome.storage.local.get(QUEUE_KEY);
  const queue = data[QUEUE_KEY] || [];
  queue.push({ ...payload, clientId: payload.clientId || crypto.randomUUID(), queuedAt: Date.now() });
  while (queue.length > MAX_QUEUE) queue.shift();
  await chrome.storage.local.set({ [QUEUE_KEY]: queue });
}

async function flushQueue() {
  const data = await chrome.storage.local.get(QUEUE_KEY);
  const queue = data[QUEUE_KEY] || [];
  if (!queue.length) return;
  const cfg = await getConfig();
  if (!cfg.serverUrl || !cfg.token) return;
  const remain = [];
  for (const item of queue) {
    try {
      await api(cfg, '/api/v1/clips', 'POST', item);
    } catch (e) {
      if (e.status === 401 || e.status === 403 || e.status === 429 || e.status === 400) {
        continue; // 永久性错误丢弃，避免死循环
      }
      remain.push(item);
    }
  }
  await chrome.storage.local.set({ [QUEUE_KEY]: remain });
}

/** 保存一条剪藏：网络失败进入离线队列，其余错误直接抛出 */
async function saveClip(payload) {
  const cfg = await getConfig();
  if (!cfg.serverUrl || !cfg.token) {
    throw new Error('请先在扩展选项中配置服务器地址与 Token');
  }
  const body = {
    url: payload.url,
    title: payload.title || '',
    note: payload.note || '',
    group_id: payload.group_id || cfg.defaultGroup || 0,
    capture: payload.capture || '',
    force: !!payload.force,
    client_id: payload.clientId || crypto.randomUUID(),
  };
  try {
    return await api(cfg, '/api/v1/clips', 'POST', body);
  } catch (e) {
    if (e.status === undefined) {
      await enqueue(body);
      return { queued: true };
    }
    throw e;
  }
}

async function quickSave(tab, extra) {
  const url = (extra && extra.url) || (tab && tab.url) || '';
  if (!/^https?:\/\//i.test(url)) {
    badge('!', '#b91c1c');
    return;
  }
  let title = (extra && extra.title) || (tab && tab.title) || '';
  if (!title && tab && tab.id) title = await pageTitle(tab.id);
  try {
    const res = await saveClip({ url, title, note: (extra && extra.note) || '', source: 'quick' });
    badge(res && res.duplicate ? '=' : '✓', res && res.duplicate ? '#b45309' : '#15803d');
  } catch (e) {
    badge('!', '#b91c1c');
  }
}

chrome.runtime.onInstalled.addListener(async () => {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({ id: MENU_PAGE, title: '保存到 SolarPanel', contexts: ['page'] });
    chrome.contextMenus.create({ id: MENU_LINK, title: '保存此链接到 SolarPanel', contexts: ['link'] });
    chrome.contextMenus.create({ id: MENU_SELECTION, title: '保存选中文本到 SolarPanel', contexts: ['selection'] });
  });
  flushQueue();
});

chrome.runtime.onStartup.addListener(() => {
  flushQueue();
});

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  if (info.menuItemId === MENU_LINK) {
    await quickSave(tab, { url: info.linkUrl, title: '' });
  } else if (info.menuItemId === MENU_SELECTION) {
    await quickSave(tab, { url: info.pageUrl || (tab && tab.url), note: info.selectionText || '' });
  } else if (info.menuItemId === MENU_PAGE) {
    await quickSave(tab, { url: info.pageUrl || (tab && tab.url) });
  }
});

chrome.commands.onCommand.addListener(async (command) => {
  if (command !== 'save-page') return;
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  const tab = tabs && tabs[0];
  if (!tab) return;
  await quickSave(tab, { url: tab.url });
});

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (!msg || !msg.type) return;
  if (msg.type === 'save') {
    saveClip(msg.payload || {}).then(sendResponse).catch((e) => sendResponse({ error: e.message, status: e.status }));
    return true;
  }
  if (msg.type === 'flush') {
    flushQueue().then(() => sendResponse({ ok: true }));
    return true;
  }
  if (msg.type === 'test') {
    getConfig().then((cfg) => {
      if (!cfg.serverUrl || !cfg.token) {
        sendResponse({ error: '请先填写服务器地址与 Token' });
        return;
      }
      api(cfg, '/api/v1/me', 'GET').then((me) => sendResponse({ me })).catch((e) => sendResponse({ error: e.message, status: e.status }));
    });
    return true;
  }
});

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
      'X-Clip-Source': 'extension',
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

/** 取当前标签页地址：快捷键触发时 tab.url 可能为空，回退到页面内 location.href */
async function tabURL(tab) {
  const direct = (tab && tab.url) || '';
  if (/^https?:\/\//i.test(direct)) return direct;
  if (!tab || !tab.id) return '';
  try {
    const [r] = await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      func: () => location.href,
    });
    return (r && r.result) || '';
  } catch (_) {
    return '';
  }
}

/** 快捷键 / 右键保存没有弹窗，在页面右下角给一条轻提示作为可见反馈 */
async function toast(tabId, text, ok) {
  if (!tabId) return;
  try {
    await chrome.scripting.executeScript({
      target: { tabId },
      func: (msg, good) => {
        const old = document.getElementById('__sp_clip_toast__');
        if (old) old.remove();
        const box = document.createElement('div');
        box.id = '__sp_clip_toast__';
        box.textContent = msg;
        box.style.cssText =
          'position:fixed;z-index:2147483647;right:18px;bottom:18px;padding:10px 16px;' +
          'border-radius:10px;color:#fff;box-shadow:0 8px 24px rgba(0,0,0,.28);' +
          'font:13px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;' +
          'background:' + (good ? '#15803d' : '#b91c1c') + ';';
        document.documentElement.appendChild(box);
        setTimeout(() => box.remove(), 2800);
      },
      args: [text, !!ok],
    });
  } catch (_) {
    /* 页面禁止注入脚本时忽略，仍保留角标提示 */
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
  const tabId = (tab && tab.id) || 0;
  const url = (extra && extra.url) || (await tabURL(tab));
  if (!/^https?:\/\//i.test(url)) {
    badge('!', '#b91c1c');
    await toast(tabId, '当前页面不支持保存（仅支持 http / https）', false);
    return;
  }
  let title = (extra && extra.title) || (tab && tab.title) || '';
  if (!title && tabId) title = await pageTitle(tabId);
  try {
    const res = await saveClip({ url, title, note: (extra && extra.note) || '', source: 'quick' });
    if (res && res.queued) {
      badge('…', '#b45309');
      await toast(tabId, '网络不可用，已存入离线队列，恢复后自动补发', true);
      return;
    }
    const dup = !!(res && res.duplicate);
    badge(dup ? '=' : '✓', dup ? '#b45309' : '#15803d');
    await toast(tabId, dup ? '该链接已存在，可在导航页查看' : '已保存到 SolarPanel', true);
  } catch (e) {
    badge('!', '#b91c1c');
    await toast(tabId, '保存失败：' + ((e && e.message) || '未知错误'), false);
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

/* ---------- 整页截图（P2）：滚动逐屏截图后在 OffscreenCanvas 拼接 ---------- */

const SHOT_MAX_CSS_HEIGHT = 12000; // 超长页面截断，避免 canvas 与上传体积失控
const SHOT_STEP_DELAY = 550;       // captureVisibleTab 配额 2 次/秒，间隔需大于 500ms
const SHOT_MAX_CANVAS = 16000;     // Chrome canvas 单边上限保护

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function dataUrlToBlob(dataUrl) {
  const comma = dataUrl.indexOf(',');
  const mimeMatch = /data:([^;]+)/.exec(dataUrl.slice(0, comma));
  const binary = atob(dataUrl.slice(comma + 1));
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return new Blob([bytes], { type: mimeMatch ? mimeMatch[1] : 'image/png' });
}

async function shotMetrics(tabId) {
  const [r] = await chrome.scripting.executeScript({
    target: { tabId },
    func: () => {
      const doc = document.documentElement;
      const body = document.body;
      let bg = '';
      try {
        bg = getComputedStyle(doc).backgroundColor || '';
        if (!bg || bg === 'rgba(0, 0, 0, 0)' || bg === 'transparent') {
          bg = body ? getComputedStyle(body).backgroundColor || '' : '';
        }
      } catch (_) { bg = ''; }
      return {
        width: Math.max(1, window.innerWidth),
        // clientWidth 不含竖向滚动条，拼接时按它裁掉右侧滚动条
        contentWidth: Math.max(1, doc ? doc.clientWidth : window.innerWidth),
        height: Math.max(1, Math.max(doc ? doc.scrollHeight : 0, body ? body.scrollHeight : 0)),
        viewport: Math.max(1, window.innerHeight),
        scrollY: window.scrollY || doc.scrollTop || 0,
        bg: bg || '',
      };
    },
  });
  return r && r.result;
}

/** 截整页前先从头滚到底再回顶部：触发懒加载、展开折叠内容，减少拼接时的高度漂移 */
async function preScroll(tabId) {
  try {
    await chrome.scripting.executeScript({
      target: { tabId },
      func: async () => {
        const doc = document.documentElement;
        const vh = Math.max(1, window.innerHeight);
        const total = Math.max(doc.scrollHeight, document.body ? document.body.scrollHeight : 0);
        for (let y = 0; y < total; y += vh) {
          window.scrollTo({ top: y, left: 0, behavior: 'instant' });
          await new Promise((r) => setTimeout(r, 80));
        }
        window.scrollTo({ top: 0, left: 0, behavior: 'instant' });
        await new Promise((r) => setTimeout(r, 120));
      },
    });
  } catch (_) { /* 页面禁止注入时跳过 */ }
}

async function shotScrollTo(tabId, top) {
  const [r] = await chrome.scripting.executeScript({
    target: { tabId },
    func: (y) => {
      window.scrollTo({ top: y, left: 0, behavior: 'instant' });
      return window.scrollY || 0;
    },
    args: [top],
  });
  return (r && r.result) || 0;
}

/** 截图期间临时隐藏 fixed / sticky 元素，避免每屏重复出现页头与浮动条 */
async function hideFloating(tabId) {
  try {
    await chrome.scripting.executeScript({
      target: { tabId },
      func: () => {
        const els = [];
        const walk = (root) => {
          let nodes = [];
          try { nodes = root.querySelectorAll('*'); } catch (_) { return; }
          nodes.forEach((el) => {
            let pos = '';
            try { pos = getComputedStyle(el).position; } catch (_) { pos = ''; }
            if (pos === 'fixed' || pos === 'sticky') {
              if (el.id === '__sp_clip_toast__') return;
              els.push([el, el.style.visibility]);
              el.style.visibility = 'hidden';
            }
            // 开放 shadow root 里的固定元素同样要隐藏（部分站点用 Shadow DOM 渲染页头）
            if (el.shadowRoot) walk(el.shadowRoot);
          });
        };
        walk(document.body || document.documentElement);
        // 截图期间禁用平滑滚动，避免滚动位置与截图不同步
        const st = document.createElement('style');
        st.id = '__sp_shot_style__';
        st.textContent = 'html,body{scroll-behavior:auto !important;}';
        document.documentElement.appendChild(st);
        window.__spShotHidden = els;
        return els.length;
      },
    });
  } catch (_) { /* 注入失败时保持原样 */ }
}

async function restoreFloating(tabId) {
  try {
    await chrome.scripting.executeScript({
      target: { tabId },
      func: () => {
        const els = window.__spShotHidden || [];
        els.forEach((pair) => {
          try { pair[0].style.visibility = pair[1] || ''; } catch (_) { /* 忽略 */ }
        });
        window.__spShotHidden = [];
        const st = document.getElementById('__sp_shot_style__');
        if (st) st.remove();
      },
    });
  } catch (_) { /* 忽略 */ }
}

async function uploadShot(cfg, itemId, blob, meta) {
  const m = meta || {};
  const res = await fetch(cfg.serverUrl + '/api/v1/clips/' + itemId + '/screenshot', {
    method: 'POST',
    headers: {
      Authorization: 'Bearer ' + cfg.token,
      'Content-Type': blob.type || 'image/png',
      'X-Shot-Mode': m.mode || 'viewport',
      'X-Shot-Width': String(m.width || 0),
      'X-Shot-Height': String(m.height || 0),
    },
    body: blob,
  });
  let data = {};
  try { data = await res.json(); } catch (_) { /* 非 JSON 响应 */ }
  if (!res.ok) {
    const err = new Error(data.msg || ('HTTP ' + res.status));
    err.status = res.status;
    throw err;
  }
  if (data && typeof data === 'object' && 'code' in data) {
    if (data.code !== 0) throw new Error(data.msg || '截图上传失败');
    return data.data;
  }
  return data;
}

/** 截图入口：mode=full 滚动拼接整页，mode=viewport 只截当前可视区；整页失败自动回退可视区 */
async function capturePage(itemId, tabId, windowId, mode) {
  if (!itemId) throw new Error('缺少卡片 ID');
  const cfg = await getConfig();
  if (!cfg.serverUrl || !cfg.token) throw new Error('请先在扩展选项中配置服务器地址与 Token');

  let tab = { id: tabId, windowId };
  if (!tab.id) {
    const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
    tab = (tabs && tabs[0]) || {};
  }
  if (!tab.id) throw new Error('找不到当前页面');
  if (!tab.windowId) {
    const t = await chrome.tabs.get(tab.id);
    tab.windowId = t.windowId;
  }

  if (mode === 'viewport') {
    const dataUrl = await chrome.tabs.captureVisibleTab(tab.windowId, { format: 'png' });
    const bmp = await createImageBitmap(dataUrlToBlob(dataUrl));
    const w = bmp.width;
    const h = bmp.height;
    bmp.close && bmp.close();
    const blob = dataUrlToBlob(dataUrl);
    await uploadShot(cfg, itemId, blob, { mode: 'viewport', width: w, height: h });
    return { ok: true, mode: 'viewport', bytes: blob.size, width: w, height: h };
  }

  // 先滚一遍触发懒加载，再重新取高度，避免拼接途中页面长高导致错位
  await preScroll(tab.id);
  const metrics = await shotMetrics(tab.id);
  const limit = Math.min(metrics.height, SHOT_MAX_CSS_HEIGHT);

  // 首屏保留站点页头（此时 sticky 元素还在正常位置），之后隐藏固定元素再继续拼接
  try {
    const canvas = { ctx: null, w: 0, h: 0 };
    let scale = 1;
    let drawScale = 1;
    let srcW = 1;
    let target = 0;
    let lastY = -1;
    let hidden = false;
    while (target < limit) {
      const actualY = await shotScrollTo(tab.id, target);
      await sleep(SHOT_STEP_DELAY);
      const dataUrl = await chrome.tabs.captureVisibleTab(tab.windowId, { format: 'png' });
      const bmp = await createImageBitmap(dataUrlToBlob(dataUrl));
      if (!canvas.ctx) {
        scale = bmp.width / metrics.width; // devicePixelRatio：截图是物理像素（宽度含滚动条）
        // 按 clientWidth 裁掉右侧滚动条，避免每屏边缘出现滚动条残影
        srcW = Math.max(1, Math.min(bmp.width, Math.round(metrics.contentWidth * scale)));
        let outW = srcW;
        let outH = Math.max(1, Math.round(limit * scale));
        if (outH > SHOT_MAX_CANVAS) {
          outW = Math.max(1, Math.round(outW * (SHOT_MAX_CANVAS / outH)));
          outH = SHOT_MAX_CANVAS;
        }
        canvas.w = outW;
        canvas.h = outH;
        canvas.ctx = new OffscreenCanvas(outW, outH).getContext('2d');
        // 先铺页面底色：即使拼接边缘有 1px 舍入差，也不会露出黑色缝隙
        const bg = metrics.bg && metrics.bg !== 'rgba(0, 0, 0, 0)' && metrics.bg !== 'transparent' ? metrics.bg : '#ffffff';
        canvas.ctx.fillStyle = bg;
        canvas.ctx.fillRect(0, 0, outW, outH);
        drawScale = outW / srcW;
      }
      if (actualY !== lastY) {
        // 用「上一帧底边 = 下一帧顶边」的坐标计算，避免四舍五入留下 1px 黑线
        const f = scale * drawScale;
        const y0 = Math.round(actualY * f);
        const y1 = Math.min(canvas.h, Math.round(Math.min(limit, actualY + metrics.viewport) * f));
        const srcH = Math.min(bmp.height, Math.max(1, Math.round((y1 - y0) / f)));
        if (y1 > y0) {
          canvas.ctx.drawImage(bmp, 0, 0, srcW, srcH, 0, y0, canvas.w, y1 - y0);
        }
        lastY = actualY;
        if (!hidden) {
          // 第一帧画完后再隐藏固定/吸顶元素，后续每屏不再重复页头
          hidden = true;
          await hideFloating(tab.id);
          await sleep(150);
        }
      }
      bmp.close && bmp.close();
      // 滚动被页面底部夹住时停止，避免重复截图
      if (actualY + metrics.viewport >= limit) break;
      target = Math.min(limit - 1, actualY + metrics.viewport);
      if (target <= actualY) break;
    }

    const offscreen = canvas.ctx.canvas;
    let blob = await offscreen.convertToBlob({ type: 'image/png' });
    if (blob.size > 8 * 1024 * 1024) {
      blob = await offscreen.convertToBlob({ type: 'image/jpeg', quality: 0.85 });
    }
    await uploadShot(cfg, itemId, blob, { mode: 'full', width: canvas.w, height: canvas.h });
    await shotScrollTo(tab.id, metrics.scrollY).catch(() => {});
    return { ok: true, mode: 'full', bytes: blob.size, width: canvas.w, height: canvas.h };
  } catch (err) {
    // 回退：只截当前可视区，保证至少有一张截图
    await shotScrollTo(tab.id, metrics.scrollY).catch(() => {});
    const dataUrl = await chrome.tabs.captureVisibleTab(tab.windowId, { format: 'png' });
    const bmp = await createImageBitmap(dataUrlToBlob(dataUrl));
    const w = bmp.width;
    const h = bmp.height;
    bmp.close && bmp.close();
    const blob = dataUrlToBlob(dataUrl);
    await uploadShot(cfg, itemId, blob, { mode: 'viewport', width: w, height: h });
    return { ok: true, mode: 'viewport', bytes: blob.size, width: w, height: h, warning: err.message };
  } finally {
    await restoreFloating(tab.id).catch(() => {});
  }
}

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
  if (msg.type === 'screenshot') {
    capturePage(msg.itemId, msg.tabId, msg.windowId, msg.mode)
      .then(sendResponse)
      .catch((e) => sendResponse({ error: e.message, status: e.status }));
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

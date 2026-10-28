/* SolarPanel 剪藏扩展设置页 */
'use strict';

const $ = (id) => document.getElementById(id);
const state = { serverUrl: '', token: '', defaultGroup: 0 };

document.addEventListener('DOMContentLoaded', init);

async function init() {
  $('ver').textContent = 'v' + chrome.runtime.getManifest().version;
  const data = await chrome.storage.local.get('spClipConfig');
  const cfg = data.spClipConfig || {};
  state.serverUrl = cfg.serverUrl || '';
  state.token = cfg.token || '';
  state.defaultGroup = parseInt(cfg.defaultGroup || '0', 10) || 0;
  $('serverUrl').value = state.serverUrl;
  $('token').value = state.token;

  $('saveBtn').addEventListener('click', save);
  $('testBtn').addEventListener('click', test);
  $('toggleToken').addEventListener('click', () => {
    const t = $('token');
    t.type = t.type === 'password' ? 'text' : 'password';
    $('toggleToken').textContent = t.type === 'password' ? '显示' : '隐藏';
  });
  $('adminBtn').addEventListener('click', () => {
    const base = normalize($('serverUrl').value);
    if (!base) {
      setStatus('请先填写服务器地址', 'err');
      return;
    }
    chrome.tabs.create({ url: base + '/admin.html' });
  });

  if (state.serverUrl) loadGroups();
}

function normalize(url) {
  return String(url || '').trim().replace(/\/+$/, '');
}

// 服务端统一响应信封 {code,data,msg}：成功取 data，失败抛错
function unwrap(payload) {
  if (payload && typeof payload === 'object' && 'code' in payload) {
    if (payload.code !== 0) throw new Error(payload.msg || '请求失败');
    return payload.data;
  }
  return payload;
}

function setStatus(text, cls) {
  const el = $('status');
  el.textContent = text || '';
  el.className = 'status' + (cls ? ' ' + cls : '');
}

async function loadGroups() {
  const base = normalize($('serverUrl').value) || state.serverUrl;
  const token = $('token').value.trim() || state.token;
  if (!base || !token) return;
  try {
    const res = await fetch(base + '/api/v1/groups', { headers: { Authorization: 'Bearer ' + token } });
    if (!res.ok) return;
    const groups = unwrap(await res.json());
    const sel = $('defaultGroup');
    sel.innerHTML = '<option value="0">默认分组（稍后读）</option>';
    groups.filter((g) => !g.parent_id).forEach((top) => {
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
    sel.value = String(state.defaultGroup || 0);
  } catch (_) {
    /* 忽略，测试连接会给出明确错误 */
  }
}

async function save() {
  const serverUrl = normalize($('serverUrl').value);
  const token = $('token').value.trim();
  if (!serverUrl || !/^https?:\/\//i.test(serverUrl)) {
    setStatus('服务器地址必须以 http:// 或 https:// 开头', 'err');
    return;
  }
  if (!token) {
    setStatus('请填写个人访问令牌', 'err');
    return;
  }
  const sel = $('defaultGroup');
  const defaultGroup = parseInt(sel.value || '0', 10) || 0;
  const defaultGroupName = sel.options[sel.selectedIndex] ? sel.options[sel.selectedIndex].textContent : '';
  await chrome.storage.local.set({
    spClipConfig: { serverUrl, token, defaultGroup, defaultGroupName },
  });
  state.serverUrl = serverUrl;
  state.token = token;
  state.defaultGroup = defaultGroup;
  setStatus('已保存', 'ok');
}

async function test() {
  const serverUrl = normalize($('serverUrl').value);
  const token = $('token').value.trim();
  if (!serverUrl || !token) {
    setStatus('请先填写服务器地址与 Token', 'err');
    return;
  }
  setStatus('正在连接…');
  try {
    const res = await fetch(serverUrl + '/api/v1/me', { headers: { Authorization: 'Bearer ' + token } });
    let data = {};
    try { data = await res.json(); } catch (_) { /* 忽略 */ }
    if (res.status === 401) {
      setStatus('连接失败：Token 无效或已过期', 'err');
      return;
    }
    if (!res.ok) {
      setStatus('连接失败：' + (data.msg || ('HTTP ' + res.status)), 'err');
      return;
    }
    let me;
    try { me = unwrap(data); } catch (e) {
      setStatus('连接失败：' + e.message, 'err');
      return;
    }
    if (!me || !me.clip_enabled) {
      setStatus('已连接，但站点关闭了剪藏功能', 'err');
      return;
    }
    setStatus('连接成功：' + (me.name || me.username) + '（' + me.role + '）', 'ok');
    loadGroups();
  } catch (e) {
    setStatus('连接失败：' + e.message, 'err');
  }
}

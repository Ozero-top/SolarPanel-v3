/* SolarPanel 剪藏库列表页 */
'use strict';

const $ = (id) => document.getElementById(id);

const state = {
  q: '',
  kind: '',
  readState: 'all', // all | unread | starred
  group: 0,
  tag: '',
  sort: 'newest',
  view: 'grid',
  offset: 0,
  limit: 24,
  total: 0,
  items: [],
  loading: false,
};

document.addEventListener('DOMContentLoaded', init);

function init() {
  try {
    const v = localStorage.getItem('sp_clips_view');
    if (v === 'list' || v === 'grid') state.view = v;
  } catch (_) { /* 忽略隐私模式 */ }
  syncTheme();

  $('cl-q').addEventListener('input', debounce(onSearchInput, 350));
  $('cl-q').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      state.q = $('cl-q').value.trim();
      reload();
    } else if (e.key === 'Escape') {
      $('cl-q').value = '';
      state.q = '';
      reload();
    }
  });
  $('cl-q-clear').addEventListener('click', () => {
    $('cl-q').value = '';
    state.q = '';
    toggleClear();
    reload();
  });

  $('cl-tabs').addEventListener('click', (e) => {
    const btn = e.target.closest('.cl-tab');
    if (!btn) return;
    state.kind = btn.dataset.kind || '';
    markActive('cl-tabs', '.cl-tab', btn);
    reload();
  });

  $('cl-stats').addEventListener('click', (e) => {
    const btn = e.target.closest('.cl-stat');
    if (!btn) return;
    if (btn.dataset.state) {
      const next = state.readState === btn.dataset.state ? 'all' : btn.dataset.state;
      state.readState = next;
    } else if (btn.dataset.kind) {
      state.kind = state.kind === btn.dataset.kind ? '' : btn.dataset.kind;
      markActive('cl-tabs', '.cl-tab', document.querySelector('.cl-tab[data-kind="' + state.kind + '"]'));
    }
    syncStats();
    reload();
  });

  $('cl-group').addEventListener('change', () => {
    state.group = parseInt($('cl-group').value || '0', 10) || 0;
    reload();
  });

  $('cl-sort').addEventListener('change', () => {
    state.sort = $('cl-sort').value;
    reload();
  });

  $('cl-view').addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-view]');
    if (!btn) return;
    state.view = btn.dataset.view;
    try { localStorage.setItem('sp_clips_view', state.view); } catch (_) { /* 忽略 */ }
    applyView();
  });

  $('cl-more').addEventListener('click', () => load(false));

  $('cl-list').addEventListener('click', onListClick);

  document.addEventListener('keydown', (e) => {
    if (e.key === '/' && !isTyping(e.target)) {
      e.preventDefault();
      $('cl-q').focus();
      $('cl-q').select();
    }
  });

  applyView();
  reload();
}

/** 主题：沿用主页逻辑，浏览器里没有 sp_style 缓存时用后端设置补齐风格主题 */
function syncTheme() {
  if (typeof applyTheme !== 'function') return;
  API.get('/api/public.php').then((d) => {
    if (d && d.settings) applyTheme(d.settings);
  }).catch(() => { /* 请求失败时沿用本地缓存 */ });
}

function isTyping(el) {
  if (!el) return false;
  const tag = (el.tagName || '').toLowerCase();
  return tag === 'input' || tag === 'textarea' || tag === 'select' || el.isContentEditable;
}

function debounce(fn, wait) {
  let timer = null;
  return function () {
    clearTimeout(timer);
    timer = setTimeout(fn, wait);
  };
}

function onSearchInput() {
  const q = $('cl-q').value.trim();
  toggleClear();
  if (q === state.q) return;
  state.q = q;
  reload();
}

function toggleClear() {
  $('cl-q-clear').classList.toggle('hidden', $('cl-q').value.trim() === '');
}

function markActive(boxId, sel, el) {
  const box = $(boxId);
  if (!box) return;
  box.querySelectorAll(sel).forEach((n) => n.classList.remove('active'));
  if (el) el.classList.add('active');
}

function syncStats() {
  document.querySelectorAll('.cl-stat').forEach((btn) => {
    let on = false;
    if (btn.dataset.state) on = btn.dataset.state === state.readState;
    else if (btn.dataset.kind) on = btn.dataset.kind === state.kind;
    btn.classList.toggle('active', on);
  });
}

function applyView() {
  const list = $('cl-list');
  list.className = 'cl-list ' + state.view;
  document.querySelectorAll('#cl-view button').forEach((b) => {
    b.classList.toggle('active', b.dataset.view === state.view);
  });
}

function reload() {
  state.offset = 0;
  state.items = [];
  $('cl-list').textContent = '';
  load(true);
}

async function load(reset) {
  if (state.loading) return;
  state.loading = true;
  setStatus(reset ? '加载中…' : '', '');
  $('cl-more').classList.add('hidden');

  const params = new URLSearchParams();
  if (state.q) params.set('q', state.q);
  if (state.kind) params.set('kind', state.kind);
  if (state.readState === 'unread') params.set('state', 'unread');
  if (state.readState === 'starred') params.set('state', 'starred');
  if (state.group > 0) params.set('group', String(state.group));
  if (state.tag) params.set('tag', state.tag);
  params.set('sort', state.sort);
  params.set('limit', String(state.limit));
  params.set('offset', String(reset ? 0 : state.offset));

  try {
    const data = await API.get('/api/v1/clips?' + params.toString());
    state.loading = false;
    if (reset) {
      state.items = [];
      $('cl-list').textContent = '';
    }
    const items = Array.isArray(data.items) ? data.items : [];
    state.total = data.total || 0;
    state.offset = (reset ? 0 : state.offset) + items.length;
    state.items = state.items.concat(items);

    renderStats(data.stats || {});
    renderGroups(data.groups || []);
    renderTags(data.tags || []);

    items.forEach((it) => $('cl-list').appendChild(card(it)));
    $('cl-empty').classList.toggle('hidden', state.items.length > 0);
    $('cl-more').classList.toggle('hidden', state.items.length < state.total || items.length === 0);
    setStatus(items.length === 0 ? '' : ('已显示 ' + state.items.length + ' / ' + state.total + ' 条'), '');
    syncStats();
  } catch (e) {
    state.loading = false;
    const msg = /登录|401|未授权/.test(e.message || '') ? '需要登录后才能访问剪藏库，请先登录。' : (e.message || '加载失败');
    setStatus(msg, 'error');
    $('cl-empty').classList.add('hidden');
  }
}

function setStatus(text, cls) {
  const el = $('cl-status');
  el.textContent = text || '';
  el.className = 'cl-status' + (cls ? ' ' + cls : '');
}

function renderStats(stats) {
  $('st-total').textContent = String(stats.total || 0);
  $('st-unread').textContent = String(stats.unread || 0);
  $('st-starred').textContent = String(stats.starred || 0);
  $('st-snapshot').textContent = String(stats.snapshot || 0);
  $('st-shot').textContent = String(stats.screenshot || 0);
}

function renderGroups(groups) {
  const sel = $('cl-group');
  const cur = String(state.group || 0);
  sel.textContent = '';
  sel.appendChild(opt('0', '全部分组'));
  groups.forEach((g) => sel.appendChild(opt(String(g.id), (g.title || '未分组') + '（' + g.count + '）')));
  // 选中项不在当前分面里时补一个，避免下拉显示错位
  if (!groups.some((g) => String(g.id) === cur) && cur !== '0') {
    sel.appendChild(opt(cur, '当前分组'));
  }
  sel.value = cur;
}

function opt(value, text) {
  const o = document.createElement('option');
  o.value = value;
  o.textContent = text;
  return o;
}

function renderTags(tags) {
  const bar = $('cl-tagbar');
  bar.textContent = '';
  if (!tags.length) {
    bar.classList.add('hidden');
    return;
  }
  bar.classList.remove('hidden');
  tags.forEach((t) => {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'cl-tagchip' + (state.tag === t.tag ? ' active' : '');
    chip.dataset.tag = t.tag;
    chip.textContent = '#' + t.tag;
    const n = document.createElement('span');
    n.className = 'n';
    n.textContent = String(t.count);
    chip.appendChild(n);
    chip.addEventListener('click', () => {
      state.tag = state.tag === t.tag ? '' : t.tag;
      reload();
    });
    bar.appendChild(chip);
  });
}

function hostOf(url) {
  try {
    return new URL(url).hostname.replace(/^www\./, '');
  } catch (_) {
    return '';
  }
}

function shotMeta(it) {
  try {
    const m = JSON.parse(it.screenshot_meta || '{}');
    return m && typeof m === 'object' ? m : {};
  } catch (_) {
    return {};
  }
}

function card(it) {
  const article = document.createElement('article');
  article.className = 'cl-card';
  article.dataset.id = String(it.id);

  const host = hostOf(it.url);

  // 缩略图：截图 > 封面 > 站点图标 > 首字
  const thumb = document.createElement('a');
  thumb.className = 'cl-thumb';
  thumb.href = '/read/' + it.id;
  thumb.dataset.read = '1';

  const shot = it.screenshot_path ? assetUrl(it.screenshot_path) : '';
  const cover = (it.icon_type === 'image' && it.icon_value) ? assetUrl(it.icon_value) : '';
  const src = shot || cover;
  if (src) {
    const img = document.createElement('img');
    img.src = src;
    img.alt = '';
    img.loading = 'lazy';
    img.referrerPolicy = 'no-referrer';
    thumb.appendChild(img);
  } else {
    const fb = document.createElement('span');
    fb.className = 'cl-thumb-fallback';
    fb.textContent = (it.title || host || '?').trim().slice(0, 1).toUpperCase();
    thumb.appendChild(fb);
  }
  if (shot) {
    const meta = shotMeta(it);
    const badge = document.createElement('span');
    badge.className = 'cl-thumb-badge';
    badge.textContent = meta.mode === 'viewport' ? '可视区截图' : '整页截图';
    thumb.appendChild(badge);
  }

  const main = document.createElement('div');
  main.className = 'cl-main';

  const top = document.createElement('div');
  top.className = 'cl-top';
  const grp = document.createElement('span');
  grp.className = 'cl-group';
  grp.textContent = it.group || '未分组';
  top.appendChild(grp);
  const dot = document.createElement('span');
  dot.className = 'cl-dot' + (it.read_at ? ' hidden' : '');
  dot.title = '未读';
  top.appendChild(dot);
  const star = document.createElement('button');
  star.type = 'button';
  star.className = 'cl-star' + (it.starred ? ' on' : '');
  star.dataset.act = 'star';
  star.textContent = it.starred ? '★' : '☆';
  star.title = it.starred ? '取消收藏' : '收藏';
  top.appendChild(star);

  const title = document.createElement('a');
  title.className = 'cl-title';
  title.href = '/read/' + it.id;
  title.dataset.read = '1';
  title.textContent = it.title || it.url || '未命名剪藏';

  const ex = document.createElement('p');
  ex.className = 'cl-excerpt';
  ex.textContent = it.summary || it.excerpt || it.description || '';

  const tagBox = document.createElement('div');
  tagBox.className = 'cl-tags';
  (it.tags || []).slice(0, 4).forEach((t) => {
    const chip = document.createElement('span');
    chip.className = 'cl-tag';
    chip.textContent = '#' + t;
    tagBox.appendChild(chip);
  });

  const foot = document.createElement('div');
  foot.className = 'cl-foot';
  const h = document.createElement('span');
  h.className = 'cl-host';
  h.textContent = host || '本地';
  h.title = it.url || '';
  foot.appendChild(h);
  if (it.word_count) {
    const w = document.createElement('span');
    w.textContent = it.word_count + ' 字';
    foot.appendChild(w);
  }
  const acts = document.createElement('div');
  acts.className = 'cl-actions';
  const open = document.createElement('a');
  open.className = 'cl-act';
  open.href = it.url || '#';
  open.target = '_blank';
  open.rel = 'noopener noreferrer';
  open.textContent = '原文';
  acts.appendChild(open);
  const del = document.createElement('button');
  del.type = 'button';
  del.className = 'cl-act danger';
  del.dataset.act = 'del';
  del.textContent = '删除';
  acts.appendChild(del);
  foot.appendChild(acts);

  main.appendChild(top);
  main.appendChild(title);
  main.appendChild(ex);
  main.appendChild(tagBox);
  main.appendChild(foot);
  article.appendChild(thumb);
  article.appendChild(main);
  return article;
}

async function onListClick(e) {
  const readLink = e.target.closest('[data-read]');
  if (readLink) {
    const art = readLink.closest('.cl-card');
    const it = state.items.find((x) => String(x.id) === (art && art.dataset.id));
    if (it && !it.read_at) markRead(it.id);
    return; // 交给浏览器跳转
  }

  const act = e.target.closest('[data-act]');
  if (!act) return;
  const art = act.closest('.cl-card');
  const id = parseInt(art && art.dataset.id, 10);
  const it = state.items.find((x) => x.id === id);
  if (!it) return;

  if (act.dataset.act === 'star') {
    act.disabled = true;
    try {
      const res = await API.request('/api/v1/clips/' + id, { method: 'PATCH', data: { starred: !it.starred } });
      it.starred = !!res.starred;
      act.classList.toggle('on', it.starred);
      act.textContent = it.starred ? '★' : '☆';
      act.title = it.starred ? '取消收藏' : '收藏';
      if (state.readState === 'starred' && !it.starred) art.remove();
      toast(it.starred ? '已收藏' : '已取消收藏');
    } catch (err) {
      toast('操作失败：' + (err.message || '未知错误'));
    } finally {
      act.disabled = false;
    }
    return;
  }

  if (act.dataset.act === 'del') {
    if (!confirm('删除这条剪藏？对应的正文快照与截图也会一并删除。')) return;
    act.disabled = true;
    try {
      await API.request('/api/v1/clips', { method: 'DELETE', data: { id: id } });
      art.remove();
      state.items = state.items.filter((x) => x.id !== id);
      state.total = Math.max(0, state.total - 1);
      $('cl-empty').classList.toggle('hidden', state.items.length > 0);
      toast('已删除');
    } catch (err) {
      act.disabled = false;
      toast('删除失败：' + (err.message || '未知错误'));
    }
  }
}

/** 打开阅读页时标记已读；keepalive 保证跳转后请求仍能发出 */
function markRead(id) {
  const tok = (document.cookie.match(/(?:^|;\s*)csrf_token=([^;]+)/) || [])[1];
  fetch('/api/v1/clips/' + id, {
    method: 'PATCH',
    credentials: 'same-origin',
    keepalive: true,
    headers: {
      'Content-Type': 'application/json',
      ...(tok ? { 'X-CSRF-Token': decodeURIComponent(tok) } : {}),
    },
    body: JSON.stringify({ read: true }),
  }).then((res) => {
    if (!res.ok) return;
    const it = state.items.find((x) => x.id === id);
    if (!it) return;
    it.read_at = 'now';
    const cardEl = document.querySelector('.cl-card[data-id="' + id + '"] .cl-dot');
    if (cardEl) cardEl.classList.add('hidden');
  }).catch(() => { /* 标记已读失败不影响阅读 */ });
}

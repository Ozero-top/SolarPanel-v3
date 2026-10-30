/* SolarPanel 正文快照阅读页（P1） */
'use strict';

const $ = (id) => document.getElementById(id);

const state = {
  id: '',
  item: null,
  content: null,
  imageDomains: [],
  starred: false,
};

document.addEventListener('DOMContentLoaded', init);

function init() {
  const m = location.pathname.match(/^\/read\/(\d+)/);
  if (!m) {
    showError('地址不合法：缺少剪藏 ID');
    return;
  }
  state.id = m[1];
  $('rd-refetch').addEventListener('click', () => refetch(false));
  $('rd-capture').addEventListener('click', () => refetch(true));
  $('rd-delete').addEventListener('click', removeSnapshot);

  // 阅读工具条
  $('rd-star').addEventListener('click', toggleStar);
  $('rd-font-dec').addEventListener('click', () => setFontScale(-1));
  $('rd-font-inc').addEventListener('click', () => setFontScale(1));
  $('rd-font-reset').addEventListener('click', () => setFontScale(0, true));
  $('rd-night').addEventListener('click', toggleNight);
  $('rd-copy').addEventListener('click', copyMarkdown);
  $('rd-print').addEventListener('click', () => window.print());

  // 截图灯箱（缩放 / 原始尺寸 / 拖拽平移）
  $('rd-shot-link').addEventListener('click', openLightbox);
  $('rd-lightbox-close').addEventListener('click', closeLightbox);
  $('rd-lightbox').addEventListener('click', (e) => {
    if (e.target === $('rd-lightbox')) closeLightbox();
  });
  $('rd-lb-in').addEventListener('click', () => zoomBy(1.25));
  $('rd-lb-out').addEventListener('click', () => zoomBy(1 / 1.25));
  $('rd-lb-fit').addEventListener('click', lbReset);
  $('rd-lb-actual').addEventListener('click', actualSize);
  $('rd-lb-stage').addEventListener('wheel', (e) => {
    if ($('rd-lightbox').classList.contains('hidden')) return;
    e.preventDefault();
    zoomBy(e.deltaY < 0 ? 1.12 : 1 / 1.12);
  }, { passive: false });
  $('rd-lb-stage').addEventListener('dblclick', () => {
    if (LB.scale > lbFitScale() + 0.01) lbReset();
    else actualSize();
  });
  $('rd-lb-stage').addEventListener('pointerdown', (e) => {
    if (!$('rd-lightbox-img').getAttribute('src')) return;
    LB.drag = { x: e.clientX - LB.tx, y: e.clientY - LB.ty };
    e.currentTarget.setPointerCapture(e.pointerId);
    e.currentTarget.classList.add('dragging');
  });
  $('rd-lb-stage').addEventListener('pointermove', (e) => {
    if (!LB.drag) return;
    LB.tx = e.clientX - LB.drag.x;
    LB.ty = e.clientY - LB.drag.y;
    lbApply();
  });
  const endDrag = (e) => {
    if (!LB.drag) return;
    LB.drag = null;
    e.currentTarget.classList.remove('dragging');
  };
  $('rd-lb-stage').addEventListener('pointerup', endDrag);
  $('rd-lb-stage').addEventListener('pointercancel', endDrag);
  document.addEventListener('keydown', (e) => {
    if ($('rd-lightbox').classList.contains('hidden')) return;
    if (e.key === 'Escape') closeLightbox();
    else if (e.key === '+' || e.key === '=') zoomBy(1.25);
    else if (e.key === '-') zoomBy(1 / 1.25);
    else if (e.key === '0') lbReset();
    else if (e.key === '1') actualSize();
  });

  restoreReadingPrefs();
  syncTheme();
  load();
}

/** 主题：沿用主页逻辑，浏览器里没有 sp_style 缓存时用后端设置补齐风格主题 */
function syncTheme() {
  if (typeof applyTheme !== 'function') return;
  API.get('/api/public.php').then((d) => {
    if (d && d.settings) applyTheme(d.settings);
  }).catch(() => { /* 请求失败时沿用本地缓存 */ });
}

/* ---------- 阅读偏好：字号 / 夜间 ---------- */

const FS_MIN = 14;
const FS_MAX = 22;
const FS_DEFAULT = 17;

function restoreReadingPrefs() {
  let fs = FS_DEFAULT;
  try {
    const v = parseInt(localStorage.getItem('sp_rd_fs') || '', 10);
    if (v >= FS_MIN && v <= FS_MAX) fs = v;
  } catch (_) { /* 忽略 */ }
  applyFont(fs);
  try {
    if (localStorage.getItem('sp_rd_night') === '1') {
      document.documentElement.dataset.rdnight = '1';
    }
  } catch (_) { /* 忽略 */ }
  syncNightBtn();
}

function applyFont(px) {
  document.documentElement.style.setProperty('--rd-fs', px + 'px');
}

function setFontScale(delta, reset) {
  let cur = parseInt(getComputedStyle(document.documentElement).getPropertyValue('--rd-fs') || String(FS_DEFAULT), 10);
  if (!cur || Number.isNaN(cur)) cur = FS_DEFAULT;
  let next = reset ? FS_DEFAULT : Math.min(FS_MAX, Math.max(FS_MIN, cur + delta));
  applyFont(next);
  try { localStorage.setItem('sp_rd_fs', String(next)); } catch (_) { /* 忽略 */ }
}

function toggleNight() {
  const on = document.documentElement.dataset.rdnight !== '1';
  if (on) {
    document.documentElement.dataset.rdnight = '1';
  } else {
    delete document.documentElement.dataset.rdnight;
  }
  try { localStorage.setItem('sp_rd_night', on ? '1' : '0'); } catch (_) { /* 忽略 */ }
  syncNightBtn();
}

function syncNightBtn() {
  const on = document.documentElement.dataset.rdnight === '1';
  $('rd-night').textContent = on ? '日间' : '夜间';
  $('rd-night').classList.toggle('on', on);
}

/* ---------- 收藏 / 复制 ---------- */

async function toggleStar() {
  const btn = $('rd-star');
  btn.disabled = true;
  try {
    const res = await API.request('/api/v1/clips/' + state.id, { method: 'PATCH', data: { starred: !state.starred } });
    state.starred = !!res.starred;
    syncStarBtn();
  } catch (e) {
    setMsg('操作失败：' + (e.message || '未知错误'), 'error');
  } finally {
    btn.disabled = false;
  }
}

function syncStarBtn() {
  const btn = $('rd-star');
  btn.textContent = state.starred ? '★ 已收藏' : '☆ 收藏';
  btn.classList.toggle('on', state.starred);
}

async function copyMarkdown() {
  const md = state.content && state.content.content_md ? String(state.content.content_md) : '';
  if (!md) {
    setMsg('没有可复制的正文', 'error');
    return;
  }
  const ok = async () => {
    try {
      await navigator.clipboard.writeText(md);
    } catch (_) {
      const ta = document.createElement('textarea');
      ta.value = md;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
    }
    setMsg('Markdown 已复制到剪贴板', 'ok');
  };
  await ok();
}

/* ---------- 截图灯箱（缩放 / 原始尺寸 / 拖拽） ---------- */

const LB = { scale: 1, tx: 0, ty: 0, natural: { w: 0, h: 0 }, drag: null };

function lbApply() {
  const img = $('rd-lightbox-img');
  img.style.transform = 'translate(' + LB.tx + 'px,' + LB.ty + 'px) scale(' + LB.scale + ')';
  $('rd-lb-zoom').textContent = Math.round(LB.scale * 100) + '%';
}

/** 适应窗口的缩放比：不放大超过 100%，避免小图被拉糊 */
function lbFitScale() {
  const stage = $('rd-lb-stage');
  const vw = Math.max(1, stage.clientWidth - 40);
  const vh = Math.max(1, stage.clientHeight - 40);
  if (!LB.natural.w || !LB.natural.h) return 1;
  return Math.max(0.05, Math.min(1, Math.min(vw / LB.natural.w, vh / LB.natural.h)));
}

function lbReset() {
  LB.scale = lbFitScale();
  LB.tx = 0;
  LB.ty = 0;
  lbApply();
}

function zoomBy(k) {
  LB.scale = Math.max(0.1, Math.min(8, LB.scale * k));
  lbApply();
}

function actualSize() {
  LB.scale = 1;
  lbApply();
}

function openLightbox(e) {
  const src = $('rd-shot-img').getAttribute('src');
  if (!src) return;
  e.preventDefault();
  const img = $('rd-lightbox-img');
  LB.natural = { w: 0, h: 0 };
  LB.tx = 0;
  LB.ty = 0;
  LB.scale = 1;
  img.onload = () => {
    LB.natural = { w: img.naturalWidth, h: img.naturalHeight };
    lbReset();
  };
  img.src = src;
  $('rd-lightbox').classList.remove('hidden');
  document.body.style.overflow = 'hidden';
  if (img.complete && img.naturalWidth) {
    LB.natural = { w: img.naturalWidth, h: img.naturalHeight };
    lbReset();
  }
}

function closeLightbox() {
  $('rd-lightbox').classList.add('hidden');
  $('rd-lightbox-img').removeAttribute('src');
  $('rd-lightbox-img').style.transform = '';
  document.body.style.overflow = '';
  LB.drag = null;
}

async function load() {
  try {
    const data = await API.get('/api/v1/clips/' + state.id);
    state.item = data.item;
    state.content = data.content;
    state.imageDomains = Array.isArray(data.image_domains) ? data.image_domains : [];
    state.starred = !!data.starred;
    render(data);
    syncStarBtn();
    if (!data.read_at) markRead();
  } catch (e) {
    if (/登录|401|未授权/.test(e.message || '')) {
      $('rd-error-link').classList.remove('hidden');
      showError('需要登录后才能阅读正文快照');
    } else {
      showError(e.message || '加载失败');
    }
  }
}

/** 打开即标记已读（失败不影响阅读） */
function markRead() {
  API.request('/api/v1/clips/' + state.id, { method: 'PATCH', data: { read: true } }).catch(() => { /* 忽略 */ });
}

function render(data) {
  $('rd-loading').classList.add('hidden');
  $('rd-error').classList.add('hidden');
  $('rd-article').classList.remove('hidden');

  const item = data.item || {};
  const c = data.content;
  document.title = (item.title || '正文快照') + ' · SolarPanel';
  $('rd-title').textContent = item.title || item.url || '未命名剪藏';
  $('rd-origin').href = item.url || '#';

  const host = hostOf(item.url);
  const bits = [];
  if (host) bits.push(host);
  if (data.group) bits.push(data.group);
  if (c && c.word_count) bits.push(c.word_count + ' 字');
  if (c && c.fetched_at) bits.push('抓取于 ' + c.fetched_at);
  $('rd-meta').textContent = bits.join(' · ');

  // 标签：只做展示，编辑在后台卡片弹窗
  const tagsBox = $('rd-tags');
  tagsBox.textContent = '';
  const tags = Array.isArray(data.tags) ? data.tags : [];
  tagsBox.classList.toggle('hidden', tags.length === 0);
  tags.forEach((t) => {
    const chip = document.createElement('span');
    chip.className = 'rd-tag';
    chip.textContent = '#' + t;
    tagsBox.appendChild(chip);
  });

  // 摘要与自动标签（P2）：本地规则或外部 API 生成
  const summaryEl = $('rd-summary');
  const summary = c && c.summary ? String(c.summary).trim() : '';
  summaryEl.textContent = summary;
  summaryEl.classList.toggle('hidden', summary === '');

  const aiBox = $('rd-aitags');
  aiBox.textContent = '';
  const autoTags = c && c.auto_tags ? String(c.auto_tags).split(',').map((t) => t.trim()).filter(Boolean) : [];
  aiBox.classList.toggle('hidden', autoTags.length === 0);
  autoTags.forEach((t) => {
    const chip = document.createElement('span');
    chip.className = 'rd-tag';
    chip.textContent = t;
    aiBox.appendChild(chip);
  });

  // 整页截图（P2）：扩展上传后在此展示
  const shot = $('rd-shot');
  const shotPath = c && c.screenshot_path ? safeUrl(c.screenshot_path) : '';
  if (shotPath) {
    $('rd-shot-img').src = shotPath;
    $('rd-shot-link').href = shotPath;
    $('rd-shot-dl').href = shotPath;
    $('rd-shot-dl').setAttribute('download', 'clip-' + state.id + (shotPath.indexOf('.jpg') > -1 ? '.jpg' : '.png'));
    $('rd-shot-meta').textContent = shotMetaText(c && c.screenshot_meta);
    shot.classList.remove('hidden');
  } else {
    shot.classList.add('hidden');
    $('rd-shot-img').removeAttribute('src');
  }

  const body = $('rd-body');
  body.textContent = '';
  if (!data.has_snapshot || !c || !c.content_md) {
    $('rd-empty').classList.remove('hidden');
    $('rd-refetch').classList.remove('hidden');
    $('rd-delete').classList.toggle('hidden', !shotPath);
    return;
  }
  $('rd-empty').classList.add('hidden');
  $('rd-refetch').classList.remove('hidden');
  $('rd-delete').classList.remove('hidden');
  $('rd-delete').disabled = false;
  $('rd-refetch').disabled = false;
  renderMarkdown(c.content_md, body);
}

function showError(msg) {
  $('rd-loading').classList.add('hidden');
  $('rd-article').classList.add('hidden');
  $('rd-error').classList.remove('hidden');
  $('rd-error-msg').textContent = msg;
}

function setMsg(text, cls) {
  const el = $('rd-msg');
  el.textContent = text || '';
  el.className = 'rd-msg' + (cls ? ' ' + cls : '');
}

async function refetch(force) {
  const btn = force ? $('rd-capture') : $('rd-refetch');
  btn.disabled = true;
  setMsg('正在重新抓取正文…');
  try {
    const res = await API.post('/api/v1/clips/' + state.id + '/refetch');
    if (res.warnings && res.warnings.length) {
      setMsg(warnText(res.warnings, res.message), 'error');
      return;
    }
    await load();
    setMsg(res.changed ? '正文已更新（' + res.word_count + ' 字）' : '内容没有变化', 'ok');
  } catch (e) {
    setMsg('抓取失败：' + (e.message || '未知错误'), 'error');
  } finally {
    btn.disabled = false;
  }
}

async function removeSnapshot() {
  if (!confirm('删除正文快照？导航卡片会保留。')) return;
  const btn = $('rd-delete');
  btn.disabled = true;
  try {
    await API.request('/api/v1/clips/' + state.id + '/content', { method: 'DELETE' });
    $('rd-body').textContent = '';
    $('rd-refetch').classList.add('hidden');
    $('rd-delete').classList.add('hidden');
    $('rd-shot').classList.add('hidden');
    $('rd-shot-img').removeAttribute('src');
    $('rd-summary').classList.add('hidden');
    $('rd-aitags').classList.add('hidden');
    $('rd-empty').classList.remove('hidden');
    setMsg('已删除快照', 'ok');
  } catch (e) {
    setMsg('删除失败：' + (e.message || '未知错误'), 'error');
    btn.disabled = false;
  }
}

function warnText(warnings, message) {
  const map = {
    fetch_failed: '页面抓取失败，可能被目标站点拦截',
    extract_failed: '没能从页面中提取出正文',
    save_failed: '快照写入失败',
  };
  const text = map[warnings[0]] || '抓取失败';
  return message ? text + '：' + message : text;
}

function hostOf(url) {
  try {
    return new URL(url).host;
  } catch (_) {
    return '';
  }
}

/** 截图元数据文案：整页 / 可视区 + 尺寸 */
function shotMetaText(raw) {
  let m = {};
  try { m = JSON.parse(raw || '{}') || {}; } catch (_) { m = {}; }
  const label = m.mode === 'viewport' ? '可视区截图' : '整页截图';
  if (m.width > 0 && m.height > 0) {
    return label + ' · ' + m.width + ' × ' + m.height + ' px';
  }
  return label;
}

/* ---------- 轻量 Markdown 渲染：全部通过 DOM 节点构建，不使用 innerHTML ---------- */

const INLINE_RE = /!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)|\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)|`([^`]+)`|\*\*([^*]+)\*\*|__([^_]+)__|\*([^*]+)\*|_([^_]+)_/g;

function safeUrl(raw) {
  try {
    const u = new URL(raw, location.origin);
    return (u.protocol === 'http:' || u.protocol === 'https:') ? u.href : '';
  } catch (_) {
    return '';
  }
}

// imageAllowed 快照图片域名白名单：空列表表示不限制；否则按主机名或其子域匹配
function imageAllowed(raw) {
  const domains = state.imageDomains;
  if (!domains || domains.length === 0) return true;
  let host = '';
  try {
    host = new URL(raw, location.origin).hostname.toLowerCase();
  } catch (_) {
    return false;
  }
  return domains.some((d) => {
    const rule = String(d).toLowerCase();
    return host === rule || host.endsWith('.' + rule);
  });
}

function el(tag, cls) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  return node;
}

function inlineInto(parent, text) {
  // 每次递归都新建正则实例，避免共享 lastIndex 被内层调用重置
  const re = new RegExp(INLINE_RE.source, 'g');
  let last = 0;
  let m;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) parent.appendChild(document.createTextNode(text.slice(last, m.index)));
    if (m[1] !== undefined) {
      const src = safeUrl(m[2]);
      if (src && imageAllowed(src)) {
        const img = el('img');
        img.src = src;
        img.alt = m[1] || '';
        img.loading = 'lazy';
        img.referrerPolicy = 'no-referrer';
        parent.appendChild(img);
      } else if (src) {
        // 白名单外的图片不自动加载，避免向第三方泄露阅读者信息，改为外链
        const a = el('a');
        a.href = src;
        a.target = '_blank';
        a.rel = 'noopener noreferrer';
        a.textContent = m[1] ? '[图片] ' + m[1] : '[外链图片]';
        parent.appendChild(a);
      } else {
        parent.appendChild(document.createTextNode(m[0]));
      }
    } else if (m[3] !== undefined) {
      const href = safeUrl(m[4]);
      if (href) {
        const a = el('a');
        a.href = href;
        a.target = '_blank';
        a.rel = 'noopener noreferrer';
        inlineInto(a, m[3]);
        parent.appendChild(a);
      } else {
        parent.appendChild(document.createTextNode(m[0]));
      }
    } else if (m[5] !== undefined) {
      const code = el('code');
      code.textContent = m[5];
      parent.appendChild(code);
    } else {
      const strong = m[6] !== undefined || m[7] !== undefined;
      const t = m[6] !== undefined ? m[6] : (m[7] !== undefined ? m[7] : (m[8] !== undefined ? m[8] : m[9]));
      const node = el(strong ? 'strong' : 'em');
      inlineInto(node, t);
      parent.appendChild(node);
    }
    last = re.lastIndex;
  }
  if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
}

function isBlockStart(line) {
  return /^\s*$/.test(line) || /^#{1,6}\s+/.test(line) || /^```/.test(line) ||
    /^>\s?/.test(line) || /^\s*[-*+]\s+/.test(line) || /^\s*\d+[.)]\s+/.test(line) ||
    /^(?:-{3,}|\*{3,}|_{3,})$/.test(line);
}

function renderMarkdown(md, root) {
  const lines = String(md).replace(/\r\n/g, '\n').split('\n');
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (/^\s*$/.test(line)) {
      i++;
      continue;
    }

    // 代码块
    const fence = line.match(/^```\s*([\w+#-]*)\s*$/);
    if (fence) {
      i++;
      const buf = [];
      while (i < lines.length && !/^```/.test(lines[i])) buf.push(lines[i++]);
      i++; // 跳过结束围栏
      const pre = el('pre');
      const code = el('code');
      if (fence[1]) code.className = 'language-' + fence[1];
      code.textContent = buf.join('\n');
      pre.appendChild(code);
      root.appendChild(pre);
      continue;
    }

    // 标题
    const h = line.match(/^(#{1,6})\s+(.*)$/);
    if (h) {
      const node = el('h' + h[1].length);
      inlineInto(node, h[2]);
      root.appendChild(node);
      i++;
      continue;
    }

    // 分隔线
    if (/^(?:-{3,}|\*{3,}|_{3,})$/.test(line.trim())) {
      root.appendChild(el('hr'));
      i++;
      continue;
    }

    // 引用
    if (/^>\s?/.test(line)) {
      const buf = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^>\s?/, ''));
        i++;
      }
      const quote = el('blockquote');
      const p = el('p');
      inlineInto(p, buf.join(' '));
      quote.appendChild(p);
      root.appendChild(quote);
      continue;
    }

    // 列表
    const isUl = /^\s*[-*+]\s+/.test(line);
    const isOl = /^\s*\d+[.)]\s+/.test(line);
    if (isUl || isOl) {
      const list = el(isUl ? 'ul' : 'ol');
      const itemRe = isUl ? /^\s*[-*+]\s+/ : /^\s*\d+[.)]\s+/;
      while (i < lines.length && itemRe.test(lines[i])) {
        let text = lines[i].replace(itemRe, '');
        i++;
        // 缩进续行并回当前条目
        while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !itemRe.test(lines[i])) {
          text += ' ' + lines[i].trim();
          i++;
        }
        const li = el('li');
        inlineInto(li, text);
        list.appendChild(li);
      }
      root.appendChild(list);
      continue;
    }

    // 段落
    const buf = [];
    while (i < lines.length && !isBlockStart(lines[i])) {
      buf.push(lines[i].trim());
      i++;
    }
    const p = el('p');
    inlineInto(p, buf.join(' '));
    root.appendChild(p);
  }
}

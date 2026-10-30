/**
 * 主页逻辑：全部展示内容由后端 public.php 提供
 */
const state = {
  settings: {},
  groups: [],
  user: null,
  updateInfo: null, // 管理员可见的新版本信息（version.php 返回）
  lanMode: false, // 由后台「默认地址模式」设置决定
  lanAuto: true,  // 当前是否由自动识别决定（false = 手动切换的临时覆盖）
  theme: 'dark',
  sortingGroupId: null, // 当前正在排序的分组 id（仅管理员，null = 未排序）
  sortDirty: false, // 当前排序分组有未保存的拖拽改动
  groupIntroDone: false, // 首屏分组入场动画只播一次（后续重绘不闪动画）
  cardsIntroDone: false, // 首屏卡片入场动画只播一次（后续重绘不闪动画）
  navActiveGroupIdx: 0,  // 导航栏模式（card_style=nav）当前激活顶级分组索引
  activeTopId: 0,        // 当前激活的顶级分组 id（两种模式共享：sidebar 模式用它决定渲染哪个顶级的子 section；navbar 模式和 navActiveGroupIdx 同步）
  activeChildId: 0,      // 当前激活的子分组 id（侧边栏点击二级定位主内容）
  searchKeyword: '',     // 实时页面内搜索关键词（空 = 全量渲染）
  cardsFolded: new Set(), // 已隐藏自身卡片区的一级分组 id（隐藏后可直接看到二级分组）
};

const API_BASE = '/api/';

// 卡片样式：仅保留 nav（导航栏详细）和 navapp（导航栏·应用小图标）；历史遗留 detail/app 自动降级
const cardStyle = () => {
  const s = (state.settings && state.settings.card_style) || 'nav';
  if (s === 'detail') return 'nav';    // 原"详细"→降级导航栏
  if (s === 'app') return 'navapp';    // 原"应用"→降级导航栏·应用
  return s;
};
const isNavbarStyle = () => cardStyle() === 'nav' || cardStyle() === 'navapp';
const isAppCardStyle = () => cardStyle() === 'navapp';

/** 判断当前浏览器访问的 hostname 是否在内网 / 本地环境（default_lan_mode=auto 时用）
 *  @param {string} [lanHostnames] 管理员配置的内网域名/主机名（逗号分隔）
 *  说明：NAS 内网也可能通过反向代理的公网域名访问，此时把该域名填进 lanHostnames 即可判定为内网。
 */
function isLikelyLAN(lanHostnames) {
  // 浏览器返回的 IPv6 字面量带方括号（如 [fd00::1]），先去掉再判断
  const h = String(location.hostname || '').toLowerCase().replace(/^\[|\]$/g, '');
  if (!h) return false;
  // 管理员配置项归一化：去协议/路径/端口，*.example.com 与 .example.com 等价
  const norm = (s) => String(s || '').trim().toLowerCase()
    .replace(/^[a-z]+:\/\//, '')
    .replace(/\/.*$/, '')
    .replace(/:\d+$/, '')
    .replace(/^\*\./, '.')
    .replace(/\.$/, '');
  if (lanHostnames) {
    const list = String(lanHostnames).split(/[,，;\s]+/).map(norm).filter(Boolean);
    // 精确匹配（含其子域），或后缀通配（.example.com 匹配所有子域）
    if (list.includes(h)) return true;
    for (const pat of list) {
      if (pat.startsWith('.')) {
        if (h === pat.slice(1) || h.endsWith(pat)) return true; // 根域 + 所有子域
      } else if (h.endsWith('.' + pat)) {
        return true; // 精确域名同时匹配其子域
      }
    }
  }
  // 内置判断：localhost / 私网域名
  if (h === 'localhost' || h.endsWith('.local') || h.endsWith('.lan') || h.endsWith('.home') || h.endsWith('.intranet')) return true;
  // IPv6 本地链路 / ULA（仅 IPv6 字面量，避免误伤以 fc/fd 开头的域名）
  if (h.includes(':') && (h.startsWith('fe80:') || h.startsWith('fc') || h.startsWith('fd'))) return true;
  // IPv4 私网
  const parts = h.split('.').map(Number);
  if (parts.length === 4 && parts.every(n => Number.isFinite(n))) {
    if (parts[0] === 10) return true;
    if (parts[0] === 172 && parts[1] >= 16 && parts[1] <= 31) return true;
    if (parts[0] === 192 && parts[1] === 168) return true;
    if (parts[0] === 127) return true;
    if (parts[0] === 169 && parts[1] === 254) return true;
    // CGNAT / Tailscale：100.64.0.0/10
    if (parts[0] === 100 && parts[1] >= 64 && parts[1] <= 127) return true;
  }
  return false;
}

/* ===== 手动切换记忆 =====
   旧实现把选择永久写进 localStorage，一旦点过「外网」就再也不走自动识别。
   现在改为记录 {模式, 访问域名, 时间}：换域名（换网络访问）或超过 24 小时即失效，
   由后台设置的自动识别重新接管。 */
const LAN_OVERRIDE_KEY = 'sp_lan_override';
const LAN_OVERRIDE_TTL = 24 * 60 * 60 * 1000;

function readLanOverride() {
  try {
    const raw = localStorage.getItem(LAN_OVERRIDE_KEY);
    if (!raw) return null;
    const o = JSON.parse(raw);
    if (!o || (o.mode !== 'lan' && o.mode !== 'public')) return null;
    if (String(o.host || '') !== (location.hostname || '').toLowerCase()) return null;
    if (!o.ts || Date.now() - o.ts > LAN_OVERRIDE_TTL) return null;
    return o.mode;
  } catch (_) {
    return null;
  }
}

function saveLanOverride(mode) {
  try {
    localStorage.setItem(LAN_OVERRIDE_KEY, JSON.stringify({
      mode: mode,
      host: (location.hostname || '').toLowerCase(),
      ts: Date.now(),
    }));
  } catch (_) { /* 隐私模式无法写入时忽略 */ }
}

/* ===== 内网地址连通性探测 =====
   Split DNS 场景下同一域名内外网都能访问，仅凭 hostname 判断会误判；
   打开内网地址前先做一次短超时探测，不可达则自动回退外网地址。 */
const LAN_PROBE_TIMEOUT = 900;
const LAN_PROBE_TTL = 60 * 1000;
const lanProbeCache = Object.create(null);

function probeLanURL(url) {
  let origin = '';
  try { origin = new URL(url).origin; } catch (_) { return Promise.resolve(false); }
  const hit = lanProbeCache[origin];
  if (hit && Date.now() - hit.ts < LAN_PROBE_TTL) return Promise.resolve(hit.ok);
  return new Promise((resolve) => {
    const finish = (ok) => {
      lanProbeCache[origin] = { ok: ok, ts: Date.now() };
      resolve(ok);
    };
    if (!navigator.onLine) { finish(false); return; }
    const ctrl = typeof AbortController === 'function' ? new AbortController() : null;
    const timer = setTimeout(() => { if (ctrl) ctrl.abort(); }, LAN_PROBE_TIMEOUT);
    // no-cors：只验证能否建立连接（拿到 opaque 响应即视为可达），不读取内容
    fetch(url, {
      mode: 'no-cors',
      method: 'GET',
      cache: 'no-store',
      credentials: 'omit',
      signal: ctrl ? ctrl.signal : undefined,
    }).then(() => { clearTimeout(timer); finish(true); })
      .catch(() => { clearTimeout(timer); finish(false); });
  });
}

function bindTopbarAutoHide() {
  const topbar = document.querySelector('.topbar');
  if (!topbar) return;

  const mobile = window.matchMedia('(max-width: 640px)').matches;
  if (mobile) return; // 移动端不走滚动隐藏，由 initTopbarBySettings 控制

  let lastY = window.scrollY;
  let ticking = false;
  const HIDE_THRESHOLD = 64;

  function apply() {
    if (ticking) return;
    ticking = true;
    requestAnimationFrame(() => {
      ticking = false;
      const y = window.scrollY;
      const dy = y - lastY;

      if (y <= 10) {
        topbar.classList.remove('hidden-scroll');
      } else if (dy > HIDE_THRESHOLD) {
        topbar.classList.add('hidden-scroll');
      }

      lastY = y;
    });
  }

  window.addEventListener('scroll', apply, { passive: true });
}

/** 根据后端设置初始化顶栏显隐 */
function initTopbarBySettings(settings) {
  const topbar = document.querySelector('.topbar');
  const fab = document.getElementById('topbarFab');
  if (!topbar) return;

  const isMobile = window.matchMedia('(max-width: 640px)').matches;
  const desktopEnabled = (settings.topbar_desktop_enabled ?? '0') === '1';
  const mobileEnabled  = (settings.topbar_mobile_enabled  ?? '0') === '1';

  if (isMobile) {
    if (!mobileEnabled) {
      topbar.classList.add('mobile-hidden');
      if (fab) fab.style.display = '';
      bindTopbarFab(topbar, fab);
    } else {
      topbar.classList.remove('mobile-hidden');
      if (fab) fab.style.display = 'none';
    }
  } else {
    if (!desktopEnabled) {
      topbar.style.display = 'none';
    } else {
      topbar.style.display = '';
      bindTopbarAutoHide();
    }
  }

  // 设置已应用：先关掉过渡再揭示顶栏，避免首帧出现收起/滑动动画
  topbar.style.transition = 'none';
  document.documentElement.classList.add('sp-topbar-ready');
  requestAnimationFrame(() => {
    topbar.style.transition = '';
  });
}

function bindTopbarFab(topbar, fab) {
  if (!fab || fab.dataset.bound === '1') return;
  fab.dataset.bound = '1';
  function syncIcon() {
    fab.textContent = topbar.classList.contains('mobile-hidden') ? '▾' : '▴';
  }
  syncIcon();
  fab.addEventListener('click', () => {
    topbar.classList.toggle('mobile-hidden');
    syncIcon();
  });
}

async function boot() {
  let data;
  try {
    data = await API.get(API_BASE + 'public.php');
  } catch (e) {
    document.getElementById('groupsWrap').innerHTML =
      '<div class="empty-tip glass">数据加载失败：' + esc(e.message) + '</div>';
    // 设置拉取失败时不再等待，直接揭示顶栏，避免永久不可见
    document.documentElement.classList.add('sp-topbar-ready');
    return;
  }

  // 访客访问密码：开启且未通过验证时，仅渲染锁屏页，不加载分组/卡片/新闻
  if (data.guest_required) {
    state.settings = data.settings || {};
    initTopbarBySettings(state.settings);
    state.theme = applyTheme(state.settings);
    updateThemeBtn();
    renderBase();
    startClock();
    renderGuestLock();
    return;
  }

  state.settings = data.settings || {};
  initTopbarBySettings(state.settings);
  state.groups = data.groups || [];
  state.user = data.user || null;
  state.theme = applyTheme(state.settings);
  updateThemeBtn();

  // 按 parent_id 构建父子树（parent_id=0 → 顶级，非零 → 子级）
  state.topGroups = [];
  state.childrenMap = {};   // parentId → [childGroup, ...]
  state.groups.forEach(g => {
    g.parent_id = g.parent_id || 0;
    if (g.parent_id === 0) {
      state.topGroups.push(g);
    } else {
      (state.childrenMap[g.parent_id] = state.childrenMap[g.parent_id] || []).push(g);
    }
  });

  renderBase();
  startClock();
  renderSearch();
  renderGroups();
  bindGlobal();
  initNewsSwitch();
  initNASPanel();
  initUpdateNotice();
  scheduleIconPrefetch();
}

/* ---------- 基础元素渲染（标题/logo/公告/页脚/壁纸/内容区） ---------- */
function renderBase() {
  const s = state.settings;

  document.title = s.site_title || 'SolarPanel';
  document.getElementById('siteTitle').textContent = s.site_title || 'SolarPanel';

  const logo = document.getElementById('logoImg');
  if (s.site_logo) {
    logo.src = assetUrl(s.site_logo);
    logo.hidden = false;
    logo.onerror = () => { logo.hidden = true; };
  }

  // 壁纸与遮罩（未设置壁纸时使用柔和渐变背景，不叠加遮罩；有壁纸时隐藏风格装饰层）
  const bg = document.getElementById('bgLayer');
  const mask = document.getElementById('bgMask');
  const hasWp = !!(s.wallpaper || s.wallpaper_source);
  document.body.classList.toggle('has-wallpaper', hasWp);
  if (hasWp) {
    const opacity = parseFloat(s.mask_opacity);
    mask.style.background =
      'rgba(0,0,0,' + (isNaN(opacity) ? 0.35 : Math.min(Math.max(opacity, 0), 1)) + ')';
    // 壁纸模糊（0~30px，默认 6），同步放大避免模糊后边缘露白
    const bRaw = parseInt(s.wallpaper_blur, 10);
    const blur = isNaN(bRaw) ? 6 : Math.min(Math.max(bRaw, 0), 30);
    if (blur > 0) {
      bg.style.filter = 'blur(' + blur + 'px)';
      bg.style.transform = 'scale(1.1)';
    } else {
      bg.style.filter = '';
      bg.style.transform = '';
    }
  } else {
    bg.style.backgroundImage = '';
    mask.style.background = 'transparent';
    bg.style.filter = '';
    bg.style.transform = '';
  }
  // 壁纸来源：每日壁纸源优先（异步拉取，sessionStorage 当日缓存），回退静态壁纸
  const applyWp = (url) => {
    if (url) bg.style.backgroundImage = 'url("' + assetUrl(url).replace(/"/g, '%22') + '")';
    else bg.style.backgroundImage = '';
  };
  if (s.wallpaper_source) {
    applyDailyWallpaper(s.wallpaper_source, applyWp, s.wallpaper || '');
  } else if (s.wallpaper) {
    applyWp(s.wallpaper);
  }

  // 内容区域尺寸（最大宽度 / 左右边距 / 顶部边距 / 底部边距）
  const num = (v, d) => {
    const n = parseInt(v, 10);
    return isNaN(n) ? d : n;
  };
  const rs = document.documentElement.style;
  rs.setProperty('--content-max', num(s.content_maxwidth, 1200) + 'px');
  rs.setProperty('--content-px', num(s.content_pad_lr, 20) + 'px');
  rs.setProperty('--content-pt', num(s.content_pad_top, 0) + 'px');
  rs.setProperty('--content-pb', num(s.content_pad_bottom, 40) + 'px');
  // 卡片透明度（40~100，默认 100 不透明）
  const co = Math.min(Math.max(num(s.card_opacity, 100), 40), 100);
  rs.setProperty('--card-opacity', String(co / 100));

  // 时钟 / 天气字体（12 款免费设计字体，空 = 跟随系统）
  spApplyClockFont(s.clock_font || '');
  // 整页字体（12 款免费设计字体，空 = 跟随系统）
  spApplyPageFont(s.page_font || '');

  // 公告：后台开关打开且有内容时才显示
  const ann = document.getElementById('annBar');
  if (s.announcement_show === '1' && s.announcement && sessionStorage.getItem('sp_ann_dismissed') !== '1') {
    ann.textContent = '📢 ' + s.announcement;
    ann.title = s.announcement + '（点击关闭）';
    ann.hidden = false;
    ann.onclick = () => {
      ann.hidden = true;
      sessionStorage.setItem('sp_ann_dismissed', '1');
    };
  }

  // 页脚：文字（可选）+ 管理入口 + 当前版本号
  const footer = document.getElementById('footer');
  document.getElementById('footerText').textContent = s.footer || '';
  document.getElementById('footerSep').hidden = !s.footer;
  const fv = document.getElementById('footerVer');
  if (fv) {
    fv.textContent = typeof APP_VERSION !== 'undefined' ? APP_VERSION : '';
    fv.onclick = openChangelogModal;
  }
  // 备案信息
  const beian = document.getElementById('footerBeian');
  beian.innerHTML = '';
  const addBeian = (number, link) => {
    if (!number) return;
    const safeLink = link && safeUrl(link) ? link : '';
    const el = safeLink ? document.createElement('a') : document.createElement('span');
    if (safeLink) { el.href = safeLink; el.target = '_blank'; el.rel = 'noopener'; }
    el.textContent = number;
    beian.appendChild(el);
  };
  if (s.icp_show === '1') addBeian(s.icp_number, (s.icp_link || '').trim());
  if (s.police_show === '1') addBeian(s.police_number, (s.police_link || '').trim());
  // 两个都显示则加分隔点
  if (beian.children.length === 2) {
    const dot = document.createElement('span');
    dot.className = 'beian-dot';
    dot.textContent = '·';
    beian.insertBefore(dot, beian.children[1]);
  }
  beian.hidden = beian.children.length === 0;
  footer.hidden = false;

  // 时钟 / 天气显隐与并排；搜索栏宽度
  const clockOn = s.clock_show !== '0';
  const weatherOn = s.weather_show !== '0';
  document.getElementById('clockTime').hidden = !clockOn;
  document.getElementById('clockDate').hidden = !clockOn;
  const clockArea = document.getElementById('clockArea');
  if (clockOn || weatherOn) clockArea.classList.add('show');
  const sw = parseInt(s.search_width, 10);
  if (!isNaN(sw) && sw > 200) document.getElementById('searchBox').style.maxWidth = sw + 'px';
  if (weatherOn) renderWeather(s.weather_city || '');

  // 地址模式：手动切换（按域名 + 24 小时记忆）优先，否则用后台默认（缺省视为自动识别）
  try { localStorage.removeItem('sp_lan_mode'); } catch (_) { /* 清理旧版永久记忆 */ }
  const lanModeSetting = String(s.default_lan_mode || 'auto').toLowerCase();
  const override = readLanOverride();
  state.lanAuto = override === null;
  state.lanMode = override !== null
    ? override === 'lan'
    : (lanModeSetting === 'lan' || (lanModeSetting === 'auto' && isLikelyLAN(s.lan_hostnames)));
  updateLanBtn();

  // 管理入口：guest 角色跳登录页（需管理员账号），其他已登录角色直达后台
  const link = document.getElementById('adminLink');
  if (state.user) {
    if (state.user.role === 'guest') {
      link.href = 'frontend/login.html';
    } else {
      link.href = 'frontend/admin.html';
      link.title = '管理后台（' + (state.user.name || state.user.username) + '）';
    }
  }
}

/* ---------- 天气 ---------- */
async function renderWeather(city, attempt) {
  const el = document.getElementById('weatherArea');
  attempt = attempt || 0;
  try {
    const w = await API.get(API_BASE + 'weather.php?action=current' + (city ? '&city=' + encodeURIComponent(city) : ''));
    if (!w || w.temp === undefined) throw new Error('天气数据为空');
    // 主行：emoji + 天气描述 + 温度；副行：城市 + 湿度 + 风速（emoji+文字）
    el.innerHTML =
      '<div class="w-main">' +
        '<span class="w-emoji">' + w.emoji + '</span>' +
        '<span class="w-desc">' + esc(w.desc) + '</span>' +
        '<span class="w-temp">' + w.temp + '°</span>' +
      '</div>' +
      '<div class="w-sub">' +
        '<span class="w-city">📍 ' + esc(w.city || '') + '</span>' +
        '<span class="w-sep">·</span>' +
        '<span class="w-humidity">💧 ' + w.humidity + '%</span>' +
        '<span class="w-sep">·</span>' +
        '<span class="w-wind">🌬️ ' + w.wind + 'km/h</span>' +
      '</div>';
    el.hidden = false;
  } catch (e) {
    // 上游定位/天气服务偶发抖动：自动重试两次，避免必须手动刷新页面才出现
    if (attempt < 2) {
      setTimeout(() => renderWeather(city, attempt + 1), 1200 * (attempt + 1));
      return;
    }
    // 已有内容时保留旧天气，不整块隐藏
    if (!el.innerHTML) el.hidden = true;
  }
}

/* ---------- 时钟 ---------- */
function startClock() {
  const timeEl = document.getElementById('clockTime');
  const timeCur = timeEl && (timeEl.querySelector('.clock-cur') || timeEl);
  const dateEl = document.getElementById('clockDate');
  const week = ['日', '一', '二', '三', '四', '五', '六'];
  const pad = n => String(n).padStart(2, '0');
  const tick = () => {
    const d = new Date();
    timeCur.textContent = pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
    dateEl.textContent = d.getFullYear() + ' 年 ' + (d.getMonth() + 1) + ' 月 ' + d.getDate() + ' 日 · 星期' + week[d.getDay()];
  };
  tick();
  setInterval(tick, 1000);
}

/* ---------- 搜索 ---------- */
/* 搜索引擎 logo：首位走本站服务端代理（缓存秒回），失败再串行回退公共图标服务，全部失败则隐藏只显示文字 */
const ENGINE_ICON_SOURCES = [
  host => FAVICON_PROXY_BASE + 'favicon.php?host=' + encodeURIComponent(host),
  host => 'https://favicon.im/' + host + '?larger=true',
  host => 'https://favicon.cccyun.cc/' + host,
  host => 'https://www.google.com/s2/favicons?domain=' + host + '&sz=64',
];

function setEngineLogo(img, engineUrl) {
  let host = '';
  try { host = new URL(engineUrl).hostname; } catch (e) { /* 非法地址不取图标 */ }
  let idx = 0;
  img.onerror = () => {
    if (idx >= ENGINE_ICON_SOURCES.length) { img.hidden = true; img.onerror = null; return; }
    img.src = ENGINE_ICON_SOURCES[idx++](host);
  };
  if (host) img.src = ENGINE_ICON_SOURCES[idx++](host);
  else img.hidden = true;
}

/* ---------- 图标 IndexedDB 缓存 ----------
   二次访问或切换分组时图标直接从本地读，秒出；首屏渲染完成后在空闲时后台补齐全部图标。 */
const ICON_DB_NAME = 'sp-icon-cache';
const ICON_DB_STORE = 'icons';
let iconDBPromise;

function iconDBOpen() {
  if (iconDBPromise !== undefined) return iconDBPromise;
  if (!('indexedDB' in window)) {
    iconDBPromise = Promise.resolve(null);
    return iconDBPromise;
  }
  iconDBPromise = new Promise(resolve => {
    try {
      const req = indexedDB.open(ICON_DB_NAME, 1);
      req.onupgradeneeded = () => { req.result.createObjectStore(ICON_DB_STORE); };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => resolve(null);
      req.onblocked = () => resolve(null);
    } catch (_) { resolve(null); }
  });
  return iconDBPromise;
}

function iconDBSend(mode, fn) {
  return iconDBOpen().then(db => {
    if (!db) return null;
    return new Promise(resolve => {
      try {
        const tx = db.transaction(ICON_DB_STORE, mode);
        const req = fn(tx.objectStore(ICON_DB_STORE));
        req.onsuccess = () => resolve(req.result);
        req.onerror = () => resolve(null);
      } catch (_) { resolve(null); }
    });
  });
}

const iconCacheGet = host => iconDBSend('readonly', s => s.get(host));
const iconCachePut = (host, dataUrl) => iconDBSend('readwrite', s => s.put(dataUrl, host));

function blobToDataURL(blob) {
  return new Promise((resolve, reject) => {
    const fr = new FileReader();
    fr.onload = () => resolve(String(fr.result || ''));
    fr.onerror = () => reject(fr.error || new Error('read error'));
    fr.readAsDataURL(blob);
  });
}

/** 后台预取全部分组的 favicon：串行 + 间隔 900ms（接口限流 300 次/分钟），不抢占首屏带宽 */
async function prefetchAllIcons() {
  if (!navigator.onLine || !('indexedDB' in window)) return;
  const hosts = [];
  const seen = new Set();
  (state.groups || []).forEach(g => (g.items || []).forEach(it => {
    if (it.icon_type !== 'favicon' || !it.url || it.icon_data) return;
    let host = '';
    try { host = new URL(it.url).hostname.toLowerCase(); } catch (_) { return; }
    if (!host || seen.has(host)) return;
    seen.add(host);
    hosts.push(host);
  }));
  if (!hosts.length) return;

  for (const host of hosts) {
    if (!navigator.onLine) break;
    if (await iconCacheGet(host)) continue;
    try {
      const resp = await fetch(FAVICON_PROXY_BASE + 'favicon.php?host=' + encodeURIComponent(host), { credentials: 'same-origin' });
      if (resp.ok) {
        const blob = await resp.blob();
        if (blob.size > 0 && blob.size <= 64 * 1024) {
          const dataUrl = await blobToDataURL(blob);
          if (dataUrl) await iconCachePut(host, dataUrl);
        }
      }
    } catch (_) { /* 单个站点失败不影响后续 */ }
    await new Promise(r => setTimeout(r, 900));
  }
}

function scheduleIconPrefetch() {
  const idle = window.requestIdleCallback || (fn => setTimeout(fn, 1200));
  try { idle(() => prefetchAllIcons(), { timeout: 3000 }); } catch (_) { setTimeout(prefetchAllIcons, 1200); }
}

function renderSearch() {
  let engines = [];
  try {
    engines = JSON.parse(state.settings.search_engines || '[]');
  } catch (e) { /* 忽略格式错误 */ }
  engines = engines.filter(e => e && e.name && e.url);
  if (!engines.length) return;

  const wrap = document.getElementById('engineSelect');
  const btn = document.getElementById('engineBtn');
  const curLogo = document.getElementById('engineCurLogo');
  const curName = document.getElementById('engineCurName');
  const menu = document.getElementById('engineMenu');
  let curIdx = Math.max(0, engines.findIndex(e => e.name === state.settings.search_default));

  // 下拉项：logo + 名称
  engines.forEach((e, i) => {
    const item = document.createElement('button');
    item.type = 'button';
    item.className = 'engine-item' + (i === curIdx ? ' active' : '');
    item.dataset.idx = String(i);
    const img = document.createElement('img');
    img.className = 'engine-logo';
    img.alt = '';
    img.loading = 'lazy';
    setEngineLogo(img, e.url);
    const name = document.createElement('span');
    name.textContent = e.name;
    item.appendChild(img);
    item.appendChild(name);
    item.addEventListener('click', () => {
      curIdx = i;
      refreshCur();
      markActive();
      toggleMenu(false);
    });
    menu.appendChild(item);
  });

  function refreshCur() {
    const cur = engines[curIdx];
    curName.textContent = cur.name;
    curLogo.hidden = false;
    setEngineLogo(curLogo, cur.url);
    markActive();
  }
  function markActive() {
    menu.querySelectorAll('.engine-item').forEach(el => {
      el.classList.toggle('active', Number(el.dataset.idx) === curIdx);
    });
  }
  function toggleMenu(show) {
    const open = show === undefined ? menu.hidden : show;
    menu.hidden = !open;
    wrap.classList.toggle('open', open);
  }

  btn.addEventListener('click', e => { e.stopPropagation(); toggleMenu(); });
  document.addEventListener('click', e => {
    if (!wrap.contains(e.target)) toggleMenu(false);
  });
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape' && !menu.hidden) toggleMenu(false);
  });

  refreshCur();
  document.getElementById('searchBox').hidden = (state.settings.search_bar_enabled === '0');

  // 搜索历史：localStorage 存储（最多 10 条）
  const HIST_KEY = 'sp_search_history';
  const FAV_KEY = 'sp_search_favorites';

  function loadHistory() {
    try { return JSON.parse(localStorage.getItem(HIST_KEY)) || []; } catch (e) { return []; }
  }
  function saveHistory(arr) { localStorage.setItem(HIST_KEY, JSON.stringify(arr.slice(0, 10))); }
  function loadFavorites() {
    try { return JSON.parse(localStorage.getItem(FAV_KEY)) || []; } catch (e) { return []; }
  }
  function saveFavorites(arr) { localStorage.setItem(FAV_KEY, JSON.stringify(arr)); }

  const dropdown = document.getElementById('searchHistoryDropdown');
  const favSection = document.getElementById('shFavSection');
  const recentSection = document.getElementById('shRecentSection');
  const favTags = document.getElementById('shFavTags');
  const recentTags = document.getElementById('shRecentTags');
  const emptyTip = document.getElementById('shEmptyTip');
  const input = document.getElementById('searchInput');

  function toggleFav(kw) {
    const favs = loadFavorites();
    const idx = favs.findIndex(f => f.kw === kw);
    if (idx >= 0) favs.splice(idx, 1);
    else favs.unshift({ kw, ts: Date.now() });
    saveFavorites(favs);
  }

  function recordHistory(kw) {
    const h = loadHistory().filter(k => k !== kw);
    h.unshift(kw);
    saveHistory(h);
  }

  function buildTag(kw, faved) {
    const tag = document.createElement('span');
    tag.className = 'sh-tag' + (faved ? ' faved' : '');
    tag.dataset.kw = kw;
    const fav = document.createElement('span');
    fav.className = 'sh-tag-fav';
    fav.textContent = faved ? '★' : '☆';
    fav.title = faved ? '取消收藏' : '收藏';
    const text = document.createElement('span');
    text.textContent = kw;
    tag.appendChild(text);
    tag.appendChild(fav);
    return tag;
  }

  function renderHistoryDropdown() {
    const h = loadHistory();
    const favs = loadFavorites();
    const recent = h.filter(kw => !favs.some(f => f.kw === kw)).slice(0, 10);

    favTags.innerHTML = '';
    recentTags.innerHTML = '';
    favs.forEach(f => favTags.appendChild(buildTag(f.kw, true)));
    recent.forEach(kw => recentTags.appendChild(buildTag(kw, false)));

    favSection.hidden = !favs.length;
    recentSection.hidden = !recent.length;
    emptyTip.hidden = !!(favs.length || recent.length);
  }

  function showDropdown() {
    renderHistoryDropdown();
    dropdown.hidden = false;
  }
  function hideDropdown() { dropdown.hidden = true; }

  dropdown.addEventListener('click', e => {
    const tag = e.target.closest('.sh-tag');
    if (!tag) return;
    e.stopPropagation();
    // 点击星标：切换收藏
    if (e.target.classList.contains('sh-tag-fav')) {
      toggleFav(tag.dataset.kw);
      renderHistoryDropdown();
      return;
    }
    // 点击标签主体：直接搜索
    input.value = tag.dataset.kw;
    doSearch();
    hideDropdown();
  });

  document.getElementById('shClearFav').onclick = e => {
    e.stopPropagation();
    saveFavorites([]);
    renderHistoryDropdown();
  };
  document.getElementById('shClearRecent').onclick = e => {
    e.stopPropagation();
    saveHistory([]);
    renderHistoryDropdown();
  };

  input.addEventListener('focus', showDropdown);
  input.addEventListener('input', () => { if (input.value) hideDropdown(); });
  input.addEventListener('blur', () => setTimeout(hideDropdown, 150));
  document.addEventListener('keydown', e => { if (e.key === 'Escape') hideDropdown(); });
  document.addEventListener('click', e => {
    if (!dropdown.contains(e.target) && !input.contains(e.target)) hideDropdown();
  });

  document.getElementById('searchBtn').onclick = doSearch;
  input.addEventListener('keydown', e => {
    if (e.key === 'Enter') doSearch();
  });

  // 实时页面内搜索（debounce 200ms，避免每击键全量重绘）
  let _searchDebounce;
  input.addEventListener('input', () => {
    // 新闻视图没有站内卡片可筛（热榜数据不受站内搜索影响），只保留「搜索」按钮 / 回车的搜索引擎搜索
    const newsView = document.getElementById('newsView');
    if (newsView && !newsView.hidden) return;
    const kw = input.value.trim();
    clearTimeout(_searchDebounce);
    _searchDebounce = setTimeout(() => {
      state.searchKeyword = kw;
      searchFilterRender();
    }, 200);
  });

  function doSearch() {
    const kw = input.value.trim();
    if (!kw) return;
    recordHistory(kw);
    hideDropdown();
    const engine = engines[curIdx] || engines[0];
    if (!safeUrl(engine.url)) {
      toast('搜索引擎地址协议不合法（仅支持 http / https）', 'error');
      return;
    }
    const url = engine.url.includes('%s')
      ? engine.url.replace('%s', encodeURIComponent(kw))
      : engine.url + encodeURIComponent(kw);
    window.open(url, '_blank');
  }
}

/* ---------- 分组与卡片 ---------- */

/** 渲染单个分组到容器（nav 模式只渲染选中分组时复用） */
function renderGroupCard(container, g, styleApp, idx, skipIntro, isChild) {
  const section = document.createElement('section');
  section.className = 'group glass' + (isChild ? ' group-child' : '') + (state.sortingGroupId === g.id ? ' sorting' : '') + (state.searchKeyword ? ' search-active' : '');
  section.id = 'group-' + g.id;
  // 首屏入场动画（nav 模式跳过，因为每次切换分组都重绘，不能闪）
  if (!skipIntro && !state.groupIntroDone) {
    section.classList.add('group-in');
    section.style.animationDelay = Math.min((idx || 0) * 120, 960) + 'ms';
  }

  const head = document.createElement('div');
  head.className = 'group-head';
  const h2 = document.createElement('h2');
  if (g.icon) {
    const ic = document.createElement('span');
    ic.className = 'g-title-icon';
    ic.textContent = g.icon;
    h2.appendChild(ic);
  }
  h2.appendChild(document.createTextNode(g.title));
  head.appendChild(h2);
  // 隐藏分组徽章（仅登录管理员会拿到隐藏分组数据）
  if (g.is_visible === 0) {
    const hid = document.createElement('span');
    hid.className = 'hidden-badge';
    hid.textContent = '🚫 已隐藏';
    hid.title = '该分组已在前端隐藏，仅登录管理员可见';
    head.appendChild(hid);
  }
  if (g.description) {
    const desc = document.createElement('span');
    desc.className = 'desc';
    desc.textContent = g.description;
    head.appendChild(desc);
  }
  // 管理员/编辑：分组名后显示排序操作 + 快速添加
  const isEditor = state.user && (state.user.role === 'admin' || state.user.role === 'editor');
  let sortWrap = null;
  if (isEditor) {
    sortWrap = document.createElement('span');
    sortWrap.className = 'group-sort';
    if (state.sortingGroupId === g.id) {
      // 排序模式提示：明确告知可拖拽，避免用户感知不到进入排序状态
      const hint = document.createElement('span');
      hint.className = 'sort-hint';
      hint.textContent = '拖拽卡片调整顺序';
      sortWrap.appendChild(hint);
      const saveBtn = document.createElement('button');
      saveBtn.type = 'button';
      saveBtn.className = 'btn btn-sm btn-save-order' + (state.sortDirty ? '' : ' disabled');
      saveBtn.textContent = '💾 保存排序';
      saveBtn.disabled = !state.sortDirty;
      saveBtn.onclick = () => saveGroupSort(g.id);
      const cancelBtn = document.createElement('button');
      cancelBtn.type = 'button';
      cancelBtn.className = 'btn btn-sm';
      cancelBtn.textContent = '取消排序';
      cancelBtn.onclick = () => cancelGroupSort(g.id);
      sortWrap.appendChild(saveBtn);
      sortWrap.appendChild(cancelBtn);
    } else {
      const sortBtn = document.createElement('button');
      sortBtn.type = 'button';
      sortBtn.className = 'btn btn-sm btn-sort-toggle';
      sortBtn.textContent = '排序';
      sortBtn.title = '拖拽卡片调整该分组的显示顺序';
      sortBtn.onclick = () => startGroupSort(g.id);
      sortWrap.appendChild(sortBtn);
    }
    head.appendChild(sortWrap);

    if (state.sortingGroupId !== g.id) {
      const addBtn = document.createElement('span');
      addBtn.className = 'group-add-btn';
      addBtn.title = '快速添加卡片';
      addBtn.textContent = '+';
      addBtn.onclick = (e) => { e.stopPropagation(); openQuickAddCard(g.id); };
      sortWrap.appendChild(addBtn);
    }
  }
  // 一级分组自身有卡片、且下面还有二级分组时，提供「隐藏 / 打开」自身卡片区的开关，
  // 隐藏后二级分组直接上移，不用再往下翻找
  const kidSections = (state.childrenMap && state.childrenMap[g.id]) || [];
  const hasChildSections = !isChild && kidSections.some(k => (k.items || []).length > 0 || k.is_visible !== 0);
  const canFoldCards = hasChildSections && (g.items || []).length > 0 && state.sortingGroupId !== g.id;
  const folded = canFoldCards && state.cardsFolded.has(g.id);
  if (canFoldCards) {
    const foldBtn = document.createElement('button');
    foldBtn.type = 'button';
    foldBtn.className = 'btn btn-sm group-cards-toggle' + (folded ? ' folded' : '');
    foldBtn.textContent = folded ? '打开卡片' : '隐藏卡片';
    foldBtn.title = folded ? '显示该一级分组的卡片' : '收起该一级分组的卡片，直接查看下面的二级分组';
    foldBtn.setAttribute('aria-expanded', folded ? 'false' : 'true');
    foldBtn.onclick = (e) => { e.stopPropagation(); toggleGroupCards(g.id); };
    // 有排序/添加按钮时并入同一容器，整体靠右，开关在最右端
    if (sortWrap) sortWrap.appendChild(foldBtn);
    else head.appendChild(foldBtn);
  }
  section.appendChild(head);

  const cards = document.createElement('div');
  cards.className = 'cards' + (styleApp ? ' style-app' : '') + (folded ? ' cards-collapsed' : '');
  // 先在 DocumentFragment 上构建整组卡片，一次性 append，减少重排次数
  const cardsFrag = document.createDocumentFragment();
  (g.items || []).forEach(item => {
    cardsFrag.appendChild(buildCard(item, styleApp));
  });
  cards.appendChild(cardsFrag);
  section.appendChild(cards);

  // 排序模式：卡片区下方居中的操作提示，避免拖到一半就松手
  if (state.sortingGroupId === g.id) {
    const tip = document.createElement('div');
    tip.className = 'sort-tip';
    tip.textContent = '拖拽卡片时切勿松开鼠标，拖到目标位置后再松手';
    section.appendChild(tip);
  }

  if (!(g.items || []).length) {
    const empty = document.createElement('div');
    empty.className = 'empty-tip';
    empty.textContent = '该分组暂无卡片';
    section.appendChild(empty);
  }

  container.appendChild(section);
}

/** 隐藏 / 打开一级分组自身的卡片区（不影响其下的二级分组） */
function toggleGroupCards(gid) {
  if (!state.cardsFolded) state.cardsFolded = new Set();
  if (state.cardsFolded.has(gid)) state.cardsFolded.delete(gid);
  else state.cardsFolded.add(gid);
  renderGroups();
}

function renderGroups() {
  const wrap = document.getElementById('groupsWrap');
  const styleApp = isAppCardStyle();
  cardIconEagerLeft = CARD_ICON_EAGER_LIMIT; // 每次重绘重置首屏图标直载预算

  // 导航栏模式：由 renderGroupsNavbar 统一渲染（入口）
  if (isNavbarStyle()) {
    renderGroupsNavbar();
    return;
  }

  wrap.innerHTML = '';

  // 确保 topGroups/childrenMap 已准备好
  if (!state.topGroups) {
    state.topGroups = state.groups.filter(g => (g.parent_id || 0) === 0);
    state.childrenMap = {};
    state.groups.forEach(g => {
      if ((g.parent_id || 0) !== 0) {
        (state.childrenMap[g.parent_id] = state.childrenMap[g.parent_id] || []).push(g);
      }
    });
  }

  // ---------- 实时页面内搜索：两级过滤 ----------
  // sidebar 模式 + 无搜索时：只渲染 activeTopId 下的子 section（一级→二级→卡片 隔离）
  // sidebar 模式 + 有搜索：打破隔离，全量 filtered 渲染
  // navbar 模式：由 renderGroupsNavbar 独立处理
  const kw = (state.searchKeyword || '').toLowerCase();
  let renderTop = state.topGroups;
  let renderChildrenMap = state.childrenMap;
  if (kw) {
    const f = searchFilterGroups(kw, state.topGroups, state.childrenMap);
    renderTop = f.top;
    renderChildrenMap = f.childrenMap;
    if (!renderTop.length) {
      wrap.innerHTML = '<div class="empty-tip glass">未找到匹配「' + esc(state.searchKeyword) + '」的分组或卡片</div>';
      renderGroupNav();
      return;
    }
  } else if (!isNavbarStyle()) {
    // 无搜索 + sidebar 模式 → 隔离：只渲染 activeTopId 下的子分组 section
    if (!renderTop.some(g => g.id === state.activeTopId)) {
      state.activeTopId = renderTop[0].id;
    }
    const tg = renderTop.find(g => g.id === state.activeTopId);
    if (tg) {
      renderTop = [tg];
    }
  }

  if (!renderTop.length) {
    wrap.innerHTML = '<div class="empty-tip glass">暂无内容，请先登录后台添加分组与卡片。</div>';
    document.getElementById('groupNav').hidden = true;
    const gnToggleBtn = document.getElementById('gnToggle');
    if (gnToggleBtn) gnToggleBtn.hidden = true;
    return;
  }

  // 两级渲染：顶级分组创建父容器，子分组 append 到对应父容器内
  state._groupsRenderToken = (state._groupsRenderToken || 0) + 1;
  const myToken = state._groupsRenderToken;
  const schedule = window.requestIdleCallback
    ? cb => requestIdleCallback(cb, { timeout: 100 })
    : cb => setTimeout(cb, 16);

  // 预先构建所有顶级父容器（让 DOM 顺序保持顶级组顺序）
  const parentContainers = {};  // topGroupId → HTMLElement
  renderTop.forEach(tg => {
    const kids = renderChildrenMap[tg.id] || [];
    const hasChildren = kids.some(k => (k.items || []).length > 0 || k.is_visible !== 0);
    const hasOwnItems = (tg.items || []).length > 0;
    if (hasChildren) {
      const parentWrap = document.createElement('div');
      parentWrap.className = 'group-parent' + (state.searchKeyword ? ' search-active' : '');
      parentWrap.dataset.parentId = String(tg.id);
      parentWrap.dataset.parentTitle = tg.title;
      // 父容器 header（当顶级本身没 items 时，作为子分组的分组标题）
      if (!hasOwnItems) {
        const ph = document.createElement('div');
        ph.className = 'group-parent-head glass';
        const h = document.createElement('h3');
        h.textContent = (tg.icon ? tg.icon + ' ' : '') + tg.title;
        ph.appendChild(h);
        if (tg.description) {
          const d = document.createElement('span');
          d.className = 'desc';
          d.textContent = tg.description;
          ph.appendChild(d);
        }
        parentWrap.appendChild(ph);
      }
      parentContainers[tg.id] = parentWrap;
      wrap.appendChild(parentWrap);
    } else {
      parentContainers[tg.id] = wrap;  // 无子分组 → 直接 append 到主 wrap
    }
  });

  // 构建渲染队列（顶级有自己 items 的 + 所有可见子分组）
  const queue = [];
  renderTop.forEach(tg => {
    if ((tg.items || []).length > 0) queue.push({ group: tg, isChild: false });
    (renderChildrenMap[tg.id] || []).forEach(kid => {
      if ((kid.items || []).length > 0 || kid.is_visible !== 0) {
        queue.push({ group: kid, isChild: true });
      }
    });
  });

  let qi = 0;
  const renderNext = () => {
    if (myToken !== state._groupsRenderToken) return;
    if (qi >= queue.length) {
      state.groupIntroDone = true;
      state.cardsIntroDone = true;
      renderGroupNav();
      syncCardSortable();  // 重绘后按排序状态重建拖拽实例
      return;
    }
    const entry = queue[qi];
    // 找到目标容器：子分组 → 父容器；顶级 → parentContainers[tg.id] 即 wrap
    const targetParent = entry.isChild
      ? parentContainers[entry.group.parent_id] || wrap
      : parentContainers[entry.group.id];
    renderGroupCard(targetParent, entry.group, styleApp, qi, false, entry.isChild);
    qi++;
    schedule(renderNext);
  };
  renderNext();
}

/* ---------- 左侧悬浮目录条（导航视图=分组目录 / 新闻视图=平台目录，共用同一对元素） ---------- */
let __gnHideTimer = null;
let __gnManual = false;
let __gnHover = false;
function __gnSetCollapsed(c) {
  const nav = document.getElementById('groupNav');
  const t = document.getElementById('gnToggle');
  if (!nav || !t) return;
  nav.classList.toggle('gn-collapsed', c);
  t.classList.toggle('gn-collapsed', c);
  t.setAttribute('aria-expanded', String(!c));
}
function __gnArmHide() {
  // 武装 1.5s 收起防抖：到点时若非手动锁定且鼠标不在目录/把手上才收起
  if (__gnHideTimer) { clearTimeout(__gnHideTimer); __gnHideTimer = null; }
  __gnHideTimer = setTimeout(() => {
    __gnHideTimer = null;
    if (!__gnManual && !__gnHover) __gnSetCollapsed(true);
  }, 1500);
}
function __gnShowNow() {
  // 展开 + 重置 1.5s 收起防抖
  __gnSetCollapsed(false);
  __gnArmHide();
}

/** 目录条把手 / 悬停 / 滚动高亮（DOM 元素常驻，仅绑定一次） */
function bindNavChrome() {
  const nav = document.getElementById('groupNav');
  const toggle = document.getElementById('gnToggle');
  if (!nav || !toggle || toggle.__gnBound) return;
  toggle.__gnBound = true;

  // 左缘把手：收起时 ▶ 点击展开（手动锁定，不自动收起）；展开时旋转为 ◀ 点击收起
  toggle.onclick = () => {
    const willShow = nav.classList.contains('gn-collapsed');
    __gnManual = willShow;
    if (__gnHideTimer) { clearTimeout(__gnHideTimer); __gnHideTimer = null; }
    __gnSetCollapsed(!willShow);
  };
  // 悬停把手自动展开目录；移出后 1.5s 收起
  toggle.onmouseenter = () => { __gnHover = true; __gnShowNow(); };
  toggle.onmouseleave = () => { __gnHover = false; __gnArmHide(); };
  // 悬停目录期间暂停收起；移出后 1.5s 收起（手动锁定除外）
  nav.onmouseenter = () => { __gnHover = true; if (__gnHideTimer) { clearTimeout(__gnHideTimer); __gnHideTimer = null; } };
  nav.onmouseleave = () => { __gnHover = false; __gnArmHide(); };

  // 滚动：高亮当前所在区块（分组 section 或新闻卡片），停止滚动 1.5s 后自动收起（滚动解除手动锁定）
  window.addEventListener('scroll', () => {
    if (nav.hidden) return;
    const items = nav.querySelectorAll('.gn-item');
    if (!items.length) return;
    // 取顶部最接近触发线（视口 35%）且已滚过该线的区块；多列同行时 top 相同取 DOM 最前者
    let activeTarget = null, bestTop = -Infinity;
    const trigger = window.innerHeight * 0.35;
    items.forEach(it => {
      const el = document.getElementById(it.dataset.target);
      if (!el) return;
      const top = el.getBoundingClientRect().top;
      if (top <= trigger && top > bestTop) { bestTop = top; activeTarget = it.dataset.target; }
    });
    items.forEach(it => it.classList.toggle('active', it.dataset.target === activeTarget));
    __gnManual = false;
    __gnShowNow();
  }, { passive: true });
}

/** 导航视图：分组目录（两级 — 顶级分组 + 可展开的二级子分组） */
function renderGroupNav() {
  const nav = document.getElementById('groupNav');
  const toggle = document.getElementById('gnToggle');

  // 导航栏模式：侧边栏由顶部标签栏替代，隐藏
  if (isNavbarStyle()) {
    if (nav) nav.hidden = true;
    if (toggle) toggle.hidden = true;
    return;
  }
  const mobile = document.getElementById('navbarMobile');
  if (mobile) mobile.hidden = true;

  // 确保 topGroups/childrenMap 已准备好（refresh 后 renderGroupNav 可能被单独调用）
  if (!state.topGroups) {
    state.topGroups = state.groups.filter(g => (g.parent_id || 0) === 0);
    state.childrenMap = {};
    state.groups.forEach(g => {
      if ((g.parent_id || 0) !== 0) {
        (state.childrenMap[g.parent_id] = state.childrenMap[g.parent_id] || []).push(g);
      }
    });
  }

  // 搜索过滤：侧边栏只显示命中的节点（顶级或子分组命中 → 父链保留）
  const kw = (state.searchKeyword || '').toLowerCase();
  let navTop = state.topGroups;
  let navChildrenMap = state.childrenMap;
  if (kw) {
    const f = searchFilterGroups(kw, state.topGroups, state.childrenMap);
    navTop = f.top;
    navChildrenMap = f.childrenMap;
  }

  const visibleTop = navTop.filter(g => {
    const kids = navChildrenMap[g.id] || [];
    const hasVisibleKids = kids.some(k => (k.items || []).length > 0 || k.is_visible !== 0);
    return (g.items || []).length > 0 || g.is_visible !== 0 || hasVisibleKids;
  });
  nav.setAttribute('aria-label', '分组目录');
  if (toggle) { toggle.setAttribute('aria-label', '展开分组目录'); toggle.title = '展开分组目录'; }
  if (!visibleTop.length) { nav.innerHTML = ''; nav.hidden = true; if (toggle) toggle.hidden = true; return; }

  // 搜索激活时：activeTopId 可能不再有效 → 重置
  if (kw && !visibleTop.some(g => g.id === state.activeTopId)) {
    state.activeTopId = visibleTop[0].id;
  }
  // 保证 activeTopId 在有效范围内
  if (!visibleTop.some(g => g.id === state.activeTopId)) {
    state.activeTopId = visibleTop[0].id;
  }

  nav.innerHTML = '';
  visibleTop.forEach(g => {
    const kids = navChildrenMap[g.id] || [];
    const li = document.createElement('li');
    // activeTopId 标记（点一级时显示高亮）
    if (g.id === state.activeTopId) li.classList.add('active-top');
    if (kids.length) li.classList.add('gn-parent');

    const a = document.createElement('a');
    a.href = '#group-' + g.id;
    if (g.icon) a.appendChild(document.createTextNode(g.icon + ' '));
    a.appendChild(document.createTextNode(g.title));
    // 点击一级顶级 → 切换主内容区只渲染该顶级下的子分组 section
    a.onclick = (e) => {
      e.preventDefault();
      if (state.activeTopId !== g.id || kw) {
        state.activeTopId = g.id;
        state.searchKeyword = '';  // 切换顶级时清搜索（搜索时已全量展示，不需要切）
        // navbar 模式用 idx 同步
        if (isNavbarStyle()) {
          const idx = (state.topGroups || []).filter(tg => tg.id === g.id).length ?
            (state.topGroups || []).findIndex(tg => tg.id === g.id) : 0;
          state.navActiveGroupIdx = idx;
        }
        renderGroups();
        renderGroupNav();
        renderGroupsNavbar();
      } else {
        // 已经激活 → 滚动到主内容区第一个 section
        const firstSection = document.querySelector('.group, .group-parent');
        if (firstSection) firstSection.scrollIntoView({ behavior: 'smooth', block: 'start' });
      }
    };
    li.appendChild(a);

    if (kids.length) {
      // activeTopId 自动展开该一级的二级列表
      if (g.id === state.activeTopId) li.classList.add('gn-open');

      const caret = document.createElement('button');
      caret.type = 'button';
      caret.className = 'gn-caret';
      caret.setAttribute('aria-label', '展开子分组');
      caret.innerHTML = '▾';
      caret.onclick = (e) => {
        e.preventDefault();
        e.stopPropagation();
        li.classList.toggle('gn-open');
        caret.setAttribute('aria-label', li.classList.contains('gn-open') ? '折叠子分组' : '展开子分组');
      };
      li.appendChild(caret);

      const ul = document.createElement('ul');
      ul.className = 'gn-children';
      kids.forEach(k => {
        const subLi = document.createElement('li');
        if (k.id === state.activeChildId) subLi.classList.add('active-child');
        const subA = document.createElement('a');
        subA.href = '#group-' + k.id;
        if (k.icon) subA.appendChild(document.createTextNode(k.icon + ' '));
        subA.appendChild(document.createTextNode(k.title));
        // 点击二级 → 联动 activeTopId + 定位主内容区
        subA.onclick = (e) => {
          e.preventDefault();
          state.activeTopId = g.id;
          state.activeChildId = k.id;
          // 如果主内容区没渲染（因为 sidebar 模式默认只渲染 activeTopId），先触发渲染
          renderGroups();
          renderGroupNav();
          requestAnimationFrame(() => {
            const target = document.getElementById('group-' + k.id);
            if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
          });
        };
        subLi.appendChild(subA);
        ul.appendChild(subLi);
      });
      li.appendChild(ul);
    }
    nav.appendChild(li);
  });
  nav.hidden = false;
  if (toggle) toggle.hidden = false;
  __gnSetCollapsed(true);
}

/* ---------- 导航栏模式（card_style=nav）：顶部横向分组标签栏 ---------- */

/** 导航栏模式主渲染入口：填充 PC 标签栏 + 移动端图标滑动条 + 触发卡片渲染
 * 两级分组处理：只顶级分组作为 tabs；顶级有子分组时，tabs 显示顶级名
 */
function renderGroupsNavbar() {
  const tabs = document.getElementById('navbarTabs');
  const mobile = document.getElementById('navbarMobile');
  const main = document.getElementById('groupsWrap');
  if (!tabs) return;

  // 确保 topGroups/childrenMap 已准备好
  if (!state.topGroups) {
    state.topGroups = state.groups.filter(g => (g.parent_id || 0) === 0);
    state.childrenMap = {};
    state.groups.forEach(g => {
      if ((g.parent_id || 0) !== 0) {
        (state.childrenMap[g.parent_id] = state.childrenMap[g.parent_id] || []).push(g);
      }
    });
  }

  // 搜索过滤（navbar 模式也要搜）
  const kw = (state.searchKeyword || '').toLowerCase();
  let navTop = state.topGroups;
  let navChildrenMap = state.childrenMap;
  if (kw) {
    const f = searchFilterGroups(kw, state.topGroups, state.childrenMap);
    navTop = f.top;
    navChildrenMap = f.childrenMap;
  }

  // 过滤：顶级分组自身有 items 或含可见子分组才作为 tab
  const visibleTop = navTop.filter(g => {
    const kids = navChildrenMap[g.id] || [];
    const hasVisibleKids = kids.some(k => (k.items || []).length > 0 || k.is_visible !== 0);
    return (g.items || []).length > 0 || hasVisibleKids;
  });

  if (!visibleTop.length) {
    tabs.hidden = true;
    if (mobile) mobile.hidden = true;
    if (main) main.innerHTML = '<div class="empty-tip glass">' + (kw ? '未找到匹配「' + esc(state.searchKeyword) + '」的分组或卡片' : '暂无内容，请先登录后台添加分组与卡片。') + '</div>';
    renderGroupNav();
    return;
  }
  // 索引在可见顶级数组内
  if (state.navActiveGroupIdx >= visibleTop.length) state.navActiveGroupIdx = 0;

  tabs.hidden = false;
  tabs.innerHTML = '';
  visibleTop.forEach((g, vIdx) => {
    const btn = document.createElement('button');
    btn.className = 'ntab-btn' + (vIdx === state.navActiveGroupIdx ? ' active' : '');
    btn.type = 'button';
    if (g.icon) btn.appendChild(document.createTextNode(g.icon + ' '));
    btn.appendChild(document.createTextNode(g.title));
    btn.onclick = () => {
      state.navActiveGroupIdx = vIdx;
      state.activeTopId = g.id;  // 两种模式共享当前顶级
      state.searchKeyword = '';
      state.groupIntroDone = true;
      state.cardsIntroDone = true;
      renderTabsOnly();
      renderNavbarCardsOnly();
      renderGroupNav();
    };
    tabs.appendChild(btn);
  });

  // 移动端图标横向滑动条（同样只顶级）
  if (mobile) {
    mobile.innerHTML = '';
    visibleTop.forEach((g, vIdx) => {
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'nm-item' + (vIdx === state.navActiveGroupIdx ? ' active' : '');
      item.dataset.idx = vIdx;
      item.title = g.title;
      item.setAttribute('aria-label', g.title);
      const face = document.createElement('span');
      face.className = 'nm-face';
      face.textContent = g.icon || '🗂️';
      item.appendChild(face);
      item.onclick = () => {
        state.navActiveGroupIdx = vIdx;
        state.activeTopId = g.id;
        state.searchKeyword = '';
        state.groupIntroDone = true;
        state.cardsIntroDone = true;
        renderTabsOnly();
        renderNavbarCardsOnly();
        renderGroupNav();
      };
      mobile.appendChild(item);
    });
    mobile.hidden = false;   // 移动端始终显示（CSS media query 控制 ≤640px 可见）
  } else {
    if (mobile) mobile.hidden = true;
  }

  state._navbarVisibleTop = visibleTop;  // 缓存供 renderNavbarCardsOnly 使用
  renderGroupNav();
  renderNavbarCardsOnly();
}

/** 同步 PC 标签栏与移动端滑动条的选中态 */
function renderTabsOnly() {
  const tabs = document.getElementById('navbarTabs');
  if (!tabs) return;
  tabs.querySelectorAll('.ntab-btn').forEach((b, i) => {
    b.classList.toggle('active', i === state.navActiveGroupIdx);
  });
  const mobile = document.getElementById('navbarMobile');
  if (mobile) {
    mobile.querySelectorAll('.nm-item').forEach(b => {
      b.classList.toggle('active', Number(b.dataset.idx) === state.navActiveGroupIdx);
    });
  }
}

/** 导航栏模式：渲染选中顶级分组的卡片
 * 两级分组处理：顶级有子分组时，渲染所有可见子分组的 section；顶级有自身 items 时也渲染
 */
function renderNavbarCardsOnly() {
  const wrap = document.getElementById('groupsWrap');
  if (!wrap) return;
  wrap.innerHTML = '';

  const visibleTop = state._navbarVisibleTop ||
    (state.topGroups || []).filter(g => {
      const kids = (state.childrenMap || {})[g.id] || [];
      const hasVisibleKids = kids.some(k => (k.items || []).length > 0 || k.is_visible !== 0);
      return (g.items || []).length > 0 || hasVisibleKids;
    });
  const tg = visibleTop[state.navActiveGroupIdx];
  if (!tg) return;

  const styleApp = isAppCardStyle();
  const kids = (state.childrenMap || {})[tg.id] || [];
  const visibleKids = kids.filter(k => (k.items || []).length > 0 || k.is_visible !== 0);

  // 顶级分组有自身 items → 先渲染顶级 section
  if ((tg.items || []).length > 0) {
    renderGroupCard(wrap, tg, styleApp, state.navActiveGroupIdx, true, false);
  }
  // 再渲染所有可见子分组（如果顶级本身无 items，这里就是全部内容）
  // 导航栏模式下子分组与顶级分组是同一层的兄弟节点，统一按普通分组渲染，
  // 复用一级分组的盒模型（内边距/边框/网格间距一致），卡片尺寸与列位置才能完全对齐
  visibleKids.forEach((k, i) => {
    renderGroupCard(wrap, k, styleApp, i, true, false);
  });
  state.groupIntroDone = true;
  state.cardsIntroDone = true;
  syncCardSortable(); // 导航栏模式重绘后同样重建拖拽实例
}

/* ---------- 实时页面内搜索：两级分组 + 卡片级别 ---------- */

/** 判断字符串是否命中关键词（null-safe + case-insensitive） */
function _hit(kw, str) { return str && str.toLowerCase().includes(kw); }

/** 判断卡片是否命中关键词（title / description / url） */
function itemHit(kw, item) {
  if (!item) return false;
  return _hit(kw, item.title) || _hit(kw, item.description) || _hit(kw, item.url);
}

/**
 * 两级分组 + 卡片过滤
 * @param {string} kw - lower-case 关键词
 * @param {Array} topGroups
 * @param {Object} childrenMap - parentId → [childGroup]
 * @returns {{ top: Array, childrenMap: Object }} 过滤后的结构（不污染原始 state.groups）
 *
 * 匹配规则：
 *  - 顶级分组 title/desc/icon 命中 → 保留（items 全保留）
 *  - 子分组 title/desc 命中 → 父链保留，子分组保留（items 全保留）
 *  - 子分组下有卡片命中 → 父链保留，子分组保留（items 过滤为仅命中卡片）
 *  - 顶级分组自身有卡片命中但分组名/描述不命中 → 保留顶级（items 过滤）
 */
function searchFilterGroups(kw, topGroups, childrenMap) {
  if (!kw) return { top: topGroups, childrenMap };

  const newTop = [];
  const newChildrenMap = {}; // parentId → [childGroup] （过滤后的子分组）
  const newItemsMap = {};    // groupId → [items]（过滤后的卡片，用于 group.items 临时替换）

  topGroups.forEach(tg => {
    const kids = childrenMap[tg.id] || [];

    // 1) 顶级自身命中？
    const topSelfHit = _hit(kw, tg.title) || _hit(kw, tg.description) || _hit(kw, tg.icon);
    // 顶级自身卡片命中？
    const topItems = (tg.items || []);
    const topHitItems = topItems.filter(it => itemHit(kw, it));
    const topHasItemsHit = topHitItems.length > 0;

    // 2) 子分组命中？
    let topHasKidHit = false;
    const keptKids = [];
    kids.forEach(k => {
      const kidSelfHit = _hit(kw, k.title) || _hit(kw, k.description);
      const kidItems = (k.items || []);
      const kidHitItems = kidItems.filter(it => itemHit(kw, it));
      const kidHasItemsHit = kidHitItems.length > 0;

      if (kidSelfHit || kidHasItemsHit) {
        topHasKidHit = true;
        const filteredKid = { ...k };
        if (kidSelfHit) {
          filteredKid.items = kidItems;     // 子分组标题命中 → 卡片全保留
        } else {
          filteredKid.items = kidHitItems;  // 仅卡片命中 → 只保留命中卡片
        }
        keptKids.push(filteredKid);
        newChildrenMap[k.parent_id] = newChildrenMap[k.parent_id] || [];
      }
    });

    // 3) 顶级是否保留
    if (topSelfHit || topHasItemsHit || topHasKidHit) {
      const filteredTg = { ...tg };
      if (topSelfHit) {
        filteredTg.items = topItems;         // 顶级标题命中 → 卡片全保留
      } else if (topHasItemsHit) {
        filteredTg.items = topHitItems;      // 仅顶级卡片命中 → 只保留命中卡片
      } else {
        filteredTg.items = [];                // 顶级本身无卡片命中，靠子分组命中带上 → 顶级无卡片
      }
      newTop.push(filteredTg);

      // 把这个顶级过滤后的子分组挂回去
      if (keptKids.length) {
        newChildrenMap[tg.id] = keptKids;
      }
    }
  });

  return { top: newTop, childrenMap: newChildrenMap };
}

/** 搜索框内容变化后的渲染入口：全量重绘 + 侧边栏同步 + 空态 */
function searchFilterRender() {
  const kw = state.searchKeyword;
  state.groupIntroDone = true;  // 搜索中不再播入场动画
  state.cardsIntroDone = true;

  if (isNavbarStyle()) {
    renderGroupsNavbar();
  } else {
    renderGroups();
  }
  renderGroupNav();
}

/** 新闻视图：平台名称目录（色点取平台主题色） */
/** 新闻视图侧边目录已废弃：与三列热榜标题重复、折叠态在蓝图主题下视觉噪音
 *  入口由 switchView('news') 直接 hidden groupNav + gnToggle
 *  本函数留空壳保持调用点不动（loadNews 成功/失败/渲染结束共 3 处还在调）
 */
function renderNewsNav() {}

/* ---------- 卡片图标加载：直出本站代理图 → onerror 逐级回退 → 文字图标 ---------- */

const CARD_ICON_EAGER_LIMIT = 10; // 首屏前 10 个图标高优先级直载，其余懒加载
let cardIconEagerLeft = CARD_ICON_EAGER_LIMIT;
// host -> 已确认可用的源下标；'fail' 表示整条链都失败，同 host 直接走文字图标
const faviconHostBest = new Map();
let cardIconIO = null;
const iconStarters = new WeakMap();

/** 元素进入视口前后 200px 时启动；不支持 IntersectionObserver 则立即启动 */
function ioObserveIcon(el, start) {
  if (!('IntersectionObserver' in window)) { start(); return; }
  if (!cardIconIO) {
    cardIconIO = new IntersectionObserver(entries => {
      for (const en of entries) {
        if (!en.isIntersecting) continue;
        const fn = iconStarters.get(en.target);
        cardIconIO.unobserve(en.target);
        if (fn) fn();
      }
    }, { root: null, rootMargin: '200px 0px' });
  }
  iconStarters.set(el, start);
  cardIconIO.observe(el);
}

/** buildCard 时卡片尚未进文档，延迟到微任务（此时 renderGroupCard 已完成 append）再观察 */
function armCardIcon(el, start) {
  queueMicrotask(() => {
    if (el.isConnected) ioObserveIcon(el, start);
    else start(); // 兜底：脱离文档直接加载，避免图标永不出现
  });
}

/** img 解码完成后再显示，避免大图解码造成卡片区掉帧；解码失败也照常显示 */
function showWhenDecoded(img, icon, iconDoneRef) {
  const finish = () => {
    if (iconDoneRef.done) return;
    iconDoneRef.done = true;
    icon.classList.remove('icon-loading');
    icon.style.background = '';
    icon.classList.add('has-img');
  };
  if (img.decode) img.decode().then(finish).catch(finish);
  else finish();
}

function buildCard(item, styleApp) {
  const a = document.createElement('a');
  const isHidden = item.is_hidden === 1;
  a.className = 'card scale-in' + (styleApp ? ' app-card' : '') + (isHidden ? ' card-hidden' : '');
  a.href = '#';
  a.dataset.id = item.id; // 排序模式拖拽落位后按 data-id 收集顺序
  // 筛选/最近常用依赖的 data-* 属性
  a.dataset.title = item.title || '';
  a.dataset.url = item.url || item.lan_url || '';
  // 搜索索引 = 标题 + 描述 + URL（空格分隔，统一小写在搜索函数里做）
  a.dataset.search = [
    (item.title || '').toLowerCase(),
    (item.description || '').toLowerCase(),
    (item.url || '').toLowerCase(),
    (item.lan_url || '').toLowerCase(),
  ].join(' ') + (item.icon_type === 'favicon' ? ' favicon' : '');
  a.draggable = false; // 默认禁止原生链接拖拽；排序模式下由 mousedown 动态开启

  // 背景渐变流动层（位于图标与文字之下）
  const bg = document.createElement('span');
  bg.className = 'card-bg';
  bg.setAttribute('aria-hidden', 'true');
  a.appendChild(bg);

  // 图标：默认占位底色，图片解码成功后转 has-img；视口外不发起任何网络请求
  const icon = document.createElement('span');
  icon.className = 'icon';
  a.appendChild(icon);

  // 用对象承载完成标志，供 showWhenDecoded 共享
  const iconState = { done: false };
  const useTextIcon = () => {
    if (iconState.done) return;
    iconState.done = true;
    icon.classList.remove('icon-loading');
    icon.classList.remove('has-img');
    // 文字型：手动文字优先（允许 2 个字母）→ 空则回退标题（英文只取大写首字母）
    const manual = item.icon_type === 'text' && item.icon_value && !isImageUrlLike(item.icon_value);
    const raw = manual ? item.icon_value : (item.title || '?');
    const bgVal = item.icon_bg || stringColor(item.title);
    icon.style.background = bgVal;
    // 背景透明度为 0 时改用主色文字，避免白字消失在透明底上
    const bgParsed = parseBgColor(bgVal);
    icon.classList.toggle('icon-bg-clear', !!bgParsed && bgParsed.alpha === 0);
    // 卡片图标盒尺寸固定（普通 52 / app 58），传入宽度免 getComputedStyle 强制布局
    renderTextIcon(icon, smartIconText(raw, manual ? 2 : 1, !manual), 21, styleApp ? 58 : 52);
  };
  if (item.icon_type === 'image' && item.icon_value) {
    const img = document.createElement('img');
    img.alt = '';
    img.loading = 'lazy';
    img.decoding = 'async';
    img.onerror = useTextIcon;
    img.onload = () => showWhenDecoded(img, icon, iconState);
    icon.classList.add('icon-loading'); // 加载中显示骨架微光，解码完成淡入
    icon.appendChild(img);
    armCardIcon(icon, () => { img.src = assetUrl(item.icon_value); });
  } else if (item.icon_type === 'favicon' && item.url) {
    // 直出本站代理图标；失败后按 onerror 逐级回退公共源，全部失败再兜底文字图标
    let host = '';
    try { host = new URL(item.url).hostname.toLowerCase(); } catch (e) { /* ignore */ }
    const proxySrcs = faviconSources(item.url);
    const known = host ? faviconHostBest.get(host) : undefined;
    if (!proxySrcs.length || known === 'fail') {
      useTextIcon();
    } else {
      const img = document.createElement('img');
      img.alt = '';
      img.decoding = 'async';
      img.referrerPolicy = 'no-referrer';
      // 首屏图标高优先级直载，其余交给浏览器懒加载
      if (cardIconEagerLeft > 0) {
        cardIconEagerLeft--;
        img.loading = 'eager';
        img.fetchPriority = 'high';
      } else {
        img.loading = 'lazy';
        img.fetchPriority = 'low';
      }
      // 服务端首屏内联：data URL 直接显示，失败再走代理回退链
      let srcs = item.icon_data ? [item.icon_data].concat(proxySrcs) : proxySrcs;
      let idx = item.icon_data ? 0 : (typeof known === 'number' ? known : 0);
      let picked = false;
      const pick = (list, i) => {
        srcs = list;
        idx = i;
        picked = true;
        img.src = srcs[idx];
      };
      img.onerror = () => {
        idx++;
        if (idx >= srcs.length) {
          if (host) faviconHostBest.set(host, 'fail');
          useTextIcon();
          return;
        }
        img.src = srcs[idx];
      };
      img.onload = () => {
        if (host) faviconHostBest.set(host, idx);
        showWhenDecoded(img, icon, iconState);
      };
      icon.classList.add('icon-loading'); // 加载中显示骨架微光，解码完成淡入
      icon.appendChild(img);
      if (item.icon_data) {
        pick(srcs, 0);
      } else {
        // 先查 IndexedDB（毫秒级命中免网络），未命中再发起代理请求
        armCardIcon(icon, () => {
          iconCacheGet(host).then(dataUrl => {
            if (picked || iconState.done) return;
            if (dataUrl) pick([dataUrl].concat(proxySrcs), 0);
            else pick(srcs, idx);
          }).catch(() => { if (!picked) pick(srcs, idx); });
        });
      }
    }
  } else {
    useTextIcon();
  }

  // 文本
  const info = document.createElement('div');
  info.className = 'info';
  const t = document.createElement('div');
  t.className = 't';
  t.textContent = item.title;
  info.appendChild(t);
  // 描述为空时不回退显示地址（避免内外网地址挤压标题）
  const descText = String(item.description || '').trim();
  if (descText) {
    const d = document.createElement('div');
    d.className = 'd';
    d.textContent = descText;
    info.appendChild(d);
  }
  a.appendChild(info);

  // 内网地址标识
  if (state.lanMode && item.lan_url) {
    const badge = document.createElement('span');
    badge.className = 'lan-badge';
    badge.textContent = '内网';
    a.appendChild(badge);
  }

  // 隐藏标识（仅 admin/editor 登录态下可见，非管理员后端已过滤）
  if (isHidden) {
    const badge = document.createElement('span');
    badge.className = 'hidden-badge';
    badge.textContent = '🚫 已隐藏';
    badge.title = '该卡片已在前端隐藏，仅登录管理员可见';
    a.appendChild(badge);
  }

  a.onclick = e => {
    e.preventDefault();
    // 当前分组正在排序时，拖拽卡片，禁止跳转打开
    if (state.sortingGroupId !== null) return;
    openPage(item);
  };
  return a;
}

/** 根据字符串生成稳定颜色（无背景色时的文字图标底色） */
function stringColor(str) {
  let hash = 0;
  for (let i = 0; i < str.length; i++) hash = str.charCodeAt(i) + ((hash << 5) - hash);
  const hue = Math.abs(hash) % 360;
  return 'hsl(' + hue + ', 55%, 48%)';
}

/** 跳转地址安全校验：仅允许 http(s)，拦截 javascript: / data: 等协议（后端已校验，前端兜底） */
function safeUrl(url) {
  return /^https?:\/\//i.test(String(url || '').trim());
}

/** 打开卡片：1 当前页 / 2 新窗口 / 3 弹层；newTab 参数可强制覆盖打开方式（右键菜单用）
    内网模式下先探测内网地址是否可达，不可达自动回退外网，避免 Split DNS 场景误判。 */
async function openPage(item, newTab) {
  const useLan = state.lanMode && !!item.lan_url;
  let url = useLan ? item.lan_url : item.url;
  if (!useLan && state.lanMode) warnMissingLan(item);
  if (!url) {
    toast('该卡片未设置地址', 'error');
    return;
  }
  if (!safeUrl(url)) {
    toast('该卡片地址协议不合法（仅支持 http / https），已阻止打开', 'error');
    return;
  }

  const openNew = typeof newTab === 'boolean' ? newTab : item.open_method === 2;
  const openIframe = typeof newTab !== 'boolean' && item.open_method === 3;
  // 新窗口必须在点击手势内同步打开，否则 await 之后会被浏览器弹窗拦截
  let win = openNew && useLan ? window.open('', '_blank') : null;

  if (useLan) {
    const reachable = await probeLanURL(url);
    if (!reachable) {
      const fallback = safeUrl(item.url) ? item.url : '';
      if (!fallback) {
        if (win) win.close();
        toast('内网地址不可达，且该卡片未填写可用的外网地址', 'error');
        return;
      }
      url = fallback;
      toast('内网地址不可达，已改用外网地址打开', 'info');
    }
  }

  if (openNew) {
    if (win) {
      try { win.opener = null; } catch (_) { /* 跨域时忽略 */ }
      win.location.replace(url);
    } else {
      window.open(url, '_blank');
    }
    return;
  }
  if (openIframe) {
    document.getElementById('iframeTitle').textContent = item.title + ' · ' + url;
    document.getElementById('iframeNewTab').href = url;
    document.getElementById('iframeWin').src = url;
    document.getElementById('iframeModal').classList.add('show');
    return;
  }
  location.href = url;
}

/** 内网模式下该卡片没填内网地址：同一卡片每个会话只提示一次，避免反复弹提示 */
const lanWarned = Object.create(null);
function warnMissingLan(item) {
  const id = item && item.id;
  if (!id || lanWarned[id]) return;
  lanWarned[id] = 1;
  toast('「' + (item.title || '该卡片') + '」未填写内网地址，已打开外网地址', 'info');
}

/** 右键卡片弹出操作菜单（仅 admin/editor 生效） */
function showCardContextMenu(x, y, item, groupId) {
  hideCardContextMenu();

  const menu = document.createElement('div');
  menu.className = 'card-context-menu glass-strong';
  menu.style.cssText = 'position:fixed;z-index:9999;left:' + x + 'px;top:' + y + 'px;';

  const items = [
    { label: '🔗 新标签页打开', action: () => openPage(item, true) },
    { label: '📑 当前页面打开', action: () => openPage(item, false) },
    { separator: true },
    { label: '✏️ 编辑卡片', action: () => openQuickAddCard(groupId, item) },
    { label: '🗑️ 删除卡片', danger: true, action: async () => {
      if (!await uiConfirm('确认删除「' + item.title + '」？', { danger: true })) return;
      try {
        await API.post(API_BASE + 'items.php?action=delete', { id: item.id });
        toast('删除成功', 'success');
        await reloadPublic();
      } catch (e) { toast(e.message, 'error'); }
    }},
  ];

  items.forEach(it => {
    if (it.separator) {
      const sep = document.createElement('div');
      sep.className = 'ccm-sep';
      menu.appendChild(sep);
      return;
    }
    const el = document.createElement('div');
    el.className = 'ccm-item' + (it.danger ? ' danger' : '');
    el.textContent = it.label;
    el.onclick = () => { it.action(); hideCardContextMenu(); };
    menu.appendChild(el);
  });

  document.body.appendChild(menu);

  requestAnimationFrame(() => {
    const r = menu.getBoundingClientRect();
    if (r.right > window.innerWidth) menu.style.left = (x - r.width) + 'px';
    if (r.bottom > window.innerHeight) menu.style.top = (y - r.height) + 'px';
  });

  setTimeout(() => {
    document.addEventListener('click', hideCardContextMenu, { once: true });
    window.addEventListener('scroll', hideCardContextMenu, { once: true, passive: true });
    window.addEventListener('resize', hideCardContextMenu, { once: true });
    document.addEventListener('keydown', e => { if (e.key === 'Escape') hideCardContextMenu(); }, { once: true });
  }, 0);
}

function hideCardContextMenu() {
  const m = document.querySelector('.card-context-menu');
  if (m) m.remove();
}

/** 外网/内网切换按钮：文字反映当前模式 */
function updateLanBtn() {
  const btn = document.getElementById('lanBtn');
  if (!btn) return;
  const src = state.lanAuto ? '自动识别' : '手动切换，24 小时后恢复自动识别';
  btn.textContent = state.lanMode ? '内网' : '外网';
  btn.title = (state.lanMode ? '当前：内网地址' : '当前：外网地址') + '（' + src + '），点击切换';
}

/* ---------- 主题切换（浅色 / 深色 / 跟随系统 三态循环） ---------- */
const THEME_CYCLE = ['light', 'dark', 'system'];
const THEME_META = {
  light: { label: '🌞 浅色', tip: '当前：浅色模式，点击切换为深色' },
  dark: { label: '🌙 深色', tip: '当前：深色模式，点击切换为跟随系统' },
  system: { label: '🖥️ 跟随', tip: '当前：跟随系统深浅，点击切换为浅色' },
};

function updateThemeBtn() {
  const btn = document.getElementById('themeBtn');
  if (!btn) return;
  const m = THEME_META[state.theme] || THEME_META.dark;
  btn.textContent = m.label;
  btn.title = m.tip;
}

function bindGlobal() {
  // 左侧悬浮目录条（分组/平台共用）把手与滚动高亮，仅绑定一次
  bindNavChrome();

  // 右键卡片操作菜单（仅 admin/editor 生效）
  document.addEventListener('contextmenu', e => {
    const card = e.target.closest('.card');
    if (!card) return;
    if (!state.user || (state.user.role !== 'admin' && state.user.role !== 'editor')) return;
    e.preventDefault();
    const itemId = +card.dataset.id;
    let foundItem = null, foundGroupId = null;
    for (const g of state.groups) {
      const it = g.items.find(i => i.id === itemId);
      if (it) { foundItem = it; foundGroupId = g.id; break; }
    }
    if (foundItem) showCardContextMenu(e.clientX, e.clientY, foundItem, foundGroupId);
  });

  document.getElementById('themeBtn').onclick = () => {
    const idx = THEME_CYCLE.indexOf(state.theme);
    const next = THEME_CYCLE[(idx + 1) % THEME_CYCLE.length] || 'light';
    state.theme = next;
    localStorage.setItem('sp_theme', next);
    setThemeMode(next);
    updateThemeBtn();
  };

  // 外网/内网切换（所有用户可用，无角色检查）
  const lanBtn = document.getElementById('lanBtn');
  if (lanBtn) {
    lanBtn.onclick = () => {
      state.lanMode = !state.lanMode;
      state.lanAuto = false;
      saveLanOverride(state.lanMode ? 'lan' : 'public');
      updateLanBtn();
      toast(state.lanMode ? '已切换到内网地址' : '已切换到外网地址', 'success');
      reloadPublic();
    };
  }

  // 关闭 iframe 弹层
  const modal = document.getElementById('iframeModal');
  // 右下角常驻「返回顶部」
  const backTop = document.getElementById('backTop');
  if (backTop) backTop.addEventListener('click', () => window.scrollTo({ top: 0, behavior: 'smooth' }));
  document.getElementById('iframeClose').onclick = () => {
    modal.classList.remove('show');
    document.getElementById('iframeWin').src = 'about:blank';
  };
  modal.addEventListener('click', e => {
    if (e.target === modal) {
      modal.classList.remove('show');
      document.getElementById('iframeWin').src = 'about:blank';
    }
  });

  bindSortMode();

  // PWA：顶栏安装按钮（beforeinstallprompt 时显示）
  bindPWAInstall();

  // 键盘快捷键
  bindShortcuts();

  // 底部版本号：点击弹出当前版本更新日志
  const footerVer = document.getElementById('footerVer');
  if (footerVer) {
    footerVer.addEventListener('click', () => {
      const cm = document.getElementById('changelogModal');
      if (cm) cm.classList.add('show');
    });
  }

  // 排序中且有未保存更改时，关闭/刷新页面走浏览器原生拦截
  window.addEventListener('beforeunload', e => {
    if (state.sortingGroupId !== null && state.sortDirty) {
      e.preventDefault();
      e.returnValue = '';
    }
  });
}

/* ---------- 卡片排序模式（仅管理员；每分组独立排序，保存后生效） ---------- */
let pendingSortLeave = null; // 守卫弹窗解决后要执行的动作（如切换到新闻视图）

function bindSortMode() {
  // 守卫弹窗：继续排序 / 放弃并离开 / 保存并离开
  const guard = document.getElementById('orderGuardModal');
  const closeGuard = () => { guard.classList.remove('show'); pendingSortLeave = null; };
  document.getElementById('guardStayBtn').onclick = closeGuard;
  guard.addEventListener('click', e => { if (e.target === guard) closeGuard(); });
  document.getElementById('guardDiscardBtn').onclick = () => {
    const after = pendingSortLeave;
    closeGuard();
    // 放弃：先清排序状态再按 state 已保存数据重绘，还原脏顺序并退出排序
    state.sortingGroupId = null;
    state.sortDirty = false;
    renderGroups();
    if (after) after();
  };
  document.getElementById('guardSaveBtn').onclick = async () => {
    const after = pendingSortLeave;
    const gid = state.sortingGroupId;
    const b = document.getElementById('guardSaveBtn');
    b.disabled = true;
    try {
      await saveGroupSort(gid); // 成功后内部已退出排序
      closeGuard();
      if (after) after();
    } catch (e) {
      toast('保存失败：' + e.message, 'error');
    } finally {
      b.disabled = false;
    }
  };
}

// 卡片拖拽排序：SortableJS（forceFallback 模式）
// forceFallback 让鼠标与触摸共用一套逻辑，避免原生 HTML5 拖放的固有问题：
// 卡片内的 <a>/<img> 抢走拖拽、拖出容器就丢弃落点、手机端完全不触发 dragstart。
// 顺序变化只标脏，仍由「保存排序」按钮提交。
let cardSortable = null;
let sortTiltCleanup = null; // 拖拽期间监听指针、实时调整拖影倾角

function disposeCardSortable() {
  if (!cardSortable) return;
  cardSortable.destroy();
  cardSortable = null;
  if (sortTiltCleanup) sortTiltCleanup();
}

/** 拖拽手感：按指针横向速度实时调整拖影倾斜角（--sort-tilt），松手归零 */
function bindSortTilt() {
  if (sortTiltCleanup) sortTiltCleanup();
  const root = document.documentElement;
  let lastX = null;
  let tilt = 1.2;
  let raf = 0;
  const onMove = (e) => {
    const x = e.clientX;
    if (lastX !== null) {
      // 速度越大倾斜越明显，限制在 1.2°±2.4° 内，避免甩来甩去太夸张
      const v = Math.max(-16, Math.min(16, x - lastX));
      tilt += (1.2 + v * 0.15 - tilt) * 0.3;
      if (!raf) {
        raf = requestAnimationFrame(() => {
          raf = 0;
          root.style.setProperty('--sort-tilt', tilt.toFixed(2) + 'deg');
        });
      }
    }
    lastX = x;
  };
  root.style.setProperty('--sort-tilt', '1.2deg');
  document.addEventListener('pointermove', onMove, { passive: true });
  sortTiltCleanup = () => {
    document.removeEventListener('pointermove', onMove);
    if (raf) cancelAnimationFrame(raf);
    raf = 0;
    root.style.removeProperty('--sort-tilt');
    sortTiltCleanup = null;
  };
}

/** 按当前排序状态启用 / 销毁拖拽；分组重绘会替换 DOM，必须重新创建实例 */
function syncCardSortable() {
  disposeCardSortable();
  if (state.sortingGroupId === null) return;
  if (typeof Sortable === 'undefined') {
    console.warn('[sort] SortableJS 未加载，拖拽排序不可用');
    return;
  }
  const sec = document.getElementById('group-' + state.sortingGroupId);
  const cardsEl = sec && sec.querySelector('.cards');
  if (!cardsEl) return;
  cardSortable = Sortable.create(cardsEl, {
    group: { name: 'group-' + state.sortingGroupId, pull: false, put: false }, // 只在本分组内排序
    animation: 160,
    forceFallback: true,   // 不用原生拖放：拖影统一、触摸可用
    fallbackOnBody: true,
    fallbackTolerance: 4,
    delay: 120,            // 触摸长按 120ms 才开始拖，避免和页面滚动冲突
    delayOnTouchOnly: true,
    touchStartThreshold: 4,
    ghostClass: 'sort-ghost',
    chosenClass: 'sort-chosen',
    dragClass: 'sort-drag',
    scroll: true,          // 拖到视口边缘自动滚动
    scrollSensitivity: 80,
    scrollSpeed: 12,
    bubbleScroll: true,
    onStart: () => {
      cardsEl.classList.add('sort-dragging'); // 其余卡片让位变淡
      bindSortTilt();                        // 拖影随甩动倾向
    },
    onEnd: (evt) => {
      cardsEl.classList.remove('sort-dragging');
      if (sortTiltCleanup) sortTiltCleanup();
      // 落位回弹：结束后移除类，避免动画残留
      const el = evt.item;
      el.classList.add('sort-settle');
      clearTimeout(el._sortSettleTimer);
      el._sortSettleTimer = setTimeout(() => el.classList.remove('sort-settle'), 260);
      state.sortDirty = true;
      updateSortSaveBtn();
    },
  });
}

/** 开启某分组的排序；若另一分组正排序且有改动，先弹守卫 */
function startGroupSort(gid) {
  if (state.sortingGroupId === gid) return;
  if (state.sortingGroupId !== null && state.sortDirty) {
    openSortGuard(() => startGroupSort(gid));
    return;
  }
  state.sortingGroupId = gid;
  state.sortDirty = false;
  renderGroups();
}

/** 保存指定分组的卡片顺序 */
async function saveGroupSort(gid) {
  const sec = document.getElementById('group-' + gid);
  if (!sec) return;
  const ids = Array.prototype.map.call(sec.querySelectorAll('.cards .card'), c => Number(c.dataset.id))
    .filter(n => !isNaN(n));
  await API.post(API_BASE + 'items.php?action=sort_batch', { group_id: gid, ids });
  // DOM 顺序为单一事实源，同步内存 state（取消时 renderGroups 可还原）
  const grp = state.groups.find(x => Number(x.id) === gid);
  if (grp && Array.isArray(grp.items)) {
    const byId = new Map(grp.items.map(i => [Number(i.id), i]));
    grp.items = ids.map(id => byId.get(id)).filter(Boolean);
  }
  state.sortingGroupId = null;
  state.sortDirty = false;
  renderGroups();
  toast('卡片排序已保存', 'success');
}

/** 取消指定分组的排序（还原已保存顺序） */
function cancelGroupSort(gid) {
  state.sortingGroupId = null;
  state.sortDirty = false;
  renderGroups(); // 按 state 已保存数据重绘，还原拖拽改动
  toast('已取消排序', 'info');
}

/** 更新当前排序分组「保存排序」按钮的可用状态 */
function updateSortSaveBtn() {
  const sec = document.getElementById('group-' + state.sortingGroupId);
  if (!sec) return;
  const btn = sec.querySelector('.group-sort .btn-save-order');
  if (btn) btn.disabled = !state.sortDirty;
}

/** 打开未保存守卫弹窗；after 为守卫解决（保存/放弃）后执行的动作（如切换到新闻视图） */
function openSortGuard(after) {
  pendingSortLeave = after;
  document.getElementById('orderGuardModal').classList.add('show');
}

/* ---------- 热点新闻视图（参考 NewsNow：多数据源卡片聚合） ---------- */
const newsState = {
  loaded: false,
  sources: [],   // 源定义
  cards: {},     // id -> 卡片 DOM
  list: {},      // id -> 最近一次数据
};

/** 是否至少配置了一个新闻源（与后端 newsEnabledSetting 判定保持一致） */
function hasConfiguredNewsSources() {
  const raw = String((state.settings || {}).news_sources || '').trim();
  if (!raw) return false;
  try {
    const arr = JSON.parse(raw);
    if (Array.isArray(arr)) return arr.length > 0;
  } catch (e) { /* 旧版逗号分隔格式 */ }
  return raw.split(',').some(s => s.trim());
}

/** 热点新闻按钮显隐：开关开启且至少一个数据源时才显示
 *  独立成函数方便 reloadPublic / 后台保存后重新执行（不依赖刷新页面）
 */
function updateNewsBtnVisible() {
  const btn = document.getElementById('hotNewsBtn');
  if (!btn) return;
  const enabled = (state.settings || {}).news_show !== '0' && hasConfiguredNewsSources();
  btn.hidden = !enabled;
  // 已关闭时退出新闻视图（避免用户停留在"未开启"空页面）
  if (!enabled && !document.getElementById('newsView').hidden) {
    switchView('nav');
  }
}

function initNewsSwitch() {
  const btn = document.getElementById('hotNewsBtn');
  if (!btn) return;

  updateNewsBtnVisible();
  if (btn.hidden) return;  // 整体关闭：不绑定 onclick、不自动切新闻视图

  switchView('nav');

  btn.onclick = () => {
    const isNews = !document.getElementById('newsView').hidden;
    // 排序中切走：有未保存改动先弹守卫
    if (state.sortingGroupId !== null && !isNews) {
      if (state.sortDirty) { openSortGuard(() => switchView('news')); return; }
      state.sortingGroupId = null;
      state.sortDirty = false;
    }
    switchView(isNews ? 'nav' : 'news');
  };
}

function switchView(v) {
  const isNav = v === 'nav';
  document.getElementById('groupsWrap').hidden = !isNav;
  document.getElementById('newsView').hidden = isNav;
  // 按钮高亮状态
  const btn = document.getElementById('hotNewsBtn');
  if (btn) btn.classList.toggle('active', !isNav);
  // 导航栏模式：新闻视图隐藏分组 tabs，显示"返回导航页"单按钮；回导航视图恢复 tabs
  if (isNavbarStyle()) {
    const tabs = document.getElementById('navbarTabs');
    const mobile = document.getElementById('navbarMobile');
    if (tabs) tabs.hidden = !isNav;
    if (mobile) mobile.hidden = !isNav;
    if (isNav) {
      renderGroupsNavbar();  // 恢复完整分组 tabs
    } else {
      renderNavbarBackBtn();  // 新闻视图：只显示返回按钮
    }
  }
  // 左侧悬浮目录：导航视图=分组目录；新闻视图=彻底隐藏（三列热榜标题已是天然导航，侧边目录纯噪音）
  const gn = document.getElementById('groupNav');
  const gt = document.getElementById('gnToggle');
  if (isNav) {
    if (gn) gn.hidden = false;
    if (gt) gt.hidden = false;
    renderGroupNav();
  } else {
    if (gn) { gn.hidden = true; gn.classList.remove('gn-collapsed'); gn.innerHTML = ''; }
    if (gt) { gt.hidden = true; gt.classList.remove('gn-collapsed'); }
  }
  if (!isNav) {
    if (!newsState.loaded) loadNews();
    window.scrollTo({ top: 0 });
  }
}

/** 新闻视图下：把 navbarTabs 区域替换为一个"返回导航页"按钮 */
function renderNavbarBackBtn() {
  const tabs = document.getElementById('navbarTabs');
  if (!tabs) return;
  tabs.hidden = false;
  tabs.innerHTML = '';
  const btn = document.createElement('button');
  btn.className = 'ntab-btn active';
  btn.type = 'button';
  btn.innerHTML = '← 返回导航页';
  btn.onclick = () => switchView('nav');
  tabs.appendChild(btn);
  const mobile = document.getElementById('navbarMobile');
  if (mobile) mobile.hidden = true;
}

async function loadNews() {
  const view = document.getElementById('newsView');
  view.innerHTML = '<div class="empty-tip glass">📰 热点新闻加载中…</div>';
  try {
    const all = await API.get(API_BASE + 'news.php?action=all');
    if (!all || all.enabled === false) {
      view.innerHTML = '<div class="empty-tip glass">热点新闻未开启。请登录后台「站点设置 → 📰 热点新闻」，把开关设为「开启」，并勾选至少一个数据源。</div>';
      newsState.sources = [];
      renderNewsNav();
      return;
    }
    newsState.sources = all.sources || [];
    newsState.list = {};
    (all.list || []).forEach(l => { newsState.list[l.id] = l; });
    renderNewsCards();
    newsState.loaded = true;
    // 首屏缓存渲染后，只补拉"没有内容或已过期"的源：
    // 后端 all 已返回真实抓取时间，新鲜的源不再重复回源（旧实现每次都全量重拉 24 个源）
    const secNow = Date.now() / 1000;
    const staleIds = newsState.sources.filter(def => {
      const rec = newsState.list[def.id];
      if (!rec || !(rec.items || []).length) return true;
      const ttl = Math.max(60, (def.interval || 600) * 0.9);
      return (secNow - (rec.updated || 0)) >= ttl;
    }).map(def => def.id);
    if (staleIds.length) newsFetchQueue(staleIds, false);
  } catch (e) {
    view.innerHTML = '<div class="empty-tip glass">新闻加载失败：' + esc(e.message) + '</div>';
    newsState.sources = [];
    renderNewsNav();
  }
}

function renderNewsCards() {
  const view = document.getElementById('newsView');
  const grid = document.createElement('div');
  grid.className = 'news-grid';
  newsState.cards = {};
  newsState.sources.forEach(def => {
    const card = buildNewsCard(def);
    newsState.cards[def.id] = card;
    grid.appendChild(card);
    const cached = newsState.list[def.id];
    if (cached && cached.items && cached.items.length) {
      fillNewsCard(card, cached.items, cached.updated, false);
    } else {
      fillNewsCard(card, [], 0, false, true);
    }
  });
  view.innerHTML = '';
  view.appendChild(grid);
  // 左侧平台目录
  renderNewsNav();
}

function buildNewsCard(def) {
  const sec = document.createElement('section');
  sec.className = 'news-card glass';
  sec.id = 'news-card-' + def.id;
  sec.style.setProperty('--nc', def.color || 'var(--accent)');

  const head = document.createElement('div');
  head.className = 'nc-head';
  if (def.logo) {
    const logo = document.createElement('img');
    logo.className = 'nc-logo';
    logo.src = def.logo;
    logo.alt = def.name;
    logo.loading = 'lazy';
    logo.onerror = () => { logo.style.display = 'none'; };
    head.appendChild(logo);
  }
  const dot = document.createElement('span');
  dot.className = 'nc-dot';
  const name = document.createElement('b');
  name.className = 'nc-name';
  name.textContent = def.name;
  const time = document.createElement('span');
  time.className = 'nc-time';
  const refresh = document.createElement('button');
  refresh.className = 'nc-refresh';
  refresh.type = 'button';
  refresh.title = '刷新本榜';
  refresh.textContent = '↻';
  refresh.onclick = () => newsFetchQueue([def.id], true);
  head.appendChild(dot);
  head.appendChild(name);
  head.appendChild(time);
  head.appendChild(refresh);

  const body = document.createElement('div');
  body.className = 'nc-body';
  const ol = document.createElement('ol');
  ol.className = 'nc-list ' + (def.type === 'hottest' ? 'nc-hot' : 'nc-time');
  body.appendChild(ol);

  sec.appendChild(head);
  sec.appendChild(body);
  sec._ol = ol;
  sec._time = time;
  sec._def = def;
  return sec;
}

/** 填充卡片内容；loading=true 显示占位，isError=true 显示失败重试 */
function fillNewsCard(card, items, updated, isError, loading) {
  const ol = card._ol;
  ol.innerHTML = '';
  if (loading) {
    const li = document.createElement('li');
    li.className = 'nc-empty';
    li.textContent = '加载中…';
    ol.appendChild(li);
  } else if (isError || !items || !items.length) {
    const li = document.createElement('li');
    li.className = 'nc-empty';
    li.textContent = isError ? '获取失败，点右上角 ↻ 重试' : '暂无数据';
    ol.appendChild(li);
  } else {
    items.slice(0, 20).forEach((it, i) => ol.appendChild(buildNewsItem(card._def, it, i)));
  }
  card._time.textContent = (!isError && !loading && updated) ? newsRelTime(updated * 1000) + '更新' : '';
}

function buildNewsItem(def, it, idx) {
  const li = document.createElement('li');
  const a = document.createElement('a');
  // 外部标题全部走 textContent 渲染（防 XSS）；链接协议白名单兜底
  const url = window.innerWidth < 768 && it.mobile_url ? it.mobile_url : it.url;
  if (safeUrl(url)) {
    a.href = url;
    a.target = '_blank';
    a.rel = 'noopener noreferrer';
  }

  if (def.type === 'hottest') {
    const rank = document.createElement('span');
    rank.className = 'nc-rank' + (idx < 3 ? ' top' : '');
    rank.textContent = String(idx + 1);
    a.appendChild(rank);
  }
  const title = document.createElement('span');
  title.className = 'nc-title';
  title.textContent = it.title || '';
  a.appendChild(title);
  if (def.type === 'realtime') {
    const when = document.createElement('span');
    when.className = 'nc-when';
    when.textContent = it.pub ? newsRelTime(it.pub * 1000) : '';
    a.appendChild(when);
  } else if (it.info) {
    const info = document.createElement('span');
    info.className = 'nc-info';
    info.textContent = it.info;
    a.appendChild(info);
  }
  li.appendChild(a);
  return li;
}

/** 并发上限 6 的拉取队列；latest=true 强制回源（卡片刷新按钮）
 *  后端对同源做了单飞（singleflight），这里的请求会与 all 的后台刷新共享同一次抓取 */
async function newsFetchQueue(ids, latest) {
  const queue = ids.slice();
  async function worker() {
    while (queue.length) {
      const id = queue.shift();
      const card = newsState.cards[id];
      if (!card) continue;
      card.classList.add('loading');
      try {
        const r = await API.get(API_BASE + 'news.php?action=source&id=' + encodeURIComponent(id) + (latest ? '&latest=1' : ''));
        const hasOld = newsState.list[id] && (newsState.list[id].items || []).length;
        if (r && r.items && r.items.length) {
          fillNewsCard(card, r.items, r.updated, false);
          newsState.list[id] = r;
        } else if (!hasOld) {
          fillNewsCard(card, [], 0, true);
        }
      } catch (e) {
        const hasOld = newsState.list[id] && (newsState.list[id].items || []).length;
        if (!hasOld) fillNewsCard(card, [], 0, true);
      } finally {
        card.classList.remove('loading');
      }
    }
  }
  await Promise.all([worker(), worker(), worker(), worker(), worker(), worker()]);
}

/** 时间戳（毫秒）→ 中文相对时间 */
function newsRelTime(ms) {
  if (!ms) return '';
  const diff = Date.now() - ms;
  if (diff < 60000) return '刚刚';
  const min = Math.floor(diff / 60000);
  if (min < 60) return min + ' 分钟前';
  const h = Math.floor(min / 60);
  if (h < 24) return h + ' 小时前';
  const d = new Date(ms);
  return (d.getMonth() + 1) + '月' + d.getDate() + '日';
}

/** 打开更新日志弹窗（只显示当前版本） */
function openChangelogModal() {
  const body = document.getElementById('changelogBody');
  body.innerHTML = '';
  // 有新版本时在弹窗顶部提示升级方式
  const upd = state.updateInfo;
  if (upd && upd.has_update) {
    const tip = document.createElement('div');
    tip.className = 'cl-update-tip';
    tip.textContent = '发现新版本 ' + upd.latest + '（当前 ' + upd.current + '）。Docker 升级：'
      + 'docker pull ovitor/solarpanel-v3:latest && docker restart solarpanel-v3';
    body.appendChild(tip);
  }
  const logs = typeof APP_CHANGELOG !== 'undefined' ? APP_CHANGELOG : [];
  const curVer = typeof APP_VERSION !== 'undefined' ? APP_VERSION : '';
  const entry = logs.find(v => v.ver === curVer);
  if (!entry) {
    body.innerHTML = '<div class="sh-empty">暂无更新日志</div>';
  } else {
    const header = document.createElement('div');
    header.className = 'cl-ver-header';
    const tag = document.createElement('span');
    tag.className = 'cl-ver-tag';
    tag.textContent = entry.ver;
    const date = document.createElement('span');
    date.className = 'cl-ver-date';
    date.textContent = entry.date || '';
    header.appendChild(tag);
    header.appendChild(date);
    body.appendChild(header);
    const ul = document.createElement('ul');
    ul.className = 'cl-ver-items';
    (entry.items || []).forEach(item => {
      const li = document.createElement('li');
      li.textContent = item;
      ul.appendChild(li);
    });
    body.appendChild(ul);
  }
  document.getElementById('changelogModal').classList.add('show');
}

/** 关闭更新日志弹窗 */
function closeChangelogModal() {
  document.getElementById('changelogModal').classList.remove('show');
}

/**
 * 升级提示：仅管理员可见。有新版本时顶栏出现徽章，点击打开更新日志弹窗。
 * 结果存 sessionStorage，同一会话只查一次；失败静默，不影响主页使用。
 */
async function initUpdateNotice() {
  const btn = document.getElementById('updateBtn');
  if (!btn || !state.user || state.user.role !== 'admin') return;

  let info = null;
  try {
    const raw = sessionStorage.getItem('sp_update_info');
    if (raw) {
      const c = JSON.parse(raw);
      if (c && Date.now() - (c.ts || 0) < 6 * 3600 * 1000) info = c.data;
    }
  } catch (e) { /* 隐私模式忽略 */ }

  if (!info) {
    try {
      info = await API.get(API_BASE + 'version.php');
      sessionStorage.setItem('sp_update_info', JSON.stringify({ ts: Date.now(), data: info }));
    } catch (e) {
      return;
    }
  }
  if (!info || !info.has_update) return;

  state.updateInfo = info;
  btn.hidden = false;
  btn.title = '发现新版本 ' + info.latest + '（当前 ' + info.current + '）';
  btn.onclick = openChangelogModal;
}

/* ---------- 访客访问锁屏（guest_required=true 时 boot 提前 return 调此函数） ---------- */
let __guestLockBound = false; // 防止 boot 重入时重复绑定 submit
function renderGuestLock() {
  const lock = document.getElementById('guestLock');
  if (!lock) return;

  // 显示全屏玻璃蒙版弹窗
  lock.hidden = false;
  // 隐藏 footer（锁屏状态下页脚不应可见）
  const footer = document.getElementById('footer');
  if (footer) footer.hidden = true;
  // 聚焦密码输入
  const pwd = document.getElementById('guestLockPwd');
  if (pwd) pwd.focus();

  // 只绑定一次 submit（boot 成功后会再次调用 renderGuestLock，需要幂等）
  if (__guestLockBound) return;
  __guestLockBound = true;

  const form = document.getElementById('guestLockForm');
  const errBox = document.getElementById('guestLockErr');
  const leftSpan = document.getElementById('guestLockLeft');
  const btn = document.getElementById('guestLockSubmit');

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (errBox) errBox.hidden = true;
    if (btn) { btn.disabled = true; btn.textContent = '验证中…'; }
    try {
      await API.post(API_BASE + 'auth.php?action=guest_login', { password: pwd.value });
      // 验证成功：隐藏锁屏并重新加载主页数据
      lock.hidden = true;
      __guestLockBound = false; // 允许下次需要时重新绑定
      boot();
    } catch (err) {
      // 显示错误 + 剩余次数（如果服务端返回的话）
      const msg = err.message || '密码错误';
      if (errBox) {
        errBox.hidden = false;
        errBox.textContent = msg;
        errBox.appendChild(document.createTextNode(' '));
        const remain = document.createElement('span');
        remain.textContent = '请重试';
        errBox.appendChild(remain);
      }
      pwd.select();
      if (btn) { btn.disabled = false; btn.textContent = '解锁访问'; }
    }
  });
}

/* ============ 键盘快捷键 ============ */
function spIsTyping() {
  const t = document.activeElement && document.activeElement.tagName;
  if (!t) return false;
  if (t === 'INPUT' || t === 'TEXTAREA' || t === 'SELECT') return true;
  if (document.activeElement.isContentEditable) return true;
  return false;
}
function spCloseTopModal() {
  // 依次尝试关：iframeModal > orderGuardModal > changelogModal > shortcutsModal
  for (const id of ['iframeModal', 'orderGuardModal', 'changelogModal', 'shortcutsModal']) {
    const m = document.getElementById(id);
    if (m && m.classList.contains('show')) { m.classList.remove('show'); return true; }
  }
  return false;
}
function bindShortcuts() {
  document.addEventListener('keydown', e => {
    // Esc：关弹窗 → 失焦
    if (e.key === 'Escape') {
      if (spCloseTopModal()) { e.preventDefault(); return; }
      const focused = document.activeElement;
      if (focused && (focused.tagName === 'INPUT' || focused.tagName === 'TEXTAREA')) focused.blur();
      return;
    }
    // 输入框内放行 Ctrl/Cmd+K（焦点搜索）
    if (e.key === 'k' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      document.getElementById('searchInput').focus();
      return;
    }
    if (spIsTyping()) return; // 其它所有单键在输入框内放行

    switch (e.key.toLowerCase()) {
      case '/':
        e.preventDefault();
        document.getElementById('searchInput').focus();
        break;
      case 't': updateThemeBtn && updateThemeBtn(); document.getElementById('themeBtn').click(); break;
      case 'n': switchView('news'); break;
      case 'b':
      case 'h': switchView('nav'); break;
      case 'g':
        document.getElementById('groupNav') && document.getElementById('gnToggle').click();
        break;
      case '?':
        e.preventDefault();
        document.getElementById('shortcutsModal').classList.add('show');
        break;
    }
  });
  // 快捷键帮助弹窗
  document.getElementById('shortcutsClose')?.addEventListener('click', () => document.getElementById('shortcutsModal').classList.remove('show'));
  document.getElementById('shortcutsClose2')?.addEventListener('click', () => document.getElementById('shortcutsModal').classList.remove('show'));
  document.getElementById('shortcutsModal')?.addEventListener('click', e => {
    if (e.target.id === 'shortcutsModal') e.target.classList.remove('show');
  });
  // 更新日志弹窗关闭
  document.getElementById('changelogClose')?.addEventListener('click', closeChangelogModal);
  document.getElementById('changelogModal')?.addEventListener('click', e => {
    if (e.target.id === 'changelogModal') closeChangelogModal();
  });
  // 顶栏快捷键按钮（nav 源码风格）
  const scBtn = document.getElementById('shortcutsBtn');
  if (scBtn) scBtn.onclick = () => document.getElementById('shortcutsModal').classList.add('show');
}

/* ============ 顶栏 PWA 安装按钮（Chrome/Edge 触发 beforeinstallprompt 时显示） ============ */
function bindPWAInstall() {
  const btn = document.getElementById('pwaInstallBtn');
  if (!btn) return;
  let deferred = null;
  window.addEventListener('beforeinstallprompt', e => {
    e.preventDefault();
    deferred = e;
    btn.hidden = false;
  });
  btn.addEventListener('click', async () => {
    if (!deferred) return;
    deferred.prompt();
    await deferred.userChoice;
    deferred = null;
    btn.hidden = true;
  });
  window.addEventListener('appinstalled', () => { deferred = null; btn.hidden = true; });
}

/* ============ P2: 每日壁纸（Bing） ============ */
/**
 * 每日壁纸：当日 sessionStorage 命中则同步应用，否则先用静态壁纸兜底再异步拉取。
 * 源地址由后端 wallpaper.php 代理抓取（Bing 等），前端只拿本地缓存 URL。
 */
async function applyDailyWallpaper(source, apply, fallback) {
  const KEY = 'sp_daily_wallpaper';
  const today = new Date().toISOString().slice(0, 10);
  let cached = null;
  try { cached = JSON.parse(sessionStorage.getItem(KEY)) || null; } catch (e) { cached = null; }
  // 缓存必须同时匹配日期与源：切换每日壁纸源后立即生效，而不是继续用当天旧源的图
  if (cached && cached.date === today && cached.source === source && cached.url) { apply(cached.url); return; }
  // 先用静态壁纸兜底，避免短暂空白
  apply(fallback || '');
  try {
    const r = await API.get(API_BASE + 'wallpaper.php?action=daily&source=' + encodeURIComponent(source));
    if (r && r.url) {
      apply(r.url);
      try { sessionStorage.setItem(KEY, JSON.stringify({ date: today, source: source, url: r.url })); } catch (e) {}
    }
  } catch (e) { /* 拉取失败保留静态壁纸 */ }
}

/* ================= 主页专用：主题化确认弹窗 + reloadPublic + quick-add 入口 ================= */

/** 主页动态注入 confirmModal（admin.html 已有，但主页 index.html 没写） */
function ensureConfirmModal() {
  if (document.getElementById('confirmModal')) return;
  const m = document.createElement('div');
  m.className = 'modal';
  m.id = 'confirmModal';
  m.innerHTML = `
    <div class="modal-box glass-strong" style="max-width:430px">
      <div class="modal-head">
        <h3 id="confirmTitle">确认操作</h3>
        <button class="btn btn-sm" type="button" id="confirmXBtn">✕</button>
      </div>
      <p id="confirmText" style="margin:0;color:var(--text-muted);font-size:13.5px;line-height:1.7;white-space:pre-line"></p>
      <div class="modal-foot">
        <button class="btn" type="button" id="confirmCancelBtn">取消</button>
        <button class="btn btn-primary" type="button" id="confirmOkBtn">确定</button>
      </div>
    </div>`;
  document.body.appendChild(m);
}

/** 主页版 uiConfirm — 与 admin.js 实现一致，复用同一个 confirmModal */
function uiConfirm(message, opts) {
  ensureConfirmModal();
  const m = document.getElementById('confirmModal');
  if (!m) return Promise.resolve(window.confirm(message));
  const o = opts || {};
  document.getElementById('confirmTitle').textContent = o.title || '确认操作';
  document.getElementById('confirmText').textContent = message;
  const ok = document.getElementById('confirmOkBtn');
  ok.textContent = o.okText || '确定';
  ok.className = 'btn ' + (o.danger ? 'btn-danger' : 'btn-primary');
  m.classList.add('show');
  return new Promise(resolve => {
    function done(v) {
      m.classList.remove('show');
      ok.onclick = cancel.onclick = x.onclick = null;
      m.removeEventListener('click', onBg);
      document.removeEventListener('keydown', onKey);
      resolve(v);
    }
    function onBg(e) { if (e.target === m) done(false); }
    function onKey(e) { if (e.key === 'Escape') done(false); }
    const cancel = document.getElementById('confirmCancelBtn');
    const x = document.getElementById('confirmXBtn');
    ok.onclick = () => done(true);
    cancel.onclick = () => done(false);
    x.onclick = () => done(false);
    m.addEventListener('click', onBg);
    document.addEventListener('keydown', onKey);
  });
}

/** 重新拉 public.php 并全量重绘 — 新增/删除卡片后调用 */
async function reloadPublic() {
  try {
    const data = await API.get(API_BASE + 'public.php?_t=' + Date.now());
    state.settings = data.settings || {};
    state.groups = data.groups || [];
    // 重建父子树
    state.topGroups = [];
    state.childrenMap = {};
    state.groups.forEach(g => {
      g.parent_id = g.parent_id || 0;
      if (g.parent_id === 0) state.topGroups.push(g);
      else (state.childrenMap[g.parent_id] = state.childrenMap[g.parent_id] || []).push(g);
    });
    // 重建全部
    const container = document.getElementById('groupsWrap');
    if (container) container.innerHTML = '';
    state.groupIntroDone = true;  // 不再播放首屏动画
    state.cardsIntroDone = true;
    renderGroups();
    updateNewsBtnVisible();       // 后台改 news_show 后按钮显隐同步更新
    initNASPanel();               // 后台改 NAS 开关 / 放置方式后同步重建面板
  } catch (e) { toast('刷新失败：' + e.message, 'error'); }
}

/** 主页快速添加/编辑卡片弹窗入口 */
function openQuickAddCard(groupId, item) {
  ensureQuickAddModal();
  const m = document.getElementById('quickAddModal');
  const isEdit = !!item;
  m.dataset.groupId = String(groupId);

  // 先 reset——必须在填分组选项之前，否则 form.reset() 会清掉动态设置的 selected
  document.getElementById('qaForm').reset();

  // 分组选项：与后台完全一致——先列全部一级，再列全部二级（└─ 一级 › 二级）
  const groupSel = document.getElementById('qa_group');
  groupSel.innerHTML = '';
  const groups = state.groups || [];
  const parentMap = {};
  groups.forEach(g => { if ((g.parent_id || 0) === 0) parentMap[g.id] = g.title; });
  const tops = groups.filter(g => (g.parent_id || 0) === 0);
  const children = groups.filter(g => (g.parent_id || 0) !== 0);
  let matched = false;
  [...tops, ...children].forEach(g => {
    const opt = document.createElement('option');
    opt.value = String(g.id);
    const pid = g.parent_id || 0;
    opt.textContent = pid === 0 ? g.title : '└─ ' + (parentMap[pid] || '?') + ' › ' + g.title;
    const matchGid = isEdit ? item.group_id : groupId;
    if (String(g.id) === String(matchGid)) { opt.selected = true; matched = true; }
    groupSel.appendChild(opt);
  });
  // 兜底：没匹配到当前分组时默认选第一个
  if (!matched && groups.length) groupSel.value = String(groups[0].id);

  // 标题 + 按钮文字（与后台编辑弹窗保持一致）
  const h3 = m.querySelector('.modal-head h3');
  const saveBtn = document.getElementById('qaSaveBtn');
  h3.textContent = isEdit ? '编辑卡片' : '添加卡片';
  saveBtn.textContent = '保存';

  document.getElementById('qa_group_id').value = isEdit ? item.id : 0;
  document.getElementById('qa_hidden').checked = !!(isEdit && item.is_hidden);
  document.getElementById('qa_title').value = isEdit ? (item.title || '') : '';
  document.getElementById('qa_url').value = isEdit ? (item.url || '') : '';
  document.getElementById('qa_lan').value = isEdit ? (item.lan_url || '') : '';
  document.getElementById('qa_desc').value = isEdit ? (item.description || '') : '';
  document.getElementById('qa_icon').value = '';
  document.getElementById('qa_iconText').value = '';
  document.getElementById('qa_open').value = isEdit ? String(item.open_method || 2) : '2';

  // 图标类型回填
  const itype = isEdit ? (item.icon_type || 'favicon') : 'favicon';
  document.querySelector('#qa_itype input[value="' + itype + '"]').checked = true;

  // 图标值回填
  document.getElementById('qa_icon').value = isEdit && item.icon_type === 'image' ? (item.icon_value || '') : '';

  // 文字图标回填
  if (isEdit && item.icon_type === 'text' && item.icon_value) {
    document.getElementById('qa_iconText').value = item.icon_value;
    window.__qaIconTextManual = true;
  } else {
    document.getElementById('qa_iconText').value = '';
    window.__qaIconTextManual = false;
    if (isEdit && item.title) document.getElementById('qa_iconText').value = smartIconText(item.title);
  }

  // 背景色回填
  if (isEdit && item.icon_bg) {
    document.getElementById('qa_bg').value = item.icon_bg;
    syncQAColorPanel('init');
  } else {
    document.getElementById('qa_bg').value = buildBgCss('#0969da', 0);
    document.getElementById('qa_color').value = '#0969da';
    document.getElementById('qa_alpha').value = '0';
    document.getElementById('qa_alphaLabel').textContent = '0%';
  }

  window.__qaMetaIcon = '';
  onQAIconTypeChange();
  m.classList.add('show');

  // 新增模式：有 URL 时自动抓 meta
  if (!isEdit) {
    setTimeout(() => {
      const urlInput = document.getElementById('qa_url');
      if (urlInput.value.trim()) fetchQAMeta();
    }, 100);
  }
}

function ensureQuickAddModal() {
  if (document.getElementById('quickAddModal')) return;
  const m = document.createElement('div');
  m.className = 'modal';
  m.id = 'quickAddModal';
  m.innerHTML = `
    <div class="modal-box glass-strong quick-add">
      <div class="modal-head">
        <h3>添加卡片</h3>
        <button class="btn btn-sm" type="button" id="qaCloseX">✕</button>
      </div>
      <form id="qaForm" autocomplete="off">
        <input type="hidden" id="qa_group_id">
        <div class="form-row">
          <div class="form-item">
            <label>所属分组</label>
            <select class="select" id="qa_group"></select>
          </div>
          <div class="form-item">
            <label>打开方式</label>
            <select class="select" id="qa_open">
              <option value="2">新窗口打开</option>
              <option value="1">当前页打开</option>
              <option value="3">弹层内嵌打开</option>
            </select>
          </div>
        </div>
        <div class="form-row" style="align-items:center;margin-top:12px">
          <div class="form-item" style="display:flex;align-items:center">
            <label style="display:flex;align-items:center;gap:8px;margin-bottom:0;cursor:pointer">
              <input type="checkbox" id="qa_hidden" value="1">
              <span>设为隐藏（访客和只读用户不可见）</span>
            </label>
          </div>
          <div class="form-item" style="flex:0 0 auto;display:flex;align-items:center">
            <button class="btn btn-sm" type="button" id="qa_fetchMeta">自动获取信息</button>
          </div>
        </div>
        <div class="form-item">
          <label>标题</label>
          <input class="input" id="qa_title" type="text" maxlength="50" required>
        </div>
        <div class="form-item">
          <label>地址</label>
          <input class="input" id="qa_url" type="text" placeholder="https://…">
        </div>
        <div class="form-item">
          <label>内网地址（可选，用于主页「内网模式」；失焦或回车自动获取）</label>
          <input class="input" id="qa_lan" type="text" placeholder="http://192.168.x.x:port">
        </div>
        <div class="form-item">
          <label>描述（可选）</label>
          <input class="input" id="qa_desc" type="text" maxlength="1000">
        </div>
        <div class="form-item">
          <label>图标类型</label>
          <div class="radio-group" id="qa_itype">
            <label><input type="radio" name="qaitype" value="favicon" checked> 自动获取站点图标</label>
            <label><input type="radio" name="qaitype" value="image"> 图片地址 / 上传</label>
            <label><input type="radio" name="qaitype" value="text"> 文字图标</label>
          </div>
        </div>
        <div class="form-item">
          <label>图标</label>
          <div class="upload-row">
            <span class="icon-preview" id="qa_preview"></span>
            <input class="input" id="qa_icon" type="text" placeholder="图标图片地址（选择「图片」类型时使用）">
            <button class="btn" type="button" id="qa_uploadIcon">上传图标</button>
            <button class="btn" type="button" id="qa_useFavicon" hidden>获取站点图标</button>
          </div>
          <div class="form-item" style="margin-top:10px" id="qa_textWrap">
            <label>手动文字（留空自动取卡片标题）</label>
            <input class="input" id="qa_iconText" type="text" maxlength="4" placeholder="支持中文 4 字 / 英文 2 个字母">
          </div>
          <div class="form-item" style="margin-top:10px" id="qa_bgWrap">
            <label>图标背景色</label>
            <div class="upload-row" style="gap:8px;flex-wrap:nowrap">
              <input type="color" id="qa_color" value="#0969da" style="width:44px;height:34px;padding:2px;border-radius:6px;cursor:pointer">
              <span style="font-size:12px;color:var(--text-muted);white-space:nowrap">透明度</span>
              <input type="range" id="qa_alpha" min="0" max="100" value="0" style="flex:1">
              <span id="qa_alphaLabel" style="font-size:12px;color:var(--text-muted);min-width:32px;text-align:right">0%</span>
              <input type="hidden" id="qa_bg" value="">
            </div>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn" type="button" id="qaCancel">取消</button>
          <button class="btn btn-primary" type="submit" id="qaSaveBtn">添加卡片</button>
        </div>
      </form>
    </div>`;
  document.body.appendChild(m);

  // 关闭绑定：表单类弹窗禁用点击空白关闭（防止移动端滑动误触）
  document.getElementById('qaCloseX').onclick = () => m.classList.remove('show');
  document.getElementById('qaCancel').onclick = () => m.classList.remove('show');

  // 图标类型切换
  document.querySelectorAll('#qa_itype input').forEach(r => {
    r.addEventListener('change', onQAIconTypeChange);
  });

  // 实时预览
  document.getElementById('qa_title').addEventListener('input', () => {
    if (!window.__qaIconTextManual) {
      document.getElementById('qa_iconText').value = smartIconText(document.getElementById('qa_title').value);
    }
    updateQAIconPreview();
  });
  document.getElementById('qa_iconText').addEventListener('input', () => {
    window.__qaIconTextManual = true;
    updateQAIconPreview();
  });
  document.getElementById('qa_icon').addEventListener('input', updateQAIconPreview);
  document.getElementById('qa_url').addEventListener('input', () => {
    window.__qaMetaIcon = ''; // 地址变了，之前抓到的图标不再对应
    updateQAIconPreview();
  });

  // 颜色联动
  document.getElementById('qa_color').addEventListener('input', syncQAColorPanel);
  document.getElementById('qa_alpha').addEventListener('input', syncQAColorPanel);

  // fetch_meta
  document.getElementById('qa_fetchMeta').addEventListener('click', () => {
    const mainEl = document.getElementById('qa_url');
    const lanEl = document.getElementById('qa_lan');
    const main = normalizeFetchUrl(mainEl.value);
    const lan = normalizeFetchUrl(lanEl.value);
    if (main && mainEl.value.trim() !== main) mainEl.value = main;
    if (lan && lanEl.value.trim() !== lan) lanEl.value = lan;
    fetchQAMeta(false, main || lan);
  });
  // 地址失焦 / 粘贴完成后，若标题为空则自动获取
  document.getElementById('qa_url').addEventListener('change', () => fetchQAMeta(true));
  document.getElementById('qa_url').addEventListener('paste', () => setTimeout(() => fetchQAMeta(true), 30));
  // 内网地址同样支持自动获取：输入完整地址后失焦 / 回车 / 点「自动获取信息」均可
  // 使用内网地址抓取时不受标题是否已有内容限制，便于先填内网地址的场景
  let qaLastLanUrl = '';
  const qaFetchLanMeta = () => {
    const el = document.getElementById('qa_lan');
    const lan = normalizeFetchUrl(el.value);
    if (!isFetchableUrl(lan)) return;
    if (el.value.trim() !== lan) el.value = lan; // 补全 http:// 后回填
    if (lan === qaLastLanUrl) return;
    qaLastLanUrl = lan;
    fetchQAMeta(false, lan);
  };
  document.getElementById('qa_lan').addEventListener('change', qaFetchLanMeta);
  document.getElementById('qa_lan').addEventListener('paste', () => setTimeout(qaFetchLanMeta, 30));
  document.getElementById('qa_lan').addEventListener('keydown', (e) => {
    if (e.key !== 'Enter') return;
    e.preventDefault();
    qaFetchLanMeta();
  });
  document.getElementById('qa_url').addEventListener('keydown', (e) => {
    if (e.key !== 'Enter') return;
    e.preventDefault();
    const url = document.getElementById('qa_url').value.trim();
    if (isFetchableUrl(url)) fetchQAMeta(false, url);
  });

  // 提交
  document.getElementById('qaForm').addEventListener('submit', async e => {
    e.preventDefault();
    const btn = document.getElementById('qaSaveBtn');
    btn.disabled = true;
    try {
      const itype = document.querySelector('#qa_itype input:checked').value;
      let iconValue = '';
      switch (itype) {
        case 'image':   iconValue = document.getElementById('qa_icon').value.trim(); break;
        case 'favicon': iconValue = ''; break;
        case 'text':
          iconValue = document.getElementById('qa_iconText').value.trim();
          if (isImageUrlLike(iconValue)) iconValue = '';
          break;
      }
      syncQAColorPanel();
      const id = Number(document.getElementById('qa_group_id').value) || 0;
      await API.post(API_BASE + 'items.php?action=edit', {
        id,
        group_id: Number(document.getElementById('qa_group').value) || 0,
        open_method: Number(document.getElementById('qa_open').value),
        is_hidden: document.getElementById('qa_hidden').checked ? 1 : 0,
        title: document.getElementById('qa_title').value.trim(),
        url: normalizeFetchUrl(document.getElementById('qa_url').value),
        lan_url: normalizeFetchUrl(document.getElementById('qa_lan').value),
        description: document.getElementById('qa_desc').value.trim(),
        icon_type: itype,
        icon_value: iconValue,
        icon_bg: document.getElementById('qa_bg').value.trim(),
      });
      m.classList.remove('show');
      toast(id ? '已保存' : '已添加', 'success');
      await reloadPublic();
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      btn.disabled = false;
    }
  });

  // 上传图标按钮
  const uploadBtn = document.getElementById('qa_uploadIcon');
  const fileInput = document.createElement('input');
  fileInput.type = 'file';
  fileInput.accept = 'image/*';
  fileInput.style.display = 'none';
  uploadBtn.parentNode.appendChild(fileInput);
  uploadBtn.addEventListener('click', () => fileInput.click());
  fileInput.addEventListener('change', async () => {
    const file = fileInput.files[0];
    if (!file) return;
    const form = new FormData();
    form.append('file', file);
    form.append('type', 'icon');
    uploadBtn.disabled = true;
    try {
      const res = await API.postForm(API_BASE + 'upload.php', form);
      document.getElementById('qa_icon').value = res.url;
      // 自动切到图片类型
      const imgRadio = document.querySelector('#qa_itype input[value="image"]');
      if (imgRadio && !imgRadio.checked) {
        imgRadio.checked = true;
        onQAIconTypeChange();
      }
      updateQAIconPreview();
      toast('图标上传成功', 'success');
    } catch (e) {
      toast('上传失败：' + e.message, 'error');
    } finally {
      uploadBtn.disabled = false;
      fileInput.value = '';
    }
  });

  // 获取站点图标按钮（仅 image 类型显示）
  document.getElementById('qa_useFavicon').addEventListener('click', fetchQAFavicon);
}

/** 图标类型切换 → 控制显隐 */
function onQAIconTypeChange() {
  const type = document.querySelector('#qa_itype input:checked').value;
  document.getElementById('qa_textWrap').style.display = type === 'text' ? '' : 'none';
  document.getElementById('qa_bgWrap').style.display = (type === 'text' || type === 'image') ? '' : 'none';
  document.getElementById('qa_useFavicon').hidden = type !== 'image';
  updateQAIconPreview();
}

/** quick-add 图标实时预览 */
function updateQAIconPreview() {
  const type = document.querySelector('#qa_itype input:checked').value;
  const icon = document.getElementById('qa_icon').value.trim();
  const title = document.getElementById('qa_title').value.trim();
  const url = document.getElementById('qa_url').value.trim();
  const bg = document.getElementById('qa_bg').value.trim();
  const preview = document.getElementById('qa_preview');

  if (type === 'image') {
    preview.style.background = bg || '#0969da';
    preview.innerHTML = icon ? '<img src="' + esc(assetUrl(icon)) + '" alt="" onerror="this.parentElement.innerHTML=\'<span>🖼</span>\'">' : '<span>🖼</span>';
  } else if (type === 'favicon') {
    preview.style.background = '';
    const metaIcon = (window.__qaMetaIcon || '').trim();
    const urls = metaIcon ? [metaIcon] : faviconSources(url);
    if (urls.length) {
      // 用 data-srcs 存候选源，onerror 调 window.__qaFavFallback(idx)
      const key = '__qaFav_' + Date.now() + '_' + Math.random().toString(36).slice(2, 6);
      window[key] = urls;
      preview.innerHTML = '<img src="' + esc(urls[0]) + '" alt="" referrerpolicy="no-referrer" onerror="window.__qaFavFallback(this,\'' + key + '\',1)">';
    } else {
      preview.innerHTML = '<span>?</span>';
    }
  } else {
    // 文字型：手动输入最多 2 个字符，否则取标题（英文只取大写首字母）
    preview.style.background = bg || '#0969da';
    const typed = document.getElementById('qa_iconText').value.trim();
    const raw = typed || (title || '?');
    renderTextIcon(preview, smartIconText(raw, typed ? 2 : 1, !typed), 18);
  }
}

/** quick-add 颜色面板联动 */
function syncQAColorPanel(source) {
  const c = document.getElementById('qa_color');
  const a = document.getElementById('qa_alpha');
  const aLbl = document.getElementById('qa_alphaLabel');
  const bgText = document.getElementById('qa_bg');

  if (source === 'init') {
    const parsed = parseBgColor(bgText.value);
    if (parsed) {
      c.value = parsed.hex;
      a.value = parsed.alpha;
      aLbl.textContent = parsed.alpha + '%';
    }
    updateQAIconPreview();
    return;
  }
  const alphaVal = parseInt(a.value);
  aLbl.textContent = alphaVal + '%';
  bgText.value = buildBgCss(c.value, alphaVal);
  updateQAIconPreview();
}

/** 解析存储的 icon_bg 值，返回 {hex, alpha}
 *  支持两种格式：hex（#0969da）和 rgba(r,g,b,a)
 */
function parseBgColor(bg) {
  if (!bg) return null;
  bg = bg.trim();
  const m = bg.match(/^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*(?:,\s*([\d.]+))?\s*\)$/);
  if (m) {
    const r = parseInt(m[1]), g = parseInt(m[2]), b = parseInt(m[3]);
    const a = m[4] !== undefined ? parseFloat(m[4]) : 1;
    const hex = '#' + [r, g, b].map(x => x.toString(16).padStart(2, '0')).join('');
    return { hex, alpha: Math.round(a * 100) };
  }
  if (bg.startsWith('#')) {
    let h = bg.slice(1);
    if (h.length === 3) h = h.split('').map(c => c + c).join('');
    if (h.length === 6) return { hex: '#' + h, alpha: 100 };
  }
  return null;
}

/** 补全协议：只输入域名 / IP 时默认补 http://，已带协议或 // 开头则原样返回 */
function normalizeFetchUrl(s) {
  s = String(s || '').trim();
  if (!s) return '';
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) return s;
  if (s.startsWith('//')) return 'http:' + s;
  return 'http://' + s;
}

/** 是否为可抓取的完整 http(s) 地址 */
function isFetchableUrl(s) {
  try {
    const u = new URL(s);
    return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.hostname;
  } catch (_) {
    return false;
  }
}

/** fetch_meta — 自动填充 title/description；favicon 无图标时交给站点图标代理 */
let qaLastMetaUrl = ''; // 自动模式去重：同一地址只抓一次
let qaMetaBusy = false;
async function fetchQAMeta(auto, overrideUrl) {
  auto = auto === true;
  if (qaMetaBusy) return;
  const url = normalizeFetchUrl(overrideUrl || document.getElementById('qa_url').value || '');
  if (!overrideUrl) {
    const el = document.getElementById('qa_url');
    if (el.value.trim() && el.value.trim() !== url) el.value = url; // 补全协议后回填
  }
  if (!url || !isFetchableUrl(url)) {
    if (!auto) toast('请填写有效地址（可只输入域名，会自动补全 http://）', 'error');
    return;
  }
  // 自动模式：标题已有内容或该地址已抓过 → 跳过，避免失焦/粘贴重复请求
  if (auto && (!document.getElementById('qa_title').value.trim() || url === qaLastMetaUrl)) return;
  const btn = document.getElementById('qa_fetchMeta');
  qaMetaBusy = true;
  btn.disabled = true; btn.textContent = '获取中…';
  try {
    const res = await API.get(API_BASE + 'items.php?action=fetch_meta&url=' + encodeURIComponent(url));
    qaLastMetaUrl = url;

    const got = [];
    const emptyTitle = !document.getElementById('qa_title').value.trim();
    const emptyDesc  = !document.getElementById('qa_desc').value.trim();
    const checked = document.querySelector('#qa_itype input:checked');
    const curType = checked ? checked.value : 'favicon';

    if (res.title && emptyTitle) { document.getElementById('qa_title').value = res.title; got.push('标题'); }
    if (res.description && emptyDesc) { document.getElementById('qa_desc').value = res.description; got.push('描述'); }

    // 图标处理：与 admin.js 对齐的三态分支
    if (curType === 'favicon') {
      if (res.icon && !res.fallback_icon) {
        window.__qaMetaIcon = res.icon; // 本地缓存图标，预览直接使用
        got.push('图标');
      } else if (res.fallback_icon) {
        // 服务端没抓到图标时保留 favicon 模式，由 /api/favicon.php 代理兜底
        window.__qaMetaIcon = '';
        got.push('图标(站点图标代理)');
      } else if (!res.icon) {
        // 确实没有可用图标 → 自动降级为文字图标
        document.querySelector('#qa_itype input[value="text"]').checked = true;
        document.getElementById('qa_iconText').value = smartIconText(res.title || url);
        onQAIconTypeChange();
        got.push('图标(自动降级为文字)');
      }
    }
    updateQAIconPreview();

    const warnTxt = res.warn ? '（' + res.warn + '）' : '';
    if (!auto) {
      if (got.length) {
        toast('已自动获取：' + got.join('、') + (res.cached ? '（缓存）' : '') + warnTxt, res.warn ? 'info' : 'success');
      } else if (res.title || res.description) {
        toast('标题/描述已有内容，已保留原值；如需更新请先清空对应输入框' + warnTxt, 'info');
      } else {
        toast('未获取到可填充的字段' + (warnTxt || '（站点无信息）'), 'info');
      }
    }
  } catch (err) {
    if (!auto) toast(err.message, 'error');
  } finally {
    qaMetaBusy = false;
    btn.disabled = false; btn.textContent = '自动获取信息';
  }
}

/** 获取站点图标（items.php?action=favicon）—— 写入 qa_icon 并自动切到 image 类型 */
async function fetchQAFavicon() {
  const url = normalizeFetchUrl(document.getElementById('qa_url').value || document.getElementById('qa_lan').value);
  if (!url) { toast('请先填写卡片地址或内网地址', 'error'); return; }
  const btn = document.getElementById('qa_useFavicon');
  btn.disabled = true; btn.textContent = '获取中…';
  try {
    const res = await API.get(API_BASE + 'items.php?action=favicon&url=' + encodeURIComponent(url));
    if (res.fallback) {
      // 服务器拿不到可缓存图标 → 保留 favicon 模式，由站点图标代理兜底
      document.querySelector('#qa_itype input[value="favicon"]').checked = true;
      window.__qaMetaIcon = '';
      onQAIconTypeChange();
      toast('服务器未能缓存图标，已改用站点图标代理加载', 'info');
      return;
    }
    document.querySelector('#qa_itype input[value="image"]').checked = true;
    document.getElementById('qa_icon').value = res.url;
    onQAIconTypeChange();
    updateQAIconPreview();
    toast(res.cached ? '已获取（本地缓存）' : '获取成功', 'success');
  } catch (e) {
    toast(e.message, 'error');
  } finally {
    btn.disabled = false; btn.textContent = '获取站点图标';
  }
}

/** favicon onerror 回退函数 —— 挂在 window 上让 innerHTML 里的 onerror 属性能调到 */
window.__qaFavFallback = function(imgEl, key, nextIdx) {
  const arr = window[key];
  if (!arr) { imgEl.parentElement.innerHTML = '<span>🌐</span>'; return; }
  if (nextIdx < arr.length) {
    imgEl.src = arr[nextIdx];
  } else {
    imgEl.parentElement.innerHTML = '<span>🌐</span>';
    try { delete window[key]; } catch(e) { window[key] = null; }
  }
};

/* ================= NAS 运行状态面板（右侧 / 左侧 / 双侧悬浮，可折叠） ================= */
const nasState = { timer: null, inflight: false, refresh: 3, fail: 0, histRx: [], histTx: [], histCpu: [], snap: null, layout: 'right' };

function nasPanels() {
  return [document.getElementById('nasPanelLeft'), document.getElementById('nasPanel')];
}

function nasBannerEl() {
  return document.getElementById('nasBanner');
}

// 任一展示形态可见即继续轮询（悬浮面板 / 底部状态条）
function nasAnyVisible() {
  const banner = nasBannerEl();
  if (banner && !banner.hidden) return true;
  return nasPanels().some(p => p && !p.hidden);
}

function initNASPanel() {
  const [left, right] = nasPanels();
  if (!left || !right) return;
  stopNASPanel();

  const s = state.settings || {};
  if (s.nas_show !== '1') return;

  // 访客可见性：未登录且未开启访客查看时不发请求（避免无效 403 轮询）
  const isMember = state.user && state.user.role && state.user.role !== 'guest';
  if (!isMember && s.nas_guest !== '1') return;

  // 移动端只支持「卡片下方状态条」：悬浮面板会遮挡卡片，统一自动降级
  const layout = desiredNASLayout(s);
  nasState.layout = layout;
  const banner = nasBannerEl();

  if (layout === 'banner') {
    left.hidden = true;
    right.hidden = true;
    if (banner) {
      banner.hidden = false;
      banner.onclick = openNASDetail;
    }
  } else {
    if (banner) banner.hidden = true;
    left.hidden = layout === 'right';
    right.hidden = layout === 'left';
    document.body.classList.add('nas-' + layout);
  }

  // 折叠状态：沿用上次选择；首次访问按视口宽度决定（窄屏先收起，避免遮挡卡片）
  let stored = null;
  try { stored = localStorage.getItem('sp_nas_collapsed'); } catch (e) { /* 隐私模式忽略 */ }
  const threshold = layout === 'both' ? 1900 : 1600;
  setNASCollapsed(stored === null ? window.innerWidth < threshold : stored === '1', false);

  nasPanels().forEach(p => {
    if (!p || p.hidden) return;
    p.onclick = (ev) => {
      if (ev.target.closest('[data-np-detail]')) { openNASDetail(); return; }
      if (ev.target.closest('[data-np-fold]') || p.classList.contains('collapsed')) {
        setNASCollapsed(!p.classList.contains('collapsed'), true);
      }
    };
  });

  nasState.histRx = [];
  nasState.histTx = [];
  nasState.histCpu = [];
  nasState.fail = 0;
  nasState.snap = null;
  refreshNAS();
}

function stopNASPanel() {
  if (nasState.timer) { clearTimeout(nasState.timer); nasState.timer = null; }
  nasPanels().forEach(p => {
    if (!p) return;
    p.hidden = true;
    p.innerHTML = '';
    p.onclick = null;
  });
  const banner = nasBannerEl();
  if (banner) {
    banner.hidden = true;
    banner.innerHTML = '';
    banner.onclick = null;
  }
  document.body.classList.remove('nas-left', 'nas-right', 'nas-both');
}

// 实际生效的布局：≤768px 一律使用状态条，悬浮面板不生效
function desiredNASLayout(s) {
  if (window.innerWidth <= 768) return 'banner';
  return ['left', 'both', 'banner'].includes(s.nas_layout) ? s.nas_layout : 'right';
}

// 窗口跨断点（如旋转屏幕、拉伸窗口）时重建面板形态
let nasResizeTimer = null;
window.addEventListener('resize', () => {
  const s = state.settings || {};
  if (s.nas_show !== '1') return;
  const isMember = state.user && state.user.role && state.user.role !== 'guest';
  if (!isMember && s.nas_guest !== '1') return;
  if (desiredNASLayout(s) === nasState.layout) return;
  clearTimeout(nasResizeTimer);
  nasResizeTimer = setTimeout(() => initNASPanel(), 300);
});

function setNASCollapsed(v, persist) {
  nasPanels().forEach(p => { if (p) p.classList.toggle('collapsed', v); });
  if (persist) {
    try { localStorage.setItem('sp_nas_collapsed', v ? '1' : '0'); } catch (e) { /* 忽略 */ }
  }
}

// 失败后指数退避（3s → 6s → 12s … 最多 60s），避免限流/离线时持续打接口
function nasNextDelay() {
  const backoff = Math.min(5, nasState.fail || 0);
  return Math.min(60, Math.max(2, nasState.refresh) * Math.pow(2, backoff));
}

function scheduleNAS(delaySec) {
  if (nasState.timer) { clearTimeout(nasState.timer); nasState.timer = null; }
  nasState.timer = setTimeout(() => {
    nasState.timer = null;
    if (document.hidden) { scheduleNAS(nasState.refresh); return; }
    refreshNAS();
  }, Math.max(2, delaySec) * 1000);
}

async function refreshNAS() {
  if (nasState.inflight) return;
  nasState.inflight = true;
  try {
    const data = await API.get(API_BASE + 'nas.php?_t=' + Date.now());
    if (!data || !data.enabled) { stopNASPanel(); return; }
    nasState.refresh = Math.min(60, Math.max(2, parseInt(data.refresh, 10) || 3));
    nasState.snap = data.snapshot || {};
    nasState.fail = 0;
    renderNAS();
    renderNASDetailIfOpen();
  } catch (e) {
    const msg = e.message || '采集失败';
    // 未开启访客可见：直接收起，不做无效轮询
    if (msg.indexOf('访客') >= 0) { stopNASPanel(); return; }
    nasState.fail = (nasState.fail || 0) + 1;
    renderNASError(msg);
    renderNASDetailIfOpen();
  } finally {
    nasState.inflight = false;
    if (nasAnyVisible()) scheduleNAS(nasNextDelay());
  }
}

function nasFmtBytes(b) {
  b = Number(b) || 0;
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  while (b >= 1024 && i < units.length - 1) { b /= 1024; i++; }
  return (b >= 100 || i === 0 ? b.toFixed(0) : b.toFixed(1)) + ' ' + units[i];
}

function nasFmtRate(bps) {
  const v = Number(bps) || 0;
  if (v >= 1024 * 1024) return (v / 1024 / 1024).toFixed(1) + ' MB/s';
  if (v >= 1024) return (v / 1024).toFixed(0) + ' KB/s';
  return v.toFixed(0) + ' B/s';
}

function nasFmtUptime(sec) {
  sec = Number(sec) || 0;
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  if (d > 0) return d + ' 天 ' + h + ' 小时';
  const m = Math.floor((sec % 3600) / 60);
  return h > 0 ? h + ' 小时 ' + m + ' 分' : m + ' 分';
}

// 网络曲线固定对数刻度：1 B/s ~ 10 GB/s，微小流量也能看出上下波动
const NAS_NET_MIN = 1;
const NAS_NET_MAX = 10 * 1024 * 1024 * 1024;

function nasSparkPath(arr, w, h, forcedMax, logScale) {
  if (!arr.length) return '';
  const linearMax = forcedMax || Math.max.apply(null, arr.concat([1]));
  const step = arr.length > 1 ? w / (arr.length - 1) : w;
  const yOf = logScale
    ? (v => {
        const t = Math.log10(Math.max(NAS_NET_MIN, Math.min(NAS_NET_MAX, v))) / Math.log10(NAS_NET_MAX);
        return (h - 1 - t * (h - 2)).toFixed(1);
      })
    : (v => (h - 1 - (v / linearMax) * (h - 2)).toFixed(1));
  // 只有一个采样点时画一条水平线，避免首帧看起来「没有曲线」
  if (arr.length === 1) {
    const y = yOf(arr[0]);
    return `M0,${y} L${w},${y}`;
  }
  return arr.map((v, i) => {
    const x = (i * step).toFixed(1);
    return (i ? 'L' : 'M') + x + ',' + yOf(v);
  }).join(' ');
}

function nasBarClass(pct) {
  return pct >= 90 ? 'danger' : pct >= 75 ? 'warn' : '';
}

// 温度：传感器不可用时区分「虚拟机」与「无传感器」，避免只显示 -- 造成误解
function nasTempText(h) {
  if (h && h.temp_c > 0) return Math.round(h.temp_c) + '°C';
  return h && h.virtualized ? '虚拟机' : '--';
}

// 卡片下方单行状态条：一眼看全关键指标，点任意位置看详情
function nasBannerHTML(snap) {
  const h = snap.host || {};
  const net = snap.net || {};
  const cts = snap.containers || [];
  const running = cts.filter(c => c.state === 'running').length;
  const cpu = Math.round(h.cpu_pct || 0);
  const memPct = h.mem_total ? Math.round((h.mem_used / h.mem_total) * 100) : 0;
  const disks = snap.disks || [];
  const diskTotal = disks.reduce((s, d) => s + d.total, 0);
  const diskUsed = disks.reduce((s, d) => s + d.used, 0);
  const diskPct = diskTotal ? Math.round((diskUsed / diskTotal) * 100) : 0;
  const diskFree = Math.max(0, diskTotal - diskUsed);
  const cpuLine = nasSparkPath(nasState.histCpu, 92, 24, 100);
  const cpuArea = cpuLine ? cpuLine + ' L92,24 L0,24 Z' : '';

  return `
  <div class="nb-host">🖥️ <b>${esc(h.hostname || 'NAS')}</b><span>${esc(h.os_name || 'Linux')} · ${h.cores || 0} 核${h.ip ? ' · ' + esc(h.ip) : ''}</span></div>
  <div class="nb-item"><span>CPU</span><b>${cpu}%</b></div>
  <svg class="nb-spark" viewBox="0 0 92 24" preserveAspectRatio="none" aria-hidden="true">
    <path d="${cpuArea}" fill="var(--accent-soft)" stroke="none"/>
    <path d="${cpuLine}" fill="none" stroke="var(--accent)" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round"/>
  </svg>
  <div class="nb-item"><span>内存</span><b>${memPct}%</b><span class="m">${nasFmtBytes(h.mem_used)} / ${nasFmtBytes(h.mem_total)}</span></div>
  <div class="nb-item"><span>磁盘</span><b>${diskPct}%</b><span class="m">${diskFree ? nasFmtBytes(diskFree) + ' 可用' : '--'}</span></div>
  <div class="nb-item"><span>网络</span><b><span class="v-down">↓${nasFmtRate(net.rx_bps)}</span> <span class="v-up">↑${nasFmtRate(net.tx_bps)}</span></b></div>
  <div class="nb-item"><span>温度</span><b>${nasTempText(h)}</b></div>
  <div class="nb-item"><span>容器</span><b>${running}</b><span class="m">/ ${cts.length}</span></div>
  <button class="nb-more" type="button">查看详情 →</button>`;
}

// mode: full = 主机 + 容器（单边面板）；host = 仅主机；ctr = 仅容器（双侧模式）
function nasHTML(snap, mode) {
  const h = snap.host || {};
  const net = snap.net || {};
  const cts = snap.containers || [];
  const running = cts.filter(c => c.state === 'running');
  const cpu = Math.round(h.cpu_pct || 0);
  const memPct = h.mem_total ? Math.round((h.mem_used / h.mem_total) * 100) : 0;
  const disks = snap.disks || [];
  const isCtr = mode === 'ctr';

  const diskTotal = disks.reduce((s, d) => s + d.total, 0);
  const diskUsed = disks.reduce((s, d) => s + d.used, 0);
  const diskFree = Math.max(0, diskTotal - diskUsed);
  const rxLine = nasSparkPath(nasState.histRx, 200, 34, 0, true);
  const txLine = nasSparkPath(nasState.histTx, 200, 34, 0, true);
  const rxArea = rxLine ? rxLine + ' L200,34 L0,34 Z' : '';

  const diskBlock = disks.length ? `
    <div class="np-stor">存储 <b>${disks.length}</b> 块盘 · 总 <b>${nasFmtBytes(diskTotal)}</b> · 剩余 <b>${nasFmtBytes(diskFree)}</b></div>
    ${disks.slice(0, 3).map(d => {
      const p = Math.round((d.used / d.total) * 100);
      return `<div class="np-metric">
        <div class="np-row"><span>${esc(d.mount)}</span><b>${p}%</b></div>
        <div class="bar"><i class="${nasBarClass(p)}" style="width:${p}%"></i></div>
      </div>`;
    }).join('')}` : '<div class="np-stor">未识别到存储池</div>';

  const updated = snap.collected_at
    ? new Date(snap.collected_at * 1000).toLocaleTimeString('zh-CN', { hour12: false })
    : '--:--:--';

  const hostBody = `
    <div>
      <div class="np-hn">${esc(h.hostname || 'NAS')}</div>
      ${h.ip ? `<div class="np-ip">${esc(h.ip)}</div>` : ''}
      <div class="np-os">${esc(h.os_name || 'Linux')}${h.kernel ? ' · ' + esc(h.kernel) : ''}</div>
      <div class="np-os">${h.cores || 0} 核 · 已运行 ${nasFmtUptime(h.uptime)}</div>
    </div>
    <div class="np-metric">
      <div class="np-row"><span>CPU</span><b>${cpu}%</b></div>
      <div class="bar"><i class="${nasBarClass(cpu)}" style="width:${cpu}%"></i></div>
    </div>
    <div class="np-metric">
      <div class="np-row"><span>内存 ${nasFmtBytes(h.mem_used)} / ${nasFmtBytes(h.mem_total)}</span><b>${memPct}%</b></div>
      <div class="bar"><i class="${nasBarClass(memPct)}" style="width:${memPct}%"></i></div>
    </div>
    ${diskBlock}
    <div class="np-net">
      <div class="np-row"><span>网络</span><b><span class="v-down">↓${nasFmtRate(net.rx_bps)}</span> <span class="v-up">↑${nasFmtRate(net.tx_bps)}</span></b></div>
      <svg viewBox="0 0 200 34" preserveAspectRatio="none" aria-hidden="true">
        <path d="${rxArea}" fill="var(--accent-soft)" stroke="none"/>
        <path d="${rxLine}" fill="none" stroke="var(--accent)" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/>
        <path d="${txLine}" fill="none" stroke="var(--success)" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/>
      </svg>
      <div class="np-legend"><span class="v-down">↓ 下行</span><span class="v-up">↑ 上行</span>${net.scope === 'container' ? '<span>容器网卡</span>' : ''}<span>近 10 分钟</span></div>
    </div>
    <div class="np-meta">
      <div><span>处理器温度</span><b title="${h.virtualized && !(h.temp_c > 0) ? '当前系统运行在虚拟机中，虚拟化平台不暴露 CPU 温度传感器' : ''}">${nasTempText(h)}</b></div>
      <div><span>架构</span><b>${esc(h.arch || '--')}</b></div>
    </div>`;

  const ctRows = running.slice(0, isCtr ? 12 : 8).map(c => {
    const mp = c.mem_limit ? Math.min(100, (c.mem_used / c.mem_limit) * 100) : 0;
    return `<div class="np-ct">
      <div class="n"><i></i>${esc(c.name)}</div>
      <div class="c">处理器 ${(c.cpu_pct || 0).toFixed(1)}%</div>
      <div class="m"><span class="mb"><i style="width:${mp.toFixed(0)}%"></i></span><span>已使用内存 ${nasFmtBytes(c.mem_used)}</span></div>
    </div>`;
  }).join('');

  const ctrBody = `
    <div class="np-ct-head">容器 <span>${running.length} 运行 / ${cts.length} 个</span></div>
    <div class="np-ct-list">${ctRows || '<div class="np-empty">暂无运行中的容器</div>'}</div>
    <div class="np-foot">${updated} 更新${snap.docker_ok ? '' : ' · Docker 未连接'}</div>`;

  const rail = isCtr
    ? `<span class="pct">${running.length}</span><span class="txt">容器</span><span class="bar"><i style="height:${cts.length ? Math.max(6, Math.min(100, (running.length / cts.length) * 100)) : 4}%"></i></span>`
    : `<span class="pct">${cpu}%</span><span class="txt">NAS</span><span class="bar"><i style="height:${Math.max(4, Math.min(100, cpu))}%"></i></span>`;

  let body;
  if (isCtr) body = ctrBody;
  else if (mode === 'host') body = hostBody + `<div class="np-foot">${updated} 更新</div>`;
  else body = hostBody + ctrBody;

  return `
  <div class="np-head">
    <span class="np-title">${isCtr ? '🐳 容器' : '🖥️ NAS'}</span>
    <span class="np-dot"></span>
    <button class="np-detail" type="button" data-np-detail title="查看完整 NAS 信息">详情</button>
    <button class="np-fold" type="button" data-np-fold aria-label="收起 / 展开面板">›</button>
  </div>
  <div class="np-rail">${rail}</div>
  <div class="np-body">${body}</div>`;
}

function paintNAS(panel, html) {
  const old = panel.querySelector('.np-body');
  const top = old ? old.scrollTop : 0;
  panel.innerHTML = html;
  const body = panel.querySelector('.np-body');
  if (body) body.scrollTop = top;
}

function renderNAS() {
  const snap = nasState.snap;
  if (!snap) return;

  const net = snap.net || {};
  const push = (arr, v) => { arr.push(Number(v) || 0); if (arr.length > 120) arr.shift(); };
  const hist = snap.history || {};
  if (Array.isArray(hist.rx) && hist.rx.length >= 2) {
    // 后端持续采样近 10 分钟历史，刷新页面不丢曲线
    nasState.histRx = hist.rx.slice();
    nasState.histTx = (hist.tx || []).slice();
    nasState.histCpu = (hist.cpu || []).slice();
  } else {
    push(nasState.histRx, net.rx_bps);
    push(nasState.histTx, net.tx_bps);
    push(nasState.histCpu, (snap.host || {}).cpu_pct);
  }

  // 状态条模式：只更新底部横条
  const banner = nasBannerEl();
  if (nasState.layout === 'banner') {
    if (banner && !banner.hidden) banner.innerHTML = nasBannerHTML(snap);
    return;
  }

  const both = nasState.layout === 'both';
  const [left, right] = nasPanels();
  if (left && !left.hidden) paintNAS(left, nasHTML(snap, both ? 'host' : 'full'));
  if (right && !right.hidden) paintNAS(right, nasHTML(snap, both ? 'ctr' : 'full'));
}

function renderNASError(msg) {
  const banner = nasBannerEl();
  if (nasState.layout === 'banner' && banner && !banner.hidden) {
    if (!nasState.snap) {
      banner.innerHTML = `<div class="nb-item" style="color:var(--danger)">采集失败：${esc(msg)}，稍后自动重试</div>`;
    }
    return;
  }

  const panels = nasPanels().filter(p => p && !p.hidden);
  if (!panels.length) return;

  // 已有数据时保留上次内容，仅提示异常，避免限流/抖动把面板清空
  if (nasState.snap) {
    panels.forEach(p => {
      const dot = p.querySelector('.np-dot');
      if (dot) dot.classList.add('bad');
      const foot = p.querySelector('.np-foot');
      if (foot) foot.textContent = '采集异常：' + msg + '（显示上次数据，稍后重试）';
    });
    return;
  }

  const folded = panels[0].classList.contains('collapsed');
  const html = `
  <div class="np-head">
    <span class="np-title">🖥️ NAS</span>
    <span class="np-dot bad"></span>
    <button class="np-fold" type="button" data-np-fold aria-label="收起 / 展开面板">›</button>
  </div>
  <div class="np-rail">
    <span class="pct">--</span>
    <span class="txt">NAS</span>
    <span class="bar"><i style="height:4%"></i></span>
  </div>
  <div class="np-body">
    <div class="np-empty">${esc(msg)}</div>
    <div class="np-foot">连接不上采集服务，稍后自动重试</div>
  </div>`;
  panels.forEach(p => {
    p.innerHTML = html;
    p.classList.toggle('collapsed', folded);
  });
}

/* ================= NAS 详细信息弹窗 ================= */
function nasDetailHTML(snap) {
  const h = snap.host || {};
  const net = snap.net || {};
  const cts = snap.containers || [];
  const running = cts.filter(c => c.state === 'running');
  const disks = snap.disks || [];
  const cpu = Math.round(h.cpu_pct || 0);
  const memPct = h.mem_total ? Math.round((h.mem_used / h.mem_total) * 100) : 0;
  const diskTotal = disks.reduce((s, d) => s + d.total, 0);
  const diskUsed = disks.reduce((s, d) => s + d.used, 0);
  const diskFree = Math.max(0, diskTotal - diskUsed);
  const diskPct = diskTotal ? Math.round((diskUsed / diskTotal) * 100) : 0;
  const rxLine = nasSparkPath(nasState.histRx, 300, 110, 0, true);
  const txLine = nasSparkPath(nasState.histTx, 300, 110, 0, true);
  const rxArea = rxLine ? rxLine + ' L300,110 L0,110 Z' : '';
  const updated = snap.collected_at
    ? new Date(snap.collected_at * 1000).toLocaleTimeString('zh-CN', { hour12: false })
    : '--:--:--';

  const kpi = (k, v, unit, s) => `<div class="nas-d-kpi">
      <div class="k">${k}</div>
      <div class="v">${v}<small>${unit}</small></div>
      <div class="s">${s}</div>
    </div>`;

  const diskRows = disks.map(d => {
    const p = Math.round((d.used / d.total) * 100);
    return `<div class="nas-d-disk">
      <div class="r"><span>${esc(d.mount)}${d.fstype ? ' · ' + esc(d.fstype) : ''}</span>
      <span>${nasFmtBytes(d.used)} / ${nasFmtBytes(d.total)}（${p}%，剩余 ${nasFmtBytes(d.total - d.used)}）</span></div>
      <div class="bar" style="margin-top:5px"><i class="${nasBarClass(p)}" style="width:${p}%"></i></div>
    </div>`;
  }).join('') || '<div class="np-empty">未识别到存储池</div>';

  const ctRows = cts.map(c => {
    const isRun = c.state === 'running';
    const memPct2 = c.mem_limit ? Math.round((c.mem_used / c.mem_limit) * 100) : 0;
    return `<tr>
      <td><div class="nm"><i class="${isRun ? '' : 'stop'}"></i>${esc(c.name)}</div></td>
      <td style="color:var(--text-muted)">${esc(c.image)}</td>
      <td>${isRun ? '运行中' : esc(c.state)}</td>
      <td class="num" style="color:var(--text-muted)">${(c.ports && c.ports.length) ? c.ports.map(p => esc(p)).join('<br>') : '—'}</td>
      <td class="num">${(c.cpu_pct || 0).toFixed(1)}%</td>
      <td class="num">${nasFmtBytes(c.mem_used)}${c.mem_limit ? ' / ' + nasFmtBytes(c.mem_limit) + '（' + memPct2 + '%）' : ''}</td>
      <td class="num"><span class="v-down">↓${nasFmtRate(c.rx_bps)}</span> / <span class="v-up">↑${nasFmtRate(c.tx_bps)}</span></td>
      <td class="num">${nasFmtUptime(c.uptime)}</td>
    </tr>`;
  }).join('') || '<tr><td colspan="8" class="np-empty">暂无容器</td></tr>';

  return `
  <div class="nas-d-head">
    <div class="nas-d-host">
      <h4>${esc(h.hostname || 'NAS')}</h4>
      <p>${h.ip ? esc(h.ip) + ' · ' : ''}${esc(h.os_name || 'Linux')}${h.kernel ? ' · ' + esc(h.kernel) : ''} · ${h.cores || 0} 核 · ${esc(h.arch || '')} · 已运行 ${nasFmtUptime(h.uptime)}</p>
    </div>
    <div style="margin-left:auto;font-size:11px;color:var(--text-muted)">${updated} 更新${net.scope === 'container' ? ' · 容器网卡口径' : ''}</div>
  </div>
  <div class="nas-d-kpis">
    ${kpi('CPU', cpu, '%', `负载 ${(h.load1 || 0).toFixed(2)} · ${h.temp_c > 0 ? Math.round(h.temp_c) + '°C' : (h.virtualized ? '虚拟机无温度传感器' : '无温度传感器')}`)}
    ${kpi('内存', nasFmtBytes(h.mem_used), '', `共 ${nasFmtBytes(h.mem_total)} · 使用 ${memPct}%`)}
    ${kpi('存储', diskPct, '%', `${disks.length} 块盘 · 总 ${nasFmtBytes(diskTotal)} · 剩余 ${nasFmtBytes(diskFree)}`)}
    <div class="nas-d-kpi">
      <div class="k">网络</div>
      <div class="v"><span class="v-down">↓${nasFmtRate(net.rx_bps)}</span></div>
      <div class="s"><span class="v-up">↑${nasFmtRate(net.tx_bps)}</span> 上行 · 对数刻度 1B/s–10GB/s</div>
    </div>
  </div>
  <div class="nas-d-grid">
    <div class="nas-d-panel">
      <h5>网络吞吐（近 10 分钟）</h5>
      <svg class="nas-d-net" viewBox="0 0 300 110" preserveAspectRatio="none" aria-hidden="true">
        <g stroke="var(--edge)" stroke-width="1">
          <line x1="0" y1="27" x2="300" y2="27"/><line x1="0" y1="55" x2="300" y2="55"/><line x1="0" y1="83" x2="300" y2="83"/>
        </g>
        <path d="${rxArea}" fill="var(--accent-soft)" stroke="none"/>
        <path d="${rxLine}" fill="none" stroke="var(--accent)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>
        <path d="${txLine}" fill="none" stroke="var(--success)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>
      </svg>
      <div class="nas-d-legend">
        <span><i style="background:var(--accent)"></i>下行 ${nasFmtRate(net.rx_bps)}</span>
        <span><i style="background:var(--success)"></i>上行 ${nasFmtRate(net.tx_bps)}</span>
        <span>对数刻度 1B/s – 10GB/s</span>
        <span>每 5s 采样 · 共 10 分钟</span>
      </div>
    </div>
    <div class="nas-d-panel">
      <h5>存储池</h5>
      ${diskRows}
    </div>
  </div>
  <div class="nas-d-panel" style="margin-top:10px">
    <h5>Docker 容器</h5>
    <div style="overflow-x:auto">
      <table class="nas-d-table">
        <thead><tr><th>容器</th><th>镜像</th><th>状态</th><th>端口</th><th>处理器</th><th>已使用内存</th><th>网络 ↓ / ↑</th><th>运行时长</th></tr></thead>
        <tbody>${ctRows}</tbody>
      </table>
    </div>
  </div>
  <div class="nas-d-foot">
    <span>容器 <b>${running.length}</b> 运行 / <b>${cts.length}</b> 个</span>
    <span>CPU 合计 <b>${cts.reduce((s, c) => s + (c.cpu_pct || 0), 0).toFixed(1)}%</b></span>
    <span>内存合计 <b>${nasFmtBytes(cts.reduce((s, c) => s + (c.mem_used || 0), 0))}</b></span>
    <span>Docker <b>${snap.docker_ok ? '已连接' : '未连接'}</b></span>
  </div>`;
}

function renderNASDetailIfOpen() {
  const m = document.getElementById('nasDetailModal');
  const body = document.getElementById('nasDetailBody');
  if (!m || !body || !m.classList.contains('show')) return;
  body.innerHTML = nasState.snap
    ? nasDetailHTML(nasState.snap)
    : '<div class="np-empty">正在采集 NAS 数据…</div>';
}

function openNASDetail() {
  const m = document.getElementById('nasDetailModal');
  if (!m) return;
  applyNasModalAlpha();
  m.classList.add('show');
  renderNASDetailIfOpen();
}

// 弹窗不透明度来自后台设置（40%~100%），以 CSS 变量控制
function applyNasModalAlpha() {
  const raw = parseInt((state.settings || {}).nas_modal_alpha, 10);
  const pct = Math.min(100, Math.max(40, isNaN(raw) ? 100 : raw));
  document.documentElement.style.setProperty('--nas-alpha', pct + '%');
}

function closeNASDetail() {
  const m = document.getElementById('nasDetailModal');
  if (m) m.classList.remove('show');
}

(function bindNASDetail() {
  const m = document.getElementById('nasDetailModal');
  if (!m) return;
  const btn = document.getElementById('nasDetailClose');
  if (btn) btn.onclick = closeNASDetail;
  m.addEventListener('click', (e) => { if (e.target === m) closeNASDetail(); });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && m.classList.contains('show')) closeNASDetail();
  });
})();

document.addEventListener('visibilitychange', () => {
  if (document.hidden) return;
  if (!nasAnyVisible()) return;
  if (nasState.timer) { clearTimeout(nasState.timer); nasState.timer = null; }
  refreshNAS();
});

boot();

// 跨标签设置变更通知：admin 标签保存后，主页标签收到 storage 事件自动刷新
// 注意：storage 事件只在**其他标签页**触发（写入方自己收不到），正好符合我们要的语义
window.addEventListener('storage', (e) => {
  if (e.key === 'sp_settings_updated') {
    if (window.__spReloadPending) return;        // 防抖：短时间内多次保存只重载一次
    window.__spReloadPending = true;
    setTimeout(() => { window.__spReloadPending = false; }, 800);
    reloadPublic();
  }
});

/**
 * 主页逻辑：全部展示内容由后端 public.php 提供
 */
const state = {
  settings: {},
  groups: [],
  user: null,
  lanMode: false, // 由后台「默认地址模式」设置决定
  theme: 'dark',
  sortingGroupId: null, // 当前正在排序的分组 id（仅管理员，null = 未排序）
  sortDirty: false, // 当前排序分组有未保存的拖拽改动
  groupIntroDone: false, // 首屏分组入场动画只播一次（后续重绘不闪动画）
  cardsIntroDone: false, // 首屏卡片入场动画只播一次（后续重绘不闪动画）
  navActiveGroupIdx: 0,  // 导航栏模式（card_style=nav）当前激活顶级分组索引
  activeTopId: 0,        // 当前激活的顶级分组 id（两种模式共享：sidebar 模式用它决定渲染哪个顶级的子 section；navbar 模式和 navActiveGroupIdx 同步）
  activeChildId: 0,      // 当前激活的子分组 id（侧边栏点击二级定位主内容）
  searchKeyword: '',     // 实时页面内搜索关键词（空 = 全量渲染）
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
 */
function isLikelyLAN(lanHostnames) {
  const h = location.hostname.toLowerCase();
  if (!h) return false;
  // 管理员配置的自定义内网域名列表优先匹配
  if (lanHostnames) {
    const list = lanHostnames.split(',').map(s => s.trim().toLowerCase()).filter(Boolean);
    if (list.includes(h)) return true;
    // 支持后缀匹配（如 ozero.top 匹配 test.ozero.top）
    for (const pat of list) {
      if (pat.startsWith('.') && h.endsWith(pat)) return true;
    }
  }
  // 内置判断：localhost / 私网域名
  if (h === 'localhost' || h.endsWith('.local') || h.endsWith('.lan') || h.endsWith('.home') || h.endsWith('.intranet')) return true;
  // IPv6 本地链路 / ULA
  if (h.startsWith('fe8:') || h.startsWith('fc') || h.startsWith('fd')) return true;
  // IPv4 私网
  const parts = h.split('.').map(Number);
  if (parts.length === 4) {
    if (parts[0] === 10) return true;
    if (parts[0] === 172 && parts[1] >= 16 && parts[1] <= 31) return true;
    if (parts[0] === 192 && parts[1] === 168) return true;
    if (parts[0] === 127) return true;
    if (parts[0] === 169 && parts[1] === 254) return true;
  }
  return false;
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
  const desktopEnabled = (settings.topbar_desktop_enabled ?? '1') === '1';
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
    const el = link ? document.createElement('a') : document.createElement('span');
    if (link) { el.href = link; el.target = '_blank'; el.rel = 'noopener'; }
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

  // 地址模式：localStorage 优先（访客自定义），否则用后台默认
  const savedLan = localStorage.getItem('sp_lan_mode');
  state.lanMode = savedLan !== null
    ? savedLan === 'lan'
    : (s.default_lan_mode === 'lan' || (s.default_lan_mode === 'auto' && isLikelyLAN(s.lan_hostnames)));
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
async function renderWeather(city) {
  const el = document.getElementById('weatherArea');
  try {
    const w = await API.get(API_BASE + 'weather.php?action=current' + (city ? '&city=' + encodeURIComponent(city) : ''));
    if (!w || w.temp === undefined) return;
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
    // 天气服务不可用时静默隐藏，不影响主页
    el.hidden = true;
  }
}

/* ---------- 时钟 ---------- */
function startClock() {
  const timeEl = document.getElementById('clockTime');
  const dateEl = document.getElementById('clockDate');
  const week = ['日', '一', '二', '三', '四', '五', '六'];
  const pad = n => String(n).padStart(2, '0');
  const tick = () => {
    const d = new Date();
    timeEl.textContent = pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
    dateEl.textContent = d.getFullYear() + ' 年 ' + (d.getMonth() + 1) + ' 月 ' + d.getDate() + ' 日 · 星期' + week[d.getDay()];
  };
  tick();
  setInterval(tick, 1000);
}

/* ---------- 搜索 ---------- */
/* 搜索引擎 logo：首位走本站服务端代理（缓存秒回），失败再串行回退公共图标服务，全部失败则隐藏只显示文字 */
const ENGINE_ICON_SOURCES = [
  host => FAVICON_PROXY_BASE + 'favicon.php?host=' + encodeURIComponent(host),
  host => 'https://api.iowen.cn/favicon/' + host + '.png',
  host => 'https://favicon.cccyun.cc/' + host,
  host => 'https://favicon.im/' + host + '?larger=true',
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
  if (isEditor) {
    const sortWrap = document.createElement('span');
    sortWrap.className = 'group-sort';
    if (state.sortingGroupId === g.id) {
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
  section.appendChild(head);

  const cards = document.createElement('div');
  cards.className = 'cards' + (styleApp ? ' style-app' : '');
  // 先在 DocumentFragment 上构建整组卡片，一次性 append，减少重排次数
  const cardsFrag = document.createDocumentFragment();
  (g.items || []).forEach(item => {
    cardsFrag.appendChild(buildCard(item, styleApp));
  });
  cards.appendChild(cardsFrag);
  section.appendChild(cards);

  if (!(g.items || []).length) {
    const empty = document.createElement('div');
    empty.className = 'empty-tip';
    empty.textContent = '该分组暂无卡片';
    section.appendChild(empty);
  }

  container.appendChild(section);
}

function renderGroups() {
  const wrap = document.getElementById('groupsWrap');
  const styleApp = isAppCardStyle();

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
        if (tg.icon) h.textContent = tg.icon + ' ';
        h.textContent = tg.title;
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

/** 构建一个目录项（targetId=跳转目标元素 id，dotColor=色点颜色，空则用默认灰点，icon=分组图标） */
function buildNavItem(targetId, label, dotColor, icon) {
  const a = document.createElement('a');
  a.href = '#';
  a.className = 'gn-item';
  a.dataset.target = targetId;
  const dot = document.createElement('span');
  dot.className = 'gn-dot';
  if (dotColor) { dot.style.background = dotColor; dot.style.opacity = '1'; }
  if (icon) {
    const ic = document.createElement('span');
    ic.className = 'gn-icon';
    ic.textContent = icon;
    a.appendChild(ic);
  } else {
    a.appendChild(dot);
  }
  const lab = document.createElement('span');
  lab.className = 'gn-label';
  lab.textContent = label;
  a.appendChild(lab);
  a.addEventListener('click', e => {
    e.preventDefault();
    const target = document.getElementById(targetId);
    if (target) {
      const y = target.getBoundingClientRect().top + window.pageYOffset - 90;
      window.scrollTo({ top: y, behavior: 'smooth' });
    }
  });
  return a;
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
    mobile.hidden = visibleTop.length > 0;  // PC 有 tabs 时隐藏移动端
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
  visibleKids.forEach((k, i) => {
    renderGroupCard(wrap, k, styleApp, i, true, true);
  });
  state.groupIntroDone = true;
  state.cardsIntroDone = true;
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

/* ---------- 卡片图标懒加载：视口门控 + 同 host 探测去重 + 超时熔断 ---------- */

const FAVICON_SRC_TIMEOUT = 8000;   // 单个图标源等待上限
const FAVICON_TOTAL_BUDGET = 15000; // 单个 host 整条回退链总预算，避免坏源拖死
const faviconProbeCache = new Map(); // host -> Promise<可用URL|''>，同 host 全卡片只探测一次
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

/** 用独立 Image 探测单个源是否可用（带超时），不触碰卡片上的 img 元素 */
function probeImage(url, timeout) {
  return new Promise(resolve => {
    const im = new Image();
    let done = false;
    const finish = ok => { if (!done) { done = true; clearTimeout(timer); im.onload = im.onerror = null; resolve(ok); } };
    const timer = setTimeout(() => finish(false), timeout);
    im.onload = () => finish(true);
    im.onerror = () => finish(false);
    im.referrerPolicy = 'no-referrer';
    im.src = url;
  });
}

/** 探测站点图标的可用源；同 host 只走一次回退链，结果被所有同 host 卡片共享 */
function probeFaviconSources(pageUrl) {
  const sources = faviconSources(pageUrl);
  if (!sources.length) return Promise.resolve('');
  let host = '';
  try { host = new URL(pageUrl).hostname; } catch (e) { /* ignore */ }
  if (host && faviconProbeCache.has(host)) return faviconProbeCache.get(host);
  const p = (async () => {
    const deadline = Date.now() + FAVICON_TOTAL_BUDGET;
    for (let i = 0; i < sources.length; i++) {
      if (Date.now() > deadline) break;
      if (await probeImage(sources[i], Math.min(FAVICON_SRC_TIMEOUT, deadline - Date.now() + 1))) {
        return sources[i];
      }
    }
    return '';
  })();
  if (host) faviconProbeCache.set(host, p);
  return p;
}

/** img 解码完成后再显示，避免大图解码造成卡片区掉帧；解码失败也照常显示 */
function showWhenDecoded(img, icon, iconDoneRef) {
  const finish = () => {
    if (iconDoneRef.done) return;
    iconDoneRef.done = true;
    icon.style.background = '';
    icon.classList.add('has-img');
  };
  if (img.decode) img.decode().then(finish).catch(finish);
  else finish();
}

function buildCard(item, styleApp) {
  const a = document.createElement('a');
  a.className = 'card' + (styleApp ? ' app-card' : '');
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

  // 图标：默认占位底色，图片解码成功后转 has-img；视口外不发起任何网络请求
  const icon = document.createElement('span');
  icon.className = 'icon';
  a.appendChild(icon);

  // 用对象承载完成标志，供 showWhenDecoded 共享
  const iconState = { done: false };
  const useTextIcon = () => {
    if (iconState.done) return;
    iconState.done = true;
    icon.classList.remove('has-img');
    // 文字型：手动文字优先 → 空则 fallback title
    let raw = '';
    if (item.icon_type === 'text') {
      if (item.icon_value && !isImageUrlLike(item.icon_value)) {
        raw = item.icon_value;
      } else {
        raw = item.title || '?';
      }
    } else {
      raw = item.title || '?';
    }
    icon.style.background = item.icon_bg || stringColor(item.title);
    // 卡片图标盒尺寸固定（普通 52 / app 58），传入宽度免 getComputedStyle 强制布局
    renderTextIcon(icon, smartIconText(raw), 21, styleApp ? 58 : 52);
  };
  if (item.icon_type === 'image' && item.icon_value) {
    const img = document.createElement('img');
    img.alt = '';
    img.loading = 'lazy';
    img.decoding = 'async';
    img.onerror = useTextIcon;
    img.onload = () => showWhenDecoded(img, icon, iconState);
    icon.style.background = 'var(--accent-soft)';
    icon.appendChild(img);
    armCardIcon(icon, () => { img.src = assetUrl(item.icon_value); });
  } else if (item.icon_type === 'favicon' && item.url) {
    // 进视口后探测可用源（同 host 共享结果 + 超时熔断），全部失败再使用文字图标
    if (faviconSources(item.url).length) {
      const img = document.createElement('img');
      img.alt = '';
      img.loading = 'lazy';
      img.decoding = 'async';
      img.referrerPolicy = 'no-referrer';
      // 探测阶段已验证源可用（且浏览器已缓存），此处失败仅为极端竞争，直接兜底文字
      img.onerror = useTextIcon;
      img.onload = () => showWhenDecoded(img, icon, iconState);
      icon.style.background = 'var(--accent-soft)';
      icon.appendChild(img);
      armCardIcon(icon, () => {
        probeFaviconSources(item.url).then(url => {
          if (iconState.done) return;
          if (url) img.src = url;
          else useTextIcon();
        });
      });
    } else {
      useTextIcon();
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
  const d = document.createElement('div');
  d.className = 'd';
  d.textContent = item.description || item.url;
  info.appendChild(d);
  a.appendChild(info);

  // 内网地址标识
  if (state.lanMode && item.lan_url) {
    const badge = document.createElement('span');
    badge.className = 'lan-badge';
    badge.textContent = '内网';
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

/** 打开卡片：1 当前页 / 2 新窗口 / 3 弹层；newTab 参数可强制覆盖打开方式（右键菜单用） */
function openPage(item, newTab) {
  const url = state.lanMode && item.lan_url ? item.lan_url : item.url;
  if (!url) {
    toast('该卡片未设置地址', 'error');
    return;
  }
  if (!safeUrl(url)) {
    toast('该卡片地址协议不合法（仅支持 http / https），已阻止打开', 'error');
    return;
  }
  // newTab 参数覆盖：右键菜单直接指定打开方式
  if (typeof newTab === 'boolean') {
    if (newTab) window.open(url, '_blank');
    else location.href = url;
    return;
  }
  if (item.open_method === 3) {
    document.getElementById('iframeTitle').textContent = item.title + ' · ' + url;
    document.getElementById('iframeNewTab').href = url;
    document.getElementById('iframeWin').src = url;
    document.getElementById('iframeModal').classList.add('show');
    return;
  }
  if (item.open_method === 1) {
    location.href = url;
    return;
  }
  window.open(url, '_blank');
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
  btn.textContent = state.lanMode ? '内网' : '外网';
  btn.title = state.lanMode ? '当前：内网地址（点击切外网）' : '当前：外网地址（点击切内网）';
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
      localStorage.setItem('sp_lan_mode', state.lanMode ? 'lan' : 'public');
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
    // 放弃：按 state 已保存数据重绘，还原脏顺序并退出排序
    renderGroups();
    state.sortingGroupId = null;
    state.sortDirty = false;
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

  // 卡片拖拽：事件委托挂 groupsWrap（重绘不丢绑定）；仅当前排序分组生效
  const wrap = document.getElementById('groupsWrap');
  let dragCard = null;
  const clearDrag = () => {
    wrap.querySelectorAll('.card.dragging, .card.drag-over-before, .card.drag-over-after')
      .forEach(c => c.classList.remove('dragging', 'drag-over-before', 'drag-over-after'));
    if (dragCard) { dragCard.draggable = false; dragCard = null; }
  };
  wrap.addEventListener('mousedown', e => {
    if (state.sortingGroupId === null) return;
    const card = e.target.closest('.card');
    // 只允许拖拽当前排序分组内的卡片
    if (card && card.closest('.group').id === 'group-' + state.sortingGroupId) card.draggable = true;
  });
  wrap.addEventListener('dragstart', e => {
    if (state.sortingGroupId === null) return;
    const card = e.target.closest('.card');
    if (!card) { e.preventDefault(); return; }
    dragCard = card;
    requestAnimationFrame(() => card.classList.add('dragging'));
    e.dataTransfer.effectAllowed = 'move';
    try { e.dataTransfer.setData('text/plain', card.dataset.id || ''); } catch (_) { /* 兼容忽略 */ }
  });
  wrap.addEventListener('dragover', e => {
    if (state.sortingGroupId === null || !dragCard) return;
    const card = e.target.closest('.card');
    wrap.querySelectorAll('.card.drag-over-before, .card.drag-over-after')
      .forEach(c => c.classList.remove('drag-over-before', 'drag-over-after'));
    // 仅允许当前排序分组内排序
    if (!card || card === dragCard || card.parentElement !== dragCard.parentElement) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    const ref = cardInsertRef(card.parentElement, e.clientX, e.clientY, dragCard);
    if (ref) {
      ref.classList.add('drag-over-before');
    } else {
      const cards = card.parentElement.querySelectorAll('.card');
      const last = cards[cards.length - 1];
      if (last && last !== dragCard) last.classList.add('drag-over-after');
    }
  });
  wrap.addEventListener('drop', e => {
    if (state.sortingGroupId === null || !dragCard) return;
    const container = dragCard.parentElement;
    if (e.target.closest('.cards') !== container) { clearDrag(); return; }
    e.preventDefault();
    const ref = cardInsertRef(container, e.clientX, e.clientY, dragCard);
    if (ref) container.insertBefore(dragCard, ref);
    else container.appendChild(dragCard);
    state.sortDirty = true;
    updateSortSaveBtn();
    clearDrag();
  });
  wrap.addEventListener('dragend', clearDrag);
}

/**
 * 网格落点计算：返回「应插到其前面」的卡片，按流式顺序（先行后列）比较指针与各卡中心；
 * 指针位于所有卡片之后时返回 null（= 追加到容器末尾）
 */
function cardInsertRef(container, x, y, ignore) {
  const cards = Array.prototype.filter.call(container.querySelectorAll('.card'), c => c !== ignore);
  for (const c of cards) {
    const r = c.getBoundingClientRect();
    const cx = r.left + r.width / 2;
    const cy = r.top + r.height / 2;
    if (y < cy - r.height * 0.25 || (Math.abs(y - cy) <= r.height * 0.25 && x < cx)) return c;
  }
  return null;
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

/** 热点新闻按钮显隐：跟着 state.settings.news_show 走
 *  独立成函数方便 reloadPublic / 后台保存后重新执行（不依赖刷新页面）
 */
function updateNewsBtnVisible() {
  const btn = document.getElementById('hotNewsBtn');
  if (!btn) return;
  const enabled = state.settings.news_show !== '0';
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

  const mode = state.settings.home_view || 'both';
  if (mode === 'news') switchView('news');
  else switchView('nav');

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
    // 首屏缓存渲染后，逐源拉取最新数据（并发 3）
    newsFetchQueue(newsState.sources.map(s => s.id), false);
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

/** 并发上限 3 的拉取队列；latest=true 强制回源（卡片刷新按钮） */
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
  await Promise.all([worker(), worker(), worker()]);
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
  if (cached && cached.date === today && cached.url) { apply(cached.url); return; }
  // 先用静态壁纸兜底，避免短暂空白
  apply(fallback || '');
  try {
    const r = await API.get(API_BASE + 'wallpaper.php?action=daily&source=' + encodeURIComponent(source));
    if (r && r.url) {
      apply(r.url);
      try { sessionStorage.setItem(KEY, JSON.stringify({ date: today, url: r.url })); } catch (e) {}
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

  const groupSel = document.getElementById('qa_group');
  groupSel.innerHTML = '';
  const groups = state.groups || [];
  let matched = false;
  groups.forEach(g => {
    const opt = document.createElement('option');
    opt.value = String(g.id);
    opt.textContent = (g.icon ? g.icon + ' ' : '') + g.title;
    const matchGid = isEdit ? item.group_id : groupId;
    if (String(g.id) === String(matchGid)) { opt.selected = true; matched = true; }
    groupSel.appendChild(opt);
  });
  // 兜底：没匹配到当前分组时默认选第一个
  if (!matched && groups.length) groupSel.value = String(groups[0].id);

  // 标题 + 按钮文字
  const h3 = m.querySelector('.modal-head h3');
  const saveBtn = document.getElementById('qaSaveBtn');
  h3.textContent = isEdit
    ? ('编辑卡片「' + item.title + '」')
    : (('向「' + (state.groups.find(g => g.id === groupId)?.title || '') + '」添加卡片') || '添加卡片');
  saveBtn.textContent = isEdit ? '保存' : '添加卡片';

  document.getElementById('qa_group_id').value = isEdit ? item.id : 0;
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
    document.getElementById('qa_bg').value = buildBgCss('#0969da', 100);
    document.getElementById('qa_color').value = '#0969da';
    document.getElementById('qa_alpha').value = '100';
    document.getElementById('qa_alphaLabel').textContent = '100%';
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
        <div class="form-item">
          <label>标题</label>
          <input class="input" id="qa_title" type="text" maxlength="50" required>
        </div>
        <div class="form-item">
          <label>地址（鼠标点击此处可自动获取标题/描述/图标）
            <button class="btn btn-sm" type="button" id="qa_fetchMeta" style="float:right">自动获取信息</button>
          </label>
          <input class="input" id="qa_url" type="text" placeholder="https://…">
        </div>
        <div class="form-item">
          <label>内网地址（可选，用于主页「内网模式」）</label>
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
            <input class="input" id="qa_iconText" type="text" maxlength="12" placeholder="支持中文 4 字 / 英文 12 字母">
          </div>
          <div class="form-item" style="margin-top:10px" id="qa_bgWrap">
            <label>图标背景色</label>
            <div class="upload-row" style="gap:8px;flex-wrap:nowrap">
              <input type="color" id="qa_color" value="#0969da" style="width:44px;height:34px;padding:2px;border-radius:6px;cursor:pointer">
              <span style="font-size:12px;color:var(--text-muted);white-space:nowrap">透明度</span>
              <input type="range" id="qa_alpha" min="0" max="100" value="100" style="flex:1">
              <span id="qa_alphaLabel" style="font-size:12px;color:var(--text-muted);min-width:32px;text-align:right">100%</span>
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
  document.getElementById('qa_url').addEventListener('input', updateQAIconPreview);

  // 颜色联动
  document.getElementById('qa_color').addEventListener('input', syncQAColorPanel);
  document.getElementById('qa_alpha').addEventListener('input', syncQAColorPanel);

  // fetch_meta
  document.getElementById('qa_fetchMeta').addEventListener('click', fetchQAMeta);

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
        title: document.getElementById('qa_title').value.trim(),
        url: document.getElementById('qa_url').value.trim(),
        lan_url: document.getElementById('qa_lan').value.trim(),
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
    preview.style.background = bg || '#0969da';
    const raw = (document.getElementById('qa_iconText').value.trim())
                ? document.getElementById('qa_iconText').value : (title || '?');
    renderTextIcon(preview, smartIconText(raw), 18);
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

/** fetch_meta — 自动填充 title/description；favicon 无 icon 自动降级 text */
async function fetchQAMeta() {
  const url = document.getElementById('qa_url').value.trim();
  if (!url || !/^https?:\/\//i.test(url)) { toast('请先填写 http(s):// 开头的地址', 'error'); return; }
  try {
    const btn = document.getElementById('qa_fetchMeta');
    btn.disabled = true; btn.textContent = '获取中…';
    const res = await API.get(API_BASE + 'items.php?action=fetch_meta&url=' + encodeURIComponent(url));

    const got = [];
    const emptyTitle = !document.getElementById('qa_title').value.trim();
    const emptyDesc  = !document.getElementById('qa_desc').value.trim();
    const curType = document.querySelector('#qa_itype input:checked').value;

    if (res.title && emptyTitle) { document.getElementById('qa_title').value = res.title; got.push('标题'); }
    if (res.description && emptyDesc) { document.getElementById('qa_desc').value = res.description; got.push('描述'); }

    // 图标处理：与 admin.js 对齐的三态分支
    if (curType === 'favicon') {
      if (res.icon) {
        if (res.fallback_icon) {
          // 公共图标源 → 切 image + 填 URL
          document.querySelector('#qa_itype input[value="image"]').checked = true;
          document.getElementById('qa_icon').value = res.icon;
          got.push('图标(浏览器加载)');
          onQAIconTypeChange();
        } else {
          got.push('图标');
        }
      } else {
        // favicon 拿不到 → 自动降级为文字图标
        document.querySelector('#qa_itype input[value="text"]').checked = true;
        document.getElementById('qa_iconText').value = smartIconText(res.title || url);
        onQAIconTypeChange();
        got.push('图标(文字降级)');
      }
    } else {
      updateQAIconPreview();
    }

    toast(got.length ? ('已获取：' + got.join(' / ')) : '没获取到新信息', got.length ? 'success' : 'info');
  } catch (err) { toast('自动获取失败：' + err.message, 'error'); }
  finally {
    const btn = document.getElementById('qa_fetchMeta');
    btn.disabled = false; btn.textContent = '自动获取信息';
  }
}

/** 获取站点图标（items.php?action=favicon）—— 写入 qa_icon 并自动切到 image 类型 */
async function fetchQAFavicon() {
  const url = document.getElementById('qa_url').value.trim();
  if (!url) { toast('请先填写卡片地址', 'error'); return; }
  const btn = document.getElementById('qa_useFavicon');
  btn.disabled = true; btn.textContent = '获取中…';
  try {
    const res = await API.get(API_BASE + 'items.php?action=favicon&url=' + encodeURIComponent(url));
    document.querySelector('#qa_itype input[value="image"]').checked = true;
    document.getElementById('qa_icon').value = res.url;
    onQAIconTypeChange();
    updateQAIconPreview();
    if (res.fallback) {
      toast('服务器未能缓存图标' + (res.diag ? '：' + res.diag : '') + '，已改用公共图标源', 'info');
    } else {
      toast(res.cached ? '已获取（本地缓存）' : '获取成功', 'success');
    }
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

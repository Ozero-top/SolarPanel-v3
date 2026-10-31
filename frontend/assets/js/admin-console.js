/**
 * 管理控制台：侧栏交互、风格切换、工作台数据可视化
 * 依赖 admin.js 暴露的全局 state 与数据加载函数，不修改原有业务逻辑。
 */
(function () {
  'use strict';

  var SKINS = ['aurora', 'neon', 'element'];
  var SKIN_KEY = 'sp_admin_skin';
  var COLLAPSE_KEY = 'sp_admin_collapsed';

  var PAGE_META = {
    dashboard: ['系统状态', '运行概览与实时状态'],
    items: ['卡片管理', '维护导航卡片'],
    groups: ['分组管理', '整理分类与排序'],
    settings: ['站点设置', '站点外观与访问控制'],
    account: ['账号管理', '个人资料与安全'],
    backup: ['系统备份', '配置导入导出'],
    clip: ['剪藏与接入', '浏览器扩展与令牌'],
    audit: ['审计日志', '登录与操作记录']
  };
  var OPEN_LABEL = { 1: '当前页打开', 2: '新窗口打开', 3: '弹层内嵌' };
  var ROLE_NAME = { admin: '管理员', editor: '编辑者', viewer: '只读', guest: '访客' };

  var lastDonut = { list: [], total: 0 };
  var donutAnim = null;

  function $(id) { return document.getElementById(id); }

  function cssVar(name) {
    var v = getComputedStyle(document.body).getPropertyValue(name);
    return v ? v.trim() : '';
  }

  function escapeHtml(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function getState() {
    try { return state; } catch (e) { return null; }
  }

  function appVersion() {
    try { return APP_VERSION; } catch (e) { return '—'; }
  }

    /* ================= 风格皮肤 ================= */
  function applySkin(skin, persist) {
    if (SKINS.indexOf(skin) < 0) skin = 'aurora';
    document.body.dataset.adminSkin = skin;
    document.documentElement.dataset.style = 'console';
    document.documentElement.dataset.theme = skin === 'element' ? 'light' : 'dark';
    if (persist !== false) {
      try { localStorage.setItem(SKIN_KEY, skin); } catch (e) { /* 隐私模式忽略 */ }
    }
    var opts = document.querySelectorAll('.skin-opt');
    for (var i = 0; i < opts.length; i++) {
      opts[i].classList.toggle('on', opts[i].dataset.skin === skin);
    }
    if (lastDonut.total) drawDonut(lastDonut.list, lastDonut.total);
  }

  function initSkin() {
    var saved = null;
    try { saved = localStorage.getItem(SKIN_KEY); } catch (e) { /* 忽略 */ }
    applySkin(saved || 'aurora', false);

    var dock = $('skinDock');
    var toggle = $('skinDockBtn');
    if (!dock || !toggle) return;

    toggle.addEventListener('click', function (e) {
      e.stopPropagation();
      dock.hidden = !dock.hidden;
    });
    dock.addEventListener('click', function (e) { e.stopPropagation(); });
    document.addEventListener('click', function () {
      if (!dock.hidden) dock.hidden = true;
    });

    var opts = dock.querySelectorAll('.skin-opt');
    for (var i = 0; i < opts.length; i++) {
      opts[i].addEventListener('click', function () {
        applySkin(this.dataset.skin, true);
      });
    }
  }

  // 后台始终使用控制台自己的外观，避免前台风格规则干扰
  function lockConsoleStyle() {
    if (document.documentElement.dataset.style !== 'console') {
      document.documentElement.dataset.style = 'console';
    }
  }

  /* ================= 侧栏 / 抽屉 ================= */
  function initShell() {
    var shell = $('consoleShell');
    if (!shell) return;
    var collapseBtn = $('sidebarCollapseBtn');
    var menuBtn = $('sidebarToggleBtn');
    var backdrop = $('sidebarBackdrop');

    try {
      if (localStorage.getItem(COLLAPSE_KEY) === '1') shell.classList.add('collapsed');
    } catch (e) { /* 忽略 */ }

    if (collapseBtn) {
      collapseBtn.addEventListener('click', function () {
        shell.classList.toggle('collapsed');
        try {
          localStorage.setItem(COLLAPSE_KEY, shell.classList.contains('collapsed') ? '1' : '0');
        } catch (e) { /* 忽略 */ }
      });
    }

    function closeDrawer() {
      shell.classList.remove('drawer-open');
      if (backdrop) backdrop.hidden = true;
    }

    if (menuBtn) {
      menuBtn.addEventListener('click', function () {
        shell.classList.add('drawer-open');
        if (backdrop) backdrop.hidden = false;
      });
    }
    if (backdrop) backdrop.addEventListener('click', closeDrawer);

    var tabs = document.querySelectorAll('.cs-nav .tab');
    for (var i = 0; i < tabs.length; i++) {
      tabs[i].addEventListener('click', closeDrawer);
    }
  }

  /* ================= 顶栏 ================= */
  function updateClock() {
    var el = $('csClock');
    if (!el) return;
    var d = new Date();
    el.textContent =
      (d.getHours() < 10 ? '0' : '') + d.getHours() + ':' +
      (d.getMinutes() < 10 ? '0' : '') + d.getMinutes() + ':' +
      (d.getSeconds() < 10 ? '0' : '') + d.getSeconds();
  }

  function setPageMeta(tab) {
    var meta = PAGE_META[tab];
    if (!meta) return;
    var el = $('csPageTitle');
    if (el) el.textContent = meta[0];
    document.title = meta[0] + ' - SolarPanel 管理后台';
  }

  function initChrome() {
    var original = window.activateTab;
    if (typeof original === 'function') {
      window.activateTab = function (tab) {
        original.call(this, tab);
        var name = tab && tab.dataset ? tab.dataset.tab : '';
        setPageMeta(name);
        if (name === 'dashboard') refreshDashboard();
      };
    }

    updateClock();
    setInterval(updateClock, 1000);
    setPageMeta('dashboard');

    var who = $('whoami');
    var avatar = $('csAvatar');
    if (who && avatar) {
      var sync = function () {
        var t = (who.textContent || '').trim();
        avatar.textContent = t ? t.charAt(0).toUpperCase() : '?';
      };
      sync();
      try {
        new MutationObserver(sync).observe(who, { childList: true, characterData: true, subtree: true });
      } catch (e) { /* 忽略 */ }
    }
  }

  /* ================= 工作台数据 ================= */
  function animateNumber(el, to) {
    if (!el) return;
    var from = Number(el.dataset.val || '0');
    el.dataset.val = String(to);
    if (!isFinite(from) || from === to) { el.textContent = String(to); return; }
    var start = performance.now();
    var dur = 700;
    function step(now) {
      var p = Math.min(1, (now - start) / dur);
      var eased = 1 - Math.pow(1 - p, 3);
      el.textContent = String(Math.round(from + (to - from) * eased));
      if (p < 1) requestAnimationFrame(step);
    }
    requestAnimationFrame(step);
  }

  function setBar(el, pct) {
    if (!el) return;
    var v = Math.max(0, Math.min(100, pct));
    requestAnimationFrame(function () { el.style.width = v + '%'; });
  }

  function buildGroupData(items, groups) {
    var nameById = {};
    groups.forEach(function (g) { nameById[String(g.id)] = g.title || '未命名分组'; });
    var counts = {};
    items.forEach(function (it) {
      var key = String(it.group_id || 0);
      counts[key] = (counts[key] || 0) + 1;
    });
    var list = Object.keys(counts).map(function (key) {
      var gid = Number(key);
      return {
        name: nameById[key] || (gid === 0 ? '未分组' : '分组 ' + key),
        count: counts[key]
      };
    });
    list.sort(function (a, b) { return b.count - a.count; });
    return list;
  }

  function palette() {
    return [
      cssVar('--stat-1'), cssVar('--stat-2'), cssVar('--stat-3'),
      cssVar('--stat-4'), cssVar('--stat-5'), cssVar('--stat-6')
    ].map(function (c, i) { return c || ['#5b8cff', '#a78bfa', '#fbbf24', '#3ddc97', '#22d3ee', '#f472b6'][i]; });
  }

  function drawDonut(list, total) {
    var canvas = $('groupDonut');
    if (!canvas) return;
    var ctx = canvas.getContext('2d');
    if (!ctx) return;

    var cssSize = canvas.clientWidth || 200;
    var dpr = window.devicePixelRatio || 1;
    var px = Math.round(cssSize * dpr);
    if (canvas.width !== px || canvas.height !== px) {
      canvas.width = px;
      canvas.height = px;
    }
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    if (donutAnim) cancelAnimationFrame(donutAnim);

    var cx = cssSize / 2;
    var cy = cssSize / 2;
    var outer = cssSize / 2 - 5;
    var inner = outer - 17;
    var ringColor = cssVar('--edge-weak') || 'rgba(255,255,255,.08)';
    var start = -Math.PI / 2;
    var t0 = performance.now();
    var dur = 900;

    function frame(now) {
      var p = Math.min(1, (now - t0) / dur);
      var eased = 1 - Math.pow(1 - p, 3);
      ctx.clearRect(0, 0, cssSize, cssSize);

      ctx.beginPath();
      ctx.arc(cx, cy, (outer + inner) / 2, 0, Math.PI * 2);
      ctx.lineWidth = outer - inner;
      ctx.strokeStyle = ringColor;
      ctx.stroke();

      if (!total) {
        if (p < 1) donutAnim = requestAnimationFrame(frame);
        return;
      }

      var angle = start;
      var gap = list.length > 1 ? 0.03 : 0;
      for (var i = 0; i < list.length; i++) {
        var seg = list[i];
        var sweep = (seg.count / total) * Math.PI * 2 * eased;
        if (sweep <= gap) { angle += sweep; continue; }
        var s = angle + gap / 2;
        var e = angle + sweep - gap / 2;
        ctx.beginPath();
        ctx.arc(cx, cy, (outer + inner) / 2, s, e);
        ctx.lineWidth = outer - inner;
        ctx.strokeStyle = seg.color;
        ctx.stroke();
        angle += sweep;
      }

      if (p < 1) donutAnim = requestAnimationFrame(frame);
    }
    donutAnim = requestAnimationFrame(frame);
  }

  function renderDonut(items, groups) {
    var all = buildGroupData(items, groups);
    var colors = palette();
    var top = all.slice(0, 6);
    var rest = all.slice(6).reduce(function (sum, seg) { return sum + seg.count; }, 0);
    var list = top.map(function (seg, i) { return { name: seg.name, count: seg.count, color: colors[i] }; });
    if (rest > 0) {
      list.push({ name: '其他分组', count: rest, color: cssVar('--text-muted') || '#8b93a3' });
    }

    var total = items.length;
    lastDonut = { list: list, total: total };
    var totalEl = $('donutTotal');
    if (totalEl) animateNumber(totalEl, total);

    var chip = $('groupChip');
    if (chip) chip.textContent = groups.length ? groups.length + ' 个分组' : '暂无分组';

    var legend = $('groupLegend');
    if (!legend) return;
    if (!list.length) {
      legend.innerHTML = '<div class="dash-empty">暂无卡片数据</div>';
      drawDonut([], 0);
      return;
    }
    var html = '';
    list.forEach(function (seg) {
      var pct = total ? Math.round((seg.count / total) * 100) : 0;
      html += '<div class="legend-row">' +
        '<span class="legend-dot" style="background:' + seg.color + '"></span>' +
        '<span class="legend-name" title="' + escapeHtml(seg.name) + '">' + escapeHtml(seg.name) + '</span>' +
        '<span class="legend-val">' + seg.count + '</span>' +
        '<span class="legend-pct">' + pct + '%</span>' +
        '</div>';
    });
    legend.innerHTML = html;
    drawDonut(list, total);
  }

  function renderOpenMethods(items) {
    var box = $('openBars');
    if (!box) return;
    var total = items.length;
    var counts = { 1: 0, 2: 0, 3: 0 };
    items.forEach(function (it) {
      var k = Number(it.open_method) || 2;
      if (!counts[k]) counts[k] = 0;
      counts[k] += 1;
    });
    var html = '';
    [1, 2, 3].forEach(function (k) {
      var n = counts[k] || 0;
      var pct = total ? Math.round((n / total) * 100) : 0;
      html += '<div class="bar-row">' +
        '<div class="bar-row-head"><span class="k">' + OPEN_LABEL[k] + '</span><span class="v">' + n + ' · ' + pct + '%</span></div>' +
        '<div class="bar-track"><span class="bar-fill" data-pct="' + pct + '"></span></div>' +
        '</div>';
    });
    box.innerHTML = html;
    var fills = box.querySelectorAll('.bar-fill');
    for (var i = 0; i < fills.length; i++) {
      setBar(fills[i], Number(fills[i].dataset.pct) || 0);
    }
  }

  function renderOverview(items, groups, hidden) {
    var box = $('overviewList');
    if (!box) return;
    var st = getState();
    var whoEl = $('whoami');
    var who = whoEl && whoEl.textContent ? whoEl.textContent.trim() : '—';
    var rows = [
      ['登录账号', who || '—'],
      ['权限组', ROLE_NAME[(st && st.role) || 'admin'] || '—'],
      ['站点地址', (st && st.siteUrl) ? st.siteUrl : '未设置'],
      ['隐藏卡片', String(hidden)],
      ['当前版本', 'v' + appVersion()]
    ];
    var html = '';
    rows.forEach(function (row) {
      html += '<div class="mini-row"><span class="k">' + escapeHtml(row[0]) + '</span>' +
        '<span class="v" title="' + escapeHtml(row[1]) + '">' + escapeHtml(row[1]) + '</span></div>';
    });
    box.innerHTML = html;
  }

  function renderRecent(items, groups) {
    var box = $('recentList');
    if (!box) return;
    if (!items.length) {
      box.innerHTML = '<div class="dash-empty">还没有卡片，点击「新建卡片」开始添加</div>';
      return;
    }
    var nameById = {};
    groups.forEach(function (g) { nameById[String(g.id)] = g.title || '未命名分组'; });

    var sorted = items.slice().sort(function (a, b) {
      return Number(b.id || 0) - Number(a.id || 0);
    }).slice(0, 6);

    var html = '';
    sorted.forEach(function (it) {
      var title = it.title || it.url || '未命名卡片';
      var groupName = nameById[String(it.group_id || 0)] || '未分组';
      var hidden = Number(it.is_hidden) === 1 || it.is_hidden === true;
      html += '<div class="recent-row">' +
        '<span class="recent-ico">' + escapeHtml(String(title).trim().charAt(0).toUpperCase() || '·') + '</span>' +
        '<div class="recent-main"><b title="' + escapeHtml(title) + '">' + escapeHtml(title) + '</b>' +
        '<span title="' + escapeHtml(it.url || '') + '">' + escapeHtml(it.url || '未填写地址') + '</span></div>' +
        '<span class="recent-tag' + (hidden ? ' hidden' : '') + '">' + (hidden ? '已隐藏' : escapeHtml(groupName)) + '</span>' +
        '</div>';
    });
    box.innerHTML = html;
  }

  function greeting(items, groups) {
    var el = $('dashGreeting');
    if (!el) return;
    var st = getState();
    var whoEl = $('whoami');
    var name = whoEl && whoEl.textContent ? whoEl.textContent.trim() : '';
    var h = new Date().getHours();
    var hello = h < 6 ? '夜深了' : h < 12 ? '上午好' : h < 18 ? '下午好' : '晚上好';
    var d = new Date();
    var week = '日一二三四五六'[d.getDay()];
    var date = d.getFullYear() + '年' + (d.getMonth() + 1) + '月' + d.getDate() + '日 周' + week;
    el.textContent = (name ? hello + '，' + name + ' · ' : '') + date +
      ' · 共 ' + items.length + ' 张卡片，分布在 ' + groups.length + ' 个分组' +
      (st && st.role && st.role !== 'admin' ? '（' + (ROLE_NAME[st.role] || st.role) + '权限）' : '');
  }

  function refreshDashboard() {
    if (!$('statCards')) return;
    document.documentElement.dataset.theme =
      document.body.dataset.adminSkin === 'element' ? 'light' : 'dark';
    var st = getState();
    var items = st && Array.isArray(st.items) ? st.items : [];
    var groups = st && Array.isArray(st.groups) ? st.groups : [];

    var total = items.length;
    var hidden = 0;
    var lan = 0;
    items.forEach(function (it) {
      if (Number(it.is_hidden) === 1 || it.is_hidden === true) hidden += 1;
      if (it.lan_url && String(it.lan_url).trim() !== '') lan += 1;
    });
    var tops = groups.filter(function (g) { return Number(g.parent_id || 0) === 0; });
    var subs = groups.filter(function (g) { return Number(g.parent_id || 0) !== 0; });

    animateNumber($('statCards'), total);
    animateNumber($('statGroups'), groups.length);
    animateNumber($('statHidden'), hidden);
    animateNumber($('statLan'), lan);

    var cardsFoot = $('statCardsFoot');
    if (cardsFoot) cardsFoot.textContent = total ? ('可见 ' + (total - hidden) + ' · 已隐藏 ' + hidden) : '暂无卡片';
    var groupsFoot = $('statGroupsFoot');
    if (groupsFoot) groupsFoot.textContent = groups.length ? ('一级 ' + tops.length + ' · 二级 ' + subs.length) : '暂无分组';
    var hiddenFoot = $('statHiddenFoot');
    if (hiddenFoot) hiddenFoot.textContent = total ? ('占全部卡片 ' + Math.round((hidden / total) * 100) + '%') : '暂无卡片';
    var lanFoot = $('statLanFoot');
    if (lanFoot) lanFoot.textContent = total ? ('已配置 ' + Math.round((lan / total) * 100) + '%') : '暂无卡片';

    setBar($('statCardsBar'), total ? 100 : 0);
    setBar($('statGroupsBar'), groups.length ? 100 : 0);
    setBar($('statHiddenBar'), total ? (hidden / total) * 100 : 0);
    setBar($('statLanBar'), total ? (lan / total) * 100 : 0);

    greeting(items, groups);
    renderDonut(items, groups);
    renderOpenMethods(items);
    renderOverview(items, groups, hidden);
    renderRecent(items, groups);
  }

  /* ================= 数据加载钩子 ================= */
  function hookLoaders() {
    ['loadGroups', 'loadItems'].forEach(function (name) {
      var fn = window[name];
      if (typeof fn !== 'function') return;
      window[name] = function () {
        var result = fn.apply(this, arguments);
        if (result && typeof result.then === 'function') {
          result.then(function () { setTimeout(refreshDashboard, 30); }).catch(function () {});
        } else {
          setTimeout(refreshDashboard, 30);
        }
        return result;
      };
    });

    // 兜底：数据已经加载完成或加载较慢时，多试几次
    [300, 1200, 3000].forEach(function (delay) {
      setTimeout(refreshDashboard, delay);
    });
  }

  /* ================= 工作台交互 ================= */
  function goTab(name) {
    var btn = document.querySelector('.cs-nav .tab[data-tab="' + name + '"]');
    if (!btn) return false;
    if (btn.style.display === 'none') return false;
    btn.click();
    return true;
  }

  function reloadData() {
    if (typeof loadGroups === 'function') loadGroups();
    if (typeof loadItems === 'function') loadItems();
    setTimeout(refreshDashboard, 80);
    try { if (typeof toast === 'function') toast('数据已刷新', 'success'); } catch (e) { /* 忽略 */ }
  }

  function initDashboardActions() {
    var refreshBtn = $('dashRefreshBtn');
    if (refreshBtn) refreshBtn.addEventListener('click', reloadData);

    var addBtn = $('dashAddItemBtn');
    if (addBtn) addBtn.addEventListener('click', function () {
      var origin = $('addItemBtn');
      if (origin) origin.click();
    });

    var gotoItems = $('dashGotoItems');
    if (gotoItems) gotoItems.addEventListener('click', function () { goTab('items'); });

    var quicks = document.querySelectorAll('.quick-btn');
    for (var i = 0; i < quicks.length; i++) {
      quicks[i].addEventListener('click', function () {
        var kind = this.dataset.quick;
        if (kind === 'item') {
          var add = $('addItemBtn');
          if (add) add.click();
        } else if (kind === 'group') {
          var addGroup = $('addGroupBtn');
          if (addGroup) addGroup.click();
        } else {
          goTab(kind);
        }
      });
    }

    // 窗口尺寸变化时重绘环形图（canvas 依赖像素尺寸）
    var resizeTimer = null;
    window.addEventListener('resize', function () {
      if (resizeTimer) clearTimeout(resizeTimer);
      resizeTimer = setTimeout(function () {
        if (lastDonut.total) drawDonut(lastDonut.list, lastDonut.total);
      }, 180);
    });
  }

  /* ================= 启动 ================= */
  function boot() {
    initSkin();
    initShell();
    initChrome();
    initDashboardActions();
    hookLoaders();

    try {
      new MutationObserver(lockConsoleStyle).observe(document.documentElement, {
        attributes: true,
        attributeFilter: ['data-style']
      });
    } catch (e) { /* 忽略 */ }

    refreshDashboard();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();

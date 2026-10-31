/**
 * 后台界面选择：新旧两套后台并存，偏好跟随账号存储在后端
 * - 从未选择 / 镜像更新后首次登录：弹窗选择（经典后台 or 新版控制台）
 * - 选择写入 users.admin_ui + admin_ui_ver，跨设备跟随账号
 * - 任一后台内均可随时切换；localStorage 仅作加速缓存，服务端为准
 */
(function () {
  'use strict';

  var KEY = 'sp_admin_ui';
  var VER_KEY = 'sp_admin_ui_ver';
  var FILE = { console: 'admin.html', classic: 'admin-classic.html' };
  var LABEL = { console: '新版控制台', classic: '经典后台' };
  var CURRENT = /admin-classic/.test(location.pathname) ? 'classic' : 'console';
  var busy = false;

  function version() {
    try {
      return (typeof APP_VERSION !== 'undefined' && APP_VERSION) || '';
    } catch (e) {
      return '';
    }
  }

  /** 本地缓存（仅加速首屏跳转，真实值以后端为准） */
  function cacheGet() {
    try {
      var ui = localStorage.getItem(KEY);
      var ver = localStorage.getItem(VER_KEY);
      if ((ui === 'classic' || ui === 'console') && ver === version()) return ui;
    } catch (e) { /* 忽略 */ }
    return '';
  }

  function cacheSet(ui) {
    try {
      localStorage.setItem(KEY, ui);
      localStorage.setItem(VER_KEY, version());
    } catch (e) { /* 隐私模式忽略 */ }
  }

  function cacheClear() {
    try {
      localStorage.removeItem(KEY);
      localStorage.removeItem(VER_KEY);
    } catch (e) { /* 忽略 */ }
  }

  function notify(msg, type) {
    try {
      if (typeof toast === 'function') toast(msg, type || 'error');
    } catch (e) { /* 忽略 */ }
  }

  /** 读取当前账号的偏好（/api/auth.php?action=me 已带 admin_ui） */
  function readPref() {
    if (typeof API === 'undefined' || !API.get) return Promise.resolve(null);
    return API.get('/api/auth.php?action=me');
  }

  /** 保存偏好到后端，成功后同步本地缓存 */
  function savePref(ui) {
    if (typeof API === 'undefined' || !API.post) {
      cacheSet(ui);
      return Promise.resolve();
    }
    return API.post('/api/user.php?action=ui_pref', { ui: ui }).then(function (res) {
      cacheSet(ui);
      return res;
    });
  }

  function previewMock(ui) {
    if (ui === 'console') {
      return '<span class="ui-mock ui-mock-console">' +
        '<span class="ui-mock-side"></span>' +
        '<span class="ui-mock-main"><i></i><i></i><i></i><i></i></span>' +
        '</span>';
    }
    return '<span class="ui-mock ui-mock-classic">' +
      '<span class="ui-mock-bar"></span>' +
      '<span class="ui-mock-body"><i></i><i></i><i></i></span>' +
      '</span>';
  }

  function showChooser() {
    if (document.getElementById('uiChooser')) return;

    var root = document.createElement('div');
    root.className = 'ui-chooser';
    root.id = 'uiChooser';
    root.innerHTML =
      '<div class="ui-chooser-card" role="dialog" aria-modal="true" aria-labelledby="uiChooserTitle">' +
        '<h2 id="uiChooserTitle">选择后台界面</h2>' +
        '<p class="ui-chooser-sub">新旧两套后台同时保留，选择一套作为默认。之后可在任一后台随时切换，偏好会跟随账号。</p>' +
        '<div class="ui-chooser-grid">' +
          '<button class="ui-opt" type="button" data-ui="console">' +
            previewMock('console') +
            '<span class="ui-opt-title">' + LABEL.console + '<em>推荐</em></span>' +
            '<span class="ui-opt-desc">左侧导航 + 系统状态工作台，支持三种配色与深色模式。</span>' +
          '</button>' +
          '<button class="ui-opt" type="button" data-ui="classic">' +
            previewMock('classic') +
            '<span class="ui-opt-title">' + LABEL.classic + '</span>' +
            '<span class="ui-opt-desc">原版横向标签布局，沿用此前所有操作习惯。</span>' +
          '</button>' +
        '</div>' +
        '<p class="ui-chooser-foot">选择会记录到账号；镜像更新后的首次登录会再次询问。</p>' +
      '</div>';

    root.addEventListener('click', function (e) {
      var btn = e.target.closest ? e.target.closest('.ui-opt') : null;
      if (!btn || busy) return;
      busy = true;
      var ui = btn.getAttribute('data-ui');
      savePref(ui).then(function () {
        busy = false;
        if (ui === CURRENT) {
          root.parentNode && root.parentNode.removeChild(root);
        } else {
          location.href = FILE[ui];
        }
      }).catch(function () {
        busy = false;
        notify('保存界面偏好失败，请重试');
      });
    });

    document.body.appendChild(root);
  }

  function ensureChooser() {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', showChooser);
    } else {
      showChooser();
    }
  }

  /** 以服务端为准：版本一致且已选 → 跳到对应页；未选或版本更新 → 弹窗 */
  function syncFromServer() {
    readPref().then(function (u) {
      if (!u || !u.role || u.role === 'guest') return;

      var ui = u.admin_ui || '';
      var ver = u.admin_ui_ver || '';
      if (ui && ver === version()) {
        cacheSet(ui);
        if (ui !== CURRENT) location.replace(FILE[ui]);
        return;
      }
      // 从未选择，或镜像更新（版本号变化）→ 重新询问
      cacheClear();
      ensureChooser();
    }).catch(function () {
      // 网络/接口异常时保持当前页，避免把用户挡在外面
    });
  }

  // 切换按钮：保存偏好后跳到另一套后台
  document.addEventListener('click', function (e) {
    var btn = e.target.closest ? e.target.closest('[data-ui-switch]') : null;
    if (!btn || busy) return;
    e.preventDefault();
    var ui = btn.getAttribute('data-ui-switch');
    if (ui !== 'classic' && ui !== 'console') return;
    busy = true;
    savePref(ui).then(function () {
      busy = false;
      if (ui !== CURRENT) location.href = FILE[ui];
    }).catch(function () {
      busy = false;
      notify('切换失败，请重试');
    });
  });

  // 缓存命中且与当前页不符：先快速跳转，避免整页闪烁
  var cached = cacheGet();
  if (cached && cached !== CURRENT) {
    location.replace(FILE[cached]);
    return;
  }

  syncFromServer();
})();

/**
 * 前端公共：API 请求封装 + 工具函数
 */

/* ================= 应用版本与更新日志（每次更新只需改这里） ================= */
const APP_VERSION = 'v3.1.2';
const APP_CHANGELOG = [
  { ver: 'v3.1.2', date: '2026-10-24', items: [
    '🎨 新增 6 套主题：清透极简、柔和工具、动态极光、IRIS 光圈、复古怀旧、企业简洁',
    '✨ 动态主题支持鼠标跟随光晕与卡片聚光效果',
    '🌙 深色模式自动为深色图标添加浅色底板，图标更清晰',
    '🐛 修复热点新闻关闭后主页入口仍显示的问题',
    '🐛 修复热点新闻开启后仍提示未开启的问题',
    '🐛 修复天气组件偶发不显示的问题',
    '✅ 热点新闻数据源新增全选 / 反选操作',
  ]},
  { ver: 'v3.1.1', date: '2026-10-24', items: [
    '🔐 安全性加固：完善接口权限、上传与备份校验、访问控制',
    '🐛 修复分组、卡片、设置与备份相关的若干异常行为',
    '⚡ 优化列表查询与首页渲染性能，清理冗余代码',
    '🎨 主题与样式细节更新',
  ]},
  { ver: 'v3.1.0', date: '2026-10-23', items: [
    '🗂️ 首次启动改为空库启动：不再注入预置分组与卡片，只写入默认设置与管理员账号',
    '🔢 默认端口 10080 → 10086，规避 Chromium 受限端口导致的 ERR_UNSAFE_PORT',
    '🌲 分组管理改为树形展示：二级分组紧跟所属一级分组，排序与显示/隐藏均在同级内生效',
    '🐛 修复分组「上移」无效（后端未读取 direction 参数），改挂一级分组时自动排到新父级末尾',
    '🐛 修复审计日志缺少时间：写入时补齐 created_at，并按时间 + id 稳定倒序',
    '💾 备份导入改为整体替换（清空分组/卡片/设置后重建），分组 ID 重映射，文件路径相对 uploads 校验',
    '⚡ favicon 增加 ETag / Last-Modified 协商缓存，二次访问返回 304',
    '🐳 Dockerfile 移除 initdb 预置库步骤，三架构镜像保持约 14MB',
    '📝 README 全量重写：完整路由表、端口说明、备份格式、开发注意事项',
  ]},
  { ver: 'v3.0.0', date: '2026-10-21', items: [
    '⚙️ 架构重写：Go 纯后端（chi + modernc.org/sqlite，零 CGO 单二进制），彻底废弃 PHP 版本',
    '🔌 Docker 单阶段 Alpine 镜像，体积 18MB，依赖项从 50+ 降到 8',
    '🎨 主题体系精简为 soft 柔和浮雕（默认）+ blueprint 工程蓝图（深蓝底 · 白线网格 · 等宽字 · 橙色标注）',
    '🗂️ 两级分组 + 预置 144 张 WebStack 卡片（灵感采集 / 素材资源 / 常用工具 / 学习教程 / 社区资讯 / UED团队）',
    '📰 热点新闻（微博 / 知乎 / B站 / GitHub Trending / IT之家 / V2EX / 少数派等 24 源），后台可独立开关',
    '🛠️ 7.2 节架构升级：设置中心化、前端构建工具链（零依赖 Node 脚本自动注入 SHA1 戳）、主题系统配置化、轻量 OTA（schema_versions 有序幂等迁移）',
    '🔐 CSRF + session + 限流 + 审计 chi middleware 化，支持 trusted_devices 受信设备',
    '🔁 跨标签实时同步：后台设置保存后，已打开的主页自动拉新数据重渲染（localStorage storage 事件）',
    '🐛 修复：SQLite WAL 锁残留、backfillParentIDs 读锁死锁、新闻数据源重复渲染、装饰层伪元素拦截按钮点击、下拉框弹窗深色适配',
  ]},
];

const API = {
  /**
   * 从 cookie 读取 CSRF 令牌（由后端 auth_session_start 以非 HttpOnly cookie 下发）
   */
  _csrfToken() {
    const m = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  },
  /**
   * @param {string} path 接口地址（相对 frontend 目录）
   * @param {{method?:string, data?:object, form?:FormData}} opts
   */
  async request(path, opts = {}) {
    const init = { method: opts.method || 'GET', credentials: 'same-origin', headers: {} };
    // 写请求携带 CSRF 令牌（同步令牌模式，后端按 session 中的值校验）
    if (init.method !== 'GET') {
      const tok = API._csrfToken();
      if (tok) init.headers['X-CSRF-Token'] = tok;
    }
    if (opts.form) {
      init.body = opts.form;
    } else if (opts.data !== undefined) {
      init.headers['Content-Type'] = 'application/json';
      init.body = JSON.stringify(opts.data);
    }
    let res;
    try {
      res = await fetch(path, init);
    } catch (e) {
      throw new Error('网络请求失败，请检查服务是否可用');
    }
    let j;
    try {
      j = await res.json();
    } catch (e) {
      // 响应不是 JSON：通常是反向代理在到达服务前拦截（如 Nginx 413、502 错误页）
      if (res.status === 413) {
        throw new Error('上传内容过大，被反向代理拦截（HTTP 413）。若前置了 Nginx，请调大 client_max_body_size（如 client_max_body_size 512m;）后重试');
      }
      let snippet = '';
      try {
        snippet = (await res.text()).replace(/\s+/g, ' ').trim().slice(0, 150);
      } catch (_) { /* ignore */ }
      throw new Error('接口返回异常（HTTP ' + res.status + (snippet ? '）：' + snippet : '）'));
    }
    if (j.code !== 0) {
      const err = new Error(j.msg || '请求失败');
      err.code = j.code;
      err.data = j.data; // 业务失败仍可能携带明细数据（如升级部分失败的文件列表）
      throw err;
    }
    return j.data;
  },
  get(path) {
    return API.request(path);
  },
  post(path, data) {
    return API.request(path, { method: 'POST', data: data || {} });
  },
  postForm(path, form) {
    return API.request(path, { method: 'POST', form });
  },
};

/** HTML 转义 */
function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/** 轻提示 */
let _toastTimer = null;
function toast(msg, type) {
  let box = document.getElementById('toast');
  if (!box) {
    box = document.createElement('div');
    box.id = 'toast';
    document.body.appendChild(box);
  }
  box.textContent = msg;
  box.className = 'toast show ' + (type || 'info');
  clearTimeout(_toastTimer);
  _toastTimer = setTimeout(() => {
    box.className = 'toast';
  }, 2600);
}

/**
 * 解析资源地址：后端返回的 /frontend/uploads/... 为站点根路径；
 * 主页位于站点根目录，根部署时直接原样使用；
 * 若部署在子路径（主页 URL 形如 /xxx/index.html），则以页面所在目录为基准自动补全
 */
function assetUrl(u) {
  if (!u) return '';
  if (/^(https?:)?\/\//i.test(u) || /^data:/i.test(u)) return u;
  if (u.startsWith('/frontend/')) {
    if (location.pathname === '/' || /^\/index\.html$/.test(location.pathname)) return u;
    const dir = location.pathname.replace(/[^/]*$/, ''); // 形如 .../（子路径部署）
    return dir + u.slice('/frontend/'.length);
  }
  return u;
}

/** 本站卡片图标的服务端代理基址（Go 版路由挂在 /api/；与 index.js 的 API_BASE 同值） */
const FAVICON_PROXY_BASE = '/api/';

/**
 * 站点图标候选源（客户端按序回退）：
 * 首位走本站服务端代理（命中本地缓存秒回、一年强缓存，未命中由服务端抓取后缓存），
 * 代理不可用时再由现有 onerror 链串行回退到公共图标服务。
 */
function faviconSources(url) {
  try {
    const host = new URL(url).hostname;
    return [
      FAVICON_PROXY_BASE + 'favicon.php?host=' + encodeURIComponent(host),
      'https://favicon.cccyun.cc/' + host,
      'https://icon.horse/icon/' + host,
      'https://favicon.im/' + host + '?larger=true',
      'https://www.google.com/s2/favicons?domain=' + host + '&sz=64',
    ];
  } catch (e) {
    return [];
  }
}

/** 根据网址获取站点 favicon 地址（首选源） */
function faviconUrl(url) {
  const s = faviconSources(url);
  return s.length ? s[0] : '';
}

/* ================= 文字图标工具（卡片 icon_type=text 专用） ================= */

/** 智能截断：含中文取前 4 码点，纯英文/数字/符号取前 12 码点 */
function smartIconText(title) {
  if (!title) return '';
  const chars = Array.from(String(title).trim());
  if (!chars.length) return '';
  const hasCN = chars.some(c => /[\u3400-\u9fff]/.test(c));
  return chars.slice(0, hasCN ? 4 : 12).join('');
}

/** 判断中文布局还是英文布局（含任何中文 → cn；全英文/数字/符号 → en） */
function textLayout(text) {
  if (!text) return 'en';
  const cnCount = Array.from(text).filter(c => /[\u3400-\u9fff]/.test(c)).length;
  return cnCount > 0 ? 'cn' : 'en';
}

/**
 * 渲染文字型图标
 * @param {HTMLElement} el        图标容器（已渲染到 DOM，可测 clientWidth）
 * @param {string}      text      已 smartIconText 截断好的文字
 * @param {number}      baseSize  字号上限（不同容器给不同值：主页 21 / 弹窗 18）
 * @param {number}     [boxWidth] 容器固定宽度（主页卡片 52/58）。传入后直接按固定 padding
 *                                计算内容宽，免 getComputedStyle/clientWidth 强制布局；
 *                                不传则维持运行时测量（后台预览等尺寸不固定的容器）
 */
function renderTextIcon(el, text, baseSize, boxWidth) {
  el.innerHTML = '';
  el.classList.remove('icon-grid-cn', 'icon-wrap-en');
  if (!text) { el.textContent = '?'; return; }

  const chars = Array.from(text);
  const layout = textLayout(text);
  el.classList.add(layout === 'cn' ? 'icon-grid-cn' : 'icon-wrap-en');

  let innerW;
  if (boxWidth) {
    // padding 与 .icon-grid-cn(5px) / .icon-wrap-en(4px) 的 CSS 一致
    innerW = Math.max(10, boxWidth - (layout === 'cn' ? 10 : 8));
  } else {
    // ★ 关键：读真实 padding，算内容区实际可用宽度
    const cs = getComputedStyle(el);
    const padH = (parseFloat(cs.paddingLeft) || 0) + (parseFloat(cs.paddingRight) || 0);
    innerW = Math.max(10, ((el.clientWidth || 52) - padH));
  }

  let size;
  if (layout === 'cn') {
    // 中文 2 列 grid，每格 glyph ≈ innerW/2 → font-size ≈ 0.82 × 格子宽
    size = Math.floor((innerW / 2) * 0.82);
  } else {
    // 英文 4 列 grid，每格 glyph ≈ innerW/4 → font-size ≈ 1.35 × 格子宽
    // 英文字母（M/W）宽度 ≈ 0.7 × fontSize，需留足空间避免溢出
    size = Math.floor((innerW / 4) * 1.35);
  }
  size = Math.max(7, Math.min(baseSize, size));
  el.style.fontSize = size + 'px';

  chars.forEach(c => {
    const s = document.createElement('span');
    s.className = 'child-text';
    s.textContent = c;
    el.appendChild(s);
  });
}

/**
 * 脏数据检测：icon_type=text 但 icon_value 里存的是图片 URL
 */
function isImageUrlLike(s) {
  if (!s) return false;
  const v = String(s).trim();
  if (!v) return false;
  if (/^https?:\/\//i.test(v)) return true;
  if (/^data:image\//i.test(v)) return true;
  if (/\/uploads\//i.test(v)) return true;
  if (/\/favicon/i.test(v)) return true;
  if (/^\/[^?#]*\.(png|jpe?g|gif|webp|svg|ico|bmp|avif)(\?|$)/i.test(v)) return true;
  if (/\.(png|jpe?g|gif|webp|svg|ico|bmp|avif)(\?|$)/i.test(v) && v.indexOf(' ') === -1) return true;
  return false;
}

/** 颜色合成：透明色存 hex，半透明存 rgba */
function buildBgCss(color, alphaPercent) {
  if (!color) return '';
  if (alphaPercent >= 100) return color.startsWith('#') ? color : color;
  let h = color.trim();
  if (!h.startsWith('#')) return color;
  h = h.slice(1);
  if (h.length === 3) h = h.split('').map(c => c + c).join('');
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `rgba(${r},${g},${b},${(alphaPercent / 100).toFixed(2)})`;
}

/** 可选风格主题（与 themes.css 的 html[data-style] 对应） */
const SP_STYLES = ['soft', 'aurora', 'blueprint', 'clarity', 'corporate-clean', 'iris', 'retro-vintage', 'sketch-style', 'soft-utility'];

/** 应用深浅模式（light/dark/system），system 实时跟随系统深浅 */
function setThemeMode(mode) {
  const root = document.documentElement;
  root.dataset.mode = mode === 'system' ? 'system' : mode;
  const dark = window.matchMedia('(prefers-color-scheme: dark)');
  root.dataset.theme = mode === 'system' ? (dark.matches ? 'dark' : 'light') : mode;
}

/**
 * IRIS 风格：鼠标跟随晕光。把指针位置平滑写入 --sp-mx / --sp-my，
 * 由 themes.css 的 .style-fx 径向渐变消费；仅在 iris + 精细指针 + 未减弱动效时启用。
 */
function spIrisField() {
  const root = document.documentElement;
  if (root.dataset.style !== 'iris' || window.__spIrisField) return;
  if (!window.matchMedia || !window.matchMedia('(pointer: fine)').matches) return;
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  window.__spIrisField = true;

  let targetX = 72, targetY = 22, curX = 72, curY = 22, trailX = 72, trailY = 22, raf = 0;
  const loop = () => {
    curX += (targetX - curX) * 0.07;
    curY += (targetY - curY) * 0.07;
    // 第二层拖尾光晕：更慢的跟随，形成流动的视差层次，避免单点"手电筒"感
    trailX += (curX - trailX) * 0.035;
    trailY += (curY - trailY) * 0.035;
    root.style.setProperty('--sp-mx', curX.toFixed(2) + '%');
    root.style.setProperty('--sp-my', curY.toFixed(2) + '%');
    root.style.setProperty('--sp-mx2', trailX.toFixed(2) + '%');
    root.style.setProperty('--sp-my2', trailY.toFixed(2) + '%');
    // 收敛后停止 rAF，避免空闲时持续耗电
    if (Math.abs(targetX - curX) < 0.05 && Math.abs(targetY - curY) < 0.05 &&
        Math.abs(curX - trailX) < 0.05 && Math.abs(curY - trailY) < 0.05) { raf = 0; return; }
    raf = requestAnimationFrame(loop);
  };
  window.addEventListener('pointermove', (e) => {
    targetX = (e.clientX / window.innerWidth) * 100;
    targetY = (e.clientY / window.innerHeight) * 100;
    if (!raf) raf = requestAnimationFrame(loop);
  }, { passive: true });
}

/** 指针跟随型主题（卡片内聚光）：把指针在卡片内的相对位置写入 --px / --py */
function spCardPointerGlow() {
  const style = document.documentElement.dataset.style;
  if ((style !== 'iris' && style !== 'aurora') || window.__spCardPointerGlow) return;
  if (!window.matchMedia || !window.matchMedia('(pointer: fine)').matches) return;
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  window.__spCardPointerGlow = true;

  document.addEventListener('pointermove', (e) => {
    const card = e.target && e.target.closest ? e.target.closest('.card') : null;
    if (!card) return;
    const r = card.getBoundingClientRect();
    if (!r.width || !r.height) return;
    card.style.setProperty('--px', (((e.clientX - r.left) / r.width) * 100).toFixed(1) + '%');
    card.style.setProperty('--py', (((e.clientY - r.top) / r.height) * 100).toFixed(1) + '%');
  }, { passive: true });
}

/**
 * 图片图标色调识别：读取图片平均亮度，在容器上标记 data-icon-tone="dark|light|mid"。
 * 供深色模式给黑色/深色 logo 自动加浅色底板（跨域图片无法读像素时静默跳过，由 CSS 光晕兜底）。
 */

function spDetectIconTone(img, box) {
  if (!img || !box || box.dataset.iconTone) return;
  const run = () => {
    if (box.dataset.iconTone || !img.naturalWidth) return;
    try {
      const size = 24;
      const cv = document.createElement('canvas');
      cv.width = cv.height = size;
      const ctx = cv.getContext('2d', { willReadFrequently: true });
      ctx.drawImage(img, 0, 0, size, size);
      const data = ctx.getImageData(0, 0, size, size).data;
      let sum = 0;
      let count = 0;
      for (let i = 0; i < data.length; i += 4) {
        if (data[i + 3] < 24) continue; // 跳过透明像素
        sum += 0.2126 * data[i] + 0.7152 * data[i + 1] + 0.0722 * data[i + 2];
        count++;
      }
      if (!count) return;
      const lum = sum / count / 255;
      box.dataset.iconTone = lum < 0.38 ? 'dark' : (lum > 0.86 ? 'light' : 'mid');
    } catch (e) { /* 跨域图片 canvas 被污染，忽略 */ }
  };
  if (img.complete && img.naturalWidth) run();
  else img.addEventListener('load', run, { once: true });
}

/** 主题：localStorage 手动锁定 > 后端默认设置；风格主题由 theme_style 决定；返回当前模式 */
function applyTheme(settings) {
  const root = document.documentElement;
  const style = settings && SP_STYLES.includes(settings.theme_style) ? settings.theme_style : 'soft';
  root.dataset.style = style;
  try { localStorage.setItem('sp_style', style); } catch (e) { /* 隐私模式忽略 */ }

  const saved = localStorage.getItem('sp_theme');
  const mode = saved === 'light' || saved === 'dark' || saved === 'system'
    ? saved
    : (settings && ['light', 'dark', 'system'].includes(settings.default_theme) ? settings.default_theme : 'dark');
  setThemeMode(mode);
  spIrisField();
  spCardPointerGlow();

  // 跟随系统：系统深浅变化时实时切换（仅绑定一次）
  if (!window.__spSysThemeHook) {
    window.__spSysThemeHook = true;
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
      if (document.documentElement.dataset.mode === 'system') {
        setThemeMode('system');
        if (typeof updateThemeBtn === 'function' && document.getElementById('themeBtn')) updateThemeBtn();
      }
    });
  }
  return mode;
}

/* ================= PWA Service Worker 注册 ================= */
/** 注册 SW，仅 https 或 localhost 环境下启用。
 *  Docker 版：不显示 update-ready 提示（镜像更新靠 docker pull + 重启，用户自己知道），
 *  updatefound 事件仅静默等下次刷新生效；skipWaiting POST 保留以备未来手动更新。
 */
function spRegisterSW() {
  if (!('serviceWorker' in navigator)) return;
  // 安全上下文：HTTPS / localhost / 127.0.0.1 / 内网私有 IP 段（自托管场景）
  const hn = location.hostname;
  const isPrivateIP = /^(10\.\d+\.\d+\.\d+|172\.(1[6-9]|2\d|3[01])\.\d+\.\d+|192\.168\.\d+\.\d+|\[?[fF][cdCD][0-9a-fA-F:]+\]?)$/.test(hn);
  const isSecure = location.protocol === 'https:' || hn === 'localhost' || hn === '127.0.0.1' || isPrivateIP;
  if (!isSecure) return;
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('sw.js').then(reg => {
      reg.addEventListener('updatefound', () => {
        const newSW = reg.installing;
        if (!newSW) return;
        newSW.addEventListener('statechange', () => {
          if (newSW.state === 'installed' && navigator.serviceWorker.controller) {
            // 有新版本可用，显示更新提示
            const box = document.getElementById('updateReady');
            if (box) {
              box.hidden = false;
              const btn = document.getElementById('updateApplyBtn');
              if (btn && !btn._bound) {
                btn._bound = true;
                btn.onclick = () => {
                  navigator.serviceWorker.getRegistration().then(r => {
                    if (r && r.waiting) {
                      r.waiting.postMessage({ type: 'SKIP_WAITING' });
                      r.waiting.addEventListener('statechange', () => {
                        if (r.waiting.state === 'activated') location.reload();
                      });
                    }
                  });
                };
              }
            }
          }
        });
      });
    }).catch(err => console.warn('[PWA] SW register failed:', err));
    // 接收已激活 SW 的 postMessage（SKIP_WAITING 触发后 reload）
    navigator.serviceWorker.addEventListener('message', ev => {
      if (ev.data && ev.data.type === 'CLIENTS_CLAIM') location.reload();
    });
  });
}
// 立即尝试注册（api.js 被所有页面加载，统一入口）
spRegisterSW();

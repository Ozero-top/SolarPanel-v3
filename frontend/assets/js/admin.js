/**
 * 管理后台逻辑
 */
const API_BASE = '/api/';

const state = {
  groups: [],
  items: [],
  currentGroup: 0, // 0 = 全部
  currentParent: 0, // 一级分组筛选，0 = 全部
  itemKeyword: '', // 卡片搜索关键字（标题 / 网址）
  itemPage: 1, // 卡片列表当前页码（每页 ITEM_PAGE_SIZE 条）
  editingItem: null,
  groupsOrderDirty: false, // 分组拖拽后有未保存的排序
  siteUrl: '', // 后台设置的站点地址（退出登录跳转用）
  role: 'admin', // 当前登录用户权限组：admin / editor / viewer
  pendingTabSwitch: null, // 未保存排序弹窗挂起的切换目标（tab 标签页模块）
  auditPage: 1,     // 审计日志分页状态
  auditTotal: 0,
  wpList: [],       // 壁纸图库列表（内置 + 上传）
  wpTab: 'uploaded', // 图库当前分区：uploaded / preset
};

const ITEM_PAGE_SIZE = 10; // 卡片管理每页条数

const ROLE_LABEL = { admin: '管理员', editor: '编辑者', viewer: '只读' };

/* —— 模块级常量（集中声明，避免 section 间散落） —— */
const OPEN_TEXT = { 1: '当前页', 2: '新窗口', 3: '弹层' };

/* ================= 初始化 ================= */
(async function boot() {
  try {
    const u = await API.get(API_BASE + 'auth.php?action=me');
    if (!u) {
      location.replace('login.html');
      return;
    }
    state.role = u.role || 'admin';
    // 访客密码解锁后不能直接进入后台管理面板
    if (state.role === 'guest') {
      toast('访客权限不足，请使用管理员账号登录', 'error');
      location.replace('login.html');
      return;
    }
    document.getElementById('whoami').textContent = u.name || u.username;
    document.getElementById('accName').value = u.name || '';
    const accUser = document.getElementById('accUsername');
    const accBadge = document.getElementById('accRoleBadge');
    if (accUser) accUser.textContent = u.username || '—';
    if (accBadge) {
      accBadge.textContent = ROLE_LABEL[state.role] || state.role;
      accBadge.classList.add('role-' + state.role);
    }
  } catch (e) {
    if (e.code === 401) {
      location.replace('login.html');
      return;
    }
    toast(e.message, 'error');
  }

  applyThemeFromBackend();
  bindTabs();
  bindOrderGuard();
  bindModals();
  bindUploads();
  await loadGroups();
  await loadItems();
  bindItems();
  bindGroups();
  bindDragSort();
  bindSettings();
  bindSettingsSubnav();
  bindNewsSourceDrag();
  bindAccount();
  bindBackup();
  bindClip();
  renderVersionInfo();
  bindUpdateCheck();
  applyRoleUI();

  // —— 2MD: 新增 5 个功能模块 ——
  bindAudit();          // 审计日志（仅 admin）
  bindFeeds();          // 自定义 RSS 源
  bindImportBookmarks();// 书签导入
  bindTwoFA();          // 两步验证
  bindDailyWallpaper(); // 每日壁纸源下拉
})();

/* ================= 按权限组显示 / 隐藏后台功能 ================= */
function applyRoleUI() {
  if (state.role === 'viewer' || state.role === 'guest') {
    // 只读 / 访客：可查看全部标签页与所有设置，但一切写操作禁用
    document.body.classList.add('role-viewer');
    if (state.role === 'guest') document.body.classList.add('role-guest');
    // 用户管理区块隐藏（不能增删用户 / 改权限）
    const um = document.getElementById('userManageBlock');
    if (um) um.style.display = 'none';
    // 审计日志：viewer/guest 无权查看敏感日志
    const auditTab = document.querySelector('.admin-tabs .tab[data-tab="audit"]');
    if (auditTab) auditTab.style.display = 'none';
    // 剪藏与接入：只读账号不能创建 Token，直接隐藏
    const clipTab = document.querySelector('.admin-tabs .tab[data-tab="clip"]');
    if (clipTab) clipTab.style.display = 'none';
    // 书签导入、2FA 开关（viewer 只读，访客也无权修改）
    document.querySelectorAll('#openBookmarksBtn, #twofaSetupBtn, #twofaEnableBtn, #twofaDisableBtn').forEach(el => {
      el.disabled = true;
      el.style.opacity = '0.5';
      el.style.pointerEvents = 'none';
    });
    // 站点设置：所有表单控件禁用（灰色不可操作），但保留二级导航可切换查看
    document.querySelectorAll('#tab-settings input, #tab-settings select, #tab-settings textarea, #tab-settings button, #tab-backup select, #tab-backup button').forEach(el => {
      if (el.closest('.settings-subnav')) return; // 分区切换按钮保留可用
      if (el.id === 'changelogHistoryBtn') return;    // 更新日志为只读信息，历史更新按钮保留可用
      el.disabled = true;
    });
    // 账号页：禁用修改密码与显示名称（只读账号无写权限）
    document.querySelectorAll('#pwdForm input, #pwdForm button, #accName, #saveNameBtn').forEach(el => { el.disabled = true; });
    // 访客隐藏账号页的修改入口
    if (state.role === 'guest') {
      document.querySelectorAll('#tab-account .btn, #tab-account input:not([readonly])').forEach(el => { el.disabled = true; });
    }
  } else if (state.role !== 'admin') {
    // 编辑者：不可见站点设置与用户管理（内容管理可用）
    const settingsTab = document.querySelector('.admin-tabs .tab[data-tab="settings"]');
    if (settingsTab) settingsTab.style.display = 'none';
    // 系统备份：仅管理员（导入 / 清空为高危写操作）
    const backupTab = document.querySelector('.admin-tabs .tab[data-tab="backup"]');
    if (backupTab) backupTab.style.display = 'none';
    // 审计日志：仅管理员可见（敏感信息）
    const auditTab = document.querySelector('.admin-tabs .tab[data-tab="audit"]');
    if (auditTab) auditTab.style.display = 'none';
    // 剪藏设置：改全站开关/默认分组仅管理员，编辑者仍可管理自己的 Token
    document.querySelectorAll('#tab-clip #s_clip_enabled, #tab-clip #s_clip_default_group, #tab-clip #s_clip_default_capture, #tab-clip #s_clip_ai_enabled, #tab-clip #s_clip_ai_provider, #tab-clip #s_clip_ai_base_url, #tab-clip #s_clip_ai_api_key, #tab-clip #s_clip_ai_model, #tab-clip #s_clip_image_domains, #tab-clip #saveClipSettingsBtn').forEach(el => { el.disabled = true; });
    // 默认页签为站点设置，编辑者不可见 → 回退到卡片管理
    const firstTab = document.querySelector('.admin-tabs .tab[data-tab="items"]');
    if (firstTab) activateTab(firstTab);
    // 用户管理区块仅管理员可见（账号页保留：修改名称 / 修改密码）
    const um = document.getElementById('userManageBlock');
    if (um) um.style.display = 'none';
  }
}

async function applyThemeFromBackend() {
  try {
    const s = await API.get(API_BASE + 'settings.php?action=list');
    applyTheme(s);
    state.siteUrl = (s.site_url || '').trim();
    fillSettingsForm(s);
    applyUpdateSettings(s);
    applyBackupSettings(s);
  } catch (e) {
    toast('设置读取失败：' + e.message, 'error');
  }
}

/* ================= 标签页 ================= */
function activateTab(tab) {
  document.querySelectorAll('.admin-tabs .tab').forEach(t => t.classList.remove('active'));
  tab.classList.add('active');
  document.querySelectorAll('.admin-panel').forEach(p => { p.hidden = true; });
  document.getElementById('tab-' + tab.dataset.tab).hidden = false;
}

function bindTabs() {
  document.querySelectorAll('.admin-tabs .tab').forEach(tab => {
    tab.addEventListener('click', () => {
      // 有未保存的分组拖拽排序时，弹主题化确认弹窗（保存 / 放弃 / 留下）
      if (state.groupsOrderDirty && !tab.classList.contains('active')) {
        state.pendingTabSwitch = tab;
        document.getElementById('orderGuardModal').classList.add('show');
        return;
      }
      activateTab(tab);
      if (tab.dataset.tab === 'backup') loadBackupList();
      if (tab.dataset.tab === 'clip') loadClipPage();
    });
  });
  // 关闭 / 刷新页面时同样拦截未保存的排序（浏览器原生确认，无法样式化）
  window.addEventListener('beforeunload', e => {
    if (state.groupsOrderDirty) {
      e.preventDefault();
      e.returnValue = '';
    }
  });
}

/** 未保存排序确认弹窗：保存并切换 / 放弃并切换 / 留在此页 */
function bindOrderGuard() {
  const modal = document.getElementById('orderGuardModal');
  const close = () => { modal.classList.remove('show'); state.pendingTabSwitch = null; };
  document.getElementById('guardStayBtn').onclick = close;
  document.getElementById('guardDiscardBtn').onclick = () => {
    const tab = state.pendingTabSwitch;
    close();
    setGroupsOrderDirty(false);
    // 重新拉取数据，把内存中的脏顺序还原为已保存状态
    loadGroups();
    if (tab) activateTab(tab);
  };
  document.getElementById('guardSaveAndSwitchBtn').onclick = async () => {
    const tab = state.pendingTabSwitch;
    const btn = document.getElementById('guardSaveAndSwitchBtn');
    btn.disabled = true;
    try {
      if (state.groupsOrderDirty) await saveGroupsOrder();
      modal.classList.remove('show');
      state.pendingTabSwitch = null;
      toast('排序已保存', 'success');
      if (tab) activateTab(tab);
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      btn.disabled = false;
    }
  };
}

/* ================= 弹窗通用 ================= */
function bindModals() {
  document.querySelectorAll('[data-close]').forEach(btn => {
    btn.addEventListener('click', () => {
      document.getElementById(btn.dataset.close).classList.remove('show');
    });
  });
  // 仅轻量弹窗（confirm/帮助/更新日志）允许点击空白关闭；表单类弹窗（卡片/分组/密码等）禁用防滑动误触
  document.querySelectorAll('.modal[data-dismiss-bg]').forEach(m => {
    m.addEventListener('click', e => {
      if (e.target === m) m.classList.remove('show');
    });
  });
  document.getElementById('logoutBtn').addEventListener('click', async () => {
    try { await API.post(API_BASE + 'auth.php?action=logout'); } catch (e) { /* 忽略 */ }
    // 优先跳转后台设置的站点地址（域名/内网地址），未设置时回登录页
    location.href = state.siteUrl !== '' ? state.siteUrl : 'login.html';
  });
}

/* ================= 分组 ================= */
async function loadGroups() {
  try {
    state.groups = await API.get(API_BASE + 'groups.php?action=list');
    setGroupsOrderDirty(false);
  } catch (e) {
    toast('分组加载失败：' + e.message, 'error');
    state.groups = [];
  }
  renderGroupFilter();
  renderGroupsTable();
  renderGroupOptions();
}

/** 卡片筛选：一级分组 / 二级分组 / 关键字（国人习惯：先选大类，再看小类） */
function renderGroupFilter() {
  const parentSel = document.getElementById('itemParentFilter');
  const subSel = document.getElementById('itemSubFilter');
  if (!parentSel || !subSel) return;
  const groups = state.groups || [];
  const tops = groups.filter(g => (g.parent_id || 0) === 0);

  parentSel.innerHTML = '';
  parentSel.appendChild(new Option('全部分组', '0'));
  tops.forEach(g => {
    parentSel.appendChild(new Option(g.title + ' (' + (g.item_count || 0) + ')', String(g.id)));
  });
  if (!tops.some(g => g.id === state.currentParent)) state.currentParent = 0;
  parentSel.value = String(state.currentParent);

  const children = state.currentParent
    ? groups.filter(g => (g.parent_id || 0) === state.currentParent)
    : groups.filter(g => (g.parent_id || 0) !== 0);
  const parentName = {};
  groups.forEach(g => { parentName[g.id] = g.title; });

  subSel.innerHTML = '';
  subSel.appendChild(new Option(state.currentParent ? '全部（含子分组）' : '全部二级分组', '0'));
  children.forEach(g => {
    const prefix = state.currentParent ? '' : ((parentName[g.parent_id] || '?') + ' › ');
    subSel.appendChild(new Option(prefix + g.title + ' (' + (g.item_count || 0) + ')', String(g.id)));
  });
  if (!children.some(g => g.id === state.currentGroup)) state.currentGroup = 0;
  subSel.value = String(state.currentGroup);

  parentSel.onchange = () => {
    state.currentParent = Number(parentSel.value);
    state.currentGroup = 0;
    state.itemPage = 1;
    renderGroupFilter();
    renderItemsTable();
  };
  subSel.onchange = () => {
    state.currentGroup = Number(subSel.value);
    if (state.currentGroup) {
      const g = groups.find(x => x.id === state.currentGroup);
      if (g && (g.parent_id || 0)) state.currentParent = g.parent_id;
    }
    state.itemPage = 1;
    renderGroupFilter();
    renderItemsTable();
  };

  const search = document.getElementById('itemSearch');
  if (search) {
    search.value = state.itemKeyword || '';
    search.oninput = () => {
      state.itemKeyword = search.value.trim().toLowerCase();
      state.itemPage = 1;
      renderItemsTable();
    };
  }
  const reset = document.getElementById('itemFilterReset');
  if (reset) {
    reset.onclick = () => {
      state.currentParent = 0;
      state.currentGroup = 0;
      state.itemKeyword = '';
      state.itemPage = 1;
      if (search) search.value = '';
      renderGroupFilter();
      renderItemsTable();
    };
  }
}

function renderGroupOptions() {
  const sel = document.getElementById('i_group');
  sel.innerHTML = '';
  // 先缓存父分组名映射（给二级分组加前缀用）
  const parentMap = {};
  (state.groups || []).forEach(g => { if ((g.parent_id || 0) === 0) parentMap[g.id] = g.title; });
  (state.groups || []).forEach(g => {
    const opt = document.createElement('option');
    opt.value = String(g.id);
    const pid = g.parent_id || 0;
    opt.textContent = pid === 0 ? g.title : `└─ ${parentMap[pid] || '?'} › ${g.title}`;
    sel.appendChild(opt);
  });
}

/** 根据当前 parent_id 同步分组弹窗的「级别」和「所属一级」显示 */
function syncGroupLevel(currentParentId) {
  const levelSel = document.getElementById('g_level');
  const parentWrap = document.getElementById('g_parent_wrap');
  const parentSel = document.getElementById('g_parent_id');
  if (!levelSel) return;

  const pid = Number(currentParentId) || 0;
  levelSel.value = pid === 0 ? '0' : '1';
  parentWrap.style.display = pid === 0 ? 'none' : '';

  // 填充 parent 下拉（只列一级分组）
  const tops = (state.groups || []).filter(g => (g.parent_id || 0) === 0);
  parentSel.innerHTML = tops.map(t => `<option value="${Number(t.id)}">${esc(t.icon || '')} ${esc(t.title)}</option>`).join('');
  if (pid !== 0) parentSel.value = pid;
}

function bindGroupLevelSwitch() {
  const levelSel = document.getElementById('g_level');
  if (!levelSel) return;
  levelSel.addEventListener('change', () => {
    const parentWrap = document.getElementById('g_parent_wrap');
    parentWrap.style.display = levelSel.value === '0' ? 'none' : '';
  });
}

/* ---------- 分组预设图标库（9 大功能类别；n=显示短名，k=搜索关键词） ---------- */
const GROUP_ICON_CATS = [
  { c: '常用', list: [
    { i: '⭐', n: '星标', k: '推荐 收藏 喜欢' }, { i: '🏠', n: '首页', k: '主页 家' },
    { i: '👤', n: '个人', k: '用户 账户' }, { i: '🧭', n: '导航', k: '指南针 方向' },
    { i: '📌', n: '置顶', k: '图钉 标记' }, { i: '🔥', n: '热门', k: '火爆 趋势' },
    { i: '🆕', n: '最新', k: '新品 new' }, { i: '💎', n: '精品', k: '钻石 高级' },
    { i: '🎯', n: '目标', k: '靶心 专注' }, { i: '💡', n: '灵感', k: '灯泡 创意' },
    { i: '🌈', n: '趣味', k: '彩虹 多彩' }, { i: '🎁', n: '福利', k: '礼物 礼品' },
    { i: '⏰', n: '时间', k: '闹钟 时钟' }, { i: '📅', n: '日历', k: '日期 日程' },
    { i: '✅', n: '完成', k: '待办 对勾' }, { i: '🔰', n: '新手', k: '入门 初心' },
    { i: '🌟', n: '闪耀', k: '星星' }, { i: '🚩', n: '旗帜', k: '旗子' },
    { i: '📍', n: '位置', k: '定位' }, { i: '❤️', n: '红心', k: '喜欢 爱心' },
  ]},
  { c: '电脑硬件', list: [
    { i: '💻', n: '笔记本', k: '电脑 开发' }, { i: '🖥️', n: '显示器', k: '桌面 屏幕' },
    { i: '📱', n: '手机', k: '移动端' }, { i: '⌨️', n: '键盘', k: '输入' },
    { i: '🖱️', n: '鼠标', k: '外设' }, { i: '🖲️', n: '轨迹球', k: '' },
    { i: '🖨️', n: '打印机', k: '打印' }, { i: '📠', n: '传真机', k: '传真' },
    { i: '☎️', n: '座机', k: '电话' }, { i: '📞', n: '电话', k: '联系' },
    { i: '📟', n: '寻呼机', k: '终端 传呼' }, { i: '📺', n: '电视', k: '显示屏' },
    { i: '🧮', n: '计算器', k: '算盘' }, { i: '🔋', n: '电池', k: '电源 电量' },
    { i: '🔌', n: '插头', k: '充电' }, { i: '🔦', n: '手电筒', k: '照明' },
    { i: '🕹️', n: '摇杆', k: '街机' }, { i: '💾', n: '软盘', k: '保存' },
    { i: '💿', n: '光盘', k: '光碟' }, { i: '📀', n: 'DVD', k: '影碟' },
    { i: '💽', n: '磁盘', k: '唱片' }, { i: '📼', n: '录像带', k: '' },
    { i: '📡', n: '天线', k: '信号 路由' }, { i: '🕰️', n: '手表', k: '钟表' },
    { i: '⌛', n: '沙漏', k: '等待' }, { i: '⏳', n: '加载', k: '沙漏' },
    { i: '🧷', n: '别针', k: '安全' }, { i: '📲', n: '手机消息', k: '来电' },
    { i: '🔣', n: '符号', k: '符号输入' }, { i: '🔢', n: '数字', k: '数字输入' },
    { i: '🔠', n: '大写字母', k: '' }, { i: '🔡', n: '小写字母', k: '' },
    { i: '🅰️', n: 'A型', k: '字母A' }, { i: '🅱️', n: 'B型', k: '字母B' },
  ]},
  { c: '网络技术', list: [
    { i: '🌐', n: '万维网', k: '网站 外网 互联网' }, { i: '☁️', n: '云服务', k: '云端 云朵' },
    { i: '🔗', n: '链接', k: '网址 连接' }, { i: '📶', n: 'WiFi', k: '无线 信号' },
    { i: '🛰️', n: '卫星', k: '' }, { i: '🐙', n: 'GitHub', k: '章鱼 代码 托管' },
    { i: '🐧', n: 'Linux', k: '企鹅 系统' }, { i: '🐳', n: 'Docker', k: '容器 鲸鱼' },
    { i: '🦊', n: '火狐', k: '浏览器 Firefox' }, { i: '🤖', n: '机器人', k: 'AI 自动化' },
    { i: '🧠', n: '大脑', k: '智能' }, { i: '⚙️', n: '齿轮', k: '设置 配置' },
    { i: '🔧', n: '扳手', k: '工具 维修' }, { i: '🧪', n: '试管', k: '测试 实验' },
    { i: '🔬', n: '显微镜', k: '科学 研究' }, { i: '🔍', n: '放大镜', k: '搜索 查找' },
    { i: '🧩', n: '拼图', k: '插件 扩展' }, { i: '🔐', n: '安全锁', k: '内网 加密' },
    { i: '🔒', n: '挂锁', k: '私密 加密' }, { i: '🛡️', n: '盾牌', k: '安全 防护' },
    { i: '🔏', n: '签名', k: '加密' }, { i: '🔑', n: '钥匙', k: '密码 key' },
    { i: '🗝️', n: '密钥', k: '钥匙 SSH' }, { i: '🆔', n: '身份', k: 'ID 证件' },
    { i: '🆘', n: '帮助', k: 'SOS 紧急' }, { i: '🧬', n: 'DNA', k: '基因' },
    { i: '🦠', n: '病毒', k: '恶意软件' }, { i: '🪝', n: '钩子', k: 'hook' },
    { i: '⚛️', n: '原子', k: '物理 科学' }, { i: '🧫', n: '培养皿', k: '生物' },
    { i: '🕸️', n: '蜘蛛网', k: '网络 web' }, { i: '🧵', n: '线程', k: 'thread' },
    { i: '🌡️', n: '温度计', k: '温度' }, { i: '🔭', n: '望远镜', k: '观测' },
    { i: '📳', n: '震动', k: '振动' }, { i: '🪢', n: '绳结', k: '节点' },
    { i: '⚡', n: '闪电', k: '电力 高速' }, { i: '🌀', n: '漩涡', k: '异步' },
  ]},
  { c: '媒体影音', list: [
    { i: '🎬', n: '电影', k: '影视 影片' }, { i: '🎥', n: '摄影机', k: '视频 拍摄' },
    { i: '🎵', n: '音符', k: '音乐 歌曲' }, { i: '🎶', n: '旋律', k: '音乐' },
    { i: '🎧', n: '耳机', k: '播客 音频' }, { i: '🎙️', n: '麦克风', k: '录音' },
    { i: '🎤', n: '话筒', k: 'K歌 唱歌' }, { i: '📻', n: '收音机', k: '电台 广播' },
    { i: '🎼', n: '乐谱', k: '作曲' }, { i: '🎹', n: '钢琴', k: '琴键' },
    { i: '🥁', n: '鼓', k: '打击乐' }, { i: '🎺', n: '小号', k: '铜管' },
    { i: '🎻', n: '小提琴', k: '弦乐' }, { i: '🎷', n: '萨克斯', k: '管乐' },
    { i: '🎸', n: '吉他', k: '' }, { i: '🪕', n: '班卓琴', k: '' },
    { i: '🪘', n: '长鼓', k: '鼓' }, { i: '🎭', n: '戏剧', k: '面具 舞台' },
    { i: '🩰', n: '芭蕾', k: '舞蹈' }, { i: '💃', n: '舞蹈', k: '跳舞' },
    { i: '🕺', n: '舞者', k: '跳舞' }, { i: '🎮', n: '游戏', k: '手柄' },
    { i: '🎲', n: '骰子', k: '桌游 随机' }, { i: '🃏', n: '扑克', k: '桌游 卡牌' },
    { i: '🎴', n: '花札', k: '卡牌' }, { i: '🎳', n: '保龄球', k: '' },
    { i: '🍿', n: '爆米花', k: '休闲 追剧' }, { i: '🎞️', n: '胶片', k: '胶卷' },
    { i: '📽️', n: '放映机', k: '老电影' }, { i: '🔊', n: '喇叭', k: '音量' },
    { i: '🔇', n: '静音', k: '' }, { i: '🔉', n: '音量', k: '中音量' },
    { i: '🔈', n: '声音', k: '小音量' }, { i: '▶️', n: '播放', k: '开始' },
    { i: '⏯️', n: '播放暂停', k: '' }, { i: '⏭️', n: '下一首', k: '' },
    { i: '⏮️', n: '上一首', k: '' }, { i: '🔁', n: '循环', k: '重复' },
    { i: '🔂', n: '单曲循环', k: '' }, { i: '⏺️', n: '录制', k: '录像' },
    { i: '⏸️', n: '暂停', k: '' }, { i: '⏹️', n: '停止', k: '' },
    { i: '🔀', n: '随机播放', k: '随机' }, { i: '📯', n: '号角', k: '邮号' },
    { i: '⏩', n: '快进', k: '' }, { i: '⏪', n: '快退', k: '' },
  ]},
  { c: '图像摄影', list: [
    { i: '📸', n: '相机', k: '摄影 拍照' }, { i: '📷', n: '照相机', k: '拍照' },
    { i: '🖼️', n: '相框', k: '图片 相册' }, { i: '🎨', n: '调色板', k: '美术 设计' },
    { i: '🖌️', n: '画笔', k: '绘画' }, { i: '🖍️', n: '蜡笔', k: '涂色' },
    { i: '✏️', n: '铅笔', k: '书写' }, { i: '✒️', n: '钢笔尖', k: '写作' },
    { i: '🖊️', n: '签字笔', k: '书写' }, { i: '🌄', n: '日出', k: '风景' },
    { i: '🏞️', n: '山水', k: '风景' }, { i: '🌅', n: '晨曦', k: '日出' },
    { i: '🌠', n: '流星', k: '星空' }, { i: '🌌', n: '银河', k: '星空 夜景' },
    { i: '✨', n: '闪星', k: '特效 闪耀' }, { i: '🎆', n: '烟花', k: '焰火' },
    { i: '🎇', n: '焰火', k: '烟花' }, { i: '🌉', n: '夜桥', k: '夜景' },
    { i: '🏙️', n: '城市', k: '都市' }, { i: '🌃', n: '夜空', k: '夜晚' },
    { i: '🏔️', n: '雪山', k: '山峰' }, { i: '🌋', n: '火山', k: '岩浆' },
    { i: '🏝️', n: '海岛', k: '岛屿' }, { i: '🏖️', n: '沙滩', k: '海边' },
    { i: '🌊', n: '海浪', k: '大海' }, { i: '🌁', n: '雾景', k: '雾气' },
    { i: '🌆', n: '黄昏', k: '暮色' }, { i: '🌇', n: '夕阳', k: '日落城市' },
  ]},
  { c: '文件办公', list: [
    { i: '📁', n: '文件夹', k: '目录' }, { i: '📂', n: '打开夹', k: '目录' },
    { i: '🗂️', n: '归档', k: '分类' }, { i: '📋', n: '清单', k: '列表 任务' },
    { i: '🏷️', n: '标签', k: '标记 tag' }, { i: '📦', n: '包裹', k: '资源 打包' },
    { i: '📥', n: '收件箱', k: '收件' }, { i: '📤', n: '发件箱', k: '发送' },
    { i: '⬇️', n: '下载', k: '保存' }, { i: '⬆️', n: '上传', k: '分享' },
    { i: '🗄️', n: '档案柜', k: '存储 数据库' }, { i: '📎', n: '回形针', k: '附件' },
    { i: '🔖', n: '书签', k: '收藏夹' }, { i: '🗃️', n: '卡片盒', k: '索引' },
    { i: '📊', n: '图表', k: '数据 统计' }, { i: '📈', n: '趋势图', k: '增长' },
    { i: '📉', n: '下降图', k: '统计' }, { i: '🗒️', n: '便签', k: '记事本' },
    { i: '🗑️', n: '回收站', k: '删除' }, { i: '📇', n: '名片', k: '卡片目录' },
    { i: '📑', n: '标签页', k: '网页' }, { i: '🗜️', n: '压缩', k: '解压' },
    { i: '📔', n: '记事本', k: '日记本' }, { i: '📕', n: '红书', k: '书本' },
    { i: '📗', n: '绿书', k: '书本' }, { i: '📘', n: '蓝书', k: '书本' },
    { i: '📙', n: '橙书', k: '书本' }, { i: '📒', n: '账本', k: '' },
    { i: '🔓', n: '解锁', k: '打开' }, { i: '📆', n: '日程', k: '日期' },
    { i: '🗓️', n: '月历', k: '日历' }, { i: '🏺', n: '陶罐', k: '古董' },
  ]},
  { c: '资讯社交', list: [
    { i: '📰', n: '报纸', k: '新闻' }, { i: '🗞️', n: '报纸卷', k: '媒体' },
    { i: '📢', n: '公告', k: '喇叭' }, { i: '📣', n: '扩音器', k: '宣传' },
    { i: '📚', n: '书籍', k: '阅读 学习' }, { i: '📖', n: '打开书', k: '阅读' },
    { i: '📓', n: '笔记本', k: '记录' }, { i: '📝', n: '笔记', k: '编辑' },
    { i: '✉️', n: '邮件', k: '邮箱 信件' }, { i: '📧', n: '邮箱', k: 'email' },
    { i: '📨', n: '来信', k: '邮件' }, { i: '💬', n: '聊天', k: '对话 评论' },
    { i: '🗨️', n: '评论', k: '气泡' }, { i: '🔔', n: '通知', k: '铃铛 提醒' },
    { i: '🎓', n: '毕业', k: '学习 教育' }, { i: '📜', n: '卷轴', k: '历史' },
    { i: '📄', n: '文件', k: '文档' }, { i: '📃', n: '文档', k: '文件' },
    { i: '🧾', n: '收据', k: '账单' }, { i: '👥', n: '团队', k: '成员' },
    { i: '🏛️', n: '政务', k: '政府' }, { i: '⚖️', n: '法律', k: '天平' },
    { i: '📮', n: '邮筒', k: '寄信' }, { i: '✍️', n: '写作', k: '书写' },
    { i: '💭', n: '想法', k: '思考' }, { i: '🗯️', n: '情绪', k: '愤怒' },
    { i: '💌', n: '情书', k: '邮件' }, { i: '🔕', n: '免打扰', k: '静音' },
    { i: '👪', n: '家庭', k: '家人' }, { i: '🤝', n: '握手', k: '合作' },
    { i: '👋', n: '挥手', k: '你好' }, { i: '🙋', n: '举手', k: '提问' },
  ]},
  { c: '美食', list: [
    { i: '🍔', n: '汉堡', k: '快餐' }, { i: '🍕', n: '披萨', k: '西餐' },
    { i: '🍜', n: '面食', k: '面条' }, { i: '🍱', n: '便当', k: '外卖' },
    { i: '☕', n: '咖啡', k: '饮品' }, { i: '🍺', n: '啤酒', k: '喝酒' },
    { i: '🍵', n: '茶', k: '饮茶' },
    { i: '🍟', n: '薯条', k: '快餐' }, { i: '🌭', n: '热狗', k: '快餐' },
    { i: '🥪', n: '三明治', k: '西餐' }, { i: '🌮', n: '塔可', k: '墨西哥' },
    { i: '🌯', n: '卷饼', k: '墨西哥' }, { i: '🥗', n: '沙拉', k: '轻食' },
    { i: '🍲', n: '火锅', k: '炖煮' }, { i: '🥡', n: '餐盒', k: '外卖' },
    { i: '🍙', n: '饭团', k: '日料' }, { i: '🍚', n: '米饭', k: '主食' },
    { i: '🍢', n: '关东煮', k: '日料' }, { i: '🍣', n: '寿司', k: '日料' },
    { i: '🍤', n: '炸虾', k: '海鲜' }, { i: '🍳', n: '煎蛋', k: '早餐' },
    { i: '🥞', n: '煎饼', k: '早餐' }, { i: '🍰', n: '蛋糕', k: '甜点' },
    { i: '🎂', n: '生日糕', k: '生日' }, { i: '🍧', n: '刨冰', k: '冰品' },
    { i: '🍨', n: '冰淇淋', k: '甜品' }, { i: '🍦', n: '甜筒', k: '冰品' },
    { i: '🧁', n: '纸杯糕', k: '甜品' }, { i: '🍫', n: '巧克力', k: '甜品' },
    { i: '🍬', n: '糖果', k: '零食' }, { i: '🍭', n: '棒棒糖', k: '零食' },
    { i: '🍯', n: '蜂蜜', k: '甜食' }, { i: '🥤', n: '饮料', k: '饮品' },
    { i: '🧋', n: '奶茶', k: '饮品' },
  ]},
  { c: '生活运动', list: [
    { i: '✈️', n: '飞机', k: '旅行' }, { i: '🚗', n: '汽车', k: '出行' },
    { i: '🚆', n: '火车', k: '高铁' }, { i: '🗺️', n: '地图', k: '旅游' },
    { i: '🧳', n: '行李', k: '旅行' }, { i: '🚕', n: '出租', k: '打车' },
    { i: '🚌', n: '公交', k: '巴士' }, { i: '🏍️', n: '摩托', k: '机车' },
    { i: '🚲', n: '单车', k: '自行车' }, { i: '🚁', n: '直升机', k: '飞行' },
    { i: '🛳️', n: '邮轮', k: '游船' }, { i: '⛵', n: '帆船', k: '' },
    { i: '🚤', n: '快艇', k: '' }, { i: '🛶', n: '独木舟', k: '皮划艇' },
    { i: '💰', n: '钱袋', k: '理财' }, { i: '🛒', n: '购物', k: '购物车' },
    { i: '⚽', n: '足球', k: '运动' }, { i: '🏀', n: '篮球', k: '体育' },
    { i: '🎾', n: '网球', k: '运动' }, { i: '🏃', n: '跑步', k: '健身' },
    { i: '⚾', n: '棒球', k: '运动' }, { i: '🏐', n: '排球', k: '运动' },
    { i: '🥏', n: '飞盘', k: '运动' }, { i: '🏸', n: '羽毛球', k: '运动' },
    { i: '🥊', n: '拳击', k: '格斗' }, { i: '⛳', n: '高尔夫', k: '运动' },
    { i: '😀', n: '笑脸', k: '表情' }, { i: '🧸', n: '玩具', k: '泰迪熊' },
    { i: '🐶', n: '小狗', k: '宠物' }, { i: '🦄', n: '独角兽', k: '神兽' },
    { i: '🔮', n: '水晶球', k: '魔法' }, { i: '🪄', n: '魔法棒', k: '魔术' },
    { i: '🎈', n: '气球', k: '派对' }, { i: '🎉', n: '彩带', k: '庆祝' },
    { i: '🛌', n: '睡觉', k: '休息' }, { i: '🧘', n: '瑜伽', k: '冥想' },
    { i: '🏥', n: '医院', k: '医疗' }, { i: '🏦', n: '银行', k: '' },
    { i: '🏫', n: '学校', k: '教育' }, { i: '⛪', n: '教堂', k: '' },
    { i: '🌿', n: '植物', k: '自然' }, { i: '🌻', n: '向日葵', k: '花朵' },
    { i: '🐾', n: '爪印', k: '宠物' }, { i: '💍', n: '戒指', k: '婚礼' },
    { i: '🌷', n: '郁金香', k: '花' }, { i: '🪴', n: '盆栽', k: '植物' },
  ]},
];

function renderGroupsTable() {
  const tbody = document.getElementById('groupsTbody');
  tbody.innerHTML = '';
  if (!state.groups.length) {
    tbody.innerHTML = '<tr><td colspan="6" style="text-align:center;color:var(--text-muted)">暂无分组，点击右上角「添加分组」创建</td></tr>';
    return;
  }
  state.groups.forEach((g, i) => {
    const vis = (g.is_visible ?? 1) ? 1 : 0;
    const isChild = (g.parent_id || 0) !== 0;
    const tr = document.createElement('tr');
    tr.dataset.idx = i;
    if (!vis) tr.classList.add('row-hidden');
    if (isChild) tr.classList.add('row-child');
    tr.innerHTML =
      '<td class="drag-cell"><span class="drag-handle" title="拖动调整顺序">⠿</span></td>' +
      '<td><span class="g-table-name' + (isChild ? ' is-child' : '') + '">' +
        (isChild ? '<span class="g-level-tag">二级</span>' : '') +
        (g.icon ? '<span class="g-table-icon">' + esc(g.icon) + '</span>' : '') +
        '<span>' + esc(g.title) + '</span>' +
      '</span></td>' +
      '<td style="color:var(--text-muted)">' + esc(g.description) + '</td>' +
      '<td>' + g.item_count + '</td>' +
      '<td class="vis-cell">' +
        '<span class="vis-badge' + (vis ? ' on' : '') + '">' + (vis ? '显示' : '隐藏') + '</span>' +
        '<button class="btn btn-sm btn-vis" data-act="vis">' + (vis ? '隐藏' : '显示') + '</button>' +
      '</td>' +
      '<td><div class="actions">' +
      '<button class="btn btn-sm" data-act="up">上移</button>' +
      '<button class="btn btn-sm" data-act="down">下移</button>' +
      '<button class="btn btn-sm" data-act="edit">编辑</button>' +
      '<button class="btn btn-sm btn-danger" data-act="del">删除</button>' +
      '</div></td>';
    tr.querySelectorAll('[data-act]').forEach(btn => {
      btn.onclick = () => groupAction(btn.dataset.act, g);
    });
    tbody.appendChild(tr);
  });
}

async function groupAction(act, g) {
  if (act === 'vis') {
    const toHide = (g.is_visible ?? 1) ? 1 : 0;
    API.post(API_BASE + 'groups.php?action=visible', { id: g.id, visible: toHide ? 0 : 1 })
      .then(() => {
        toast(toHide ? '「' + g.title + '」已在前端隐藏（仅登录管理员可见）' : '「' + g.title + '」已恢复前端显示', 'success');
        loadGroups();
      })
      .catch(e => toast(e.message, 'error'));
    return;
  }
  if (act === 'edit') {
    document.getElementById('groupModalTitle').textContent = '编辑分组';
    document.getElementById('g_id').value = g.id;
    document.getElementById('g_title').value = g.title;
    document.getElementById('g_desc').value = g.description || '';
    setGroupIconValue(g.icon || '');
    syncGroupLevel(g.parent_id || 0);
    document.getElementById('groupModal').classList.add('show');
    return;
  }
  if (act === 'up' || act === 'down') {
    API.post(API_BASE + 'groups.php?action=sort', { id: g.id, direction: act === 'up' ? 'up' : 'down' })
      .then(loadGroups)
      .catch(e => toast(e.message, 'error'));
    return;
  }
  if (act === 'del') {
    if (!await uiConfirm('删除分组「' + g.title + '」将同时删除组内 ' + g.item_count + ' 张卡片，确定？', { danger: true })) return;
    API.post(API_BASE + 'groups.php?action=delete', { id: g.id })
      .then(() => { toast('已删除', 'success'); loadGroups().then(loadItems); })
      .catch(e => toast(e.message, 'error'));
  }
}

/** 填充"父级分组"下拉：顶级分组 + 当前编辑分组的 id 排除（不能把自己设为父） */
function bindGroups() {
  bindGroupLevelSwitch();
  document.getElementById('addGroupBtn').onclick = () => {
    document.getElementById('groupModalTitle').textContent = '添加分组';
    document.getElementById('g_id').value = '';
    document.getElementById('g_title').value = '';
    document.getElementById('g_desc').value = '';
    setGroupIconValue('');
    syncGroupLevel(0); // 默认一级分组
    document.getElementById('groupModal').classList.add('show');
  };
  document.getElementById('groupForm').addEventListener('submit', async e => {
    e.preventDefault();
    const btn = document.getElementById('groupSaveBtn');
    btn.disabled = true;
    try {
      const level = document.getElementById('g_level').value;
      const parentId = level === '1' ? Number(document.getElementById('g_parent_id').value || 0) : 0;
      // 安全约束：二级必须选有效父分组
      if (level === '1' && parentId === 0) { toast('请选择所属一级分组', 'error'); return; }
      await API.post(API_BASE + 'groups.php?action=edit', {
        id: Number(document.getElementById('g_id').value) || 0,
        parent_id: parentId,
        title: document.getElementById('g_title').value.trim(),
        icon: document.getElementById('g_icon').value,
        description: document.getElementById('g_desc').value.trim(),
      });
      document.getElementById('groupModal').classList.remove('show');
      toast('已保存', 'success');
      await loadGroups();
      await loadItems();
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      btn.disabled = false;
    }
  });
  bindGroupIconPicker();
}

/* ---------- 分组图标选择器 ---------- */
/** 写入当前选中图标并更新选择按钮预览 */
function setGroupIconValue(v) {
  document.getElementById('g_icon').value = v || '';
  const btn = document.getElementById('gIconPick');
  btn.textContent = '';
  if (v) {
    btn.appendChild(document.createTextNode(v));
  } else {
    const ph = document.createElement('span');
    ph.className = 'g-icon-placeholder';
    ph.textContent = '图标';
    btn.appendChild(ph);
  }
}

function bindGroupIconPicker() {
  const pickBtn = document.getElementById('gIconPick');
  const modal = document.getElementById('groupIconModal');
  const box = modal.querySelector('.modal-box');
  const body = document.getElementById('gicBody');
  const search = document.getElementById('gicSearch');
  let currentKw = '';

  /* 构建一个图标按钮（图标 + 短名，短名永不换行） */
  const buildItem = (o, current) => {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'gic-item' + (o.i === current ? ' active' : '');
    const emo = document.createElement('span');
    emo.className = 'gic-emoji';
    emo.textContent = o.i;
    const nm = document.createElement('span');
    nm.className = 'gic-name';
    nm.textContent = o.n;
    btn.appendChild(emo);
    btn.appendChild(nm);
    btn.title = o.n + (o.k ? ' ' + o.k : '');
    btn.onclick = () => {
      setGroupIconValue(o.i);
      modal.classList.remove('show');
    };
    return btn;
  };

  /* 按分类渲染（搜索时仅保留命中分类，分组结构不变） */
  const renderBody = () => {
    const current = document.getElementById('g_icon').value;
    body.innerHTML = '';
    let total = 0;
    GROUP_ICON_CATS.forEach((cat, ci) => {
      const list = currentKw
        ? cat.list.filter(o =>
            o.n.toLowerCase().includes(currentKw) ||
            o.k.toLowerCase().includes(currentKw) ||
            o.i.includes(currentKw))
        : cat.list;
      if (!list.length) return;
      total += list.length;
      const sec = document.createElement('section');
      sec.className = 'gic-cat';
      sec.dataset.ci = ci;
      const head = document.createElement('div');
      head.className = 'gic-cat-head';
      const bar = document.createElement('span');
      bar.className = 'gic-cat-bar';
      head.appendChild(bar);
      head.appendChild(document.createTextNode(cat.c));
      const grid = document.createElement('div');
      grid.className = 'gic-cat-grid';
      list.forEach(o => grid.appendChild(buildItem(o, current)));
      sec.appendChild(head);
      sec.appendChild(grid);
      body.appendChild(sec);
    });
    if (!total) {
      const empty = document.createElement('div');
      empty.className = 'gic-empty';
      empty.textContent = '没有找到匹配的图标';
      body.appendChild(empty);
    }
  };

  pickBtn.onclick = () => {
    search.value = '';
    currentKw = '';
    renderBody();
    modal.classList.add('show');
    box.scrollTop = 0;
    setTimeout(() => search.focus(), 50);
  };
  search.addEventListener('input', () => {
    currentKw = search.value.trim().toLowerCase();
    renderBody();
    box.scrollTop = 0;
  });
  document.getElementById('gicClear').onclick = () => {
    setGroupIconValue('');
    modal.classList.remove('show');
  };
}

/* ================= 卡片 ================= */
async function loadItems() {
  try {
    // 一次取全量，前端按分组建树筛选，切换分组/搜索无需重复请求
    state.items = await API.get(API_BASE + 'items.php?action=list');
  } catch (e) {
    toast('卡片加载失败：' + e.message, 'error');
    state.items = [];
  }
  renderItemsTable();
}

function groupName(id) {
  const g = state.groups.find(x => x.id === id);
  return g ? g.title : '#' + id;
}

/** 应用筛选条件：一级分组（含其子分组）/ 二级分组 / 关键字 */
function filteredItems() {
  const keyword = state.itemKeyword;
  const groups = state.groups || [];
  const parentOf = (gid) => {
    const g = groups.find(x => x.id === gid);
    return g ? (g.parent_id || 0) : 0;
  };
  return state.items.filter(it => {
    if (state.currentGroup && it.group_id !== state.currentGroup) return false;
    if (!state.currentGroup && state.currentParent) {
      if (it.group_id !== state.currentParent && parentOf(it.group_id) !== state.currentParent) return false;
    }
    if (keyword) {
      const hay = ((it.title || '') + ' ' + (it.url || '') + ' ' + (it.lan_url || '') + ' ' + (it.description || '')).toLowerCase();
      if (!hay.includes(keyword)) return false;
    }
    return true;
  });
}

/** 表格上方筛选摘要：当前维度 + 命中条数，避免用户迷失在筛选中 */
function updateFilterInfo(count) {
  const el = document.getElementById('itemsFilterInfo');
  if (!el) return;
  const parts = [];
  if (state.currentParent) {
    const p = state.groups.find(g => g.id === state.currentParent);
    parts.push('一级：' + (p ? p.title : '#'));
  }
  if (state.currentGroup) {
    const g = state.groups.find(x => x.id === state.currentGroup);
    parts.push('二级：' + (g ? g.title : '#'));
  }
  if (state.itemKeyword) parts.push('搜索：' + state.itemKeyword);
  el.textContent = (parts.length ? '筛选 ' + parts.join(' · ') + ' · ' : '') + '共 ' + count + ' 条';
}

function renderItemsTable() {
  const tbody = document.getElementById('itemsTbody');
  tbody.innerHTML = '';
  const list = filteredItems();
  updateFilterInfo(list.length);
  if (!list.length) {
    const hasFilter = !!(state.currentGroup || state.currentParent || state.itemKeyword);
    tbody.innerHTML = '<tr><td colspan="6" style="text-align:center;color:var(--text-muted)">' +
      (hasFilter ? '没有匹配的卡片，试试调整筛选或清空搜索' : '暂无卡片，点击右上角「添加卡片」创建') + '</td></tr>';
    renderItemsPager(0);
    return;
  }
  const total = list.length;
  const totalPages = Math.max(1, Math.ceil(total / ITEM_PAGE_SIZE));
  if (state.itemPage > totalPages) state.itemPage = totalPages;
  if (state.itemPage < 1) state.itemPage = 1;
  const start = (state.itemPage - 1) * ITEM_PAGE_SIZE;
  const pageItems = list.slice(start, start + ITEM_PAGE_SIZE);
  pageItems.forEach(item => {
    const isHidden = !!(item.is_hidden & 1);
    const tr = document.createElement('tr');
    if (isHidden) tr.classList.add('row-hidden');
    tr.innerHTML =
      '<td><span class="icon-preview" style="width:34px;height:34px;font-size:14px;border-radius:8px" data-cell="icon"></span></td>' +
      '<td><div>' + esc(item.title) + (isHidden ? ' <span class="vis-badge" style="background:var(--color-warn)">隐藏</span>' : '') + '</div></td>' +
      '<td style="max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--text-muted)">' + esc(item.url || item.lan_url) + '</td>' +
      '<td style="white-space:nowrap"><button type="button" class="item-group-tag" data-gid="' + item.group_id + '" title="只看该分组">' + esc(groupName(item.group_id)) + '</button></td>' +
      '<td>' + (OPEN_TEXT[item.open_method] || '新窗口') + '</td>' +
      '<td><div class="actions">' +
      '<button class="btn btn-sm" data-act="up">上移</button>' +
      '<button class="btn btn-sm" data-act="down">下移</button>' +
      '<button class="btn btn-sm btn-vis" data-act="toggle_hidden">' + (isHidden ? '显示' : '隐藏') + '</button>' +
      '<button class="btn btn-sm" data-act="edit">编辑</button>' +
      '<button class="btn btn-sm btn-danger" data-act="del">删除</button>' +
      '</div></td>';
    // 图标预览：iconFor 统一返回 HTML（img 或 grid span），直接 innerHTML
    const iconCell = tr.querySelector('[data-cell="icon"]');
    iconCell.innerHTML = iconFor(item);
    tr.querySelectorAll('[data-act]').forEach(btn => {
      btn.onclick = () => itemAction(btn.dataset.act, item);
    });
    const tag = tr.querySelector('.item-group-tag');
    if (tag) {
      tag.onclick = () => {
        const gid = Number(tag.dataset.gid);
        const g = (state.groups || []).find(x => x.id === gid);
        state.currentParent = g ? (g.parent_id || g.id) : 0;
        state.currentGroup = g && (g.parent_id || 0) ? gid : 0;
        state.itemPage = 1;
        renderGroupFilter();
        renderItemsTable();
      };
    }
    tbody.appendChild(tr);
  });
  renderItemsPager(total);
}

/** 渲染卡片列表分页控件（上一页/页码/下一页 + 总数） */
function renderItemsPager(total) {
  const pager = document.getElementById('itemsPager');
  if (!pager) return;
  pager.hidden = false;
  const totalPages = Math.max(1, Math.ceil(total / ITEM_PAGE_SIZE));
  // 只有一页时只显示总数信息，不渲染翻页按钮
  if (totalPages <= 1) {
    pager.innerHTML = '<span class="pager-info">共 ' + total + ' 条</span>';
    return;
  }
  const cur = state.itemPage;
  const mkBtn = (label, page, opts = {}) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'pager-btn' + (opts.active ? ' active' : '') + (opts.ellipsis ? ' ellipsis' : '');
    b.textContent = label;
    if (opts.ellipsis || opts.disabled) {
      b.disabled = true;
    } else {
      b.onclick = () => {
        state.itemPage = page;
        renderItemsTable();
        // 翻页后回到表格顶部，避免停在旧位置
        const table = document.getElementById('itemsTbody');
        if (table && table.closest('.admin-panel')) {
          table.closest('.admin-panel').scrollIntoView({ block: 'start', behavior: 'smooth' });
        }
      };
    }
    return b;
  };
  pager.innerHTML = '';
  const info = document.createElement('span');
  info.className = 'pager-info';
  const start = (cur - 1) * ITEM_PAGE_SIZE + 1;
  const end = Math.min(cur * ITEM_PAGE_SIZE, total);
  info.textContent = '第 ' + start + '–' + end + ' 条 / 共 ' + total + ' 条';
  pager.appendChild(info);
  const nav = document.createElement('span');
  nav.className = 'pager-nav';
  nav.appendChild(mkBtn('‹ 上一页', cur - 1, { disabled: cur <= 1 }));
  // 页码窗口：当前页前后各 2 页，超出用 …
  const pages = [];
  for (let p = 1; p <= totalPages; p++) {
    if (p === 1 || p === totalPages || Math.abs(p - cur) <= 2) pages.push(p);
    else if (pages[pages.length - 1] !== '…') pages.push('…');
  }
  pages.forEach(p => {
    if (p === '…') nav.appendChild(mkBtn('…', 0, { ellipsis: true }));
    else nav.appendChild(mkBtn(String(p), p, { active: p === cur }));
  });
  nav.appendChild(mkBtn('下一页 ›', cur + 1, { disabled: cur >= totalPages }));
  pager.appendChild(nav);
}

/** 后台表格里的图标 HTML（统一返回 HTML 片段，三态一致） */
function iconFor(item) {
  if (item.icon_type === 'image' && item.icon_value) {
    return '<img src="' + esc(assetUrl(item.icon_value)) + '" alt="">';
  }
  if (item.icon_type === 'favicon' && (item.url || item.lan_url)) {
    const fu = faviconUrl(item.url || item.lan_url);
    if (fu) return '<img src="' + esc(fu) + '" alt="" referrerpolicy="no-referrer">';
  }

  // 文字型：手动文字优先 → 空则 fallback title
  const raw = (item.icon_type === 'text' && item.icon_value
               && !isImageUrlLike(item.icon_value))
              ? item.icon_value : (item.title || '?');
  const txt = smartIconText(raw);
  const layout = textLayout(txt);
  const chars = Array.from(txt);

  // 后台表格图标容器约 26px（style="width:34px;height:34px" 但有 padding？实际 render 内测）
  let fontSize;
  if (layout === 'cn')
    fontSize = Math.min(14, Math.floor((26 / 2) * 0.92));
  else
    fontSize = Math.min(14, Math.floor((26 / 4) * 1.75));
  fontSize = Math.max(7, fontSize);

  const bg = item.icon_bg ? 'background:' + esc(item.icon_bg) + ';' : '';
  const cls = layout === 'cn' ? 'icon-grid-cn' : 'icon-wrap-en';
  const inner = chars.map(c => '<span class="child-text">' + esc(c) + '</span>').join('');
  return '<span class="' + cls + '" style="' + bg + 'font-size:' + fontSize + 'px;">' + inner + '</span>';
}

async function itemAction(act, item) {
  if (act === 'edit') {
    openItemModal(item);
    return;
  }
  if (act === 'up' || act === 'down') {
    API.post(API_BASE + 'items.php?action=sort', { id: item.id, direction: act === 'up' ? 'up' : 'down' })
      .then(loadItems)
      .catch(e => toast(e.message, 'error'));
    return;
  }
  if (act === 'toggle_hidden') {
    const newHidden = (item.is_hidden & 1) ? 0 : 1;
    API.post(API_BASE + 'items.php?action=toggle_hidden', { id: item.id, is_hidden: newHidden })
      .then(() => loadItems())
      .then(() => toast(newHidden ? '已隐藏' : '已显示', 'success'))
      .catch(e => toast(e.message, 'error'));
    return;
  }
  if (act === 'del') {
    if (!await uiConfirm('确定删除卡片「' + item.title + '」？', { danger: true })) return;
    API.post(API_BASE + 'items.php?action=delete', { id: item.id })
      .then(() => { toast('已删除', 'success'); loadGroups().then(loadItems); })
      .catch(e => toast(e.message, 'error'));
  }
}

function openItemModal(item) {
  document.getElementById('itemModalTitle').textContent = item ? '编辑卡片' : '添加卡片';
  document.getElementById('i_id').value = item ? item.id : '';
  document.getElementById('i_group').value = String(item ? item.group_id : (state.currentGroup || state.currentParent || (state.groups[0] && state.groups[0].id) || 0));
  document.getElementById('i_open').value = String(item ? item.open_method : 2);
  document.getElementById('i_hidden').checked = !!(item && item.is_hidden);
  document.getElementById('i_title').value = item ? item.title : '';
  document.getElementById('i_url').value = item ? item.url : '';
  document.getElementById('i_lan').value = item ? item.lan_url : '';
  document.getElementById('i_desc').value = item ? item.description : '';
  document.getElementById('i_tags').value = item && Array.isArray(item.tags) ? item.tags.join('，') : '';

  // 图标类型 + 手动文字回填 + __iconTextManual 标记
  const itype = item ? item.icon_type : 'favicon';
  document.querySelector('#i_itype input[value="' + itype + '"]').checked = true;
  document.getElementById('i_icon').value = item && item.icon_type === 'image' ? item.icon_value : '';

  if (item && item.icon_type === 'text') {
    if (item.icon_value && !isImageUrlLike(item.icon_value)) {
      // ✓ 真正的手动文字 → 回填，标记手动
      document.getElementById('i_iconText').value = item.icon_value;
      window.__iconTextManual = true;
    } else {
      // ✗ 脏数据！icon_value 里存了图片 URL → 自动纠正
      document.getElementById('i_iconText').value = smartIconText(item.title || '');
      window.__iconTextManual = false;
    }
  } else if (item) {
    // favicon / image → 预填标题智能截断结果，跟随模式
    document.getElementById('i_iconText').value = smartIconText(item.title || '');
    window.__iconTextManual = false;
  } else {
    // 新建：空
    document.getElementById('i_iconText').value = '';
    window.__iconTextManual = false;
  }

  // 颜色面板初始化（解析已有 icon_bg → color + alpha；新建默认透明度 0）
  const bgInput = item ? (item.icon_bg || '') : '';
  let cVal = '#0969da', aVal = 0;
  if (bgInput) {
    const m = bgInput.match(/^rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)$/i);
    if (m) {
      const r = parseInt(m[1]), g = parseInt(m[2]), b = parseInt(m[3]);
      cVal = '#' + [r, g, b].map(x => x.toString(16).padStart(2, '0')).join('');
      aVal = m[4] ? Math.round(parseFloat(m[4]) * 100) : 100;
    } else if (bgInput.startsWith('#')) {
      cVal = bgInput;
      aVal = 100;
    }
  }
  document.getElementById('i_color').value = cVal;
  document.getElementById('i_alpha').value = String(aVal);
  document.getElementById('i_alphaLabel').textContent = aVal + '%';
  document.getElementById('i_bg').value = buildBgCss(cVal, aVal);

  // ★ 关键：先 display:flex（.modal.show）再同步渲染；读 clientWidth 会强制 reflow 拿到真实尺寸
  document.getElementById('itemModal').classList.add('show');
  updateIconPreview();
}

function updateIconPreview() {
  const type = document.querySelector('#i_itype input:checked').value;
  const icon = document.getElementById('i_icon').value.trim();
  const title = document.getElementById('i_title').value.trim();
  const url = document.getElementById('i_url').value.trim();
  const bg = document.getElementById('i_bg').value.trim();
  const preview = document.getElementById('i_preview');
  const faviconBtn = document.getElementById('i_useFavicon');

  // 显隐：文字输入框仅 text 类型显示；颜色面板 text/image 显示，favicon 隐藏
  document.getElementById('i_textWrap').style.display = type === 'text' ? '' : 'none';
  document.getElementById('i_bgWrap').style.display = (type === 'text' || type === 'image') ? '' : 'none';
  faviconBtn.hidden = type !== 'image';

  if (type === 'image') {
    preview.style.background = bg || '#0969da';
    preview.innerHTML = icon ? '<img src="' + esc(assetUrl(icon)) + '" alt="">' : '';
  } else if (type === 'favicon') {
    preview.style.background = '';
    const fu = faviconUrl(url);
    preview.innerHTML = fu ? '<img src="' + esc(fu) + '" alt="" referrerpolicy="no-referrer">' : '<span>?</span>';
  } else {
    // 文字型
    preview.style.background = bg || '#0969da';
    const raw = (document.getElementById('i_iconText').value.trim())
                ? document.getElementById('i_iconText').value : (title || '?');
    renderTextIcon(preview, smartIconText(raw), 18);
  }
}

/** 颜色面板联动 → 合成值写入 i_bg hidden 字段 */
function syncColorPanel() {
  const c = document.getElementById('i_color').value;
  const a = parseInt(document.getElementById('i_alpha').value);
  document.getElementById('i_alphaLabel').textContent = a + '%';
  document.getElementById('i_bg').value = buildBgCss(c, a);
}

/* ================= 拖拽排序（拖动 → 点「保存排序」才提交） ================= */
function setGroupsOrderDirty(d) {
  state.groupsOrderDirty = d;
  const save = document.getElementById('saveGroupsOrderBtn');
  const reset = document.getElementById('resetGroupsOrderBtn');
  if (save) save.hidden = !d;
  if (reset) reset.hidden = !d;
}

/**
 * 表格行拖拽（事件委托挂在 tbody 上，重绘不丢绑定）
 * 仅从 ⠿ 把手按下时才允许整行拖拽，避免干扰按钮点击与文本选择
 */
function bindTableDrag(tbodyId, opts) {
  const tbody = document.getElementById(tbodyId);
  if (!tbody) return;
  let dragIdx = -1;
  let dropAfter = false; // 鼠标在目标行下半部时插入到该行之后
  const clearDragState = () => {
    dragIdx = -1;
    dropAfter = false;
    tbody.querySelectorAll('tr.dragging, tr.drag-over-top, tr.drag-over-bottom')
      .forEach(x => x.classList.remove('dragging', 'drag-over-top', 'drag-over-bottom'));
    tbody.querySelectorAll('tr[draggable="true"]').forEach(x => { x.draggable = false; });
  };
  tbody.addEventListener('mousedown', e => {
    const tr = e.target.closest('tr[data-idx]');
    if (tr) tr.draggable = !!e.target.closest('.drag-handle') && opts.canDrag();
  });
  tbody.addEventListener('dragstart', e => {
    const tr = e.target.closest('tr[data-idx]');
    if (!tr || !opts.canDrag()) { e.preventDefault(); clearDragState(); return; }
    dragIdx = Number(tr.dataset.idx);
    requestAnimationFrame(() => tr.classList.add('dragging'));
    e.dataTransfer.effectAllowed = 'move';
    try { e.dataTransfer.setData('text/plain', tr.dataset.idx); } catch (_) { /* IE 兼容忽略 */ }
  });
  tbody.addEventListener('dragover', e => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    const tr = e.target.closest('tr[data-idx]');
    tbody.querySelectorAll('tr.drag-over-top, tr.drag-over-bottom')
      .forEach(x => x.classList.remove('drag-over-top', 'drag-over-bottom'));
    if (!tr || Number(tr.dataset.idx) === dragIdx) return;
    const rect = tr.getBoundingClientRect();
    dropAfter = e.clientY > rect.top + rect.height / 2;
    tr.classList.add(dropAfter ? 'drag-over-bottom' : 'drag-over-top');
  });
  tbody.addEventListener('drop', e => {
    e.preventDefault();
    const tr = e.target.closest('tr[data-idx]');
    if (!tr || dragIdx < 0) { clearDragState(); return; }
    const to = Number(tr.dataset.idx);
    if (to !== dragIdx) {
      opts.reorder(dragIdx, to + (dropAfter ? 1 : 0));
    }
    clearDragState();
  });
  tbody.addEventListener('dragend', clearDragState);
  tbody.addEventListener('mouseleave', clearDragState);
}

function saveGroupsOrder() {
  return API.post(API_BASE + 'groups.php?action=sort_batch', {
    ids: state.groups.map(g => g.id),
  }).then(() => {
    toast('分组排序已保存', 'success');
    setGroupsOrderDirty(false);
    return loadGroups();
  });
}

function bindDragSort() {
  // 卡片排序已移至前端主页（管理员登录后进入排序模式拖拽保存）；后台仅保留上移/下移按钮
  // 分组：按树形顺序拖拽，只允许在同一级分组内调整
  bindTableDrag('groupsTbody', {
    canDrag: () => true,
    reorder: (from, insertAt) => {
      const next = state.groups.slice();
      const moved = next.splice(from, 1)[0];
      next.splice(insertAt > from ? insertAt - 1 : insertAt, 0, moved);
      if (!isSiblingContiguous(next)) {
        toast('只能在同一级分组内拖动排序，跨级请用「编辑」改所属一级分组', 'error');
        renderGroupsTable();
        return;
      }
      state.groups = next;
      renderGroupsTable();
      setGroupsOrderDirty(true);
    },
  });
  document.getElementById('saveGroupsOrderBtn').onclick = () => {
    saveGroupsOrder().catch(e => toast(e.message, 'error'));
  };
  document.getElementById('resetGroupsOrderBtn').onclick = () => { setGroupsOrderDirty(false); loadGroups(); };
}

/** 校验树形顺序：每个二级分组必须紧跟所属一级分组或同父级的二级分组 */
function isSiblingContiguous(list) {
  for (let i = 0; i < list.length; i += 1) {
    const pid = list[i].parent_id || 0;
    if (pid === 0) continue;
    const prev = list[i - 1];
    if (!prev) return false;
    const prevPid = prev.parent_id || 0;
    if (prevPid !== pid && !(prevPid === 0 && prev.id === pid)) return false;
  }
  return true;
}

function bindItems() {
  document.getElementById('addItemBtn').onclick = () => openItemModal(null);

  // ============ 事件绑定 ============
  // 标题 → iconText 自动同步（未手动编辑过时才跟随）
  document.getElementById('i_title').addEventListener('input', () => {
    if (!window.__iconTextManual) {
      document.getElementById('i_iconText').value = smartIconText(document.getElementById('i_title').value);
    }
    updateIconPreview();
  });
  // iconText 一旦有 input → 标记手动，以后标题改动不再自动覆盖
  document.getElementById('i_iconText').addEventListener('input', () => {
    window.__iconTextManual = true;
    updateIconPreview();
  });
  // URL 输入
  document.getElementById('i_url').addEventListener('input', updateIconPreview);
  // 图片地址输入
  document.getElementById('i_icon').addEventListener('input', updateIconPreview);
  // 颜色面板联动
  document.getElementById('i_color').addEventListener('input', () => { syncColorPanel(); updateIconPreview(); });
  document.getElementById('i_alpha').addEventListener('input', () => { syncColorPanel(); updateIconPreview(); });
  // 图标类型切换
  document.querySelectorAll('#i_itype input').forEach(r => {
    r.addEventListener('change', () => {
      syncColorPanel();
      updateIconPreview();
    });
  });

  // ============ fetchMeta（三态分支 + 自动降级 text）============
  let metaBusy = false;
  let lastMetaUrl = ''; // 自动模式去重：同一地址只抓一次
  const fetchMeta = async (auto) => {
    if (metaBusy) return;
    const url = document.getElementById('i_url').value.trim();
    if (!url || !/^https?:\/\//i.test(url)) {
      if (!auto) toast('请先填写以 http:// 或 https:// 开头的地址', 'error');
      return;
    }
    if (auto && url === lastMetaUrl) return;
    const btn = document.getElementById('i_fetchMeta');
    metaBusy = true;
    btn.disabled = true;
    btn.textContent = '获取中…';
    try {
      const res = await API.get(API_BASE + 'items.php?action=fetch_meta&url=' + encodeURIComponent(url));
      lastMetaUrl = url;
      const got = [];
      const checked = document.querySelector('#i_itype input:checked');
      const curType = checked ? checked.value : 'favicon';
      const emptyTitle = !document.getElementById('i_title').value.trim();
      const emptyDesc  = !document.getElementById('i_desc').value.trim();

      if (res.title && emptyTitle) {
        document.getElementById('i_title').value = res.title;
        got.push('标题');
      }
      if (res.description && emptyDesc) {
        document.getElementById('i_desc').value = res.description;
        got.push('描述');
      }

      // 图标处理：三态分支
      if (curType === 'favicon') {
        if (res.icon && !res.fallback_icon) {
          // 站点图标已缓存到本地，favicon 模式直接可用
          got.push('图标');
        } else if (res.fallback_icon) {
          // 服务端抓不到图标时保留 favicon 模式，由 /api/favicon.php 代理兜底，
          // 不再改成第三方图片外链（外链易失效且可能被浏览器拦截）
          got.push('图标(站点图标代理)');
        } else if (!res.icon) {
          // 确实没有可用图标 → 自动降级为文字图标
          document.querySelector('#i_itype input[value="text"]').checked = true;
          document.getElementById('i_iconText').value = smartIconText(res.title || document.getElementById('i_title').value);
          window.__iconTextManual = false;
          got.push('图标(自动降级为文字)');
        }
      }

      updateIconPreview();
      const warnTxt = res.warn ? '（' + res.warn + '）' : '';
      if (!auto) {
        if (got.length) {
          toast('已自动获取：' + got.join('、') + (res.cached ? '（缓存）' : '') + warnTxt, res.warn ? 'info' : 'success');
        } else {
          toast('未获取到可填充的字段' + (warnTxt || '（站点无信息）'), 'info');
        }
      }
    } catch (e) {
      if (!auto) toast(e.message, 'error');
    } finally {
      metaBusy = false;
      btn.disabled = false;
      btn.textContent = '自动获取信息';
    }
  };
  document.getElementById('i_fetchMeta').onclick = () => fetchMeta(false);
  // 地址失焦 / 粘贴完成后，若标题为空则自动获取（同一地址不重复请求）
  const autoFetchIfEmpty = () => {
    if (!document.getElementById('i_title').value.trim()) fetchMeta(true);
  };
  document.getElementById('i_url').addEventListener('change', autoFetchIfEmpty);
  document.getElementById('i_url').addEventListener('paste', () => setTimeout(autoFetchIfEmpty, 30));

  document.getElementById('i_useFavicon').onclick = async () => {
    const url = document.getElementById('i_url').value.trim();
    if (!url) {
      toast('请先填写卡片地址', 'error');
      return;
    }
    const btn = document.getElementById('i_useFavicon');
    btn.disabled = true;
    btn.textContent = '获取中…';
    try {
      const res = await API.get(API_BASE + 'items.php?action=favicon&url=' + encodeURIComponent(url));
      if (res.fallback) {
        // 拿不到可缓存的图标 → 保留 favicon 模式，交给站点图标代理兜底
        document.querySelector('#i_itype input[value="favicon"]').checked = true;
        updateIconPreview();
        toast('服务器未能缓存图标，已改用站点图标代理加载', 'info');
        return;
      }
      document.querySelector('#i_itype input[value="image"]').checked = true;
      document.getElementById('i_icon').value = res.url;
      updateIconPreview();
      toast(res.cached ? '已获取（本地缓存）' : '获取成功', 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      btn.disabled = false;
      btn.textContent = '获取站点图标';
    }
  };

  // ============ submit（双重防线 + 颜色面板合成）============
  document.getElementById('itemForm').addEventListener('submit', async e => {
    e.preventDefault();
    const btn = document.getElementById('itemSaveBtn');
    btn.disabled = true;
    try {
      const itype = document.querySelector('#i_itype input:checked').value;
      let iconValue = '';
      switch (itype) {
        case 'image':   iconValue = document.getElementById('i_icon').value.trim(); break;
        case 'favicon': iconValue = ''; break;
        case 'text':
          iconValue = document.getElementById('i_iconText').value.trim();
          // ★ 脏数据防线：保存前再校验一次
          if (isImageUrlLike(iconValue)) iconValue = '';
          break;
      }
      syncColorPanel();  // 确保 i_bg 是最新合成值

      await API.post(API_BASE + 'items.php?action=edit', {
        id: Number(document.getElementById('i_id').value) || 0,
        group_id: Number(document.getElementById('i_group').value) || 0,
        open_method: Number(document.getElementById('i_open').value),
        is_hidden: document.getElementById('i_hidden').checked ? 1 : 0,
        title: document.getElementById('i_title').value.trim(),
        url: document.getElementById('i_url').value.trim(),
        lan_url: document.getElementById('i_lan').value.trim(),
        description: document.getElementById('i_desc').value.trim(),
        tags: document.getElementById('i_tags').value.split(/[,，;；\s]+/).map(t => t.trim()).filter(Boolean),
        icon_type: itype,
        icon_value: iconValue,
        icon_bg: document.getElementById('i_bg').value.trim(),
      });
      document.getElementById('itemModal').classList.remove('show');
      toast('已保存', 'success');
      await loadGroups();
      await loadItems();
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      btn.disabled = false;
    }
  });
}

/* ================= 上传 ================= */
function bindUploads() {
  document.querySelectorAll('[data-upload]').forEach(btn => {
    const targetId = btn.dataset.upload;
    const fileType = targetId === 's_wallpaper' ? 'wallpaper' : (targetId === 's_site_logo' ? 'logo' : 'icon');
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = 'image/*';
    input.style.display = 'none';
    btn.appendChild(input);
    btn.addEventListener('click', () => input.click());
    input.addEventListener('change', async () => {
      const file = input.files[0];
      if (!file) return;
      const form = new FormData();
      form.append('file', file);
      form.append('type', fileType);
      btn.disabled = true;
      try {
        const res = await API.postForm(API_BASE + 'upload.php', form);
        document.getElementById(targetId).value = res.url;
        if (targetId.startsWith('s_')) updateSettingPreview(targetId);
        if (targetId === 's_wallpaper') loadWallpaperGallery();
        updateIconPreview();
        toast('上传成功', 'success');
      } catch (e) {
        toast(e.message, 'error');
      } finally {
        btn.disabled = false;
        input.value = '';
      }
    });
  });
}

/* ================= 站点设置 ================= */
function isSafeAssetUrl(val) {
  if (!val || typeof val !== 'string') return false;
  // 拒绝 javascript/vbscript 等危险协议（大小写不敏感）
  if (/^\s*(javascript|vbscript|livescript|data:text\/html)\s*:/i.test(val)) return false;
  // 拒绝 HTML 标签和事件处理器注入
  if (/<[^>]+>/i.test(val)) return false;
  if (/on\w+\s*=/i.test(val)) return false;
  return true;
}

function updateSettingPreview(id) {
  const img = document.getElementById('p_' + id.replace('s_', ''));
  if (!img) return;
  const val = document.getElementById(id).value.trim();
  if (val && isSafeAssetUrl(val)) {
    img.src = assetUrl(val);
    img.classList.add('show');
    img.onerror = () => img.classList.remove('show');
  } else {
    img.classList.remove('show');
  }
}

function fillSettingsForm(s) {
  document.getElementById('s_site_title').value = s.site_title || '';
  document.getElementById('s_site_logo').value = s.site_logo || '';
  document.getElementById('s_wallpaper').value = s.wallpaper || '';
  // 遮罩透明度滑条回填（0~1，step 0.05）
  const mv = parseFloat(s.mask_opacity);
  const maskVal = isNaN(mv) ? 0.35 : Math.min(Math.max(mv, 0), 1);
  document.getElementById('s_mask_opacity').value = maskVal;
  document.getElementById('v_mask_opacity').textContent = String(Number(maskVal.toFixed(2)));
  // 壁纸模糊滑条回填（0~30px）
  const bv = parseInt(s.wallpaper_blur, 10);
  const blurVal = isNaN(bv) ? 6 : Math.min(Math.max(bv, 0), 30);
  document.getElementById('s_wallpaper_blur').value = blurVal;
  document.getElementById('v_wallpaper_blur').textContent = String(blurVal);
  document.getElementById('s_announcement').value = s.announcement || '';
  document.getElementById('s_announcement_show').value = s.announcement_show === '1' ? '1' : '0';
  document.getElementById('s_default_lan_mode').value = s.default_lan_mode === 'lan' ? 'lan' : (s.default_lan_mode === 'auto' ? 'auto' : 'public');
  document.getElementById('s_lan_hostnames').value = s.lan_hostnames || '';
  document.getElementById('s_site_url').value = s.site_url || '';
  document.getElementById('s_footer').value = s.footer || '';
  document.getElementById('s_clock_show').value = s.clock_show === '0' ? '0' : '1';
  document.getElementById('s_weather_show').value = s.weather_show === '0' ? '0' : '1';
  document.getElementById('s_weather_city').value = s.weather_city || '';
  ensurePageFontOptions();
  document.getElementById('s_page_font').value =
    SP_PAGE_FONTS.some(f => f.id === s.page_font) ? s.page_font : '';
  updatePageFontPreview();
  ensureClockFontOptions();
  document.getElementById('s_clock_font').value =
    SP_CLOCK_FONTS.some(f => f.id === s.clock_font) ? s.clock_font : '';
  updateClockFontPreview();
  document.getElementById('s_news_show').value = s.news_show === '0' ? '0' : '1';
  loadNewsSourceRows(s.news_sources || '', s.news_order || '');
  document.getElementById('s_default_theme').value = ['light', 'dark', 'system'].includes(s.default_theme) ? s.default_theme : 'light';
  document.getElementById('s_theme_style').value = SP_STYLES.includes(s.theme_style) ? s.theme_style : 'liquid-glass';
  document.getElementById('s_default_theme_note').textContent =
    s.default_theme === 'system' ? '跟随系统：访客未手动切换主题时，随其系统深浅自动变化' : '';
  document.getElementById('s_card_style').value = ['nav', 'navapp'].includes(s.card_style) ? s.card_style : 'nav';
  const cv = parseInt(s.card_opacity, 10);
  const cardOpacity = isNaN(cv) ? 100 : Math.min(Math.max(cv, 40), 100);
  document.getElementById('s_card_opacity').value = cardOpacity;
  document.getElementById('v_card_opacity').textContent = String(cardOpacity);
  document.getElementById('s_search_bar_enabled').value = s.search_bar_enabled === '0' ? '0' : '1';

  // 内容区域滑条
  document.getElementById('s_content_maxwidth').value = s.content_maxwidth || '1200';
  document.getElementById('s_content_pad_lr').value = s.content_pad_lr != null && s.content_pad_lr !== '' ? s.content_pad_lr : '20';
  document.getElementById('s_content_pad_top').value = s.content_pad_top != null && s.content_pad_top !== '' ? s.content_pad_top : '0';
  document.getElementById('s_content_pad_bottom').value = s.content_pad_bottom != null && s.content_pad_bottom !== '' ? s.content_pad_bottom : '40';
  ['maxwidth', 'pad_lr', 'pad_top', 'pad_bottom'].forEach(k => {
    const out = document.getElementById('v_content_' + k);
    if (out) out.textContent = document.getElementById('s_content_' + k).value;
  });

  // 搜索栏宽度滑条
  const swVal = parseInt(s.search_width, 10);
  document.getElementById('s_search_width').value = isNaN(swVal) ? 640 : Math.min(1200, Math.max(360, swVal));
  document.getElementById('v_search_width').textContent = document.getElementById('s_search_width').value;

  let engines = [];
  try { engines = JSON.parse(s.search_engines || '[]'); } catch (e) { /* 忽略 */ }
  renderEngineRows(engines);
  if (s.search_default) document.getElementById('s_search_default').value = s.search_default;

  updateSettingPreview('s_site_logo');
  updateSettingPreview('s_wallpaper');
  syncGalleryActive(s.wallpaper || '');

  // —— 2MD: 访客密码开关 / 每日壁纸源 回填 ——
  const gaEl = document.getElementById('s_guest_access_enabled');
  if (gaEl) gaEl.value = s.guest_access_enabled === '1' ? '1' : '0';
  const wsEl = document.getElementById('s_wallpaper_source');
  if (wsEl) {
    let wpSrc = s.wallpaper_source || '';
    // 旧值 bing / bing_en / bing_jp 统一归一化为 bing_cn
    if (['bing','bing_en','bing_jp'].includes(wpSrc)) wpSrc = 'bing_cn';
    wsEl.value = wpSrc;
  }
  // 访客密码不回填（安全），每次打开清空
  const gpEl = document.getElementById('s_guest_password');
  if (gpEl) gpEl.value = '';

  // —— NAS 监控 ——
  const nasShowEl = document.getElementById('s_nas_show');
  if (nasShowEl) {
    nasShowEl.value = s.nas_show === '1' ? '1' : '0';
    const layoutEl = document.getElementById('s_nas_layout');
    layoutEl.value = ['left', 'both', 'banner'].includes(s.nas_layout) ? s.nas_layout : 'right';
    document.getElementById('s_nas_guest').value = s.nas_guest === '1' ? '1' : '0';
    const nr = parseInt(s.nas_refresh, 10);
    document.getElementById('s_nas_refresh').value = isNaN(nr) ? 3 : Math.min(60, Math.max(2, nr));
    document.getElementById('s_nas_disk_paths').value = s.nas_disk_paths || '';
    const alphaEl = document.getElementById('s_nas_modal_alpha');
    const alpha = parseInt(s.nas_modal_alpha, 10);
    alphaEl.value = isNaN(alpha) ? 100 : Math.min(100, Math.max(40, alpha));
    document.getElementById('nasAlphaVal').textContent = alphaEl.value + '%';
  }
}

// 弹窗不透明度滑块实时回显
(function bindNasAlpha() {
  const el = document.getElementById('s_nas_modal_alpha');
  if (!el) return;
  el.addEventListener('input', () => {
    const out = document.getElementById('nasAlphaVal');
    if (out) out.textContent = el.value + '%';
  });
})();

/* ---------- 热点新闻数据源勾选与排序（表格排版，参考分组管理） ---------- */
async function loadNewsSourceRows(saved, savedOrder) {
  const wrap = document.getElementById('newsSourceRows');
  if (!wrap) return;
  // 空配置 = 未启用任何源（与后端 newsEnabledSetting 语义一致），不再默认全选
  let checked = [];
  try { checked = JSON.parse(saved || '[]'); } catch (e) { checked = []; }
  if (!Array.isArray(checked)) checked = [];
  if (!checked.length && saved && !/^\[/.test(String(saved).trim())) {
    // 兼容旧版逗号分隔格式
    checked = String(saved).split(',').map(s => s.trim()).filter(Boolean);
  }
  let order = [];
  try { order = JSON.parse(savedOrder || '[]'); } catch (e) { order = []; }
  if (!Array.isArray(order)) order = [];
  try {
    // meta 返回全部内置源（不受启用列表过滤，保证被关掉的源也能重新勾选），且已按保存的显示顺序排序
    const meta = await API.get(API_BASE + 'news.php?action=meta');
    let sources = (meta && meta.sources) || [];
    // 双保险：本地再按保存顺序排一次（未包含的源按 meta 顺序追加）
    if (order.length) {
      const pos = new Map(order.map((id, i) => [id, i]));
      sources = sources
        .map(s => [pos.has(s.id) ? pos.get(s.id) : 9999, s])
        .sort((a, b) => a[0] - b[0])
        .map(x => x[1]);
    }
    wrap.innerHTML = '';
    if (!sources.length) {
      wrap.innerHTML = '<div class="form-hint">暂无可选数据源</div>';
      return;
    }
    const table = document.createElement('table');
    table.className = 'table news-source-table';
    table.innerHTML =
      '<thead><tr>' +
      '<th style="width:220px">数据源</th>' +
      '<th>数据源API地址</th>' +
      '<th style="width:70px;text-align:center">排序号</th>' +
      '<th style="width:110px;text-align:center">前端显示</th>' +
      '<th style="width:110px">操作</th>' +
      '</tr></thead><tbody></tbody>';
    const tbody = table.querySelector('tbody');
    sources.forEach((src, i) => {
      const tr = document.createElement('tr');
      tr.className = 'news-source-item';
      tr.dataset.id = src.id;
      // 第 1 列：数据源（拖拽手柄 + 色点 + 名称）
      const tdName = document.createElement('td');
      tdName.style.whiteSpace = 'nowrap';
      const handle = document.createElement('span');
      handle.className = 'ns-handle';
      handle.title = '拖动调整主页显示顺序';
      handle.textContent = '⠿';
      handle.addEventListener('click', e => { e.preventDefault(); e.stopPropagation(); });
      const dot = document.createElement('span');
      dot.className = 'ns-dot';
      dot.style.background = src.color || 'var(--accent)';
      const name = document.createElement('span');
      name.textContent = src.name;
      tdName.appendChild(handle);
      tdName.appendChild(dot);
      tdName.appendChild(name);
      // 第 2 列：数据源 API 地址（home）
      const tdUrl = document.createElement('td');
      const home = src.home || '-';
      tdUrl.innerHTML = '<span class="ns-url" title="' + esc(home) + '">' + esc(home) + '</span>';
      // 第 3 列：排序号（当前行序号，1 起始）
      const tdOrder = document.createElement('td');
      tdOrder.style.textAlign = 'center';
      tdOrder.textContent = i + 1;
      // 第 4 列：前端显示（启用开关）
      const tdVis = document.createElement('td');
      tdVis.style.textAlign = 'center';
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.value = src.id;
      cb.checked = checked.includes(src.id);
      cb.title = '勾选后在主页新闻视图显示';
      tdVis.appendChild(cb);
      // 第 5 列：操作（上移/下移）
      const tdAct = document.createElement('td');
      const acts = document.createElement('div');
      acts.className = 'actions';
      const upBtn = document.createElement('button');
      upBtn.className = 'btn btn-sm';
      upBtn.textContent = '上移';
      upBtn.onclick = () => moveNewsRow(tr, -1);
      const downBtn = document.createElement('button');
      downBtn.className = 'btn btn-sm';
      downBtn.textContent = '下移';
      downBtn.onclick = () => moveNewsRow(tr, 1);
      acts.appendChild(upBtn);
      acts.appendChild(downBtn);
      tdAct.appendChild(acts);

      tr.appendChild(tdName);
      tr.appendChild(tdUrl);
      tr.appendChild(tdOrder);
      tr.appendChild(tdVis);
      tr.appendChild(tdAct);
      tbody.appendChild(tr);
    });
    wrap.appendChild(table);
    refreshNewsOrderNumbers();

    // 全选 / 反选（表格每次重绘后重新绑定）
    const allBtn = document.getElementById('newsSelectAll');
    const invertBtn = document.getElementById('newsSelectInvert');
    if (allBtn) allBtn.onclick = () => {
      wrap.querySelectorAll('tbody input[type="checkbox"]').forEach(cb => { cb.checked = true; });
    };
    if (invertBtn) invertBtn.onclick = () => {
      wrap.querySelectorAll('tbody input[type="checkbox"]').forEach(cb => { cb.checked = !cb.checked; });
    };
  } catch (e) {
    wrap.innerHTML = '<div class="form-hint">数据源列表加载失败：' + esc(e.message) + '</div>';
  }
}

/** 上移/下移某行并刷新排序号显示 */
function moveNewsRow(tr, dir) {
  const tbody = tr.parentElement;
  if (!tbody) return;
  const rows = Array.prototype.slice.call(tbody.querySelectorAll('.news-source-item'));
  const idx = rows.indexOf(tr);
  if (idx < 0) return;
  const target = idx + dir;
  if (target < 0 || target >= rows.length) return;
  const ref = rows[target];
  if (dir < 0) tbody.insertBefore(tr, ref);
  else tbody.insertBefore(tr, ref.nextSibling);
  refreshNewsOrderNumbers();
}

/** 重排后刷新「排序号」列的数字显示 */
function refreshNewsOrderNumbers() {
  const rows = document.querySelectorAll('#newsSourceRows .news-source-item');
  rows.forEach((r, i) => {
    const td = r.children[2];
    if (td) td.textContent = i + 1;
  });
}

/** 数据源行拖拽排序（仅从 ⠿ 手柄起拖；网格布局按 DOM 顺序落位） */
function bindNewsSourceDrag() {
  const wrap = document.getElementById('newsSourceRows');
  if (!wrap) return;
  let dragEl = null;
  const clearMarks = () => {
    wrap.querySelectorAll('.drag-over-before, .drag-over-after, .dragging')
      .forEach(el => el.classList.remove('drag-over-before', 'drag-over-after', 'dragging'));
    wrap.querySelectorAll('.news-source-item').forEach(el => { el.draggable = false; });
  };
  wrap.addEventListener('mousedown', e => {
    const item = e.target.closest('.news-source-item');
    if (item) item.draggable = !!e.target.closest('.ns-handle');
  });
  wrap.addEventListener('dragstart', e => {
    // 注意：dragstart 的 e.target 是被拖元素本身（label），不是手柄；
    // 能否拖拽已由 mousedown 中「仅按下 ⠿ 手柄才设 draggable=true」把关，这里不能再判定 closest('.ns-handle')
    const item = e.target.closest('.news-source-item');
    if (!item || item.draggable === false) { e.preventDefault(); return; }
    dragEl = item;
    requestAnimationFrame(() => item.classList.add('dragging'));
    e.dataTransfer.effectAllowed = 'move';
    try { e.dataTransfer.setData('text/plain', item.dataset.id || ''); } catch (_) { /* 兼容忽略 */ }
  });
  wrap.addEventListener('dragover', e => {
    if (!dragEl) return;
    const item = e.target.closest('.news-source-item');
    wrap.querySelectorAll('.drag-over-before, .drag-over-after')
      .forEach(el => el.classList.remove('drag-over-before', 'drag-over-after'));
    if (!item || item === dragEl) { e.preventDefault(); return; }
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    const rect = item.getBoundingClientRect();
    const after = (e.clientX - rect.left) > rect.width / 2 || (e.clientY - rect.top) > rect.height / 2;
    item.classList.add(after ? 'drag-over-after' : 'drag-over-before');
  });
  wrap.addEventListener('drop', e => {
    e.preventDefault();
    const item = e.target.closest('.news-source-item');
    if (!dragEl || !item || item === dragEl) { clearMarks(); dragEl = null; return; }
    const rect = item.getBoundingClientRect();
    const after = (e.clientX - rect.left) > rect.width / 2 || (e.clientY - rect.top) > rect.height / 2;
    if (after) item.after(dragEl);
    else item.before(dragEl);
    clearMarks();
    dragEl = null;
  });
  wrap.addEventListener('dragend', () => { clearMarks(); dragEl = null; });
}

/* ---------- 壁纸图库 ---------- */
async function loadWallpaperGallery() {
  const wrap = document.getElementById('wpGallery');
  const empty = document.getElementById('wpGalleryEmpty');
  if (!wrap || !empty) return;
  wrap.querySelectorAll('.wp-item').forEach(el => el.remove());
  empty.hidden = false;
  empty.textContent = '图库加载中…';
  try {
    const list = await API.get(API_BASE + 'gallery.php?action=list');
    state.wpList = Array.isArray(list) ? list : [];
    renderWallpaperGallery();
  } catch (e) {
    empty.hidden = false;
    empty.textContent = '图库加载失败：' + e.message;
  }
}

/** 按「我的上传 / 内置壁纸」分区渲染图库 */
function renderWallpaperGallery() {
  const wrap = document.getElementById('wpGallery');
  const empty = document.getElementById('wpGalleryEmpty');
  if (!wrap || !empty) return;
  wrap.querySelectorAll('.wp-item').forEach(el => el.remove());

  const uploaded = state.wpList.filter(w => !w.preset);
  const presets = state.wpList.filter(w => w.preset);
  const cntUploaded = document.getElementById('wpCountUploaded');
  const cntPreset = document.getElementById('wpCountPreset');
  if (cntUploaded) cntUploaded.textContent = uploaded.length;
  if (cntPreset) cntPreset.textContent = presets.length;
  document.querySelectorAll('#wpGalleryTabs .wp-tab').forEach(t => {
    const on = t.dataset.wpTab === state.wpTab;
    t.classList.toggle('active', on);
    t.setAttribute('aria-selected', on ? 'true' : 'false');
  });

  const list = state.wpTab === 'preset' ? presets : uploaded;
  empty.hidden = list.length > 0;
  empty.textContent = state.wpTab === 'preset'
    ? '暂无内置壁纸'
    : '还没有上传壁纸，点击上方「上传壁纸」添加';
  list.forEach(w => wrap.appendChild(buildWpItem(w)));
  syncGalleryActive(document.getElementById('s_wallpaper').value.trim());
}

function buildWpItem(w) {
  const div = document.createElement('div');
  div.className = 'wp-item';
  div.dataset.url = w.url;
  div.title = w.preset ? w.name + '（内置壁纸，点击选用）' : w.name + '（点击选用）';
  const img = document.createElement('img');
  img.src = assetUrl(w.url);
  img.alt = '';
  img.loading = 'lazy';
  div.appendChild(img);
  if (w.preset) {
    // 内置壁纸：带角标，不可删除
    const tag = document.createElement('span');
    tag.className = 'wp-tag';
    tag.textContent = '内置';
    div.appendChild(tag);
  } else {
    const del = document.createElement('button');
    del.className = 'wp-del';
    del.type = 'button';
    del.textContent = '×';
    del.title = '从图库删除';
    del.onclick = async e => {
      e.stopPropagation();
      if (!await uiConfirm('从图库删除壁纸「' + w.name + '」？文件将被移除，已引用它的设置需另行调整。', { danger: true })) return;
      try {
        await API.post(API_BASE + 'gallery.php?action=delete', { url: w.url });
        if (document.getElementById('s_wallpaper').value.trim() === w.url) {
          document.getElementById('s_wallpaper').value = '';
          updateSettingPreview('s_wallpaper');
          syncGalleryActive('');
        }
        toast('壁纸已删除', 'success');
        loadWallpaperGallery();
      } catch (err) {
        toast(err.message, 'error');
      }
    };
    div.appendChild(del);
  }
  const name = document.createElement('span');
  name.className = 'wp-name';
  name.textContent = w.name;
  div.appendChild(name);
  div.onclick = () => {
    document.getElementById('s_wallpaper').value = w.url;
    updateSettingPreview('s_wallpaper');
    syncGalleryActive(w.url);
    // 选用后关闭图库弹窗
    document.getElementById('wpGalleryModal').classList.remove('show');
    toast('壁纸已选用，点击分区「保存」生效', 'success');
  };
  return div;
}

/** 图库选中态与壁纸地址联动 */
function syncGalleryActive(url) {
  document.querySelectorAll('#wpGallery .wp-item').forEach(el => {
    el.classList.toggle('active', el.dataset.url === url);
  });
}

/* ================= 版本号 & 更新日志 ================= */
/** 更新日志日期统一显示为「年-月-日 时:分:秒」；仅填了日期（YYYY-MM-DD）的旧记录补 00:00:00 */
function formatLogDate(d) {
  const s = String(d || '').trim();
  if (/^\d{4}-\d{2}-\d{2}$/.test(s)) return s + ' 00:00:00';
  return s;
}

function renderVersionInfo() {
  const verEl = document.getElementById('appVer');
  if (verEl) verEl.textContent = APP_VERSION;
  const verEl2 = document.getElementById('appVer2');
  if (verEl2) verEl2.textContent = APP_VERSION;
  const detail = document.getElementById('changelogDetail');
  const historyList = document.getElementById('changelogHistoryList');
  const historyBtn = document.getElementById('changelogHistoryBtn');
  const historyModal = document.getElementById('changelogHistoryModal');

  // 渲染单个版本条目
  const buildLogItem = (rel) => {
    const item = document.createElement('div');
    item.className = 'log-item';
    const head = document.createElement('div');
    head.className = 'log-head';
    const ver = document.createElement('span');
    ver.className = 'log-ver';
    ver.textContent = rel.ver + (rel.ver === APP_VERSION ? '（当前版本）' : '');
    const date = document.createElement('span');
    date.className = 'log-date';
    date.textContent = formatLogDate(rel.date);
    head.appendChild(ver);
    head.appendChild(date);
    item.appendChild(head);
    const ul = document.createElement('ul');
    if (!rel.items.length) {
      const li = document.createElement('li');
      li.textContent = '暂无记录，功能更新后将在此显示';
      li.style.color = 'var(--text-muted)';
      ul.appendChild(li);
    }
    rel.items.forEach(t => {
      const li = document.createElement('li');
      li.textContent = t;
      ul.appendChild(li);
    });
    item.appendChild(ul);
    return item;
  };

  // 主页区域：仅显示最新版本
  if (detail) {
    detail.innerHTML = '';
    const latest = APP_CHANGELOG[0];
    if (latest) detail.appendChild(buildLogItem(latest));
  }
  // 历史弹窗：渲染全部版本
  if (historyList) {
    historyList.innerHTML = '';
    APP_CHANGELOG.forEach(rel => historyList.appendChild(buildLogItem(rel)));
  }
  // 按钮打开弹窗
  if (historyBtn && historyModal) {
    historyBtn.addEventListener('click', () => historyModal.classList.add('show'));
  }
}

/* ================= 版本检查（GitHub 最新版本） ================= */
const UPDATE_SESSION_KEY = 'sp_update_info';
const UPGRADE_CMD = 'docker pull ovitor/solarpanel-v3:latest && docker restart solarpanel-v3';

/** 回填「自动检查更新」开关与间隔（值来自 settings.php?action=list） */
function applyUpdateSettings(s) {
  const toggle = document.getElementById('updateCheckToggle');
  const sel = document.getElementById('updateIntervalSel');
  if (toggle) toggle.checked = (s.update_check ?? '1') !== '0';
  if (sel) sel.value = s.update_check_interval || '6';
}

/** 请求版本信息；force=false 时同一会话内复用结果，避免重复请求 */
async function fetchUpdateInfo(force) {
  if (!force) {
    try {
      const raw = sessionStorage.getItem(UPDATE_SESSION_KEY);
      if (raw) {
        const c = JSON.parse(raw);
        if (c && Date.now() - (c.ts || 0) < 6 * 3600 * 1000) return c.data;
      }
    } catch (e) { /* 隐私模式忽略 */ }
  }
  const d = await API.get(API_BASE + 'version.php' + (force ? '?refresh=1' : ''));
  try {
    sessionStorage.setItem(UPDATE_SESSION_KEY, JSON.stringify({ ts: Date.now(), data: d }));
  } catch (e) { /* 忽略 */ }
  return d;
}

/** 渲染版本状态与升级提示 */
function renderUpdateInfo(info) {
  const stateEl = document.getElementById('updateState');
  const notice = document.getElementById('updateNotice');
  if (!stateEl || !notice) return;
  notice.hidden = true;
  notice.innerHTML = '';
  stateEl.style.fontWeight = '';
  stateEl.style.color = 'var(--text-muted)';
  if (!info || !info.enabled) {
    stateEl.textContent = '（自动检查已关闭）';
    return;
  }
  if (!info.latest) {
    stateEl.textContent = '（暂时无法连接更新源，可稍后手动检查）';
    return;
  }
  if (!info.has_update) {
    stateEl.textContent = '（已是最新 ' + info.latest + '）';
    return;
  }
  stateEl.textContent = '（发现新版本 ' + info.latest + '）';
  stateEl.style.fontWeight = '600';
  stateEl.style.color = '#e07a1f';
  notice.hidden = false;
  notice.innerHTML =
    '发现新版本 <b>' + escapeHtml(info.latest) + '</b>（当前 ' + escapeHtml(info.current) + '）。' +
    'Docker 部署升级：<code>' + escapeHtml(UPGRADE_CMD) + '</code>　' +
    '<a href="' + escapeHtml(info.release_url || '#') + '" target="_blank" rel="noopener">查看发布说明</a>';
}

async function runUpdateCheck(force) {
  try {
    renderUpdateInfo(await fetchUpdateInfo(force));
  } catch (e) { /* 静默：检查失败不影响使用 */ }
}

function bindUpdateCheck() {
  // 非管理员只保留「检查更新」与「历史更新」，不显示开关与间隔
  if (state.role !== 'admin') {
    const label = document.getElementById('updateCheckToggle');
    if (label && label.parentElement) label.parentElement.style.display = 'none';
    const sel = document.getElementById('updateIntervalSel');
    if (sel) sel.style.display = 'none';
  }

  const toggle = document.getElementById('updateCheckToggle');
  if (toggle) {
    toggle.addEventListener('change', async () => {
      try {
        await API.post(API_BASE + 'settings.php?action=save', { update_check: toggle.checked ? '1' : '0' });
        toast(toggle.checked ? '已开启自动检查更新' : '已关闭自动检查更新', 'success');
        // 开关状态变了，丢掉会话缓存后重新取一次
        try { sessionStorage.removeItem(UPDATE_SESSION_KEY); } catch (e) { /* 忽略 */ }
        runUpdateCheck(false);
      } catch (e) {
        toast(e.message, 'error');
      }
    });
  }

  const sel = document.getElementById('updateIntervalSel');
  if (sel) {
    sel.addEventListener('change', async () => {
      try {
        await API.post(API_BASE + 'settings.php?action=save', { update_check_interval: sel.value });
        toast('检查间隔已保存', 'success');
      } catch (e) {
        toast(e.message, 'error');
      }
    });
  }

  const btn = document.getElementById('checkUpdateBtn');
  if (btn) {
    btn.addEventListener('click', async () => {
      btn.disabled = true;
      const old = btn.textContent;
      btn.textContent = '检查中…';
      try {
        const info = await fetchUpdateInfo(true);
        renderUpdateInfo(info);
        toast(info && info.has_update ? '发现新版本 ' + info.latest : '已是最新版本', 'success');
      } catch (e) {
        toast('检查失败：' + e.message, 'error');
      } finally {
        btn.disabled = false;
        btn.textContent = old;
      }
    });
  }

  runUpdateCheck(false);
}

function renderEngineRows(engines) {
  const wrap = document.getElementById('engineRows');
  wrap.innerHTML = '';
  (engines || []).forEach(eng => addEngineRow(eng.name, eng.url));
  refreshEngineDefaults();
}

function addEngineRow(name, url) {
  const row = document.createElement('div');
  row.className = 'engine-row';
  row.innerHTML =
    '<input class="input engine-name" placeholder="名称" maxlength="20">' +
    '<input class="input engine-url" placeholder="https://…/%s" maxlength="500">' +
    '<button class="btn btn-sm btn-danger" type="button" data-del>删除</button>';
  row.querySelector('.engine-name').value = name || '';
  row.querySelector('.engine-url').value = url || '';
  row.querySelector('[data-del]').onclick = () => {
    row.remove();
    refreshEngineDefaults();
  };
  row.querySelectorAll('input').forEach(inp => inp.addEventListener('change', refreshEngineDefaults));
  document.getElementById('engineRows').appendChild(row);
}

function refreshEngineDefaults() {
  const sel = document.getElementById('s_search_default');
  const current = sel.value;
  sel.innerHTML = '';
  document.querySelectorAll('#engineRows .engine-row').forEach(row => {
    const name = row.querySelector('.engine-name').value.trim();
    if (!name) return;
    const opt = document.createElement('option');
    opt.value = name;
    opt.textContent = name;
    sel.appendChild(opt);
  });
  if ([...sel.options].some(o => o.value === current)) sel.value = current;
}

/* 分区定义：每个分区独立保存，只提交自己的设置项（后端白名单兼容部分提交）
   分区顺序与站点设置的二级导航一一对应：站点信息 → 外观与卡片 → 导航与搜索 → 访问控制 → 热点新闻 → NAS 监控 */
const SETTING_SECTIONS = {
  basic:  { label: '站点信息', keys: ['site_title', 'site_url', 'footer', 'site_logo', 'announcement_show', 'announcement', 'icp_show', 'icp_number', 'icp_link', 'police_show', 'police_number', 'police_link', 'default_lan_mode', 'lan_hostnames'] },
  theme:  { label: '外观与卡片', keys: ['default_theme', 'theme_style', 'card_style', 'card_opacity', 'content_maxwidth', 'content_pad_lr', 'content_pad_top', 'content_pad_bottom', 'page_font', 'clock_font', 'clock_show', 'weather_show', 'weather_city', 'wallpaper', 'mask_opacity', 'wallpaper_blur', 'wallpaper_source'] },
  search: { label: '导航与搜索', keys: ['search_bar_enabled', 'topbar_desktop_enabled', 'topbar_mobile_enabled', 'search_engines', 'search_default', 'search_width'] },
  access: { label: '访问控制', keys: ['guest_access_enabled', 'guest_password_hash'] },
  news:   { label: '热点新闻', keys: ['news_show', 'news_sources', 'news_order'] },
  nas:    { label: 'NAS 监控', keys: ['nas_show', 'nas_layout', 'nas_refresh', 'nas_guest', 'nas_disk_paths', 'nas_modal_alpha'] },
  clip:   { label: '剪藏与接入', keys: ['clip_enabled', 'clip_default_group', 'clip_default_capture'] },
};

function numSetting(id, def) {
  const n = parseInt(document.getElementById(id).value, 10);
  return isNaN(n) ? def : String(n);
}

/** 按设置项键名收集当前表单值（特殊键单独处理，其余按 s_键名 取输入值） */
function collectSetting(key) {
  if (key === 'search_engines') {
    const engines = [];
    document.querySelectorAll('#engineRows .engine-row').forEach(row => {
      const name = row.querySelector('.engine-name').value.trim();
      const url = row.querySelector('.engine-url').value.trim();
      if (name && url) engines.push({ name: name, url: url });
    });
    return JSON.stringify(engines);
  }
  if (key === 'mask_opacity') {
    const v = document.getElementById('s_mask_opacity').value.trim();
    return v === '' ? '0.35' : v;
  }
  if (key === 'clip_enabled') {
    return document.getElementById('s_clip_enabled').checked ? '1' : '0';
  }
  if (key === 'content_maxwidth') return numSetting('s_content_maxwidth', '1200');
  if (key === 'content_pad_lr') return numSetting('s_content_pad_lr', '20');
  if (key === 'content_pad_top') return numSetting('s_content_pad_top', '0');
  if (key === 'content_pad_bottom') return numSetting('s_content_pad_bottom', '40');
  if (key === 'search_width') return numSetting('s_search_width', '640');
  if (key === 'card_opacity') return numSetting('s_card_opacity', '100');
  if (key === 'wallpaper_blur') return numSetting('s_wallpaper_blur', '6');
  if (key === 'nas_refresh') {
    const n = parseInt(document.getElementById('s_nas_refresh').value, 10);
    return String(isNaN(n) ? 3 : Math.min(60, Math.max(2, n)));
  }
  if (key === 'news_sources') {
    // 始终保存显式 id 列表：全选时也写入全部 id，避免后端把空值判定为“未启用”
    // 列表未加载成功则不提交，避免误存空数组关掉全部源
    const boxes = document.querySelectorAll('#newsSourceRows input[type="checkbox"]');
    if (!boxes.length) return undefined;
    const ids = [];
    boxes.forEach(cb => { if (cb.checked && cb.value) ids.push(cb.value); });
    return JSON.stringify(ids);
  }
  if (key === 'news_order') {
    // 全部数据源（含未勾选）的显示顺序，按当前行排列收集；列表未加载成功则不提交
    const rows = document.querySelectorAll('#newsSourceRows .news-source-item');
    if (!rows.length) return undefined;
    const ids = [];
    rows.forEach(r => { if (r.dataset.id) ids.push(r.dataset.id); });
    return JSON.stringify(ids);
  }
  if (key === 'guest_password_hash') {
    // 访客密码：前端输入明文（id=s_guest_password），留空=不改，返回 undefined 让后端跳过
    const el = document.getElementById('s_guest_password');
    if (!el) return undefined;
    const v = el.value.trim();
    return v === '' ? undefined : v;
  }
  const el = document.getElementById('s_' + key);
  if (!el) return undefined;
  return el.value.trim();
}

/* ================= 时钟 / 天气字体 ================= */
function ensurePageFontOptions() {
  const sel = document.getElementById('s_page_font');
  if (!sel || sel.dataset.ready === '1') return;
  const opts = ['<option value="">跟随系统（默认）</option>'];
  SP_PAGE_FONTS.forEach(f => {
    opts.push('<option value="' + f.id + '">' + esc(f.name) + '</option>');
  });
  sel.innerHTML = opts.join('');
  sel.dataset.ready = '1';
  sel.addEventListener('change', updatePageFontPreview);
}

function updatePageFontPreview() {
  const sel = document.getElementById('s_page_font');
  const preview = document.getElementById('pageFontPreview');
  if (!sel || !preview) return;
  const id = sel.value;
  const stack = spPageFontStack(id);
  if (stack) spLoadPageFont(id);
  preview.style.fontFamily = stack || '';
}

function ensureClockFontOptions() {
  const sel = document.getElementById('s_clock_font');
  if (!sel || sel.dataset.ready === '1') return;
  const opts = ['<option value="">跟随系统（默认）</option>'];
  SP_CLOCK_FONTS.forEach(f => {
    opts.push('<option value="' + f.id + '">' + esc(f.name) + '</option>');
  });
  sel.innerHTML = opts.join('');
  sel.dataset.ready = '1';
  sel.addEventListener('change', updateClockFontPreview);
}

function updateClockFontPreview() {
  const sel = document.getElementById('s_clock_font');
  const preview = document.getElementById('clockFontPreview');
  if (!sel || !preview) return;
  const id = sel.value;
  const stack = spClockFontStack(id);
  if (stack) spLoadClockFont(id);
  preview.style.fontFamily = stack || '';
}

/* 分区内部二级导航：通用实现（外观 / 导航 / 新闻） */
function bindPaneNav(navId, formSel, storageKey) {
  const nav = document.getElementById(navId);
  if (!nav) return;
  const panes = Array.from(document.querySelectorAll(formSel + ' .subpane'));
  const tabs = Array.from(nav.querySelectorAll('.subtab'));
  if (!panes.length || !tabs.length) return;

  const show = (pane) => {
    tabs.forEach(t => t.classList.toggle('active', t.dataset.subpane === pane));
    panes.forEach(p => { p.hidden = p.dataset.pane !== pane; });
    try { localStorage.setItem(storageKey, pane); } catch (e) { /* 隐私模式忽略 */ }
  };

  tabs.forEach(t => t.addEventListener('click', () => show(t.dataset.subpane)));

  let saved = null;
  try { saved = localStorage.getItem(storageKey); } catch (e) { /* 忽略 */ }
  if (!saved || !tabs.some(t => t.dataset.subpane === saved)) {
    saved = tabs[0].dataset.subpane;
  }
  show(saved);
}

function bindThemeSubnav() {
  bindPaneNav('themeSubnav', '#sf-theme', 'sp_theme_pane');
}

function bindSettings() {
  // 外观与卡片内部二级导航（主题 / 布局 / 字体 / 时钟 / 壁纸）
  bindThemeSubnav();
  // 字体下拉：整页 + 时钟，填充 12 款字体并绑定预览
  ensurePageFontOptions();
  ensureClockFontOptions();
  // 内容区域滑条：拖动时同步显示当前数值
  ['maxwidth', 'pad_lr', 'pad_top', 'pad_bottom'].forEach(k => {
    const range = document.getElementById('s_content_' + k);
    const out = document.getElementById('v_content_' + k);
    if (range && out) {
      range.addEventListener('input', () => { out.textContent = range.value; });
    }
  });

  // 搜索栏宽度滑条联动
  const swRange = document.getElementById('s_search_width');
  const swOut = document.getElementById('v_search_width');
  if (swRange && swOut) swRange.addEventListener('input', () => { swOut.textContent = swRange.value; });

  // 遮罩透明度 / 壁纸模糊 / 卡片透明度滑条联动
  [['s_mask_opacity', 'v_mask_opacity'], ['s_wallpaper_blur', 'v_wallpaper_blur'], ['s_card_opacity', 'v_card_opacity']].forEach(([rid, oid]) => {
    const r = document.getElementById(rid);
    const o = document.getElementById(oid);
    if (r && o) r.addEventListener('input', () => { o.textContent = r.value; });
  });

  // 壁纸图库弹窗：打开时加载最新列表
  const openGalleryBtn = document.getElementById('openGalleryBtn');
  if (openGalleryBtn) openGalleryBtn.onclick = () => {
    document.getElementById('wpGalleryModal').classList.add('show');
    loadWallpaperGallery();
  };
  // 图库分区切换：我的上传 / 内置壁纸
  const wpTabs = document.getElementById('wpGalleryTabs');
  if (wpTabs) {
    wpTabs.addEventListener('click', e => {
      const btn = e.target.closest('.wp-tab');
      if (!btn) return;
      state.wpTab = btn.dataset.wpTab === 'preset' ? 'preset' : 'uploaded';
      renderWallpaperGallery();
    });
  }

  // 壁纸清空：地址置空并取消图库选中态
  const clearWpBtn = document.getElementById('clearWallpaperBtn');
  if (clearWpBtn) clearWpBtn.onclick = () => {
    document.getElementById('s_wallpaper').value = '';
    updateSettingPreview('s_wallpaper');
    syncGalleryActive('');
  };

  document.getElementById('addEngineBtn').onclick = () => addEngineRow('', '');
  ['s_site_logo', 's_wallpaper'].forEach(id => {
    document.getElementById(id).addEventListener('input', () => updateSettingPreview(id));
  });

  // 主题实时预览：切换选择立即在后台生效
  ['s_default_theme', 's_theme_style'].forEach(id => {
    document.getElementById(id).addEventListener('change', () => {
      applyTheme({
        default_theme: document.getElementById('s_default_theme').value,
        theme_style: document.getElementById('s_theme_style').value,
      });
      document.getElementById('s_default_theme_note').textContent =
        document.getElementById('s_default_theme').value === 'system'
          ? '跟随系统：访客未手动切换主题时，随其系统深浅自动变化'
          : '';
    });
  });

  // 分区独立保存：每个分区表单只提交自己的设置项
  document.querySelectorAll('#tab-settings form[data-section]').forEach(form => {
    form.addEventListener('submit', async e => {
      e.preventDefault();
      const key = form.dataset.section;
      const sec = SETTING_SECTIONS[key];
      if (!sec) return;
      const btn = e.submitter || document.querySelector('.section-save[form="' + form.id + '"]') ||
        form.querySelector('[type="submit"]');
      if (btn) { btn.disabled = true; btn.textContent = '保存中…'; }
      try {
        const payload = {};
        sec.keys.forEach(k => { payload[k] = collectSetting(k); });
        await API.post(API_BASE + 'settings.php?action=save', payload);
        if (key === 'basic') state.siteUrl = (payload.site_url || '').replace(/\/+$/, '');
        if (key === 'theme') {
          applyTheme({
            default_theme: document.getElementById('s_default_theme').value,
            theme_style: document.getElementById('s_theme_style').value,
          });
        }
        toast(sec.label + '已保存' + (key === 'theme' ? '，部分设置需刷新主页生效' : ''), 'success');
        // 跨标签广播：让已打开的主页（另一个标签）自动拉新设置并重渲染
        try { localStorage.setItem('sp_settings_updated', Date.now().toString()); } catch (e) {}
      } catch (err) {
        toast(err.message, 'error');
      } finally {
        if (btn) { btn.disabled = false; btn.textContent = '保存'; }
      }
    });
  });
}

/* ================= 站点设置：二级导航分区切换 ================= */
function bindSettingsSubnav() {
  const nav = document.getElementById('settingsSubnav');
  if (!nav) return;
  // 分区卡片与导航按钮按文档顺序一一对应（basic/theme/media/clock/search/news）
  const sections = Array.from(document.querySelectorAll('#tab-settings .settings-section'));
  const tabs = Array.from(nav.querySelectorAll('.subtab'));

  function show(panel) {
    tabs.forEach((t, i) => {
      const on = t.dataset.panel === panel;
      t.classList.toggle('active', on);
      if (sections[i]) sections[i].hidden = !on;
    });
    try { localStorage.setItem('sp_settings_panel', panel); } catch (e) { /* 隐私模式忽略 */ }
  }

  tabs.forEach(t => t.addEventListener('click', () => show(t.dataset.panel)));

  let saved = null;
  try { saved = localStorage.getItem('sp_settings_panel'); } catch (e) { /* 忽略 */ }
  if (!saved || !tabs.some(t => t.dataset.panel === saved)) saved = 'basic';
  show(saved);
}

/* ================= 备份与恢复 ================= */
function bindBackup() {
  const exportBtn = document.getElementById('exportBackupBtn');
  const importBtn = document.getElementById('importBackupBtn');
  const resetBtn = document.getElementById('resetBackupBtn');
  const fileInput = document.getElementById('importBackupFile');
  if (!exportBtn || !importBtn || !fileInput) return;

  const exportFormatSel = document.getElementById('exportFormatSel');
  // 导出：直接下载（GET 带登录态 Cookie），可选 JSON 完整备份或 HTML 书签
  exportBtn.onclick = () => {
    const format = exportFormatSel ? exportFormatSel.value : 'json';
    exportBtn.disabled = true;
    setTimeout(() => { exportBtn.disabled = false; }, 2000);
    location.href = API_BASE + 'backup.php?action=export&format=' + encodeURIComponent(format) + '&t=' + Date.now();
  };

  // 导入：选文件 → 强确认 → 上传 → 成功后整页刷新
  importBtn.onclick = () => fileInput.click();

  fileInput.addEventListener('change', async () => {
    const file = fileInput.files && fileInput.files[0];
    fileInput.value = '';
    if (!file) return;
    if (!/\.json$/i.test(file.name)) {
      toast('请选择导出生成的 .json 备份文件', 'error');
      return;
    }
    if (!await uiConfirm('导入将【清空】当前全部分组、卡片、站点设置和图标 / 壁纸文件，然后用「' + file.name + '」恢复。\n用户账号不受影响。\n\n确定继续吗？', { danger: true })) return;
    if (!await uiConfirm('再次确认：此操作不可撤销，建议先点「导出配置」备份当前数据。现在开始导入吗？', { danger: true })) return;

    importBtn.disabled = true;
    const oldText = importBtn.textContent;
    importBtn.textContent = '导入中…';
    try {
      const fd = new FormData();
      fd.append('file', file);
      const res = await API.postForm(API_BASE + 'backup.php?action=import', fd);
      let msg = '导入完成：' + res.groups + ' 个分组、' + res.items + ' 张卡片、' + res.settings + ' 项设置、' + res.files + ' 个文件，页面即将刷新';
      if (res.orphan_items) msg += '（跳过 ' + res.orphan_items + ' 张引用失效分组的卡片）';
      toast(msg, 'success');
      setTimeout(() => location.reload(), 1200);
    } catch (err) {
      toast('导入失败：' + err.message, 'error');
    } finally {
      importBtn.disabled = false;
      importBtn.textContent = oldText;
    }
  });

  // 清空恢复初始状态
  if (resetBtn) {
    resetBtn.onclick = async () => {
      if (!await uiConfirm('此操作将【删除】全部分组、卡片、自定义站点设置和图标 / 壁纸文件，恢复到刚安装时的默认状态。\n用户账号不受影响。\n\n确定继续吗？', { danger: true })) return;
      if (!await uiConfirm('再次确认：此操作不可撤销，建议先点「导出配置」备份当前数据。现在开始恢复初始状态吗？', { danger: true })) return;
      resetBtn.disabled = true;
      const oldText = resetBtn.textContent;
      resetBtn.textContent = '恢复中…';
      try {
        const res = await API.post(API_BASE + 'backup.php?action=reset', {});
        toast('已恢复初始状态：' + res.settings + ' 项默认设置、' + res.groups + ' 个示例分组、' + res.items + ' 张示例卡片，清除 ' + res.files_cleared + ' 个文件，页面即将刷新', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (err) {
        toast('恢复失败：' + err.message, 'error');
      } finally {
        resetBtn.disabled = false;
        resetBtn.textContent = oldText;
      }
    };
  }

  // ===== 自动备份 =====
  const autoToggle = document.getElementById('autoBackupToggle');
  const autoIntervalSel = document.getElementById('autoBackupIntervalSel');
  const manualBackupBtn = document.getElementById('manualBackupBtn');

  if (autoToggle) {
    autoToggle.addEventListener('change', async () => {
      try {
        await API.post(API_BASE + 'settings.php?action=save', { auto_backup_enabled: autoToggle.checked ? '1' : '0' });
        toast(autoToggle.checked ? '已开启自动备份' : '已关闭自动备份', 'success');
      } catch (e) {
        toast(e.message, 'error');
      }
    });
  }
  if (autoIntervalSel) {
    autoIntervalSel.addEventListener('change', async () => {
      try {
        await API.post(API_BASE + 'settings.php?action=save', { auto_backup_interval: autoIntervalSel.value });
        toast('自动备份间隔已设为每 ' + autoIntervalSel.value + ' 天', 'success');
      } catch (e) {
        toast(e.message, 'error');
      }
    });
  }
  if (manualBackupBtn) {
    manualBackupBtn.onclick = async () => {
      manualBackupBtn.disabled = true;
      const oldText = manualBackupBtn.textContent;
      manualBackupBtn.textContent = '备份中…';
      try {
        const res = await API.post(API_BASE + 'backup.php?action=create', {});
        toast('已生成手动备份：' + res.name, 'success');
        await loadBackupList();
      } catch (e) {
        toast('备份失败：' + e.message, 'error');
      } finally {
        manualBackupBtn.disabled = false;
        manualBackupBtn.textContent = oldText;
      }
    };
  }

  if (state.role === 'admin') loadBackupList();
}

/** 回填自动备份开关与间隔（值来自 settings.php?action=list） */
function applyBackupSettings(s) {
  const toggle = document.getElementById('autoBackupToggle');
  const sel = document.getElementById('autoBackupIntervalSel');
  if (toggle) toggle.checked = (s.auto_backup_enabled ?? '0') === '1';
  if (sel) sel.value = s.auto_backup_interval || '3';
}

function fmtBackupSize(n) {
  if (!n) return '0 B';
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  return (n / 1024 / 1024).toFixed(1) + ' MB';
}

function renderBackupList(el, list, type) {
  el.innerHTML = '';
  if (!list || !list.length) {
    const empty = document.createElement('div');
    empty.className = 'backup-list-empty';
    empty.textContent = type === 'auto' ? '暂无自动备份' : '暂无手动备份';
    el.appendChild(empty);
    return;
  }
  list.forEach(item => {
    const row = document.createElement('div');
    row.className = 'backup-item';

    const meta = document.createElement('div');
    meta.className = 'backup-item-meta';
    const name = document.createElement('span');
    name.className = 'backup-item-name';
    name.textContent = item.name;
    const sub = document.createElement('span');
    sub.className = 'backup-item-sub';
    sub.textContent = (item.created || '') + ' · ' + fmtBackupSize(item.size);
    meta.appendChild(name);
    meta.appendChild(sub);

    const actions = document.createElement('div');
    actions.className = 'backup-item-actions';

    const dl = document.createElement('a');
    dl.className = 'btn btn-sm';
    dl.textContent = '下载';
    dl.href = API_BASE + 'backup.php?action=download&type=' + encodeURIComponent(type) + '&name=' + encodeURIComponent(item.name);

    const restore = document.createElement('button');
    restore.type = 'button';
    restore.className = 'btn btn-sm';
    restore.textContent = '恢复';
    restore.onclick = () => restoreBackup(type, item.name);

    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'btn btn-sm btn-danger';
    del.textContent = '删除';
    del.onclick = () => deleteBackup(type, item.name);

    actions.appendChild(dl);
    actions.appendChild(restore);
    actions.appendChild(del);
    row.appendChild(meta);
    row.appendChild(actions);
    el.appendChild(row);
  });
}

// loadClipStorageStats 备份页显示正文快照与整页截图的占用体积
async function loadClipStorageStats() {
  const hint = document.getElementById('clipStorageHint');
  if (!hint || state.role !== 'admin') return;
  try {
    const s = await API.get(API_BASE + 'backup.php?action=stats');
    hint.textContent =
      '正文快照 ' + (s.snapshot_count || 0) + ' 条（' + formatSize(s.snapshot_bytes) + '） · ' +
      '整页截图 ' + (s.screenshot_count || 0) + ' 张（' + formatSize(s.screenshot_bytes) + '） · ' +
      '合计 ' + formatSize(s.total_bytes);
  } catch (e) {
    hint.textContent = '占用统计失败：' + e.message;
  }
}

function formatSize(bytes) {
  const n = Number(bytes) || 0;
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  return (n / 1024 / 1024).toFixed(2) + ' MB';
}

async function loadBackupList() {
  const autoEl = document.getElementById('autoBackupList');
  const manualEl = document.getElementById('manualBackupList');
  if (!autoEl || !manualEl || state.role !== 'admin') return;
  try {
    const res = await API.get(API_BASE + 'backup.php?action=list');
    renderBackupList(autoEl, res.auto || [], 'auto');
    renderBackupList(manualEl, res.manual || [], 'manual');
    loadClipStorageStats();
  } catch (e) {
    autoEl.innerHTML = '';
    const err = document.createElement('div');
    err.className = 'backup-list-empty';
    err.textContent = '备份列表读取失败：' + e.message;
    autoEl.appendChild(err);
    manualEl.innerHTML = '';
  }
}

async function restoreBackup(type, name) {
  const label = type === 'auto' ? '自动备份' : '手动备份';
  if (!await uiConfirm('将用' + label + '「' + name + '」覆盖当前全部分组、卡片和站点设置（用户账号不受影响，图片文件保持不变）。\n\n确定继续吗？', { danger: true })) return;
  if (!await uiConfirm('再次确认：此操作不可撤销，当前配置将被覆盖。现在恢复吗？', { danger: true })) return;
  try {
    const res = await API.post(API_BASE + 'backup.php?action=restore', { type, name });
    toast('恢复完成：' + res.groups + ' 个分组、' + res.items + ' 张卡片、' + res.settings + ' 项设置，页面即将刷新', 'success');
    setTimeout(() => location.reload(), 1200);
  } catch (e) {
    toast('恢复失败：' + e.message, 'error');
  }
}

async function deleteBackup(type, name) {
  if (!await uiConfirm('确定删除备份「' + name + '」吗？删除后无法找回。', { danger: true })) return;
  try {
    await API.post(API_BASE + 'backup.php?action=delete', { type, name });
    toast('已删除备份：' + name, 'success');
    await loadBackupList();
  } catch (e) {
    toast('删除失败：' + e.message, 'error');
  }
}

/* ================= 主题化确认弹窗（替代浏览器原生 confirm：与主题风格同步 + 居中） ================= */
// 用法：await uiConfirm('文案') / await uiConfirm('文案', { title, okText, danger })
function uiConfirm(message, opts) {
  const m = document.getElementById('confirmModal');
  if (!m) return Promise.resolve(window.confirm(message)); // 弹窗缺失时兜底原生
  const o = opts || {};
  const t = document.getElementById('confirmTitle');
  const txt = document.getElementById('confirmText');
  const ok = document.getElementById('confirmOkBtn');
  const cancel = document.getElementById('confirmCancelBtn');
  const x = document.getElementById('confirmXBtn');
  t.textContent = o.title || '确认操作';
  txt.textContent = message;
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
    ok.onclick = () => done(true);
    cancel.onclick = () => done(false);
    x.onclick = () => done(false);
    m.addEventListener('click', onBg);
    document.addEventListener('keydown', onKey);
  });
}

/* ================= 账号 & 用户管理 ================= */
async function loadUsers() {
  const tbody = document.getElementById('usersTbody');
  if (!tbody) return;
  if (state.role !== 'admin') return; // 仅管理员可管理用户
  try {
    const data = await API.get(API_BASE + 'user.php?action=list');
    tbody.innerHTML = '';
    (data.users || []).forEach(u => {
      const role = ROLE_LABEL[u.role] ? u.role : 'admin';
      const tr = document.createElement('tr');
      tr.innerHTML =
        '<td>' + u.id + '</td>' +
        '<td>' + esc(u.username) + (u.id === data.current_id ? ' <span style="font-size:12px;color:var(--accent)">（当前）</span>' : '') + '</td>' +
        '<td style="color:var(--text-muted)">' + esc(u.name || '-') + '</td>' +
        '<td><span class="role-badge role-' + role + '">' + ROLE_LABEL[role] + '</span></td>' +
        '<td><div class="actions">' +
        '<button class="btn btn-sm" data-act="pwd">重置密码</button>' +
        '<button class="btn btn-sm" data-act="role" ' + (u.id === data.current_id ? 'disabled title="不能修改当前登录账号的权限组"' : '') + '>权限</button>' +
        '<button class="btn btn-sm btn-danger" data-act="del" ' + (u.id === data.current_id ? 'disabled title="不能删除当前登录账号"' : '') + '>删除</button>' +
        '</div></td>';
      tr.querySelectorAll('[data-act]').forEach(btn => {
        btn.onclick = () => userAction(btn.dataset.act, u);
      });
      tbody.appendChild(tr);
    });
  } catch (e) {
    tbody.innerHTML = '<tr><td colspan="5" style="text-align:center;color:var(--text-muted)">用户列表加载失败：' + esc(e.message) + '</td></tr>';
  }
}

async function userAction(act, u) {
  if (act === 'pwd') {
    document.getElementById('userPwdTitle').textContent = '重置密码 - ' + u.username;
    document.getElementById('up_id').value = u.id;
    document.getElementById('up_password').value = '';
    document.getElementById('userPwdModal').classList.add('show');
    return;
  }
  if (act === 'role') {
    document.getElementById('userRoleTitle').textContent = '权限分配 - ' + (u.name || u.username);
    document.getElementById('ur_id').value = u.id;
    document.getElementById('ur_role').value = ROLE_LABEL[u.role] ? u.role : 'admin';
    document.getElementById('userRoleModal').classList.add('show');
    return;
  }
  if (act === 'del') {
    if (!await uiConfirm('确定删除用户「' + (u.name || u.username) + '」？', { danger: true })) return;
    API.post(API_BASE + 'user.php?action=delete', { id: u.id })
      .then(() => { toast('已删除', 'success'); loadUsers(); })
      .catch(e => toast(e.message, 'error'));
  }
}

function bindAccount() {
  document.getElementById('saveNameBtn').onclick = async () => {
    try {
      const res = await API.post(API_BASE + 'user.php?action=update', {
        name: document.getElementById('accName').value.trim(),
      });
      document.getElementById('whoami').textContent = res.name || '';
      toast('已保存', 'success');
    } catch (e) {
      toast(e.message, 'error');
    }
  };

  document.getElementById('pwdForm').addEventListener('submit', async e => {
    e.preventDefault();
    const oldPwd = document.getElementById('oldPwd').value;
    const newPwd = document.getElementById('newPwd').value;
    const newPwd2 = document.getElementById('newPwd2').value;
    if (newPwd !== newPwd2) {
      toast('两次输入的新密码不一致', 'error');
      return;
    }
    const btn = document.getElementById('savePwdBtn');
    btn.disabled = true;
    try {
      await API.post(API_BASE + 'user.php?action=change_password', {
        old_password: oldPwd,
        new_password: newPwd,
      });
      toast('密码已修改', 'success');
      document.getElementById('oldPwd').value = '';
      document.getElementById('newPwd').value = '';
      document.getElementById('newPwd2').value = '';
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      btn.disabled = false;
    }
  });

  // 新增用户（可指定权限组）
  document.getElementById('userAddForm').addEventListener('submit', async e => {
    e.preventDefault();
    const btn = document.getElementById('userAddBtn');
    btn.disabled = true;
    try {
      await API.post(API_BASE + 'user.php?action=create', {
        username: document.getElementById('u_username').value.trim(),
        name: document.getElementById('u_name').value.trim(),
        password: document.getElementById('u_password').value,
        role: document.getElementById('u_role').value,
      });
      toast('用户已创建', 'success');
      document.getElementById('u_username').value = '';
      document.getElementById('u_name').value = '';
      document.getElementById('u_password').value = '';
      loadUsers();
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      btn.disabled = false;
    }
  });

  // 修改指定用户权限组
  document.getElementById('userRoleForm').addEventListener('submit', async e => {
    e.preventDefault();
    const btn = document.getElementById('userRoleSaveBtn');
    btn.disabled = true;
    try {
      await API.post(API_BASE + 'user.php?action=set_role', {
        id: Number(document.getElementById('ur_id').value) || 0,
        role: document.getElementById('ur_role').value,
      });
      document.getElementById('userRoleModal').classList.remove('show');
      toast('权限已更新', 'success');
      loadUsers();
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      btn.disabled = false;
    }
  });

  // 重置指定用户密码
  document.getElementById('userPwdForm').addEventListener('submit', async e => {
    e.preventDefault();
    const btn = document.getElementById('userPwdSaveBtn');
    btn.disabled = true;
    try {
      await API.post(API_BASE + 'user.php?action=set_password', {
        id: Number(document.getElementById('up_id').value) || 0,
        new_password: document.getElementById('up_password').value,
      });
      document.getElementById('userPwdModal').classList.remove('show');
      toast('密码已重置', 'success');
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      btn.disabled = false;
    }
  });

  loadUsers();
}

/* ================= 审计日志 tab ================= */
const AUDIT_PAGE_SIZE = 20; // 审计日志每页条数

function bindAudit() {
  if (state.role !== 'admin') return;
  const tbody = document.getElementById('auditTbody');
  if (!tbody) return;
  document.getElementById('auditRefreshBtn')?.addEventListener('click', () => {
    state.auditPage = 1;
    loadAudit();
  });
  document.getElementById('auditFilterType')?.addEventListener('change', () => {
    state.auditPage = 1;
    loadAudit();
  });
  document.getElementById('auditClearBtn')?.addEventListener('click', async () => {
    const filter = document.getElementById('auditFilterType')?.value || '';
    const scope = filter ? '当前筛选类型' : '全部';
    if (!await uiConfirm('确定清空' + scope + '审计日志？此操作不可撤销', { danger: true })) return;
    try {
      await API.post(API_BASE + 'audit.php?action=clear' + (filter ? '&filter=' + encodeURIComponent(filter) : ''));
      toast('已清空' + scope + '审计日志', 'success');
      state.auditPage = 1;
      loadAudit();
    } catch (e) { toast(e.message, 'error'); }
  });
  // tab 切换到 audit 时自动加载
  document.querySelectorAll('.admin-tabs .tab').forEach(t => {
    if (t.dataset.tab === 'audit') t.addEventListener('click', () => {
      state.auditPage = 1;
      loadAudit();
    });
  });
  // 分页按钮（事件委托）
  document.getElementById('auditPager')?.addEventListener('click', e => {
    const btn = e.target.closest('[data-audit-page]');
    if (!btn || btn.disabled) return;
    const pages = Math.max(1, Math.ceil(state.auditTotal / AUDIT_PAGE_SIZE));
    if (btn.dataset.auditPage === 'prev' && state.auditPage > 1) state.auditPage--;
    else if (btn.dataset.auditPage === 'next' && state.auditPage < pages) state.auditPage++;
    else return;
    loadAudit();
  });
}

async function loadAudit() {
  const tbody = document.getElementById('auditTbody');
  if (!tbody) return;
  const filter = document.getElementById('auditFilterType')?.value || '';
  tbody.innerHTML = '<tr><td colspan="8" style="text-align:center;color:var(--text-muted);padding:20px">加载中…</td></tr>';
  try {
    const url = API_BASE + 'audit.php?action=list&page=' + state.auditPage +
      '&pagesize=' + AUDIT_PAGE_SIZE + (filter ? '&filter=' + encodeURIComponent(filter) : '');
    const data = await API.get(url);
    state.auditTotal = data.total || 0;
    const pages = Math.max(1, Math.ceil(state.auditTotal / AUDIT_PAGE_SIZE));
    if (state.auditPage > pages) {
      state.auditPage = pages;
      return loadAudit();
    }
    const list = data.list || [];
    tbody.innerHTML = list.length === 0
      ? '<tr><td colspan="8" style="text-align:center;color:var(--text-muted);padding:30px">暂无日志</td></tr>'
      : list.map(r => {
          const typeTag = r.action.startsWith('auth') ? '登录'
            : (r.action.startsWith('2fa') ? '2FA'
            : (r.action.startsWith('guest') ? '访客'
            : (r.action.startsWith('item') || r.action.startsWith('group') ? '操作' : '系统')));
          const typeColor = r.action.startsWith('auth') ? 'var(--blue)'
            : (r.action === 'audit.clear' ? 'var(--danger)' : 'var(--text-muted)');
          const resultTag = r.result === 'success'
            ? '<span style="color:#2fa85a">成功</span>'
            : '<span style="color:var(--danger)">失败</span>';
          return `<tr>
            <td style="white-space:nowrap">${(r.created_at || '').replace('T', ' ').slice(0, 19)}</td>
            <td><span style="background:${typeColor}22;color:${typeColor};padding:2px 8px;border-radius:4px;font-size:12px">${typeTag}</span></td>
            <td>${esc(r.actor || '')}</td>
            <td style="font-family:monospace;font-size:12px">${esc(r.action)}</td>
            <td>${esc(r.target || '')}</td>
            <td>${resultTag}</td>
            <td style="font-family:monospace;font-size:12px">${esc(r.ip || '')}</td>
            <td title="${esc(r.region || '')}">${esc(r.region || '—')}</td>
          </tr>`;
        }).join('');
    // 分页
    const pager = document.getElementById('auditPager');
    if (pager) {
      pager.hidden = false;
      pager.innerHTML = '<div class="pager-nav">' +
        '<button class="pager-btn" type="button" data-audit-page="prev"' + (state.auditPage <= 1 ? ' disabled' : '') + '>‹ 上一页</button>' +
        '<span class="pager-info">第 ' + state.auditPage + ' / ' + pages + ' 页 · 共 ' + state.auditTotal + ' 条</span>' +
        '<button class="pager-btn" type="button" data-audit-page="next"' + (state.auditPage >= pages ? ' disabled' : '') + '>下一页 ›</button>' +
        '</div>';
    }
  } catch (e) {
    tbody.innerHTML = '<tr><td colspan="8" style="text-align:center;color:var(--danger);padding:20px">' + esc(e.message) + '</td></tr>';
  }
}

/* ================= 自定义 RSS 源 ================= */
function bindFeeds() {
  const tbody = document.getElementById('feedsTbody');
  if (!tbody) return;
  loadFeeds();
  document.getElementById('addFeedBtn')?.addEventListener('click', async () => {
    const title = document.getElementById('feedTitle').value.trim();
    const url = document.getElementById('feedURL').value.trim();
    if (!title) { toast('请填写标题', 'error'); return; }
    if (!/^https?:\/\//i.test(url)) { toast('URL 必须以 http:// 或 https:// 开头', 'error'); return; }
    try {
      await API.post(API_BASE + 'feeds.php?action=save', { title, url, sort: 0, enabled: 1 });
      document.getElementById('feedTitle').value = '';
      document.getElementById('feedURL').value = '';
      toast('已添加', 'success');
      loadFeeds();
    } catch (e) { toast(e.message, 'error'); }
  });
}
async function loadFeeds() {
  const tbody = document.getElementById('feedsTbody');
  tbody.innerHTML = '<tr><td colspan="6" style="text-align:center;color:var(--text-muted);padding:14px">加载中…</td></tr>';
  try {
    const feeds = await API.get(API_BASE + 'feeds.php?action=list');
    tbody.innerHTML = feeds.length === 0
      ? '<tr><td colspan="6" style="text-align:center;color:var(--text-muted);padding:20px">尚无自定义 RSS 源，在上方添加</td></tr>'
      : feeds.map(f => `
          <tr>
            <td>${f.id}</td>
            <td>${esc(f.title)}</td>
            <td style="font-family:monospace;font-size:12px;word-break:break-all">${esc(f.url)}</td>
            <td><input type="checkbox" ${f.enabled ? 'checked' : ''} data-feed-id="${f.id}" class="feed-toggle"></td>
            <td>${f.sort ?? 0}</td>
            <td>
              <button class="btn btn-sm" data-feed-del="${f.id}">删除</button>
            </td>
          </tr>
        `).join('');
    // 事件委托
    tbody.querySelectorAll('.feed-toggle').forEach(cb => cb.addEventListener('change', async () => {
      const id = cb.dataset.feedId;
      try {
        await API.post(API_BASE + 'feeds.php?action=toggle', { id });
        toast('状态已切换', 'success');
      } catch (e) { toast(e.message, 'error'); cb.checked = !cb.checked; }
    }));
    tbody.querySelectorAll('[data-feed-del]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.dataset.feedDel;
      if (!await uiConfirm('确定删除该 RSS 源？', { danger: true })) return;
      try { await API.post(API_BASE + 'feeds.php?action=delete', { id }); toast('已删除', 'success'); loadFeeds(); }
      catch (e) { toast(e.message, 'error'); }
    }));
  } catch (e) {
    tbody.innerHTML = '<tr><td colspan="6" style="text-align:center;color:var(--danger);padding:20px">' + esc(e.message) + '</td></tr>';
  }
}

/* ================= 书签导入 ================= */
function bindImportBookmarks() {
  const openBtn = document.getElementById('openBookmarksBtn');
  const fileInput = document.getElementById('bookmarksFile');
  const preview = document.getElementById('bookmarksPreview');
  const applyBtn = document.getElementById('applyBookmarksBtn');
  const modeSel = document.getElementById('bookmarksMode');
  if (!openBtn) return;

  openBtn.addEventListener('click', () => fileInput.click());
  fileInput.addEventListener('change', async () => {
    const f = fileInput.files[0];
    if (!f) { preview.textContent = ''; applyBtn.hidden = true; return; }
    preview.textContent = `解析 ${f.name}…`;
    applyBtn.hidden = true;
    const form = new FormData();
    form.append('file', f);
    try {
      const data = await API.postForm(API_BASE + 'import.php?action=bookmarks', form);
      preview.textContent = `✅ ${data.total} 条 / ${data.groups.length} 个分组 可导入`;
      applyBtn.hidden = false;
      applyBtn.onclick = async () => {
        const mode = modeSel ? modeSel.value : 'merge';
        const tip = mode === 'overwrite'
          ? `覆盖导入会先删除现有的全部分组和卡片，再导入 ${data.total} 条链接，是否继续？`
          : `将增量导入 ${data.groups.length} 个分组、${data.total} 张卡片，已有链接不会重复，是否继续？`;
        if (!await uiConfirm(tip, { danger: mode === 'overwrite' })) return;
        applyBtn.disabled = true;
        try {
          // 后端不缓存解析结果，确认后重新上传同一文件执行写入
          const form2 = new FormData();
          form2.append('file', f);
          form2.append('mode', mode);
          const r = await API.postForm(API_BASE + 'import.php?action=import', form2);
          const prefix = r.mode === 'overwrite' ? '覆盖导入完成' : '导入完成';
          toast(`${prefix}：新增 ${r.imported} 张，跳过重复 ${r.skipped} 张，新建 ${r.groups_created} 个分组`, 'success');
          preview.textContent = `✅ 已导入 ${r.imported} 张卡片到 ${data.groups.length} 个分组`;
          applyBtn.hidden = true;
          fileInput.value = '';
        } catch (e) {
          toast(e.message, 'error');
        } finally {
          applyBtn.disabled = false;
        }
      };
    } catch (e) {
      preview.textContent = '❌ 解析失败：' + e.message;
    }
  });
}

/* ================= 两步验证 (2FA) ================= */
function bindTwoFA() {
  const status = document.getElementById('twofaStatus');
  const setupBtn = document.getElementById('twofaSetupBtn');
  const enableBtn = document.getElementById('twofaEnableBtn');
  const disableBtn = document.getElementById('twofaDisableBtn');
  const cancelBtn = document.getElementById('twofaCancelBtn');
  const setupBox = document.getElementById('twofaSetup');
  const qr = document.getElementById('twofaQR');
  const secretEl = document.getElementById('twofaSecret');

  if (!status) return;
  refresh2FAStatus();

  setupBtn?.addEventListener('click', async () => {
    try {
      const data = await API.post(API_BASE + 'auth.php?action=2fa_setup');
      // 用 qrcode-generator 库渲染真正的二维码 + 显示 secret
      if (typeof qrcode === 'function') {
        const qrg = qrcode(0, 'M');
        qrg.addData(data.url);
        qrg.make();
        qr.innerHTML = `<div style="display:flex;flex-direction:column;align-items:center;gap:10px">${qrg.createImgTag(4, 4)}</div>`;
        // 修正 img 样式：白底圆角 + 适当尺寸
        const img = qr.querySelector('img');
        if (img) {
          img.style.background = '#fff';
          img.style.padding = '6px';
          img.style.borderRadius = '12px';
          img.style.width = '200px';
          img.style.height = '200px';
          img.style.display = 'block';
        }
      } else {
        // 降级：无 QR 库时显示纯文本 URL
        qr.innerHTML = `<div style="background:rgba(255,255,255,0.08);padding:16px;border-radius:12px;font-family:monospace;font-size:13px;max-width:320px;word-break:break-all">${esc(data.url)}</div>`;
      }
      secretEl.textContent = data.secret;
      setupBox.hidden = false;
      setupBtn.style.display = 'none';
    } catch (e) { toast(e.message, 'error'); }
  });
  cancelBtn?.addEventListener('click', () => { setupBox.hidden = true; setupBtn.style.display = 'inline-block'; });

  enableBtn?.addEventListener('click', async () => {
    const code = document.getElementById('twofaCode').value.trim();
    if (!code) { toast('请输入 6 位动态码', 'error'); return; }
    const secret = secretEl.textContent;
    try {
      await API.post(API_BASE + 'auth.php?action=2fa_enable', { secret, code });
      toast('2FA 已开启', 'success');
      setupBox.hidden = true;
      refresh2FAStatus();
    } catch (e) { toast(e.message, 'error'); }
  });

  disableBtn?.addEventListener('click', async () => {
    const pwd = prompt('请输入当前登录密码以确认关闭 2FA：');
    if (!pwd) return;
    const code = prompt('请输入 6 位动态码：');
    if (!code) return;
    try {
      await API.post(API_BASE + 'auth.php?action=2fa_disable', { password: pwd, code });
      toast('2FA 已关闭', 'success');
      refresh2FAStatus();
    } catch (e) { toast(e.message, 'error'); }
  });
}
async function refresh2FAStatus() {
  try {
    const u = await API.get(API_BASE + 'auth.php?action=me');
    const enabled = u.has_2fa;
    document.getElementById('twofaStatus').textContent = `状态：${enabled ? '✅ 已开启' : '未开启'}`;
    const setupBtn = document.getElementById('twofaSetupBtn');
    const disableBtn = document.getElementById('twofaDisableBtn');
    if (enabled) {
      setupBtn.style.display = 'none';
      disableBtn.style.display = 'inline-block';
    } else {
      setupBtn.style.display = 'inline-block';
      disableBtn.style.display = 'none';
    }
    // 受信任设备卡片：仅 2FA 开启时显示
    const tc = document.getElementById('trustedDevicesCard');
    if (enabled) {
      tc.hidden = false;
      loadTrustedDevices();
    } else {
      tc.hidden = true;
    }
  } catch (e) { /* ignore */ }
}

/* ================= 受信任设备 ================= */
function escapeHtml(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}
async function loadTrustedDevices() {
  const body = document.getElementById('trustedDevicesBody');
  if (!body) return;
  try {
    const list = await API.get(API_BASE + 'auth.php?action=list_trusted_devices');
    if (!Array.isArray(list) || list.length === 0) {
      body.innerHTML = '<div class="form-hint">暂无，2FA 登录时勾选「信任此设备」即可加入（每账号最多 10 台，30 天自动过期）</div>';
      return;
    }
    const rows = list.map(d => `
      <div class="td-row${d.is_current ? ' td-current' : ''}">
        <div class="td-main">
          <span class="td-name">${escapeHtml(d.device_name)}${d.is_current ? ' <span class="td-tag">当前设备</span>' : ''}</span>
          <span class="td-sub">${escapeHtml(d.ip_snippet)} · 上次 ${d.last_used_at || '—'} · 到期 ${d.expires_at}</span>
        </div>
        <button class="btn btn-danger td-revoke" data-id="${d.id}" type="button">撤销信任</button>
      </div>
    `).join('');
    body.innerHTML = `<div class="td-list">${rows}</div>
      <div style="margin-top:12px;text-align:right">
        <button class="btn btn-danger" type="button" id="revokeAllTrusted">⚠️ 一键清除所有受信任设备</button>
      </div>`;
    // 绑定
    body.querySelectorAll('.td-revoke').forEach(btn => btn.addEventListener('click', async () => {
      if (!await uiConfirm('确认撤销此设备的信任？该设备下次 2FA 登录时必须重新输入动态码。', { danger: true })) return;
      API.post(API_BASE + 'auth.php?action=delete_trusted_device', { id: Number(btn.dataset.id) }).then(() => {
        toast('已撤销', 'success'); loadTrustedDevices();
      }).catch(e => toast(e.message, 'error'));
    }));
    const ra = document.getElementById('revokeAllTrusted');
    if (ra) ra.addEventListener('click', async () => {
      if (!await uiConfirm('确认清除所有受信任设备？所有设备下次登录都必须输入 2FA 动态码。')) return;
      API.post(API_BASE + 'auth.php?action=revoke_all_trusted_devices').then(() => {
        toast('已清除全部受信任设备', 'success'); loadTrustedDevices();
      }).catch(e => toast(e.message, 'error'));
    });
  } catch (e) {
    body.innerHTML = `<div style="color:var(--danger);font-size:13px">加载失败：${escapeHtml(e.message)}</div>`;
  }
}

/* ================= 每日壁纸源下拉 ================= */
function bindDailyWallpaper() {
  // 每日壁纸源 select：change 事件给用户即时提示；实际保存走 settings.php form
  const sel = document.getElementById('s_wallpaper_source');
  if (!sel) return;
  sel.addEventListener('change', () => {
    if (sel.value) {
      toast('已选择：' + sel.options[sel.selectedIndex].text + '，刷新主页后生效', 'info');
    }
  });
  const previewBtn = document.getElementById('dailyPreviewBtn');
  if (previewBtn) previewBtn.onclick = openDailyWpModal;
  const disableBtn = document.getElementById('dailyWpDisable');
  if (disableBtn) {
    disableBtn.onclick = () => {
      sel.value = '';
      document.getElementById('dailyWpModal')?.classList.remove('show');
      toast('已关闭每日壁纸，记得点击分区「保存」生效', 'info');
    };
  }
}

const DAILY_WP_SOURCES = [
  { id: 'bing_cn', name: '🇨🇳 Bing 中文', desc: '每日必应壁纸（zh-CN）' },
  { id: 'picsum', name: '📷 Lorem Picsum', desc: '随机精选风景' },
];

/** 每日壁纸源预览弹窗：每个源随机 10 张缩略图，点选后才下载生效 */
function openDailyWpModal() {
  const modal = document.getElementById('dailyWpModal');
  const wrap = document.getElementById('dailyWpGrid');
  if (!modal || !wrap) return;

  wrap.innerHTML = DAILY_WP_SOURCES.map(s => (
    '<section class="dwp-section" id="dwpSec_' + s.id + '" data-source="' + s.id + '">' +
      '<div class="dwp-head">' +
        '<b>' + s.name + '</b>' +
        '<span class="dwp-state" id="dwpState_' + s.id + '" hidden>已固定</span>' +
        '<span class="dwp-actions">' +
          '<button class="btn btn-sm" type="button" data-dwp-refresh="' + s.id + '">🔄 换一批</button>' +
          '<button class="btn btn-sm" type="button" data-dwp-unpin="' + s.id + '" hidden>↩️ 恢复每日自动</button>' +
        '</span>' +
      '</div>' +
      '<div class="dwp-grid" id="dwpGrid_' + s.id + '"><div class="dwp-loading">加载预览…</div></div>' +
    '</section>'
  )).join('');

  wrap.querySelectorAll('[data-dwp-refresh]').forEach(btn => {
    btn.onclick = () => loadDailyPreviews(btn.dataset.dwpRefresh);
  });
  wrap.querySelectorAll('[data-dwp-unpin]').forEach(btn => {
    btn.onclick = () => unpinDailyWallpaper(btn.dataset.dwpUnpin);
  });

  modal.classList.add('show');
  DAILY_WP_SOURCES.forEach(s => loadDailyPreviews(s.id));
}

/** 拉取某源随机 10 张远程缩略图（服务端只取元数据，不下载原图） */
async function loadDailyPreviews(source) {
  const grid = document.getElementById('dwpGrid_' + source);
  if (!grid) return;
  grid.innerHTML = '<div class="dwp-loading">加载预览…</div>';
  try {
    const r = await API.get(API_BASE + 'wallpaper.php?action=preview&source=' + encodeURIComponent(source) +
      '&n=10&_=' + Date.now());
    const list = (r && r.list) || [];
    const stateEl = document.getElementById('dwpState_' + source);
    const unpinBtn = document.querySelector('[data-dwp-unpin="' + source + '"]');
    const pinned = !!(r && r.pinned);
    if (stateEl) { stateEl.hidden = !pinned; }
    if (unpinBtn) { unpinBtn.hidden = !pinned; }

    if (list.length === 0) {
      grid.innerHTML = '<div class="dwp-loading">暂无预览</div>';
      return;
    }
    grid.innerHTML = '';
    list.forEach(it => {
      const card = document.createElement('div');
      card.className = 'dwp-thumb';
      card.title = it.title || '';
      const img = document.createElement('img');
      img.src = it.thumb;
      img.alt = '';
      img.loading = 'lazy';
      img.referrerPolicy = 'no-referrer';
      const tip = document.createElement('span');
      tip.className = 'dwp-tip';
      tip.textContent = it.title || '';
      card.appendChild(img);
      card.appendChild(tip);
      card.onclick = () => pinDailyWallpaper(source, it, card);
      grid.appendChild(card);
    });
  } catch (e) {
    grid.innerHTML = '<div class="dwp-loading">预览失败：' + esc(e.message) + '</div>';
  }
}

/** 点选预览图：此时才下载原图并固定为该源壁纸，同时立即保存源设置 */
async function pinDailyWallpaper(source, item, card) {
  card.classList.add('loading');
  try {
    await API.post(API_BASE + 'wallpaper.php?action=pin', {
      source,
      url: item.full,
      title: item.title || '',
      copyright: item.copyright || '',
    });
    let applied = true;
    try {
      await API.post(API_BASE + 'settings.php?action=save', { wallpaper_source: source });
    } catch (e) {
      applied = false; // 编辑者账号无权改设置，但固定图已生效
    }
    const sel = document.getElementById('s_wallpaper_source');
    if (sel && applied) sel.value = source;
    document.getElementById('dailyWpModal')?.classList.remove('show');
    toast(applied ? '已设为壁纸，刷新主页即可看到' : '图片已固定，但当前账号无权修改壁纸源设置', applied ? 'success' : 'info');
  } catch (e) {
    card.classList.remove('loading');
    toast(e.message, 'error');
  }
}

/** 取消固定，恢复每日自动抓取 */
async function unpinDailyWallpaper(source) {
  try {
    await API.post(API_BASE + 'wallpaper.php?action=unpin', { source });
    toast('已恢复每日自动壁纸', 'success');
    loadDailyPreviews(source);
  } catch (e) {
    toast(e.message, 'error');
  }
}

/* ================= 剪藏与接入 ================= */
function bindClip() {
  const copyServer = document.getElementById('copyServerAddrBtn');
  if (!copyServer) return;

  copyServer.addEventListener('click', () => copyClipText(location.origin, copyServer));
  document.getElementById('copyTokenBtn').addEventListener('click', () =>
    copyClipText(document.getElementById('tokenValueText').value, document.getElementById('copyTokenBtn')));
  document.getElementById('copyBookmarkletBtn').addEventListener('click', () =>
    copyClipText(document.getElementById('bookmarkletCode').value, document.getElementById('copyBookmarkletBtn')));
  document.getElementById('saveClipSettingsBtn').addEventListener('click', saveClipSettings);
  document.getElementById('createTokenBtn').addEventListener('click', createClipToken);

  // 正文快照检索
  document.getElementById('clipSearchBtn').addEventListener('click', runClipSearch);
  document.getElementById('clipSearchInput').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      runClipSearch();
    }
  });

  document.getElementById('tokensTbody').addEventListener('click', async (e) => {
    const btn = e.target.closest('button[data-revoke]');
    if (!btn) return;
    const id = parseInt(btn.dataset.revoke, 10);
    const ok = await uiConfirm('吊销后该 Token 立即失效，扩展与快捷指令需要重新配置。确定吊销？', { danger: true, okText: '吊销' });
    if (!ok) return;
    btn.disabled = true;
    try {
      await API.request('/api/v1/tokens', { method: 'DELETE', body: { id } });
      toast('Token 已吊销', 'success');
      loadClipTokens();
    } catch (err) {
      toast(err.message, 'error');
      btn.disabled = false;
    }
  });

  // 书签小工具只做顶层跳转，避免页面 CSP / CORS 拦截
  document.getElementById('bookmarkletCode').value =
    "javascript:(function(){var u=location.origin+'/clip?url='+encodeURIComponent(location.href)+'&title='+encodeURIComponent(document.title);window.open(u,'_blank');})();";
}

async function loadClipPage() {
  document.getElementById('clipServerAddrText').value = location.origin;
  buildClipGroupOptions();
  try {
    const s = await API.get(API_BASE + 'settings.php?action=list');
    document.getElementById('s_clip_enabled').checked = (s.clip_enabled || '1') !== '0';
    const g = document.getElementById('s_clip_default_group');
    g.value = s.clip_default_group || '0';
    if (g.value !== (s.clip_default_group || '0')) g.value = '0';
    document.getElementById('s_clip_default_capture').value =
      (s.clip_default_capture === 'snapshot') ? 'snapshot' : 'card';
    document.getElementById('s_clip_ai_enabled').checked = (s.clip_ai_enabled || '1') !== '0';
    document.getElementById('s_clip_ai_provider').value = s.clip_ai_provider === 'api' ? 'api' : 'local';
    document.getElementById('s_clip_ai_base_url').value = s.clip_ai_base_url || '';
    document.getElementById('s_clip_ai_api_key').value = s.clip_ai_api_key || '';
    document.getElementById('s_clip_ai_model').value = s.clip_ai_model || '';
    document.getElementById('s_clip_image_domains').value = s.clip_image_domains || '';
  } catch (_) {
    /* 编辑者无站点设置读取权限时保持默认展示 */
  }
  loadClipTokens();
}

function buildClipGroupOptions() {
  const sel = document.getElementById('s_clip_default_group');
  sel.innerHTML = '<option value="0">自动（稍后读）</option>';
  const groups = state.groups || [];
  groups.filter(g => !g.parent_id).forEach(top => {
    const opt = document.createElement('option');
    opt.value = String(top.id);
    opt.textContent = top.title;
    sel.appendChild(opt);
    groups.filter(c => c.parent_id === top.id).forEach(child => {
      const copt = document.createElement('option');
      copt.value = String(child.id);
      copt.textContent = '　└ ' + child.title;
      sel.appendChild(copt);
    });
  });
}

async function saveClipSettings() {
  if (state.role !== 'admin') {
    toast('仅管理员可以修改剪藏设置', 'error');
    return;
  }
  const btn = document.getElementById('saveClipSettingsBtn');
  btn.disabled = true;
  btn.textContent = '保存中…';
  try {
    await API.post(API_BASE + 'settings.php?action=save', {
      clip_enabled: collectSetting('clip_enabled'),
      clip_default_group: collectSetting('clip_default_group'),
      clip_default_capture: collectSetting('clip_default_capture'),
      clip_ai_enabled: collectSetting('clip_ai_enabled'),
      clip_ai_provider: collectSetting('clip_ai_provider'),
      clip_ai_base_url: collectSetting('clip_ai_base_url'),
      clip_ai_api_key: collectSetting('clip_ai_api_key'),
      clip_ai_model: collectSetting('clip_ai_model'),
      clip_image_domains: collectSetting('clip_image_domains'),
    });
    toast('剪藏设置已保存', 'success');
  } catch (e) {
    toast(e.message, 'error');
  } finally {
    btn.disabled = false;
    btn.textContent = '保存设置';
  }
}

// 正文快照检索：结果渲染到后台表格，标题进入阅读页
async function runClipSearch() {
  const q = document.getElementById('clipSearchInput').value.trim();
  const wrap = document.getElementById('clipSearchWrap');
  if (!q) {
    wrap.hidden = true;
    return;
  }
  const btn = document.getElementById('clipSearchBtn');
  btn.disabled = true;
  btn.textContent = '检索中…';
  try {
    const list = await API.get('/api/v1/clips/search?q=' + encodeURIComponent(q) + '&limit=50');
    renderClipSearch(list || []);
  } catch (e) {
    toast(e.message || '检索失败', 'error');
  } finally {
    btn.disabled = false;
    btn.textContent = '检索';
  }
}

function renderClipSearch(list) {
  const wrap = document.getElementById('clipSearchWrap');
  const tbody = document.getElementById('clipSearchTbody');
  const empty = document.getElementById('clipSearchEmpty');
  wrap.hidden = false;
  tbody.innerHTML = '';
  empty.hidden = list.length > 0;

  list.forEach((hit) => {
    const tr = document.createElement('tr');

    const tdTitle = document.createElement('td');
    const link = document.createElement('a');
    link.href = '/read/' + parseInt(hit.id, 10);
    link.target = '_blank';
    link.rel = 'noopener';
    link.textContent = hit.title || hit.url || '未命名剪藏';
    tdTitle.appendChild(link);
    if (hit.snippet) {
      const tip = document.createElement('div');
      tip.className = 'form-hint';
      tip.style.marginTop = '2px';
      tip.textContent = hit.snippet;
      tdTitle.appendChild(tip);
    }
    if (Array.isArray(hit.tags) && hit.tags.length) {
      const tagLine = document.createElement('div');
      tagLine.className = 'form-hint';
      tagLine.style.marginTop = '2px';
      tagLine.textContent = hit.tags.map(t => '#' + t).join(' ');
      tdTitle.appendChild(tagLine);
    }
    tr.appendChild(tdTitle);

    const tdGroup = document.createElement('td');
    tdGroup.textContent = hit.group || '—';
    tr.appendChild(tdGroup);

    const tdWords = document.createElement('td');
    tdWords.textContent = hit.word_count ? hit.word_count + ' 字' : '—';
    tr.appendChild(tdWords);

    const tdTime = document.createElement('td');
    tdTime.textContent = hit.fetched_at || '—';
    tr.appendChild(tdTime);

    const tdOp = document.createElement('td');
    const read = document.createElement('a');
    read.href = '/read/' + parseInt(hit.id, 10);
    read.target = '_blank';
    read.rel = 'noopener';
    read.textContent = '阅读';
    tdOp.appendChild(read);
    tr.appendChild(tdOp);

    tbody.appendChild(tr);
  });
}

async function createClipToken() {
  const input = document.getElementById('tokenNameInput');
  const name = input.value.trim();
  if (!name) {
    toast('请先填写令牌名称', 'error');
    input.focus();
    return;
  }
  const btn = document.getElementById('createTokenBtn');
  btn.disabled = true;
  btn.textContent = '生成中…';
  try {
    const res = await API.post('/api/v1/tokens', {
      name,
      scopes: ['clip:write', 'groups:read'],
      expires_days: parseInt(document.getElementById('tokenExpiresSel').value, 10) || 0,
    });
    document.getElementById('tokenValueText').value = res.token;
    document.getElementById('tokenRevealBox').style.display = 'block';
    input.value = '';
    loadClipTokens();
    toast('Token 已生成，请立即复制保存', 'success');
  } catch (e) {
    toast(e.message, 'error');
  } finally {
    btn.disabled = false;
    btn.textContent = '生成 Token';
  }
}

async function loadClipTokens() {
  const tbody = document.getElementById('tokensTbody');
  const hint = document.getElementById('tokensEmptyHint');
  try {
    const list = await API.get('/api/v1/tokens');
    tbody.innerHTML = '';
    (list || []).forEach(t => {
      const stateInfo = clipTokenState(t);
      const tr = document.createElement('tr');
      tr.innerHTML = '<td>' + esc(t.name) + '<div class="form-hint" style="margin:0;font-family:ui-monospace,Consolas,monospace">' + esc(t.prefix) + '</div></td>' +
        '<td>' + esc(t.owner || '') + '</td>' +
        '<td>' + esc(t.scopes || '') + '</td>' +
        '<td>' + (t.expires_at ? esc(t.expires_at.slice(0, 10)) : '永久') + '</td>' +
        '<td>' + (t.last_used_at ? esc(t.last_used_at.slice(0, 16)) : '从未') + '</td>' +
        '<td><span class="' + stateInfo.cls + '">' + stateInfo.label + '</span></td>' +
        '<td>' + (t.revoked_at ? '—' : '<button class="btn btn-sm" type="button" data-revoke="' + t.id + '">吊销</button>') + '</td>';
      tbody.appendChild(tr);
    });
    hint.hidden = (list || []).length > 0;
  } catch (e) {
    tbody.innerHTML = '';
    hint.hidden = false;
    hint.textContent = 'Token 读取失败：' + e.message;
  }
}

function clipTokenState(t) {
  if (t.revoked_at) return { label: '已吊销', cls: 'form-hint' };
  if (t.expires_at) {
    const exp = new Date(t.expires_at.replace(' ', 'T')).getTime();
    if (!isNaN(exp) && exp < Date.now()) return { label: '已过期', cls: 'form-hint' };
  }
  return { label: '有效', cls: 'form-hint' };
}

function copyClipText(text, btn) {
  const done = () => toast('已复制', 'success');
  const fallback = () => {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand('copy');
      done();
    } catch (_) {
      toast('复制失败，请手动选择复制', 'error');
    }
    ta.remove();
  };
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(done).catch(fallback);
  } else {
    fallback();
  }
}


/**
 * 登录页逻辑 + 2FA 两步验证流程
 */
(async function () {
  // 已登录（且非访客角色）则直接进入后台
  try {
    const u = await API.get('/api/auth.php?action=me');
    if (u && u.role && u.role !== 'guest') {
      location.replace('admin.html');
      return;
    }
  } catch (e) { /* 未登录，正常展示登录页 */ }

  // 登录页只跟随后端皮肤（极光暗色 / 赛博霓虹 / 极简白），不跟随前台主题
  function applyAdminSkin(skin) {
    if (['aurora', 'neon', 'element'].indexOf(skin) < 0) skin = 'aurora';
    document.body.dataset.adminSkin = skin;
    document.documentElement.dataset.style = 'console';
    document.documentElement.dataset.theme = skin === 'element' ? 'light' : 'dark';
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.content = skin === 'element' ? '#f4f6fb' : '#0a0e17';
    try { localStorage.setItem('sp_admin_skin', skin); } catch (e) { /* 隐私模式忽略 */ }
  }

  try {
    const data = await API.get('/api/public.php');
    if (data && data.settings) applyAdminSkin(data.settings.admin_skin || 'aurora');
  } catch (e) { /* 忽略 */ }

  const form = document.getElementById('loginForm');
  const errEl = document.getElementById('loginError');
  const btn = document.getElementById('loginBtn');
  const sub = document.getElementById('loginSub');

  form.addEventListener('submit', async e => {
    e.preventDefault();
    errEl.textContent = '';
    btn.disabled = true;
    const is2FA = !document.getElementById('twofaField').hidden;
    const username = document.getElementById('username').value.trim();
    const password = document.getElementById('password').value;

    try {
      if (is2FA) {
        // 第二步：提交 2FA 动态码
        const uid = document.getElementById('twofaUID').value;
        const code = document.getElementById('twofaCode').value.trim();
        const trust = document.getElementById('trustDevice').checked ? 1 : 0;
        await API.post('/api/auth.php?action=login_2fa', { uid: Number(uid), code, trust_device: trust });
      } else {
        btn.textContent = '登录中…';
        const data = await API.post('/api/auth.php?action=login', { username, password });
        // 后端返回 need_2fa=true 说明密码过了但需要 2FA
        if (data && data.need_2fa) {
          document.getElementById('twofaUID').value = data.uid;
          document.getElementById('twofaField').hidden = false;
          document.getElementById('twofaCode').focus();
          sub.textContent = '请输入两步验证动态码';
          btn.textContent = '✅ 完成登录';
          btn.disabled = false;
          return;
        }
      }
      toast('登录成功', 'success');
      location.href = 'admin.html';
    } catch (err) {
      errEl.textContent = err.message;
      btn.disabled = false;
      btn.textContent = is2FA ? '✅ 完成登录' : '登 录';
    }
  });
})();

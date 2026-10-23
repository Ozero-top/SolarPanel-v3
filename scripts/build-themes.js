#!/usr/bin/env node
/**
 * SolarPanel 主题构建器
 * 从 themes.json 自动生成 themes.css + 同步更新 api.js SP_STYLES + admin.html option 下拉
 * 
 * 用法：node scripts/build-themes.js
 * 支持 --dry-run 只打印不写文件
 * 
 * 被 Dockerfile 的 frontend-builder 阶段调用（与 build-frontend.js 一起）
 */

const fs = require('fs');
const path = require('path');

const ROOT = path.resolve(__dirname, '..');
const FE = path.join(ROOT, 'frontend');

const THEMES_JSON = path.join(FE, 'themes.json');
const THEMES_CSS = path.join(FE, 'assets/css/themes.css');
const API_JS = path.join(FE, 'assets/js/api.js');
const ADMIN_HTML = path.join(FE, 'admin.html');

const DRY_RUN = process.argv.includes('--dry-run');

/** 主题名 → 中文显示名（与原始 admin.html option 逐字一致） */
const DISPLAY_NAMES = {
  'soft': 'Soft 柔和浮雕（默认）',
  'blueprint': 'Blueprint 工程蓝图（深蓝底 · 白线网格 · 等宽字 · 橙色标注）',
  'clarity': 'Clarity 清透极简（雾白细线 · 大留白 · 静谧蓝）',
  'soft-utility': 'Soft Utility 柔和工具（深青主色 · 薄荷状态 · 清晰边界）',
  'aurora': 'Aurora 动态极光（柔光渐变 · 缓慢流动 · 光环描边）',
  'iris': 'IRIS 光圈（深空 HUD · 氙蓝酸绿 · 漂移网格）',
  'sketch-style': '铅笔手绘风（纸张纹理 · 虚线铅笔框 · 涂黑反白）',
};

function displayName(name) {
  return DISPLAY_NAMES[name] || name;
}

/** 生成单个主题变量块 */
function renderVarsBlock(themeName, mode, vars) {
  if (!vars || Object.keys(vars).length === 0) return '';
  const indent = '  ';
  const lines = Object.entries(vars).map(([k, v]) => `${indent}${k}: ${v};`);
  return `:root[data-theme='${mode}'][data-style='${themeName}'] {\n${lines.join('\n')}\n}`;
}

/** 生成完整 themes.css */
function buildThemesCss(json) {
  const out = [];

  // 头部注释
  out.push(`/* ============================================================`);
  out.push(`   风格主题包 —— 自动生成，勿手动编辑！`);
  out.push(`   数据源：frontend/themes.json`);
  out.push(`   生成器：scripts/build-themes.js`);
  out.push(`   ============================================================ */`);
  out.push('');

  // 装饰层容器基座规则（跨主题共享，不属于任何主题块）
  out.push(`/* 装饰层载体（三个页面均放置 <div class="style-fx">）：
   必须 fixed + z-index:-1 + pointer-events:none，
   否则主题伪元素装饰层会浮在内容之上拦截点击 */`);
  out.push(`.style-fx {
  position: fixed;
  inset: 0;
  z-index: -1;
  overflow: hidden;
  pointer-events: none;
}`);
  out.push(`body.has-wallpaper .style-fx {
  display: none;
}`);
  out.push('');

  // 全部主题（含 soft）都从 themes.json 渲染，JSON 是唯一事实源
  const themeNames = Object.keys(json).filter(k => k !== '_meta').sort();
  // soft 排最前（与原始 CSS 顺序一致）
  themeNames.sort((a, b) => (a === 'soft' ? -1 : b === 'soft' ? 1 : a.localeCompare(b)));

  let num = 0;
  const emittedKeyframes = new Set(); // @keyframes 全局生效，跨主题共享的只输出一份
  for (const name of themeNames) {
    const t = json[name];
    if (!t) continue;
    num++;

    out.push(`/* ============================================================`);
    out.push(`   ${num}) ${displayName(name)} — ${name}`);
    out.push(`   ============================================================ */`);

    // light 变量块
    if (t.light && Object.keys(t.light).length) {
      out.push(renderVarsBlock(name, 'light', t.light));
      out.push('');
    }
    // dark 变量块
    if (t.dark && Object.keys(t.dark).length) {
      out.push(renderVarsBlock(name, 'dark', t.dark));
      out.push('');
    }
    // extra_css（装饰规则 + keyframes，原样 append）
    if (t.extra_css && t.extra_css.length) {
      for (const block of t.extra_css) {
        const kfMatch = /^@keyframes\s+([\w-]+)/.exec(block.trim());
        if (kfMatch) {
          if (emittedKeyframes.has(kfMatch[1])) continue; // 已输出过，跳过
          emittedKeyframes.add(kfMatch[1]);
        }
        out.push(block.trim());
        out.push('');
      }
    }
    out.push('');
  }

  return out.join('\n');
}

/** 更新 api.js 的 SP_STYLES 数组 */
function updateSPStyles(content, styleNames) {
  const re = /const SP_STYLES = \[[^\]]+\];/s;
  const quoted = styleNames.map(n => `'${n}'`).join(', ');
  const replacement = `const SP_STYLES = [${quoted}];`;
  return content.replace(re, replacement);
}

/** 更新 admin.html 的 theme_style option 下拉 */
function updateAdminOptions(content, styleNames) {
  // 找到 theme_style 的 <select>，改写 <option> 列表
  const selectRe = /(<select[^>]*id="s_theme_style"[^>]*>)([\s\S]*?)(<\/select>)/;
  if (!selectRe.test(content)) {
    console.warn('[admin] 找不到 #s_theme_style select，跳过');
    return content;
  }
  const options = styleNames
    .map(n => `                <option value="${n}">${displayName(n)}</option>`)
    .join('\n');
  return content.replace(selectRe, (match, open, inner, close) => {
    return open + '\n' + options + '\n              ' + close;
  });
}

function main() {
  const t0 = Date.now();

  // 读 themes.json
  if (!fs.existsSync(THEMES_JSON)) {
    console.error(`❌ themes.json 不存在，请先运行: node scripts/extract-themes.js`);
    process.exit(1);
  }
  const json = JSON.parse(fs.readFileSync(THEMES_JSON, 'utf8'));

  // 全部主题名（soft + 18 套变量主题）
  const allStyles = ['soft', ...Object.keys(json).filter(k => k !== '_meta' && k !== 'soft').sort()];

  // 1. 生成 themes.css
  const css = buildThemesCss(json);
  if (DRY_RUN) {
    console.log(`[dry-run] themes.css: ${css.length} chars`);
  } else {
    fs.writeFileSync(THEMES_CSS, css, 'utf8');
    console.log(`[css] themes.css: ${css.length} chars 已生成`);
  }

  // 2. 更新 api.js SP_STYLES
  const apiContent = fs.readFileSync(API_JS, 'utf8');
  const newApiContent = updateSPStyles(apiContent, allStyles);
  if (newApiContent !== apiContent) {
    if (DRY_RUN) console.log(`[dry-run] api.js SP_STYLES: ${allStyles.length} items`);
    else {
      fs.writeFileSync(API_JS, newApiContent, 'utf8');
      console.log(`[js] api.js SP_STYLES: ${allStyles.length} items 已同步`);
    }
  } else {
    console.log(`[js] api.js SP_STYLES: 已是最新 (${allStyles.length} items)`);
  }

  // 3. 更新 admin.html option 下拉
  const adminContent = fs.readFileSync(ADMIN_HTML, 'utf8');
  const newAdminContent = updateAdminOptions(adminContent, allStyles);
  if (newAdminContent !== adminContent) {
    if (DRY_RUN) console.log(`[dry-run] admin.html option: ${allStyles.length} items`);
    else {
      fs.writeFileSync(ADMIN_HTML, newAdminContent, 'utf8');
      console.log(`[html] admin.html option: ${allStyles.length} items 已同步`);
    }
  } else {
    console.log(`[html] admin.html option: 已是最新 (${allStyles.length} items)`);
  }

  const dt = Date.now() - t0;
  console.log(`\n✅ 主题构建完成：${allStyles.length} 套主题，耗时 ${dt}ms`);
}

main();

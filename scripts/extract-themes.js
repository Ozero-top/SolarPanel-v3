#!/usr/bin/env node
/**
 * SolarPanel 主题提取器（一次性运行）
 * 从 themes.css 完整提取所有主题的 CSS 变量 + 装饰规则 + keyframes → themes.json
 *
 * 解析策略（替代旧版启发式正则，避免漏抓 [data-style='x'] .card 这类无前缀选择器）：
 * 1. 顶层规则用大括号深度分词器完整遍历
 * 2. 变量块：:root[data-theme='mode'][data-style='name'] { --var: ... }
 * 3. 装饰块：任何选择器含 [data-style='name'] 的非变量规则 → extra_css[name]
 * 4. @media 块：内部规则按 data-style 归属，保留 @media 包裹
 * 5. @keyframes：按各主题 extra_css/变量值中的 animation 引用名归属
 *
 * 用法：node scripts/extract-themes.js
 * 输出：frontend/themes.json
 */

const fs = require('fs');
const path = require('path');

const ROOT = path.resolve(__dirname, '..');
const THEMES_CSS = path.join(ROOT, 'frontend/assets/css/themes.css');
const OUT_JSON = path.join(ROOT, 'frontend/themes.json');

const raw = fs.readFileSync(THEMES_CSS, 'utf8');

/**
 * 把 CSS 源码切成顶层块列表：{ selector, body, isAtRule }
 * 用大括号深度跟踪，正确处理嵌套（@media/@keyframes）
 */
function parseTopLevel(src) {
  const blocks = [];
  let i = 0;
  while (i < src.length) {
    // 跳过空白与注释
    while (i < src.length && /[\s]/.test(src[i])) i++;
    if (i >= src.length) break;
    if (src[i] === '/' && src[i + 1] === '*') {
      const end = src.indexOf('*/', i + 2);
      i = end === -1 ? src.length : end + 2;
      continue;
    }
    // 找到本块的 {
    const braceStart = src.indexOf('{', i);
    if (braceStart === -1) break;
    const selector = src.slice(i, braceStart).trim();
    // 深度跟踪找块尾
    let depth = 1;
    let j = braceStart + 1;
    while (j < src.length && depth > 0) {
      const c = src[j];
      if (c === '/' && src[j + 1] === '*') {
        const end = src.indexOf('*/', j + 2);
        j = end === -1 ? src.length : end + 2;
        continue;
      }
      if (c === '{') depth++;
      else if (c === '}') depth--;
      j++;
    }
    const body = src.slice(braceStart + 1, j - 1);
    blocks.push({ selector, body, full: src.slice(i, j).trim(), isAtRule: selector.startsWith('@') });
    i = j;
  }
  return blocks;
}

/** 从变量块 body 提取 --var: value */
function extractVars(body) {
  const vars = {};
  const re = /(--[\w-]+)\s*:\s*([^;]+);/g;
  let m;
  while ((m = re.exec(body)) !== null) {
    vars[m[1]] = m[2].trim();
  }
  return vars;
}

/** 选择器里出现的 data-style 名（可能多个逗号选择器） */
function stylesInSelector(sel) {
  const set = new Set();
  const re = /\[data-style='([^']+)'\]/g;
  let m;
  while ((m = re.exec(sel)) !== null) set.add(m[1]);
  return Array.from(set);
}

/** 去掉 CSS 注释（判断纯变量块前必须去注释，否则注释+声明混合段误判） */
function stripComments(s) {
  return s.replace(/\/\*[\s\S]*?\*\//g, '');
}

/** 是否纯变量块（body 里全部声明以 -- 开头） */
function isPureVarsBlock(body) {
  const decls = stripComments(body).split(';').map(s => s.trim()).filter(Boolean);
  if (!decls.length) return false;
  return decls.every(d => /^\s*--[\w-]+\s*:/.test(d));
}

/** 拆分 @media 头与内部规则 */
function parseMediaInner(body) {
  // 内部规则同样是 selector { ... }，用简化分词（内部不会再嵌套 at-rule 含大括号的情况少见，但防御处理）
  return parseTopLevel(body);
}

function main() {
  const themes = {}; // name → { light, dark, extra_css:Set(用数组+去重) }
  const ensure = name => {
    if (!themes[name]) themes[name] = { light: null, dark: null, extra_css: [] };
    return themes[name];
  };

  const blocks = parseTopLevel(raw);
  const keyframes = []; // { name, full }
  const declaredThemes = new Set();

  for (const b of blocks) {
    if (b.isAtRule) {
      if (b.selector.startsWith('@keyframes')) {
        const name = b.selector.replace(/@keyframes\s+/, '').trim();
        keyframes.push({ name, full: b.full });
      } else if (b.selector.startsWith('@media')) {
        // 内部规则按 data-style 归属，保留 @media 包裹
        const inners = parseMediaInner(b.body);
        for (const inner of inners) {
          const names = stylesInSelector(inner.selector);
          for (const n of names) {
            declaredThemes.add(n);
            ensure(n).extra_css.push(`${b.selector} {\n${inner.full}\n}`);
          }
        }
      }
      continue;
    }

    // 普通规则
    const varMatch = /^:root\[data-theme='(light|dark)'\]\[data-style='([^']+)'\]$/.exec(b.selector);
    if (varMatch && isPureVarsBlock(b.body)) {
      const t = ensure(varMatch[2]);
      declaredThemes.add(varMatch[2]);
      t[varMatch[1]] = extractVars(b.body);
      continue;
    }

    // 任何含 [data-style='x'] 的规则 → extra_css
    const names = stylesInSelector(b.selector);
    if (names.length) {
      for (const n of names) {
        declaredThemes.add(n);
        ensure(n).extra_css.push(b.full);
      }
    }
  }

  // keyframes 归属：主题 extra_css / 变量值中引用了 animation: NAME 则拉入
  for (const [name, t] of Object.entries(themes)) {
    const refText = (t.extra_css.join('\n') + ' ' + JSON.stringify(t.light || {}) + JSON.stringify(t.dark || {}));
    for (const kf of keyframes) {
      const re = new RegExp(`animation(?:-name)?\\s*:[^;}\\n]*\\b${kf.name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\b`);
      if (re.test(refText)) {
        t.extra_css.push(kf.full);
      }
    }
  }

  // 去重（同一规则可能被 light+dark 选择器各匹配一次）
  for (const t of Object.values(themes)) {
    const seen = new Set();
    t.extra_css = t.extra_css.filter(b => {
      const k = b.replace(/\s+/g, ' ').trim();
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
  }

  // soft 特殊：:root:not([data-style]) 规则归属 soft
  if (themes.soft) {
    const notStyle = /:root:not\(\[data-style\]\)[^{]*\{[^}]*\}/g;
    let m;
    while ((m = notStyle.exec(raw)) !== null) {
      themes.soft.extra_css.push(m[0].trim());
    }
  }

  // sanity check：原始 CSS 中所有含 data-style 的顶层规则块数 vs 提取到的 extra 总数
  const origRuleCount = (raw.match(/\[data-style='[^']+'\]/g) || []).length;

  const output = {
    _meta: {
      generated_from: 'themes.css',
      total_themes: Object.keys(themes).length,
      themes: Object.keys(themes).sort(),
      note: 'display 字段可手动改中文名；vars/extra_css 自动提取'
    }
  };
  for (const [name, t] of Object.entries(themes)) {
    output[name] = { display: name, light: t.light, dark: t.dark, extra_css: t.extra_css };
  }

  fs.writeFileSync(OUT_JSON, JSON.stringify(output, null, 2), 'utf8');

  const names = Object.keys(themes).sort();
  console.log(`✅ 提取完成：${names.length} 套主题 → ${OUT_JSON}`);
  for (const n of names) {
    const t = themes[n];
    console.log(`  ${n.padEnd(16)} light:${t.light ? Object.keys(t.light).length : 0}  dark:${t.dark ? Object.keys(t.dark).length : 0}  extra:${t.extra_css.length}`);
  }
  console.log(`\n原始 data-style 选择器出现次数: ${origRuleCount}（校验参考）`);
}

main();

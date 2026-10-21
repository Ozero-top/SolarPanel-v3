#!/usr/bin/env node
/**
 * SolarPanel 前端构建脚本 —— 零依赖资源戳注入
 *
 * 扫描 frontend/ 下所有 HTML 引用的静态资源，用内容 SHA1 短 hash
 * 改写 ?v= 版本号，彻底消除 18 处手动同步痛点（硬约束 3.1）。
 *
 * 零 Node 依赖：只用 fs / crypto / path 内置模块。
 * 可直接 node scripts/build-frontend.js 运行。
 */

const fs = require('fs');
const crypto = require('crypto');
const path = require('path');

const ROOT = path.resolve(__dirname, '..');
const FE = path.join(ROOT, 'frontend');

// HTML 文件：含资源戳需改写
const HTML_FILES = ['index.html', 'admin.html', 'login.html'].map(n => path.join(FE, n));

// 资源戳匹配：src="xxx.js?v=OLD" / href="xxx.css?v=OLD" 等
// 捕获组 1=引号后路径  2=文件扩展（小写）
const ASSET_RE = /(?:href|src)="([^"?#]+?)\?v=[^"]+/g;

// sw.js CACHE_NAME 匹配
const CACHE_NAME_RE = /const\s+CACHE_NAME\s*=\s*['"]([^'"]+)['"]/;
const RUNTIME_CACHE_RE = /const\s+RUNTIME_CACHE\s*=\s*['"]([^'"]+)['"]/;

/** 算文件内容 SHA1 前 8 位 */
function fileHash(fp) {
  const buf = fs.readFileSync(fp);
  return crypto.createHash('sha1').update(buf).digest('hex').slice(0, 8);
}

/** HTML 里所有 asset 路径 → 相对 frontend/ → 绝对路径 → 算 hash */
function collectAssetHashes(htmlContent) {
  const hashes = {};
  let m;
  ASSET_RE.lastIndex = 0;
  while ((m = ASSET_RE.exec(htmlContent)) !== null) {
    const rel = m[1];
    if (hashes[rel]) continue;
    const abs = path.join(FE, rel);
    if (fs.existsSync(abs)) {
      hashes[rel] = fileHash(abs);
    } else {
      console.warn(`[hash] 找不到文件，跳过: ${rel}`);
    }
  }
  return hashes;
}

/** 把 HTML 里所有 ?v=OLD 替换为 ?v=NEW */
function rewriteHtml(src, hashes) {
  return src.replace(ASSET_RE, (match, rel) => {
    const h = hashes[rel];
    if (!h) return match;
    // 保持引号风格（双引号）
    return match.replace(/\?v=[^"]+/, `?v=${h}`);
  });
}

/** sw.js CACHE_NAME 自增：sp-cache-v21 → sp-cache-v22 */
function bumpCacheName(content) {
  return content
    .replace(CACHE_NAME_RE, (m, old) => {
      const n = parseInt((old.match(/v(\d+)$/) || [,'0'])[1], 10) + 1;
      const name = old.replace(/v\d+$/, `v${n}`);
      console.log(`[sw] CACHE_NAME: ${old} → ${name}`);
      return m.replace(old, name);
    })
    .replace(RUNTIME_CACHE_RE, (m, old) => {
      const n = parseInt((old.match(/v(\d+)$/) || [,'0'])[1], 10) + 1;
      const name = old.replace(/v\d+$/, `v${n}`);
      console.log(`[sw] RUNTIME_CACHE: ${old} → ${name}`);
      return m.replace(old, name);
    });
}

function main() {
  const t0 = Date.now();
  let totalRewritten = 0;

  // 1. HTML 资源戳
  for (const fp of HTML_FILES) {
    const rel = path.basename(fp);
    const raw = fs.readFileSync(fp, 'utf8');
    const hashes = collectAssetHashes(raw);
    const out = rewriteHtml(raw, hashes);
    if (out !== raw) {
      fs.writeFileSync(fp, out, 'utf8');
      const count = Object.keys(hashes).length;
      console.log(`[html] ${rel}: ${count} 处 ?v= 已注入内容 hash`);
      totalRewritten += count;
    } else {
      console.log(`[html] ${rel}: 无变化`);
    }
  }

  // 2. sw.js 版本号自增（每次 build 都 bump，确保旧缓存被淘汰）
  const swJs = path.join(FE, 'sw.js');
  if (fs.existsSync(swJs)) {
    const raw = fs.readFileSync(swJs, 'utf8');
    const out = bumpCacheName(raw);
    if (out !== raw) fs.writeFileSync(swJs, out, 'utf8');
  }

  const dt = Date.now() - t0;
  console.log(`\n✅ 构建完成：${totalRewritten} 处资源戳注入，耗时 ${dt}ms`);
}

main();

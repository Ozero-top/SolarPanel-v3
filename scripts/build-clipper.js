#!/usr/bin/env node
/**
 * 打包桌面剪藏扩展：固定白名单 + 顶层目录 solarpanel-clipper/ + SHA256SUMS.txt
 * 产物：dist/solarpanel-clipper-vX.Y.Z.zip、dist/SHA256SUMS.txt
 */
'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { execFileSync } = require('child_process');

const ROOT = path.resolve(__dirname, '..');
const SRC = path.join(ROOT, 'clipper-extension');
const DIST = path.join(ROOT, 'dist');
const STAGE_ROOT = path.join(DIST, 'clipper');
const STAGE = path.join(STAGE_ROOT, 'solarpanel-clipper');

const FILES = [
  'manifest.json',
  'background.js',
  'popup.html',
  'popup.js',
  'popup.css',
  'options.html',
  'options.js',
  'README.md',
];

function fail(msg) {
  console.error('[build-clipper] ' + msg);
  process.exit(1);
}

function copyFile(rel) {
  const from = path.join(SRC, rel);
  const to = path.join(STAGE, rel);
  if (!fs.existsSync(from)) fail('缺少文件：' + rel);
  fs.mkdirSync(path.dirname(to), { recursive: true });
  fs.copyFileSync(from, to);
}

function main() {
  const manifest = JSON.parse(fs.readFileSync(path.join(SRC, 'manifest.json'), 'utf8'));
  const version = manifest.version;
  if (!version) fail('manifest.json 缺少 version');

  fs.rmSync(STAGE_ROOT, { recursive: true, force: true });
  fs.mkdirSync(STAGE, { recursive: true });

  FILES.forEach(copyFile);
  const iconsDir = path.join(SRC, 'icons');
  if (!fs.existsSync(iconsDir)) fail('缺少 icons 目录');
  for (const name of fs.readdirSync(iconsDir)) {
    if (!name.endsWith('.png')) continue;
    fs.mkdirSync(path.join(STAGE, 'icons'), { recursive: true });
    fs.copyFileSync(path.join(iconsDir, name), path.join(STAGE, 'icons', name));
  }

  const zipName = `solarpanel-clipper-v${version}.zip`;
  const zipPath = path.join(DIST, zipName);
  fs.rmSync(zipPath, { force: true });

  // 使用 Windows 自带 Compress-Archive，避免引入第三方打包依赖
  const cmd = `Compress-Archive -Path '${STAGE}' -DestinationPath '${zipPath}' -CompressionLevel Optimal -Force`;
  execFileSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', cmd], { stdio: 'inherit' });

  if (!fs.existsSync(zipPath)) fail('打包失败，未生成 zip');

  const hash = crypto.createHash('sha256').update(fs.readFileSync(zipPath)).digest('hex');
  const sumsPath = path.join(DIST, 'SHA256SUMS.txt');
  let prev = '';
  if (fs.existsSync(sumsPath)) {
    prev = fs.readFileSync(sumsPath, 'utf8')
      .split(/\r?\n/)
      .filter((line) => line && !line.endsWith(zipName))
      .join('\n');
    if (prev) prev += '\n';
  }
  fs.writeFileSync(sumsPath, prev + `${hash}  ${zipName}\n`, 'utf8');

  console.log('[build-clipper] 完成');
  console.log('  zip   : ' + zipPath);
  console.log('  sha256: ' + hash);
}

main();

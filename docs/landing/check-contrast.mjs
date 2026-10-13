#!/usr/bin/env node
// 落地页配色的对比度自检。
//
// 用法：node docs/landing/check-contrast.mjs
//
// 为什么有这个东西：落地页的令牌是「裸 HSL 三元组」，明度一改就可能悄悄跌破
// 4.5:1，而肉眼看不出来。这个脚本直接从 docs/pages/index.html 里读回令牌，
// 按 WCAG 公式实算每一组「文字 / 其所在表面」的配对比，改完配色跑一次即可。
//
// 为什么放在 docs/landing/ 而不是 docs/pages/：pages.yml 把整个 docs/pages/
// 目录原样发布到站点根，任何放在那里的文件都会变成公开可访问的静态资源。
// 这个脚本和参考笔记属于工程材料，不该出现在公开站点上。
//
// 退出码：0 = 全部通过；1 = 有配对不达标（可用于 CI 或 pre-commit）。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const HERE = dirname(fileURLToPath(import.meta.url));
const PAGES = join(HERE, "..", "pages"); // 落地页所在的、被 pages.yml 发布的目录
const MIN = 4.5; // WCAG 2.x AA，正文

// ---------- 颜色换算 ----------
function hslToHex(h, s, l) {
  h = ((h % 360) + 360) % 360; s /= 100; l /= 100;
  const c = (1 - Math.abs(2 * l - 1)) * s;
  const hp = h / 60;
  const x = c * (1 - Math.abs((hp % 2) - 1));
  let r = 0, g = 0, b = 0;
  if (hp < 1) { r = c; g = x; }
  else if (hp < 2) { r = x; g = c; }
  else if (hp < 3) { g = c; b = x; }
  else if (hp < 4) { g = x; b = c; }
  else if (hp < 5) { r = x; b = c; }
  else { r = c; b = x; }
  const m = l - c / 2;
  const to = v => Math.max(0, Math.min(255, Math.round((v + m) * 255))).toString(16).padStart(2, "0");
  return "#" + to(r) + to(g) + to(b);
}

function relativeLuminance(hex) {
  const h = hex.replace("#", "");
  const ch = c => { c /= 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
  return 0.2126 * ch(parseInt(h.slice(0, 2), 16))
       + 0.7152 * ch(parseInt(h.slice(2, 4), 16))
       + 0.0722 * ch(parseInt(h.slice(4, 6), 16));
}

function contrast(a, b) {
  const l1 = relativeLuminance(a), l2 = relativeLuminance(b);
  return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
}

// ---------- 从 index.html 读回令牌 ----------
const html = readFileSync(join(PAGES, "index.html"), "utf8");
const css = html.match(/<style>([\s\S]*?)<\/style>/)[1].replace(/\/\*[\s\S]*?\*\//g, "");

const blocks = [...css.matchAll(/:root\s*\{([^}]*)\}/g)].map(m => {
  const o = {};
  for (const x of m[1].matchAll(/(--c-[a-z-]+)\s*:\s*([^;]+);/g)) o[x[1]] = x[2].trim();
  return o;
});

if (blocks.length !== 2) {
  console.error(`预期两个调色板块（浅色默认 + 深色覆盖），实际 ${blocks.length} 个`);
  process.exit(1);
}

// 两个板块必须覆盖同一组键，否则某个值会从另一种模式泄漏进来
const keySets = blocks.map(b => Object.keys(b).sort().join("|"));
if (keySets[0] !== keySets[1]) {
  const [a, b] = blocks.map(x => new Set(Object.keys(x)));
  console.error("两个调色板块的键集不一致：");
  console.error("  仅浅色有:", [...a].filter(k => !b.has(k)).join(", ") || "无");
  console.error("  仅深色有:", [...b].filter(k => !a.has(k)).join(", ") || "无");
  process.exit(1);
}

const toHex = t => {
  const p = t.trim().split(/\s+/);
  if (p.length !== 3) throw new Error(`不是裸 HSL 三元组: "${t}"`);
  return hslToHex(parseFloat(p[0]), parseFloat(p[1]), parseFloat(p[2]));
};

// ---------- 断言每处「文字 / 其所在表面」 ----------
// 表面说明：band 用 card 底、quiet band 用 muted 底，两者都要覆盖，
// 因为强调色和次要文字都可能落在其中任意一个上。
const PAIRS = [
  ["正文 / 页面底",            "--c-fg",       "--c-bg"],
  ["正文 / 卡片底",            "--c-fg",       "--c-card"],
  ["正文 / 次级区块底",        "--c-fg",       "--c-muted"],
  ["次要文字 / 页面底",        "--c-fg-muted", "--c-bg"],
  ["次要文字 / 卡片底",        "--c-fg-muted", "--c-card"],
  ["次要文字 / 次级区块底",    "--c-fg-muted", "--c-muted"],
  ["强调色文字 / 页面底",      "--c-accent",   "--c-bg"],
  ["强调色文字 / 卡片底",      "--c-accent",   "--c-card"],
  ["强调色文字 / 次级区块底",  "--c-accent",   "--c-muted"],
  ["按钮标签 / 强调色底",      "--c-on-accent","--c-accent"],
];

const MODES = ["浅色", "深色"];
let failures = 0;

for (const [i, palette] of blocks.entries()) {
  const hex = {};
  for (const k in palette) hex[k] = toHex(palette[k]);
  console.log(`\n=== ${MODES[i]} ===`);
  console.log("  " + Object.keys(hex).map(k => `${k.replace("--c-", "")}=${hex[k]}`).join("  "));

  for (const [label, fgKey, bgKey] of PAIRS) {
    const r = contrast(hex[fgKey], hex[bgKey]);
    const ok = r >= MIN;
    if (!ok) failures++;
    console.log(`  ${label.padEnd(26)} ${r.toFixed(2).padStart(6)}  ${ok ? "PASS" : "** FAIL **"}`);
  }

  // 信息性：描边不承担可识别性职责，不作为门槛（理由见 index.html 内注释）
  console.log(`  (参考) 描边 / 页面底 ${contrast(hex["--c-border"], hex["--c-bg"]).toFixed(2)}`);
  console.log(`  (参考) 描边 / 卡片底 ${contrast(hex["--c-border"], hex["--c-card"]).toFixed(2)}`);
}

console.log("\n" + "=".repeat(58));
if (failures === 0) {
  console.log(`通过：两套配色共 ${PAIRS.length * 2} 组文字配对全部 >= ${MIN}:1`);
  process.exit(0);
}
console.error(`不通过：${failures} 组配对低于 ${MIN}:1`);
process.exit(1);

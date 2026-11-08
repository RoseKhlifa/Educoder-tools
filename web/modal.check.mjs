import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { renderUserGuidelines } from './dist/guidelines.js';

const guidelines = renderUserGuidelines();
const protocolSection = guidelines.match(/<section([^>]*)aria-labelledby="guidelines-protocol"[^>]*>([\s\S]*?)<\/section>/);
assert.ok(protocolSection, 'guidelines must retain a clear service explanation');
assert.doesNotMatch(protocolSection[1], /guidelines-highlight/, 'service method should be ordinary copy, not a highlighted promotion');
assert.match(protocolSection[2], /服务方式与使用提醒/);
assert.match(protocolSection[2], /接口请求同步课程、题目和任务状态/);
assert.doesNotMatch(protocolSection[2], /<strong>|协议请求实现，请合理使用|不承诺账号不受限制/);
assert.match(protocolSection[2], /独立服务/);
assert.match(protocolSection[2], /自行评估相关风险/);
assert.match(guidelines, /<section class="guidelines-highlight" aria-labelledby="guidelines-data">/);
assert.match(guidelines, /平台会收集用户做题数据/);
const dataSection = guidelines.match(/<section class="guidelines-highlight" aria-labelledby="guidelines-data">([\s\S]*?)<\/section>/);
assert.ok(dataSection?.[1].includes('<p><strong>具有用户个人独特特征的图文作业及 ZIP 压缩包，本站不会收集，也不会纳入题库或用于答案复用。</strong></p>'), 'the highlighted data notice must clearly exclude personalized homework and ZIP materials');
assert.ok(dataSection[1].indexOf('本站不会收集') < dataSection[1].indexOf('请在了解并同意上述用途后登录使用'), 'the collection boundary must appear before consent copy');

const css = (await readFile(new URL('./styles.css', import.meta.url), 'utf8')).replace(/\/\*[\s\S]*?\*\//g, '');
function lastRule(selector) {
  const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .filter(([, candidate]) => candidate.trim() === selector);
  assert.ok(rules.length, `missing rule ${selector}`);
  return rules.at(-1)[2];
}
// Parent clips the dialog while its child scrolls: the close button is not a descendant of the scroll area.
const modalRules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
  .filter(([, selector]) => selector.trim() === '.modal').map(([, , body]) => body).join(';');
for (const declaration of ['display:flex', 'flex-direction:column', 'overflow:hidden', 'padding-top:56px']) {
  assert.ok(modalRules.replace(/\s/g, '').includes(declaration), `missing fixed dialog chrome ${declaration}`);
}
assert.match(lastRule('.modal'), /100dvh/);
const content = lastRule('.modal > .modal-content');
assert.match(content, /padding:0 18px 24px/, 'mobile content must not overlap the reserved close strip');
assert.match(css, /\.modal > \.modal-content\s*\{[^}]*min-height:0;[^}]*overflow-y:auto;[^}]*overscroll-behavior:contain;/);
assert.match(lastRule('.modal > .modal-close'), /min-height:44px/, 'close control keeps a touch-friendly target');
const app = await readFile(new URL('./src/app.ts', import.meta.url), 'utf8');
assert.match(app, /class="icon-button modal-close"[^>]*aria-label="关闭"[\s\S]*?<\/button><div class="modal-content">/);
assert.match(app, /event\.key === 'Escape' && state\.modal\) closeModal\(\)/);
console.log('modal layout and guidelines checks: PASS');

assert.doesNotMatch(app, /state-legend|状态图例/);

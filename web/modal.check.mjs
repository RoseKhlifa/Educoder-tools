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
assert.match(guidelines, /平台会收集做题数据/);
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
// The panel clips and only its content scrolls, so the close control and the
// action footer cannot be scrolled out of reach on a long record.
const modalRules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
  .filter(([, selector]) => selector.trim() === '.modal').map(([, , body]) => body).join(';');
for (const declaration of ['display:flex', 'flex-direction:column', 'overflow:hidden']) {
  assert.ok(modalRules.replace(/\s/g, '').includes(declaration), `missing fixed dialog chrome ${declaration}`);
}
// The sheet is bounded by the visual viewport. Wide screens scale the page
// with zoom, which does not scale viewport units, so the bound is expressed in
// the same zoomed coordinate space as the rest of the layout.
assert.match(modalRules, /var\(--screen-dh\)/, 'a phone sheet must be bounded by the visual viewport');
assert.match(css, /--screen-dh:\s*calc\(100dvh\s*\/\s*var\(--zoom\)\)/, 'the viewport bound must track the page zoom');
const content = lastRule('.modal > .modal-content');
assert.match(content, /min-height:\s*0/);
assert.match(content, /overflow-y:\s*auto/);
assert.match(content, /overscroll-behavior:\s*contain/);
// The close control is a sibling of the scroll area, not a descendant of it.
const close = lastRule('.modal > .modal-close');
assert.match(close, /position:\s*absolute/);
assert.match(close, /min-height:\s*44px/, 'close control keeps a touch-friendly target');
const footRules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
  .filter(([, selector]) => selector.trim() === '.sheet-foot').map(([, , body]) => body).join(';');
assert.match(footRules, /position:\s*sticky/, 'sheet actions stay reachable while the record scrolls');
const app = await readFile(new URL('./src/app.ts', import.meta.url), 'utf8');
assert.match(app, /class="btn ghost square modal-close"[^>]*aria-label="关闭"[\s\S]*?<\/button><div class="modal-content">/);
assert.match(app, /event\.key === 'Escape' && state\.modal\) closeModal\(\)/);
console.log('modal layout and guidelines checks: PASS');

assert.doesNotMatch(app, /state-legend|状态图例/);

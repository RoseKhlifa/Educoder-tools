import { readFile } from 'node:fs/promises';
import assert from 'node:assert/strict';
const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const source = await readFile(new URL('./src/app.ts', import.meta.url), 'utf8');
const api = await readFile(new URL('./src/api.ts', import.meta.url), 'utf8');
const jobs = await readFile(new URL('./src/jobs.ts', import.meta.url), 'utf8');
const css = await readFile(new URL('./styles.css', import.meta.url), 'utf8');
assert.match(html, /id="app"/);
assert.match(html, /dist\/app\.js/);
assert.match(html, /rel="icon" type="image\/png" href="\/assets\/brand-logo\.png/);
const logo = await readFile(new URL('./assets/brand-logo.png', import.meta.url));
assert.equal(logo.subarray(0, 8).toString('hex'), '89504e470d0a1a0a', 'brand asset must be a genuine PNG');
assert.equal(logo[24], 8, 'brand asset should use standard 8-bit channels');
assert.ok([4, 6].includes(logo[25]), 'brand asset must carry an alpha channel, not a baked-in white background');
assert.equal(logo[28], 0, 'brand asset should use non-interlaced PNG encoding');
for (const [, selector, body] of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
  if (!/\.brand-logo(?![\w-])/.test(selector)) continue;
  for (const [, background] of body.matchAll(/(?:^|;)\s*background(?:-color)?\s*:\s*([^;}]+)/g)) {
    assert.match(background.trim(), /^(?:transparent|none)(?:\s*!important)?$/i, 'brand-logo CSS must not paint an opaque background');
  }
}

for (const route of ['/api/auth/login', '/api/auth/sms/send', '/api/auth/sms/login', '/api/auth/wechat/start', '/api/auth/wechat/poll', '/api/auth/wechat/cancel', '/api/auth/me', '/api/courses', '/api/jobs', '/api/admin/overview', '/api/admin/users', '/api/admin/jobs', '/api/admin/audit']) assert.ok(api.includes(route), `missing route ${route}`);
assert.doesNotMatch(`${source}\n${api}`, /eval\s*\(|qrconnect|open\.weixin\.qq\.com|wx_code|signedState/, 'browser must not execute or manage the upstream WeChat protocol');
assert.match(api, /credentials:\s*'include'/);
assert.doesNotMatch(api, /access_code|access_card|cardCode|authConfig|adminCards|adminCreateCard|adminRevokeCard|\/api\/admin\/cards|\/api\/auth\/config/, 'retired access-card API must not remain in the browser bundle');
const admin = await readFile(new URL('./src/admin.ts', import.meta.url), 'utf8');
const types = await readFile(new URL('./src/types.ts', import.meta.url), 'utf8');
assert.doesNotMatch(admin, /AccessCard|卡密|create-card|revoke-card|page === 'cards'/, 'administrator view must not retain access-card management');
assert.doesNotMatch(types, /AccessCard/, 'retired access-card DTO must not remain');
assert.doesNotMatch(`${api}\n${source}\n${jobs}\n${admin}\n${types}`, /recoverJob|\/recover|data-(?:job|admin)-recover|can_recover|复核/, 'retired read-only recheck must not remain in the browser bundle');

assert.equal(/localStorage|sessionStorage|console\.(log|debug|info)\s*\(/.test(`${source}\n${api}`), false, 'sensitive browser persistence/logging found');
assert.equal(/error_code|job\.message|model_calls|cost_micro_usd|diagnostics|attempts|budget/.test(jobs), false, 'consumer task renderer contains technical details');
assert.match(css, /@media \(max-width: 680px\)/);
assert.match(css, /\.topbar/);
// Phone layout contract. A column flex container reads align-items on the
// inline axis, so the stacked shell must be told to stretch; inherited from
// the desktop grid it was `start`, which sized the main column and the sidebar
// to their own text and made the task cards narrower than the page.
const phoneCSS = css.slice(css.indexOf('@media (max-width: 680px)'));
assert.match(phoneCSS, /\.shell \{[^}]*align-items: stretch/, 'the stacked phone shell must stretch its columns to the page');
assert.match(phoneCSS, /\.consumer-app \.shell:not\(\[data-page="courses"\]\) \.side \{ display: none; \}/, 'summary cards belong to the course list on a phone, not to every tab');
assert.match(phoneCSS, /\.field input, \.field textarea, \.field select, \.search input \{ font-size: 16px; \}/, 'phone fields keep 16px type so iOS and WeChat do not zoom on focus');
assert.match(phoneCSS, /body\.modal-open \{ overflow: hidden; \}/, 'a phone sheet locks the page behind it');
assert.match(phoneCSS, /\.ledger-table thead \{ display: none; \}/, 'operator tables stack as records on a phone instead of scrolling sideways');
assert.match(phoneCSS, /\.table-wrap \{ overflow-x: visible; \}/, 'nothing scrolls sideways on a phone');
assert.match(phoneCSS, /\.pick \{ width: 44px; height: 44px;/, 'the course tick box keeps a 44px touch target');
assert.match(css, /@media \(hover: none\)/, 'touch screens must not keep a hover state after a tap');
// The shell must not fetch anything from a third party: users are in mainland
// China and the fonts are vendored. Inline data: URIs and same-origin paths are
// the only permitted sources, and the stylesheet must not serialize an extra
// round trip with @import. Inline SVG namespaces are declarations, not fetches,
// so data: URIs are removed before looking for an external origin.
const shell = `${html}\n${css}`.replace(/url\(\s*"data:[^"]*"\s*\)|url\(\s*'data:[^']*'\s*\)/g, 'url(data:)');
assert.equal(/https?:\/\//.test(shell), false, 'shell should not reference external origins');
assert.equal(/@import/.test(css), false, 'stylesheet should not @import; link stylesheets from index.html');
for (const [, quoted, single, bare] of css.matchAll(/url\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s]*))\s*\)/g)) {
  const source = quoted ?? single ?? bare ?? '';
  assert.match(source, /^data:/, `stylesheet url() must be an inline data URI, found ${source}`);
}
// 霞鹜文楷 is self-hosted and sliced by unicode-range, so a browser downloads
// only the subsets a page actually needs.
assert.match(html, /<link rel="stylesheet" href="\/fonts\/lxgw-wenkai\/lxgwwenkai-regular\.css"/);
assert.match(html, /<link rel="stylesheet" href="\/fonts\/lxgw-wenkai\/lxgwwenkai-bold\.css"/);
assert.match(css, /--font:\s*"LXGW WenKai",\s*"霞鹜文楷",\s*"KaiTi",\s*"STKaiti",\s*"PingFang SC",\s*"Microsoft YaHei",\s*serif/);
for (const weight of ['regular', 'bold']) {
  const face = await readFile(new URL(`./fonts/lxgw-wenkai/lxgwwenkai-${weight}.css`, import.meta.url), 'utf8');
  assert.match(face, /font-family:\s*'LXGW WenKai'/);
  assert.match(face, /unicode-range:/, 'vendored font CSS must keep its unicode-range slicing');
  assert.equal(/https?:\/\/|@import/.test(face), false, 'vendored font CSS must not reach a CDN');
  const slices = [...face.matchAll(/url\('\.\/(files\/[^']+\.woff2)'\)/g)];
  assert.ok(slices.length > 20, `expected many ${weight} subsets, found ${slices.length}`);
  const first = await readFile(new URL(`./fonts/lxgw-wenkai/${slices[0][1]}`, import.meta.url));
  assert.equal(first.subarray(0, 4).toString('latin1'), 'wOF2', 'vendored subset must be a real woff2');
}
const ofl = await readFile(new URL('./fonts/lxgw-wenkai/OFL.txt', import.meta.url), 'utf8');
assert.match(ofl, /SIL OPEN FONT LICENSE/i, 'the vendored font must ship its licence');
console.log('web service static checks: PASS');
await import('./api.check.mjs');
await import('./app.check.mjs');
await import('./admin.check.mjs');
await import('./jobs.check.mjs');

await import('./course-cache.check.mjs');
await import('./course-status.check.mjs');
await import('./tickets.check.mjs');

await import('./modal.check.mjs');

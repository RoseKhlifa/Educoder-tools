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

for (const route of ['/api/auth/login', '/api/auth/me', '/api/auth/config', '/api/courses', '/api/jobs', '/api/admin/overview', '/api/admin/users', '/api/admin/jobs', '/api/admin/cards', '/api/admin/audit']) assert.ok(api.includes(route), `missing route ${route}`);
assert.match(api, /credentials:\s*'include'/);
assert.equal(/localStorage|sessionStorage|console\.(log|debug|info)\s*\(/.test(`${source}\n${api}`), false, 'sensitive browser persistence/logging found');
assert.equal(/error_code|\.message|model_calls|cost_micro_usd|diagnostics|attempts|budget/.test(jobs), false, 'consumer task renderer contains technical details');
assert.match(css, /@media \(max-width: 620px\)/);
assert.match(css, /\.topbar/);
assert.equal(/https?:\/\/|@import|url\(/.test(`${html}\n${css}`), false, 'shell should not download external fonts or media');
console.log('web service static checks: PASS');
await import('./api.check.mjs');
await import('./app.check.mjs');
await import('./admin.check.mjs');
await import('./jobs.check.mjs');

import { readFile } from 'node:fs/promises';
import assert from 'node:assert/strict';

const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const source = await readFile(new URL('./src/app.ts', import.meta.url), 'utf8');
const api = await readFile(new URL('./src/api.ts', import.meta.url), 'utf8');
const css = await readFile(new URL('./styles.css', import.meta.url), 'utf8');

assert.match(html, /id="app"/);
assert.match(html, /dist\/app\.js/);
for (const route of ['/api/auth/login', '/api/auth/me', '/api/courses', '/homeworks/', '/enter']) assert.ok(api.includes(route), `missing route ${route}`);
assert.match(api, /credentials:\s*'include'/);
assert.equal(/localStorage|sessionStorage|console\.(log|debug|info)\s*\(/.test(`${source}\n${api}`), false, 'sensitive browser persistence/logging found');
for (const marker of ['renderLogin', 'renderCourseRail', 'renderHomeworkRow', 'renderTaskDrawer', 'loading', 'drawerOpen']) assert.ok(source.includes(marker), `missing UI state ${marker}`);
assert.match(css, /@media \(max-width: 620px\)/);
assert.match(css, /\.topbar/);
assert.equal(/https?:\/\/|@import|url\(/.test(`${html}\n${css}`), false, 'workbench shell should not download external fonts or media');
console.log('web static checks: PASS');
await import('./api.check.mjs');
await import('./app.check.mjs');

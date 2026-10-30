import assert from 'node:assert/strict';
import { test } from 'node:test';

// A small event/element harness runs the compiled app and its actual fetch calls.
// Browser layout is checked separately; this harness exercises async UI state.
class Element {
  constructor(tag, attributes, document) {
    this.tag = tag;
    this.attributes = attributes;
    this.document = document;
    this.value = attributes.value ?? '';
    this.dataset = Object.fromEntries(Object.entries(attributes).filter(([key]) => key.startsWith('data-')).map(([key, value]) => [key.slice(5), value]));
    this.listeners = new Map();
    this.elements = { namedItem: name => document.items.find(item => item.attributes.name === name) };
  }
  addEventListener(name, handler) { this.listeners.set(name, [...this.listeners.get(name) ?? [], handler]); }
  dispatch(name) {
    if (name === 'click' && 'disabled' in this.attributes) return;
    for (const handler of this.listeners.get(name) ?? []) handler({ currentTarget: this, target: this, preventDefault() {} });
  }
  reset() { for (const item of this.document.items) if (item.tag === 'input') item.value = ''; }
  focus() {}
  setSelectionRange() {}
}

class Document {
  items = [];
  html = '';
  constructor() {
    const document = this;
    this.root = {
      get innerHTML() { return document.html; },
      set innerHTML(value) {
        document.html = value;
        document.items = [...value.matchAll(/<(button|input|form|img)\b([^>]*)>/g)].map(([, tag, attrs]) => new Element(tag, Object.fromEntries([...attrs.matchAll(/([\w-]+)(?:="([^"]*)")?/g)].map(([, key, content]) => [key, content ?? ''])), document));
      },
    };
  }
  querySelector(selector) { return selector === '#app' ? this.root : this.querySelectorAll(selector)[0] ?? null; }
  querySelectorAll(selector) {
    if (selector.startsWith('#')) return this.items.filter(item => item.attributes.id === selector.slice(1));
    const match = selector.match(/^\[([^=\]]+)(?:="([^"]*)")?\]$/);
    return match ? this.items.filter(item => match[1] in item.attributes && (match[2] === undefined || item.attributes[match[1]] === match[2])) : [];
  }
}

const ok = data => new Response(JSON.stringify({ data, error: null }));
const failure = (status, code, message) => new Response(JSON.stringify({ data: null, error: { code, message } }), { status });
const user = { login: 'demo', name: '<测试同学>', avatarUrl: '/api/avatar' };
const courses = [{ id: 1, identifier: 'COURSE_A', name: '课堂甲' }, { id: 2, identifier: 'COURSE_B', name: '课堂乙' }];
const homeworks = [{ id: 20, name: '作业一', status: ['未完成'] }];
const task = { courseIdentifier: 'COURSE_A', homeworkId: 20, game: { identifier: 'game-1' }, challenge: { subject: '<题目一>' }, shixun: { name: '实训一' }, url: 'https://www.educoder.net/tasks/game-1' };
const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve)); };
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; };
let appVersion = 0;

async function app(overrides = {}) {
  const document = new Document();
  const requests = [];
  const opened = [];
  globalThis.document = document;
  globalThis.window = { open: (...args) => opened.push(args) };
  globalThis.localStorage = globalThis.sessionStorage = { setItem() { throw new Error('Browser persistence must not store credentials'); } };
  globalThis.fetch = async (url, options) => {
    requests.push({ url, options });
    if (overrides[url]) return overrides[url](options);
    if (url === '/api/auth/me') return ok({ user });
    if (url === '/api/auth/login') return ok({ user });
    if (url === '/api/auth/logout') return ok({ loggedOut: true });
    if (url === '/api/courses') return ok(courses);
    if (url.endsWith('/enter')) return ok(task);
    if (url.endsWith('/homeworks')) return ok(homeworks);
    throw new Error(`Unexpected URL ${url}`);
  };
  await import(`./dist/app.js?test=${++appVersion}`);
  await settle();
  return { document, requests, opened, html: () => document.html, get: selector => {
    const element = document.querySelector(selector);
    assert.ok(element, `Missing element ${selector}`);
    return element;
  } };
}

await test('compiled workbench UI behavior', async t => {
  await t.test('restores session, loads courses and homework using identifiers, and renders real avatar', async () => {
    const page = await app();
    assert.deepEqual(page.requests.map(item => item.url), ['/api/auth/me', '/api/courses', '/api/courses/COURSE_A/homeworks']);
    assert.match(page.html(), /课堂甲/);
    assert.match(page.html(), /作业一/);
    assert.match(page.html(), /&lt;测试同学&gt;/);
    assert.match(page.html(), /status-progress">未完成/);
    assert.equal(page.get('[data-role="avatar"]').attributes.src, '/api/avatar');
    page.get('[data-role="avatar"]').dispatch('error');
    assert.equal(page.document.querySelector('[data-role="avatar"]'), null);
    assert.match(page.html(), /class="avatar"/);
  });
  await t.test('login/logout complete through the Go endpoints and clear private UI state', async () => {
    const page = await app({ '/api/auth/me': () => failure(401, 'AUTH_REQUIRED', '请先登录') });
    page.get('#login').value = ' demo ';
    page.get('#password').value = 'test-only-password';
    page.get('#login-form').dispatch('submit');
    await settle();
    assert.deepEqual(JSON.parse(page.requests.find(item => item.url === '/api/auth/login').options.body), { login: 'demo', password: 'test-only-password' });
    assert.doesNotMatch(page.html(), /test-only-password/);
    page.get('[data-action="logout"]').dispatch('click');
    await settle();
    assert.match(page.html(), /id="login-form"/);
    assert.doesNotMatch(page.html(), /课堂甲|作业一|测试同学/);
    assert.equal(page.get('#password').value, '');
  });
  await t.test('wrong credentials keep the actual login error and permit retry', async () => {
    const page = await app({ '/api/auth/me': () => failure(401, 'AUTH_REQUIRED', '请先登录'), '/api/auth/login': () => failure(401, 'AUTH_FAILED', '账号或密码不正确') });
    page.get('#login').value = 'demo';
    page.get('#password').value = 'wrong';
    page.get('#login-form').dispatch('submit');
    await settle();
    assert.match(page.html(), /账号或密码不正确/);
    assert.doesNotMatch(page.html(), /登录已过期|正在登录/);
  });
  await t.test('enter opens a local drawer and only an explicit action opens the official site', async () => {
    const page = await app();
    page.get('[data-homework="20"]').dispatch('click');
    await settle();
    assert.match(page.html(), /role="dialog"/);
    assert.match(page.html(), /&lt;题目一&gt;/);
    assert.match(page.html(), /头歌官方平台/);
    assert.equal(page.opened.length, 0);
    page.get('[data-action="open-official"]').dispatch('click');
    assert.deepEqual(page.opened, [['https://www.educoder.net/tasks/game-1', '_blank', 'noopener,noreferrer']]);
    page.get('[data-action="close-drawer"]').dispatch('click');
    assert.doesNotMatch(page.html(), /role="dialog"/);
  });
  await t.test('untrusted task URLs cannot become executable navigation', async () => {
    const page = await app({ '/api/courses/COURSE_A/homeworks/20/enter': () => ok({ ...task, url: 'javascript:alert(1)' }) });
    page.get('[data-homework="20"]').dispatch('click');
    await settle();
    assert.ok('disabled' in page.get('[data-action="open-official"]').attributes);
    page.get('[data-action="open-official"]').dispatch('click');
    assert.equal(page.opened.length, 0);
  });
  await t.test('empty courses, empty tasks and search have distinct feedback', async () => {
    const empty = await app({ '/api/courses': () => ok([]) });
    assert.match(empty.html(), /还没有课堂/);
    const tasks = await app({ '/api/courses/COURSE_A/homeworks': () => ok([]) });
    assert.match(tasks.html(), /这个课堂还没有任务/);
    const filtered = await app();
    filtered.get('[data-role="filter"]').value = '不存在的任务';
    filtered.get('[data-role="filter"]').dispatch('input');
    assert.match(filtered.html(), /没有找到相关任务/);
    filtered.get('[data-course="COURSE_B"]').dispatch('click');
    await settle();
    assert.equal(filtered.get('[data-role="filter"]').value, '');
    assert.match(filtered.html(), /作业一/);
  });
  await t.test('slow classroom responses cannot overwrite a newer classroom selection', async () => {
    const pending = deferred();
    const page = await app({ '/api/courses/COURSE_A/homeworks': () => pending.promise, '/api/courses/COURSE_B/homeworks': () => ok([{ id: 99, name: '乙课堂作业' }]) });
    assert.match(page.html(), /正在加载课堂内容/);
    page.get('[data-course="COURSE_B"]').dispatch('click');
    await settle();
    pending.resolve(ok([{ id: 21, name: '迟到的甲课堂作业' }]));
    await settle();
    assert.match(page.html(), /乙课堂作业/);
    assert.doesNotMatch(page.html(), /迟到的甲课堂作业/);
  });
  await t.test('logout invalidates pending classroom requests', async () => {
    const pending = deferred();
    const page = await app({ '/api/courses/COURSE_A/homeworks': () => pending.promise });
    page.get('[data-action="logout"]').dispatch('click');
    pending.resolve(ok(homeworks));
    await settle();
    assert.match(page.html(), /id="login-form"/);
    assert.doesNotMatch(page.html(), /作业一/);
  });
  await t.test('closing a pending task keeps its eventual result out of the drawer', async () => {
    const pending = deferred();
    const page = await app({ '/api/courses/COURSE_A/homeworks/20/enter': () => pending.promise });
    page.get('[data-homework="20"]').dispatch('click');
    assert.match(page.html(), /正在打开任务/);
    page.get('[data-action="close-drawer"]').dispatch('click');
    pending.resolve(ok(task));
    await settle();
    assert.doesNotMatch(page.html(), /role="dialog"|&lt;题目一&gt;/);
  });
  await t.test('expired authenticated requests return to an enabled login form', async () => {
    const page = await app({ '/api/courses/COURSE_A/homeworks': () => failure(401, 'AUTH_REQUIRED', '请先登录') });
    assert.match(page.html(), /登录已过期，请重新登录/);
    assert.doesNotMatch(page.html(), /正在登录/);
    assert.ok(page.get('#login-form'));
  });
  await t.test('upstream task errors show feedback and allow a later retry', async () => {
    let attempts = 0;
    const page = await app({ '/api/courses/COURSE_A/homeworks/20/enter': () => ++attempts === 1 ? failure(502, 'UPSTREAM_ERROR', '任务服务暂时不可用') : ok(task) });
    page.get('[data-homework="20"]').dispatch('click');
    await settle();
    assert.match(page.html(), /任务服务暂时不可用/);
    page.get('[data-action="close-drawer"]').dispatch('click');
    page.get('[data-homework="20"]').dispatch('click');
    await settle();
    assert.match(page.html(), /&lt;题目一&gt;/);
    assert.doesNotMatch(page.html(), /任务服务暂时不可用/);
  });
});

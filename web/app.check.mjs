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
    this.dataset = Object.fromEntries(Object.entries(attributes).filter(([key]) => key.startsWith('data-')).map(([key, value]) => [key.slice(5).replace(/-([a-z])/g, (_, letter) => letter.toUpperCase()), value]));
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
const task = { kind: 'programming', courseIdentifier: 'COURSE_A', homeworkId: 20, game: { identifier: 'game-1' }, challenge: { subject: '<题目一>' }, shixun: { name: '实训一' }, url: 'https://www.educoder.net/tasks/game-1' };
const capacity = { workers: 4, queue_size: 100, queued: 0, running: 0, per_owner_pending: 5, model_concurrency: 2, model_configured: true };
const answerJob = (next = {}) => ({ id: 'job-1', status: 'queued', phase: 'queued', kind: 'programming', request: { course: 'COURSE_A', homework_id: 20, game: 'game-1', mode: 'generate_only' }, created_at: '2026-10-31T00:00:00Z', updated_at: '2026-10-31T00:00:00Z', ...next });
const collectionJob = (next = {}) => answerJob({ kind: 'collection', phase: 'collecting', request: { course: '', homework_id: 0, mode: 'collect_verified' }, ...next });
const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve)); };
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; };
let appVersion = 0;

async function app(overrides = {}) {
  const document = new Document();
  const requests = [];
  const opened = [];
  const timers = new Map();
  let timerId = 0;
  globalThis.setTimeout = (handler, delay) => { const id = ++timerId; timers.set(id, { handler, delay }); return id; };
  globalThis.clearTimeout = id => { timers.delete(id); };
  globalThis.document = document;
  globalThis.window = { open: (...args) => opened.push(args) };
  globalThis.localStorage = globalThis.sessionStorage = { setItem() { throw new Error('Browser persistence must not store credentials'); } };
  globalThis.fetch = async (url, options) => {
    requests.push({ url, options });
    if (overrides[url]) return overrides[url](options);
    if (url === '/api/auth/me') return ok({ user });
    if (url === '/api/auth/login') return ok({ user });
    if (url === '/api/auth/logout') return ok({ loggedOut: true });
    if (url === '/api/jobs') return ok({ jobs: [], capacity });
    if (url === '/api/courses') return ok(courses);
    if (url.endsWith('/enter')) return ok(task);
    if (url.endsWith('/homeworks')) return ok(homeworks);
    throw new Error(`Unexpected URL ${url}`);
  };
  await import(`./dist/app.js?test=${++appVersion}`);
  await settle();
  return { document, requests, opened, timers, tick: async () => { const entries = [...timers.entries()]; for (const [id, timer] of entries) { timers.delete(id); timer.handler(); } await settle(); }, html: () => document.html, get: selector => {
    const element = document.querySelector(selector);
    assert.ok(element, `Missing element ${selector}`);
    return element;
  } };
}

await test('compiled workbench UI behavior', async t => {
  await t.test('restores session, loads courses and homework using identifiers, and renders real avatar', async () => {
    const page = await app();
    assert.deepEqual(page.requests.map(item => item.url), ['/api/auth/me', '/api/jobs', '/api/courses', '/api/courses/COURSE_A/homeworks']);
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
  await t.test('accepted jobs are queued asynchronously, protected against duplicate clicks and polled to completion', async () => {
    const accepting = deferred();
    let job = null;
    let postCount = 0;
    const page = await app({ '/api/jobs': options => {
      if (options.method === 'POST') { postCount++; return accepting.promise; }
      return ok({ jobs: job ? [job] : [], capacity });
    } });
    page.get('[data-homework="20"]').dispatch('click');
    await settle();
    page.get('[data-job-mode="submit"]').dispatch('change');
    page.get('[data-action="create-job"]').dispatch('click');
    page.get('[data-action="create-job"]').dispatch('click');
    assert.equal(postCount, 1);
    const request = JSON.parse(page.requests.find(item => item.url === '/api/jobs' && item.options.method === 'POST').options.body);
    assert.deepEqual({ ...request, idempotency_key: 'redacted' }, { course: 'COURSE_A', homework_id: 20, game: 'game-1', mode: 'submit', idempotency_key: 'redacted' });
    assert.match(request.idempotency_key, /^[a-f0-9-]{36}$/);
    job = answerJob({ request, status: 'queued' });
    accepting.resolve(new Response(JSON.stringify({ data: job, error: null }), { status: 202 }));
    await settle();
    assert.match(page.html(), /排队中/);
    assert.equal(page.timers.size, 1);
    assert.equal([...page.timers.values()][0].delay, 2000);
    page.get('[data-action="close-drawer"]').dispatch('click');
    page.get('[data-course="COURSE_B"]').dispatch('click');
    await settle();
    assert.match(page.html(), /排队中/);
    job = answerJob({ status: 'running', phase: 'generating', request });
    await page.tick();
    assert.match(page.html(), /生成答案/);
    job = answerJob({ status: 'succeeded', phase: 'completed', request, result: { model_calls: 1, status: 'submitted', score: 50 } });
    await page.tick();
    assert.match(page.html(), /流程已完成/);
    assert.match(page.html(), /平台得分 50/);
    assert.doesNotMatch(page.html(), /已核验满分/);
    assert.equal(page.timers.size, 0);
  });
  await t.test('objective tasks omit selftest, unknown types and missing model disable execution', async () => {
    const objective = await app({ '/api/courses/COURSE_A/homeworks/20/enter': () => ok({ ...task, kind: 'choice' }) });
    objective.get('[data-homework="20"]').dispatch('click');
    await settle();
    assert.equal(objective.document.querySelector('[data-job-mode="selftest"]'), null);
    assert.match(objective.html(), /提交并核验满分/);
    const unknown = await app({ '/api/courses/COURSE_A/homeworks/20/enter': () => ok({ ...task, kind: 'unrecognized' }) });
    unknown.get('[data-homework="20"]').dispatch('click');
    await settle();
    assert.ok('disabled' in unknown.get('[data-action="create-job"]').attributes);
    assert.match(unknown.html(), /尚未识别该关卡题型/);
    const unconfigured = await app({ '/api/jobs': () => ok({ jobs: [], capacity: { ...capacity, model_configured: false } }) });
    unconfigured.get('[data-homework="20"]').dispatch('click');
    await settle();
    assert.ok('disabled' in unconfigured.get('[data-action="create-job"]').attributes);
    assert.match(unconfigured.html(), /服务端尚未配置模型/);
  });
  await t.test('explicit cancel requests preserve uncertain platform outcomes and do not imply a rollback', async () => {
    let job = answerJob({ status: 'running', phase: 'submitting', request: { ...answerJob().request, mode: 'submit' } });
    const page = await app({ '/api/jobs': () => ok({ jobs: [job], capacity }), '/api/jobs/job-1/cancel': () => {
      job = { ...job, status: 'uncertain', phase: 'verifying', message: '<提交结果待确认>' };
      return ok(job);
    } });
    page.get('[data-cancel-job="job-1"]').dispatch('click');
    await settle();
    assert.equal(page.requests.find(item => item.url.endsWith('/cancel')).options.method, 'POST');
    assert.match(page.html(), /平台结果待复核/);
    assert.match(page.html(), /先核对平台状态/);
    assert.match(page.html(), /&lt;提交结果待确认&gt;/);
    assert.equal(page.timers.size, 0);
    assert.equal(page.document.querySelector('[data-cancel-job="job-1"]'), null);
  });
  await t.test('logout stops job polling and invalidates late private results without canceling accepted jobs', async () => {
    const pending = deferred();
    let reads = 0;
    const page = await app({ '/api/jobs': () => ++reads === 1 ? ok({ jobs: [answerJob()], capacity }) : pending.promise });
    const timer = [...page.timers.values()][0];
    await page.tick();
    page.get('[data-action="logout"]').dispatch('click');
    await settle();
    pending.resolve(ok({ jobs: [answerJob({ message: '私有任务状态' })], capacity }));
    await settle();
    timer.handler();
    await settle();
    assert.match(page.html(), /id="login-form"/);
    assert.doesNotMatch(page.html(), /私有任务状态|答题任务记录/);
    assert.equal(page.timers.size, 0);
    assert.equal(page.requests.filter(item => item.url.endsWith('/cancel')).length, 0);
    assert.equal(reads, 2);
  });
  await t.test('new session restores job history and polling authorization expiry clears it', async () => {
    let reads = 0;
    const page = await app({ '/api/jobs': () => ++reads === 1 ? ok({ jobs: [answerJob({ message: '已有任务' })], capacity }) : failure(401, 'UNAUTHENTICATED', '请重新登录') });
    assert.match(page.html(), /已有任务/);
    await page.tick();
    assert.match(page.html(), /登录已过期，请重新登录/);
    assert.doesNotMatch(page.html(), /已有任务/);
    assert.equal(page.timers.size, 0);
  });
  await t.test('job API failure remains visible and does not erase coursework or leak raw result fields', async () => {
    const page = await app({ '/api/jobs': () => failure(503, 'JOBS_UNAVAILABLE', '任务服务暂时不可用') });
    assert.match(page.html(), /任务服务暂时不可用/);
    assert.match(page.html(), /作业一/);
    const result = await app({ '/api/jobs': () => ok({ jobs: [answerJob({ status: 'succeeded', result: { full_score_verified: false, raw_credentials: 'secret-not-rendered', score: 70 } })], capacity }) });
    assert.match(result.html(), /平台得分 70/);
    assert.doesNotMatch(result.html(), /secret-not-rendered|已核验满分/);
  });
  await t.test('objective full-score claims require verified score evidence and programming progress remains separate', async () => {
    const verified = await app({ '/api/jobs': () => ok({ jobs: [answerJob({ status: 'succeeded', result: { status: 'passed', kind: 'objective', score: '10', max_score: '10', score_known: true, score_check: 'full_score', cost_micro_usd: 1500 } })], capacity }) });
    assert.match(verified.html(), /平台得分 10 \/ 10/);
    assert.match(verified.html(), /已核验满分/);
    assert.match(verified.html(), /模型费用 \$0.001500/);
    const unknown = await app({ '/api/jobs': () => ok({ jobs: [answerJob({ status: 'succeeded', result: { status: 'unconfirmed', kind: 'objective', score: '10', max_score: '10', score_known: false, score_check: 'unknown' } })], capacity }) });
    assert.doesNotMatch(unknown.html(), /已核验满分/);
    assert.match(unknown.html(), /平台分数待确认/);
    const programming = await app({ '/api/jobs': () => ok({ jobs: [answerJob({ status: 'succeeded', result: { status: 'passed', programming: { progress_error: 'upstream-status-error', self_test_status: 'skipped' } } })], capacity }) });
    assert.match(programming.html(), /课堂进度待同步/);
    assert.match(programming.html(), /本关无公开样例/);
    assert.doesNotMatch(programming.html(), /upstream-status-error/);
  });
  await t.test('logout invalidates a late create response and next login reloads account-scoped history', async () => {
    const accepting = deferred();
    const page = await app({ '/api/jobs': options => options.method === 'POST' ? accepting.promise : ok({ jobs: [], capacity }) });
    page.get('[data-homework="20"]').dispatch('click');
    await settle();
    page.get('[data-action="create-job"]').dispatch('click');
    page.get('[data-action="logout"]').dispatch('click');
    await settle();
    accepting.resolve(new Response(JSON.stringify({ data: answerJob({ message: '旧账号任务' }), error: null }), { status: 202 }));
    await settle();
    assert.match(page.html(), /id="login-form"/);
    assert.doesNotMatch(page.html(), /旧账号任务/);
    assert.equal(page.timers.size, 0);
    page.get('#login').value = 'new-user';
    page.get('#password').value = 'test-password';
    page.get('#login-form').dispatch('submit');
    await settle();
    assert.equal(page.requests.filter(item => item.url === '/api/jobs' && item.options.method !== 'POST').length, 2);
    assert.doesNotMatch(page.html(), /旧账号任务/);
  });
  await t.test('login collection distinguishes a completed scan from successful answer collection and explains skipped records', async () => {
    const job = collectionJob({ status: 'succeeded', message: '已采集 0，重复 0，跳过 68，未确认 56，失败 0', result: { collected: 0, courses: 5, duplicates: 0, failed: 0, homeworks: 68, levels: 0, limit_reached: false, reasons: { no_confirmed_full_score: 12, no_readonly_game_locator: 56 }, skipped: 68, unconfirmed: 56 } });
    const page = await app({ '/api/jobs': () => ok({ jobs: [job], capacity }) });
    assert.match(page.html(), /<strong>已通过任务采集<\/strong>/);
    assert.match(page.html(), /登录后自动扫描/);
    assert.match(page.html(), /status-attention">扫描结束，未新增答案/);
    assert.match(page.html(), /采集扫描已结束/);
    assert.match(page.html(), /已扫描课堂 5 · 作业 68 · 关卡读取尝试 0/);
    assert.match(page.html(), /<dt>新增答案<\/dt><dd>0<\/dd>/);
    assert.match(page.html(), /<dt>跳过<\/dt><dd>68<\/dd>/);
    assert.match(page.html(), /未确认 56 条（包含在跳过中）/);
    assert.match(page.html(), /未能读取任何关卡，尚未获取到可入库的题目与答案/);
    assert.match(page.html(), /未取得可核验的满分记录：12 项/);
    assert.match(page.html(), /未取得只读关卡入口：56 项/);
    assert.doesNotMatch(page.html(), /作业 0|collect_verified|no_readonly_game_locator|no_confirmed_full_score|>collecting<|流程已完成|已采集 0，重复/);
    assert.equal(page.timers.size, 0);
  });
  await t.test('collection reports positive partial results and scan limits without model costs or raw fields', async () => {
    const job = collectionJob({ status: 'succeeded', result: { collected: 3, duplicates: 2, skipped: 1, failed: 1, courses: 1, homeworks: 10, levels: 4, limit_reached: true, model_calls: 999, cost_micro_usd: 999, raw_credentials: 'must-stay-private', reasons: { request_byte_or_time_limit: 1 } } });
    const page = await app({ '/api/jobs': () => ok({ jobs: [job], capacity }) });
    assert.match(page.html(), /扫描结束，新增 3 条答案/);
    assert.match(page.html(), /<dt>题库已有<\/dt><dd>2<\/dd>/);
    assert.match(page.html(), /<dt>读取或入库失败<\/dt><dd>1<\/dd>/);
    assert.match(page.html(), /已达到本次扫描上限，以上仅为部分结果，尚未遍历全部任务/);
    assert.match(page.html(), /达到请求流量或扫描时长上限：1 项/);
    assert.doesNotMatch(page.html(), /模型调用|模型费用|must-stay-private|未能读取任何关卡|未确认 0 条/);
  });
  await t.test('queued and running collection is polled and canceled as a read-only scan, not a submission', async () => {
    let job = collectionJob({ status: 'queued', phase: 'queued' });
    const page = await app({ '/api/jobs': () => ok({ jobs: [job], capacity }), '/api/jobs/job-1/cancel': () => {
      job = { ...job, status: 'canceled', message: 'raw-cancel-message' };
      return ok(job);
    } });
    assert.match(page.html(), /等待扫描/);
    assert.match(page.html(), /等待后台扫描/);
    assert.match(page.html(), /统计尚未返回/);
    assert.doesNotMatch(page.html(), /<dt>新增答案<\/dt><dd>0/);
    job = { ...job, status: 'running', phase: 'collecting' };
    await page.tick();
    assert.match(page.html(), /扫描中/);
    assert.match(page.html(), /核验已通过任务/);
    assert.match(page.html(), /停止扫描/);
    page.get('[data-homework="20"]').dispatch('click');
    await settle();
    assert.equal(page.document.querySelector('[data-job-mode="collect_verified"]'), null);
    page.get('[data-cancel-job="job-1"]').dispatch('click');
    await settle();
    assert.match(page.html(), /扫描已停止/);
    assert.match(page.html(), /后续扫描已停止；已存入本地题库的记录会保留/);
    assert.doesNotMatch(page.html(), /已送达平台|提交不会撤回|上游可能已受理|raw-cancel-message/);
    assert.equal(page.timers.size, 0);
    assert.equal(page.requests.filter(item => item.url.endsWith('/cancel')).length, 1);
  });
  await t.test('restarted or failed collection states retain partial counts and do not imply answer submission', async () => {
    for (const [status, text] of [['interrupted', '扫描已中断'], ['failed', '扫描未完成'], ['uncertain', '扫描结果待确认']]) {
      const page = await app({ '/api/jobs': () => ok({ jobs: [collectionJob({ status, result: { collected: 1, skipped: 2 } })], capacity }) });
      assert.ok(page.html().includes(text));
      assert.match(page.html(), /<dt>新增答案<\/dt><dd>1<\/dd>/);
      assert.match(page.html(), /采集扫描已结束/);
      assert.doesNotMatch(page.html(), /上游可能已受理|请核对平台状态，再手动创建|保存或提交不会撤回|未能读取任何关卡|作业 0/);
      assert.equal(page.timers.size, 0);
    }
  });
  await t.test('collection only displays allowlisted summaries and maps unknown reasons without exposing keys or messages', async () => {
    const page = await app({ '/api/jobs': () => ok({ jobs: [collectionJob({ id: '<scan-id>', status: 'succeeded', message: '<img src=x onerror=alert(1)>raw-secret-message', events: [{ phase: 'collecting', message: 'raw-event-secret' }], result: { reasons: { '<img src=x onerror=alert(1)>': 2, secret_token: 3, constructor: 1, editor_changed: 1, invalid: -4, nonnumber: '2' }, raw_credentials: 'hidden-cookie-value' } })], capacity }) });
    assert.match(page.html(), /data-job-id="&lt;scan-id&gt;"/);
    assert.match(page.html(), /扫描已结束，统计待获取/);
    assert.match(page.html(), /统计尚未返回/);
    assert.match(page.html(), /两次读取的编辑器内容发生变化：1 项/);
    assert.match(page.html(), /其他未能确认或读取的记录：6 项/);
    assert.doesNotMatch(page.html(), /<img src=x|onerror|secret_token|constructor|raw-secret-message|raw-event-secret|hidden-cookie-value|新增答案<\/dt><dd>0|未新增答案/);
  });
  await t.test('collection distinguishes actual zero counters from missing or malformed statistics', async () => {
    for (const result of [undefined, {}, { collected: '0', levels: -1, courses: null, homeworks: 1.5, skipped: false, failed: Infinity }]) {
      const page = await app({ '/api/jobs': () => ok({ jobs: [collectionJob({ status: 'succeeded', result })], capacity }) });
      assert.match(page.html(), /扫描已结束，统计待获取/);
      assert.match(page.html(), /统计尚未返回/);
      assert.doesNotMatch(page.html(), /新增答案<\/dt><dd>0|未能读取任何关卡|已扫描课堂 0|未新增答案/);
    }
  });
});

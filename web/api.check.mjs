import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ApiRequestError, BrowserApi } from './dist/api.js';

const envelope = (data, status = 200) => new Response(JSON.stringify({ data, error: null, requestId: 'test-request' }), { status });

await test('browser API uses the Go workbench contract', async t => {
  await t.test('question-bank summary uses authenticated read-only counts and retains genuine zeroes', async () => {
    let request;
    const summary = { verified_objective_questions: 0, verified_programming_problems: 19, total_verified_questions: 19 };
    globalThis.fetch = async (url, options) => {
      request = { url, options };
      return envelope({ ...summary, questions: ['not part of this public contract'], owner: 'private-owner', answers: ['private-answer'] });
    };
    assert.deepEqual(await new BrowserApi().questionBankSummary(), summary);
    assert.equal(request.url, '/api/question-bank/summary');
    assert.equal(request.options.credentials, 'include');
    assert.equal(request.options.body, undefined);
    assert.ok(request.options.method === undefined || request.options.method === 'GET');
    globalThis.fetch = async () => envelope({ verified_objective_questions: 0, verified_programming_problems: 0, total_verified_questions: 0 });
    assert.deepEqual(await new BrowserApi().questionBankSummary(), { verified_objective_questions: 0, verified_programming_problems: 0, total_verified_questions: 0 });
  });
  await t.test('question-bank summary rejects missing, coerced, negative, fractional, unsafe or inconsistent counts', async () => {
    const valid = { verified_objective_questions: 2, verified_programming_problems: 3, total_verified_questions: 5 };
    const invalid = [null, [], {}, { ...valid, verified_objective_questions: undefined },
      { ...valid, verified_objective_questions: '2' }, { ...valid, verified_programming_problems: null },
      { ...valid, verified_objective_questions: -1 }, { ...valid, verified_objective_questions: 2.5 },
      { ...valid, total_verified_questions: Number.MAX_SAFE_INTEGER + 1 },
      { ...valid, total_verified_questions: 0 },
      { verified_objective_questions: Number.MAX_SAFE_INTEGER, verified_programming_problems: 1, total_verified_questions: Number.MAX_SAFE_INTEGER },
    ];
    for (const value of invalid) {
      globalThis.fetch = async () => envelope(value);
      await assert.rejects(new BrowserApi().questionBankSummary(), error => error instanceof ApiRequestError && error.code === 'INVALID_RESPONSE' && !error.isSessionExpired, JSON.stringify(value));
    }
  });
  await t.test('question-bank failures preserve authentication and availability errors instead of showing zero counts', async () => {
    globalThis.fetch = async () => new Response(JSON.stringify({ data: null, error: { code: 'UNAUTHENTICATED', message: '请先登录' } }), { status: 401 });
    await assert.rejects(new BrowserApi().questionBankSummary(), error => error instanceof ApiRequestError && error.isSessionExpired);
    globalThis.fetch = async () => new Response(JSON.stringify({ data: null, error: { code: 'QUESTION_BANK_UNAVAILABLE', message: '题库暂时不可用' } }), { status: 503 });
    await assert.rejects(new BrowserApi().questionBankSummary(), error => error instanceof ApiRequestError && error.status === 503 && error.code === 'QUESTION_BANK_UNAVAILABLE' && !error.isSessionExpired);
    globalThis.fetch = async () => new Response('<html>proxy error</html>');
    await assert.rejects(new BrowserApi().questionBankSummary(), error => error instanceof ApiRequestError && error.code === 'INVALID_RESPONSE');
  });
  await t.test('exam catalog keeps explicit submission and grade evidence without treating hidden or missing scores as published', async () => {
    let requested;
    globalThis.fetch = async url => { requested = url; return envelope({ items: [
      { id: 701, name: '试卷一', available: false, submitted: true, scoreKnown: false, score: '--' },
      { id: 702, name: '试卷二', available: false, submitted: true, scoreKnown: true, score: '0.00', maxScore: '100.00' },
      { id: 703, name: '试卷三', submitted: false },
    ], courseIdentifier: 'COURSE/A' }); };
    const result = await new BrowserApi().exercises('COURSE/A');
    assert.equal(requested, '/api/courses/COURSE%2FA/exercises');
    assert.equal(result.courseIdentifier, 'COURSE/A');
    assert.equal(result.items[0].submitted, true); assert.equal(result.items[0].scoreKnown, false);
    assert.equal(result.items[1].score, '0.00'); assert.equal(result.items[1].maxScore, '100.00');
    assert.equal(result.items[2].available, false); assert.equal(result.items[2].scoreKnown, false);
  });
  await t.test('login sends credentials only in the request body and uses the session cookie', async () => {
    let request;
    globalThis.fetch = async (url, options) => {
      request = { url, options };
      return envelope({ user: { id: 7, login: 'demo', name: '测试同学', avatarUrl: '/api/avatar' } });
    };
    const user = await new BrowserApi().login(' demo ', 'test-only-password');
    assert.equal(request.url, '/api/auth/login');
    assert.equal(request.options.method, 'POST');
    assert.equal(request.options.credentials, 'include');
    assert.deepEqual(JSON.parse(request.options.body), { login: 'demo', password: 'test-only-password' });
    assert.equal(user.avatarUrl, '/api/avatar');
    assert.equal(user.name, '测试同学');
  });
  await t.test('classroom identifiers take priority over numeric IDs and URL fallback remains usable', async () => {
    globalThis.fetch = async () => envelope([
      { id: 123, identifier: 'CLASSROOM_A', name: '第一课堂', ended: false, homeworkCount: 0 },
      { id: 456, first_category_url: '/classrooms/CLASSROOM_B/shixun_homework', name: '第二课堂' },
    ]);
    const courses = await new BrowserApi().courses();
    assert.deepEqual(courses.map(course => course.identifier), ['CLASSROOM_A', 'CLASSROOM_B']);
  });
  await t.test('homework and enter responses normalize the current Go DTO', async () => {
    const requests = [];
    globalThis.fetch = async (url, options) => {
      requests.push({ url, options });
      return url.endsWith('/enter') ? envelope({ courseIdentifier: 'CLASSROOM_A', homeworkId: 20, game: { identifier: 'game-1', status: 0 }, challenge: { subject: '编程练习', description: '题干' }, shixun: { name: '实训一' }, url: 'https://www.educoder.net/tasks/game-1' }) : envelope([{ id: 20, name: '作业一', status: ['未完成', '进行中'], shixunIdentifier: 'shixun-1' }]);
    };
    const api = new BrowserApi();
    const homeworks = await api.homeworks('CLASSROOM_A');
    const entered = await api.enter('CLASSROOM_A', homeworks[0].id);
    assert.equal(requests[0].url, '/api/courses/CLASSROOM_A/homeworks');
    assert.equal(requests[1].url, '/api/courses/CLASSROOM_A/homeworks/20/enter');
    assert.equal(homeworks[0].status, '未完成、进行中');
    assert.equal(entered.gameIdentifier, 'game-1');
    assert.equal(entered.challenge.description, '题干');
  });
  await t.test('server errors preserve diagnostics, expired sessions differ from wrong credentials', async () => {
    globalThis.fetch = async () => new Response(JSON.stringify({ data: null, error: { code: 'AUTH_FAILED', message: '账号或密码不正确' }, requestId: 'request-42' }), { status: 401 });
    await assert.rejects(new BrowserApi().login('demo', 'wrong'), error => error instanceof ApiRequestError && error.message === '账号或密码不正确' && error.requestId === 'request-42' && !error.isSessionExpired);
    globalThis.fetch = async () => new Response(JSON.stringify({ data: null, error: { code: 'AUTH_REQUIRED', message: '请先登录' } }), { status: 401 });
    await assert.rejects(new BrowserApi().courses(), error => error.isSessionExpired);
  });
  await t.test('required-level progress keeps explicit zeroes and does not fall back to raw totals', async () => {
    globalThis.fetch = async () => envelope([{id:1,name:'必做未完成',challengeCount:10,finishedChallengeCount:5,requiredProgressKnown:true,requiredChallengeCount:5,finishedRequiredChallengeCount:0},{id:2,name:'读取未完成',challengeCount:10,finishedChallengeCount:5,requiredProgressKnown:false}]);
    const [known, unknown] = await new BrowserApi().homeworks('COURSE_A');
    assert.equal(known.requiredProgressKnown,true); assert.equal(known.requiredChallengeCount,5); assert.equal(known.finishedRequiredChallengeCount,0);
    assert.equal(unknown.requiredProgressKnown,false); assert.equal(unknown.requiredChallengeCount,undefined); assert.equal(unknown.finishedRequiredChallengeCount,undefined);
  });
  await t.test('HTML responses fail visibly instead of becoming an empty successful result', async () => {
    globalThis.fetch = async () => new Response('<html>proxy error</html>');
    await assert.rejects(new BrowserApi().courses(), error => error.code === 'INVALID_RESPONSE');
  });
  await t.test('avatar sources remain session-bound local URLs', async () => {
    globalThis.fetch = async () => envelope({ user: { login: 'demo', name: '测试', avatarUrl: 'https://unrelated.example/track' } });
    assert.equal((await new BrowserApi().me()).avatarUrl, undefined);
  });
  await t.test('answer jobs use accepted asynchronous responses, session cookies and encoded IDs', async () => {
    const requests = [];
    const job = { id: 'job/1', status: 'queued', phase: 'queued' };
    globalThis.fetch = async (url, options) => {
      requests.push({ url, options });
      return envelope(url === '/api/jobs' && options.method !== 'POST' ? { jobs: [job], capacity: { model_configured: true } } : job, options.method === 'POST' && url === '/api/jobs' ? 202 : 200);
    };
    const api = new BrowserApi();
    const input = { course: 'COURSE_A', homework_id: 20, game: 'game-1', mode: 'submit', idempotency_key: 'click-1' };
    assert.equal((await api.createJob(input)).status, 'queued');
    assert.deepEqual(JSON.parse(requests[0].options.body), input);
    assert.equal(requests[0].options.credentials, 'include');
    assert.equal((await api.jobs()).jobs[0].id, 'job/1');
    await api.job('job/1');
    await api.cancelJob('job/1');
    assert.equal(requests[2].url, '/api/jobs/job%2F1');
    assert.equal(requests[3].url, '/api/jobs/job%2F1/cancel');
    assert.equal(requests[3].options.method, 'POST');
  });
  await t.test('foreign job errors remain errors rather than empty successful histories', async () => {
    globalThis.fetch = async () => new Response(JSON.stringify({ error: { code: 'JOB_NOT_FOUND', message: '任务不存在' } }), { status: 404 });
    await assert.rejects(new BrowserApi().job('foreign-job'), error => error.code === 'JOB_NOT_FOUND' && !error.isSessionExpired);
  });
  await t.test('login-triggered collection history retains read-only mode and structured counters without creating an answer request', async () => {
    const requests = [];
    const job = { id: 'scan-1', status: 'succeeded', phase: 'collecting', request: { mode: 'collect_verified' }, result: { collected: 0, skipped: 68, unconfirmed: 56, levels: 0, reasons: { no_readonly_game_locator: 56, no_confirmed_full_score: 12 } } };
    globalThis.fetch = async (url, options) => { requests.push({ url, options }); return envelope({ jobs: [job], capacity: { model_configured: true } }); };
    const result = await new BrowserApi().jobs();
    assert.deepEqual(result.jobs[0], job);
    assert.equal(result.jobs[0].result.duplicates, undefined);
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, '/api/jobs');
    assert.notEqual(requests[0].options.method, 'POST');
  });
  await t.test('local role comes from the server platform identity and login accepts only upstream credentials', async () => {
    let request;
    globalThis.fetch = async (url, options) => { request = { url, options }; return envelope({ user: { id: 91, name: '同学', role: 'admin' }, platform: { id: 'local-uuid', role: 'user', status: 'active' } }); };
    const api = new BrowserApi();
    const user = await api.login('demo', 'test-password');
    assert.equal(user.role, 'user');
    assert.deepEqual(JSON.parse(request.options.body), { login: 'demo', password: 'test-password' });
    assert.doesNotMatch(request.url, /test-password/);
  });
  await t.test('retired access-card configuration and administration are absent from the browser API', () => {
    const api = new BrowserApi();
    for (const method of ['authConfig', 'adminCards', 'adminCreateCard', 'adminRevokeCard']) assert.equal(method in api, false, method);
  });
  await t.test('administrator user IDs stay opaque strings and mutation permission errors are not session expiry', async () => {
    let request;
    globalThis.fetch = async (url, options) => { request = { url, options }; return envelope({}); };
    await new BrowserApi().adminUserStatus('platform-user/opaque', 'disabled');
    assert.equal(request.url, '/api/admin/users/platform-user%2Fopaque/status');
    assert.deepEqual(JSON.parse(request.options.body), { status: 'disabled' });
    globalThis.fetch = async () => new Response(JSON.stringify({ error: { code: 'ADMIN_REQUIRED', message: '无权限' } }), { status: 403 });
    await assert.rejects(new BrowserApi().adminOverview(), error => !error.isSessionExpired && error.status === 403);
  });

});

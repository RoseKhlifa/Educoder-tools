import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ApiRequestError, BrowserApi } from './dist/api.js';

const envelope = (data, status = 200) => new Response(JSON.stringify({ data, error: null, requestId: 'test-request' }), { status });

await test('browser API uses the Go workbench contract', async t => {
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
});

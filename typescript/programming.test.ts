import test from 'node:test';
import assert from 'node:assert/strict';
import { EducoderClient, HarTransport, type HarEntry } from './client.ts';
import { HttpTransport, isNotModified, NOT_MODIFIED, type ProtocolConstants, type Transport } from './protocol.ts';
import { ProgrammingClient, WebSocketLogRunner, HarLogRunner, classifyTask, normalizeLineEndings } from './programming.ts';

const constants: ProtocolConstants = { baseUrl: 'http://127.0.0.1', origin: 'https://www.educoder.net', referer: 'https://www.educoder.net/', ak: 'fixture-ak', sk: 'fixture-sk', aesKey: '1234567890123456', aesIv: '1234567890123456' };
const task = () => ({ st: 0, game_count: 2, homework_common_id: 10,
  user: { user_id: 50 }, code_editor: { shixun_environment_id: 60 },
  game: { id: 20, identifier: 'g', user_id: 50, challenge_id: 30, myshixun_id: 40 },
  myshixun: { id: 40, identifier: 'm', user_id: 50 },
  challenge: { id: 30, st: 0, with_code_file: true, subject: '代码题', task_pass: '# 描述\n测试', path: 'src/中文.cpp；include/stack.h；' },
  shixun: { challenges_count: 12 }, shixun_environments: [{ tab_type: 1, shixun_environment_id: 60 }],
  test_sets: [{ input: '1\n', output: 'YES\n', result: false }] });
function client(transport: Transport): EducoderClient {
  const c = new EducoderClient(transport, constants); c.user = { login: 'fixture-user', name: 'Fixture', id: 50 }; return c;
}

test('coding type is explicit and rejects missing/unknown discriminators before writes', async () => {
  for (const data of [{}, { st: 0 }, { challenge: { st: 0, with_code_file: true } }, { st: 1, challenge: { st: 0, with_code_file: true } }, { st: 0, challenge: { st: 1, with_code_file: true } }, { st: 0, challenge: { st: 0 } }]) {
    assert.equal(classifyTask(data), 'unsupported');
    let writes = 0;
    const p = new ProgrammingClient(client({ request: async method => { if (method !== 'GET') writes++; return data; } }));
    await assert.rejects(() => p.task('g', 10), /题型/); assert.equal(writes, 0);
    await assert.rejects(() => p.save({ ...task(), kind: 'unsupported' } as any, 'x', 'x'), /题型/); assert.equal(writes, 0);
  }
});

test('coding client binds filenames, current code, save/evaluate, WebSocket and 304 polling', async () => {
  const calls: Array<{ method: string; path: string; query: any; body: any }> = [];
  let polls = 0;
  const transport: Transport = { async request(method, path, query, body) {
    calls.push({ method, path, query, body });
    if (path === '/api/tasks/g.json') return task();
    if (path.endsWith('rep_content.json')) return { content: { content: Buffer.from('/* 当前代码 */\n').toString('base64'), size: 20 }, filename: '中文.cpp', file_type: 'txt', language: 'cpp' };
    if (path.endsWith('update_file.json')) return { content: { commitID: 'commit-fixture' }, resubmit: '', sec_key: (body as any).evaluate === 1 ? 'fixture-secret' : '', content_modified: 0 };
    if (path.endsWith('log_output')) return { status: 0, data: { wss_url: 'wss://fixture.invalid' } };
    if (path.endsWith('game_build.json')) return { status: 1, port: 4312, had_done: true, res: { success: false } };
    if (path.endsWith('game_status.json')) {
      polls++;
      if (polls === 1) return { running_code_status: 1 };
      if (polls === 2) return NOT_MODIFIED;
      if (polls === 3) return { running_code_status: 2 };
      return { status: 2, had_done: true, test_sets_count: 1, sets_error_count: 0, test_sets: [{ result: true, compile_success: 1 }] };
    }
    throw new Error('unexpected route');
  } };
  const p = new ProgrammingClient(client(transport), { logs: { async run(url, id) {
    assert.equal(url, 'wss://fixture.invalid/log'); assert.equal(id, 40);
    return { stdout: 'YES\r\n', finished: true, closeCode: 1000 };
  } } });
  const context = await p.task('g', 10);
  assert.deepEqual(context.paths, ['src/中文.cpp', 'include/stack.h']);
  assert.equal(context.gameCount, 2); assert.equal(context.challengesCount, 12);
  const file = await p.readFile(context, context.paths[0]);
  assert.equal(file.filename, '中文.cpp'); assert.equal(file.extension, '.cpp'); assert.equal(file.fileType, 'txt'); assert.equal(file.currentCode, '/* 当前代码 */\n');
  assert.equal(file.provenance, 'recorded-current-workspace');
  await assert.rejects(() => p.readFile(context, 'foreign.cpp'), /不属于/);
  const saved0 = await p.save(context, file.path, 'void f() {}\n', 0);
  assert.equal(saved0.secKey, '');
  await assert.rejects(() => p.startEvaluation(context, saved0), /evaluate=1/);
  const self = await p.selfTest(context, '1\n', 'YES\n'); assert.equal(self.matchesExpected, true);
  const saved = await p.save(context, file.path, 'void f() {}\n', 1);
  await p.startEvaluation(context, saved);
  const result = await p.pollEvaluation(context, saved, { intervalMs: 0 });
  assert.equal(result.passed, true); assert.equal(result.pollCount, 4); assert.equal(result.notModifiedCount, 1);
  const save = calls.find(x => x.path.endsWith('update_file.json'))!;
  assert.equal(save.body.content, 'void f() {}\n'); assert.equal(save.body.exercise_id, null);
  assert.deepEqual(save.body.extras, { exercise_id: '', question_id: '', challenge_id: 30, subject_id: '', smart_plan_page_item_bank_id: '', homework_common_id: '10', competition_entry_id: '', currentUserId: 50 });
  const build = calls.find(x => x.path.endsWith('game_build.json'))!;
  assert.equal(build.body.extras.commitID, 'commit-fixture'); assert.equal(build.body.first, 1);
  assert.equal(calls.filter(x => x.path.endsWith('game_build.json')).length, 1);
  assert.deepEqual(calls.find(x => x.path.endsWith('game_status.json'))!.query, { resubmit: '', time_out: 'false', port: '4312', sec_key: 'fixture-secret', challenge_id: 30, subject_id: '', homework_common_id: '10', zzud: 'fixture-user' });
  assert.equal(normalizeLineEndings('x \r\n\n'), 'x \n\n');
});

test('polling is bounded and had_done alone never grants passage', async () => {
  let polls = 0, writes = 0;
  const p = new ProgrammingClient(client({ async request(method, path) {
    if (method === 'POST') writes++;
    if (path.endsWith('/g.json')) return task();
    if (path.endsWith('game_build.json')) return { status: 1, port: 0 };
    polls++; return { running_code_status: 1, had_done: true };
  } }));
  const context = await p.task('g', 10);
  const saved = { gameId: 20, evaluate: 1 as const, commitId: 'c', resubmit: '', secKey: 's', contentModified: 0 };
  await p.startEvaluation(context, saved);
  await assert.rejects(() => p.pollEvaluation(context, saved, { maxPolls: 2, intervalMs: 0 }), /上限/);
  assert.equal(polls, 2); assert.equal(writes, 1);
});

test('task ownership mismatches and empty/incomplete compile results never pass', async () => {
  for (const variant of [ { ...task(), homework_common_id: 11 }, { ...task(), game: { ...task().game, user_id: 51 } } ]) {
    const p = new ProgrammingClient(client({ request: async () => variant }));
    await assert.rejects(() => p.task('g', 10), /上下文标识不一致/);
  }
  for (const testSets of [[], [{ result: true }], [{ result: true, compile_success: 0 }]]) {
    const p = new ProgrammingClient(client({ async request(_method, path) {
      if (path.endsWith('/g.json')) return task();
      if (path.endsWith('game_build.json')) return { status: 1, port: 7 };
      return { status: 2, had_done: true, test_sets_count: 1, sets_error_count: 0, test_sets: testSets };
    } }));
    const context = await p.task('g', 10), saved = { gameId: 20, evaluate: 1 as const, commitId: 'c', resubmit: 'recorded-resubmit', secKey: 's', contentModified: 0 };
    await p.startEvaluation(context, saved);
    assert.equal((await p.pollEvaluation(context, saved, { intervalMs: 0 })).passed, false);
  }
});

test('task uses recorded user and editor environment rather than first environment/game fallback', async () => {
  const source: any = task();
  delete source.game.user_id;
  source.shixun_environments.unshift({ tab_type: 1, shixun_environment_id: 61 });
  source.challenge.path = ' src/中文.cpp；include/stack.h；src/中文.cpp； ';
  const c = client({ request: async () => source }); delete c.user!.id;
  const context = await new ProgrammingClient(c).task('g', 10);
  assert.equal(context.userId, 50); assert.equal(context.environmentId, 60);
  assert.deepEqual(context.paths, ['src/中文.cpp', 'include/stack.h']);
});

function entry(time: number, path: string, value: unknown, extra: Partial<HarEntry> = {}): HarEntry {
  return { startedDateTime: new Date(time).toISOString(), request: { method: 'GET', url: `https://data.educoder.net${path}` }, response: { status: 200, content: { text: JSON.stringify(value) } }, ...extra };
}
test('HAR orders requests, isolates API host, matches query/body and consumes 304 exactly once', async () => {
  const route = '/api/tasks/g/game_status.json';
  const t = new HarTransport([
    entry(3, `${route}?challenge_id=30`, { status: 2 }),
    entry(0, `${route}?challenge_id=30`, { poison: true }, { request: { method: 'GET', url: `https://pollution.invalid${route}?challenge_id=30` } }),
    entry(2, `${route}?challenge_id=30`, null, { response: { status: 304, content: {} } }),
    entry(1, `${route}?challenge_id=31`, { wrong: true }),
    entry(1, `${route}?challenge_id=30`, { running_code_status: 1 }),
    entry(4, '/api/myshixuns/m/update_file.json', { saved: true }, { request: { method: 'POST', url: 'https://data.educoder.net/api/myshixuns/m/update_file.json', postData: { text: JSON.stringify({ content: 'clear text', evaluate: 1 }) } } }),
  ]);
  assert.deepEqual(await t.request('GET', route, { challenge_id: 30 }), { running_code_status: 1 });
  assert.equal(isNotModified(await t.request('GET', route, { challenge_id: 30 })), true);
  assert.deepEqual(await t.request('GET', route, { challenge_id: 30 }), { status: 2 });
  await assert.rejects(() => t.request('GET', route, { challenge_id: 30 }), /未录制或已消费/);
  await assert.rejects(() => t.request('POST', '/api/myshixuns/m/update_file.json', {}, { content: 'different', evaluate: 1 }), /查询\/请求体/);
  assert.deepEqual(await t.request('POST', '/api/myshixuns/m/update_file.json', {}, { evaluate: 1, content: 'clear text' }), { saved: true });
});

test('legacy offline session uses an actual logged-in response and preserves recorded pagination', async () => {
  const t = new HarTransport([
    entry(0, '/api/users/get_user_info.json?school=1', null, { response: { status: 304, content: {} } }),
    entry(1, '/api/users/get_user_info.json?school=1', { login: 'fixture-user', user_id: 50 }),
    entry(2, '/api/users/fixture-user/courses.json?status=processing&page=1&per_page=15&sort_by=updated_at&sort_direction=desc&username=fixture-user&zzud=fixture-user', { count: 1, courses: [{ id: 'C1', name: 'Course' }] }),
  ]);
  const c = new EducoderClient(t, constants); c.user = t.restoreRecordedUser();
  assert.equal((await c.courses())[0].identifier, 'C1');
  const missingExec = new HarTransport([entry(0, '/api/shixuns/S/shixun_exec.json', null, { response: { status: 304, content: {} } })]);
  assert.equal(isNotModified(await missingExec.request('GET', '/api/shixuns/S/shixun_exec.json')), true);
});

test('HTTP business-state exception applies only to build/status routes and 304 remains bodyless', async () => {
  const responses = [new Response(JSON.stringify({ status: 1, res: { success: false } })), new Response(JSON.stringify({ status: 2 })), new Response(null, { status: 304 }), new Response(JSON.stringify({ status: 1, message: 'sensitive upstream text' }))];
  const t = new HttpTransport(constants, { fetcher: (async () => responses.shift()!) as typeof fetch });
  assert.deepEqual(await t.request('POST', '/api/tasks/g/game_build.json'), { status: 1, res: { success: false } });
  assert.deepEqual(await t.request('GET', '/api/tasks/g/game_status.json'), { status: 2 });
  assert.equal(isNotModified(await t.request('GET', '/api/tasks/g/game_status.json')), true);
  await assert.rejects(() => t.request('POST', '/api/accounts/login.json'), error => error instanceof Error && error.message.includes('status=1') && !error.message.includes('sensitive'));
});

class FakeSocket extends EventTarget {
  binaryType = ''; sent: string[] = []; closes: number[] = [];
  send(value: string) { this.sent.push(value); }
  close(code: number) { this.closes.push(code); }
  emit(value: string | ArrayBuffer) { this.dispatchEvent(new MessageEvent('message', { data: value })); }
}
test('WebSocket runner sends plain JSON, decodes Base64 text/binary once and closes at exact marker', async () => {
  const socket = new FakeSocket();
  const runner = new WebSocketLogRunner(() => socket as unknown as WebSocket);
  const result = runner.run('wss://fixture.invalid/log', 40);
  socket.dispatchEvent(new Event('open'));
  socket.emit(Buffer.from('prints ProcessFinished as output\r\n').toString('base64'));
  const frame = Buffer.from(Buffer.from('通过\n').toString('base64'));
  socket.emit(frame.buffer.slice(frame.byteOffset, frame.byteOffset + frame.byteLength));
  socket.emit(Buffer.from('ProcessFinished').toString('base64').replace(/=+$/, ''));
  assert.deepEqual(socket.sent, ['{"tp":"log","data":40}']);
  assert.deepEqual(await result, { stdout: 'prints ProcessFinished as output\r\n通过\n', finished: true, closeCode: 1000 });
  assert.deepEqual(socket.closes, [1000]);
});
test('WebSocket output cap and timeout fail without silently treating close as completion', async () => {
  const socket = new FakeSocket();
  const runner = new WebSocketLogRunner(() => socket as unknown as WebSocket);
  const over = runner.run('wss://fixture.invalid/log', 40, { maxOutputBytes: 2 });
  socket.emit(Buffer.from('large').toString('base64'));
  await assert.rejects(over, /上限/); assert.deepEqual(socket.closes, [1000]);
  const timeoutSocket = new FakeSocket();
  const timeout = new WebSocketLogRunner(() => timeoutSocket as unknown as WebSocket).run('wss://fixture.invalid/log', 40, { timeoutMs: 5 });
  await assert.rejects(timeout, /超时/);
});
test('HAR WebSocket validates context, exact output/marker and closure without external traffic', async () => {
  const recorded = entry(0, '/unused', null, { request: { method: 'GET', url: 'wss://fixture.invalid/log' }, _webSocketMessages: [
    { flow: 0, timestamp: 0, payload: { type: 2, text: '{"tp":"log","data":40}' } },
    { flow: 1, timestamp: 1, payload: { type: 2, text: Buffer.from('YQ==\r\n').toString('base64') } },
    { flow: 1, timestamp: 2, payload: { type: 2, text: Buffer.from('ProcessFinished').toString('base64') } },
    { flow: 0, timestamp: 3, payload: { type: 6, code: 1000 } },
  ] });
  const runner = new HarLogRunner([recorded]);
  assert.equal((await runner.run('wss://fixture.invalid/log', 40)).stdout, 'YQ==\r\n');
  await assert.rejects(() => runner.run('wss://fixture.invalid/log', 40), /已消费/);
  await assert.rejects(() => new HarLogRunner([recorded]).run('wss://fixture.invalid/log', 41), /不匹配/);
});

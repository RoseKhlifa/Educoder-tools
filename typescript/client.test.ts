import test from 'node:test';
import assert from 'node:assert/strict';
import { createDecipheriv } from 'node:crypto';
import { createServer } from 'node:http';
import { encryptPassword, HttpTransport, signRequest, type ProtocolConstants } from './protocol.ts';
import { EducoderClient } from './client.ts';

const constants: ProtocolConstants = {
  baseUrl: 'http://127.0.0.1', origin: 'https://www.educoder.net', referer: 'https://www.educoder.net/',
  ak: 'e9dd5b4322f9f7d83d009de9bfa100c3', sk: '2e3da06ae26ba9f76a5d8d355746f2fe',
  aesKey: '5183666c72eec9e4', aesIv: '5183666c72eec9e4', passwordOuterBase64: false,
};

test('signature and password encryption match protocol', () => {
  assert.equal(signRequest('GET', '1793348448103', constants), '48d0f98354bbf9387e51614db35b4cae');
  const encrypted = encryptPassword(' test-password ', constants);
  const decipher = createDecipheriv('aes-128-cbc', constants.aesKey, constants.aesIv);
  const clear = Buffer.concat([decipher.update(Buffer.from(encrypted, 'base64')), decipher.final()]).toString('utf8');
  assert.equal(clear, 'test-password');
  assert.equal(encrypted.length % 4, 0);
});

test('mock server supports login, cookie auth, pagination and enter', async t => {
  const requests: Array<{ path: string; cookie: string; auth: string; requestId: string }> = [];
  const server = createServer(async (req, res) => {
    const url = new URL(req.url ?? '/', 'http://127.0.0.1');
    const chunks: Buffer[] = []; for await (const c of req) chunks.push(Buffer.from(c));
    requests.push({ path: `${url.pathname}${url.search}`, cookie: req.headers.cookie ?? '', auth: req.headers['pc-authorization'] as string ?? '', requestId: req.headers['x-request-id'] as string ?? '' });
    res.setHeader('Content-Type', 'application/json');
    if (url.pathname === '/api/accounts/login.json') { res.setHeader('Set-Cookie', ['_educoder_session=sess; Path=/', 'foo=bar; Path=/']); return res.end(JSON.stringify({ login: 'alice' })); }
    if (url.pathname === '/api/users/get_user_info.json') return res.end(JSON.stringify({ login: 'alice', real_name: 'Alice', user_id: 7 }));
    if (url.pathname === '/api/users/alice/courses.json') {
      const page = Number(url.searchParams.get('page')); return res.end(JSON.stringify({ count: 2, courses: page === 1 ? [{ id: 'C1', name: 'One' }] : [{ id: 'C2', name: 'Two' }] }));
    }
    if (url.pathname === '/api/courses/C1/homework_commons.json') {
      const page = Number(url.searchParams.get('page')); return res.end(JSON.stringify({ query_total_count: 2, homeworks: page === 1 ? [{ homework_id: 10, name: 'Task A', shixun_identifier: 'S1', status: ['提交中'] }] : [{ homework_id: 11, name: 'Task B', shixun_identifier: 'S2', status: ['已截止'] }] }));
    }
    if (url.pathname === '/api/shixuns/S1/shixun_exec.json') return res.end(JSON.stringify({ game_identifier: 'G1' }));
    if (url.pathname === '/api/tasks/G1.json') return res.end(JSON.stringify({ status: 'required', game: { id: 3, status: 0 }, challenge: { id: 4, subject: 'A' }, shixun: { identifier: 'S1', name: 'S', challenges_count: 2 }, myshixun: { identifier: 'M1' } }));
    res.statusCode = 404; res.end('{}');
  });
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  const c = { ...constants, baseUrl: `http://127.0.0.1:${address.port}` };
  const client = new EducoderClient(new HttpTransport(c, { clock: () => 1700000000000 }), c, { pageSize: 1 });
  await client.login('alice', 'password');
  assert.equal(client.user?.name, 'Alice');
  const courses = await client.courses(); assert.deepEqual(courses.map(x => x.identifier), ['C1', 'C2']);
  const tasks = await client.homeworks('C1'); assert.deepEqual(tasks.map(x => x.id), [10, 11]);
  const entered = await client.enter('C1', 10); assert.equal(entered.gameIdentifier, 'G1'); assert.equal(entered.requiredStatus, 'required');
  const protectedRequests = requests.filter(x => !x.path.includes('login.json') && !x.path.includes('get_user_info'));
  assert.ok(protectedRequests.every(x => x.cookie.includes('_educoder_session=sess') && x.auth === 'sess'));
  assert.equal(requests.find(x => x.path.includes('login.json'))?.auth, 'null');
  assert.ok(requests.every(x => /^[0-9a-f-]{36}$/.test(x.requestId)));
});

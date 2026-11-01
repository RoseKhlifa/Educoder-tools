import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { CourseCacheClearedError, CourseDetailsCache } from './dist/course-cache.js';

const tick = () => new Promise(resolve => setImmediate(resolve));
const homework = name => [{ id: 1, name, status: 'required' }];
const exercise = name => [{ id: 2, name, status: 'open', submitted: false, scoreKnown: false, available: true }];

function harness(options) {
  const calls = [];
  let active = 0, maxActive = 0;
  const loader = kind => identifier => {
    let resolve, reject;
    const promise = new Promise((done, fail) => { resolve = done; reject = fail; });
    const call = { kind, identifier, settled: false, resolve: data => { call.settled = true; resolve(data); }, reject: error => { call.settled = true; reject(error); } };
    calls.push(call);
    active++;
    maxActive = Math.max(maxActive, active);
    return promise.then(data => { active--; return data; }, error => { active--; throw error; });
  };
  const cache = new CourseDetailsCache({ homeworks: loader('homeworks'), exercises: loader('exercises') }, options);
  return {
    cache, calls,
    get active() { return active; },
    get maxActive() { return maxActive; },
    keys: () => calls.map(call => `${call.kind}:${call.identifier}`),
    async finish() {
      for (let round = 0; round < 30; round++) {
        for (const call of calls.filter(call => !call.settled)) call.resolve(call.kind === 'homeworks' ? homework(call.identifier) : exercise(call.identifier));
        await tick();
        if (!active) { await tick(); if (!active) return; }
      }
      assert.fail('cache queue failed to drain');
    },
  };
}

await test('course details cache keeps course reads within the current browser session', async t => {
  await t.test('coalesces same-list reads and caches homework and exam data independently', async () => {
    const h = harness({ clock: () => 100 });
    const first = h.cache.getHomeworks('A');
    assert.equal(h.cache.getHomeworks('A'), first);
    const exams = h.cache.getExercises('A');
    await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A']);
    const tasks = homework('Course A task'), papers = exercise('Course A exam');
    h.calls[0].resolve(tasks); h.calls[1].resolve(papers);
    assert.equal(await first, tasks); assert.equal(await exams, papers);
    await tick();
    assert.deepEqual(h.cache.peekHomeworks('A'), { data: tasks, updatedAt: 100, stale: false });
    assert.deepEqual(h.cache.peekExercises('A'), { data: papers, updatedAt: 100, stale: false });
    assert.equal(await h.cache.getHomeworks('A'), tasks);
    assert.equal(h.calls.length, 2);
  });

  await t.test('prefetches progressively with at most two background reads and deduplicated courses', async () => {
    const h = harness();
    h.cache.prefetch(['A', 'B', 'A', '', 'C']);
    await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A']);
    h.calls[0].resolve(homework('A'));
    await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A', 'homeworks:B']);
    assert.equal(h.active, 2);
    await h.finish();
    assert.equal(h.maxActive, 2);
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A', 'homeworks:B', 'exercises:B', 'homeworks:C', 'exercises:C']);
    h.cache.prefetch(['A', 'B', 'C']);
    await tick(); assert.equal(h.calls.length, 6);
  });

  await t.test('promotes a clicked queued list into the reserved foreground slot and prioritizes later clicks', async () => {
    const h = harness();
    h.cache.prefetch(['A', 'B', 'C']);
    await tick();
    const selectedExam = h.cache.getExercises('C');
    assert.equal(h.cache.getExercises('C', true), selectedExam);
    await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A', 'exercises:C']);
    assert.equal(h.active, 3);
    const nextClick = h.cache.getHomeworks('X');
    await tick(); assert.equal(h.calls.length, 3);
    h.calls[0].resolve(homework('A'));
    await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A', 'exercises:C', 'homeworks:X']);
    await h.finish();
    assert.equal(h.maxActive, 3);
    assert.equal(h.keys().filter(key => key === 'exercises:C').length, 1);
    assert.equal((await selectedExam)[0].name, 'C');
    assert.equal((await nextClick)[0].name, 'X');
  });

  await t.test('clicking a running prefetch uses that exact read without a duplicate request', async () => {
    const h = harness();
    h.cache.prefetch(['A', 'B']);
    await tick();
    const selected = h.cache.getHomeworks('A');
    await tick(); assert.equal(h.calls.length, 2);
    const rows = homework('A');
    h.calls[0].resolve(rows);
    assert.equal(await selected, rows);
    await h.finish();
    assert.equal(h.keys().filter(key => key === 'homeworks:A').length, 1);
  });

  await t.test('expiration keeps usable data visible, refetches at the TTL boundary and supports forced refresh', async () => {
    let now = 100;
    const h = harness({ clock: () => now, ttlMs: 20 });
    const initial = h.cache.getHomeworks('A');
    await tick(); h.calls[0].resolve(homework('before')); await initial; await tick();
    now = 119;
    assert.equal(h.cache.peekHomeworks('A').stale, false);
    await h.cache.getHomeworks('A'); assert.equal(h.calls.length, 1);
    now = 120;
    assert.equal(h.cache.peekHomeworks('A').stale, true);
    const refresh = h.cache.getHomeworks('A');
    await tick(); assert.equal(h.calls.length, 2);
    assert.equal(h.cache.peekHomeworks('A').data[0].name, 'before');
    h.calls[1].resolve(homework('after')); await refresh; await tick();
    assert.deepEqual(h.cache.peekHomeworks('A'), { data: homework('after'), updatedAt: 120, stale: false });
    const forced = h.cache.getHomeworks('A', true);
    assert.equal(h.cache.getHomeworks('A', true), forced);
    await tick(); assert.equal(h.calls.length, 3);
    h.calls[2].resolve(homework('forced')); await forced; await tick();
    assert.equal(h.cache.peekHomeworks('A').data[0].name, 'forced');
  });

  await t.test('refresh errors preserve previous data and timestamp while failed first reads stay absent', async () => {
    let now = 100;
    const h = harness({ clock: () => now, ttlMs: 20 });
    const initial = h.cache.getExercises('A');
    await tick(); h.calls[0].resolve(exercise('saved')); await initial; await tick();
    now = 110;
    const forced = h.cache.getExercises('A', true), rejected = assert.rejects(forced, /upstream unavailable/);
    await tick(); h.calls[1].reject(new Error('upstream unavailable')); await rejected; await tick();
    assert.deepEqual(h.cache.peekExercises('A'), { data: exercise('saved'), updatedAt: 100, stale: false });
    now = 120;
    const expired = h.cache.getExercises('A'), expiredRejected = assert.rejects(expired, /still unavailable/);
    await tick(); h.calls[2].reject(new Error('still unavailable')); await expiredRejected; await tick();
    assert.deepEqual(h.cache.peekExercises('A'), { data: exercise('saved'), updatedAt: 100, stale: true });
    const missing = h.cache.getHomeworks('B'), missingRejected = assert.rejects(missing, /no response/);
    await tick(); h.calls[3].reject(new Error('no response')); await missingRejected; await tick();
    assert.equal(h.cache.peekHomeworks('B'), undefined);
  });

  await t.test('successful empty lists are cached without retrying them as failures', async () => {
    const h = harness();
    const initial = h.cache.getHomeworks('A');
    await tick(); h.calls[0].resolve([]); assert.deepEqual(await initial, []); await tick();
    assert.deepEqual(h.cache.peekHomeworks('A').data, []);
    assert.deepEqual(await h.cache.getHomeworks('A'), []);
    assert.equal(h.calls.length, 1);
  });

  await t.test('a forced refresh immediately after awaiting a read starts a new request', async () => {
    const h = harness();
    const initial = h.cache.getHomeworks('A');
    await tick(); h.calls[0].resolve(homework('before')); await initial;
    const refresh = h.cache.getHomeworks('A', true);
    assert.notEqual(refresh, initial);
    await tick(); assert.equal(h.calls.length, 2);
    h.calls[1].resolve(homework('after'));
    assert.equal((await refresh)[0].name, 'after');
  });

  await t.test('default lifetime expires after two minutes and simultaneous clicked lists can run together', async () => {
    let now = 100;
    const h = harness({ clock: () => now });
    const tasks = h.cache.getHomeworks('A'), exams = h.cache.getExercises('A');
    await tick(); assert.equal(h.active, 2);
    h.calls[0].resolve([]); h.calls[1].resolve([]); await Promise.all([tasks, exams]);
    now = 120_099; assert.equal(h.cache.peekHomeworks('A').stale, false);
    now = 120_100; assert.equal(h.cache.peekHomeworks('A').stale, true);
  });

  await t.test('background rejections are handled, leave no false empty cache and permit retry', async () => {
    const h = harness();
    h.cache.prefetch(['A']); await tick();
    h.calls[0].reject(new Error('read failed')); h.calls[1].resolve([]); await tick();
    assert.equal(h.cache.peekHomeworks('A'), undefined);
    assert.deepEqual(h.cache.peekExercises('A').data, []);
    h.cache.prefetch(['A']); await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A', 'homeworks:A']);
    await h.finish();
  });

  await t.test('clear rejects queued and running callers, drops old queues and ignores late old responses', async () => {
    const h = harness({ clock: () => 500 });
    h.cache.prefetch(['A', 'OLD_QUEUE']); await tick();
    const oldRunning = h.cache.getHomeworks('A');
    const runningRejected = assert.rejects(oldRunning, CourseCacheClearedError);
    const oldForeground = h.cache.getExercises('OLD_FOREGROUND');
    const foregroundRejected = assert.rejects(oldForeground, CourseCacheClearedError);
    const oldQueued = h.cache.getHomeworks('OLD_QUEUED');
    const queuedRejected = assert.rejects(oldQueued, CourseCacheClearedError);
    await tick(); assert.equal(h.calls.length, 3);
    h.cache.clear();
    await Promise.all([runningRejected, foregroundRejected, queuedRejected]);
    assert.equal(h.cache.peekHomeworks('A'), undefined);
    const current = h.cache.getHomeworks('A');
    h.cache.prefetch(['NEW_SESSION']); await tick();
    // Old transports cannot occupy the new session's independent scheduler.
    assert.deepEqual(h.keys().slice(3), ['homeworks:A', 'homeworks:NEW_SESSION', 'exercises:NEW_SESSION']);
    for (const call of h.calls.slice(0, 3)) call.resolve(call.kind === 'homeworks' ? homework('private old session') : exercise('private old session'));
    await tick();
    assert.equal(h.cache.peekHomeworks('A'), undefined);
    assert.equal(h.cache.peekExercises('OLD_FOREGROUND'), undefined);
    h.calls[3].resolve(homework('current session')); await current; await tick();
    assert.equal(h.cache.peekHomeworks('A').data[0].name, 'current session');
    await h.finish();
    assert.equal(h.keys().some(key => key.includes('OLD_QUEUE')), false);
    assert.equal(h.keys().some(key => key.includes('OLD_QUEUED')), false);
    assert.equal(h.cache.peekHomeworks('A').data[0].name, 'current session');
  });

  await t.test('clear also erases previously settled cache entries before another account uses the same identifier', async () => {
    const h = harness();
    const initial = h.cache.getHomeworks('A'); await tick();
    h.calls[0].resolve(homework('first account')); await initial; await tick();
    h.cache.clear(); assert.equal(h.cache.peekHomeworks('A'), undefined);
    const second = h.cache.getHomeworks('A'); await tick();
    assert.equal(h.calls.length, 2);
    h.calls[1].resolve(homework('second account')); assert.equal((await second)[0].name, 'second account');
  });

  await t.test('an immediate session change never launches even the reserved old-session transport', async () => {
    const h = harness();
    h.cache.prefetch(['OLD', 'OLD_QUEUED']);
    h.cache.clear(); await tick();
    assert.deepEqual(h.keys(), []);
    h.cache.prefetch(['NEW']); await tick();
    assert.deepEqual(h.keys(), ['homeworks:NEW', 'exercises:NEW']);
    await h.finish();
  });

  await t.test('synchronous loader failures reject normally without poisoning the scheduler', async () => {
    let count = 0;
    const cache = new CourseDetailsCache({
      homeworks: () => { if (!count++) throw new Error('sync failure'); return Promise.resolve([]); },
      exercises: () => Promise.resolve([]),
    });
    await assert.rejects(cache.getHomeworks('A'), /sync failure/); await tick();
    assert.equal(cache.peekHomeworks('A'), undefined);
    assert.deepEqual(await cache.getHomeworks('A'), []);
  });

  await t.test('cache owns no persistent storage, transport, mutation or diagnostic logging', async () => {
    const source = await readFile(new URL('./src/course-cache.ts', import.meta.url), 'utf8');
    assert.doesNotMatch(source, /localStorage|sessionStorage|indexedDB|document\.cookie|console\.(?:log|debug|info|warn|error)/);
    assert.doesNotMatch(source, /\bfetch\s*\(|XMLHttpRequest|WebSocket|\bPOST\b|\/api\//);
    assert.match(source, /import type \{ Exercise, Homework \}/);
  });
});

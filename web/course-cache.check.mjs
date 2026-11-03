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
    assert.deepEqual(h.cache.peekHomeworks('A'), { data: tasks, requestedAt: 100, updatedAt: 100, stale: false });
    assert.deepEqual(h.cache.peekExercises('A'), { data: papers, requestedAt: 100, updatedAt: 100, stale: false });
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

  await t.test('homework force refresh after a background read is coalesced once and emits cache state changes', async () => {
    const events = [], h = harness({ onChange: event => events.push(event) });
    h.cache.prefetch(['A']); await tick();
    const forced = h.cache.getHomeworks('A', true); assert.equal(h.cache.getHomeworks('A', true), forced);
    assert.equal(h.calls.filter(call => call.kind === 'homeworks').length, 1); const first = h.calls.find(call => call.kind === 'homeworks'); first.resolve(homework('initial')); await tick();
    assert.equal(h.calls.filter(call => call.kind === 'homeworks').length, 2); const second = h.calls.filter(call => call.kind === 'homeworks')[1]; second.resolve(homework('refreshed')); await forced;
    assert.deepEqual(h.cache.peekHomeworks('A').data, homework('refreshed'));
    const homeworkEvents = events.filter(event => event.kind === 'homeworks');
    assert.deepEqual(homeworkEvents.map(event => event.state), ['loading', 'success', 'loading', 'success']);
    assert.equal(homeworkEvents.filter(event => event.state === 'loading').length, 2);
  });
  await t.test('clearing a prefetched forced refresh never starts its follow-up in the next session', async () => {
    const h = harness();
    h.cache.prefetch(['A']); await tick();
    const forced = h.cache.getHomeworks('A', true);
    const rejected = assert.rejects(forced, CourseCacheClearedError);
    h.cache.clear(); await rejected; await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A']);
    await h.finish();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A']);
    assert.equal(h.cache.peekHomeworks('A'), undefined);
    const current = h.cache.getHomeworks('NEW'); await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A', 'homeworks:NEW']);
    h.calls[2].resolve(homework('new session')); await current;
  });
  await t.test('a settled old read cannot enqueue a forced follow-up after the session clears', async () => {
    let clearOnSuccess = false;
    const h = harness({ onChange: event => {
      if (clearOnSuccess && event.kind === 'homeworks' && event.state === 'success') queueMicrotask(() => h.cache.clear());
    } });
    h.cache.prefetch(['A']); await tick();
    const forced = h.cache.getHomeworks('A', true), rejected = assert.rejects(forced, CourseCacheClearedError);
    clearOnSuccess = true; h.calls[0].resolve(homework('old settled result')); await rejected; await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A']);
    await h.finish(); assert.equal(h.cache.peekHomeworks('A'), undefined);
  });
  await t.test('terminal revalidation follows an already forced read while duplicate terminal events coalesce', async () => {
    let now = 100;
    const h = harness({ clock: () => now });
    const manual = h.cache.getHomeworks('A', true); await tick();
    assert.equal(h.cache.getHomeworks('A', true), manual);
    now = 200;
    const terminal = h.cache.refreshHomeworksAfterPending('A');
    assert.notEqual(terminal, manual);
    assert.equal(h.cache.refreshHomeworksAfterPending('A'), terminal);
    now = 300;
    h.calls[0].resolve(homework('started before terminal status')); await manual; await tick();
    assert.equal(h.cache.peekHomeworks('A').requestedAt, 100);
    assert.equal(h.cache.peekHomeworks('A').updatedAt, 300);
    assert.deepEqual(h.keys(), ['homeworks:A', 'homeworks:A']);
    now = 400;
    h.calls[1].resolve(homework('after terminal status')); await terminal;
    assert.equal(h.cache.peekHomeworks('A').data[0].name, 'after terminal status');
    assert.equal(h.cache.peekHomeworks('A').requestedAt, 300);
    assert.equal(h.cache.peekHomeworks('A').updatedAt, 400);
  });

  await t.test('visible homework revalidation refreshes expired entries without exams and keeps foreground capacity', async () => {
    let now = 100;
    const h = harness({ clock: () => now, ttlMs: 20 });
    h.cache.revalidateHomeworks(['A', 'A', '', 'B', 'C']);
    h.cache.revalidateHomeworks(['A', 'B', 'C']);
    await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'homeworks:B']);
    const clicked = h.cache.getHomeworks('D'); await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'homeworks:B', 'homeworks:D']);
    assert.equal(h.active, 3);
    now = 110;
    h.calls[0].resolve(homework('A')); await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'homeworks:B', 'homeworks:D', 'homeworks:C']);
    assert.equal(h.cache.peekHomeworks('A').requestedAt, 100);
    assert.equal(h.cache.peekHomeworks('A').updatedAt, 110);
    now = 120;
    await h.finish(); await clicked;
    assert.equal(h.cache.peekHomeworks('C').requestedAt, 110, 'queued reads record actual loader dispatch');
    assert.equal(h.cache.peekHomeworks('C').updatedAt, 120);
    now = 129;
    h.cache.revalidateHomeworks(['A', 'B', 'C', 'D']); await tick();
    assert.equal(h.calls.length, 4, 'fresh data does not refetch on visible revalidation');
    now = 130;
    h.cache.revalidateHomeworks(['A', 'A']); await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'homeworks:B', 'homeworks:D', 'homeworks:C', 'homeworks:A']);
    assert.equal(h.cache.peekHomeworks('A').data[0].name, 'A', 'stale data remains visible');
    h.cache.revalidateHomeworks(['A']); await tick(); assert.equal(h.calls.length, 5);
    await h.finish();
    h.cache.revalidateHomeworks(['A']); await tick(); assert.equal(h.calls.length, 5, 'successful refresh does not spin');
    assert.ok(h.calls.every(call => call.kind === 'homeworks'));
  });

  await t.test('background failures back off independently per key and cap retries at five minutes', async () => {
    let now = 100;
    const h = harness({ clock: () => now });
    h.cache.revalidateHomeworks(['A']); await tick();
    for (const cooldown of [30_000, 60_000, 120_000, 240_000, 300_000, 300_000]) {
      const callCount = h.calls.length;
      h.calls.at(-1).reject(new Error('upstream unavailable')); await tick();
      assert.equal(h.cache.peekHomeworks('A'), undefined);
      now += cooldown - 1;
      h.cache.revalidateHomeworks(['A', 'A']); await tick();
      assert.equal(h.calls.length, callCount, 'polling waits until the cooldown ends');
      now++;
      h.cache.revalidateHomeworks(['A', 'A']); await tick();
      assert.equal(h.calls.length, callCount + 1);
    }
    h.calls.at(-1).reject(new Error('still unavailable')); await tick();
    h.cache.revalidateHomeworks(['A', 'B']); await tick();
    assert.equal(h.calls.at(-1).identifier, 'B', 'one failed course does not block another');
    await h.finish();
  });

  await t.test('foreground and force reads bypass background cooldown and success resets backoff', async () => {
    let now = 100;
    const h = harness({ clock: () => now, ttlMs: 1 });
    h.cache.revalidateHomeworks(['A']); await tick();
    h.calls[0].reject(new Error('background failure')); await tick();
    const foreground = h.cache.getHomeworks('A'), rejected = assert.rejects(foreground, /foreground failure/);
    await tick(); assert.equal(h.calls.length, 2);
    h.calls[1].reject(new Error('foreground failure')); await rejected; await tick();
    h.cache.revalidateHomeworks(['A']); await tick(); assert.equal(h.calls.length, 2);
    const forced = h.cache.getHomeworks('A', true); await tick();
    h.calls[2].resolve(homework('recovered')); await forced; await tick();
    now++;
    h.cache.revalidateHomeworks(['A']); await tick(); assert.equal(h.calls.length, 4);
    h.calls[3].reject(new Error('new failure')); await tick();
    assert.equal(h.cache.peekHomeworks('A').data[0].name, 'recovered');
    now += 29_999;
    h.cache.revalidateHomeworks(['A']); await tick(); assert.equal(h.calls.length, 4);
    now++;
    h.cache.revalidateHomeworks(['A']); await tick(); assert.equal(h.calls.length, 5, 'success resets cooldown to the initial thirty seconds');
    await h.finish();
  });

  await t.test('background retry cooldown is erased when the browser session changes', async () => {
    const h = harness({ clock: () => 100 });
    h.cache.revalidateHomeworks(['A']); await tick();
    h.calls[0].reject(new Error('old account failure')); await tick();
    h.cache.revalidateHomeworks(['A']); await tick(); assert.equal(h.calls.length, 1);
    const oldRead = h.cache.getHomeworks('A', true), rejected = assert.rejects(oldRead, CourseCacheClearedError);
    await tick(); h.cache.clear(); await rejected;
    h.cache.revalidateHomeworks(['A']); await tick(); assert.equal(h.calls.length, 3);
    h.calls[1].reject(new Error('late old account failure')); await tick();
    h.calls[2].resolve(homework('new account')); await tick();
    assert.equal(h.cache.peekHomeworks('A').data[0].name, 'new account');
    h.cache.clear(); h.cache.revalidateHomeworks(['A']); await tick();
    assert.equal(h.calls.length, 4);
    await h.finish();
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
    assert.deepEqual(h.cache.peekHomeworks('A'), { data: homework('after'), requestedAt: 120, updatedAt: 120, stale: false });
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
    assert.deepEqual(h.cache.peekExercises('A'), { data: exercise('saved'), requestedAt: 100, updatedAt: 100, stale: false });
    now = 120;
    const expired = h.cache.getExercises('A'), expiredRejected = assert.rejects(expired, /still unavailable/);
    await tick(); h.calls[2].reject(new Error('still unavailable')); await expiredRejected; await tick();
    assert.deepEqual(h.cache.peekExercises('A'), { data: exercise('saved'), requestedAt: 100, updatedAt: 100, stale: true });
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

  await t.test('background rejections are handled, leave no false empty cache and permit retry after cooldown', async () => {
    let now = 100;
    const h = harness({ clock: () => now });
    h.cache.prefetch(['A']); await tick();
    h.calls[0].reject(new Error('read failed')); h.calls[1].resolve([]); await tick();
    assert.equal(h.cache.peekHomeworks('A'), undefined);
    assert.deepEqual(h.cache.peekExercises('A').data, []);
    h.cache.prefetch(['A']); await tick();
    assert.deepEqual(h.keys(), ['homeworks:A', 'exercises:A']);
    now += 30_000;
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

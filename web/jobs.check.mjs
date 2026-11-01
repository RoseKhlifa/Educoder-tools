import assert from 'node:assert/strict';
import { test } from 'node:test';
import { currentServiceJobs, partitionServiceJobs, renderJobs, renderServiceDetail, renderServiceJob } from './dist/jobs.js';

const job = (id, day, extra = {}) => ({ id, status: 'failed', phase: 'completed', kind: 'answer', request: { mode: 'submit', scope: 'course', course: 'COURSE_A' }, created_at: `2026-01-${String(day).padStart(2, '0')}T00:00:00Z`, updated_at: `2026-01-${String(day).padStart(2, '0')}T00:00:00Z`, ...extra });
const ids = rows => rows.map(row => row.id);
const courses = [{ identifier: 'COURSE_A', name: '示例数据结构课程' }, { identifier: 'COURSE_B', name: '示例系统课程' }];
const visible = html => html.replace(/<details\b[\s\S]*?<\/details>/g, '');

await test('consumer completion progress shows one current arrangement per original target', async t => {
  await t.test('new completion supersedes old failed and successful attempts by creation time without mutating state', () => {
    const old = job('older-error', 1, { updated_at: '2026-03-01T00:00:00Z' });
    const completed = job('older-completed', 2, { status: 'succeeded' });
    const latest = job('latest-completed', 3, { status: 'succeeded', result: { total_homeworks: 4, completed_homeworks: 4 } });
    const rows = [old, latest, completed], before = JSON.stringify(rows), parts = partitionServiceJobs(rows);
    assert.deepEqual(ids(parts.current), ['latest-completed']); assert.deepEqual(ids(parts.history), ['older-completed', 'older-error']);
    assert.equal(JSON.stringify(rows), before); assert.deepEqual(currentServiceJobs(rows), parts.current);
    const html = renderJobs(rows, courses), shown = visible(html);
    assert.match(shown, /data-job-detail="latest-completed"/); assert.doesNotMatch(shown, /older-error|older-completed|服务人员/);
    assert.match(shown, /已完成 <b>1<\/b>/); assert.match(shown, /4 \/ 4 份作业/);
    assert.match(html, /<details class="service-history" data-service-history>/); assert.match(html, /历史记录 <span class="count">2/);
  });
  await t.test('every genuinely queued or running request remains visible even if a newer terminal request exists', () => {
    const rows = [job('failed-before', 1), job('running', 2, { status: 'running', result: { service_status: 'completed' } }), job('waiting', 3, { status: 'waiting' }), job('queued', 4, { status: 'queued' }), job('processing', 5, { status: 'processing' }), job('newer-ended', 6, { status: 'succeeded' })];
    const parts = partitionServiceJobs(rows);
    assert.deepEqual(ids(parts.current), ['processing', 'queued', 'waiting', 'running']);
    assert.deepEqual(ids(parts.history), ['newer-ended', 'failed-before']);
  });
  await t.test('original scope, course, homework, game and execution mode stay distinct despite changing result metadata', () => {
    const requests = [
      { scope: 'all', mode: 'submit' },
      { scope: 'course', course: 'COURSE_A', mode: 'submit' },
      { scope: 'course', course: 'COURSE_B', mode: 'submit' },
      { scope: 'homework', course: 'COURSE_A', homework_id: 10, mode: 'submit' },
      { scope: 'homework', course: 'COURSE_A', homework_id: 20, mode: 'submit' },
      { scope: 'current', course: 'COURSE_A', homework_id: 10, game: 'GAME_A', mode: 'submit' },
      { scope: 'current', course: 'COURSE_A', homework_id: 10, game: 'GAME_B', mode: 'submit' },
      { scope: 'current', course: 'COURSE_A', homework_id: 10, game: 'GAME_A', mode: 'selftest' },
      { scope: 'current', course: 'COURSE_A', homework_id: 10, game: 'GAME_A', mode: 'generate_only' },
    ];
    const rows = requests.map((request, index) => job(`target-${index}`, index + 1, { request }));
    assert.equal(partitionServiceJobs(rows).current.length, requests.length);
    rows.push(job('latest-course', 20, { request: requests[1], result: { course: 'COURSE_B', homework_id: 999, current_title: 'Unrelated progress' } }));
    assert.deepEqual(ids(partitionServiceJobs(rows).history), ['target-1']);
  });
  await t.test('latest unresolved, unknown status and incomplete target records never disappear', () => {
    const rows = [job('old', 1), job('latest-unresolved', 2), job('future-state', 3, { status: 'future_unknown' }), job('no-target-a', 4, { request: { mode: 'submit', scope: 'homework' } }), job('no-target-b', 5, { request: { mode: 'submit', scope: 'homework' } }), job('missing-scope', 6, { request: { mode: 'submit', course: 'COURSE_A' } })];
    const parts = partitionServiceJobs(rows);
    assert.deepEqual(ids(parts.current), ['missing-scope', 'no-target-b', 'no-target-a', 'future-state']);
    assert.deepEqual(ids(parts.history), ['latest-unresolved', 'old']);
    assert.deepEqual(ids(partitionServiceJobs(rows.slice(0, 2)).current), ['latest-unresolved']);
    assert.equal(partitionServiceJobs([job('bad-date-a', 1, { created_at: 'invalid', request: { mode: 'submit' } }), job('bad-date-b', 1, { created_at: 'invalid', request: { mode: 'submit' } })]).current.length, 2);
  });
  await t.test('collections are excluded without changing source data or calling an API', () => {
    const collection = job('collection', 3, { request: { mode: 'collect_verified' }, result: { collected: 100 } });
    assert.deepEqual(partitionServiceJobs([collection]), { current: [], history: [] });
    assert.doesNotMatch(renderJobs([collection], courses), /collection|100|采集|题库/);
  });
  await t.test('history uses past-tense status and details without claiming failed work is still active', () => {
    const old = job('old', 1, { status: 'support', result: { homeworks: [{ title: '作业一', status: 'support' }, { title: '作业二', status: 'completed' }] } });
    const html = renderServiceJob(old, courses, true) + renderServiceDetail(old, courses, true);
    assert.match(html, /当次未完成/); assert.match(html, /当次已完成/); assert.match(html, /当次进度/); assert.match(html, /data-job-history="true"/);
    assert.doesNotMatch(html, /服务人员正在处理|服务处理中|正在逐项完成|会在处理后更新|关闭页面也会继续/);
    assert.match(renderServiceDetail(old, courses), /放心，服务人员正在处理，无需重复提交/);
  });
  await t.test('history expansion is a controllable native disclosure and current cards stay compact', () => {
    const rows = [job('old', 1), job('latest', 2, { status: 'succeeded' })];
    assert.match(renderJobs(rows, courses, true), /data-service-history open>/);
    assert.doesNotMatch(renderJobs(rows, courses), /data-service-history open>/);
    assert.match(renderServiceJob(rows[1], courses), /service-job-compact/);
    assert.doesNotMatch(renderServiceJob(rows[1], courses), /job-note|放心|结果已核验/);
  });
  await t.test('titles and identifiers are escaped and private execution fields never enter consumer cards or details', () => {
    const sample = job('evil"><img src=x onerror=alert(1)>', 1, { request: { scope: 'homework', mode: 'submit', course: 'COURSE_A', homework_id: 10 }, error_code: 'PRIVATE_CODE', message: 'PRIVATE_TRACE', result: { title: '<script>BAD</script>', model_calls: 42, cost_micro_usd: 999, diagnostics: ['PRIVATE_TRACE'], homeworks: [{ title: '<img src=x onerror=alert(1)>', status: 'failed', reason_code: 'PRIVATE_REASON' }] } });
    const html = renderJobs([sample], courses) + renderServiceDetail(sample, courses, true);
    assert.match(html, /&lt;script&gt;BAD&lt;\/script&gt;/); assert.match(html, /&lt;img/);
    assert.doesNotMatch(html, /<script|<img|PRIVATE_CODE|PRIVATE_TRACE|PRIVATE_REASON|999|42 次/);
  });
});

import test from 'node:test';
import assert from 'node:assert/strict';
import { courseRecordIsHistorical, courseTerminalVersion, historicalCourseRecordIds, summarizeCourseWork } from './dist/course-status.js';
import { partitionServiceJobs, renderJobs } from './dist/jobs.js';

const finished = '2026-11-02T12:00:00Z', read = Date.parse('2026-11-03T12:00:00Z');
const full = { id: 1, name: 'required', requiredProgressKnown: true, requiredChallengeCount: 1, finishedRequiredChallengeCount: 1 };
const job = (extra = {}) => ({ id: 'old', status: 'failed', phase: 'completed', created_at: finished, updated_at: finished,
  request: { scope: 'course', course: 'C', mode: 'submit' }, result: { service_status: 'support', total_homeworks: 10, completed_homeworks: 6 }, ...extra });
const proofs = (data = [full], extra = {}, source = job()) => new Map([['C', { data, requestedAt: read, updatedAt: read + 100, stale: false, sync: 'ready', observedTerminalVersions: new Set([courseTerminalVersion(source)]), ...extra }]]);

test('current course evidence archives an old attempt without changing its recorded result', () => {
  const source = job({ status: 'uncertain', can_recover: true });
  const original = JSON.stringify(source), ids = historicalCourseRecordIds([source], proofs([full], {}, source));
  assert.deepEqual([...ids], ['old']);
  const parts = partitionServiceJobs([source], ids);
  assert.equal(parts.current.length, 0); assert.equal(parts.history[0], source);
  const html = renderJobs([source], [], true, new Map(), ids);
  assert.match(html, /6 \/ 10 份作业/); assert.match(html, /当次未完成/);
  assert.doesNotMatch(html, /data-job-recover=|data-job-retry=/);
  assert.equal(JSON.stringify(source), original);
});

test('empty actionable catalog proves no current work, not historic grade completion', () => {
  const source = job();
  assert.equal(courseRecordIsHistorical(source, proofs([])), true);
  assert.deepEqual(summarizeCourseWork([]), { pending: 0, shixunPending: 0, commonPending: 0, commonSubmitted: 0, commonTotal: 0, hasRequired: false });
  assert.deepEqual(summarizeCourseWork([full]), { pending: 0, shixunPending: 0, commonPending: 0, commonSubmitted: 0, commonTotal: 0, hasRequired: true });
  assert.equal(summarizeCourseWork([{ id: 3968749, name: '图文作业', homeworkKind: 'common', submissionStatus: 'submitted' }]).pending, 0);
  assert.equal(summarizeCourseWork([{ id: 3968749, name: '图文作业', homeworkKind: 'common', submissionStatus: 'not_submitted' }]).pending, 1);
  assert.equal(summarizeCourseWork([{ id: 3968749, name: '图文作业', homeworkKind: 'common', submissionStatus: 'unknown' }]).pending, 1);
  assert.equal(source.status, 'failed'); assert.equal(source.result.completed_homeworks, 6);
});

test('stale or failed refresh retains history classification but cannot authorize scheduling', () => {
  for (const extra of [{ stale: true }, { sync: 'loading' }, { sync: 'error' }]) {
    const source = job();
    assert.equal(courseRecordIsHistorical(source, proofs([full], extra)), true);
    assert.equal(courseRecordIsHistorical(source, proofs([full], extra), true), false);
  }
});

test('active jobs, exams, missing facts, unknown progress and newer terminal outcomes remain current', () => {
  const source = job();
  assert.equal(courseRecordIsHistorical(job({ status: 'running' }), proofs()), false);
  assert.equal(courseRecordIsHistorical(job({ request: { scope: 'exercise', course: 'C', mode: 'submit', exercise_id: 1 } }), proofs()), false);
  assert.equal(courseRecordIsHistorical(source, new Map()), false);
  assert.equal(courseRecordIsHistorical(source, proofs([{ ...full, requiredProgressKnown: false }])), false);
  assert.equal(courseRecordIsHistorical(source, proofs([{ ...full, finishedRequiredChallengeCount: 2 }])), false);
  assert.equal(courseRecordIsHistorical(source, proofs([full], { requestedAt: Date.parse(finished) - 1 })), false);
  assert.equal(courseRecordIsHistorical(job({ updated_at: '2026-11-04T12:00:00Z' }), proofs()), false);
});

test('recorded target coverage respects homework, course and all-course boundaries', () => {
  const explicit = job({ result: { total_homeworks: 1, homeworks: [{ course: 'C', homework_id: 1 }] } });
  const extraWork = proofs([full, { ...full, id: 2, finishedRequiredChallengeCount: 0 }]);
  assert.equal(courseRecordIsHistorical(explicit, extraWork), true, 'new unrelated homework does not reactivate old plan');
  assert.equal(courseRecordIsHistorical(job(), extraWork), false, 'unrecorded scope requires the whole course');
  assert.equal(courseRecordIsHistorical(job({ request: { scope: 'homework', course: 'C', homework_id: 1, mode: 'submit' } }), extraWork), true);
  assert.equal(courseRecordIsHistorical(job({ request: { scope: 'homework', course: 'C', homework_id: 0, mode: 'submit' } }), extraWork), false);
  const all = job({ request: { scope: 'all', mode: 'submit' } });
  assert.equal(courseRecordIsHistorical(all, proofs([])), false, 'one course never covers an unrecorded all-course request');
  assert.equal(courseRecordIsHistorical({ ...all, result: { homeworks: [{ course: 'C', homework_id: 1 }, { course: 'OTHER', homework_id: 2 }] } }, proofs()), false);
  assert.equal(courseRecordIsHistorical({ ...all, result: explicit.result }, proofs()), true);
});

test('redacted consumer summaries use the whole course, never incomplete all-course coverage', () => {
  const source = job({ result: { total_homeworks: 10, completed_homeworks: 6, homeworks: [{ title: '作业', status: 'support' }] } });
  assert.equal(courseRecordIsHistorical(source, proofs([])), true);
  assert.equal(courseRecordIsHistorical(source, proofs([{ ...full, finishedRequiredChallengeCount: 0 }])), false);
  assert.equal(courseRecordIsHistorical({ ...source, request: { scope: 'all', mode: 'submit' } }, proofs([])), false);
  assert.equal(courseRecordIsHistorical(job({ result: { homeworks: [{ title: '作业', homework_id: null }] } }), proofs([])), false);
});

test('explicit plan coverage must be complete, unique and well formed', () => {
  const source = job({ request: { scope: 'all', mode: 'submit' } });
  for (const result of [
    { total_homeworks: 19, homeworks: [{ course: 'C', homework_id: 1 }] },
    { homeworks: [{ course: 'C', homework_id: 1 }] },
    { total_homeworks: 2, homeworks: [{ course: 'C', homework_id: 1 }, { course: 'C', homework_id: 1 }] },
    { total_homeworks: 1, homeworks: [{ title: '无标识' }] },
    { total_homeworks: 1, homeworks: [{ course: 'C' }] },
  ]) assert.equal(courseRecordIsHistorical({ ...source, result }, proofs([])), false);
});

test('proof must start after observing the exact terminal version, regardless of clock skew', () => {
  const source = job();
  assert.equal(courseRecordIsHistorical(source, proofs([], { observedTerminalVersions: new Set(), requestedAt: read + 3600000 })), false, 'fast client clock cannot validate an early read');
  assert.equal(courseRecordIsHistorical(source, proofs([], { requestedAt: Date.parse(finished) - 1, updatedAt: read })), false, 'late response from an early read');
  const changed = job({ updated_at: '2026-11-03T11:00:00Z' });
  assert.equal(courseRecordIsHistorical(changed, proofs([])), false, 'previous record version cannot validate a new outcome');
  assert.equal(courseRecordIsHistorical(changed, proofs([], {}, changed)), true);
  for (const source of [job({ finished_at: 'invalid' }), job({ updated_at: '2026-11-04T12:00:00Z' })]) {
    assert.equal(courseRecordIsHistorical(source, proofs([], {}, source)), false);
  }
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { canRetryJob, canCancelJob, currentServiceJobs, jobProgress, partitionServiceJobs, renderJobs, renderServiceDetail, renderServiceJob, serviceLabel, serviceStatus } from './dist/jobs.js';

await test('only an explicit fixed upstream conflict attention shows the consumer explanation', () => {
  const value = { id:'conflict',status:'support',phase:'support',request:{scope:'homework',mode:'submit'},result:{attention:'upstream_question_conflict',total_levels:5,completed_levels:4} };
  const notice = '题目要求和评测样例矛盾，做好的保留，等课程方核对后继续。';
  assert.ok(renderServiceJob(value).includes(notice));
  assert.ok(renderServiceDetail(value).includes(notice));
  assert.ok(!renderServiceJob(value, [], true).includes(notice));
  assert.ok(!renderServiceJob({...value,status:'succeeded',result:{...value.result,service_status:'completed'}}).includes(notice));
  assert.ok(!renderServiceJob({...value,result:{attention:'PRIVATE_TRACE'}}).includes('PRIVATE_TRACE'));
});

const job = (id, day, extra = {}) => ({ id, status: 'failed', phase: 'completed', kind: 'answer', request: { mode: 'submit', scope: 'course', course: 'COURSE_A' }, created_at: `2026-01-${String(day).padStart(2, '0')}T00:00:00Z`, updated_at: `2026-01-${String(day).padStart(2, '0')}T00:00:00Z`, ...extra });
const ids = rows => rows.map(row => row.id);
const courses = [{ identifier: 'COURSE_A', name: '示例数据结构课程' }, { identifier: 'COURSE_B', name: '示例系统课程' }];
const visible = html => html.replace(/<details\b[\s\S]*?<\/details>/g, '');

await test('consumer completion progress shows one current arrangement per original target', async t => {
  await t.test('lifecycle controls status despite stale projections and paused work is counted separately', () => {
    for (const [raw, expected] of [['failed','support'], ['uncertain','support'], ['interrupted','support'], ['support','support'], ['canceled','stopped'], ['queued','waiting'], ['waiting','waiting'], ['running','processing'], ['processing','processing'], ['succeeded','completed']]) {
      for (const projected of ['processing','support','completed']) {
        assert.equal(serviceStatus(job('lifecycle', 1, { status: raw, result: { service_status: projected } })), expected, `${raw}/${projected}`);
      }
    }
    const paused = job('paused', 1, { result: { service_status: 'processing', total_homeworks: 19, completed_homeworks: 13 } });
    const html = renderJobs([paused]);
    assert.match(html, /需要跟进/); assert.match(html, /13 \/ 19 份作业/);
    assert.doesNotMatch(html, /服务处理中|class="pulse"/);
    const active = job('active', 1, { status: 'running', request: { scope: 'course', course: 'COURSE_B', mode: 'submit' } });
    assert.match(renderJobs([active, paused]), /class="pulse"/);
  });
  await t.test('terminal parent stops stale running and waiting child labels while preserving completed children', () => {
    const result = { levels: [{ title: '已过关', status: 'passed' }, { title: '运行旧快照', status: 'processing' }, { title: '排队旧快照', status: 'waiting' }] };
    for (const status of ['failed','support','canceled','uncertain','interrupted']) {
      const html = renderServiceDetail(job('terminal', 1, { status, result }));
      assert.match(html, />已完成</); assert.match(html, />待跟进</);
      assert.doesNotMatch(html, />正在处理<|服务人员正在处理|服务处理中/);
    }
    assert.match(renderServiceDetail(job('active', 1, { status: 'running', result })), />正在处理</);
  });
  await t.test('retry and stop actions require explicit backend capabilities and never appear in historical records', () => {
    const failed = job('retryable', 1, { status: 'support', can_retry: true });
    assert.equal(canRetryJob(failed), true);
    assert.match(renderServiceJob(failed), /可重新执行|data-job-retry="retryable"/);
    assert.match(renderServiceDetail(failed), /data-job-retry="retryable"/);
    assert.doesNotMatch(renderServiceDetail(failed), /服务人员正在处理|无需重复提交/);
    assert.match(renderServiceDetail(failed), /已核验通过的必做关卡会自动跳过/);
    for (const item of [
      { ...failed, can_retry: undefined }, { ...failed, can_retry: 'true' }, { ...failed, status: 'succeeded' },
      { ...failed, status: 'running' }, { ...failed, request: { mode: 'submit', scope: 'exercise', course: 'COURSE_A', exercise_id: 1 } },
      { ...failed, request: { mode: 'collect_verified' } }, { ...failed, request: { mode: 'selftest', scope: 'course', course: 'COURSE_A' } },
    ]) { assert.equal(canRetryJob(item), false); assert.doesNotMatch(renderServiceJob(item) + renderServiceDetail(item), /data-job-retry/); }
    assert.doesNotMatch(renderServiceJob(failed, [], true) + renderServiceDetail(failed, [], true), /data-job-retry|data-job-cancel|重新执行/);
    const running = { ...failed, status: 'running', can_retry: false, can_cancel: true };
    assert.equal(canCancelJob(running), true); assert.match(renderServiceJob(running), /data-job-cancel="retryable"/);
    assert.doesNotMatch(renderServiceJob({ ...running, can_cancel: undefined }), /data-job-cancel/);
    assert.doesNotMatch(renderServiceJob(running, [], true), /data-job-cancel/);
    for (const request of [
      { scope: 'exercise', course: 'COURSE_A', exercise_id: 1, mode: 'submit' },
      { scope: 'current', course: 'COURSE_A', homework_id: 1, game: 'game', mode: 'submit' },
      { scope: 'course', course: 'COURSE_A', mode: 'selftest' },
      { scope: 'course', course: 'COURSE_A', mode: 'generate_only' }, { mode: 'collect_verified' },
    ]) { const ineligible={...running,request}; assert.equal(canCancelJob(ineligible),false); assert.doesNotMatch(renderServiceJob(ineligible), /data-job-cancel/); }
    assert.match(renderServiceJob(running, [], false, 'stopping'), /disabled>正在停止/);
  });

  await t.test('homework progress preserves zero, partial, full and unknown counts with a dedicated fill', () => {
    for (const [completed, total, percent] of [[0, 4, 0], [2, 4, 50], [4, 4, 100], [6, 4, 100]]) {
      const item = job('progress-sample', 1, { status: completed >= total ? 'succeeded' : 'running', result: { total_homeworks: total, completed_homeworks: completed } });
      assert.deepEqual(jobProgress(item), { completed: Math.min(completed, total), total, unit: '份作业', percent });
      const html = renderServiceJob(item);
      assert.match(html, new RegExp(`${Math.min(completed, total)} \\/ ${total} 份作业`));
      assert.doesNotMatch(html, /class="bar"|progress-fill|progress-track/);
    }
    const unknown = job('unknown-progress', 1, { status: 'running' });
    assert.deepEqual(jobProgress(unknown), { completed: 0, total: 0, unit: '关', percent: 0 });
    assert.match(renderServiceJob(unknown), /进度待更新/);
    assert.doesNotMatch(renderServiceJob(unknown), /0 \/ 0|100%/);
  });
  await t.test('running details expose public progress even before result rows are available', () => {
    const running = job('progress-only', 1, { status: 'running', progress: { current_title: '正在处理第 4 关', total_levels: 8, completed_levels: 3 } });
    const html = renderServiceDetail(running);
    assert.match(html, /class="verdict"/);
    assert.match(html, /正在处理第 4 关/);
    assert.match(html, /3 \/ 8 关/);
  });
  await t.test('a stored unconfirmed record is an ordinary stopped task with a retry', () => {
    const stopped = job('legacy-uncertain', 1, { status: 'uncertain', can_retry: true, error_code: 'RESULT_UNCONFIRMED', result: { service_status: 'support', total_homeworks: 19, completed_homeworks: 13 } });
    assert.equal(canRetryJob(stopped), true);
    assert.equal(serviceLabel(stopped), '需要跟进');
    const html = renderServiceJob(stopped) + renderServiceDetail(stopped);
    assert.match(html, /data-job-retry="legacy-uncertain"/); assert.match(html, /13 \/ 19 份作业/);
    assert.doesNotMatch(html, /复核|请勿重复提交|data-job-recover|RESULT_UNCONFIRMED/);
    assert.doesNotMatch(renderServiceJob(stopped, [], true) + renderServiceDetail(stopped, [], true), /data-job-retry/);
  });
  await t.test('exams have separate targets from homework IDs and from one another', () => {
    const exercise = exercise_id => ({ scope: 'exercise', course: 'COURSE_A', exercise_id, mode: 'submit' });
    const rows = [job('old-exam', 1, { request: exercise(701) }), job('new-exam', 4, { request: exercise(701), status: 'submitted' }), job('other-exam', 3, { request: exercise(702) }), job('same-number-homework', 2, { request: { scope: 'homework', course: 'COURSE_A', homework_id: 701, mode: 'submit' } })];
    assert.deepEqual(ids(partitionServiceJobs(rows).current), ['new-exam', 'other-exam', 'same-number-homework']);
    assert.deepEqual(ids(partitionServiceJobs(rows).history), ['old-exam']);
  });
  await t.test('exam submission and grade states never imply a verified perfect score or disclose answers', () => {
    const exam = job('submitted', 1, { request: { scope: 'exercise', course: 'COURSE_A', exercise_id: 701, mode: 'submit' }, status: 'succeeded', result: { title: '期中在线考试', submitted: true, score_known: false, total_questions: 50, completed_questions: 49, answers: 'PRIVATE_ANSWER', diagnostics: ['PRIVATE_DIAG'] } });
    assert.equal(serviceStatus(exam), 'awaiting_review');
    assert.deepEqual(jobProgress(exam), { completed: 49, total: 50, unit: '道题', percent: 98 });
    const html = renderServiceDetail(exam) + renderServiceJob(exam);
    assert.match(html, /期中在线考试|待公布成绩|共 50 道题/);
    assert.match(html, /已交卷，成绩以头歌公布为准。/); assert.doesNotMatch(html, /成绩公布后会在这里更新/);
    assert.doesNotMatch(html, /49 \/ 50|progress-track|progress-fill/);
    assert.doesNotMatch(html, /满分|结果已核验|PRIVATE_|已完成，结果/);
    const graded = { ...exam, result: { ...exam.result, score_known: true, score: '81.50', max_score: '100.00' } };
    assert.equal(serviceStatus(graded), 'graded'); assert.match(renderServiceDetail(graded), /成绩 81.50 \/ 100.00/);
    assert.equal(serviceStatus({ ...exam, result: { title: '试卷', submitted: false } }), 'support');
  });
  await t.test('historical confirmed submission shows the paper size without inventing saved-answer counts', () => {
    const request = { scope: 'exercise', course: 'COURSE_A', exercise_id: 701, mode: 'submit' };
    const submitted = job('existing-submission', 1, { request, status: 'submitted', result: { submitted: true, score_known: false, total_questions: 50, completed_questions: 0 } });
    assert.deepEqual(jobProgress(submitted), { completed: 0, total: 50, unit: '道题', percent: 0 });
    const html = renderServiceJob(submitted);
    assert.match(html, /已交卷 · 共 50 道题/); assert.doesNotMatch(html, /0 \/ 50|50 \/ 50|progress-track|已保存/);
    const saving = { ...submitted, status: 'processing', result: { submitted: false, total_questions: 50, completed_questions: 12 } };
    assert.match(renderServiceJob(saving), /12 \/ 50 道题/); assert.doesNotMatch(renderServiceJob(saving), /progress-fill|progress-track/);
  });
  await t.test('new completion supersedes old failed and successful attempts by creation time without mutating state', () => {
    const old = job('older-error', 1, { updated_at: '2026-03-01T00:00:00Z' });
    const completed = job('older-completed', 2, { status: 'succeeded' });
    const latest = job('latest-completed', 3, { status: 'succeeded', result: { total_homeworks: 4, completed_homeworks: 4 } });
    const rows = [old, latest, completed], before = JSON.stringify(rows), parts = partitionServiceJobs(rows);
    assert.deepEqual(ids(parts.current), []); assert.deepEqual(ids(parts.history), ['latest-completed', 'older-completed', 'older-error']);
    assert.equal(JSON.stringify(rows), before); assert.deepEqual(currentServiceJobs(rows), parts.current);
    const html = renderJobs(rows, courses), shown = visible(html);
    assert.doesNotMatch(shown, /data-job-detail="latest-completed"/); assert.doesNotMatch(shown, /服务人员/);
    assert.match(html, /现在没有要处理的任务/); assert.match(html, /4 份全部完成/);
    assert.match(html, /<details class="hist" data-service-history>/); assert.match(html, /历史记录<span>3 次<\/span>/);
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
    assert.match(html, />未完成</); assert.match(html, />已完成</); assert.match(html, /class="tag">历史<\/span>/); assert.match(html, /data-job-history="true"/);
    assert.doesNotMatch(html, /服务人员正在处理|服务处理中|正在逐项完成|会在处理后更新|关闭页面也会继续/);
    assert.doesNotMatch(html, /当次/, 'a consumer record must not carry the 当次 bookkeeping prefix');
    assert.match(renderServiceDetail(old, courses), /需要跟进/);
  });
  await t.test('history expansion is a controllable native disclosure and current cards stay compact', () => {
    const rows = [job('old', 1), job('latest', 2, { status: 'succeeded' })];
    assert.match(renderJobs(rows, courses, true), /data-service-history open>/);
    assert.doesNotMatch(renderJobs(rows, courses), /data-service-history open>/);
    assert.match(renderServiceJob(rows[1], courses), /class="card slip/);
    assert.doesNotMatch(renderServiceJob(rows[1], courses), /job-note|放心|结果已核验/);
  });
  await t.test('titles and identifiers are escaped and private execution fields never enter consumer cards or details', () => {
    const sample = job('evil"><img src=x onerror=alert(1)>', 1, { request: { scope: 'homework', mode: 'submit', course: 'COURSE_A', homework_id: 10 }, error_code: 'PRIVATE_CODE', message: 'PRIVATE_TRACE', result: { title: '<script>BAD</script>', model_calls: 42, cost_micro_usd: 999, diagnostics: ['PRIVATE_TRACE'], homeworks: [{ title: '<img src=x onerror=alert(1)>', status: 'failed', reason_code: 'PRIVATE_REASON' }] } });
    const html = renderJobs([sample], courses) + renderServiceDetail(sample, courses, true);
    assert.match(html, /&lt;script&gt;BAD&lt;\/script&gt;/); assert.match(html, /&lt;img/);
    assert.doesNotMatch(html, /<script|<img|PRIVATE_CODE|PRIVATE_TRACE|PRIVATE_REASON|999|42 次/);
  });
  await t.test('a task sheet draws one 田字格 cell per level and never repeats a status word on them', () => {
    const levels = [
      { position: 1, title: '栈的基本操作', status: 'completed' },
      { position: 2, title: '队列的基本操作', status: 'completed' },
      { position: 3, title: '进制转换', status: 'processing' },
      { position: 4, title: '括号匹配', status: 'waiting' },
      { position: 5, title: '表达式求值', status: 'needs_service', reason: 'platform_busy' },
    ];
    const running = job('level-grid', 1, { status: 'running', result: { total_homeworks: 1, completed_homeworks: 0, homeworks: [{ title: '栈与队列应用', status: 'processing', total_levels: 5, completed_levels: 2, levels }] } });
    const sheet = renderServiceDetail(running, courses);
    const cells = [...sheet.matchAll(/<li class="[^"]*" title="[^"]*" aria-label="[^"]*"><span class="n num">(\d+)<\/span><\/li>/g)];
    assert.equal(cells.length, levels.length, 'one cell per level');
    assert.deepEqual(cells.map(cell => cell[1]), ['1', '2', '3', '4', '5']);
    // The state is the cell's shape, named only in its label.
    assert.match(sheet, /<li class="done" title="第 1 关 · 栈的基本操作 · 已完成"/);
    assert.match(sheet, /<li class="run" title="第 3 关 · 进制转换 · 正在处理"/);
    assert.match(sheet, /<li class="warn" title="第 5 关 · 表达式求值 · 待跟进"/);
    const grid = sheet.slice(sheet.indexOf('<ol class="tzg">'), sheet.indexOf('</ol>'));
    const gridText = grid.replace(/<[^>]*>/g, ' ');
    assert.doesNotMatch(gridText, /已完成|正在处理|待处理|待跟进/, 'a level cell must not print its status as text');
    assert.match(grid, /aria-label="第 1 关 · 栈的基本操作 · 已完成"/, 'the state is carried by the accessible label');
    // Only the levels needing attention are listed underneath, with a reason.
    const notes = sheet.slice(sheet.indexOf('<ul class="level-notes">'), sheet.indexOf('</ul>', sheet.indexOf('<ul class="level-notes">')));
    assert.match(notes, /第 5 关/); assert.match(notes, /表达式求值/); assert.match(notes, /头歌评测资源紧张/);
    assert.doesNotMatch(notes, /栈的基本操作|进制转换|括号匹配/);
    assert.doesNotMatch(sheet, /当次/, 'consumer output must not carry the 当次 bookkeeping prefix');
  });
  await t.test('the 全部完成 seal appears only when every assignment of a task passed', () => {
    const rows = count => Array.from({ length: count }, (_, index) => ({ title: `作业 ${index + 1}`, status: 'completed' }));
    const done = job('all-done', 1, { status: 'succeeded', result: { service_status: 'completed', total_homeworks: 3, completed_homeworks: 3, homeworks: rows(3) } });
    assert.match(renderServiceDetail(done, courses), /class="seal square"[^>]*aria-label="全部完成"/);
    const partial = { ...done, result: { ...done.result, completed_homeworks: 2, homeworks: [...rows(2), { title: '作业 3', status: 'needs_service' }] } };
    assert.doesNotMatch(renderServiceDetail(partial, courses), /seal/);
    const running = { ...done, status: 'running', result: { ...done.result, homeworks: [...rows(2), { title: '作业 3', status: 'processing' }] } };
    assert.doesNotMatch(renderServiceDetail(running, courses), /seal/);
    const empty = job('no-rows', 1, { status: 'succeeded', result: { service_status: 'completed' } });
    assert.doesNotMatch(renderServiceDetail(empty, courses), /seal/, 'a record with no assignment rows cannot be sealed');
  });
});

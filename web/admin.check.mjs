import assert from 'node:assert/strict';
import { test } from 'node:test';
import { partitionAdminJobs, renderAdmin, renderAdminJobDetail } from './dist/admin.js';
import { renderServiceDetail, renderServiceJob } from './dist/jobs.js';

const job = (extra = {}) => ({
  id: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', owner_id: 'platform:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  status: 'failed', phase: 'completed', request: { scope: 'course', course: 'SAMPLE_COURSE', mode: 'submit' },
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:21Z',
  message: '部分作业需要服务人员处理', error_code: 'service_required',
  context: {
    user: { id: 'user-beyond-first-100', name: '测试同学甲', login: 'classmate' },
    course: { identifier: 'SAMPLE_COURSE', name: '数据结构·示例课堂' },
    homework: { id: 101, name: '示例作业一' },
    level: { title: '示例第六关', position: 6, score: '0.00', max_score: '0.0' },
    summary: { reason_code: 'verification_unconfirmed', progress_label: '9 / 10 份作业', attention_homeworks: [{ id: 101, name: '示例作业一' }] },
  },
  result: { status: 'needs_service', total_homeworks: 10, completed_homeworks: 9, model_calls: 0, homeworks: [{ homework_id: 101, course: 'SAMPLE_COURSE', title: '示例作业一', status: 'needs_service', reason_code: 'execution_failed', result: { total_levels: 10, completed_levels: 5, levels: [{ title: '示例第六关', position: 6, status: 'uncertain', reason_code: 'verification_unconfirmed' }] } }] },
  ...extra,

});

const state = jobs => ({ overview: {}, users: [], jobs, audit: [], loading: false, error: false, jobsKind: 'all' });
const visible = html => html.replace(/<details\b[\s\S]*?<\/details>/g, '').replace(/<[^>]*>/g, ' ');

await test('current operations keep live service targets clear and retain previous attempts in history', async t => {
  await t.test('a confirmed exam submission is not a follow-up alert when the outer worker expired after reconciliation', () => {
    const value = job({ status: 'uncertain', request: { scope: 'exercise', course: 'SAMPLE_COURSE', exercise_id: 12, mode: 'submit' }, result: { title: '示例考试', submitted: true, score_known: false, total_questions: 50, completed_questions: 0 } });
    const shown = renderAdmin('overview', state([value]));
    assert.doesNotMatch(shown, /data-admin-job-detail=/);
    assert.match(shown, /当前需跟进<\/span><strong>0/);
    assert.match(visible(renderAdmin('operations', state([value]))), /已交卷/);
  });
  const attempt = (id, day, extra = {}) => job({ id, created_at: `2026-01-${String(day).padStart(2, '0')}T00:00:00Z`, ...extra });
  const ids = rows => rows.map(row => row.id);
  await t.test('exam service stop reasons are readable to administrators while consumer details retain fixed service copy', () => {
    const cases = [
      ['answer_readback_unconfirmed', '保存后的答案回读尚未确认'], ['answer_save_unconfirmed', '答案保存响应尚未确认'], ['paper_changed', '题卷或选项已变化，需要重新核对'], ['checkpoint_persistence_failed', '执行记录保存失败'], ['commit_readiness_unconfirmed', '交卷前检查尚未确认'], ['unanswered_questions', '仍有未作答项目'], ['model_provider_missing', '考试模型尚未配置'], ['model_generation_failed', '考试答案生成失败'], ['model_budget_exhausted', '达到本次内部用量上限'], ['previous_execution_interrupted', '上次执行已中断，需要先核对结果'], ['submitted_state_unconfirmed', '交卷状态尚未确认'], ['exercise_candidate_contract', '生成答案与当前题卷不匹配'], ['exercise_media_unverified', '题目中的媒体内容尚未核验'],
    ];
    for (const [code, label] of cases) {
      const item = job({ request: { scope: 'exercise', exercise_id: 701, course: 'SAMPLE_COURSE', mode: 'submit' }, context: { user: { id: 'sample-user', name: '示例同学' }, course: { name: '示例课堂' }, exercise: { id: 701, name: '示例考试' }, summary: { reason_code: code } }, error_code: code, message: 'PRIVATE_TRACE', result: { title: '示例考试', reason_code: code, diagnostics: ['PRIVATE_DIAG'] } });
      const admin = visible(renderAdmin('operations', state([item])) + renderAdminJobDetail(item));
      assert.ok(admin.includes(label), `missing readable reason ${code}`);
      const consumer = renderServiceDetail(item) + renderServiceJob(item);
      assert.ok(!consumer.includes(code)); assert.ok(!consumer.includes(label)); assert.doesNotMatch(consumer, /PRIVATE_TRACE|PRIVATE_DIAG/);
      assert.match(consumer, /考试事项由服务人员跟进/);
    }
  });
  await t.test('exam targets remain independent and use names, question progress, submission and grade states', () => {
    const request = exercise_id => ({ scope: 'exercise', exercise_id, course: 'SAMPLE_COURSE', mode: 'submit' });
    const context = { user: { id: 'sample-user', name: '示例同学' }, course: { name: '示例课堂' }, exercise: { id: 701, name: '示例在线考试' } };
    const old = attempt('old-exam', 1, { request: request(701), context });
    const newest = attempt('new-exam', 3, { request: request(701), context, status: 'succeeded', result: { title: '示例在线考试', submitted: true, score_known: false, total_questions: 20, completed_questions: 20 } });
    const other = attempt('other-exam', 2, { request: request(702), context: { ...context, exercise: { id: 702, name: '另一场考试' } } });
    const homework = attempt('same-number-homework', 2, { request: { scope: 'homework', homework_id: 701, course: 'SAMPLE_COURSE', mode: 'submit' } });
    const parts = partitionAdminJobs([old, newest, other, homework]);
    assert.deepEqual(ids(parts.history), ['old-exam']); assert.deepEqual(ids(parts.current), ['new-exam', 'other-exam', 'same-number-homework']);
    const html = renderAdmin('operations', state([newest]));
    assert.match(html, /示例在线考试|在线考试|20 \/ 20 道题|待公布成绩/);
    assert.doesNotMatch(html, /尚未进入关卡|必做内容已完成并核验|满分/);
    assert.match(renderAdminJobDetail(newest), /示例在线考试|成绩尚未公布/);
  });
  await t.test('latest creation replaces a prior failed or successful attempt regardless of later log updates', () => {
    const older = attempt('old-error', 1, { updated_at: '2026-02-01T00:00:00Z' });
    const latest = attempt('new-complete', 2, { status: 'succeeded' });
    const input = [older, latest], parts = partitionAdminJobs(input);
    assert.deepEqual(ids(parts.current), ['new-complete']); assert.deepEqual(ids(parts.history), ['old-error']);
    assert.deepEqual(ids(input), ['old-error', 'new-complete'], 'partition must not reorder shared application state');
    const history = visible(renderAdmin('operations', { ...state(input), jobsView: 'history' }));
    assert.match(history, /历史执行记录|当次未完成/); assert.doesNotMatch(history, /需要处理/);
    assert.match(history, /零分值关卡待核验/, 'historical reason remains inspectable');
    assert.match(renderAdmin('operations', { ...state(input), jobsView: 'history' }), /data-admin-job-detail="old-error"/);
  });
  await t.test('all queued and running attempts stay visible while terminal attempts for their target move to history', () => {
    const rows = [attempt('older-failure', 1), attempt('running', 2, { status: 'running' }), attempt('queued', 3, { status: 'queued' }), attempt('terminal-after-running', 4)];
    const parts = partitionAdminJobs(rows);
    assert.deepEqual(ids(parts.current), ['queued', 'running']);
    assert.deepEqual(ids(parts.history), ['terminal-after-running', 'older-failure']);
  });
  await t.test('owner, mode, scope, course, homework and game identities remain separate', () => {
    const requests = [
      { scope: 'all', mode: 'submit' },
      { scope: 'course', course: 'COURSE_A', mode: 'submit' },
      { scope: 'course', course: 'COURSE_B', mode: 'submit' },
      { scope: 'homework', course: 'COURSE_A', homework_id: 1, mode: 'submit' },
      { scope: 'homework', course: 'COURSE_A', homework_id: 2, mode: 'submit' },
      { scope: 'current', course: 'COURSE_A', homework_id: 1, game: 'GAME_A', mode: 'submit' },
      { scope: 'current', course: 'COURSE_A', homework_id: 1, game: 'GAME_B', mode: 'submit' },
      { scope: 'current', course: 'COURSE_A', homework_id: 1, game: 'GAME_A', mode: 'selftest' },
    ];
    const rows = requests.map((request, index) => attempt(`target-${index}`, index + 1, { request }));
    rows.push(attempt('other-owner', 15, { owner_id: 'platform:other', request: requests[0] }));
    const parts = partitionAdminJobs(rows);
    assert.equal(parts.current.length, 9); assert.equal(parts.history.length, 0);
    const a = attempt('same-request-earlier-context', 1), b = attempt('same-request-later-context', 2);
    b.context.course.identifier = 'HIGHLIGHT_OTHER_COURSE'; b.context.homework.id = 222;
    assert.deepEqual(ids(partitionAdminJobs([a, b]).current), ['same-request-later-context'], 'displayed failed homework cannot change the submitted service target');
  });
  await t.test('unlinked ended accounts are historical, but live work and records with insufficient identity stay visible', () => {
    const unlinked = { user: { name: '历史账号未关联', linked: false } };
    const rows = [attempt('unlinked-ended', 1, { context: unlinked }), attempt('unlinked-live', 2, { context: unlinked, status: 'running' }), attempt('anonymous-a', 3, { owner_id: undefined, context: undefined }), attempt('anonymous-b', 4, { owner_id: undefined, context: undefined }), attempt('missing-target-a', 5, { request: { scope: 'homework', mode: 'submit' } }), attempt('missing-target-b', 6, { request: { scope: 'homework', mode: 'submit' } })];
    const parts = partitionAdminJobs(rows);
    assert.deepEqual(ids(parts.history), ['unlinked-ended']); assert.equal(parts.current.length, 5);
  });
  await t.test('latest unresolved service stays current and contributes exactly once to current follow-up metrics', () => {
    const old = attempt('old-failure', 1), latest = attempt('latest-failure', 2);
    latest.context.user.name = '仍需处理同学';
    const overview = renderAdmin('overview', { ...state([old, latest]), overview: { support_jobs: 999 } });
    assert.match(overview, /当前需跟进<\/span><strong>1/); assert.match(visible(overview), /仍需处理同学/);
    assert.doesNotMatch(overview, /data-admin-job-detail="old-failure"|999/);
  });
  await t.test('default current service view separates read-only collection and exposes clear reversible filters', () => {
    const service = attempt('current-service', 2);
    const old = attempt('historical-service', 1);
    const collection = attempt('current-collection', 3, { request: { mode: 'collect_verified' }, context: { user: { name: '示例采集账户' } }, result: { collected: 3, duplicates: 6, skipped: 2, failed: 0 } });
    const input = { ...state([old, collection, service]), jobsKind: undefined };
    const html = renderAdmin('operations', input), shown = visible(html);
    assert.match(html, /data-admin-jobs-view="current" aria-pressed="true"/); assert.match(html, /data-admin-jobs-view="history"/);
    assert.match(html, /data-admin-jobs-kind="collection"/); assert.match(html, /data-admin-job-detail="current-service"/);
    assert.doesNotMatch(html, /data-admin-job-detail="historical-service"|data-admin-job-detail="current-collection"/);
    assert.match(shown, /当前任务|历史记录|答题服务|题库采集|基于最近 3 条记录/);
    const collectionHtml = renderAdmin('operations', { ...input, jobsKind: 'collection' });
    assert.match(collectionHtml, /data-admin-job-detail="current-collection"/); assert.doesNotMatch(collectionHtml, /data-admin-job-detail="current-service"/);
    assert.match(visible(collectionHtml), /新增 3 条答案 · 已有 6 条 · 跳过 2 项/);
    assert.match(renderAdmin('operations', { ...input, jobsView: 'history' }), /data-admin-job-detail="historical-service"/);
  });
  await t.test('a capped source is explicitly recent history rather than a claim to list all platform tasks', () => {
    const rows = Array.from({ length: 100 }, (_, index) => attempt(`record-${index}`, 1, { owner_id: `platform:sample-${index}` }));
    assert.match(visible(renderAdmin('operations', state(rows))), /基于最近 100 条记录（服务端上限 100 条）/);
  });
});

await test('administrator sees people and work instead of storage identifiers', async t => {
  await t.test('all administrator pages omit retired access cards and preserve internal usage meaning', () => {
    const input = { ...state([]), overview: { usage: { model_calls: 17 } } };
    for (const page of ['overview', 'users', 'operations', 'audit']) {
      assert.doesNotMatch(renderAdmin(page, input), /卡密|data-action="create-card"|data-revoke-card/);
    }
    const overview = visible(renderAdmin('overview', input));
    assert.match(overview, /模型用量与费用仅供管理员核对服务成本/);
    assert.match(overview, /不代表用户余额或可用额度/);
    assert.match(overview, /17/);
  });

  await t.test('course table resolves user, course, assignment, stopped level and units from backend context', () => {
    const html = renderAdmin('overview', state([job()]));
    const shown = visible(html);
    for (const expected of ['测试同学甲', '数据结构·示例课堂', '整门课程', '示例作业一', '第 6 关', '示例第六关', '9 / 10 份作业', '5 / 10 个关卡', '零分值关卡待核验']) assert.ok(shown.includes(expected), expected);
    assert.doesNotMatch(shown, /platform:|bbbbbbbbbbbb|SAMPLE_COURSE|service_required|verification_unconfirmed|部分作业需要服务人员处理/);
    assert.match(shown, /未调用模型/); assert.doesNotMatch(shown, /题库命中/);
  });
  await t.test('deep level cause takes precedence over generic batch failure even for older records', () => {
    const old = job({ context: undefined }); old.result.homeworks[0].result.levels[0].reason_code = 'model_output_truncated';
    const shown = visible(renderAdmin('operations', state([old])));
    assert.match(shown, /模型返回内容被截断/); assert.doesNotMatch(shown, /部分作业需要服务人员处理|execution_failed|model_output_truncated/);
    assert.match(shown, /用户名称待补充/); assert.match(shown, /课程名称待补充/);
  });
  await t.test('details keep readable identity and phase while raw diagnostics stay in a collapsed technical section', () => {
    const value = job(); value.result.homeworks[0].result.diagnostics = ['API status 0 <upstream-trace>'];
    const html = renderAdminJobDetail(value), shown = visible(html);
    for (const expected of ['测试同学甲', '数据结构', '示例作业一', '第 6 关', '执行结束']) assert.ok(shown.includes(expected), expected);
    assert.doesNotMatch(shown, /platform:|bbbbbbbbbbbb|verification_unconfirmed|API status|completed|needs_service|uncertain/);
    assert.match(html, /<details class="operator-technical">/); assert.doesNotMatch(html, /<details[^>]*\bopen\b/);
    assert.match(html, /platform:aaaaaaaa/); assert.match(html, /verification_unconfirmed/); assert.match(html, /API status 0 &lt;upstream-trace&gt;/);
  });
  await t.test('all-course request names each blocked course and assignment', () => {
    const value = job({ request: { scope: 'all', mode: 'submit' } });
    value.context.summary.attention_homeworks.push({ id: 8, name: '二叉树实验', course_name: '算法设计' });
    const shown = visible(renderAdmin('operations', state([value])));
    assert.match(shown, /全部课程/); assert.doesNotMatch(shown, /算法设计 · 二叉树实验/); assert.match(shown, /另有 1 份需跟进，详情查看/);
    const detail = visible(renderAdminJobDetail(value));
    assert.match(detail, /算法设计/); assert.match(detail, /二叉树实验/); assert.match(detail, /需要跟进的作业 · 2 份/);
  });
  await t.test('collection is a read-only bank task with question counters, never a nameless homework', () => {
    const value = job({ request: { mode: 'collect_verified' }, context: { user: { name: '采集同学' } }, message: 'Read-only collection stopped; inspect its summary', error_code: 'COLLECTION_FAILED', result: { collected: 3, duplicates: 2, skipped: 68, reasons: { request_byte_or_time_limit: 1 } } });
    const html = renderAdmin('operations', state([value])), shown = visible(html);
    assert.match(shown, /采集同学/); assert.match(shown, /题库采集 · 只读/); assert.match(shown, /新增 3 条答案 · 已有 2 条 · 跳过 68 项/); assert.doesNotMatch(shown, /Read-only|作业名称待补充|进度尚未获取/);
    assert.match(renderAdminJobDetail(value), /题库采集详情/);
  });
  await t.test('missing historical labels stay explicit and do not invent a person or assignment', () => {
    const value = job({ request: { scope: 'homework', course: 'SAMPLE_COURSE', homework_id: 101, mode: 'submit' }, context: undefined, result: {} });
    const shown = visible(renderAdmin('operations', state([value])));
    for (const expected of ['用户名称待补充', '课程名称待补充', '作业名称未留存', '进度尚未获取']) assert.ok(shown.includes(expected), expected);
    assert.doesNotMatch(shown, /platform:|bbbbbbbbbbbb|SAMPLE_COURSE|测试同学甲|示例作业/);
  });
  await t.test('course-wide completion and queued batches are not presented as missing a single homework name', () => {
    const completed = job({ id: 'done', status: 'succeeded', request: { scope: 'course', course: 'COURSE_DONE', mode: 'submit' }, context: undefined, result: { status: 'passed', total_homeworks: 3, completed_homeworks: 3 } });
    const all = job({ id: 'all', status: 'succeeded', request: { scope: 'all', mode: 'submit' }, context: undefined, result: { status: 'passed' } });
    const queued = job({ id: 'queued', status: 'queued', context: undefined, result: {} });
    const html = visible(renderAdmin('operations', state([completed, all, queued])));
    assert.match(html, /课程全部必做作业/); assert.match(html, /所有课程必做作业/); assert.match(html, /等待同步作业/);
    assert.doesNotMatch(html, /作业名称待补充|作业名称未留存/);
  });
  await t.test('server-provided and historical metadata cannot inject HTML in a list or detail', () => {
    const injection = '<img src=x onerror=alert(1)>';
    const value = job(); value.context.user.name = injection; value.context.course.name = injection; value.context.homework.name = injection; value.context.level.title = injection; value.context.summary.message = injection;
    value.result.homeworks[0].title = injection; value.result.homeworks[0].result.diagnostics = [injection];
    const html = renderAdmin('operations', state([value])) + renderAdminJobDetail(value);
    assert.doesNotMatch(html, /<img|<script|<upstream/); assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
  });
  await t.test('authoritative empty context level never borrows the last progress from a different assignment', () => {
    const value = job();
    value.context.level = {};
    value.progress = { current_homework_id: 202, current_course: 'OTHER_COURSE', current_title: '不得混入的关卡', current_position: 3 };
    const list = visible(renderAdmin('operations', state([value]))), detail = visible(renderAdminJobDetail(value));
    assert.match(list, /示例作业一/); assert.match(list, /尚未进入关卡/); assert.doesNotMatch(list, /示例第六关|不得混入的关卡/);
    assert.match(detail, /当前关卡\s+尚未进入关卡/);
  });
  await t.test('legacy progress level is used only when the selected homework and course both match', () => {
    const value = job({ context: undefined, result: { homeworks: [{ course: 'COURSE_A', homework_id: 101, title: '待开始作业', status: 'needs_service', reason_code: 'deadline_exceeded' }] }, progress: { current_homework_id: 202, current_course: 'COURSE_A', current_title: '旧进度标题', current_position: 4 } });
    assert.doesNotMatch(visible(renderAdmin('operations', state([value]))), /旧进度标题|第 4 关/);
    value.progress.current_homework_id = 101; value.progress.current_course = 'COURSE_B';
    assert.doesNotMatch(visible(renderAdmin('operations', state([value]))), /旧进度标题|第 4 关/);
    value.progress.current_course = 'COURSE_A';
    assert.match(visible(renderAdmin('operations', state([value]))), /第 4 关 · 旧进度标题/);
  });
  await t.test('legacy collection time limits preserve results and do not count as support failures', () => {
    const value = job({ request: { mode: 'collect_verified' }, context: { user: { name: '示例采集用户' } }, error_code: 'execution_failed', started_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:03:00Z', result: { collected: 0, duplicates: 12, skipped: 4, unconfirmed: 3, failed: 0, limit_reached: true, reasons: { no_confirmed_full_score: 1, programming_submitted_source_unproven: 2, objective_media_content_unproven: 1, request_byte_or_time_limit: 1 } } });
    const list = visible(renderAdmin('operations', state([value]))), detail = visible(renderAdminJobDetail(value));
    assert.match(list, /达到采集上限/); assert.match(list, /约 3 分钟/); assert.match(list, /尚未扫描完全部内容/); assert.doesNotMatch(list, /需要处理|采集完成/);
    const overview = renderAdmin('overview', state([value]));
    assert.doesNotMatch(overview, /data-admin-job-detail=/); assert.match(overview, /当前需跟进<\/span><strong>0/);
    for (const expected of ['新增入库答案', '题库已有答案', '12 条', '跳过', '4 项', '其中证据未确认', '3 项', '子集', '1 份作业', '2 个关卡', '不是图片张数', '1 次终止事件', '不计入跳过数量', '已有答案仍可复用']) assert.ok(detail.includes(expected), expected);
    assert.doesNotMatch(detail, /no_confirmed_full_score|programming_submitted_source_unproven|objective_media_content_unproven|request_byte_or_time_limit/);
    assert.match(renderAdminJobDetail(value), /programming_submitted_source_unproven/);
    value.updated_at = '2026-01-01T00:00:30Z';
    assert.doesNotMatch(visible(renderAdminJobDetail(value)), /3 分钟/);
    assert.match(visible(renderAdminJobDetail(value)), /读取预算或时间上限/);
  });
  await t.test('collection failures and unexpected exceptions remain attention items even when a limit was reached', () => {
    const value = job({ request: { mode: 'collect_verified' }, context: { user: { name: '示例采集用户' } }, result: { collected: 2, failed: 1, limit_reached: true, reasons: { progress_read_failed: 1, request_byte_or_time_limit: 1 } } });
    assert.match(visible(renderAdmin('overview', state([value]))), /1 项只读采集需核对/); assert.match(visible(renderAdmin('operations', state([value]))), /需要处理/);
    assert.match(visible(renderAdminJobDetail(value)), /作业分数读取失败/);
    value.result.failed = 0; value.error_code = 'DATABASE_WRITE_PANIC';
    assert.match(visible(renderAdmin('overview', state([value]))), /1 项只读采集需核对/);
    assert.doesNotMatch(visible(renderAdmin('operations', state([value]))), /达到采集上限/);
  });
  await t.test('new collection stop metadata distinguishes scan caps, read budget and collection busy', () => {
    const value = job({ status: 'succeeded', request: { mode: 'collect_verified' }, context: { user: { name: '示例采集用户' } }, result: { collected: 0, duplicates: 2, skipped: 0, failed: 0, limit_reached: true, stop_reason: 'scan_limit', reasons: {} } });
    assert.match(visible(renderAdminJobDetail(value)), /达到本次课堂、作业或关卡扫描上限/);
    value.result.stop_reason = 'read_budget_limit';
    assert.match(visible(renderAdminJobDetail(value)), /达到本次读取预算上限/);
    value.result = { status: 'skipped', reason_code: 'collector_busy' };
    assert.match(visible(renderAdminJobDetail(value)), /本次采集已跳过/); assert.match(visible(renderAdminJobDetail(value)), /本次没有启动扫描/);
    value.result = { collected: 0, duplicates: 2, failed: 0, limit_reached: false, reasons: {} };
    assert.match(visible(renderAdminJobDetail(value)), /采集完成（无新增）/);
  });
  await t.test('explicit unknown collection stop metadata overrides aggregate limit counters', () => {
    const value = job({ request: { mode: 'collect_verified' }, context: { user: { name: '示例采集用户' } }, error_code: 'execution_failed', result: { collected: 2, duplicates: 3, failed: 0, limit_reached: true, scan_complete: false, stop_reason: 'collection_stopped', reasons: { request_byte_or_time_limit: 1 } } });
    for (const status of ['failed', 'succeeded']) {
      value.status = status;
      const list = visible(renderAdmin('operations', state([value]))), detail = visible(renderAdminJobDetail(value));
      assert.match(list, /需要处理/); assert.doesNotMatch(list, /达到采集上限|采集完成/);
      assert.match(detail, /需要核对停止原因/);
      assert.match(visible(renderAdmin('overview', state([value]))), /1 项只读采集需核对/);
    }
    value.result.stop_reason = 'future_unknown_stop';
    assert.match(visible(renderAdmin('overview', state([value]))), /1 项只读采集需核对/);
  });
  await t.test('explicit incomplete collection is never displayed as a completed scan', () => {
    const value = job({ status: 'succeeded', request: { mode: 'collect_verified' }, context: { user: { name: '示例采集用户' } }, message: '扫描任务结束', result: { collected: 0, duplicates: 3, skipped: 0, failed: 0, limit_reached: false, scan_complete: false, reasons: {} } });
    const shown = visible(renderAdminJobDetail(value));
    assert.match(shown, /需要处理/); assert.match(shown, /本次扫描尚未完成/); assert.doesNotMatch(shown, /采集完成|已完成本次只读采集/);
    assert.match(visible(renderAdmin('overview', state([value]))), /1 项只读采集需核对/);
    value.result.scan_complete = true;
    assert.match(visible(renderAdminJobDetail(value)), /采集完成（无新增）/);
  });
  await t.test('batch timeout and pending assignments are distinct from a failed current level', () => {
    const value = job(); value.context.level = {}; value.context.summary.stop_reason = 'time_limit'; value.context.summary.pending_homeworks = 2;
    const html = visible(renderAdminJobDetail(value));
    assert.match(html, /整批执行达到 30 分钟时限/); assert.match(html, /2 份作业尚未开始，不计作已完成/);
    assert.match(visible(renderAdmin('operations', state([value]))), /另有 2 份尚未开始/);
  });
  await t.test('batch phase is explicitly scoped to the whole batch instead of the highlighted assignment', () => {
    const value = job({ phase: 'selftesting' }); value.context.level = {};
    const html = visible(renderAdminJobDetail(value));
    assert.match(html, /当前关卡\s+尚未进入关卡/);
    assert.match(html, /整批最后阶段\s+执行自测/);
    value.request.scope = 'homework';
    assert.match(visible(renderAdminJobDetail(value)), /执行阶段\s+执行自测/);
    assert.doesNotMatch(visible(renderAdminJobDetail(value)), /整批最后阶段/);
  });
  await t.test('historical child timeout evidence is not presented as a confirmed whole-batch timeout', () => {
    const value = job({ status: 'uncertain', request: { scope: 'all', mode: 'submit' } });
    value.context.level = {}; value.context.summary.stop_reason = 'deadline_observed'; value.context.summary.pending_homeworks = 3;
    const list = visible(renderAdmin('operations', state([value]))), detail = visible(renderAdminJobDetail(value));
    for (const shown of [list, detail]) {
      assert.match(shown, /整批任务已停止，最后执行的作业记录了超时/);
      assert.match(shown, /后续作业尚未开始/); assert.doesNotMatch(shown, /30 分钟|服务重启/);
    }
    assert.match(list, /另有 3 份尚未开始/); assert.match(detail, /3 份作业尚未开始/);
    value.status = 'canceled'; value.context.summary.stop_reason = 'canceled';
    const canceled = visible(renderAdminJobDetail(value));
    assert.match(canceled, /整批任务已停止，已保留完成结果/); assert.doesNotMatch(canceled, /30 分钟|服务重启|最后执行的作业记录了超时/);
    value.context.summary.stop_reason = 'time_limit';
    assert.match(visible(renderAdminJobDetail(value)), /整批执行达到 30 分钟时限/);
  });
  await t.test('answer source mismatch is explained in the attention list and child details without assigning blame', () => {
    const message = '保存后的源码与候选答案不一致，尚未确认评测通过';
    const value = job();
    value.context.summary.reason_code = 'service_required';
    value.context.summary.attention_homeworks = [{ id: 101, name: '示例作业一', course_name: '数据结构·示例课堂', reason_code: 'editor_changed' }];
    value.result.homeworks[0].reason_code = 'editor_changed';
    value.result.homeworks[0].result.levels[0].reason_code = 'editor_changed';
    value.result.homeworks[0].result.levels[0].diagnostics = ['Saved source hash differs from candidate hash'];
    const html = renderAdminJobDetail(value), shown = visible(html);
    assert.ok(shown.split(message).length - 1 >= 2, 'attention list and nested level both explain source mismatch');
    assert.doesNotMatch(shown, /editor_changed|Saved source|用户修改|用户编辑|请查看技术记录/);
    assert.match(html, /Saved source hash differs from candidate hash/);
  });
  await t.test('collection unknown reason text is contained in escaped technical output', () => {
    const value = job({ request: { mode: 'collect_verified' }, context: { user: { name: '示例采集用户' } }, result: { failed: 1, reasons: { '<img src=x onerror=alert(1)>': 2 } } });
    const html = renderAdminJobDetail(value);
    assert.doesNotMatch(html, /<img|<script/); assert.match(html, /&lt;img/); assert.doesNotMatch(visible(html), /onerror/);
    assert.match(visible(html), /其他采集记录/);
  });

});

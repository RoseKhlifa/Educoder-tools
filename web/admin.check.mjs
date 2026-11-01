import assert from 'node:assert/strict';
import { test } from 'node:test';
import { renderAdmin, renderAdminJobDetail } from './dist/admin.js';

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
const state = jobs => ({ overview: {}, users: [], jobs, audit: [], cards: [], loading: false, error: false });
const visible = html => html.replace(/<details\b[\s\S]*?<\/details>/g, '').replace(/<[^>]*>/g, ' ');

await test('administrator sees people and work instead of storage identifiers', async t => {
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
    const completed = job({ status: 'succeeded', context: undefined, result: { status: 'passed', total_homeworks: 3, completed_homeworks: 3 } });
    const all = job({ status: 'succeeded', request: { scope: 'all', mode: 'submit' }, context: undefined, result: { status: 'passed' } });
    const queued = job({ status: 'queued', context: undefined, result: {} });
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
    assert.doesNotMatch(overview, /data-admin-job-detail=/); assert.match(overview, /近期需跟进<\/span><strong>0/);
    for (const expected of ['新增入库答案', '题库已有答案', '12 条', '跳过', '4 项', '其中证据未确认', '3 项', '子集', '1 份作业', '2 个关卡', '不是图片张数', '1 次终止事件', '不计入跳过数量', '已有答案仍可复用']) assert.ok(detail.includes(expected), expected);
    assert.doesNotMatch(detail, /no_confirmed_full_score|programming_submitted_source_unproven|objective_media_content_unproven|request_byte_or_time_limit/);
    assert.match(renderAdminJobDetail(value), /programming_submitted_source_unproven/);
    value.updated_at = '2026-01-01T00:00:30Z';
    assert.doesNotMatch(visible(renderAdminJobDetail(value)), /3 分钟/);
    assert.match(visible(renderAdminJobDetail(value)), /读取预算或时间上限/);
  });
  await t.test('collection failures and unexpected exceptions remain attention items even when a limit was reached', () => {
    const value = job({ request: { mode: 'collect_verified' }, context: { user: { name: '示例采集用户' } }, result: { collected: 2, failed: 1, limit_reached: true, reasons: { progress_read_failed: 1, request_byte_or_time_limit: 1 } } });
    assert.match(visible(renderAdmin('overview', state([value]))), /示例采集用户/); assert.match(visible(renderAdmin('operations', state([value]))), /需要处理/);
    assert.match(visible(renderAdminJobDetail(value)), /作业分数读取失败/);
    value.result.failed = 0; value.error_code = 'DATABASE_WRITE_PANIC';
    assert.match(visible(renderAdmin('overview', state([value]))), /示例采集用户/);
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
      assert.match(visible(renderAdmin('overview', state([value]))), /示例采集用户/);
    }
    value.result.stop_reason = 'future_unknown_stop';
    assert.match(visible(renderAdmin('overview', state([value]))), /需要处理/);
  });
  await t.test('explicit incomplete collection is never displayed as a completed scan', () => {
    const value = job({ status: 'succeeded', request: { mode: 'collect_verified' }, context: { user: { name: '示例采集用户' } }, message: '扫描任务结束', result: { collected: 0, duplicates: 3, skipped: 0, failed: 0, limit_reached: false, scan_complete: false, reasons: {} } });
    const shown = visible(renderAdminJobDetail(value));
    assert.match(shown, /需要处理/); assert.match(shown, /本次扫描尚未完成/); assert.doesNotMatch(shown, /采集完成|已完成本次只读采集/);
    assert.match(visible(renderAdmin('overview', state([value]))), /示例采集用户/);
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

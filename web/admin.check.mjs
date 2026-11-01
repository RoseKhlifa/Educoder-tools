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
  result: { status: 'needs_service', total_homeworks: 10, completed_homeworks: 9, model_calls: 0, homeworks: [{ title: '示例作业一', status: 'needs_service', reason_code: 'execution_failed', result: { total_levels: 10, completed_levels: 5, levels: [{ title: '示例第六关', position: 6, status: 'uncertain', reason_code: 'verification_unconfirmed' }] } }] },
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
    assert.match(shown, /全部课程/); assert.match(shown, /算法设计 · 二叉树实验/); assert.match(shown, /另有 1 份需跟进/);
  });
  await t.test('collection is a read-only bank task with question counters, never a nameless homework', () => {
    const value = job({ request: { mode: 'collect_verified' }, context: { user: { name: '采集同学' } }, message: 'Read-only collection stopped; inspect its summary', error_code: 'COLLECTION_FAILED', result: { collected: 3, duplicates: 2, skipped: 68, reasons: { request_byte_or_time_limit: 1 } } });
    const html = renderAdmin('operations', state([value])), shown = visible(html);
    assert.match(shown, /采集同学/); assert.match(shown, /题库采集 · 只读/); assert.match(shown, /已采集 3 题 · 重复 2 · 跳过 68/); assert.doesNotMatch(shown, /Read-only|作业名称待补充|进度尚未获取/);
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
});

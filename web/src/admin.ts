import type { AdminAudit, AdminJob, AdminJobAction, AdminOverview, AdminUser, CollectionSummary, ProgressItem } from './types.js';
import { count, dateTime, escapeHtml as e, icon, record } from './ui.js';
import { jobProgress, serviceLabels, serviceStatus } from './jobs.js';
import { autoRetryText, internalReasonLabels as reasonLabels, progressCounts, renderProgressTree, settledTone, stageLabel } from './progress.js';
export type AdminPage = 'overview' | 'users' | 'operations' | 'audit';
export type AdminJobsView = 'current' | 'history';
export type AdminJobsKind = 'service' | 'collection' | 'all';
export interface AdminState { overview: AdminOverview; users: AdminUser[]; jobs: AdminJob[]; audit: AdminAudit[]; loading: boolean; error: boolean; jobsView?: AdminJobsView; jobsKind?: AdminJobsKind; userSearch?: string; userFilter?: 'all' | 'active' | 'disabled' }
const statusLabels: Record<string, string> = { queued: '排队中', running: '执行中', succeeded: '已完成', failed: '需要处理', uncertain: '待核验', interrupted: '已中断', canceled: '已停止', active: '正常', disabled: '已暂停', passed: '已通过', completed: '已完成', skipped: '已满分，已跳过', skipped_optional: '选做，已跳过', planned: '尚未开始', needs_service: '需要跟进', verification_unconfirmed: '待核验', processing: '执行中', support: '需要跟进', waiting: '排队中', deadline_passed: '已截止' };
const phaseLabels: Record<string, string> = { initializing: '准备后续关卡', starting: '准备执行', preflight: '核对关卡清单', verified: '已核验当前结果', service_pending: '等待服务人员跟进', queued: '排队中', loading: '读取课程与作业', loading_resources: '读取题目图片', preparing: '准备执行', collecting: '采集已验证题目', bank_lookup: '查询题库答案', bank_saved: '保存已验证答案', generating: '生成答案', answering: '生成答案', solving: '处理题目', validating: '校验答案格式', saving: '保存代码', selftesting: '执行自测', environment_retry: '重试自测环境', selftest: '执行自测', submitting: '提交答案', evaluating: '等待评测', polling: '读取评测结果', verifying: '核验完成状态', reconciling: '核对提交结果', repairing: '根据反馈修复', completed: '执行结束', failed: '执行结束，等待处理', canceled: '已停止', processing: '执行中', running: '执行中' };
const text = (value: unknown): string => typeof value === 'string' ? value.trim() : '';
const pendingStatuses = new Set(['queued', 'running', 'waiting', 'processing']);
const isPending = (job: AdminJob): boolean => pendingStatuses.has(job.status);
/**
 * adminJobItems is the operator twin of the consumer's assignment list: the
 * live snapshot while the job runs, the stored outcome once it has settled.
 */
function adminJobItems(job: AdminJob): ProgressItem[] {
  const live = job.context?.progress?.homeworks;
  if (Array.isArray(live) && live.length) return live;
  const data = record(job.result);
  return resultChildren(data).map(item => {
    const nested = record(item.result);
    const issue = deepestIssue({ ...item, ...nested });
    return {
      title: text(item.title) || text(item.subject),
      course_name: text(item.course_name),
      status: text(item.status),
      reason: text(issue.reason_code) || text(item.reason_code),
      total_levels: count(nested.total_levels),
      completed_levels: count(nested.completed_levels),
      skipped_levels: count(nested.skipped_levels),
      current_position: count(nested.current_position),
      current_title: text(nested.current_title),
      levels: (Array.isArray(nested.levels) ? nested.levels : []).map(record).map(level => ({
        position: count(level.position), title: text(level.title), status: text(level.status),
        reason: text(level.reason_code),
      })),
    } satisfies ProgressItem;
  });
}
/** adminLiveStage names the step a running job is on, for the list and detail. */
function adminLiveStage(job: AdminJob): string {
  const progress = job.context?.progress;
  if (!progress || !isPending(job)) return '';
  const position = count(progress.current_position);
  const level = text(progress.current_level);
  const stage = stageLabel(text(progress.stage));
  return [position ? `第 ${position} 关` : '', level, stage].filter(Boolean).join(' · ');
}
/** adminAutoRetry surfaces the scheduled retry the backend recorded, if any. */
function adminAutoRetry(job: AdminJob): string {
  return autoRetryText(record(job.result).auto_retry as { next_at?: string; attempt?: number; max?: number } | undefined);
}
export function adminJobResumeAction(job: AdminJob): 'retry' | 'recover' | undefined {
  if (pendingStatuses.has(job.status)) return undefined;
  // An unresolved submission must be reconciled before a normal retry.
  if (job.can_recover === true) return 'recover';
  return job.can_retry === true ? 'retry' : undefined;
}
function adminResumeButton(job: AdminJob, historical: boolean, pending?: AdminJobAction, detail = false): string {
  if (historical) return '';
  const action = adminJobResumeAction(job);
  if (!action) return '';
  const label = pending === 'retry' ? '正在重新执行…' : pending === 'recover' ? '正在复核…' : pending === 'cancel' ? '正在停止…' : action === 'retry' ? '重新执行' : '复核并继续';
  return `<button class="${detail ? 'btn red' : 'link'}" data-admin-${action}="${e(job.id)}" ${pending ? 'disabled' : ''}>${label}</button>`;
}
function targetKey(job: AdminJob): string {
  const owner = String(job.owner_id ?? job.owner ?? job.context?.user?.id ?? '').trim();
  const request = job.request;
  if (!owner) return `record:${job.id}`;
  if (request.mode === 'collect_verified') return JSON.stringify([owner, 'collect_verified']);
  const scope = request.scope || (request.game ? 'current' : 'homework');
  if (scope !== 'all' && !request.course || ['homework', 'current'].includes(scope) && !request.homework_id || scope === 'current' && !request.game) return `record:${job.id}`;
  if (scope === 'exercise' && !request.exercise_id) return `record:${job.id}`;
  return JSON.stringify([owner, request.mode, scope, request.course ?? '', request.homework_id ?? 0, request.game ?? '', request.exercise_id ?? 0]);
}
export function partitionAdminJobs(jobs: AdminJob[]): { current: AdminJob[]; history: AdminJob[] } {
  const ordered = jobs.map((job, index) => ({ job, index })).sort((a, b) => {
    const created = Date.parse(b.job.created_at) - Date.parse(a.job.created_at);
    return (Number.isFinite(created) ? created : 0) || a.index - b.index;
  }).map(item => item.job);
  const current: AdminJob[] = [], history: AdminJob[] = [], seen = new Set<string>();
  for (const job of ordered) {
    const target = targetKey(job);
    const status = serviceStatus(job);
    const active = pendingStatuses.has(job.status) || needsAttention(job) || ['submitted', 'awaiting_review'].includes(status);
    // The operations view should answer “what needs attention now?”. Every
    // completed attempt belongs in history, even if it is the latest target.
    const currentAttempt = pendingStatuses.has(job.status) || active && !seen.has(target);
    if (active && currentAttempt || job.context?.user?.linked === false && pendingStatuses.has(job.status)) current.push(job); else history.push(job);
    seen.add(target);
  }
  return { current, history };
}
function filterJobKind(jobs: AdminJob[], kind: AdminJobsKind): AdminJob[] {
  return jobs.filter(job => kind === 'all' || (job.request.mode === 'collect_verified') === (kind === 'collection'));
}
const isAttention = (value: Record<string, unknown>): boolean => ['failed', 'uncertain', 'needs_service', 'verification_unconfirmed', 'interrupted', 'support'].includes(text(value.status)) || !!text(value.reason_code) && !['passed', 'skipped', 'completed', 'skipped_optional', 'deadline_passed'].includes(text(value.status));
function resultChildren(value: Record<string, unknown>): Record<string, unknown>[] {
  return (Array.isArray(value.homeworks) ? value.homeworks : Array.isArray(value.levels) ? value.levels : []).map(record);
}
function deepestIssue(data: Record<string, unknown>, depth = 0): Record<string, unknown> {
  if (depth > 5) return data;
  for (const child of resultChildren(data)) {
    const nested = record(child.result);
    if (isAttention(child) || isAttention(nested)) return deepestIssue({ ...child, ...nested }, depth + 1);
  }
  return data;
}
function collectionSummary(job: AdminJob): CollectionSummary {
  const data = record(job.result);
  return record(data.collection ?? data.summary ?? data) as CollectionSummary;
}
function collectionBudgetEnd(job: AdminJob): boolean {
  if (job.request.mode !== 'collect_verified' || !['failed', 'succeeded'].includes(job.status)) return false;
  const summary = collectionSummary(job), reason = text(summary.stop_reason);
  if (summary.failed !== 0 || summary.limit_reached !== true) return false;
  // Explicit new metadata is authoritative. Historical aggregate limit counters
  // cannot turn an unrelated later exception into a normal bounded scan.
  if (reason) return ['time_limit', 'read_budget_limit', 'scan_limit'].includes(reason);
  return count(summary.reasons?.request_byte_or_time_limit) > 0 && (job.status === 'succeeded' || ['execution_failed', 'collection_failed', 'collection_stopped', ''].includes(text(job.error_code).toLowerCase()));
}
function collectionNeedsReview(job: AdminJob): boolean {
  if (job.request.mode !== 'collect_verified' || !['failed', 'succeeded', 'uncertain', 'interrupted'].includes(job.status) || text(record(job.result).reason_code) === 'collector_busy' || collectionBudgetEnd(job)) return false;
  const summary = collectionSummary(job);
  return count(summary.failed) > 0 || !!text(summary.stop_reason) || summary.scan_complete === false;
}
function needsAttention(job: AdminJob): boolean {
	if (job.request.scope === 'exercise' && record(job.result).submitted === true) return false;
  if (collectionNeedsReview(job)) return true;
  return ['failed', 'uncertain', 'interrupted'].includes(job.status) && !collectionBudgetEnd(job);
}
function collectionEndReason(job: AdminJob): string {
  const started = Date.parse(job.started_at || job.created_at), finished = Date.parse(job.finished_at || job.updated_at);
  const elapsed = (finished - started) / 1000;
  const reason = text(collectionSummary(job).stop_reason);
  if (reason === 'read_budget_limit') return '达到本次读取预算上限，已保留结果；尚未扫描完全部内容';
  if (reason === 'scan_limit') return '达到本次课堂、作业或关卡扫描上限，已保留结果；尚未扫描完全部内容';
  return reason === 'time_limit' || Number.isFinite(elapsed) && elapsed >= 175 && elapsed <= 190
    ? '达到约 3 分钟的本次采集时限，已保留结果；尚未扫描完全部内容'
    : '达到本次读取预算或时间上限，已保留结果；尚未扫描完全部内容';
}
function displayStatus(job: AdminJob): { label: string; tone: string } {
  if (job.request.scope === 'exercise' && !pendingStatuses.has(job.status)) {
    const status = serviceStatus(job);
    return { label: serviceLabels[status], tone: status === 'graded' ? 'completed' : status === 'support' ? 'support' : 'processing' };
  }
  if (job.request.mode === 'collect_verified' && text(record(job.result).reason_code) === 'collector_busy') return { label: '本次采集已跳过', tone: 'limited' };
  if (collectionNeedsReview(job)) return { label: '需要处理', tone: 'support' };
  if (collectionBudgetEnd(job)) return { label: '达到采集上限', tone: 'limited' };
  if (job.request.mode === 'collect_verified' && job.status === 'succeeded' && count(collectionSummary(job).failed) === 0) {
    if (collectionSummary(job).scan_exhausted === true && collectionSummary(job).scan_complete === true) return { label: '完整扫描完成', tone: 'completed' };
    return { label: count(collectionSummary(job).collected) === 0 ? '采集完成（无新增）' : '采集完成', tone: 'completed' };
  }
  return { label: statusLabels[job.status] ?? '处理中', tone: job.status === 'succeeded' ? 'completed' : needsAttention(job) ? 'support' : 'processing' };
}
const collectionReasons: Record<string, { label: string; unit: string; note?: string }> = {
  verified_checkpoint_reused: { label: '复用已核验检查点，省去重复题目与答案读取', unit: '个关卡' },
  no_confirmed_full_score: { label: '作业中没有已确认的正分满分关卡', unit: '份作业' },
  programming_submitted_source_unproven: { label: '当前代码缺少与本平台正式通过记录匹配的源码证据', unit: '个关卡', note: '外部历史通过记录不能证明当前编辑器代码就是当时提交的代码。' },
  programming_evaluation_unconfirmed: { label: '当前代码的编译或评测通过证据不足', unit: '个关卡' },
  objective_media_content_unproven: { label: '题干或选项含有尚未核验内容的图片或媒体', unit: '个关卡', note: '按关卡计数，不是图片张数。' },
  unsupported_homework_kind: { label: '作业类型尚不支持采集', unit: '份作业' },
  classroom_identifier_missing: { label: '课堂标识缺失', unit: '个课堂' },
  ambiguous_challenge_list: { label: '关卡清单缺失或存在重复身份', unit: '份作业' },
  no_readonly_game_locator: { label: '没有找到可只读访问的已通过任务', unit: '份作业' },
  target_not_confirmed_full_score: { label: '目标关卡尚未确认满分', unit: '个关卡' },
  target_identity_mismatch: { label: '任务详情与目标身份不一致', unit: '个关卡' },
  runtime_game_challenge_identity_mismatch: { label: '运行实例与关卡身份不一致', unit: '个关卡' },
  score_changed_or_unconfirmed: { label: '再次读取时分数变化或尚未确认', unit: '个关卡' },
  editor_changed: { label: '两次读取之间编辑器代码发生变化', unit: '个关卡' },
  objective_answer_changed_or_mismatched: { label: '两次读取的答卷或题目不一致', unit: '个关卡' },
  objective_status_unconfirmed: { label: '客观题通过状态尚未确认', unit: '个关卡' },
  objective_answer_evidence_missing: { label: '客观题完整答卷或正确性证据不足', unit: '个关卡' },
  unsupported_objective_question_kind: { label: '客观题题型尚不支持采集', unit: '个关卡' },
  objective_question_identity_mismatch: { label: '题目身份不一致', unit: '个关卡' },
  objective_result_mismatch: { label: '题目作答结果与目标题目不一致', unit: '个关卡' },
  objective_actual_answer_missing: { label: '没有读到完整的实际作答', unit: '个关卡' },
  objective_actual_answer_invalid: { label: '实际作答格式与选项不匹配', unit: '个关卡' },
  objective_result_position_mismatch: { label: '作答记录的位置不匹配', unit: '个关卡' },
  objective_contract_invalid: { label: '答卷未通过题目结构核验', unit: '个关卡' },
  unsupported_task_kind: { label: '任务类型尚不支持采集', unit: '个关卡' },
  request_timeout: { label: '单次采集请求超时，完整扫描尚未结束', unit: '次' },
  course_read_failed: { label: '课堂列表读取失败', unit: '次' },
  homework_read_failed: { label: '作业列表读取失败', unit: '次' },
  progress_read_failed: { label: '作业分数读取失败', unit: '次' },
  readonly_locator_read_failed: { label: '只读任务定位读取失败', unit: '次' },
  task_read_failed: { label: '任务详情读取失败', unit: '次' },
  answer_capture_failed: { label: '答案读取或保存失败', unit: '次' },
  request_byte_or_time_limit: { label: '本次采集达到读取预算或时间上限', unit: '次终止事件', note: '这是采集终止记录，不计入跳过数量。' },
  homework_page_limit: { label: '本次作业列表达到分页上限', unit: '次', note: '本轮没有继续扫描后续页。' },
  course_page_limit: { label: '本次课堂列表达到分页上限', unit: '次', note: '本轮没有继续扫描后续页。' },
};
function renderCollectionSummary(job: AdminJob): string {
  const summary = collectionSummary(job);
  const counters = [['新增入库答案', count(summary.collected), '条'], ['题库已有答案', count(summary.duplicates), '条'], ['其中免重复读取', count(summary.reused_questions), '题'], ['检查点复用关卡', count(summary.reused_levels), '个'], ['跳过', count(summary.skipped), '项'], ['其中证据未确认', count(summary.unconfirmed), '项'], ['读取或保存失败', count(summary.failed), '次']];
  const reasons = Object.entries(record(summary.reasons)).filter(([, value]) => count(value) > 0);
  return `<section class="sheet-note"><h3 class="sheet-h">本次只读采集结果</h3><dl class="ledger cap">${counters.map(([label, value, unit]) => `<div><dt>${label}</dt><span class="dots"></span><dd>${value} ${unit}</dd></div>`).join('')}</dl><p class="muted small-note">新增和已有答案按答案条目计数；跳过按课堂、作业或关卡计项，不代表跳过的题目总数。证据未确认是跳过的子集，不重复相加。</p>${count(summary.collected) === 0 && count(summary.duplicates) > 0 ? '<p class="muted small-note">本次没有新增答案，是因为已核验的答案在题库中已存在；已有答案仍可复用。</p>' : ''}<h3 class="sheet-h">跳过、失败与终止原因</h3><ul class="reason-list">${reasons.map(([code, value]) => { const reason = collectionReasons[code]; return `<li><div><strong>${e(reason?.label ?? '其他采集记录，请展开技术记录查看原因代码')}</strong><span>${count(value)} ${e(reason?.unit ?? '项')}</span></div>${reason?.note ? `<p>${e(reason.note)}</p>` : ''}</li>`; }).join('') || '<li><p>没有记录跳过、失败或终止原因。</p></li>'}</ul></section>`;
}
function readableReason(job: AdminJob): string {
  if (job.request.scope === 'exercise' && ['submitted', 'awaiting_review', 'graded'].includes(serviceStatus(job))) return serviceStatus(job) === 'graded' ? '已交卷，成绩已公布' : '已交卷，成绩尚未公布';
  // A job that has not finished has no failure reason. Its own lifecycle, and
  // the step it is on, answer "what is happening" better than a stored message.
  if (['queued', 'waiting'].includes(job.status)) return job.request.mode === 'collect_verified' ? '等待采集通道' : '等待后台处理';
  if (['running', 'processing'].includes(job.status) && job.request.mode !== 'collect_verified') {
    const stage = adminLiveStage(job);
    return stage ? `正在执行 · ${stage}` : '后台正在执行';
  }
  if (collectionBudgetEnd(job)) return collectionEndReason(job);
  if (job.request.mode === 'collect_verified' && text(record(job.result).reason_code) === 'collector_busy') return '已有其他只读采集正在进行，本次没有启动扫描';
  if (job.request.mode === 'collect_verified' && count(collectionSummary(job).failed) > 0) {
    const failed = collectionSummary(job);
    const failures = Object.entries(failed.reasons ?? {}).filter(([code, value]) => code.endsWith('_failed') && count(value) > 0).map(([code]) => collectionReasons[code]?.label).filter(Boolean);
    return `本次有 ${count(failed.failed)} 次读取或保存失败${failures.length ? '：' + failures.join('；') : '，请查看采集原因明细'}；已经入库的答案已保留`;
  }
  if (collectionNeedsReview(job)) return '本次扫描尚未完成，已保留已有结果；需要核对停止原因';
  const summary = job.context?.summary;
  const stopReason = text(summary?.stop_reason).toLowerCase();
  if (stopReason && job.request.mode !== 'collect_verified') {
    if (['course', 'all'].includes(job.request.scope ?? '')) {
      if (stopReason === 'time_limit') return '整批执行达到 30 分钟时限，已保留完成结果；未开始的作业等待重新处理';
      if (stopReason === 'deadline_observed') return '整批任务已停止，最后执行的作业记录了超时；后续作业尚未开始';
      if (stopReason === 'canceled') return '整批任务已停止，已保留完成结果';
    }
    return reasonLabels[stopReason] || '本次执行已停止，已保留完成结果；请查看停止记录';
  }
  const contextCode = text(summary?.reason_code).toLowerCase();
  if (contextCode === 'verification_unconfirmed' && Number(job.context?.level?.score) === 0 && Number(job.context?.level?.max_score) === 0 && text(job.context?.level?.max_score)) return '零分值关卡待核验，当前得分与满分均为 0';
  if (contextCode && contextCode !== 'service_required' && reasonLabels[contextCode]) return reasonLabels[contextCode];
  const supplied = text(summary?.reason || summary?.message);
  if (supplied && (!contextCode || contextCode !== 'service_required')) return reasonLabels[supplied.toLowerCase()] || (/\p{Script=Han}/u.test(supplied) ? supplied : '执行未完成，请展开技术记录查看原始原因');
  const issue = deepestIssue(record(job.result));
  if (!text(issue.reason_code) && /\p{Script=Han}/u.test(job.message ?? '') && !/部分作业|整份作业尚未|需要服务人员处理/.test(job.message ?? '')) return job.message!;
  const code = (text(issue.reason_code) || text(job.error_code)).toLowerCase();
  if (reasonLabels[code]) return reasonLabels[code];
  const diagnostics = Array.isArray(issue.diagnostics) ? issue.diagnostics.map(text).find(item => /\p{Script=Han}/u.test(item)) : '';
  if (diagnostics) return diagnostics;
  if (['succeeded', 'completed'].includes(job.status)) return job.request.mode === 'collect_verified' ? '已完成本次只读采集' : '必做内容已完成并核验';
  if (['queued', 'waiting'].includes(job.status)) return '等待后台处理';
  if (['running', 'processing'].includes(job.status)) return '后台正在执行';
  if (job.status === 'canceled') return '本次执行已停止';
  if (job.request.mode === 'collect_verified') return '题库采集未完成，请检查采集明细';
  return /\p{Script=Han}/u.test(job.message ?? '') ? job.message! : '执行未完成，请展开技术记录查看原始原因';
}
function jobContext(job: AdminJob) {
  const data = record(job.result), progress = record(job.progress), context = job.context;
  const homeworks = resultChildren(data);
  const namedHomeworks = context?.homework?.id ? homeworks.filter(item => count(item.homework_id) === context.homework!.id) : [];
  const currentHomework = context?.homework?.id
    ? namedHomeworks.find(item => !context.course?.identifier || text(item.course) === context.course.identifier) ?? (namedHomeworks.length === 1 && !text(namedHomeworks[0].course) ? namedHomeworks[0] : undefined)
    : homeworks.find(item => isAttention(item) || isAttention(record(item.result))) ?? homeworks.find(item => item.homework_id === progress.current_homework_id);
  const batch = record(currentHomework?.result);
  const issue = deepestIssue(batch);
  const progressMatches = !!currentHomework && count(currentHomework.homework_id) > 0 && count(currentHomework.homework_id) === count(progress.current_homework_id) && !!text(currentHomework.course) && text(currentHomework.course) === text(progress.current_course);
  const issueLevel = count(issue.position) > 0 || text(issue.title) && Array.isArray(batch.levels);
  return {
    user: context?.user?.name || context?.user?.login || '用户名称待补充',
    course: context?.course?.name || text(currentHomework?.course_name) || '课程名称待补充',
    homework: context?.exercise?.name || context?.homework?.name || text(currentHomework?.title) || text(data.title) || (job.request.scope === 'exercise' ? '考试名称待补充' : '作业名称待补充'),
    level: context ? context.level?.title || '' : (issueLevel ? text(issue.title) : '') || text(batch.current_title) || (progressMatches ? text(progress.current_title) : ''),
    position: context ? context.level?.position || 0 : count(issue.position) || count(batch.current_position) || (progressMatches ? count(progress.current_position) : 0),
    attention: context?.summary?.attention_homeworks ?? homeworks.filter(item => isAttention(item) || isAttention(record(item.result))).map(item => ({ id: count(item.homework_id), name: text(item.title), course_name: text(item.course_name), reason_code: text(deepestIssue(record(item.result)).reason_code) || text(item.reason_code) })),
    batch,
  };
}
function progressLabel(job: AdminJob): string {
  if (job.request.mode === 'collect_verified') {
    const summary = collectionSummary(job);
    return `新增 ${count(summary.collected)} 条答案 · 已有 ${count(summary.duplicates)} 条 · 跳过 ${count(summary.skipped)} 项`;
  }
  if (job.context?.summary?.progress_label) return job.context.summary.progress_label;
  const progress = jobProgress(job);
  return progress.total ? `${progress.completed} / ${progress.total} ${progress.unit === '关' ? '个关卡' : progress.unit}` : '进度尚未获取';
}
function homeworkLabel(job: AdminJob, context: ReturnType<typeof jobContext>): string {
  if (job.request.scope === 'exercise') return context.homework;
  if (context.homework !== '作业名称待补充') return context.homework;
  if (job.request.scope === 'course' || job.request.scope === 'all') {
    if (['queued', 'waiting', 'running', 'processing'].includes(job.status)) return '等待同步作业';
    return job.request.scope === 'all' ? '所有课程必做作业' : '课程全部必做作业';
  }
  return '作业名称未留存';
}
export function adminJobTarget(job: AdminJob): string {
  const context = jobContext(job);
  return `${context.user} · ${job.request.scope === 'all' ? '全部课程' : context.course} · ${homeworkLabel(job, context)}`;
}
function moneyText(amount: string, currency: string): string {
  return currency === 'CNY' ? `¥${amount}` : currency === 'USD' ? `$${amount}` : `${amount} ${currency}`.trim();
}
/** modelBalanceText is the provider account balance the server reads every few minutes. */
function modelBalanceText(overview: AdminOverview): string {
  const balance = record(overview.model_balance);
  if (text(balance.error)) return '暂时无法读取';
  const total = text(balance.total);
  return total ? moneyText(total, text(balance.currency)) : '—';
}
/**
 * renderModelBalanceWarning is a sticky note on the overview: once the model
 * account is empty, every level that needs generation fails.
 */
function renderModelBalanceWarning(overview: AdminOverview): string {
  const balance = record(overview.model_balance);
  if (balance.low !== true) return '';
  const threshold = text(balance.threshold);
  const title = balance.available === false
    ? '模型账户现在读不到。'
    : `模型余额 ${modelBalanceText(overview)}${threshold ? `，低于提醒线 ${moneyText(threshold, text(balance.currency))}` : ''}。`;
  return `<div class="sticky-note" role="status">${icon('alert')}<div><b>${e(title)}</b>余额用完后，需要生成答案的关卡会失败，题库里已有答案的关卡不受影响。到供应商平台充值，这里每 5 分钟更新一次。</div></div>`;
}
/** kpis are index cards with large numerals, built only from published fields. */
function kpis(state: AdminState, jobs: AdminJob[]): string {
  const data = state.overview;
  const capacity = record(record(data.capacity).jobs ?? data.capacity);
  const users = record(data.users);
  const usage = record(data.usage);
  const totalUsers = data.total_users ?? users.total_users;
  const enabled = state.users.filter(user => user.status !== 'disabled').length;
  const running = data.running_jobs ?? data.running ?? capacity.running;
  const queued = data.queued_jobs ?? data.queued ?? capacity.queued;
  const attention = jobs.filter(needsAttention);
  const retrying = attention.filter(job => adminAutoRetry(job)).length;
  const calls = count(usage.model_calls);
  const cost = `$${(count(usage.cost_micro_usd) / 1000000).toFixed(2)}`;
  const card = (label: string, value: string, hint: string, hot = false): string =>
    `<div class="card kpi${hot ? ' hot' : ''}"><div class="k">${e(label)}</div><div class="v num">${e(value)}</div><div class="h">${e(hint)}</div></div>`;
  return `<div class="kpis" aria-label="服务指标">
    ${card('平台用户', typeof totalUsers === 'number' ? String(count(totalUsers)) : String(state.users.length), `已启用 ${enabled} 位`)}
    ${card('执行中', typeof running === 'number' ? String(count(running)) : '—', typeof queued === 'number' ? `排队 ${count(queued)}` : '排队数待同步')}
    ${card('需要跟进', String(attention.length), retrying ? `其中 ${retrying} 项会自动重试` : '都要手动处理', attention.length > 0)}
    ${card('模型费用', cost, `累计 ${calls.toLocaleString('zh-CN')} 次调用`)}
  </div>`;
}
/**
 * renderActiveJobs is the operator's "what is running right now" panel: every
 * queued or running job with the same live detail the user sees.
 */
function renderActiveJobs(jobs: AdminJob[], actions: ReadonlyMap<string, AdminJobAction>): string {
  const active = jobs.filter(isPending);
  const rows = active.slice(0, 12).map(job => {
    const context = jobContext(job), stage = adminLiveStage(job);
    const items = adminJobItems(job), counts = progressCounts(items);
    const squares = items.length
      ? `<div class="squares" role="img" aria-label="${e(`${items.length} 份作业，完成 ${counts.completed} 份`)}">${items.slice(0, 40).map(item => {
        const tone = settledTone(item.status, true);
        return `<i class="${tone === 'completed' ? 'done' : tone === 'processing' ? 'run' : tone === 'support' ? 'warn' : ''}"></i>`;
      }).join('')}</div>` : `<span class="what">${e(progressLabel(job))}</span>`;
    return `<div class="run-row">
      <div><div class="who">${e(context.user)}</div><div class="what">${e(job.request.scope === 'all' ? '全部课程' : context.course)} · ${e(homeworkLabel(job, context))}</div>
        <div class="stage-line"><span class="pulse"></span>${e(stage || '正在准备')}</div></div>
      <div class="bar-cell">${squares}</div>
      <div class="run-acts"><button class="link" data-admin-job-detail="${e(job.id)}">详情</button><button class="btn sm stop" data-admin-cancel="${e(job.id)}" ${actions.has(job.id) ? 'disabled' : ''}>${actions.get(job.id) === 'cancel' ? '正在停止…' : '停止'}</button></div>
    </div>`;
  }).join('');
  return `<section class="card"><div class="panel-head"><h2>正在执行<span>${active.length} 项</span></h2><button class="link" data-admin-show-jobs="service">全部任务${icon('right')}</button></div>
    ${rows || '<div class="empty"><h3>现在没有执行中的任务</h3><p>有用户开始任务，这里会立刻显示。</p></div>'}
    ${active.length > 12 ? `<p class="cap muted">另有 ${active.length - 12} 项，可在任务运营查看。</p>` : ''}</section>`;
}
/**
 * renderAttentionGroups groups recent failures by reason so an operator sees
 * which problem affects several users rather than one row at a time.
 */
function renderAttentionGroups(jobs: AdminJob[]): string {
  const groups = new Map<string, AdminJob[]>();
  for (const job of jobs.filter(needsAttention)) {
    const reason = (text(job.context?.summary?.reason_code) || text(record(job.result).reason_code) || text(job.error_code) || 'unknown').toLowerCase();
    groups.set(reason, [...(groups.get(reason) ?? []), job]);
  }
  const rows = [...groups.entries()].sort((a, b) => b[1].length - a[1].length);
  return `<section class="card"><div class="panel-head"><h2>需要跟进<span>按原因</span></h2></div>
    ${rows.length ? rows.map(([reason, items]) => {
      const retries = items.map(adminAutoRetry).filter(Boolean);
      return `<div class="reason"><div class="reason-top">${icon('bang')}${e(reasonLabels[reason] ?? `原因代码：${reason}`)}<span class="cnt">${items.length} 位</span></div>
        ${retries.length ? `<p>${e(retries[0]!)}${retries.length > 1 ? `，另有 ${retries.length - 1} 项也会自动重试` : ''}</p>` : '<p>需要手动处理。</p>'}
        <div class="names">${items.slice(0, 6).map(job => `<button class="link" data-admin-job-detail="${e(job.id)}">${e(jobContext(job).user)}${icon('right')}</button>`).join('')}${items.length > 6 ? `<span class="muted">等 ${items.length} 位</span>` : ''}</div></div>`;
    }).join('') : '<div class="empty"><h3>现在没有需要跟进的任务</h3><p>失败的任务会按原因归在这里。</p></div>'}</section>`;
}
/** renderCapacity is the ledger of how much headroom the service has left. */
function renderCapacity(overview: AdminOverview): string {
  const capacity = { ...record(record(overview.capacity).jobs), ...record(overview.capacity) };
  const labels: Array<[string, string]> = [
    ['answer_running', '答题执行'], ['collection_running', '题库采集'],
    ['queued', '答题排队'], ['collection_queued', '采集排队'],
    ['model_concurrency', '模型并发上限'], ['per_owner_running', '单用户并发'],
  ];
  const workers = count(capacity.workers), collectors = count(capacity.collection_workers);
  const rows = labels.map(([key, label]) => {
    const value = capacity[key];
    if (typeof value !== 'number') return '';
    const limit = key === 'answer_running' ? workers : key === 'collection_running' ? collectors : 0;
    return `<div><dt>${e(label)}</dt><span class="dots"></span><dd>${count(value)}${limit ? ` / ${limit}` : ''}</dd></div>`;
  }).filter(Boolean).join('');
  const balance = record(overview.model_balance);
  const balanceRow = text(balance.total) || text(balance.error)
    ? `<div><dt>模型余额</dt><span class="dots"></span><dd class="${balance.low === true ? 'hot' : ''}">${e(modelBalanceText(overview))}</dd></div>` : '';
  return `<section class="card"><div class="panel-head"><h2>运行容量</h2></div>
    ${rows || balanceRow ? `<dl class="ledger cap">${rows}${balanceRow}</dl>` : '<div class="empty"><h3>暂无容量数据</h3></div>'}</section>`;
}
/** renderUsage is the operator-only cost ledger. */
function renderUsage(overview: AdminOverview): string {
  const usage = record(overview.usage);
  const money = (key: string): string => `$${(count(usage[key]) / 1000000).toFixed(6)}`;
  const rows: Array<[string, string]> = [
    ['供应商余额', modelBalanceText(overview)],
    ['模型调用', count(usage.model_calls).toLocaleString('zh-CN')],
    ['输入 Token', count(usage.input_tokens).toLocaleString('zh-CN')],
    ['输出 Token', count(usage.output_tokens).toLocaleString('zh-CN')],
    ['记录费用（含估算）', money('cost_micro_usd')],
    ['已确认费用', money('settled_cost_micro_usd')],
    ['待核对调用', count(usage.unknown_calls).toLocaleString('zh-CN')],
    ['预留费用', money('reserved_micro_usd')],
  ];
  return `<section class="card"><div class="panel-head"><h2>模型用量<span>仅管理员可见</span></h2></div>
    <dl class="ledger cap">${rows.map(([label, value]) => `<div><dt>${e(label)}</dt><span class="dots"></span><dd>${e(value)}</dd></div>`).join('')}</dl>
    <p class="cap muted">这些数字用于核对服务成本，不代表用户余额或可用额度。</p></section>`;
}
/**
 * adminJobRows is a ledger: rules, no heavy header bar. Rows stay dense enough
 * to scan a hundred records, and every row opens the full job sheet.
 */
function adminJobRows(jobs: AdminJob[], limited = false, history = false, actions: ReadonlyMap<string, AdminJobAction> = new Map()): string {
  const rows = limited ? jobs.filter(needsAttention).slice(0, 10) : jobs;
  const body = rows.map(job => {
    const data = record(job.result);
    const context = jobContext(job), collecting = job.request.mode === 'collect_verified', status = displayStatus(job);
    const scope = collecting ? '题库采集 · 只读' : job.request.scope === 'all' ? '全部课程' : job.request.scope === 'course' ? '整门课程' : job.request.scope === 'current' ? '单个关卡' : job.request.scope === 'exercise' ? '在线考试' : '单份作业';
    const work = collecting ? '采集已通过题目' : homeworkLabel(job, context);
    const extra = !collecting && context.attention.length > 1 ? `<small class="cell-meta">另有 ${context.attention.length - 1} 份需跟进，详情查看</small>` : '';
    const level = job.request.scope === 'exercise' ? '<small class="cell-meta">在线考试</small>'
      : context.level ? `<small class="cell-meta">${context.position ? `第 ${context.position} 关 · ` : ''}${e(context.level)}</small>`
        : '<small class="cell-meta">尚未进入关卡</small>';
    const stage = adminLiveStage(job);
    const retry = adminAutoRetry(job);
    const tone = history ? '' : status.tone === 'completed' ? 'done' : status.tone === 'support' ? 'run' : status.tone === 'processing' ? 'run' : '';
    const label = history && needsAttention(job) ? '当次未完成' : status.label;
    const mark = history ? '' : status.tone === 'completed' ? `<span class="tick">${icon('tick')}</span>` : status.tone === 'support' ? icon('bang') : isPending(job) ? '<span class="pulse"></span>' : '';
    return `<tr class="tap" data-admin-job-detail="${e(job.id)}">
      <td><div class="cell-strong">${e(context.user)}</div><small class="cell-meta">${e(dateTime(job.created_at))} 创建</small></td>
      <td><div class="cell-strong">${e(collecting ? '全部已通过任务' : context.course)}</div><small class="cell-meta">${e(scope)}</small></td>
      <td><div class="cell-strong">${e(work)}</div>${collecting ? '' : level}${stage ? `<small class="cell-meta"><span class="pulse"></span> ${e(stage)}</small>` : ''}${extra}</td>
      <td><span class="state ${tone}">${mark}${e(label)}</span><small class="cell-meta">${e(progressLabel(job))}</small>${!collecting && count(context.batch.total_levels) && !job.context?.summary?.progress_label?.includes('当前作业') ? `<small class="cell-meta">当前作业 ${count(context.batch.completed_levels)} / ${count(context.batch.total_levels)} 个关卡</small>` : ''}</td>
      <td>${e(readableReason(job))}${retry ? `<small class="cell-meta">${e(retry)}</small>` : ''}${count(job.context?.summary?.pending_homeworks) ? `<small class="cell-meta">另有 ${count(job.context?.summary?.pending_homeworks)} 份尚未开始</small>` : ''}</td>
      <td class="nowrap"><div class="mono">${count(data.model_calls)} 次</div><small class="cell-meta">$${(count(data.cost_micro_usd) / 1000000).toFixed(6)}</small>${count(data.model_calls) === 0 && !collecting ? '<small class="cell-meta">未调用模型</small>' : ''}</td>
      <td class="acts"><button class="link" data-admin-job-detail="${e(job.id)}">详情</button>${adminResumeButton(job, history, actions.get(job.id))}${['queued', 'running'].includes(job.status) ? `<button class="link" data-admin-cancel="${e(job.id)}" ${actions.has(job.id) ? 'disabled' : ''}>${actions.get(job.id) === 'cancel' ? '正在停止…' : '停止'}</button>` : ''}</td>
    </tr>`;
  }).join('');
  if (!body) {
    return `<div class="empty"><h3>${history ? '没有历史记录' : limited ? '现在没有需要跟进的任务' : '没有当前任务'}</h3><p>${history ? '被新任务替代的旧记录会留在这里。' : limited ? '旧记录收在任务运营的历史记录里。' : '可以切换任务类型看题库采集。新任务会显示在这里。'}</p></div>`;
  }
  return `<div class="table-wrap"><table class="ledger-table wide"><thead><tr><th>用户</th><th>课程 / 范围</th><th>作业 / 当前关卡</th><th>状态 / 进度</th><th>原因</th><th>模型 / 费用</th><th>管理</th></tr></thead><tbody>${body}</tbody></table></div>`;
}
function renderOperations(state: AdminState, actions: ReadonlyMap<string, AdminJobAction>): string {
  const parts = partitionAdminJobs(state.jobs), view = state.jobsView ?? 'current', kind = state.jobsKind ?? 'service';
  const selected = parts[view], rows = filterJobKind(selected, kind);
  const views: Array<[AdminJobsView, string]> = [['current', '当前任务'], ['history', '历史记录']];
  const kinds: Array<[AdminJobsKind, string]> = [['service', '答题服务'], ['collection', '题库采集'], ['all', '全部类型']];
  return `<section class="card">
    <div class="panel-head">
      <div class="seg" role="group" aria-label="任务记录范围">${views.map(([value, label]) => `<button class="${view === value ? 'on' : ''}" data-admin-jobs-view="${value}" aria-pressed="${view === value}">${label}<span>${filterJobKind(parts[value], kind).length}</span></button>`).join('')}</div>
      <div class="seg" role="group" aria-label="任务类型">${kinds.map(([value, label]) => `<button class="${kind === value ? 'on' : ''}" data-admin-jobs-kind="${value}" aria-pressed="${kind === value}">${label}<span>${filterJobKind(selected, value).length}</span></button>`).join('')}</div>
    </div>
    <p class="cap muted">${view === 'current' ? '按用户和相同范围显示最新任务；执行中和排队中的任务全部保留，旧的尝试在历史记录里。' : '保留被新任务替代的旧尝试，以及没有关联账号的已结束记录。历史状态只代表当次执行。'}
      <br>显示 ${rows.length} 条 · 基于最近 ${state.jobs.length} 条记录${state.jobs.length >= 100 ? '（服务端上限 100 条）' : '（最多 100 条）'}</p>
    ${adminJobRows(rows, false, view === 'history', actions)}</section>`;
}
export function renderAdmin(page: AdminPage, state: AdminState, actions: ReadonlyMap<string, AdminJobAction> = new Map()): string {
  const current = partitionAdminJobs(state.jobs).current;
  const services = filterJobKind(current, 'service');
  const collectionAttention = filterJobKind(current, 'collection').filter(needsAttention).length;
  const titles: Record<AdminPage, string> = { overview: '服务概览', users: '用户管理', operations: '任务运营', audit: '操作审计' };
  const eyebrow: Record<AdminPage, string> = { overview: '每 6 秒自动更新', users: '最多 100 位', operations: '最多 100 条记录', audit: '最多 100 条记录' };
  const header = `<div class="head"><div><p class="date">${eyebrow[page]}</p><h1>${titles[page]}</h1></div>
    <div class="actions"><button class="btn" data-action="admin-refresh" ${state.loading ? 'disabled' : ''}>${icon('refresh')}${state.loading ? '更新中…' : '刷新'}</button></div></div>`;
  if (state.error) return `${header}<div class="sticky-note" role="status">${icon('alert')}<div>管理数据没更新上，稍后刷新。已经在跑的任务不受影响。</div></div>`;
  if (page === 'overview') {
    return `${header}${renderModelBalanceWarning(state.overview)}${kpis(state, services)}
      <div class="grid-2">${renderActiveJobs(services, actions)}${renderAttentionGroups(services)}</div>
      <section class="card"><div class="panel-head"><h2>需要跟进的任务<span>只看当前记录</span></h2>
        <span class="run-acts"><button class="link" data-admin-show-jobs="service">任务运营${icon('right')}</button>${collectionAttention ? `<button class="link" data-admin-show-jobs="collection">${collectionAttention} 项采集待核对</button>` : ''}</span></div>
        ${adminJobRows(services, true, false, actions)}</section>
      <div class="grid-2">${renderCapacity(state.overview)}${renderUsage(state.overview)}</div>`;
  }
  if (page === 'users') {
    const userSearch = text(state.userSearch).toLocaleLowerCase();
    const userFilter = state.userFilter === 'active' || state.userFilter === 'disabled' ? state.userFilter : 'all';
    const filteredUsers = state.users.filter(user => {
      const matchesFilter = userFilter === 'all' || (userFilter === 'disabled' ? user.status === 'disabled' : user.status !== 'disabled');
      if (!matchesFilter) return false;
      if (!userSearch) return true;
      return [user.name, user.login, user.upstream_id, user.id].some(value => String(value ?? '').toLocaleLowerCase().includes(userSearch));
    });
    const filterButton = (value: 'all' | 'active' | 'disabled', label: string): string =>
      `<button class="${userFilter === value ? 'on' : ''}" type="button" data-admin-user-filter="${value}" aria-pressed="${userFilter === value}">${label}</button>`;
    const body = filteredUsers.map(user => `<tr>
      <td><div class="cell-strong">${e(user.name || user.login || '同学')}</div><small class="cell-meta">头歌 ID ${e(user.upstream_id ?? user.id)}</small></td>
      <td class="nowrap">${user.role === 'admin' ? '管理员' : '用户'}</td>
      <td class="nowrap"><span class="state ${user.status === 'disabled' ? '' : 'done'}">${user.status === 'disabled' ? icon('bang') : `<span class="tick">${icon('tick')}</span>`}${e(statusLabels[user.status] ?? '正常')}</span></td>
      <td class="nowrap muted">${e(dateTime(user.last_login_at ?? user.updated_at))}</td>
      <td class="acts">${user.role !== 'admin'
        ? `<button class="link" data-user-status="${e(user.id)}" data-next-status="${user.status === 'disabled' ? 'active' : 'disabled'}">${user.status === 'disabled' ? '恢复服务' : '暂停服务'}</button>`
        : '<span class="muted">服务端授权</span>'}</td>
    </tr>`).join('');
    return `${header}<section class="card">
      <div class="panel-head"><h2>近期登录用户<span>${filteredUsers.length} / ${state.users.length} 位</span></h2>
        <span class="run-acts"><label class="search">${icon('search')}<span class="sr-only">搜索用户</span><input data-admin-user-search type="search" placeholder="搜索昵称 / 头歌账号" value="${e(state.userSearch ?? '')}"></label>
        <span class="seg" role="group" aria-label="用户状态">${filterButton('all', '全部')}${filterButton('active', '正常')}${filterButton('disabled', '已暂停')}</span></span></div>
      ${body ? `<div class="table-wrap"><table class="ledger-table"><thead><tr><th>头歌用户</th><th>角色</th><th>服务状态</th><th>最近登录</th><th>操作</th></tr></thead><tbody>${body}</tbody></table></div>`
        : '<div class="empty"><h3>没有匹配的用户</h3><p>换个关键字或筛选条件。</p></div>'}</section>`;
  }
  if (page === 'operations') return `${header}${renderOperations(state, actions)}`;
  const audit = state.audit.map(row => `<tr>
    <td class="nowrap muted">${e(dateTime(row.created_at))}</td>
    <td class="nowrap">${e(row.actor_id ?? '系统')}</td>
    <td>${e(row.action || '—')}</td>
    <td class="mono">${e(row.target_id ?? '—')}</td>
    <td class="nowrap">${e(row.outcome || '已记录')}</td>
  </tr>`).join('');
  return `${header}<section class="card"><div class="panel-head"><h2>最近管理操作<span>${state.audit.length} 条</span></h2><span class="muted">只有管理员看得到</span></div>
    ${audit ? `<div class="table-wrap"><table class="ledger-table"><thead><tr><th>时间</th><th>操作人</th><th>操作</th><th>目标</th><th>结果</th></tr></thead><tbody>${audit}</tbody></table></div>`
      : '<div class="empty"><h3>还没有管理操作记录</h3><p>暂停用户、重试任务这些操作都会记在这里。</p></div>'}</section>`;
}
function renderAdminSteps(data: Record<string, unknown>, depth = 0): string {
  if (depth > 4) return '';
  const children = resultChildren(data).map((value, index) => ({ value, index })).sort((a, b) => Number(isAttention(b.value) || isAttention(record(b.value.result))) - Number(isAttention(a.value) || isAttention(record(a.value.result))) || a.index - b.index).map(item => item.value);
  return children.length ? `<ol class="steps">${children.map(value => {
    const item = record(value);
    const nested = record(item.result);
    const issue = deepestIssue({ ...item, ...nested });
    const reason = text(issue.reason_code);
    const position = count(item.position);
    const score = item.score !== undefined && item.max_score !== undefined ? `<p class="meta">得分 ${e(item.score)} / ${e(item.max_score)}</p>` : '';
    return `<li><div class="step-head"><strong>${position ? `第 ${position} 关 · ` : ''}${e(item.title || item.subject || '名称待补充')}</strong><span class="state">${e(statusLabels[text(item.status)] ?? '状态待核对')}</span></div>${item.course_name ? `<p class="meta">${e(item.course_name)}</p>` : ''}${score}${reason && isAttention({ ...item, ...nested }) ? `<p class="step-reason">${e(reasonLabels[reason.toLowerCase()] ?? '执行未完成，请查看技术记录')}</p>` : ''}${renderAdminSteps(Object.keys(nested).length ? nested : item, depth + 1)}</li>`;
  }).join('')}</ol>` : '';
}
/**
 * renderAdminProgress is the operator view of a job's assignment tree. It uses
 * the same component as the consumer page, with the raw reason code shown when
 * no friendly label exists, so a new state is still traceable.
 */
function renderAdminProgress(job: AdminJob): string {
  const items = adminJobItems(job);
  if (!items.length) return '';
  const counts = progressCounts(items);
  const live = job.context?.progress;
  const tally = `已完成 ${counts.completed} · 进行中 ${counts.processing} · 待处理 ${counts.waiting} · 需跟进 ${counts.support}`;
  const updated = live ? '' : '<p class="muted small-note">以下为本次执行结束时保留的进度。</p>';
  return `<section class="admin-progress"><div class="sheet-note step-head"><h3 class="sheet-h">作业与关卡进度</h3><span class="muted">${e(tally)}</span></div>${updated}${renderProgressTree(items, { admin: true, levels: true, parentActive: isPending(job) })}</section>`;
}
function renderAttentionHomework(items: ReturnType<typeof jobContext>['attention']): string {
  if (!items.length) return '';
  return `<section class="sheet-note"><h3 class="sheet-h">需要跟进的作业 · ${items.length} 份</h3><ol class="steps">${items.map(item => `<li><strong>${e(item.name || '作业名称待补充')}</strong>${item.course_name ? `<p class="meta">${e(item.course_name)}</p>` : ''}<p>${e(reasonLabels[text(item.reason_code).toLowerCase()] || ('reason' in item && text(item.reason) && /\p{Script=Han}/u.test(text(item.reason)) ? text(item.reason) : '需要跟进，请查看该作业执行明细'))}</p></li>`).join('')}</ol></section>`;
}
function technicalResult(data: Record<string, unknown>, depth = 0): Record<string, unknown> {
  if (depth > 5) return {};
  const keys = ['exercise_id', 'total_questions', 'completed_questions', 'submitted', 'score_known', 'scan_complete', 'scan_exhausted', 'title', 'subject', 'course', 'course_name', 'homework_id', 'challenge_id', 'position', 'status', 'reason_code', 'score', 'max_score', 'diagnostics', 'model_calls', 'reasons', 'collected', 'duplicates', 'skipped', 'unconfirmed', 'failed', 'limit_reached', 'total_homeworks', 'completed_homeworks', 'needs_service_homeworks', 'pending_homeworks', 'stop_reason'];
  const result = Object.fromEntries(keys.filter(key => data[key] !== undefined).map(key => [key, data[key]]));
  const children = resultChildren(data);
  if (children.length) result.steps = children.map(item => ({ ...technicalResult(item, depth + 1), ...Object.keys(record(item.result)).length ? { result: technicalResult(record(item.result), depth + 1) } : {} }));
  return result;
}
export function renderAdminJobDetail(job: AdminJob, historical = false, pending?: AdminJobAction): string {
  const data = record(job.result);
  const context = jobContext(job), collecting = job.request.mode === 'collect_verified', status = displayStatus(job);
  if (historical) { status.tone = 'limited'; if (needsAttention(job)) status.label = '当次未完成'; }
  const scope = collecting ? '题库采集（只读）' : job.request.scope === 'all' ? '全部课程' : job.request.scope === 'course' ? '整门课程' : job.request.scope === 'current' ? '单个关卡' : job.request.scope === 'exercise' ? '在线考试' : '单份作业';
  const raw = { job_id: job.id, owner_id: job.owner_id ?? job.owner, request: job.request, status: job.status, phase: job.phase, error_code: job.error_code, message: job.message, context_reason_code: job.context?.summary?.reason_code, context_diagnostics: job.context?.summary?.diagnostics, stop_reason: job.context?.summary?.stop_reason, result: technicalResult(data) };
  const facts: Array<[string, string]> = [
    ['用户', context.user],
    ['任务范围', scope],
    ...(collecting ? [] : [['课程', context.course] as [string, string], [job.request.scope === 'exercise' ? '考试' : '作业', homeworkLabel(job, context)] as [string, string]]),
    ...(collecting || job.request.scope === 'exercise' ? [] : [['当前关卡', context.level ? `${context.position ? `第 ${context.position} 关 · ` : ''}${context.level}` : '尚未进入关卡'] as [string, string]]),
    [['course', 'all'].includes(job.request.scope ?? '') ? '整批最后阶段' : '执行阶段', phaseLabels[job.phase] ?? '阶段待补充'],
    ['创建时间', dateTime(job.created_at)],
    ['模型调用', `${count(data.model_calls)} 次${!count(data.model_calls) ? '（未调用模型）' : ''}`],
    ['记录费用（含估算）', `$${(count(data.cost_micro_usd) / 1000000).toFixed(6)}`],
  ];
  return `<header class="sheet-head"><div class="eyebrow">${historical ? '<span class="tag">历史</span>' : ''}${e(collecting ? '题库采集' : '任务执行')} · ${e(status.label)}</div>
    <h2 id="modal-title">${e(context.user)}</h2>
    <p class="verdict">${e(collecting ? '采集已通过题目' : `${context.course} · ${homeworkLabel(job, context)}`)}</p>
    <p class="sheet-meta num">${e(progressLabel(job))}</p></header>
    <div class="sheet-body">
      ${historical ? '<p class="memo sheet-note">历史执行记录：下面的状态和原因只代表当次尝试，不计入当前待处理。</p>' : ''}
      <p class="memo sheet-note${needsAttention(job) ? ' bad' : ''}">${e(readableReason(job))}${count(job.context?.summary?.pending_homeworks) ? ` 另有 ${count(job.context?.summary?.pending_homeworks)} 份作业尚未开始，不计作已完成。` : ''}</p>
      <dl class="ledger cap sheet-note">${facts.map(([label, value]) => `<div><dt>${e(label)}</dt><span class="dots"></span><dd>${e(value)}</dd></div>`).join('')}</dl>
      ${collecting ? renderCollectionSummary(job) : renderAdminProgress(job) + renderAttentionHomework(context.attention) + renderAdminSteps(data)}
      <details class="operator-technical"><summary>技术记录（原始标识与诊断）</summary><pre>${e(JSON.stringify(raw, null, 2))}</pre></details>
    </div>
    <footer class="sheet-foot">${!historical ? adminResumeButton(job, false, pending, true) : ''}<button class="btn" data-action="close-modal">关闭</button></footer>`;
}

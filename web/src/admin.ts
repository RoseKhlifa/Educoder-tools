import type { AdminAudit, AdminJob, AdminOverview, AdminUser, AccessCard, CollectionSummary } from './types.js';
import { count, dateTime, escapeHtml as e, record } from './ui.js';
import { jobProgress, serviceLabels, serviceStatus } from './jobs.js';
export type AdminPage = 'overview' | 'users' | 'operations' | 'audit' | 'cards';
export type AdminJobsView = 'current' | 'history';
export type AdminJobsKind = 'service' | 'collection' | 'all';
export interface AdminState { overview: AdminOverview; users: AdminUser[]; jobs: AdminJob[]; audit: AdminAudit[]; cards: AccessCard[]; loading: boolean; error: boolean; jobsView?: AdminJobsView; jobsKind?: AdminJobsKind }
const statusLabels: Record<string, string> = { queued: '等待安排', running: '执行中', succeeded: '已完成', failed: '需要处理', uncertain: '待核验', interrupted: '已中断', canceled: '已停止', active: '正常', disabled: '已暂停', passed: '已通过', completed: '已完成', skipped: '已满分，已跳过', skipped_optional: '选做，已跳过', planned: '尚未开始', needs_service: '需要跟进', verification_unconfirmed: '待核验', processing: '执行中', support: '需要跟进', waiting: '等待安排', deadline_passed: '已截止' };
const phaseLabels: Record<string, string> = { starting: '准备执行', preflight: '核对关卡清单', verified: '已核验当前结果', service_pending: '等待服务人员跟进', queued: '等待安排', loading: '读取课程与作业', loading_resources: '读取题目图片', preparing: '准备执行', collecting: '采集已验证题目', bank_lookup: '查询题库答案', bank_saved: '保存已验证答案', generating: '生成答案', answering: '生成答案', solving: '处理题目', validating: '校验答案格式', saving: '保存代码', selftesting: '执行自测', selftest: '执行自测', submitting: '提交答案', evaluating: '等待评测', polling: '读取评测结果', verifying: '核验完成状态', reconciling: '核对提交结果', repairing: '根据反馈修复', completed: '执行结束', failed: '执行结束，等待处理', canceled: '已停止', processing: '执行中', running: '执行中' };
const reasonLabels: Record<string, string> = {
  answer_readback_unconfirmed: '保存后的答案回读尚未确认', answer_save_unconfirmed: '答案保存响应尚未确认', paper_changed: '题卷或选项已变化，需要重新核对', checkpoint_persistence_failed: '执行记录保存失败', commit_readiness_unconfirmed: '交卷前检查尚未确认', unanswered_questions: '仍有未作答项目', model_provider_missing: '考试模型尚未配置', model_generation_failed: '考试答案生成失败', model_budget_exhausted: '达到本次内部用量上限', previous_execution_interrupted: '上次执行已中断，需要先核对结果', submitted_state_unconfirmed: '交卷状态尚未确认', exercise_candidate_contract: '生成答案与当前题卷不匹配', exercise_media_unverified: '题目中的媒体内容尚未核验',
  action_required: '考试入场事项需要在头歌处理', locked: '考试已锁定，需要教师解锁', expired: '考试已截止', already_submitted: '考试已经交卷，未重复提交', unsupported_question: '考试包含尚未适配的题型', identity_mismatch: '考试或题目身份不一致', answer_invalid: '考试答案未通过格式核验', grading_pending: '考试已交卷，成绩尚未公布', not_started: '考试尚未开始', proctoring_required: '考试需要原站监考或入场确认',
  editor_changed: '保存后的源码与候选答案不一致，尚未确认评测通过',
  model_error: '模型服务调用失败', model_timeout: '模型响应超时', model_network_error: '模型服务连接失败', collection_stopped: '只读题库采集已停止，请查看采集统计和跳过原因', service_restarted: '服务重启中断了任务，需要核对已完成结果',
  verification_unconfirmed: '头歌返回的完成或分值信息不足，尚未核验通过', submission_uncertain: '提交结果尚未确认，需要先读取头歌结果', attempts_exhausted: '已达到本次修复次数，仍未通过', budget_exhausted: '已达到本次任务费用上限', budget_unavailable: '执行预算暂不可用', selftest_failed: '代码自测未通过', selftest_connection_failed: '自测连接失败', selftest_stream_unconfirmed: '自测输出未完整确认', invalid_candidate: '生成的答案未满足提交要求', invalid_output_json: '模型返回内容格式不正确', model_response_invalid: '模型返回内容格式不正确', model_output_truncated: '模型返回内容被截断', model_rate_limited: '模型请求受到频率限制', execution_failed: '执行未完成，需要检查原始响应', unsupported_multifile: '当前作业的多文件结构尚未支持', unsupported_roster: '关卡清单不完整，暂停执行', unsupported_homework: '当前作业类型尚未支持', usage_persistence_failed: '执行用量保存失败', verification_persistence_failed: '完成核验结果保存失败', course_sync_failed: '课程列表读取失败', homework_sync_failed: '作业列表读取失败', course_identity_invalid: '课程标识不完整，暂停执行', homework_identity_invalid: '作业标识不完整，暂停执行', course_limit: '课程数量达到本次任务上限', homework_limit: '作业数量达到本次任务上限', deadline_passed: '作业已经截止', no_required_levels: '没有需要执行的必做关卡', deadline_exceeded: '整批执行达到时间上限，尚未开始此作业', time_limit: '整批执行达到时间上限，尚未开始此作业', context_deadline_exceeded: '整批执行达到时间上限，尚未开始此作业', collection_failed: '题库采集未完成，请检查采集明细', collect_failed: '题库采集未完成，请检查采集明细', request_byte_or_time_limit: '题库采集达到读取量或时间上限', limit_reached: '题库采集达到本次上限', service_required: '存在尚未完成的作业，需跟进', canceled: '管理员或用户已停止执行', interrupted: '服务重启中断了执行，需要核对已完成结果',
};
const text = (value: unknown): string => typeof value === 'string' ? value.trim() : '';
const pendingStatuses = new Set(['queued', 'running', 'waiting', 'processing']);
const terminalStatuses = new Set(['succeeded', 'failed', 'canceled', 'uncertain', 'interrupted', 'completed', 'support', 'submitted', 'awaiting_review', 'graded']);
function targetKey(job: AdminJob): string {
  const owner = String(job.owner_id ?? job.owner ?? job.context?.user?.id ?? '').trim();
  const request = job.request;
  if (!owner) return `record:${job.id}`;
  if (request.mode === 'collect_verified') return JSON.stringify([owner, 'collect_verified']);
  const scope = request.scope || (request.game ? 'current' : 'homework');
  // Only submitted request identities define a service target. A batch's
  // highlighted failed homework and current progress are display metadata.
  if (scope !== 'all' && !request.course || ['homework', 'current'].includes(scope) && !request.homework_id || scope === 'current' && !request.game) return `record:${job.id}`;
  if (scope === 'exercise' && !request.exercise_id) return `record:${job.id}`;
  return JSON.stringify([owner, request.mode, scope, request.course ?? '', request.homework_id ?? 0, request.game ?? '', request.exercise_id ?? 0]);
}
export function partitionAdminJobs(jobs: AdminJob[]): { current: AdminJob[]; history: AdminJob[] } {
  const ordered = jobs.map((job, index) => ({ job, index })).sort((a, b) => {
    const created = Date.parse(b.job.created_at) - Date.parse(a.job.created_at);
    return (Number.isFinite(created) ? created : 0) || a.index - b.index;
  }).map(item => item.job);
  const activeTargets = new Set(ordered.filter(job => pendingStatuses.has(job.status)).map(targetKey));
  const current: AdminJob[] = [], history: AdminJob[] = [], seen = new Set<string>();
  for (const job of ordered) {
    const target = targetKey(job);
    const terminal = terminalStatuses.has(job.status);
    if (terminal && (job.context?.user?.linked === false || activeTargets.has(target) || seen.has(target))) history.push(job);
    else current.push(job);
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
    return { label: count(collectionSummary(job).collected) === 0 ? '采集完成（无新增）' : '采集完成', tone: 'completed' };
  }
  return { label: statusLabels[job.status] ?? '处理中', tone: job.status === 'succeeded' ? 'completed' : needsAttention(job) ? 'support' : 'processing' };
}
const collectionReasons: Record<string, { label: string; unit: string; note?: string }> = {
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
  const counters = [['新增入库答案', count(summary.collected), '条'], ['题库已有答案', count(summary.duplicates), '条'], ['跳过', count(summary.skipped), '项'], ['其中证据未确认', count(summary.unconfirmed), '项'], ['读取或保存失败', count(summary.failed), '次']];
  const reasons = Object.entries(record(summary.reasons)).filter(([, value]) => count(value) > 0);
  return `<section class="collection-summary"><h3>本次只读采集结果</h3><dl class="collection-counters">${counters.map(([label, value, unit]) => `<div><dt>${label}</dt><dd>${value} ${unit}</dd></div>`).join('')}</dl><p class="meta">新增和已有答案按答案条目计数；跳过按课堂、作业或关卡计项，不代表跳过的题目总数。证据未确认是跳过的子集，不重复相加。</p>${count(summary.collected) === 0 && count(summary.duplicates) > 0 ? '<p class="detail-note">本次没有新增答案，是因为已核验的答案在题库中已存在；已有答案仍可复用。</p>' : ''}<h3>跳过、失败与终止原因</h3><ul class="collection-reasons">${reasons.map(([code, value]) => { const reason = collectionReasons[code]; return `<li><div><strong>${e(reason?.label ?? '其他采集记录，请展开技术记录查看原因代码')}</strong><span>${count(value)} ${e(reason?.unit ?? '项')}</span></div>${reason?.note ? `<p>${e(reason.note)}</p>` : ''}</li>`; }).join('') || '<li><p>没有记录跳过、失败或终止原因。</p></li>'}</ul></section>`;
}
function readableReason(job: AdminJob): string {
  if (job.request.scope === 'exercise' && ['submitted', 'awaiting_review', 'graded'].includes(serviceStatus(job))) return serviceStatus(job) === 'graded' ? '已交卷，成绩已公布' : '已交卷，成绩尚未公布';
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
      if (stopReason === 'time_limit') return '整批执行达到 30 分钟时限，已保留完成结果；未开始的作业等待重新安排';
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
  if (['queued', 'waiting'].includes(job.status)) return '等待后台安排';
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
function metrics(data: AdminOverview, jobs: AdminJob[]): string {
  const capacity = record(record(data.capacity).jobs ?? data.capacity);
  const users = record(data.users);
  const values: Array<[string, unknown]> = [ ['平台用户', data.total_users ?? users.total_users], ['执行中', data.running_jobs ?? data.running ?? capacity.running], ['等待安排', data.queued_jobs ?? data.queued ?? capacity.queued], ['当前需跟进', jobs.filter(needsAttention).length] ];
  return `<div class="admin-metrics">${values.map(([label, value]) => `<div><span>${label}</span><strong>${typeof value === 'number' ? count(value) : '—'}</strong></div>`).join('')}</div>`;
}
function adminJobRows(jobs: AdminJob[], limited = false, history = false): string {
  const rows = limited ? jobs.filter(needsAttention).slice(0, 10) : jobs;
  return `<div class="table-scroll"><table class="admin-jobs-table"><thead><tr><th>用户</th><th>课程 / 任务范围</th><th>作业 / 当前关卡</th><th>状态 / 完成进度</th><th>具体原因</th><th>模型 / 记录费用</th><th>管理</th></tr></thead><tbody>${rows.map(job => {
    const data = record(job.result);
    const context = jobContext(job), collecting = job.request.mode === 'collect_verified', status = displayStatus(job);
    const scope = collecting ? '题库采集 · 只读' : job.request.scope === 'all' ? '全部课程' : job.request.scope === 'course' ? '整门课程' : job.request.scope === 'current' ? '单个关卡' : job.request.scope === 'exercise' ? '在线考试' : '单份作业';
    const work = collecting ? '采集已通过题目' : homeworkLabel(job, context);
    const extra = !collecting && context.attention.length > 1 ? `<small class="admin-attention">另有 ${context.attention.length - 1} 份需跟进，详情查看</small>` : '';
    const level = job.request.scope === 'exercise' ? '<small>在线考试</small>' : context.level ? `<small>${context.position ? `第 ${context.position} 关 · ` : ''}${e(context.level)}</small>` : '<small>尚未进入关卡</small>';
    return `<tr><td class="admin-user-cell"><strong>${e(context.user)}</strong></td><td class="admin-course-cell"><strong>${e(collecting ? '全部已通过任务' : context.course)}</strong><small>${scope}</small></td><td class="admin-work-cell"><strong>${e(work)}</strong>${collecting ? '' : level}${extra}<small class="admin-created-at">${e(dateTime(job.created_at))} 创建</small></td><td class="admin-progress-cell">${history ? '<span class="admin-history-label">历史记录</span>' : ''}<span class="badge ${history ? 'limited' : status.tone}">${e(history && needsAttention(job) ? '当次未完成' : status.label)}</span><small>${e(progressLabel(job))}</small>${!collecting && count(context.batch.total_levels) && !job.context?.summary?.progress_label?.includes('当前作业') ? `<small>当前作业 ${count(context.batch.completed_levels)} / ${count(context.batch.total_levels)} 个关卡</small>` : ''}</td><td class="reason-cell">${e(readableReason(job))}${count(job.context?.summary?.pending_homeworks) ? `<small>另有 ${count(job.context?.summary?.pending_homeworks)} 份尚未开始</small>` : ''}</td><td class="admin-usage-cell">${count(data.model_calls)} 次<small>$${(count(data.cost_micro_usd) / 1000000).toFixed(6)}</small>${count(data.model_calls) === 0 && !collecting ? '<small>未调用模型</small>' : ''}</td><td><button class="text-button" data-admin-job-detail="${e(job.id)}">详情</button>${['queued', 'running'].includes(job.status) ? `<button class="text-button danger" data-admin-cancel="${e(job.id)}">停止</button>` : ''}</td></tr>`;
  }).join('') || `<tr><td colspan="7"><div class="empty"><h3>${history ? '暂无历史记录' : limited ? '当前没有需要跟进的服务' : '暂无当前任务'}</h3><p>${history ? '被新安排替代的旧记录会保留在这里。' : limited ? '旧记录已收进任务运营的历史记录。' : '可切换任务类型查看题库采集。新的用户安排会显示在这里。'}</p></div></td></tr>`}</tbody></table></div>`;
}
function renderOperations(state: AdminState): string {
  const parts = partitionAdminJobs(state.jobs), view = state.jobsView ?? 'current', kind = state.jobsKind ?? 'service';
  const selected = parts[view], rows = filterJobKind(selected, kind);
  const views: Array<[AdminJobsView, string]> = [['current', '当前任务'], ['history', '历史记录']];
  const kinds: Array<[AdminJobsKind, string]> = [['service', '答题服务'], ['collection', '题库采集'], ['all', '全部类型']];
  return `<section class="panel"><div class="admin-job-toolbar"><div class="admin-job-tabs" role="group" aria-label="任务记录范围">${views.map(([value, label]) => `<button class="${view === value ? 'selected' : ''}" data-admin-jobs-view="${value}" aria-pressed="${view === value}">${label}<span>${filterJobKind(parts[value], kind).length}</span></button>`).join('')}</div><div class="admin-job-kind" role="group" aria-label="任务类型">${kinds.map(([value, label]) => `<button class="${kind === value ? 'selected' : ''}" data-admin-jobs-kind="${value}" aria-pressed="${kind === value}">${label}<span>${filterJobKind(selected, value).length}</span></button>`).join('')}</div></div><p class="admin-job-explanation">${view === 'current' ? '按用户和相同任务范围展示最新安排；执行中、排队中的任务全部保留。旧尝试可在历史记录查看。' : '保留被后续安排替代的旧尝试及未关联账号的已结束记录。历史状态仅代表当次执行，不计入当前待处理。'}</p><div class="section-heading admin-job-list-heading"><h2>${view === 'current' ? kind === 'collection' ? '当前题库采集' : '当前服务任务' : '历史执行记录'}</h2><span class="muted">显示 ${rows.length} 条 · 基于最近 ${state.jobs.length} 条记录${state.jobs.length >= 100 ? '（服务端上限 100 条）' : '（最多 100 条）'}</span></div>${adminJobRows(rows, false, view === 'history')}</section>`;
}
export function renderAdmin(page: AdminPage, state: AdminState): string {
  const current = partitionAdminJobs(state.jobs).current;
  const services = filterJobKind(current, 'service');
  const collectionAttention = filterJobKind(current, 'collection').filter(needsAttention).length;
  const titles: Record<AdminPage, string> = { overview: '服务概览', users: '用户管理', operations: '任务运营', audit: '操作审计', cards: '服务卡密' };
  const intro: Record<AdminPage, string> = { overview: '用户安心交给我们，异常留在这里解决。', users: '统一查看服务状态，管理用户访问权限。', operations: '查看处理原因与用量，跟进尚未完成的服务。', audit: '管理操作与权限变化，都有迹可循。', cards: '管理服务凭证，暂不引入价格或套餐。' };
  const header = `<div class="page-heading"><div><h1>${titles[page]}</h1><p>${intro[page]}</p></div><button class="button" data-action="admin-refresh" ${state.loading ? 'disabled' : ''}>${state.loading ? '更新中…' : '刷新数据'}</button></div>`;
  if (state.error) return `${header}<div class="service-notice" role="status">管理数据暂未更新，请稍后刷新。已有任务会继续执行。</div>`;
  if (page === 'overview') return `${header}${metrics(state.overview, services)}<section class="panel"><div class="section-heading"><h2>需要跟进的任务</h2><button class="text-button" data-admin-show-jobs="service">查看当前任务</button></div><p class="admin-job-explanation">仅列当前答题服务；旧尝试保留在历史记录。${collectionAttention ? `<button class="text-button" data-admin-show-jobs="collection">另有 ${collectionAttention} 项只读采集需核对</button>` : ''}</p>${adminJobRows(services, true)}</section><div class="admin-bottom"><section class="panel"><h2>模型与执行容量</h2><dl class="info-list">${Object.entries({ ...record(record(state.overview.capacity).jobs), ...record(state.overview.capacity) }).filter(([key]) => ['workers', 'model_concurrency', 'running', 'queued', 'model_configured', 'per_owner_running'].includes(key)).map(([key, value]) => `<div><dt>${e(({ workers: '后台任务并发', model_concurrency: '模型并发', running: '正在运行', queued: '等待安排', model_configured: '模型配置', per_owner_running: '单用户并发' } as Record<string, string>)[key])}</dt><dd>${e(typeof value === 'boolean' ? value ? '已配置' : '待配置' : value)}</dd></div>`).join('') || '<div><dt>状态</dt><dd>暂无容量数据</dd></div>'}</dl></section><section class="panel"><h2>模型用量</h2><dl class="info-list">${[['模型调用', count(record(state.overview.usage).model_calls)], ['输入 Token', count(record(state.overview.usage).input_tokens)], ['输出 Token', count(record(state.overview.usage).output_tokens)], ['记录费用（含估算）', '$' + (count(record(state.overview.usage).cost_micro_usd) / 1000000).toFixed(6)], ['已确认费用', '$' + (count(record(state.overview.usage).settled_cost_micro_usd) / 1000000).toFixed(6)], ['待核对调用', count(record(state.overview.usage).unknown_calls)], ['预留费用', '$' + (count(record(state.overview.usage).reserved_micro_usd) / 1000000).toFixed(6)]].map(([label, value]) => `<div><dt>${e(label)}</dt><dd>${e(value)}</dd></div>`).join('')}</dl><h2 class="billing-heading">卡密与收费</h2><p class="muted">卡密规则待定。当前不展示价格、套餐或付款入口，也不向用户展示模型额度。</p><div class="detail-note">服务策略由管理员统一维护。</div></section></div>`;
  if (page === 'users') return `${header}<section class="panel"><div class="section-heading"><h2>近期登录用户</h2><span class="muted">返回 ${state.users.length} 位，最多 100 位</span></div><div class="table-scroll"><table><thead><tr><th>头歌用户</th><th>角色</th><th>服务状态</th><th>最近登录</th><th>操作</th></tr></thead><tbody>${state.users.map(user => `<tr><td><strong>${e(user.name || user.login || '同学')}</strong><small>头歌 ID ${e(user.upstream_id ?? user.id)}</small></td><td>${user.role === 'admin' ? '管理员' : '用户'}</td><td><span class="badge ${user.status === 'disabled' ? 'support' : 'completed'}">${statusLabels[user.status] ?? '正常'}</span></td><td>${e(dateTime(user.last_login_at ?? user.updated_at))}</td><td>${user.role !== 'admin' ? `<button class="text-button ${user.status === 'disabled' ? '' : 'danger'}" data-user-status="${e(user.id)}" data-next-status="${user.status === 'disabled' ? 'active' : 'disabled'}">${user.status === 'disabled' ? '恢复服务' : '暂停服务'}</button>` : '<span class="muted">服务端授权</span>'}</td></tr>`).join('') || '<tr><td colspan="5"><div class="empty">暂无用户</div></td></tr>'}</tbody></table></div></section>`;
  if (page === 'cards') return `${header}<section class="panel"><div class="section-heading"><div><h2>最近卡密记录</h2><p class="muted">新建的卡密仅显示一次，请妥善交付给用户。</p></div><button class="button primary" data-action="create-card">新建卡密</button></div><div class="table-scroll"><table><thead><tr><th>备注</th><th>状态</th><th>绑定用户</th><th>创建时间</th><th>管理</th></tr></thead><tbody>${state.cards.map(card => `<tr><td>${e(card.label || '服务卡密')}<small>${e(card.masked_code || '')}</small></td><td>${e(({ active: '可使用', used: '已使用', revoked: '已停用', expired: '已过期' } as Record<string, string>)[card.status ?? 'active'] ?? '可使用')}</td><td>${e(card.bound_user_id ?? '未绑定')}</td><td>${e(dateTime(card.created_at))}</td><td>${card.status !== 'revoked' ? `<button class="text-button danger" data-revoke-card="${e(card.id)}">停用</button>` : '—'}</td></tr>`).join('') || '<tr><td colspan="5"><div class="empty">暂无卡密记录</div></td></tr>'}</tbody></table></div></section>`;
  if (page === 'operations') return `${header}${renderOperations(state)}`;
  return `${header}<section class="panel"><div class="section-heading"><h2>最近管理操作</h2><span class="muted">最多 100 条记录</span></div><div class="table-scroll"><table><thead><tr><th>时间</th><th>操作人</th><th>操作</th><th>目标</th><th>结果</th></tr></thead><tbody>${state.audit.map(row => `<tr><td>${e(dateTime(row.created_at))}</td><td>${e(row.actor_id ?? '系统')}</td><td>${e(row.action || '—')}</td><td>${e(row.target_id ?? '—')}</td><td>${e(row.outcome || '已记录')}</td></tr>`).join('') || '<tr><td colspan="5"><div class="empty">暂无管理操作记录</div></td></tr>'}</tbody></table></div></section>`;
}
function renderAdminSteps(data: Record<string, unknown>, depth = 0): string {
  if (depth > 4) return '';
  const children = resultChildren(data).map((value, index) => ({ value, index })).sort((a, b) => Number(isAttention(b.value) || isAttention(record(b.value.result))) - Number(isAttention(a.value) || isAttention(record(a.value.result))) || a.index - b.index).map(item => item.value);
  return children.length ? `<ol class="operator-steps">${children.map(value => {
    const item = record(value);
    const nested = record(item.result);
    const issue = deepestIssue({ ...item, ...nested });
    const reason = text(issue.reason_code);
    const position = count(item.position);
    const score = item.score !== undefined && item.max_score !== undefined ? `<p class="meta">得分 ${e(item.score)} / ${e(item.max_score)}</p>` : '';
    return `<li><div class="operator-step-head"><strong>${position ? `第 ${position} 关 · ` : ''}${e(item.title || item.subject || '名称待补充')}</strong><span class="badge">${e(statusLabels[text(item.status)] ?? '状态待核对')}</span></div>${item.course_name ? `<p class="meta">${e(item.course_name)}</p>` : ''}${score}${reason && isAttention({ ...item, ...nested }) ? `<p class="operator-reason">${e(reasonLabels[reason.toLowerCase()] ?? '执行未完成，请查看技术记录')}</p>` : ''}${renderAdminSteps(Object.keys(nested).length ? nested : item, depth + 1)}</li>`;
  }).join('')}</ol>` : '';
}
function renderAttentionHomework(items: ReturnType<typeof jobContext>['attention']): string {
  if (!items.length) return '';
  return `<section class="admin-attention-list"><h3>需要跟进的作业 · ${items.length} 份</h3><ol>${items.map(item => `<li><strong>${e(item.name || '作业名称待补充')}</strong>${item.course_name ? `<p class="meta">${e(item.course_name)}</p>` : ''}<p>${e(reasonLabels[text(item.reason_code).toLowerCase()] || ('reason' in item && text(item.reason) && /\p{Script=Han}/u.test(text(item.reason)) ? text(item.reason) : '需要跟进，请查看该作业执行明细'))}</p></li>`).join('')}</ol></section>`;
}
function technicalResult(data: Record<string, unknown>, depth = 0): Record<string, unknown> {
  if (depth > 5) return {};
  const keys = ['exercise_id', 'total_questions', 'completed_questions', 'submitted', 'score_known', 'scan_complete', 'title', 'subject', 'course', 'course_name', 'homework_id', 'challenge_id', 'position', 'status', 'reason_code', 'score', 'max_score', 'diagnostics', 'model_calls', 'reasons', 'collected', 'duplicates', 'skipped', 'unconfirmed', 'failed', 'limit_reached', 'total_homeworks', 'completed_homeworks', 'needs_service_homeworks', 'pending_homeworks', 'stop_reason'];
  const result = Object.fromEntries(keys.filter(key => data[key] !== undefined).map(key => [key, data[key]]));
  const children = resultChildren(data);
  if (children.length) result.steps = children.map(item => ({ ...technicalResult(item, depth + 1), ...Object.keys(record(item.result)).length ? { result: technicalResult(record(item.result), depth + 1) } : {} }));
  return result;
}
export function renderAdminJobDetail(job: AdminJob, historical = false): string {
  const data = record(job.result);
  const context = jobContext(job), collecting = job.request.mode === 'collect_verified', status = displayStatus(job);
  if (historical) { status.tone = 'limited'; if (needsAttention(job)) status.label = '当次未完成'; }
  const scope = collecting ? '题库采集（只读）' : job.request.scope === 'all' ? '全部课程' : job.request.scope === 'course' ? '整门课程' : job.request.scope === 'current' ? '单个关卡' : job.request.scope === 'exercise' ? '在线考试' : '单份作业';
  const raw = { job_id: job.id, owner_id: job.owner_id ?? job.owner, request: job.request, status: job.status, phase: job.phase, error_code: job.error_code, message: job.message, context_reason_code: job.context?.summary?.reason_code, context_diagnostics: job.context?.summary?.diagnostics, stop_reason: job.context?.summary?.stop_reason, result: technicalResult(data) };
  return `<h2>${collecting ? '题库采集详情' : '任务执行详情'}</h2>${historical ? '<p class="detail-note">历史执行记录：以下状态与原因仅代表当次尝试，不计入当前待处理。当前结果请查看当前任务。</p>' : ''}<p class="subtext">${e(context.user)} · ${e(collecting ? '采集已通过题目' : context.course)}</p><div class="operator-summary"><span class="badge ${status.tone}">${e(status.label)}</span><strong>${e(progressLabel(job))}</strong><p>${e(readableReason(job))}</p>${count(job.context?.summary?.pending_homeworks) ? `<p>另有 ${count(job.context?.summary?.pending_homeworks)} 份作业尚未开始，不计作已完成。</p>` : ''}</div><dl class="info-list admin-detail-info"><div><dt>用户</dt><dd>${e(context.user)}</dd></div><div><dt>任务范围</dt><dd>${scope}</dd></div>${collecting ? '' : `<div><dt>课程</dt><dd>${e(context.course)}</dd></div><div><dt>${job.request.scope === 'exercise' ? '考试' : '作业'}</dt><dd>${e(homeworkLabel(job, context))}</dd></div>${job.request.scope === 'exercise' ? '' : `<div><dt>当前关卡</dt><dd>${context.level ? `${context.position ? `第 ${context.position} 关 · ` : ''}${e(context.level)}` : '尚未进入关卡'}</dd></div>`}`}<div><dt>${['course', 'all'].includes(job.request.scope ?? '') ? '整批最后阶段' : '执行阶段'}</dt><dd>${e(phaseLabels[job.phase] ?? '阶段待补充')}</dd></div><div><dt>创建时间</dt><dd>${e(dateTime(job.created_at))}</dd></div><div><dt>模型调用</dt><dd>${count(data.model_calls)} 次${!count(data.model_calls) ? '（未调用模型）' : ''}</dd></div><div><dt>记录费用（含估算）</dt><dd>$${(count(data.cost_micro_usd) / 1000000).toFixed(6)}</dd></div></dl>${collecting ? renderCollectionSummary(job) : renderAttentionHomework(context.attention) + renderAdminSteps(data)}<details class="operator-technical"><summary>技术记录（原始标识与诊断）</summary><pre>${e(JSON.stringify(raw, null, 2))}</pre></details>`;
}

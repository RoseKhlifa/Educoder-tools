import type { AnswerJob, AppState, CollectionSummary, JobMode, JobStatus } from './types.js';

const escape = (value: unknown): string => String(value ?? '').replace(/[&<>'"]/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' }[char] as string));
export const pendingJob = (job: AnswerJob): boolean => job.status === 'queued' || job.status === 'running';
const statusLabels: Record<JobStatus, string> = { queued: '排队中', running: '执行中', succeeded: '流程已完成', failed: '执行失败', canceled: '已停止', uncertain: '平台结果待复核', interrupted: '执行已中断' };
const modes: Record<JobMode, string> = { generate_only: '仅生成答案', selftest: '自测当前关卡', submit: '提交并验证' };
const phases: Record<string, string> = {
  queued: '等待执行', starting: '准备任务', snapshot: '读取题目', loading: '读取题目', fetching: '读取题目',
  generating: '生成答案', generation: '生成答案', model: '生成答案', validating: '检查答案',
  saving: '保存答案', selftest: '执行自测', testing: '执行自测', repairing: '根据反馈修复',
  submitting: '提交答案', submit: '提交答案', polling: '等待评测', verifying: '复核平台状态',
  recording: '记录题库', completed: '流程结束', done: '流程结束', canceled: '停止执行',
  selftesting: '执行自测', evaluating: '等待正式评测', bank_lookup: '查询题库', bank_saved: '记录题库验证',
};
const resultStatuses: Record<string, string> = {
  generated: '答案已生成，未提交', passed: '平台评测已通过', selftest_passed: '公开样例自测通过，未正式提交',
  selftest_skipped: '无公开样例，未执行自测', selftest_failed: '公开自测未通过',
  uncertain: '平台结果待复核', unconfirmed: '平台分数待确认', canceled: '执行已停止',
  needs_provider: '需要配置模型', budget_exhausted: '已达到本次费用或调用上限',
  budget_unavailable: '费用预算暂不可用', attempts_exhausted: '已达到本次修复上限',
  model_error: '模型请求失败', unsupported_multifile: '暂不支持此多文件任务',
  verification_persistence_failed: '评测证据未完整入库', usage_persistence_failed: '模型用量未完整记录',
};
const collectionReasons: Record<string, string> = {
  no_readonly_game_locator: '未取得只读关卡入口',
  no_confirmed_full_score: '未取得可核验的满分记录',
  target_not_confirmed_full_score: '目标关卡缺少可核验的满分记录',
  classroom_identifier_missing: '课堂标识缺失',
  unsupported_homework_kind: '暂不支持此作业类型',
  unsupported_task_kind: '暂不支持此关卡题型',
  ambiguous_challenge_list: '关卡列表缺失或存在重复标识',
  target_identity_mismatch: '题目与目标关卡不匹配',
  programming_evaluation_unconfirmed: '编程题正式评测记录尚未确认',
  programming_submitted_source_unproven: '当前代码与通过版本的对应关系尚未确认',
  score_changed_or_unconfirmed: '复核时分数发生变化或未能确认',
  editor_changed: '两次读取的编辑器内容发生变化',
  objective_status_unconfirmed: '客观题通过状态尚未确认',
  objective_answer_evidence_missing: '客观题缺少逐题正确答案记录',
  unsupported_objective_question_kind: '暂不支持此客观题类型',
  objective_question_identity_mismatch: '客观题题目或选项标识不匹配',
  objective_media_content_unproven: '客观题含有尚未核验的图片或媒体内容',
  objective_result_mismatch: '客观题评测结果与题目不匹配',
  objective_actual_answer_missing: '客观题评测记录未返回作答内容',
  objective_actual_answer_invalid: '客观题作答内容不符合题目要求',
  objective_result_position_mismatch: '客观题评测记录与题目顺序不匹配',
  objective_contract_invalid: '客观题题目与答案结构未通过检查',
  objective_answer_changed_or_mismatched: '复核时客观题题目或答案发生变化',
  course_read_failed: '课堂列表读取失败',
  homework_read_failed: '作业列表读取失败',
  progress_read_failed: '作业进度读取失败',
  task_read_failed: '关卡内容读取失败',
  answer_capture_failed: '答案读取或入库失败',
  request_byte_or_time_limit: '达到请求流量或扫描时长上限',
  homework_page_limit: '达到作业分页扫描上限',
  course_page_limit: '达到课堂分页扫描上限',
};

function collectionSummary(value: AnswerJob['result']): CollectionSummary | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const result: CollectionSummary = {};
  for (const key of ['courses', 'homeworks', 'levels', 'collected', 'duplicates', 'skipped', 'unconfirmed', 'failed'] as const) {
    const count = value[key];
    if (typeof count === 'number' && Number.isSafeInteger(count) && count >= 0) result[key] = count;
  }
  if (typeof value.limit_reached === 'boolean') result.limit_reached = value.limit_reached;
  if (value.reasons && typeof value.reasons === 'object' && !Array.isArray(value.reasons)) {
    result.reasons = {};
    for (const [key, count] of Object.entries(value.reasons)) {
      if (typeof count === 'number' && Number.isSafeInteger(count) && count > 0) result.reasons[key] = count;
    }
  }
  return Object.keys(result).length ? result : null;
}

function collectionCard(job: AnswerJob, canceling: boolean): string {
  const pending = pendingJob(job);
  const result = collectionSummary(job.result);
  const collected = result?.collected;
  const label = job.status === 'succeeded'
    ? collected === undefined ? '扫描已结束，统计待获取' : collected > 0 ? `扫描结束，新增 ${collected} 条答案` : '扫描结束，未新增答案'
    : ({ queued: '等待扫描', running: '扫描中', failed: '扫描未完成', canceled: '扫描已停止', interrupted: '扫描已中断', uncertain: '扫描结果待确认' } as const)[job.status];
  const tone = job.status === 'succeeded' && collected !== undefined && collected > 0 && !result?.limit_reached && !result?.failed ? 'status-done' : pending ? 'status-progress' : 'status-attention';
  const stamp = new Date(job.created_at);
  const time = Number.isNaN(stamp.getTime()) ? '' : ` · ${stamp.toLocaleString('zh-CN')}`;
  const phase = job.status === 'queued' ? '等待后台扫描' : pending ? '核验已通过任务' : '采集扫描已结束';
  const coverage: string[] = [];
  if (result?.courses !== undefined) coverage.push(`已扫描课堂 ${result.courses}`);
  if (result?.homeworks !== undefined) coverage.push(`作业 ${result.homeworks}`);
  if (result?.levels !== undefined) coverage.push(`关卡读取尝试 ${result.levels}`);
  const counts = ([['collected', '新增答案'], ['duplicates', '题库已有'], ['skipped', '跳过'], ['failed', '读取或入库失败']] as const)
    .filter(([key]) => result?.[key] !== undefined)
    .map(([key, name]) => `<div><dt>${name}</dt><dd>${result![key]}</dd></div>`).join('');
  const reasons: string[] = [];
  let other = 0;
  for (const [key, count] of Object.entries(result?.reasons ?? {})) {
    if (Object.hasOwn(collectionReasons, key)) reasons.push(`<li>${escape(collectionReasons[key])}：${count} 项</li>`);
    else other += count;
  }
  if (other) reasons.push(`<li>其他未能确认或读取的记录：${other} 项</li>`);
  const note = job.status === 'canceled' ? '后续扫描已停止；已存入本地题库的记录会保留。'
    : job.status === 'interrupted' ? '服务重启中断了扫描；已存入本地题库的记录会保留，本次扫描不会自动重放。'
    : job.status === 'failed' ? '本次扫描未完成；以下仅显示已经返回的统计。'
    : job.status === 'uncertain' ? '扫描结果尚未确认，请刷新记录查看。' : '';
  return `<article class="job-card job-collection" data-job-id="${escape(job.id)}"><div class="job-card-top"><strong>已通过任务采集</strong><span class="status-pill ${tone}">${escape(label)}</span></div><p class="job-context">登录后自动扫描${escape(time)}</p><p class="job-phase">${pending ? '<span class="spinner spinner-blue"></span>' : ''}${phase}</p><p class="job-message">只读核验已通过的任务，不调用模型，也不向平台保存或提交答案。</p>${coverage.length ? `<p class="job-result">${coverage.join(' · ')}</p>` : ''}${counts ? `<dl class="collection-counts">${counts}</dl>` : '<p class="job-note">统计尚未返回，请刷新记录查看。</p>'}${result?.unconfirmed !== undefined ? `<p class="job-result">未确认 ${result.unconfirmed} 条（包含在跳过中）</p>` : ''}${result?.levels === 0 && !pending ? '<p class="job-note">未能读取任何关卡，尚未获取到可入库的题目与答案。</p>' : ''}${result?.limit_reached ? '<p class="job-note">已达到本次扫描上限，以上仅为部分结果，尚未遍历全部任务。</p>' : ''}${reasons.length ? `<details class="collection-details" open><summary>未入库原因</summary><ul class="collection-reasons">${reasons.join('')}</ul></details>` : ''}${note ? `<p class="job-note">${note}</p>` : ''}${pending ? `<button class="button button-outline job-cancel" data-cancel-job="${escape(job.id)}" ${canceling ? 'disabled' : ''}>${canceling ? '正在请求停止…' : '停止扫描'}</button>` : ''}</article>`;
}

function resultLabel(job: AnswerJob): string {
  const result = job.result;
  if (!result) return '';
  const parts: string[] = [];
  if (typeof result.status === 'string') parts.push(`结果：${resultStatuses[result.status] ?? result.status}`);
  if (typeof result.model_calls === 'number') parts.push(`模型调用 ${result.model_calls} 次`);
  if (typeof result.score === 'number' || typeof result.score === 'string' && result.score.length) {
    parts.push(`平台得分 ${result.score}${typeof result.max_score === 'string' && result.max_score ? ` / ${result.max_score}` : ''}${result.score_known === false ? '（待核实）' : ''}`);
  }
  if (result.score_known === true && result.score_check === 'full_score') parts.push('已核验满分');
  if (typeof result.cost_micro_usd === 'number' && result.cost_micro_usd >= 0) parts.push(`模型费用 $${(result.cost_micro_usd / 1_000_000).toFixed(6)}`);
  if (result.programming && typeof result.programming === 'object') {
    const programming = result.programming as Record<string, unknown>;
    if (programming.progress_error) parts.push('课堂进度待同步，请复核平台状态');
    if (programming.self_test_status === 'skipped') parts.push('本关无公开样例，自测已跳过');
  }
  return parts.length ? `<p class="job-result">${parts.map(escape).join(' · ')}</p>` : '';
}

function jobCard(job: AnswerJob, canceling: boolean): string {
  if (job.request.mode === 'collect_verified') return collectionCard(job, canceling);
  const latest = job.events?.at(-1);
  const pending = pendingJob(job);
  const tone = job.status === 'succeeded' ? 'status-done' : job.status === 'failed' || job.status === 'uncertain' || job.status === 'interrupted' ? 'status-attention' : 'status-progress';
  const stamp = new Date(job.created_at);
  const time = Number.isNaN(stamp.getTime()) ? '' : stamp.toLocaleString('zh-CN');
  const statusNote = job.status === 'uncertain' ? '上游可能已受理，请先核对平台状态后再决定是否重试。' : job.status === 'interrupted' ? '服务重启中断了任务。请核对平台状态，再手动创建新任务。' : job.status === 'canceled' ? '后续步骤已停止，已送达平台的保存或提交不会撤回。' : '';
  return `<article class="job-card" data-job-id="${escape(job.id)}"><div class="job-card-top"><strong>作业 ${escape(job.request.homework_id)} · ${escape(modes[job.request.mode] ?? job.request.mode)}</strong><span class="status-pill ${tone}">${escape(statusLabels[job.status] ?? job.status)}</span></div><p class="job-context">${escape(job.request.course)}${job.request.game ? ` · 关卡 ${escape(job.request.game)}` : ''} · ${escape(time)}</p><p class="job-phase">${pending ? '<span class="spinner spinner-blue"></span>' : ''}${escape(phases[job.phase] ?? (job.phase || '准备任务'))}</p>${job.message || latest?.message ? `<p class="job-message">${escape(job.message || latest?.message)}</p>` : ''}${resultLabel(job)}${statusNote ? `<p class="job-note">${statusNote}</p>` : ''}${pending ? `<button class="button button-outline job-cancel" data-cancel-job="${escape(job.id)}" ${canceling ? 'disabled' : ''}>${canceling ? '正在请求停止…' : '停止后续步骤'}</button>` : ''}</article>`;
}

export function renderJobs(state: AppState): string {
  const capacity = state.jobCapacity;
  return `<section class="jobs-section" id="answer-jobs" aria-label="答题任务记录"><div class="section-heading"><div><p class="eyebrow">ANSWER JOBS</p><h2>答题任务记录</h2></div><button class="button button-outline" data-action="refresh-jobs" ${state.jobsLoading ? 'disabled' : ''}>${state.jobsLoading ? '更新中…' : '刷新记录'}</button></div><p class="jobs-intro">后台异步执行，切换课堂或关闭页面后任务仍会继续。答题任务只处理所选当前关卡；登录后的已通过任务采集会另行扫描课堂，并展示入库与跳过原因。</p>${capacity ? `<p class="jobs-capacity">执行中 ${escape(capacity.running)} · 排队 ${escape(capacity.queued)} · 并行上限 ${escape(capacity.workers)}</p>${!capacity.model_configured ? '<p class="job-config-note">服务端尚未配置模型，暂时不能创建答题任务。请联系管理员配置模型后刷新。</p>' : ''}` : ''}${state.jobsError ? `<div class="alert alert-error jobs-alert" role="alert">${escape(state.jobsError)}</div>` : ''}${state.jobs.length ? `<div class="job-list">${state.jobs.map(job => jobCard(job, state.cancelingJobs.includes(job.id))).join('')}</div>` : `<p class="empty-mini">${state.jobsLoading ? '正在读取任务记录…' : '暂无答题任务。从任务详情中选择执行方式即可开始。'}</p>`}</section>`;
}

export function renderJobControls(state: AppState): string {
  const task = state.enteredTask;
  if (!task) return '';
  const kind = task.kind;
  const mode = kind === 'choice' && state.jobMode === 'selftest' ? 'generate_only' : state.jobMode;
  const busy = state.creatingJob || state.jobs.some(job => pendingJob(job) && job.request.course === task.courseIdentifier && job.request.homework_id === task.homeworkId && job.request.game === task.gameIdentifier);
  const ready = kind !== 'unknown' && Boolean(state.jobCapacity?.model_configured) && Boolean(task.gameIdentifier);
  const label = mode === 'submit' && kind === 'choice' ? '提交并核验满分' : modes[mode];
  return `<section class="job-controls" aria-label="执行当前关卡"><h4>执行当前关卡 <span class="muted">${kind === 'programming' ? '编程题' : kind === 'choice' ? '客观题' : '题型待确认'}</span></h4><div class="job-modes">${(['generate_only', ...(kind === 'programming' ? ['selftest'] : []), 'submit'] as JobMode[]).map(value => `<label><input type="radio" name="job-mode" data-job-mode="${value}" value="${value}" ${mode === value ? 'checked' : ''} ${busy ? 'disabled' : ''} /><span>${value === 'submit' && kind === 'choice' ? '提交并核验满分' : modes[value]}</span></label>`).join('')}</div><p class="job-control-note">${mode === 'generate_only' ? '生成答案并记录候选，不向平台提交。' : mode === 'selftest' ? '保存代码并运行当前关卡的公开样例，不进行正式提交。' : '将答案提交到头歌并复核评测结果。只执行当前关卡，不会自动进入下一关。'}</p><button class="button button-primary button-block" data-action="create-job" ${!ready || busy ? 'disabled' : ''}>${state.creatingJob ? '正在创建任务…' : busy ? '该关卡正在执行' : label}</button>${kind === 'unknown' ? '<p class="job-config-note">尚未识别该关卡题型，暂时不能执行。</p>' : !state.jobCapacity ? '<p class="job-config-note">请先刷新下方任务记录，确认服务可用。</p>' : !state.jobCapacity.model_configured ? '<p class="job-config-note">服务端尚未配置模型，请联系管理员配置后刷新记录。</p>' : ''}<p class="job-control-note">可在下方任务记录查看进度或停止后续步骤。退出登录不会自动停止任务。</p></section>`;
}

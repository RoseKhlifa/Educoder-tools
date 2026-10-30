import type { AnswerJob, AppState, JobMode, JobStatus } from './types.js';

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
  return `<section class="jobs-section" id="answer-jobs" aria-label="答题任务记录"><div class="section-heading"><div><p class="eyebrow">ANSWER JOBS</p><h2>答题任务记录</h2></div><button class="button button-outline" data-action="refresh-jobs" ${state.jobsLoading ? 'disabled' : ''}>${state.jobsLoading ? '更新中…' : '刷新记录'}</button></div><p class="jobs-intro">后台异步执行，切换课堂或关闭页面后任务仍会继续。只处理所选当前关卡。</p>${capacity ? `<p class="jobs-capacity">执行中 ${escape(capacity.running)} · 排队 ${escape(capacity.queued)} · 并行上限 ${escape(capacity.workers)}</p>${!capacity.model_configured ? '<p class="job-config-note">服务端尚未配置模型，暂时不能创建答题任务。请联系管理员配置模型后刷新。</p>' : ''}` : ''}${state.jobsError ? `<div class="alert alert-error jobs-alert" role="alert">${escape(state.jobsError)}</div>` : ''}${state.jobs.length ? `<div class="job-list">${state.jobs.map(job => jobCard(job, state.cancelingJobs.includes(job.id))).join('')}</div>` : `<p class="empty-mini">${state.jobsLoading ? '正在读取任务记录…' : '暂无答题任务。从任务详情中选择执行方式即可开始。'}</p>`}</section>`;
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

import type { AnswerJob, Course } from './types.js';
import { count, dateTime, escapeHtml as e, record } from './ui.js';
export const pendingJob = (job: AnswerJob): boolean => ['queued', 'running', 'waiting', 'processing'].includes(job.status);
export const serviceJob = (job: AnswerJob): boolean => job.request.mode !== 'collect_verified';
// Capability flags come from the session-bound API. Missing flags fail closed.
export const canRetryJob = (job: AnswerJob): boolean => job.can_retry === true && job.request.mode === 'submit' && ['homework', 'course', 'all'].includes(job.request.scope ?? '') && ['failed', 'canceled', 'interrupted', 'support'].includes(job.status);
export const canCancelJob = (job: AnswerJob): boolean => job.can_cancel === true && job.request.mode === 'submit' && ['homework', 'course', 'all'].includes(job.request.scope ?? '') && pendingJob(job);
export const canRecoverJob = (job: AnswerJob): boolean => job.can_recover === true && job.request.mode === 'submit' && ['homework', 'course', 'all'].includes(job.request.scope ?? '') && ['failed', 'canceled', 'uncertain', 'interrupted', 'support'].includes(job.status);
export type JobActionState = 'retry' | 'recover' | 'cancel' | 'stopping';
function recoveryState(job: AnswerJob): string {
  const recovery = job.recovery;
  return typeof recovery?.state === 'string' ? recovery.state : '';
}
function recoveryMessage(job: AnswerJob): string {
  const recovery = job.recovery;
  return typeof recovery?.message === 'string' ? recovery.message : '';
}
function recoveryLabel(job: AnswerJob): string {
  switch (recoveryState(job)) {
    case 'queued': return '复核排队中';
    case 'reconciling': return '正在复核';
    case 'continuing': return '复核后继续';
    case 'blocked': return '复核未通过';
    case 'failed': return '复核后未完成';
    case 'completed': return '复核已完成';
    case 'stopped': return '复核已停止';
    default: return '';
  }
}
export function serviceLabel(job: AnswerJob): string { return recoveryLabel(job) || serviceLabels[serviceStatus(job)]; }
export function renderJobActions(job: AnswerJob, historical = false, pending?: JobActionState): string {
  if (historical) return '';
  if (pending === 'retry') return '<div class="job-actions"><button class="button primary compact" disabled>正在重新执行…</button></div>';
  if (pending === 'recover') return '<div class="job-actions"><button class="button primary compact" disabled>正在复核…</button></div>';
  if (pending === 'cancel' || pending === 'stopping') return '<div class="job-actions"><button class="text-button" disabled>正在停止…</button></div>';
  if (canRetryJob(job)) return `<div class="job-actions"><button class="button primary compact" data-job-retry="${e(job.id)}">重新执行</button></div>`;
  if (canRecoverJob(job)) return `<div class="job-actions"><button class="button primary compact" data-job-recover="${e(job.id)}">${recoveryState(job) === 'blocked' ? '再次复核并继续' : '复核并继续'}</button></div>`;
  if (canCancelJob(job)) return `<div class="job-actions"><button class="text-button" data-job-cancel="${e(job.id)}">停止任务</button></div>`;
  return '';
}
function serviceTarget(job: AnswerJob, index: number): string {
  const request = job.request;
  const scope = request.scope;
  // The signed-in API already isolates users. Only the original request defines
  // an arrangement; its last displayed homework or level cannot replace it.
  const course = typeof request.course === 'string' ? request.course.trim() : '';
  const homework = request.homework_id;
  const game = typeof request.game === 'string' ? request.game.trim() : '';
  const exercise = 'exercise_id' in request ? request.exercise_id : undefined;
  if (!['submit', 'selftest', 'generate_only'].includes(request.mode) || !scope || !['all', 'course', 'homework', 'current', 'exercise'].includes(scope) || scope !== 'all' && !course || ['homework', 'current'].includes(scope) && (!Number.isSafeInteger(homework) || !homework || homework < 1) || scope === 'current' && !game || scope === 'exercise' && (!Number.isSafeInteger(exercise) || !exercise || exercise < 1)) return `record:${index}`;
  return JSON.stringify([request.mode, scope, scope === 'all' ? '' : course, ['homework', 'current'].includes(scope) ? homework : 0, scope === 'current' ? game : '', scope === 'exercise' ? exercise : 0]);
}
export function partitionServiceJobs(jobs: AnswerJob[]): { current: AnswerJob[]; history: AnswerJob[] } {
  const ordered = jobs.filter(serviceJob).map((job, index) => ({ job, index, target: serviceTarget(job, index) })).sort((a, b) => {
    const created = Date.parse(b.job.created_at) - Date.parse(a.job.created_at);
    return (Number.isFinite(created) ? created : 0) || a.index - b.index;
  });
  const activeTargets = new Set(ordered.filter(item => pendingJob(item.job)).map(item => item.target));
  const current: AnswerJob[] = [], history: AnswerJob[] = [], seen = new Set<string>();
  for (const { job, target } of ordered) {
    const status = serviceStatus(job);
    // The current view is deliberately actionable: it contains work that is
    // still running, waiting for a result, or needs a user/service follow-up.
    // Completed tasks always move to history so the page answers one
    // question at a time: “what needs attention now?”
    const active = pendingJob(job) || status === 'support' || canRetryJob(job) || status === 'submitted' || status === 'awaiting_review';
    const currentAttempt = pendingJob(job) || active && !seen.has(target) && !activeTargets.has(target);
    if (active && currentAttempt) current.push(job); else history.push(job);
    seen.add(target);
  }
  return { current, history };
}
export const currentServiceJobs = (jobs: AnswerJob[]): AnswerJob[] => partitionServiceJobs(jobs).current;
export type ServiceStatus = 'waiting' | 'processing' | 'support' | 'completed' | 'stopped' | 'submitted' | 'awaiting_review' | 'graded';
export function serviceStatus(job: AnswerJob): ServiceStatus {
  const publicStatus = record(job.result).service_status ?? record(job.progress).service_status;
  if (job.request.scope === 'exercise') {
    if (job.status === 'queued' || job.status === 'waiting') return 'waiting';
    if (job.status === 'running' || job.status === 'processing') return 'processing';
    if (record(job.result).submitted === true) return record(job.result).score_known === true ? 'graded' : publicStatus === 'submitted' ? 'submitted' : 'awaiting_review';
    if (['submitted', 'awaiting_review', 'graded'].includes(String(publicStatus))) return publicStatus as ServiceStatus;
    if (['submitted', 'awaiting_review', 'graded'].includes(job.status)) return job.status as ServiceStatus;
    if (job.status === 'canceled') return 'stopped';
    return 'support';
  }
  if (job.status === 'queued' || job.status === 'waiting') return 'waiting';
  if (job.status === 'running' || job.status === 'processing') return 'processing';
  if (job.status === 'canceled') return 'stopped';
  if (['failed', 'uncertain', 'interrupted', 'support'].includes(job.status)) return 'support';
  if (job.status === 'succeeded' || job.status === 'completed') return 'completed';
  if (job.status === 'submitted' || job.status === 'awaiting_review' || job.status === 'graded') return job.status;
  if (publicStatus === 'support' || publicStatus === 'completed' || publicStatus === 'submitted' || publicStatus === 'awaiting_review' || publicStatus === 'graded') return publicStatus;
  return 'support';
}
export const serviceLabels: Record<ServiceStatus, string> = { waiting: '等待处理', processing: '正在处理', support: '待跟进', completed: '已完成', stopped: '已停止', submitted: '已交卷', awaiting_review: '待公布成绩', graded: '成绩已公布' };
export function jobTitle(job: AnswerJob, courses: Course[] = []): string {
  const result = record(job.result);
  if (job.request.scope === 'all') return '全部课程的未完成作业';
  const course = courses.find(item => item.identifier === job.request.course);
  const resultTitle = typeof result.title === 'string' ? result.title : '';
  if (job.request.scope === 'exercise') return resultTitle || '在线考试';
  if (job.request.scope === 'course') return course?.name ?? '课程作业';
  return resultTitle || (course ? `${course.name}的作业` : '作业完成服务');
}
export function jobProgress(job: AnswerJob): { completed: number; total: number; unit: string; percent: number } {
  const data = { ...record(job.result), ...record(job.progress) };
  const homeworks = count(data.total_homeworks);
  const questions = count(data.total_questions);
  const exercise = job.request.scope === 'exercise';
  const total = exercise ? questions : homeworks || count(data.total_levels);
  const completed = Math.min(total, exercise ? count(data.completed_questions) : homeworks ? count(data.completed_homeworks) : count(data.completed_levels));
  return { completed, total, unit: exercise ? '道题' : homeworks ? '份作业' : '关', percent: total ? Math.round(completed / total * 100) : ['completed', 'graded', 'submitted', 'awaiting_review'].includes(serviceStatus(job)) ? 100 : 0 };
}
function displayLabel(status: ServiceStatus, historical: boolean): string {
  if (!historical) return serviceLabels[status];
  if (status === 'submitted' || status === 'awaiting_review') return '当次已交卷';
  if (status === 'graded') return '当次已评分';
  return status === 'completed' ? '当次已完成' : status === 'stopped' ? '当次已停止' : '当次未完成';
}
function progressText(job: AnswerJob): string {
  const progress = jobProgress(job);
  if (job.request.scope === 'exercise' && ['submitted', 'awaiting_review', 'graded'].includes(serviceStatus(job))) return `已交卷${progress.total ? ` · 共 ${progress.total} 道题` : ''}`;
  return progress.total ? `${progress.completed} / ${progress.total} ${progress.unit}` : serviceStatus(job) === 'completed' ? '已完成' : '进度待更新';
}
export function renderServiceJob(job: AnswerJob, courses: Course[] = [], historical = false, action?: JobActionState): string {
  const status = serviceStatus(job);
  const progress = jobProgress(job);
  const submitted = job.request.scope === 'exercise' && ['submitted', 'awaiting_review', 'graded'].includes(status);
  return `<article class="service-job service-job-compact hw-card status-${status}${historical ? ' service-job-history' : ''}" data-service-record="${historical ? 'history' : 'current'}"><div class="hw-top job-heading"><div class="hw-title"><span class="job-symbol" aria-hidden="true">${status === 'completed' || submitted ? '✓' : status === 'support' ? '·' : '◌'}</span><div><h3>${e(jobTitle(job, courses))}</h3><p class="meta">${e(dateTime(job.created_at))} · ${historical ? '本次任务' : '开始时间'}</p></div></div><div class="hw-badges"><span class="badge ${historical ? 'stopped' : status}">${historical ? displayLabel(status, true) : serviceLabel(job)}</span></div></div><div class="hw-progress job-footer"><div class="job-progress">${submitted ? '' : `<div class="progress-track bar-wrap" aria-hidden="true"><span class="progress-fill" style="width:${progress.percent}%"></span></div>`}<span>${historical ? '本次进度 · ' : ''}${progressText(job)}</span></div><button class="text-button" data-job-detail="${e(job.id)}"${historical ? ' data-job-history="true"' : ''}>${historical ? '查看记录' : '查看详情'}</button>${renderJobActions(job, historical, action)}</div></article>`;
}
export function renderJobs(jobs: AnswerJob[], courses: Course[] = [], historyOpen = false, actions: ReadonlyMap<string, JobActionState> = new Map()): string {
  const { current, history } = partitionServiceJobs(jobs);
  if (!current.length && !history.length) return '<div class="empty"><span class="empty-circle">✓</span><h3>每一项任务，都会在这里记录</h3><p>先选择课程，接下来的事情交给我们。</p></div>';
  const submitted = current.filter(job => ['submitted', 'awaiting_review', 'graded'].includes(serviceStatus(job))).length;
  const retryable = current.filter(canRetryJob).length;
  const processing = current.filter(pendingJob).length;
  const followUp = current.filter(job => serviceStatus(job) === 'support').length;
  const reconciling = current.filter(job => ['queued', 'reconciling', 'continuing'].includes(recoveryState(job))).length;
  const blocked = current.filter(job => recoveryState(job) === 'blocked').length;
  return `<section class="service-progress-list panel-box" aria-label="完成进度"><div class="service-progress-summary panel-toolbar"><div><h2>当前任务 <span class="count">${current.length}</span></h2><p>${processing ? `<span>处理中 <b>${processing}</b></span>` : ''}${reconciling ? `<span>复核中 <b>${reconciling}</b></span>` : ''}${blocked ? `<span>复核未通过 <b>${blocked}</b></span>` : ''}${followUp ? `<span>待跟进 <b>${followUp}</b></span>` : ''}${submitted ? `<span>已交卷 <b>${submitted}</b></span>` : ''}${retryable ? `<span>可重新执行 <b>${retryable}</b></span>` : ''}</p></div></div>${current.length ? `<div class="service-jobs hw-list" data-service-current>${current.map(job => renderServiceJob(job, courses, false, actions.get(job.id))).join('')}</div>` : '<div class="progress-empty"><strong>当前没有需要处理的任务</strong><span>已完成的内容已收进历史记录。</span></div>'}${history.length ? `<details class="service-history" data-service-history${historyOpen ? ' open' : ''}><summary><span>历史记录 <span class="count">${history.length}</span></span><span class="service-history-hint">查看已完成和之前的任务</span></summary><p class="service-history-note">这里保留每次任务的结果，当前需要处理的内容会显示在上方。</p><div class="service-jobs hw-list" data-service-history-list>${history.map(job => renderServiceJob(job, courses, true)).join('')}</div></details>` : ''}</section>`;
}
export function renderServiceDetail(job: AnswerJob, courses: Course[] = [], historical = false, action?: JobActionState): string {
  const data = { ...record(job.progress), ...record(job.result) };
  const rows = Array.isArray(data.homeworks) ? data.homeworks.map(record) : Array.isArray(data.levels) ? data.levels.map(record) : [];
  const status = serviceStatus(job);
  const currentTitle = typeof data.current_title === 'string' ? data.current_title.trim() : '';
  const liveProgress = !historical && ['waiting', 'processing', 'support'].includes(status) ? `<div class="detail-progress-card"><div><span>当前状态</span><b>${e(serviceLabel(job))}</b></div>${currentTitle ? `<strong>${e(currentTitle)}</strong>` : '<strong>正在准备下一步</strong>'}<p>${e(progressText(job))}</p>${recoveryMessage(job) ? `<small>${e(recoveryMessage(job))}</small>` : ''}</div>` : '';
  const exerciseNote = job.request.scope === 'exercise' ? historical ? '历史记录：以下为本次考试的交卷状态，当前任务请查看上方列表。' : status === 'submitted' || status === 'awaiting_review' ? '已交卷，成绩以头歌公布为准。' : status === 'graded' ? `考试成绩已公布。${data.score_known === true && (typeof data.score === 'number' || typeof data.score === 'string') ? `成绩 ${e(data.score)}${typeof data.max_score === 'number' || typeof data.max_score === 'string' ? ` / ${e(data.max_score)}` : ''}` : ''}` : status === 'support' ? '考试事项需要服务人员跟进，请勿重复提交。' : '服务人员会处理整场考试，完成后显示交卷状态。' : '';
  const recoveryNote = recoveryMessage(job);
  return `<h2>${e(jobTitle(job, courses))}</h2><p class="subtext">${e(dateTime(job.created_at))} · ${historical ? '本次任务' : '开始时间'}</p><span class="badge ${historical ? 'stopped' : status}">${historical ? displayLabel(status, true) : serviceLabel(job)}</span>${renderJobActions(job, historical, action)}<div class="detail-note">${exerciseNote || (historical ? '历史记录：以下为本次任务的进度，不代表当前仍在处理。' : recoveryNote ? recoveryNote : canRetryJob(job) ? '本次任务已暂停。可以重新执行原来的任务；已核验通过的必做关卡会自动跳过，只继续未完成的内容。' : canRecoverJob(job) ? '可以先复核上次任务的完成结果，再继续剩余的必做内容；结果仍未明确时不会重复提交。' : status === 'support' ? '本次处理已暂停，已完成进度保留，剩余内容待跟进。' : status === 'completed' ? '本次服务已经完成，结果已核验。' : status === 'stopped' ? '本次服务已停止，已完成的结果会保留。' : '我们会按顺序完成未完成的内容。')}</div>${liveProgress}${rows.length ? `<ol class="completion-list">${rows.map((row, index) => {
    const done = ['passed', 'skipped', 'completed', 'succeeded'].includes(String(row.status));
    return `<li><span class="completion-icon ${done ? 'done' : ''}">${done ? '✓' : index + 1}</span><div><strong>${e(typeof row.title === 'string' ? row.title : `作业 ${index + 1}`)}</strong><p>${historical ? done ? '当次已完成' : '当次未完成' : done ? '已完成' : status === 'support' || status === 'stopped' ? '待跟进' : row.status === 'support' ? '待跟进' : status === 'processing' && ['running', 'processing'].includes(String(row.status)) ? '正在处理' : '待完成'}</p></div></li>`;
  }).join('')}</ol>` : `<p class="muted">${historical ? '本次未记录详细进度。' : status === 'support' || status === 'stopped' ? '本次未记录详细进度，已完成结果保留。' : '处理详情会随任务推进更新。'}</p>`}`;
}

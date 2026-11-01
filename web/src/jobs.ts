import type { AnswerJob, Course } from './types.js';
import { count, dateTime, escapeHtml as e, record } from './ui.js';
export const pendingJob = (job: AnswerJob): boolean => ['queued', 'running', 'waiting', 'processing'].includes(job.status);
export const serviceJob = (job: AnswerJob): boolean => job.request.mode !== 'collect_verified';
const terminalStatuses = new Set(['succeeded', 'failed', 'canceled', 'uncertain', 'interrupted', 'completed', 'support']);
function serviceTarget(job: AnswerJob, index: number): string {
  const request = job.request;
  const scope = request.scope;
  // The signed-in API already isolates users. Only the original request defines
  // an arrangement; its last displayed homework or level cannot replace it.
  const course = typeof request.course === 'string' ? request.course.trim() : '';
  const homework = request.homework_id;
  const game = typeof request.game === 'string' ? request.game.trim() : '';
  if (!['submit', 'selftest', 'generate_only'].includes(request.mode) || !scope || !['all', 'course', 'homework', 'current'].includes(scope) || scope !== 'all' && !course || ['homework', 'current'].includes(scope) && (!Number.isSafeInteger(homework) || !homework || homework < 1) || scope === 'current' && !game) return `record:${index}`;
  return JSON.stringify([request.mode, scope, scope === 'all' ? '' : course, ['homework', 'current'].includes(scope) ? homework : 0, scope === 'current' ? game : '']);
}
export function partitionServiceJobs(jobs: AnswerJob[]): { current: AnswerJob[]; history: AnswerJob[] } {
  const ordered = jobs.filter(serviceJob).map((job, index) => ({ job, index, target: serviceTarget(job, index) })).sort((a, b) => {
    const created = Date.parse(b.job.created_at) - Date.parse(a.job.created_at);
    return (Number.isFinite(created) ? created : 0) || a.index - b.index;
  });
  const liveTargets = new Set(ordered.filter(item => pendingJob(item.job)).map(item => item.target));
  const current: AnswerJob[] = [], history: AnswerJob[] = [], seen = new Set<string>();
  for (const { job, target } of ordered) {
    if (terminalStatuses.has(job.status) && (liveTargets.has(target) || seen.has(target))) history.push(job);
    else current.push(job);
    seen.add(target);
  }
  return { current, history };
}
export const currentServiceJobs = (jobs: AnswerJob[]): AnswerJob[] => partitionServiceJobs(jobs).current;
export type ServiceStatus = 'waiting' | 'processing' | 'support' | 'completed' | 'stopped';
export function serviceStatus(job: AnswerJob): ServiceStatus {
  const publicStatus = record(job.result).service_status ?? record(job.progress).service_status;
  if (publicStatus === 'waiting' || publicStatus === 'processing' || publicStatus === 'support' || publicStatus === 'completed') return publicStatus;
  if (job.status === 'waiting' || job.status === 'processing' || job.status === 'support' || job.status === 'completed') return job.status;
  if (job.status === 'succeeded' && (publicStatus === 'completed' || ['passed', 'completed'].includes(String(record(job.result).status)))) return 'completed';
  if (job.status === 'succeeded') return 'completed';
  if (job.status === 'queued') return 'waiting';
  if (job.status === 'running') return 'processing';
  if (job.status === 'canceled') return 'stopped';
  return 'support';
}
export const serviceLabels: Record<ServiceStatus, string> = { waiting: '等待安排', processing: '正在处理', support: '服务处理中', completed: '已完成', stopped: '已停止' };
export function jobTitle(job: AnswerJob, courses: Course[] = []): string {
  const result = record(job.result);
  if (job.request.scope === 'all') return '全部课程的未完成作业';
  const course = courses.find(item => item.identifier === job.request.course);
  if (job.request.scope === 'course') return course?.name ?? '课程作业';
  return typeof result.title === 'string' ? result.title : course ? `${course.name}的作业` : '作业完成服务';
}
export function jobProgress(job: AnswerJob): { completed: number; total: number; unit: string; percent: number } {
  const data = { ...record(job.result), ...record(job.progress) };
  const homeworks = count(data.total_homeworks);
  const total = homeworks || count(data.total_levels);
  const completed = Math.min(total, homeworks ? count(data.completed_homeworks) : count(data.completed_levels));
  return { completed, total, unit: homeworks ? '份作业' : '关', percent: total ? Math.round(completed / total * 100) : serviceStatus(job) === 'completed' ? 100 : 0 };
}
function displayLabel(status: ServiceStatus, historical: boolean): string {
  if (!historical) return serviceLabels[status];
  return status === 'completed' ? '当次已完成' : status === 'stopped' ? '当次已停止' : '当次未完成';
}
function progressText(job: AnswerJob): string {
  const progress = jobProgress(job);
  return progress.total ? `${progress.completed} / ${progress.total} ${progress.unit}` : serviceStatus(job) === 'completed' ? '已完成' : '进度待更新';
}
export function renderServiceJob(job: AnswerJob, courses: Course[] = [], historical = false): string {
  const status = serviceStatus(job);
  const progress = jobProgress(job);
  return `<article class="service-job service-job-compact ${status}${historical ? ' service-job-history' : ''}" data-service-record="${historical ? 'history' : 'current'}"><div class="job-heading"><span class="job-symbol" aria-hidden="true">${status === 'completed' ? '✓' : status === 'support' ? '·' : '◌'}</span><div><h3>${e(jobTitle(job, courses))}</h3><p class="meta">${e(dateTime(job.created_at))} ${historical ? '当次安排' : '安排'}</p></div><span class="badge ${historical ? 'stopped' : status}">${displayLabel(status, historical)}</span></div><div class="job-footer"><div class="job-progress"><div class="progress-track" aria-hidden="true"><span style="width:${progress.percent}%"></span></div><span>${historical ? '当次进度 · ' : ''}${progressText(job)}</span></div><button class="text-button" data-job-detail="${e(job.id)}"${historical ? ' data-job-history="true"' : ''}>${historical ? '查看记录' : '查看详情'}</button></div></article>`;
}
export function renderJobs(jobs: AnswerJob[], courses: Course[] = [], historyOpen = false): string {
  const { current, history } = partitionServiceJobs(jobs);
  if (!current.length && !history.length) return '<div class="empty"><span class="empty-circle">✓</span><h3>每一份安排，都会在这里记录</h3><p>先选择课程，接下来的事情交给我们。</p></div>';
  const completed = current.filter(job => serviceStatus(job) === 'completed').length;
  const stopped = current.filter(job => serviceStatus(job) === 'stopped').length;
  const processing = current.length - completed - stopped;
  return `<section class="service-progress-list" aria-label="完成进度"><div class="service-progress-summary"><h2>当前安排 <span class="count">${current.length}</span></h2><p>${processing ? `<span>处理中 <b>${processing}</b></span>` : ''}${completed ? `<span>已完成 <b>${completed}</b></span>` : ''}${stopped ? `<span>已停止 <b>${stopped}</b></span>` : ''}</p></div><div class="service-jobs" data-service-current>${current.map(job => renderServiceJob(job, courses)).join('')}</div>${history.length ? `<details class="service-history" data-service-history${historyOpen ? ' open' : ''}><summary><span>历史记录 <span class="count">${history.length}</span></span><span class="service-history-hint">查看之前的安排</span></summary><p class="service-history-note">以下仅记录之前的安排，最新完成情况请以上方当前安排为准。</p><div class="service-jobs" data-service-history-list>${history.map(job => renderServiceJob(job, courses, true)).join('')}</div></details>` : ''}</section>`;
}
export function renderServiceDetail(job: AnswerJob, courses: Course[] = [], historical = false): string {
  const data = record(job.result);
  const rows = Array.isArray(data.homeworks) ? data.homeworks.map(record) : Array.isArray(data.levels) ? data.levels.map(record) : [];
  const status = serviceStatus(job);
  return `<h2>${e(jobTitle(job, courses))}</h2><p class="subtext">${e(dateTime(job.created_at))} ${historical ? '当次安排' : '创建'}</p><span class="badge ${historical ? 'stopped' : status}">${displayLabel(status, historical)}</span><div class="detail-note">${historical ? '历史记录：以下为当次安排的进度，不代表当前仍在处理。最新完成情况请查看当前安排。' : status === 'support' ? '放心，服务人员正在处理，无需重复提交。完成后，进度会在这里更新。' : status === 'completed' ? '本次服务已经完成，结果已核验。' : status === 'stopped' ? '本次服务已停止，已完成的结果会保留。' : '我们会按顺序完成未完成的内容。你可以随时回来查看。'}</div>${rows.length ? `<ol class="completion-list">${rows.map((row, index) => {
    const done = ['passed', 'skipped', 'completed', 'succeeded'].includes(String(row.status));
    return `<li><span class="completion-icon ${done ? 'done' : ''}">${done ? '✓' : index + 1}</span><div><strong>${e(typeof row.title === 'string' ? row.title : `作业 ${index + 1}`)}</strong><p>${historical ? done ? '当次已完成' : '当次未完成' : done ? '已完成' : row.status === 'support' ? '服务处理中' : row.status === 'processing' ? '正在处理' : '待完成'}</p></div></li>`;
  }).join('')}</ol>` : `<p class="muted">${historical ? '当次未记录详细进度。' : '详细完成进度会在处理后更新。'}</p>`}`;
}

import type { AnswerJob, Course } from './types.js';
import { count, dateTime, escapeHtml as e, record } from './ui.js';
export const pendingJob = (job: AnswerJob): boolean => ['queued', 'running', 'waiting', 'processing'].includes(job.status);
export const serviceJob = (job: AnswerJob): boolean => job.request.mode !== 'collect_verified';
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
export function renderServiceJob(job: AnswerJob, courses: Course[] = []): string {
  const status = serviceStatus(job);
  const progress = jobProgress(job);
  const note: Record<ServiceStatus, string> = {
    waiting: '已收到你的安排，准备好后会自动开始。',
    processing: '正在逐项完成，关闭页面也会继续。',
    support: '放心，服务人员正在处理，无需重复提交。',
    completed: '本次安排已完成，结果已核验。',
    stopped: '本次服务已停止，已完成的结果会保留。',
  };
  return `<article class="service-job ${status}"><div class="job-heading"><span class="job-symbol">${status === 'completed' ? '✓' : status === 'support' ? '·' : '◌'}</span><div><h3>${e(jobTitle(job, courses))}</h3><p class="meta">${e(dateTime(job.created_at))} 创建</p></div><span class="badge ${status}">${serviceLabels[status]}</span></div><div class="job-progress"><div class="progress-track"><span style="width:${progress.percent}%"></span></div><span>${progress.total ? `${progress.completed} / ${progress.total} ${progress.unit}` : status === 'completed' ? '已完成' : '正在整理进度'}</span></div><p class="job-note">${note[status]}</p><button class="text-button" data-job-detail="${e(job.id)}">查看完成进度</button></article>`;
}
export function renderJobs(jobs: AnswerJob[], courses: Course[] = []): string {
  const list = jobs.filter(serviceJob);
  return list.length ? `<div class="service-jobs">${list.map(job => renderServiceJob(job, courses)).join('')}</div>` : '<div class="empty"><span class="empty-circle">✓</span><h3>每一份安排，都会在这里记录</h3><p>先选择课程，接下来的事情交给我们。</p></div>';
}
export function renderServiceDetail(job: AnswerJob, courses: Course[] = []): string {
  const data = record(job.result);
  const rows = Array.isArray(data.homeworks) ? data.homeworks.map(record) : Array.isArray(data.levels) ? data.levels.map(record) : [];
  const status = serviceStatus(job);
  return `<h2>${e(jobTitle(job, courses))}</h2><p class="subtext">${e(dateTime(job.created_at))} 创建</p><span class="badge ${status}">${serviceLabels[status]}</span><div class="detail-note">${status === 'support' ? '放心，服务人员正在处理，无需重复提交。完成后，进度会在这里更新。' : status === 'completed' ? '本次服务已经完成，结果已核验。' : '我们会按顺序完成未完成的内容。你可以随时回来查看。'}</div>${rows.length ? `<ol class="completion-list">${rows.map((row, index) => {
    const done = ['passed', 'skipped', 'completed', 'succeeded'].includes(String(row.status));
    return `<li><span class="completion-icon ${done ? 'done' : ''}">${done ? '✓' : index + 1}</span><div><strong>${e(typeof row.title === 'string' ? row.title : `作业 ${index + 1}`)}</strong><p>${done ? '已完成' : row.status === 'support' ? '服务处理中' : row.status === 'processing' ? '正在处理' : '待完成'}</p></div></li>`;
  }).join('')}</ol>` : '<p class="muted">详细完成进度会在处理后更新。</p>'}`;
}

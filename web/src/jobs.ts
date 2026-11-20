import type { AnswerJob, Course, ProgressItem } from './types.js';
import { count, dayTime, duration, escapeHtml as e, icon, record } from './ui.js';
import { autoRetryText, marginMark, progressCounts, progressTone, reasonText, renderProgressTree, settledTone, stageLabel } from './progress.js';
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
export function recoveryNotice(job: AnswerJob): string {
  if (!job.recovery) return '';
  switch (recoveryState(job)) {
    case 'queued': return '已提交复核，等待开始。';
    case 'reconciling': return '正在复核上次结果，还没有新的提交。';
    case 'continuing': return '复核通过，正在继续剩下的内容。';
    case 'blocked': return '复核没通过，没有重试；做好的保留。';
    case 'completed': return '复核和后续处理都完成了。';
    case 'stopped': return '复核已停止，做好的保留。';
    case 'failed': return '复核后仍未完成，做好的保留。';
    default: return '';
  }
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
  if (pending === 'retry') return '<button class="btn sm" disabled>正在重试…</button>';
  if (pending === 'recover') return '<button class="btn sm" disabled>正在复核…</button>';
  if (pending === 'cancel' || pending === 'stopping') return '<button class="btn sm stop" disabled>正在停止…</button>';
  if (canRetryJob(job)) return `<button class="btn sm" data-job-retry="${e(job.id)}">现在重试</button>`;
  if (canRecoverJob(job)) return `<button class="btn sm" data-job-recover="${e(job.id)}">${recoveryState(job) === 'blocked' ? '再次复核并继续' : '复核并继续'}</button>`;
  if (canCancelJob(job)) return `<button class="btn sm stop" data-job-cancel="${e(job.id)}">停止</button>`;
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
export function partitionServiceJobs(jobs: AnswerJob[], historicalIds?: ReadonlySet<string>): { current: AnswerJob[]; history: AnswerJob[] } {
  const ordered = jobs.filter(serviceJob).map((job, index) => ({ job, index, target: serviceTarget(job, index) })).sort((a, b) => {
    const created = Date.parse(b.job.created_at) - Date.parse(a.job.created_at);
    return (Number.isFinite(created) ? created : 0) || a.index - b.index;
  });
  const activeTargets = new Set(ordered.filter(item => pendingJob(item.job)).map(item => item.target));
  const current: AnswerJob[] = [], history: AnswerJob[] = [], seen = new Set<string>();
  for (const { job, target } of ordered) {
    if (historicalIds?.has(job.id) && !pendingJob(job) && job.request.scope !== 'exercise') {
      history.push(job); seen.add(target); continue;
    }
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
export const currentServiceJobs = (jobs: AnswerJob[], historicalIds?: ReadonlySet<string>): AnswerJob[] => partitionServiceJobs(jobs, historicalIds).current;
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
export const serviceLabels: Record<ServiceStatus, string> = { waiting: '排队中', processing: '正在处理', support: '需要跟进', completed: '已完成', stopped: '已停止', submitted: '已交卷', awaiting_review: '待公布成绩', graded: '成绩已公布' };
export function jobTitle(job: AnswerJob, courses: Course[] = []): string {
  const result = record(job.result);
  if (job.request.scope === 'all') return '全部课程';
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
function progressText(job: AnswerJob): string {
  const progress = jobProgress(job);
  if (job.request.scope === 'exercise' && ['submitted', 'awaiting_review', 'graded'].includes(serviceStatus(job))) return `已交卷${progress.total ? ` · 共 ${progress.total} 道题` : ''}`;
  return progress.total ? `${progress.completed} / ${progress.total} ${progress.unit}` : serviceStatus(job) === 'completed' ? '已完成' : '进度待更新';
}
/**
 * jobHomeworks returns one assignment list for a job whatever its state: the
 * live snapshot while it runs, the stored outcome once it has settled. Both
 * publish the same shape, so the pages share a single progress renderer.
 */
export function jobHomeworks(job: AnswerJob): ProgressItem[] {
  const live = job.progress?.homeworks;
  if (Array.isArray(live) && live.length) return live;
  const result = record(job.result);
  const rows = Array.isArray(result.homeworks) ? result.homeworks : Array.isArray(result.levels) ? result.levels : [];
  return rows.map(row => record(row) as ProgressItem);
}
/** currentStage names the step the job is on, e.g. "第 3 关 · 公开自测". */
export function currentStage(job: AnswerJob): string {
  if (!pendingJob(job)) return '';
  const progress = record(job.progress);
  const position = count(progress.current_position);
  const level = typeof progress.current_level === 'string' ? progress.current_level.trim() : '';
  const stage = stageLabel(typeof progress.stage === 'string' ? progress.stage : '');
  return [position ? `第 ${position} 关` : '', level, stage].filter(Boolean).join(' · ');
}
/** autoRetryNotice renders the automatic retry the backend has scheduled. */
export function autoRetryNotice(job: AnswerJob): string {
  return autoRetryText(job.result?.auto_retry);
}
/**
 * serviceReason explains a stop in the user's own words. A resource shortage
 * on Educoder is a "try again later", never a staff hand-off.
 */
export function serviceReason(job: AnswerJob): string {
  if (pendingJob(job)) return '';
  const reason = record(job.result).reason;
  return reasonText(typeof reason === 'string' ? reason : '');
}
function serviceAttention(job: AnswerJob): string {
  return serviceStatus(job) === 'support' && record(job.result).attention === 'upstream_question_conflict'
    ? '题目要求和评测样例矛盾，做好的保留，等课程方核对后继续。' : '';
}
/** sentence closes a clause so two reasons do not run together. */
const sentence = (value: string): string => !value ? '' : /[。！？，、]$/.test(value) ? value : `${value}。`;
/** jobElapsed is real wall time between two recorded timestamps, or nothing. */
function jobElapsed(job: AnswerJob): string {
  const started = job.started_at || job.created_at;
  if (pendingJob(job)) {
    const span = duration(started, new Date().toISOString());
    return span ? `已用时 ${span}` : '';
  }
  const span = duration(started, job.finished_at || job.updated_at);
  return span ? `用时 ${span}` : '';
}
/** homeworkSquares draws one square per assignment of a job. */
export function homeworkSquares(items: ProgressItem[], active: boolean, large = false): string {
  if (!items.length) return '';
  const counts = progressCounts(items);
  const cells = items.slice(0, 60).map(item => {
    const tone = settledTone(item.status, active);
    return `<i class="${tone === 'completed' ? 'done' : tone === 'processing' ? 'run' : tone === 'support' ? 'warn' : ''}"></i>`;
  }).join('');
  return `<div class="squares${large ? ' lg' : ''}" role="img" aria-label="${e(`${items.length} 份作业，完成 ${counts.completed} 份`)}">${cells}</div>`;
}
/**
 * assignmentTally is how many assignments a job finished out of how many. The
 * published counters are authoritative: the assignment list is a projection
 * that can be truncated, so it is only a fallback when no counter exists.
 */
function assignmentTally(job: AnswerJob): { completed: number; total: number } {
  const progress = jobProgress(job);
  if (job.request.scope !== 'exercise' && progress.unit === '份作业' && progress.total) return { completed: progress.completed, total: progress.total };
  const items = jobHomeworks(job);
  if (items.length) return { completed: progressCounts(items).completed, total: items.length };
  return { completed: progress.completed, total: progress.total };
}
/**
 * jobVerdict is the one line that answers "where does this task stand?" — what
 * happened, and what happens next. It never echoes the assignment a stopped
 * job was last working on as if the work were still going.
 */
export function jobVerdict(job: AnswerJob, historical: boolean): string {
  const items = jobHomeworks(job);
  const status = serviceStatus(job);
  const progress = jobProgress(job);
  const tally = assignmentTally(job);
  const elapsed = jobElapsed(job);
  const data = { ...record(job.progress), ...record(job.result) };
  const currentTitle = typeof data.current_title === 'string' ? data.current_title.trim() : '';
  const stage = currentStage(job);
  if (job.request.scope === 'exercise') {
    if (['submitted', 'awaiting_review'].includes(status)) return '已交卷，成绩以头歌公布为准。';
    if (status === 'graded') {
      const score = data.score_known === true && (typeof data.score === 'number' || typeof data.score === 'string') ? String(data.score) : '';
      const max = typeof data.max_score === 'number' || typeof data.max_score === 'string' ? String(data.max_score) : '';
      return score ? `成绩 ${e(score)}${max ? ` / ${e(max)}` : ''}` : '成绩已公布，以头歌为准。';
    }
    if (pendingJob(job)) return `正在做这场考试${elapsed ? ` · ${elapsed}` : ''}`;
  }
  const allDone = tally.total > 0 && tally.completed === tally.total;
  if (status === 'completed' || allDone && !pendingJob(job)) {
    return `<b>${tally.total && progress.unit === '份作业' ? `${tally.total} 份全部完成` : '全部完成'}</b>${elapsed ? ` · ${elapsed}` : ''}`;
  }
  if (pendingJob(job)) {
    const focus = currentTitle || items.filter(item => progressTone(item.status) === 'processing').map(item => String(item.title ?? '').trim()).filter(Boolean)[0] || '';
    const where = focus ? `正在做「${e(focus)}」${stage ? ` ${e(stage)}` : ''}` : '正在安排处理';
    const nth = progress.unit === '份作业' && tally.total ? `第 ${Math.min(tally.total, tally.completed + 1)} 份 · ` : '';
    return `${nth}<mark>${where}</mark>${elapsed ? ` · ${elapsed}` : ''}`;
  }
  const done = tally.total ? `<b>完成 ${tally.completed} / ${tally.total} ${progress.unit === '份作业' ? '份' : progress.unit}</b>` : '<b>没有完成</b>';
  const reason = historical ? '' : serviceReason(job);
  const tail = reason ? `，${e(reason)}` : status === 'stopped' ? '，已按请求停止' : '';
  return `${done}${tail}${elapsed ? ` · ${elapsed}` : ''}`;
}
/** historyResult is the ruled record's result column: a mark plus a tally. */
function historyResult(job: AnswerJob): string {
  const status = serviceStatus(job);
  const tally = assignmentTally(job);
  if (job.request.scope === 'exercise') {
    const label = status === 'graded' ? '成绩已公布' : ['submitted', 'awaiting_review'].includes(status) ? '已交卷' : status === 'stopped' ? '已停止' : '未完成';
    const done = ['graded', 'submitted', 'awaiting_review'].includes(status);
    return `<span class="state ${done ? 'done' : ''} res">${done ? `<span class="tick">${icon('tick')}</span>` : icon('bang')}${e(label)}</span>`;
  }
  if (status === 'completed' || tally.total > 0 && tally.completed === tally.total) {
    return `<span class="state done res"><span class="tick">${icon('tick')}</span>${tally.total ? `${tally.total} 份全部完成` : '全部完成'}</span>`;
  }
  if (status === 'stopped' && !tally.completed) return `<span class="state res">${icon('bang')}已停止</span>`;
  return `<span class="state run res">${icon('bang')}${tally.total ? `完成 ${tally.completed} / ${tally.total} 份` : '未完成'}</span>`;
}
/**
 * renderServiceJob is one live task slip: what is happening, how far it got,
 * and the single action available right now.
 */
export function renderServiceJob(job: AnswerJob, courses: Course[] = [], historical = false, action?: JobActionState): string {
  const status = serviceStatus(job);
  const items = jobHomeworks(job);
  const counts = progressCounts(items);
  const progress = jobProgress(job);
  const active = pendingJob(job);
  const retry = autoRetryNotice(job);
  const reason = historical ? '' : serviceReason(job) || serviceAttention(job) || (recoveryMessage(job) ? recoveryNotice(job) : '');
  const stage = currentStage(job);
  const focus = (typeof record(job.result).current_title === 'string' ? String(record(job.result).current_title).trim() : '')
    || items.filter(item => progressTone(item.status) === 'processing').map(item => String(item.title ?? '').trim()).filter(Boolean)[0] || '';
  const mark = active ? '<span class="pulse"></span>'
    : status === 'completed' ? `<span class="tick">${icon('tick')}</span>`
      : `<span class="mark warn" style="height:auto">${icon('bang')}</span>`;
  const scope = items.length ? `${items.length} 份作业` : progress.total ? `${progress.total} ${progress.unit}` : '';
  const started = dayTime(job.started_at || job.created_at);
  const elapsed = jobElapsed(job);
  const tally = items.length ? `${counts.completed} / ${items.length} 份作业` : progressText(job);
  const line = active
    ? `<p class="slip-stage">${focus ? `正在做「${e(focus)}」` : '正在安排处理'}${stage ? ` · <mark>${e(stage)}</mark>` : ''}</p>`
    : reason || retry ? `<p class="memo">${e(sentence(reason))}${retry ? `${e(retry)}，做好的关卡不会重做。` : ''}</p>` : '';
  return `<section class="card slip${historical ? ' slip-history' : ''}" data-service-record="${historical ? 'history' : 'current'}" aria-label="${e(jobTitle(job, courses))}">
    <div class="slip-top">${mark}<span class="slip-title">${e(jobTitle(job, courses))}</span>${scope ? `<span class="slip-scope">${e(scope)}</span>` : ''}${active ? '' : `<span class="slip-scope">${e(historical ? progressText(job) : serviceLabel(job))}</span>`}
      <div class="slip-actions">${renderJobActions(job, historical, action)}<button class="btn sm" data-job-detail="${e(job.id)}"${historical ? ' data-job-history="true"' : ''}>详情</button></div></div>
    ${line}${homeworkSquares(items, active, true)}
    <div class="slip-meta num"><span>${e(tally)}</span><span>${e([started ? `${started} 开始` : '', elapsed].filter(Boolean).join(' · '))}</span></div>
  </section>`;
}
export function renderJobs(jobs: AnswerJob[], courses: Course[] = [], historyOpen = false, actions: ReadonlyMap<string, JobActionState> = new Map(), historicalIds?: ReadonlySet<string>): string {
  const { current, history } = partitionServiceJobs(jobs, historicalIds);
  if (!current.length && !history.length) {
    return `<div class="card empty"><div class="ring">${icon('tick')}</div><h3>还没有任务</h3><p>在「我的课程」里选课程，交给我们就行。</p></div>`;
  }
  const records = history.map(job => {
    const when = dayTime(job.created_at);
    const span = duration(job.started_at || job.created_at, job.finished_at || job.updated_at);
    return `<li><button class="row" data-job-detail="${e(job.id)}" data-job-history="true"><span class="when num">${e(when)}</span><span class="what">${e(jobTitle(job, courses))}</span>${historyResult(job)}<span class="took num">${e(span)}</span>${icon('right')}</button></li>`;
  }).join('');
  return `${current.length ? `<div class="slips" data-service-current>${current.map(job => renderServiceJob(job, courses, false, actions.get(job.id))).join('')}</div>`
    : '<div class="card empty"><h3>现在没有要处理的任务</h3><p>结束的任务都收在下面的记录里。</p></div>'}
    ${history.length ? `<section class="card"><details class="hist" data-service-history${historyOpen ? ' open' : ''}><summary class="panel-head"><h2>历史记录<span>${history.length} 次</span></h2>${icon('down')}</summary><ul class="records" data-service-history-list>${records}</ul></details></section>` : ''}`;
}
/**
 * renderServiceDetail is the exercise-book sheet for one task: a verdict line,
 * the assignments down the red margin with their marks, and a square seal only
 * when every assignment passed.
 */
/** examNote states what an exam record means for the user, if anything. */
function examNote(job: AnswerJob, status: ServiceStatus): string {
  if (job.request.scope !== 'exercise') return '';
  if (status === 'support') return '考试事项需要服务人员跟进，请勿重复提交。';
  if (pendingJob(job)) return '我们会处理整场考试，完成后显示交卷状态。';
  return '';
}
export function renderServiceDetail(job: AnswerJob, courses: Course[] = [], historical = false, action?: JobActionState): string {
  const items = jobHomeworks(job);
  const counts = progressCounts(items);
  const status = serviceStatus(job);
  const active = pendingJob(job);
  const retry = autoRetryNotice(job);
  const started = dayTime(job.started_at || job.created_at);
  const complete = status === 'completed' && items.length > 0 && counts.completed === items.length;
  const eyebrow = historical
    ? `<span class="tag">历史</span>${e(started)} 开始`
    : `${active ? '<span class="pulse"></span>' : ''}${e(serviceLabel(job))}${started ? ` · ${e(started)} 开始` : ''}`;
  // The server's own recovery sentence is more specific than our summary, so
  // it wins when present.
  const note = !historical && (examNote(job, status) || serviceAttention(job) || recoveryMessage(job) || (retry ? `${retry}，做好的关卡不会重做。` : '')
    || (canRetryJob(job) ? '重试会接着处理原来的范围，已核验通过的必做关卡会自动跳过。' : ''));
  // The verdict already names the assignment tally, so the meta line only adds
  // a count it does not carry — levels or exam questions — plus the time spent.
  const tally = assignmentTally(job);
  const unit = jobProgress(job).unit;
  const meta = [unit !== '份作业' || !tally.total ? progressText(job) : '', jobElapsed(job)].filter(Boolean).join(' · ');
  const actions = renderJobActions(job, historical, action);
  return `<header class="sheet-head"><div class="eyebrow">${eyebrow}</div>
    <h2 id="modal-title">${e(jobTitle(job, courses))}</h2>
    <p class="verdict">${jobVerdict(job, historical)}</p>
    ${meta ? `<p class="sheet-meta num">${e(meta)}</p>` : ''}
    ${complete ? '<span class="seal square" role="img" aria-label="全部完成"><span>全</span><span>部</span><span>完</span><span>成</span></span>' : ''}</header>
    <div class="sheet-body">${note ? `<p class="memo sheet-note">${e(note)}</p>` : ''}
    ${items.length ? renderProgressTree(items, { historical, levels: true, parentActive: active })
      : `<p class="muted sheet-note">${historical ? '这次没有留下逐份进度。' : status === 'support' || status === 'stopped' ? '这次没有留下逐份进度，做好的结果保留。' : '进度会随任务推进更新。'}</p>`}
    ${items.length ? `<div class="legend sheet-note"><span><span class="tick">${icon('tick')}</span>已完成</span><span><span class="pulse"></span>正在做</span><span class="warn-key"><i class="bang-key"></i>需要注意</span><span><i class="blank-key"></i>待完成</span></div>` : ''}</div>
    <footer class="sheet-foot">${actions}<button class="btn" data-action="close-modal">关闭</button></footer>`;
}
/** marginMark is re-exported so the operator sheet draws the same pen. */
export { marginMark };

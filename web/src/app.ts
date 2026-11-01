import { ApiRequestError, BrowserApi } from './api.js';
import { partitionAdminJobs, renderAdmin, renderAdminJobDetail, type AdminPage, type AdminState } from './admin.js';
import { currentServiceJobs, jobProgress, jobTitle, pendingJob, renderJobs, renderServiceDetail, serviceJob, serviceLabels, serviceStatus } from './jobs.js';
import { count, escapeHtml as e, icon } from './ui.js';
import type { AnswerJob, Course, Exercise, Homework, User } from './types.js';

const root = document.querySelector<HTMLDivElement>('#app');
if (!root) throw new Error('Missing application root');
const api = new BrowserApi();
type Page = 'courses' | 'progress' | `admin-${AdminPage}`;
const state = {
  user: null as User | null, page: 'courses' as Page, courses: [] as Course[], jobs: [] as AnswerJob[],
  selected: [] as string[], cardRequired: false, loading: false, signingIn: false, creating: false, notice: '', loginError: '', search: '',
  modal: '' as string, modalAction: null as (() => Promise<void> | void) | null,
  progressHistoryOpen: false,
  admin: { overview: {}, users: [], jobs: [], audit: [], cards: [], loading: false, error: false, jobsView: 'current', jobsKind: 'service' } as AdminState,
};
let generation = 0;
let jobsRevision = 0;
let coursesRevision = 0;
let adminRevision = 0;
let modalRevision = 0;
let pollTimer: ReturnType<typeof setTimeout> | undefined;
let previousFocus: HTMLElement | null = null;
let courseDialog: { course: Course; homeworks: Homework[]; exercises: Exercise[] | null; tab: 'homework' | 'exercise' } | null = null;
const isAdmin = (): boolean => state.user?.role === 'admin' || Boolean(state.user?.permissions?.includes('admin'));
const accessible = (): boolean => state.user?.service_access !== false && state.user?.status !== 'disabled';
const activeJobs = (): AnswerJob[] => state.jobs.filter(job => serviceJob(job) && pendingJob(job));
function stopPoll(): void { if (pollTimer !== undefined) clearTimeout(pollTimer); pollTimer = undefined; }
function resetSession(message = ''): void {
  generation++; jobsRevision++; coursesRevision++; adminRevision++; modalRevision++; stopPoll(); courseDialog = null;
  Object.assign(state, { user: null, page: 'courses', courses: [], jobs: [], selected: [], loading: false, signingIn: false, creating: false, notice: '', loginError: message, modal: '', modalAction: null, search: '', progressHistoryOpen: false, admin: { overview: {}, users: [], jobs: [], audit: [], cards: [], loading: false, error: false, userSearch: '', userFilter: 'all' } });
  render();
}
function handleError(error: unknown): boolean {
  if (error instanceof ApiRequestError && error.isSessionExpired) { resetSession('登录已过期，请重新登录。你的任务进度会保留。'); return true; }
  return false;
}
function brandLogo(): string {
  return '<img class="brand-logo" src="/assets/brand-logo.png?v=0.1.46" alt="头歌妈妈" width="40" height="40" decoding="async">';
}

function avatar(): string {
  return state.user?.avatarUrl ? `<img class="avatar" src="${e(state.user.avatarUrl)}" alt="个人头像" data-avatar>` : `<span class="avatar">${e(state.user?.name.slice(0, 1) || '同')}</span>`;
}
function renderLogin(): string {
  return `<main id="view-login" class="login login-page"><aside class="login-brand" aria-label="品牌介绍"><div class="brand-mark">${brandLogo()}<div class="mark-text"><strong>头歌妈妈</strong><span>touge.mom</span></div></div><div class="login-brand-copy"><span class="login-kicker">头歌平台服务</span><h1>选好课程，<br>剩下交给我们。</h1><p class="lede">登录你的头歌账号，选择需要完成的课程。每一步进度，回来就能看见。</p><div class="brand-points"><ul><li><span class="dot">1</span><span>选择课程，或一键安排全部未完成作业</span></li><li><span class="dot">2</span><span>后台持续处理，关闭页面也不会中断</span></li><li><span class="dot">3</span><span>随时查看课程与作业完成进度</span></li></ul></div></div><p class="login-foot">你的头歌账号，就是这里的账号。</p></aside><section class="login-panel"><div class="login-panel-inner"><div class="login-kicker">TOUGE.MOM</div><h2>登录头歌妈妈</h2><p class="sub">使用头歌账号进入。无需额外注册，卡密仅用于开通服务。</p><form class="login-card" id="login-form"><div class="field"><label for="login">头歌账号</label><input id="login" name="login" autocomplete="username" placeholder="手机号 / 邮箱 / 学号" required maxlength="120"></div><div class="field"><label for="password">头歌密码</label><input id="password" name="password" type="password" autocomplete="current-password" placeholder="输入头歌密码" required maxlength="512"></div><div class="field"><label for="card-code">卡密 <span data-card-requirement>${state.cardRequired ? '必填' : '选填'}</span></label><input id="card-code" name="access_code" type="password" autocomplete="off" placeholder="${state.cardRequired ? '输入你的服务卡密' : '如有卡密，请在这里填写'}" maxlength="256" ${state.cardRequired ? 'required' : ''}><p class="field-help" data-card-help>${state.cardRequired ? '使用有效卡密开通服务，具体规则请联系服务人员。' : '卡密规则尚未开放，当前可暂不填写。'}</p></div>${state.loginError ? `<div class="form-feedback" role="alert">${e(state.loginError)}</div>` : ''}<button class="btn-primary button primary full" type="submit" ${state.signingIn ? 'disabled' : ''}>${state.signingIn ? '正在登录…' : '登录并进入'}</button><div class="login-meta"><span>对象：头歌平台</span><span>密码不会在本站保存</span></div></form><p class="login-foot">登录后只展示你的课程和进度，服务过程中的异常由服务人员处理。</p></div></section></main>`;
}
function navItem(page: Page, label: string, name: string): string { return `<button class="nav-link nav-item ${state.page === page ? 'active' : ''}" data-nav="${page}" ${state.page === page ? 'aria-current="page"' : ''}>${icon(name)}<span>${label}</span></button>`; }
function renderShell(): string {
  const admin = state.page.startsWith('admin-') && isAdmin();
  const consumerLinks = `${navItem('courses', '我的课程', 'courses')}${navItem('progress', '完成进度', 'progress')}${isAdmin() ? navItem('admin-overview', '管理后台', 'overview') : ''}`;
  const adminLinks = `${navItem('admin-overview', '服务概览', 'overview')}${navItem('admin-users', '用户管理', 'users')}${navItem('admin-operations', '任务运营', 'progress')}${navItem('admin-cards', '服务卡密', 'audit')}${navItem('admin-audit', '操作审计', 'audit')}`;
  const adminSideLinks = adminLinks.replaceAll('class="nav-link nav-item', 'class="side-link nav-item');
  const links = admin ? adminLinks : consumerLinks;
  const title = admin ? ({ 'admin-overview': '服务概览', 'admin-users': '用户管理', 'admin-operations': '任务运营', 'admin-audit': '操作审计', 'admin-cards': '服务卡密' } as Record<string, string>)[state.page] : state.page === 'courses' ? '我的课程' : '完成进度';
  const selectedCount = state.selected.length;
  const current = currentServiceJobs(state.jobs);
  const active = current.filter(pendingJob).length;
  const completed = current.filter(job => serviceStatus(job) === 'completed').length;
  return `<div id="view-app" class="app-shell ${admin ? 'admin-app' : 'consumer-app'}"><header class="global-nav topbar ${admin ? 'admin-topbar' : ''}"><div class="top-left"><button class="nav-brand" type="button" data-nav="${admin ? 'admin-overview' : 'courses'}" aria-label="头歌妈妈首页">${brandLogo()}<span class="name">头歌妈妈</span><span class="domain">touge.mom</span></button>${admin ? '<span class="admin-chip">服务管理</span>' : ''}</div><nav class="nav-links" aria-label="${admin ? '管理员导航' : '主导航'}">${links}</nav><div class="nav-user"><span class="connection"><i></i>${admin ? '管理会话' : '头歌已连接'}</span>${avatar()}<span class="user-name">${e(state.user?.name || '同学')}</span><button class="icon-button" data-action="logout" aria-label="退出登录" title="退出登录">${icon('logout')}</button></div></header><div class="mobile-nav">${links}${isAdmin() ? navItem(admin ? 'courses' : 'admin-overview', admin ? '用户端' : '管理后台', admin ? 'courses' : 'overview') : ''}</div><div class="${admin ? 'shell admin-shell' : 'workspace consumer-workspace'}">${admin ? `<aside class="side admin-side" aria-label="管理员导航"><div class="side-label">服务管理</div><nav class="side-nav">${adminSideLinks}</nav><div class="side-foot"><p class="t">服务工作台</p><p>异常与执行记录只在管理端查看，用户端只展示完成进度。</p><button class="btn ghost text-button" data-nav="courses">${icon('courses')}返回用户端</button></div></aside>` : `<aside class="side consumer-side" aria-label="侧栏概况"><div class="side-card"><h3>当前概况</h3><div class="stat-row"><span class="l">我的课程</span><span class="n">${state.courses.length}</span></div><div class="stat-row"><span class="l">已选中</span><span class="n">${selectedCount}</span></div><div class="stat-row"><span class="l">处理中</span><span class="n">${active}</span></div><div class="stat-row"><span class="l">已完成安排</span><span class="n">${completed}</span></div></div><div class="side-card"><h3>服务状态</h3><span class="side-pill"><i></i> 头歌账号已连接</span><p class="side-note">你只需要选择课程并查看进度，其他事情交给服务人员。</p></div><div class="side-card state-legend"><h3>状态图例</h3><div><span class="badge waiting">待安排</span><span class="badge processing">处理中</span><span class="badge completed">已完成</span></div></div></aside>`}<main class="main ${admin ? 'admin-main' : 'consumer-main'}">${state.notice ? `<div class="service-notice" role="status">${e(state.notice)}</div>` : ''}${admin ? renderAdmin(state.page.replace('admin-', '') as AdminPage, state.admin) : state.page === 'courses' ? renderCourses() : renderProgress()}<footer class="page-footer">头歌妈妈 · 把安排交给我们</footer></main></div></div>`;
}
function courseService(course: string): AnswerJob | undefined {
  const matching = currentServiceJobs(state.jobs).filter(job => job.request.scope !== 'exercise' && (job.request.scope === 'all' || job.request.course === course));
  return matching.find(pendingJob) ?? matching[0];
}
function coursePending(course: string): boolean {
  const latest = courseService(course);
  return Boolean(latest && (pendingJob(latest) || serviceStatus(latest) === 'support'));
}
function allServicePending(): boolean {
  return activeJobs().some(job => job.request.scope !== 'exercise') || currentServiceJobs(state.jobs).some(job => job.request.scope === 'all' && serviceStatus(job) === 'support');
}
function renderCourses(): string {
  const visible = state.courses.filter(course => !state.search || course.name.toLowerCase().includes(state.search.toLowerCase()));
  const active = activeJobs();
  const current = currentServiceJobs(state.jobs);
  const latest = active[0] ?? current.find(job => serviceStatus(job) === 'support') ?? current.find(job => ['completed', 'submitted', 'awaiting_review', 'graded'].includes(serviceStatus(job)));
  const completed = current.filter(job => serviceStatus(job) === 'completed').length;
  const examSubmitted = latest?.request.scope === 'exercise' && ['submitted', 'awaiting_review', 'graded'].includes(serviceStatus(latest));
  const selectable = state.courses.filter(course => !coursePending(course.identifier));
  const selectedAll = selectable.length > 0 && selectable.every(course => state.selected.includes(course.identifier));
  const latestProgress = latest ? jobProgress(latest) : null;
  return `<div class="page-head page-heading"><div><span class="eyebrow">我的学习</span><h1>今天，也轻松一点。</h1><p>${e(state.user?.name || '同学')}，选好课程，我们来完成。</p></div><div class="head-actions"><button class="btn button" data-action="refresh" ${state.loading ? 'disabled' : ''}>${icon('refresh')}刷新课程</button><button class="btn accent button primary" data-action="start-all" ${state.loading || state.creating || !state.courses.length || !accessible() || allServicePending() ? 'disabled' : ''}>一键完成全部未完成作业 ${icon('arrow')}</button></div></div>${!accessible() ? '<div class="service-notice">你的服务暂未开通，请联系服务人员。已有完成进度仍可查看。</div>' : ''}<section class="strip" aria-label="课程摘要"><div class="strip-item"><div class="k">我的课程</div><div class="v">${state.courses.length}</div><div class="h">头歌平台同步</div></div><div class="strip-item"><div class="k">当前安排</div><div class="v">${current.length}</div><div class="h">后台持续处理</div></div><div class="strip-item"><div class="k">已选中</div><div class="v">${selectedCount(state.selected)}</div><div class="h">可多选后安排</div></div><div class="strip-item"><div class="k">最近安排进度</div><div class="v">${latestProgress?.percent ?? (completed ? 100 : 0)}%</div><div class="h">当前服务记录</div></div></section><div class="content-grid"><section class="panel-box course-panel"><div class="panel-toolbar"><div><h2>我的课程 <span class="count">${state.courses.length}</span></h2><p class="muted">选择课程，安排其中尚未完成的必做作业。</p></div><div class="toolbar-row"><label class="search-input"><span class="sr-only">搜索课程</span><input data-search placeholder="搜索课程" value="${e(state.search)}"></label><button class="btn button" data-action="select-all" ${state.creating || !accessible() ? 'disabled' : ''}>${selectedAll ? '清空选择' : '全选可安排'}</button></div></div>${state.loading ? '<div class="loading" role="status"><span class="spinner"></span>正在同步你的课程…</div>' : visible.length ? `<div class="course-table-wrap"><table class="course-table" aria-label="我的课程列表"><thead><tr><th class="select-col">选</th><th>课程</th><th>作业</th><th>状态</th><th></th></tr></thead><tbody>${visible.map((course, index) => { const pending = coursePending(course.identifier); const selected = state.selected.includes(course.identifier); return `<tr class="course-row ${selected ? 'selected' : ''}" data-course-row="${e(course.identifier)}"><td class="select-col"><label class="course-select"><input type="checkbox" data-select-course="${e(course.identifier)}" ${selected ? 'checked' : ''} ${pending || state.creating || !accessible() ? 'disabled' : ''}><span class="sr-only">选择${e(course.name)}</span></label></td><td><div class="course-name"><span class="course-index">${String(index + 1).padStart(2, '0')}</span><div><strong>${e(course.name)}</strong><span class="course-meta">${e(course.identifier)}</span></div></div></td><td><span class="table-number">${course.homeworkCount === undefined ? '—' : `${course.homeworkCount} 份`}</span></td><td><span class="badge ${pending ? 'processing' : 'waiting'}">${pending ? serviceStatus(courseService(course.identifier)!) === 'support' ? '服务处理中' : '服务已安排' : '可安排'}</span></td><td><button class="text-button" data-course-detail="${e(course.identifier)}">查看详情 ${icon('arrow')}</button></td></tr>`; }).join('')}</tbody></table></div>` : `<div class="panel empty"><h3>${state.search ? '没有找到这门课程' : '还没有课程'}</h3><p>${state.search ? '试试其他课程名称。' : '加入头歌课堂后，刷新这里即可同步。'}</p></div>`}<div class="selection-bar"><label class="select-all"><input type="checkbox" data-action="select-all" ${selectedAll ? 'checked' : ''} ${state.creating || !accessible() ? 'disabled' : ''}> 全选可安排课程</label><span>已选 ${selectedCount(state.selected)} 门课程</span><button class="button primary" data-action="start-selected" ${!state.selected.length || state.creating || !accessible() ? 'disabled' : ''}>${state.creating ? '正在安排…' : '完成所选课程'}</button></section><aside class="status-rail side-card"><section class="current-service"><div class="rail-label"><i></i> 我的完成进度</div><h2>${latest ? serviceLabels[serviceStatus(latest)] : completed ? '安排已完成' : '一切准备就绪'}</h2><p>${latest ? e(jobTitle(latest, state.courses)) : '选好课程后，我们会在这里跟进。'}</p>${latest && examSubmitted ? `<p class="rail-exam-status">已交卷${jobProgress(latest).total ? ` · 共 ${jobProgress(latest).total} 道题` : ''}</p>` : latest ? `<div class="large-progress"><strong>${jobProgress(latest).completed}</strong><span>/ ${jobProgress(latest).total || '—'} ${jobProgress(latest).unit}</span></div><div class="rail-track"><span style="width:${jobProgress(latest).percent}%"></span></div>` : '<div class="ready-mark" aria-hidden="true">✓</div>'}<p class="rail-note">${latest && serviceStatus(latest) === 'support' ? '放心，服务人员正在处理，无需重复提交。' : '关闭页面也没关系，进度会为你保留。'}</p><button class="text-button" data-nav="progress">查看全部进度 ${icon('arrow')}</button></section><section class="panel reassurance"><h3>只需要三步</h3><ol><li><b>选择课程</b><p>按需要选择，或一键安排全部。</p></li><li><b>交给我们</b><p>处理过程中无需守候。</p></li><li><b>查看进度</b><p>完成后，结果清楚可见。</p></li></ol></section></aside></div>`;
}
function selectedCount(values: string[]): number { return values.length; }
function renderProgress(): string {
  return `<div class="page-head page-heading"><div><span class="eyebrow">服务记录</span><h1>完成进度</h1><p>查看正在处理和已完成的课程。</p></div><div class="head-actions"><button class="btn button" data-action="jobs-refresh">${icon('refresh')}刷新进度</button></div></div>${renderJobs(state.jobs, state.courses, state.progressHistoryOpen)}`;
}
function exerciseStatus(exercise: Exercise): { label: string; tone: string } {
  if (exercise.submitted) return exercise.scoreKnown ? { label: '成绩已公布', tone: 'completed' } : { label: '已交卷 · 待公布成绩', tone: 'processing' };
  const code = (exercise.reasonCode || '').toLowerCase().replace(/^exercise_/, '');
  if (!exercise.available || exercise.actionRequired === true || ['locked', 'proctoring_required', 'not_started', 'deadline_passed', 'expired', 'action_required'].includes(code)) return { label: '需在头歌处理', tone: 'waiting' };
  return { label: '待完成', tone: 'processing' };
}
function exerciseReason(exercise: Exercise): string {
  if (exercise.submitted) return exercise.scoreKnown && exercise.score !== undefined ? `成绩 ${exercise.score}${exercise.maxScore !== undefined ? ` / ${exercise.maxScore}` : ''}` : '已交卷，成绩公布后可查看';
  switch ((exercise.reasonCode || '').toLowerCase().replace(/^exercise_/, '')) {
    case 'locked': return '考试被锁定，请先在头歌完成解锁流程。';
    case 'proctoring_required': return '考试需要监考或浏览器确认，请在头歌进入。';
    case 'not_started': return '考试尚未开始，请按头歌开放时间进入。';
    case 'deadline_passed':
    case 'expired': return '考试已截止，请在头歌查看处理入口。';
    case 'action_required': return '入场事项需要在头歌确认，请先在头歌处理。';
    default: return exercise.available && exercise.actionRequired !== true ? '确认后由服务人员完成整场考试。' : '当前不可安排，请在头歌查看处理入口。';
  }
}
function exerciseLink(course: Course, exercise: Exercise): string {
  const path = `/classrooms/${encodeURIComponent(course.identifier)}/exercise`;
  let url = `https://www.educoder.net${path}`;
  try {
    const source = new URL(exercise.url || url);
    if (source.protocol === 'https:' && ['www.educoder.net', 'educoder.net'].includes(source.hostname) && !source.username && !source.password && !source.port && (source.pathname === path || source.pathname.startsWith(`${path}/${exercise.id}/`))) url = `${source.origin}${source.pathname}`;
  } catch { /* Use the verified classroom exam list if a source URL is malformed. */ }
  return `<a class="text-button" href="${e(url)}" target="_blank" rel="noopener noreferrer">在头歌查看 ${icon('arrow')}</a>`;
}
function renderCourseDetail(course: Course, homeworks: Homework[], exercises: Exercise[] | null, tab: 'homework' | 'exercise'): string {
  const homeworkBody = `<p class="subtext">${homeworks.length} 份作业，只安排必做关卡，已完成的会自动跳过。</p><ul class="completion-list">${homeworks.map(homework => {
    const complete = Boolean(homework.requiredProgressKnown && homework.requiredChallengeCount && (homework.finishedRequiredChallengeCount ?? 0) >= homework.requiredChallengeCount);
    return `<li><span class="completion-icon ${complete ? 'done' : ''}">${complete ? '✓' : '·'}</span><div><strong>${e(homework.name)}</strong><p>${homework.requiredProgressKnown ? `${complete ? '已完成' : '待完成'} · ${count(homework.finishedRequiredChallengeCount)} / ${count(homework.requiredChallengeCount)} 必做关卡` : '安排时核验必做进度'}</p></div></li>`;
  }).join('') || '<li>这门课程暂无需要安排的必做作业。</li>'}</ul>`;
  const examBody = exercises === null ? '<div class="loading"><span class="spinner"></span>正在同步在线考试…</div>' : exercises.length ? `<p class="subtext">在线考试单独安排，不包含在一键完成课程作业中；已交卷的考试不会重复安排。</p><ul class="completion-list">${exercises.map(exercise => { const status = exerciseStatus(exercise); return `<li><span class="completion-icon ${status.tone === 'completed' ? 'done' : ''}">${status.tone === 'completed' ? '✓' : '·'}</span><div class="exercise-row"><strong>${e(exercise.name)}</strong><p>${status.label}${exercise.totalQuestions ? ` · ${exercise.totalQuestions} 道题` : ''}${exercise.deadline ? ` · 截止 ${e(exercise.deadline)}` : ''}</p><p>${e(exerciseReason(exercise))}</p><div class="exercise-actions">${exercise.submitted || !exercise.available || exercise.actionRequired === true ? '' : `<button class="button primary compact" data-exam-submit="${exercise.id}">安排完成考试</button>`}${exerciseLink(course, exercise)}</div></div></li>`; }).join('')}</ul>` : '<div class="empty"><h3>暂无在线考试</h3><p>这门课程当前没有可查看的在线考试。</p></div>';
  return `<h2 id="modal-title">${e(course.name)}</h2><div class="detail-tabs" role="tablist"><button class="detail-tab ${tab === 'homework' ? 'selected' : ''}" data-detail-tab="homework" role="tab" aria-selected="${tab === 'homework'}">课程实验</button><button class="detail-tab ${tab === 'exercise' ? 'selected' : ''}" data-detail-tab="exercise" role="tab" aria-selected="${tab === 'exercise'}">在线考试</button></div>${tab === 'homework' ? homeworkBody : examBody}<div class="modal-actions"><button class="button" data-action="close-modal">关闭</button></div>`;
}
function render(): void {
  root!.innerHTML = `${state.user ? renderShell() : renderLogin()}${state.modal ? `<div class="modal-backdrop"><section class="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title" tabindex="-1"><button class="icon-button modal-close" data-action="close-modal" aria-label="关闭">${icon('close')}</button><div class="modal-content">${state.modal}</div></section></div>` : ''}`;
  bindEvents();
}
function showModal(content: string, action: (() => Promise<void> | void) | null = null): void {
  previousFocus = document.activeElement as HTMLElement | null; modalRevision++;
  state.modal = content; state.modalAction = action; render();
  document.querySelector<HTMLButtonElement>('[data-action="close-modal"]')?.focus();
}
function closeModal(): void { state.modal = ''; state.modalAction = null; modalRevision++; courseDialog = null; render(); previousFocus?.isConnected && previousFocus.focus(); }
function scheduleJobs(): void {
  stopPoll(); if (!state.user) return;
  pollTimer = setTimeout(() => { pollTimer = undefined; void loadJobs(); }, activeJobs().length ? 3000 : 15000);
}
async function loadJobs(): Promise<void> {
  if (!state.user) return; const revision = ++jobsRevision; const session = generation;
  try {
    const result = await api.jobs();
    if (session !== generation || revision !== jobsRevision) return;
    state.jobs = result.jobs; state.selected = state.selected.filter(id => !coursePending(id)); if (!state.modal) render();
  } catch (error) {
    if (session !== generation || revision !== jobsRevision) return;
    if (handleError(error)) return;
  }
  scheduleJobs();
}
async function loadCourses(): Promise<void> {
  const revision = ++coursesRevision; const session = generation; state.loading = true; render();
  try {
    const courses = await api.courses();
    if (session !== generation || revision !== coursesRevision) return;
    state.courses = courses; state.selected = state.selected.filter(id => courses.some(course => course.identifier === id));
    state.notice = '';
  } catch (error) {
    if (session !== generation || revision !== coursesRevision) return;
    if (handleError(error)) return;
    state.notice = '课程暂未同步完成，服务人员会协助处理。请稍后回来查看。';
  }
  state.loading = false; render();
}
async function login(event: Event): Promise<void> {
  event.preventDefault(); if (state.signingIn) return;
  const form = event.currentTarget as HTMLFormElement;
  const value = (name: string): string => (form.elements.namedItem(name) as HTMLInputElement).value;
  const account = value('login').trim(); const password = value('password'); const card = value('access_code').trim();
  if (!account || !password) { state.loginError = '请输入头歌账号和密码。'; render(); return; }
  form.reset(); const session = ++generation; state.signingIn = true; state.loginError = ''; render();
  try {
    const user = await api.login(account, password, card);
    if (session !== generation) return;
    state.user = user; state.signingIn = false; render(); void loadJobs(); void loadCourses();
  } catch (error) {
    if (session !== generation) return;
    const code = error instanceof ApiRequestError ? error.code : '';
    const messages: Record<string, string> = { AUTH_FAILED: '账号或密码不正确，请检查后重新登录。', CARD_REQUIRED: '请填写你的卡密。', CARD_INVALID: '卡密暂未验证通过，请联系服务人员。', USER_SUSPENDED: '账号服务暂不可用，请联系服务人员。', ACCOUNT_DISABLED: '账号服务暂不可用，请联系服务人员。', ACCESS_CARD_REQUIRED: '请填写你的卡密。', ACCESS_CARD_INVALID: '卡密暂未验证通过，请联系服务人员。', RATE_LIMITED: '登录有点频繁，请稍后再试。' };
    state.signingIn = false; state.loginError = messages[code || ''] || '登录暂未完成，请稍后再试或联系服务人员。'; render();
  }
}
async function logout(): Promise<void> { resetSession(); try { await api.logout(); } catch { /* The local account context has already been cleared. */ } }
function confirmStart(all: boolean): void {
  if (state.creating || !accessible() || all && allServicePending()) return;
  const selected = [...state.selected].filter(id => !coursePending(id));
  if (!all && !selected.length) return;
  const names = all ? state.courses.map(course => course.name) : state.courses.filter(course => selected.includes(course.identifier)).map(course => course.name);
  showModal(`<h2 id="modal-title">${all ? '完成全部未完成作业？' : '把这些课程交给我们？'}</h2><p class="subtext">只安排必做关卡，已完成和选做内容会自动跳过。</p><ul class="confirm-courses">${names.map(name => `<li>${icon('check')}${e(name)}</li>`).join('')}</ul><div class="detail-note">处理期间无需守候。完成进度随时可查，遇到问题会由服务人员跟进。</div><div class="modal-actions"><button class="button" data-action="close-modal">再看看</button><button class="button primary" data-action="modal-confirm">确认安排</button></div>`, () => startService(all, selected));
}
async function startService(all: boolean, selected: string[]): Promise<void> {
  if (state.creating || !accessible()) return;
  selected = selected.filter(id => !coursePending(id));
  if (all ? allServicePending() : !selected.length) { closeModal(); return; }
  jobsRevision++; stopPoll();
  const session = generation; state.modal = ''; state.modalAction = null; state.creating = true; render();
  const requests = all ? [{ scope: 'all' as const, mode: 'submit' as const }] : selected.map(course => ({ scope: 'course' as const, course, mode: 'submit' as const }));
  const responses = await Promise.allSettled(requests.map(request => api.createJob({ ...request, idempotency_key: crypto.randomUUID() })));
  if (session !== generation) return;
  let accepted = 0; let expired = false;
  for (const response of responses) {
    if (response.status === 'fulfilled') { accepted++; state.jobs = [response.value, ...state.jobs.filter(job => job.id !== response.value.id)]; }
    else if (response.reason instanceof ApiRequestError && response.reason.isSessionExpired) expired = true;
  }
  if (expired) { resetSession('登录已过期，请重新登录后查看进度。'); return; }
  state.creating = false; state.selected = [];
  state.notice = accepted === responses.length ? '已收到你的安排，接下来交给我们。完成进度会自动更新。' : '你的安排正在核对，请先查看完成进度，无需重复提交。';
  state.page = 'progress'; render(); void loadJobs();
}
async function courseDetail(id: string): Promise<void> {
  const course = state.courses.find(item => item.identifier === id); if (!course) return;
  courseDialog = { course, homeworks: [], exercises: null, tab: 'homework' };
  showModal(`<h2 id="modal-title">${e(course.name)}</h2><div class="detail-tabs" role="tablist"><button class="detail-tab selected" data-detail-tab="homework" role="tab" aria-selected="true">课程实验</button><button class="detail-tab" data-detail-tab="exercise" role="tab" aria-selected="false">在线考试</button></div><div class="loading"><span class="spinner"></span>正在同步作业…</div>`);
  const revision = modalRevision; const session = generation;
  try {
    const homeworks = await api.homeworks(id);
    if (session !== generation || revision !== modalRevision) return;
    courseDialog = { course, homeworks, exercises: null, tab: 'homework' };
    state.modal = renderCourseDetail(course, homeworks, null, 'homework'); render();
  } catch (error) {
    if (session !== generation || revision !== modalRevision) return;
    if (handleError(error)) return;
    state.modal = `<h2 id="modal-title">${e(course.name)}</h2><div class="detail-tabs"><button class="detail-tab selected" data-detail-tab="homework">课程实验</button><button class="detail-tab" data-detail-tab="exercise">在线考试</button></div><p class="subtext">作业正在同步，服务人员会协助处理，请稍后查看。</p>`; render();
  }
}
async function courseExercises(course: Course): Promise<void> {
  const revision = ++modalRevision; const session = generation;
  if (!courseDialog || courseDialog.course.identifier !== course.identifier) return;
  courseDialog.tab = 'exercise';
  state.modal = renderCourseDetail(course, [], null, 'exercise'); render();
  try {
    const result = await api.exercises(course.identifier);
    if (session !== generation || revision !== modalRevision) return;
    if (courseDialog) courseDialog.exercises = result.items;
    state.modal = renderCourseDetail(course, [], result.items, 'exercise'); render();
  } catch (error) {
    if (session !== generation || revision !== modalRevision) return;
    if (handleError(error)) return;
    state.modal = `<h2 id="modal-title">${e(course.name)}</h2><p class="subtext">在线考试暂未同步完成，服务人员会协助处理。</p><div class="modal-actions"><button class="button" data-action="close-modal">关闭</button></div>`; render();
  }
}
function confirmExercise(course: Course, exercise: Exercise): void {
  if (state.creating || !accessible() || exercise.submitted || !exercise.available || exercise.actionRequired === true) return;
  const existing = (): boolean => currentServiceJobs(state.jobs).some(job => job.request.scope === 'exercise' && job.request.course === course.identifier && 'exercise_id' in job.request && job.request.exercise_id === exercise.id && (pendingJob(job) || serviceStatus(job) === 'support' || ['submitted', 'awaiting_review', 'graded'].includes(serviceStatus(job))));
  if (existing()) { closeModal(); state.page = 'progress'; render(); return; }
  showModal(`<h2 id="modal-title">安排这场考试？</h2><p class="subtext">${e(exercise.name)}</p><div class="detail-note">本次仅安排这场考试，完成后会提交答案并核对交卷状态。需要在头歌处理的入场事项会单独提示。</div><div class="modal-actions"><button class="button" data-action="close-modal">取消</button><button class="button primary" data-action="modal-confirm">确认安排</button></div>`, async () => {
    if (state.creating || !accessible() || existing()) { closeModal(); return; }
    const session = generation; state.creating = true; closeModal();
    try {
      const response = await api.createJob({ scope: 'exercise', course: course.identifier, exercise_id: exercise.id, mode: 'submit', idempotency_key: crypto.randomUUID() });
      if (session !== generation) return;
      state.jobs = [response, ...state.jobs.filter(job => job.id !== response.id)]; state.page = 'progress'; state.notice = '考试已安排，完成进度会自动更新。';
    } catch (error) { if (session !== generation || handleError(error)) return; state.notice = '考试安排正在核对，请查看完成进度，无需重复提交。'; }
    if (session === generation) { state.creating = false; render(); void loadJobs(); }
  });
}
async function loadAdmin(): Promise<void> {
  if (!isAdmin()) return; const session = generation; const revision = ++adminRevision;
  state.admin.loading = true; state.admin.error = false; render();
  try {
    const [overview, users, jobs, audit, cards] = await Promise.all([api.adminOverview(), api.adminUsers(), api.adminJobs(), api.adminAudit(), api.adminCards()]);
    if (session !== generation || revision !== adminRevision) return;
    Object.assign(state.admin, { overview, users, jobs, audit, cards, loading: false, error: false }); render();
  } catch (error) {
    if (session !== generation || revision !== adminRevision) return;
    if (handleError(error)) return;
    if (error instanceof ApiRequestError && error.status === 403) { state.page = 'courses'; state.notice = '当前账号未获管理授权。'; }
    state.admin.loading = false; state.admin.error = true; render();
  }
}
function navigate(page: string): void {
  if (!['courses', 'progress', 'admin-overview', 'admin-users', 'admin-operations', 'admin-audit', 'admin-cards'].includes(page)) return;
  if (page.startsWith('admin-') && !isAdmin()) return;
  state.page = page as Page; state.notice = ''; render();
  if (page.startsWith('admin-')) void loadAdmin(); else if (page === 'progress') void loadJobs();
}
function bindEvents(): void {
  document.querySelector<HTMLFormElement>('#login-form')?.addEventListener('submit', event => { void login(event); });
  document.querySelectorAll<HTMLButtonElement>('[data-nav]').forEach(button => button.addEventListener('click', () => navigate(button.dataset.nav!)));
  document.querySelectorAll<HTMLInputElement>('[data-select-course]').forEach(input => input.addEventListener('change', () => {
    const id = input.dataset.selectCourse!; state.selected = input.checked ? [...new Set([...state.selected, id])] : state.selected.filter(value => value !== id); render();
  }));
  document.querySelector<HTMLInputElement>('[data-search]')?.addEventListener('input', event => {
    const input = event.currentTarget as HTMLInputElement; const position = input.selectionStart;
    state.search = input.value; render(); const next = document.querySelector<HTMLInputElement>('[data-search]'); next?.focus(); if (position !== null) next?.setSelectionRange(position, position);
  });
  document.querySelectorAll<HTMLButtonElement>('[data-course-detail]').forEach(button => button.addEventListener('click', () => { void courseDetail(button.dataset.courseDetail!); }));
  document.querySelectorAll<HTMLButtonElement>('[data-detail-tab]').forEach(button => button.addEventListener('click', () => {
    if (!courseDialog) return;
    if (button.dataset.detailTab === 'exercise') void courseExercises(courseDialog.course);
    else void courseDetail(courseDialog.course.identifier);
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-exam-submit]').forEach(button => button.addEventListener('click', () => {
    const exerciseID = Number(button.dataset.examSubmit);
    const exercise = courseDialog?.exercises?.find(item => item.id === exerciseID);
    if (courseDialog && exercise) confirmExercise(courseDialog.course, exercise);
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-job-detail]').forEach(button => button.addEventListener('click', () => {
    const job = state.jobs.find(item => item.id === button.dataset.jobDetail); if (job) showModal(`<div id="modal-title">${renderServiceDetail(job, state.courses, button.dataset.jobHistory === 'true')}</div>`);
  }));
  document.querySelector<HTMLDetailsElement>('[data-service-history]')?.addEventListener('toggle', event => {
    state.progressHistoryOpen = (event.currentTarget as HTMLDetailsElement).open;
  });
  document.querySelectorAll<HTMLButtonElement>('[data-admin-job-detail]').forEach(button => button.addEventListener('click', () => {
    if (!isAdmin()) return;
    const job = state.admin.jobs.find(item => item.id === button.dataset.adminJobDetail);
    if (job) showModal(`<div id="modal-title">${renderAdminJobDetail(job, partitionAdminJobs(state.admin.jobs).history.some(item => item.id === job.id))}</div>`);
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-admin-jobs-view]').forEach(button => button.addEventListener('click', () => {
    if (!isAdmin()) return;
    state.admin.jobsView = button.dataset.adminJobsView === 'history' ? 'history' : 'current'; render();
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-admin-jobs-kind]').forEach(button => button.addEventListener('click', () => {
    if (!isAdmin()) return;
    const kind = button.dataset.adminJobsKind;
    state.admin.jobsKind = kind === 'collection' || kind === 'all' ? kind : 'service'; render();
  }));
  document.querySelector<HTMLInputElement>('[data-admin-user-search]')?.addEventListener('input', event => {
    if (!isAdmin()) return;
    const input = event.currentTarget as HTMLInputElement; const position = input.selectionStart;
    state.admin.userSearch = input.value; render();
    const next = document.querySelector<HTMLInputElement>('[data-admin-user-search]'); next?.focus();
    if (position !== null) next?.setSelectionRange(position, position);
  });
  document.querySelectorAll<HTMLButtonElement>('[data-admin-user-filter]').forEach(button => button.addEventListener('click', () => {
    if (!isAdmin()) return;
    const value = button.dataset.adminUserFilter;
    state.admin.userFilter = value === 'active' || value === 'disabled' ? value : 'all';
    render();
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-admin-show-jobs]').forEach(button => button.addEventListener('click', () => {
    if (!isAdmin()) return;
    state.admin.jobsView = 'current'; state.admin.jobsKind = button.dataset.adminShowJobs === 'collection' ? 'collection' : 'service'; navigate('admin-operations');
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-user-status]').forEach(button => button.addEventListener('click', () => {
    if (!isAdmin()) return; const id = button.dataset.userStatus!; const status = button.dataset.nextStatus === 'active' ? 'active' : 'disabled';
    showModal(`<h2 id="modal-title">${status === 'active' ? '恢复' : '暂停'}该用户的服务？</h2><p class="subtext">此操作会记录到管理审计。不会更改用户的头歌账号。</p><div class="modal-actions"><button class="button" data-action="close-modal">取消</button><button class="button primary" data-action="modal-confirm">确认${status === 'active' ? '恢复' : '暂停'}</button></div>`, async () => { await adminAction(() => api.adminUserStatus(id, status)); });
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-admin-cancel]').forEach(button => button.addEventListener('click', () => {
    if (!isAdmin()) return; const id = button.dataset.adminCancel!;
    showModal('<h2 id="modal-title">停止该任务？</h2><p class="subtext">会保留已完成的结果，停止后续处理。此操作会记录到管理审计。</p><div class="modal-actions"><button class="button" data-action="close-modal">取消</button><button class="button primary" data-action="modal-confirm">确认停止</button></div>', async () => { await adminAction(() => api.adminCancelJob(id)); });
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-revoke-card]').forEach(button => button.addEventListener('click', () => {
    if (!isAdmin()) return; const id = button.dataset.revokeCard!;
    showModal('<h2 id="modal-title">停用这张卡密？</h2><p class="subtext">停用后将不能用于开通新的服务。此操作会记录到审计。</p><div class="modal-actions"><button class="button" data-action="close-modal">取消</button><button class="button primary" data-action="modal-confirm">确认停用</button></div>', async () => { await adminAction(() => api.adminRevokeCard(id)); });
  }));
  document.querySelectorAll<HTMLElement>('[data-action]').forEach(button => button.addEventListener(button instanceof HTMLInputElement ? 'change' : 'click', () => {
    switch (button.dataset.action) {
      case 'logout': void logout(); break;
      case 'refresh': void loadCourses(); break;
      case 'jobs-refresh': void loadJobs(); break;
      case 'create-card': newCard(); break;
      case 'admin-refresh': void loadAdmin(); break;
      case 'start-all': confirmStart(true); break;
      case 'start-selected': confirmStart(false); break;
      case 'select-all': {
        const checked = button instanceof HTMLInputElement ? button.checked : !(state.selected.length && state.selected.length === state.courses.filter(course => !coursePending(course.identifier)).length);
        state.selected = checked ? state.courses.filter(course => !coursePending(course.identifier)).map(course => course.identifier) : [];
        render(); break;
      }
      case 'close-modal': closeModal(); break;
      case 'modal-confirm': { const action = state.modalAction; state.modalAction = null; if (action) void action(); break; }
    }
  }));
  document.querySelector<HTMLImageElement>('[data-avatar]')?.addEventListener('error', () => { if (state.user) { state.user.avatarUrl = undefined; render(); } });
}
function newCard(): void {
  if (!isAdmin()) return;
  showModal('<h2 id="modal-title">新建服务卡密</h2><p class="subtext">当前仅创建一次性服务凭证，不设置价格或套餐。</p><label class="card-label" for="card-label">备注</label><input class="card-field" id="card-label" maxlength="80" placeholder="例如：课程服务，张同学"><div class="modal-actions"><button class="button" data-action="close-modal">取消</button><button class="button primary" data-action="modal-confirm">创建卡密</button></div>', async () => {
    const label = document.querySelector<HTMLInputElement>('#card-label')?.value.trim() || '服务卡密';
    const session = generation;
    try {
      const result = await api.adminCreateCard(label); if (session !== generation || !isAdmin()) return;
      const code = typeof result.access_code === 'string' ? result.access_code : typeof result.code === 'string' ? result.code : '';
      showModal(`<h2 id="modal-title">${code ? '卡密已创建' : '卡密已登记'}</h2><p class="subtext">${code ? '明文只显示这一次。关闭前请妥善交付给用户。' : '请刷新列表查看记录。'}</p>${code ? `<pre class="new-card-code">${e(code)}</pre>` : ''}<div class="modal-actions"><button class="button primary" data-action="close-modal">已妥善保存</button></div>`); void loadAdmin();
    } catch (error) { if (session !== generation || handleError(error)) return; closeModal(); state.notice = '卡密创建状态待确认，请先刷新列表，避免重复创建。'; render(); }
  });
}
async function adminAction(action: () => Promise<void>): Promise<void> {
  const session = generation; closeModal();
  try { await action(); if (session === generation) await loadAdmin(); }
  catch (error) { if (session !== generation || handleError(error)) return; state.notice = '管理操作暂未完成，请刷新数据确认状态。'; render(); }
}
document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && state.modal) closeModal();
  if (event.key === 'Tab' && state.modal) {
    const items = [...document.querySelectorAll<HTMLElement>('.modal button, .modal input, .modal a[href]')].filter(item => !(item as HTMLButtonElement).disabled);
    const first = items[0]; const last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  }
});
async function restore(): Promise<void> {
  const session = generation;
  try { const user = await api.me(); if (session !== generation) return; state.user = user; render(); void loadJobs(); void loadCourses(); }
  catch { /* Keep the existing login form intact, including fields currently being entered. */ }
}
render(); void api.authConfig().then(config => { state.cardRequired = config.access_card_required; if (!state.user && !state.signingIn) { const field = document.querySelector<HTMLInputElement>('#card-code'); if (field) field.required = state.cardRequired; const label = document.querySelector<HTMLElement>('[data-card-requirement]'); if (label) label.textContent = state.cardRequired ? '必填' : '选填'; const help = document.querySelector<HTMLElement>('[data-card-help]'); if (help) help.textContent = state.cardRequired ? '使用有效卡密开通服务，具体规则请联系服务人员。' : '卡密规则尚未开放，当前可暂不填写。'; } }).catch(() => { /* Login remains available if the optional settings request has not completed. */ }); void restore();

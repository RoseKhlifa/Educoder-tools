import { ApiRequestError, BrowserApi } from './api.js';
import { partitionAdminJobs, renderAdmin, renderAdminJobDetail, type AdminPage, type AdminState } from './admin.js';
import { currentServiceJobs, jobProgress, jobTitle, pendingJob, renderJobs, renderServiceDetail, serviceJob, serviceLabels, serviceStatus } from './jobs.js';
import { count, escapeHtml as e, icon } from './ui.js';
import type { AnswerJob, Course, Homework, User } from './types.js';

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
const isAdmin = (): boolean => state.user?.role === 'admin' || Boolean(state.user?.permissions?.includes('admin'));
const accessible = (): boolean => state.user?.service_access !== false && state.user?.status !== 'disabled';
const activeJobs = (): AnswerJob[] => state.jobs.filter(job => serviceJob(job) && pendingJob(job));
function stopPoll(): void { if (pollTimer !== undefined) clearTimeout(pollTimer); pollTimer = undefined; }
function resetSession(message = ''): void {
  generation++; jobsRevision++; coursesRevision++; adminRevision++; modalRevision++; stopPoll();
  Object.assign(state, { user: null, page: 'courses', courses: [], jobs: [], selected: [], loading: false, signingIn: false, creating: false, notice: '', loginError: message, modal: '', modalAction: null, search: '', progressHistoryOpen: false, admin: { overview: {}, users: [], jobs: [], audit: [], cards: [], loading: false, error: false } });
  render();
}
function handleError(error: unknown): boolean {
  if (error instanceof ApiRequestError && error.isSessionExpired) { resetSession('登录已过期，请重新登录。你的任务进度会保留。'); return true; }
  return false;
}
function brand(): string { return '<div class="brand"><span class="brand-mark" aria-hidden="true"><i></i><i></i></span><div><strong>头歌妈妈</strong><small>touge.mom</small></div></div>'; }
function avatar(): string {
  return state.user?.avatarUrl ? `<img class="avatar" src="${e(state.user.avatarUrl)}" alt="个人头像" data-avatar>` : `<span class="avatar">${e(state.user?.name.slice(0, 1) || '同')}</span>`;
}
function renderLogin(): string {
  return `<main class="login"><section class="login-visual">${brand()}<div class="login-copy"><span class="welcome-label">让学习的安排，轻松一点。</span><h1>选好课程，<br>剩下交给我们。</h1><p>登录你的头歌账号，选择需要完成的课程。<br>每一步进度，回来就能看见。</p><div class="login-illustration" aria-hidden="true"><div class="illustration-line"><span class="illustration-icon">✓</span><div><b>选择课程</b><i></i></div><em>你来决定</em></div><div class="illustration-connector"></div><div class="illustration-line"><span class="illustration-icon live">◌</span><div><b>我们接手</b><i></i></div><em>安心等待</em></div><div class="illustration-connector"></div><div class="illustration-line"><span class="illustration-icon">✓</span><div><b>查看完成进度</b><i></i></div><em>随时可查</em></div></div></div><p class="login-foot">你的头歌账号，就是这里的账号。</p></section><section class="login-form-area"><form id="login-form"><span class="badge waiting">无需额外注册</span><h2>欢迎回来</h2><p class="subtext">用头歌账号登录，开始安排你的课程。</p><label for="login">头歌账号</label><input id="login" name="login" autocomplete="username" placeholder="手机号 / 邮箱 / 学号" required maxlength="120"><label for="password">头歌密码</label><input id="password" name="password" type="password" autocomplete="current-password" placeholder="输入头歌密码" required maxlength="512"><label for="card-code">卡密 <span data-card-requirement>${state.cardRequired ? '必填' : '选填'}</span></label><input id="card-code" name="access_code" type="password" autocomplete="off" placeholder="${state.cardRequired ? '输入你的服务卡密' : '如有卡密，请在这里填写'}" maxlength="256" ${state.cardRequired ? 'required' : ''}><p class="field-help" data-card-help>${state.cardRequired ? '使用有效卡密开通服务，具体规则请联系服务人员。' : '卡密规则尚未开放，当前可暂不填写。'}</p>${state.loginError ? `<div class="form-feedback" role="alert">${e(state.loginError)}</div>` : ''}<button class="button primary full" type="submit" ${state.signingIn ? 'disabled' : ''}>${state.signingIn ? '正在登录…' : '登录并进入'}</button><p class="login-help">登录仅用于读取你自己的课堂和完成服务。密码不在本站保存。</p></form></section></main>`;
}
function navItem(page: Page, label: string, name: string): string { return `<button class="nav-item ${state.page === page ? 'active' : ''}" data-nav="${page}" ${state.page === page ? 'aria-current="page"' : ''}>${icon(name)}<span>${label}</span></button>`; }
function renderShell(): string {
  const admin = state.page.startsWith('admin-') && isAdmin();
  return `<div class="app-shell"><aside class="sidebar">${brand()}<div class="nav-label">${admin ? '服务管理' : '我的学习'}</div><nav aria-label="${admin ? '管理员导航' : '主要导航'}">${admin ? `${navItem('admin-overview', '服务概览', 'overview')}${navItem('admin-users', '用户管理', 'users')}${navItem('admin-operations', '任务运营', 'progress')}${navItem('admin-cards', '服务卡密', 'audit')}${navItem('admin-audit', '操作审计', 'audit')}` : `${navItem('courses', '我的课程', 'courses')}${navItem('progress', '完成进度', 'progress')}`}</nav><div class="sidebar-bottom">${isAdmin() ? `<button class="nav-item" data-nav="${admin ? 'courses' : 'admin-overview'}">${icon(admin ? 'courses' : 'overview')}<span>${admin ? '返回用户端' : '管理后台'}</span></button>` : ''}<div class="sidebar-note">${admin ? '异常由服务人员处理，<br>用户端只展示完成进度。' : '你负责安心，<br>我们负责把事情做好。'}</div><div class="profile">${avatar()}<div><strong>${e(state.user?.name || '同学')}</strong><small>${admin ? '管理员' : '头歌账号已连接'}</small></div><button class="icon-button" data-action="logout" aria-label="退出登录" title="退出登录">${icon('logout')}</button></div></div></aside><main class="main"><header class="topbar"><div class="mobile-brand">${brand()}</div><span class="breadcrumb">${admin ? '管理后台' : '个人中心'}<span>/</span>${admin ? ({ 'admin-overview': '服务概览', 'admin-users': '用户管理', 'admin-operations': '任务运营', 'admin-audit': '操作审计', 'admin-cards': '服务卡密' } as Record<string, string>)[state.page] : state.page === 'courses' ? '我的课程' : '完成进度'}</span><div class="topbar-right"><span class="connection"><i></i> ${admin ? '管理会话' : '头歌已连接'}</span><button class="icon-button mobile-logout" data-action="logout" aria-label="退出登录">${icon('logout')}</button></div></header><div class="mobile-nav">${admin ? `${navItem('admin-overview', '概览', 'overview')}${navItem('admin-users', '用户', 'users')}${navItem('admin-operations', '任务', 'progress')}${navItem('admin-cards', '卡密', 'audit')}${navItem('admin-audit', '审计', 'audit')}${navItem('courses', '用户端', 'courses')}` : `${navItem('courses', '我的课程', 'courses')}${navItem('progress', '完成进度', 'progress')}${isAdmin() ? navItem('admin-overview', '管理后台', 'overview') : ''}`}</div><div class="workspace">${state.notice ? `<div class="service-notice" role="status">${e(state.notice)}</div>` : ''}${admin ? renderAdmin(state.page.replace('admin-', '') as AdminPage, state.admin) : state.page === 'courses' ? renderCourses() : renderProgress()}</div><footer class="page-footer">头歌妈妈 · 把安排交给我们</footer></main></div>`;
}
function courseService(course: string): AnswerJob | undefined {
  const matching = currentServiceJobs(state.jobs).filter(job => job.request.scope === 'all' || job.request.course === course);
  return matching.find(pendingJob) ?? matching[0];
}
function coursePending(course: string): boolean {
  const latest = courseService(course);
  return Boolean(latest && (pendingJob(latest) || serviceStatus(latest) === 'support'));
}
function allServicePending(): boolean {
  return activeJobs().length > 0 || currentServiceJobs(state.jobs).some(job => job.request.scope === 'all' && serviceStatus(job) === 'support');
}
function renderCourses(): string {
  const visible = state.courses.filter(course => !state.search || course.name.toLowerCase().includes(state.search.toLowerCase()));
  const active = activeJobs();
  const current = currentServiceJobs(state.jobs);
  const latest = active[0] ?? current.find(job => serviceStatus(job) === 'support') ?? current.find(job => serviceStatus(job) === 'completed');
  const completed = current.filter(job => serviceStatus(job) === 'completed').length;
  return `<div class="page-heading"><div><h1>今天，也轻松一点。</h1><p>${e(state.user?.name || '同学')}，选好课程，我们来完成。</p></div><button class="button" data-action="refresh" ${state.loading ? 'disabled' : ''}>${icon('refresh')}刷新课程</button></div>${!accessible() ? '<div class="service-notice">你的服务暂未开通，请联系服务人员。已有完成进度仍可查看。</div>' : ''}<section class="service-banner"><div><span class="banner-label">把未完成的，交给我们。</span><h2>一次安排，不必守着页面。</h2><p>完成所有课程的未完成必做作业，已完成和选做内容会自动跳过。</p></div><button class="button primary" data-action="start-all" ${state.loading || state.creating || !state.courses.length || !accessible() || allServicePending() ? 'disabled' : ''}>一键完成全部未完成作业 ${icon('arrow')}</button></section><div class="content-grid"><section><div class="section-heading"><div><h2>我的课程 <span class="count">${state.courses.length}</span></h2><p class="muted">选择课程，安排其中尚未完成的作业。</p></div><label class="search-box"><span class="sr-only">搜索课程</span><input data-search placeholder="搜索课程" value="${e(state.search)}"></label></div>${state.loading ? '<div class="loading" role="status"><span class="spinner"></span>正在同步你的课程…</div>' : visible.length ? `<div class="courses">${visible.map((course, index) => `<article class="course ${state.selected.includes(course.identifier) ? 'selected' : ''}"><label class="course-select"><input type="checkbox" data-select-course="${e(course.identifier)}" ${state.selected.includes(course.identifier) ? 'checked' : ''} ${coursePending(course.identifier) || state.creating || !accessible() ? 'disabled' : ''}><span class="sr-only">选择${e(course.name)}</span></label><span class="book book-${index % 3}" aria-hidden="true"><i></i></span><h3>${e(course.name)}</h3><p>${course.homeworkCount === undefined ? '课程作业' : `${course.homeworkCount} 份作业`}</p><div class="course-footer"><span class="badge ${coursePending(course.identifier) ? 'processing' : 'waiting'}">${coursePending(course.identifier) ? serviceStatus(courseService(course.identifier)!) === 'support' ? '服务处理中' : '服务已安排' : '可安排'}</span><button class="text-button" data-course-detail="${e(course.identifier)}">查看作业 ${icon('arrow')}</button></div></article>`).join('')}</div>` : `<div class="panel empty"><h3>${state.search ? '没有找到这门课程' : '还没有课程'}</h3><p>${state.search ? '试试其他课程名称。' : '加入头歌课堂后，刷新这里即可同步。'}</p></div>`}<div class="selection-bar"><label class="select-all"><input type="checkbox" data-action="select-all" ${state.selected.length > 0 && state.selected.length === state.courses.filter(course => !coursePending(course.identifier)).length ? 'checked' : ''} ${state.creating || !accessible() ? 'disabled' : ''}> 全选可安排课程</label><span>已选 ${state.selected.length} 门课程</span><button class="button primary" data-action="start-selected" ${!state.selected.length || state.creating || !accessible() ? 'disabled' : ''}>${state.creating ? '正在安排…' : '完成所选课程'}</button></div></section><aside class="status-rail"><section class="current-service"><div class="rail-label"><i></i> 我的完成进度</div><h2>${latest ? serviceLabels[serviceStatus(latest)] : completed ? '安排已完成' : '一切准备就绪'}</h2><p>${latest ? e(jobTitle(latest, state.courses)) : '选好课程后，我们会在这里跟进。'}</p>${latest ? `<div class="large-progress"><strong>${jobProgress(latest).completed}</strong><span>/ ${jobProgress(latest).total || '—'} ${jobProgress(latest).unit}</span></div><div class="rail-track"><span style="width:${jobProgress(latest).percent}%"></span></div>` : '<div class="ready-mark" aria-hidden="true">✓</div>'}<p class="rail-note">${latest && serviceStatus(latest) === 'support' ? '放心，服务人员正在处理，无需重复提交。' : '关闭页面也没关系，进度会为你保留。'}</p><button data-nav="progress">查看全部进度 ${icon('arrow')}</button></section><section class="panel reassurance"><h3>只需要三步</h3><ol><li><b>选择课程</b><p>按需要选择，或一键安排全部。</p></li><li><b>交给我们</b><p>处理过程中无需守候。</p></li><li><b>查看进度</b><p>完成后，结果清楚可见。</p></li></ol></section></aside></div>`;
}
function renderProgress(): string {
  return `<div class="page-heading"><div><h1>完成进度</h1><p>查看正在处理和已完成的课程。</p></div><button class="button" data-action="jobs-refresh">${icon('refresh')}刷新进度</button></div>${renderJobs(state.jobs, state.courses, state.progressHistoryOpen)}`;
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
function closeModal(): void { state.modal = ''; state.modalAction = null; modalRevision++; render(); previousFocus?.isConnected && previousFocus.focus(); }
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
  showModal(`<h2 id="modal-title">${e(course.name)}</h2><div class="loading"><span class="spinner"></span>正在同步作业…</div>`);
  const revision = modalRevision; const session = generation;
  try {
    const homeworks = await api.homeworks(id);
    if (session !== generation || revision !== modalRevision) return;
    const complete = (homework: Homework): boolean => Boolean(homework.requiredProgressKnown && homework.requiredChallengeCount && (homework.finishedRequiredChallengeCount ?? 0) >= homework.requiredChallengeCount);
    state.modal = `<h2 id="modal-title">${e(course.name)}</h2><p class="subtext">${homeworks.length} 份作业，只安排必做关卡，已完成的会自动跳过。</p><ul class="completion-list">${homeworks.map(homework => `<li><span class="completion-icon ${complete(homework) ? 'done' : ''}">${complete(homework) ? '✓' : '·'}</span><div><strong>${e(homework.name)}</strong><p>${homework.requiredProgressKnown ? `${complete(homework) ? '已完成' : '待完成'} · ${count(homework.finishedRequiredChallengeCount)} / ${count(homework.requiredChallengeCount)} 必做关卡` : '安排时核验必做进度'}</p></div></li>`).join('') || '<li>这门课程暂无需要安排的必做作业。</li>'}</ul><div class="modal-actions"><button class="button" data-action="close-modal">关闭</button></div>`; render();
  } catch (error) {
    if (session !== generation || revision !== modalRevision) return;
    if (handleError(error)) return;
    state.modal = '<h2 id="modal-title">作业正在同步</h2><p class="subtext">服务人员会协助处理，请稍后查看。</p>'; render();
  }
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
      case 'select-all': state.selected = (button as HTMLInputElement).checked ? state.courses.filter(course => !coursePending(course.identifier)).map(course => course.identifier) : []; render(); break;
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

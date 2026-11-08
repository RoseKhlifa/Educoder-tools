import { ApiRequestError, BrowserApi } from './api.js';
import { adminJobResumeAction, adminJobTarget, partitionAdminJobs, renderAdmin, renderAdminJobDetail, type AdminPage, type AdminState } from './admin.js';
import { canCancelJob, canRetryJob, canRecoverJob, recoveryNotice, serviceLabel, type JobActionState, currentServiceJobs, jobProgress, jobTitle, pendingJob, renderJobs, renderServiceDetail, serviceJob, serviceStatus } from './jobs.js';
import { count, escapeHtml as e, icon } from './ui.js';
import { renderUserGuidelines } from './guidelines.js';
import { CourseDetailsCache, type CourseCacheEvent } from './course-cache.js';
import { courseRecordIsHistorical, courseTerminalVersion, historicalCourseRecordIds, summarizeCourseWork, type CourseWorkEvidence } from './course-status.js';
import { renderTickets, renderTicketDetail, type TicketState } from './tickets.js';
import type { AdminJob, AdminJobAction, AnswerJob, Course, Exercise, Homework, User, QuestionBankSummary, DailyServiceSummary, SupportTicketDetail, TicketCategory, TicketStatus } from './types.js';

const root = document.querySelector<HTMLDivElement>('#app');
if (!root) throw new Error('Missing application root');
const assetRevisionMeta = document.querySelector<HTMLMetaElement>('meta[name="app-asset-revision"]');
const appVersion = document.querySelector<HTMLMetaElement>('meta[name="app-version"]')?.content.trim() ?? '';
let assetUpdateAvailable = false;
const api = new BrowserApi('', {
  initialAssetRevision: assetRevisionMeta?.content ?? '',
  onAssetRevisionChange: () => {
    assetUpdateAvailable = true;
    // Keep this independent from the session state. A logout or an expired
    // session must not hide a notice that the loaded page is stale.
    render();
  },
});
type Page = 'courses' | 'progress' | 'support' | 'admin-support' | `admin-${AdminPage}`;
interface TicketScreen extends TicketState { revision: number; detailRevision: number; detailError: string; detailLoading: boolean }
const emptyTickets = (): TicketScreen => ({ items: [], loading: false, error: '', filter: 'all', nextCursor: '', submitting: false, detail: null, revision: 0, detailRevision: 0, detailError: '', detailLoading: false });
let userTickets = emptyTickets(), adminTickets = emptyTickets();
let ticketDialog: { admin: boolean; id: string } | null = null;
const ticketRetryKeys = new Map<string, Map<string, string>>();
const ticketFormDrafts = new Map<string, TicketFormDraft>();
const state = {
  user: null as User | null, page: 'courses' as Page, courses: [] as Course[], jobs: [] as AnswerJob[],
  selected: [] as string[], loading: false, signingIn: false, creating: false, notice: '', loginError: '', search: '',
  modal: '' as string, modalKind: '' as '' | 'guidelines' | 'ticket' | 'job' | 'admin-job' | 'course', modalAction: null as (() => Promise<void> | void) | null,
  progressHistoryOpen: false, questionBank: null as QuestionBankSummary | null, bankLoading: false, bankUnavailable: false, todaySummary: null as DailyServiceSummary | null, todayLoading: false, todayUnavailable: false,
  admin: { overview: {}, users: [], jobs: [], audit: [], loading: false, error: false, jobsView: 'current', jobsKind: 'service' } as AdminState,
};
let generation = 0;
let jobsRevision = 0;
let recoveryNoticeID = '';
let coursesRevision = 0;
let adminRevision = 0;
let modalRevision = 0;
let bankRevision = 0;
let bankLoadedAt = 0;
let todayRevision = 0;
let todayLoadedAt = 0;
let todayLoadedDate = '';
let guidelinesSource = 'login';
let pollTimer: ReturnType<typeof setTimeout> | undefined;
let previousFocus: HTMLElement | null = null;
type DetailTab = 'homework' | 'common' | 'exercise';
interface CourseDialog {
  course: Course; homeworks: Homework[] | null; exercises: Exercise[] | null; tab: DetailTab;
  loading: Record<DetailTab, boolean>; failed: Record<DetailTab, boolean>; updatedAt: Record<DetailTab, number>;
}
let courseDialog: CourseDialog | null = null;
type HomeworkSync = { state: 'loading' | 'ready' | 'error'; hasPreviousData: boolean };
const homeworkSync = new Map<string, HomeworkSync>();
const homeworkJobObservations = new Map<string, ReadonlySet<string>>();
let jobDialog: { id: string; historical: boolean } | null = null;
let adminJobDialog: { id: string; historical: boolean } | null = null;
const jobActions = new Map<string, JobActionState>();
const adminJobActions = new Map<string, AdminJobAction>();
const courseCache = new CourseDetailsCache({
  homeworks: async id => {
    const session = generation;
    const observed = new Set(state.jobs.filter(job => !pendingJob(job)).map(courseTerminalVersion));
    const data = await readCourseData(() => api.homeworks(id));
    if (session === generation) homeworkJobObservations.set(id, observed);
    return data;
  },
  exercises: id => readCourseData(async () => (await api.exercises(id)).items),
}, { onChange: handleCourseCacheChange });
function handleCourseCacheChange(event: CourseCacheEvent): void {
  if (!state.user || !state.courses.some(course => course.identifier === event.identifier)) return;
  if (event.kind === 'homeworks') {
    homeworkSync.set(event.identifier, {
      state: event.state === 'success' ? 'ready' : event.state,
      hasPreviousData: event.hasPreviousData || Boolean(courseCache.peekHomeworks(event.identifier)),
    });
    state.selected = state.selected.filter(id => courseSelectable(id));
  }
  const dialog = courseDialog;
  if (dialog && state.modalKind === 'course' && dialog.course.identifier === event.identifier) {
    const tab: DetailTab = event.kind === 'homeworks' ? (courseDialog?.tab === 'common' ? 'common' : 'homework') : 'exercise';
    if (event.kind === 'homeworks') {
      const cached = courseCache.peekHomeworks(event.identifier);
      if (cached) { dialog.homeworks = cached.data; dialog.updatedAt.homework = cached.updatedAt; }
    } else {
      const cached = courseCache.peekExercises(event.identifier);
      if (cached) { dialog.exercises = cached.data; dialog.updatedAt.exercise = cached.updatedAt; }
    }
    dialog.loading[tab] = event.state === 'loading';
    dialog.failed[tab] = event.state === 'error';
    renderCourseDialog();
  } else if (!state.modal) render();
}

async function readCourseData<T>(read: () => Promise<T>): Promise<T> {
  const session = generation;
  try { return await read(); }
  catch (error) { if (session === generation) handleError(error); throw error; }
}
const isAdmin = (): boolean => state.user?.role === 'admin' || Boolean(state.user?.permissions?.includes('admin'));
const accessible = (): boolean => state.user?.service_access !== false && state.user?.status !== 'disabled';
const activeJobs = (): AnswerJob[] => state.jobs.filter(job => serviceJob(job) && pendingJob(job));
function stopPoll(): void { if (pollTimer !== undefined) clearTimeout(pollTimer); pollTimer = undefined; }
function resetSession(message = ''): void {
  jobActions.clear(); recoveryNoticeID = ''; adminJobActions.clear(); jobDialog = null; adminJobDialog = null; homeworkSync.clear(); homeworkJobObservations.clear();
  userTickets = emptyTickets(); adminTickets = emptyTickets(); ticketDialog = null; ticketRetryKeys.clear(); ticketFormDrafts.clear();
  generation++; jobsRevision++; coursesRevision++; adminRevision++; modalRevision++; bankRevision++; bankLoadedAt = 0; todayRevision++; todayLoadedAt = 0; todayLoadedDate = ''; guidelinesSource = 'login'; stopPoll(); courseDialog = null; courseCache.clear();
  Object.assign(state, { user: null, page: 'courses', courses: [], jobs: [], selected: [], loading: false, signingIn: false, creating: false, notice: '', loginError: message, modal: '', modalKind: '', modalAction: null, search: '', progressHistoryOpen: false, questionBank: null, bankLoading: false, bankUnavailable: false, todaySummary: null, todayLoading: false, todayUnavailable: false, admin: { overview: {}, users: [], jobs: [], audit: [], loading: false, error: false, userSearch: '', userFilter: 'all' } });
  render();
}
function handleError(error: unknown): boolean {
  if (error instanceof ApiRequestError && error.isSessionExpired) { resetSession('登录已过期，请重新登录。你的任务进度会保留。'); return true; }
  return false;
}
function renderAssetUpdateNotice(): string {
  if (!assetUpdateAvailable) return '';
  return '<div class="asset-update-banner" role="status"><span>检测到头歌妈妈已发布新版本，刷新页面后继续使用。</span><button class="button" type="button" data-action="asset-update-refresh">刷新页面</button></div>';
}
function reloadForAssetUpdate(): void {
  if (typeof window !== 'undefined' && typeof window.location?.reload === 'function') window.location.reload();
}
function brandLogo(): string {
  return '<img class="brand-logo" src="/assets/brand-logo.png?v=0.1.50" alt="头歌妈妈" width="40" height="40" decoding="async">';
}

function avatar(): string {
  return state.user?.avatarUrl ? `<img class="avatar" src="${e(state.user.avatarUrl)}" alt="个人头像" data-avatar>` : `<span class="avatar">${e(state.user?.name.slice(0, 1) || '同')}</span>`;
}
function questionBankCredit(): string {
  return '<span class="bank-credit">感谢 <a href="https://github.com/ppxxforever/fuck-educoder" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">ppxxforever/fuck-educoder</a> 提供题库支持。</span>';
}
function renderLogin(): string {
  return `<main id="view-login" class="login login-page"><aside class="login-brand" aria-label="品牌介绍"><div class="brand-mark">${brandLogo()}<div class="mark-text"><strong>头歌妈妈</strong><span>touge.mom</span></div></div><div class="login-brand-copy"><span class="login-kicker">头歌平台服务</span><h1>选好课程，<br>剩下交给我们。</h1><p class="lede">登录你的头歌账号，选择需要完成的课程。每一步进度，回来就能看见。</p><div class="brand-points"><ul><li><span class="dot">1</span><span>选择课程，或一键处理全部未完成作业</span></li><li><span class="dot">2</span><span>后台持续处理，关闭页面也不会中断</span></li><li><span class="dot">3</span><span>随时查看课程与作业完成进度</span></li></ul></div></div><div class="login-brand-footer"><p>你的头歌账号，就是这里的账号。</p><p class="login-bank-credit">${questionBankCredit()}</p></div></aside><section class="login-panel"><div class="login-panel-inner"><div class="login-kicker">TOUGE.MOM</div><h2>登录头歌妈妈</h2><p class="sub">使用头歌账号进入，无需额外注册。</p><form class="login-card" id="login-form"><div class="field"><label for="login">头歌账号</label><input id="login" name="login" autocomplete="username" placeholder="手机号 / 邮箱 / 学号" required maxlength="120"></div><div class="field"><label for="password">头歌密码</label><input id="password" name="password" type="password" autocomplete="current-password" placeholder="输入头歌密码" required maxlength="512"></div>${state.loginError ? `<div class="form-feedback" role="alert">${e(state.loginError)}</div>` : ''}<button class="btn-primary button primary full" type="submit" ${state.signingIn ? 'disabled' : ''}>${state.signingIn ? '正在登录…' : '登录并进入'}</button></form><p class="login-guidelines">登录前请阅读<button class="guidelines-link" type="button" data-action="user-guidelines" data-guidelines-source="login">《用户准则》</button>。<span>平台会收集做题数据，用于题目与答案复用。</span></p></div></section></main>`;
}
function navItem(page: Page, label: string, name: string): string { return `<button class="nav-link nav-item ${state.page === page ? 'active' : ''}" data-nav="${page}" ${state.page === page ? 'aria-current="page"' : ''}>${icon(name)}<span>${label}</span></button>`; }
function renderQuestionBank(placement: 'sidebar' | 'mobile'): string {
  const summary = state.questionBank;
  return `<section class="side-card question-bank-card" aria-label="题库概况"><div class="bank-card-heading"><h3>题库概况</h3><button class="bank-refresh" type="button" data-action="bank-refresh" aria-label="刷新题库概况" ${state.bankLoading ? 'disabled' : ''}>${icon('refresh')}</button></div>${summary ? `<p class="bank-total"><strong>${summary.total_questions.toLocaleString('zh-CN')}</strong><span>道题目</span></p><dl class="bank-counts"><div><dt>客观题</dt><dd>${summary.objective_questions.toLocaleString('zh-CN')}</dd></div><div><dt>编程题</dt><dd>${summary.programming_problems.toLocaleString('zh-CN')}</dd></div><div><dt>绘图题</dt><dd>${summary.graphic_problems.toLocaleString('zh-CN')}</dd></div></dl>` : `<p class="bank-empty" role="status">${state.bankUnavailable ? '题库概况暂未同步' : '正在同步题库概况…'}</p>`}<p class="bank-contribution" title="按贡献记录去重统计，非全站首次新增题目数">${summary?.contributed_questions === undefined ? '正在核对您的题库贡献，感谢您的支持。' : `您已为题库贡献 <strong>${summary.contributed_questions.toLocaleString('zh-CN')}</strong> 道题，感谢您的付出。`}</p><p class="bank-source-credit">${questionBankCredit()}</p><div class="bank-service-notice"><p>本站为独立服务；结果以头歌为准，不承诺成绩或完成时限。</p><button class="guidelines-link" type="button" data-action="user-guidelines" data-guidelines-source="${placement}">用户准则与数据说明 ${icon('arrow')}</button></div></section>`;
}
function serviceDate(): string {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date());
  const field = (type: string): string => parts.find(part => part.type === type)?.value || '';
  return `${field('year')}-${field('month')}-${field('day')}`;
}
function renderTodaySummary(): string {
  const date = serviceDate(); const summary = state.todaySummary?.date === date ? state.todaySummary : null;
  return `<section class="side-card today-service-card" aria-label="累计服务"><div class="today-heading"><h3>累计服务</h3><span>自建站以来</span></div>${summary ? `<p class="today-completion">已为 <strong>${summary.total_served_students.toLocaleString('zh-CN')}</strong> 位同学<br>完成 <strong>${summary.total_completed_questions.toLocaleString('zh-CN')}</strong> 道题</p><p class="today-daily">其中今天 <b>${summary.completed_students.toLocaleString('zh-CN')}</b> 位同学 · <b>${summary.completed_questions.toLocaleString('zh-CN')}</b> 道题</p>` : `<p class="today-empty" role="status">${state.todayUnavailable || state.todaySummary ? '服务统计暂未同步' : '正在同步服务统计…'}</p>`}<p class="today-note">北京时间 · 按可核验记录累计<br>同一同学的同一题不重复计数</p></section>`;
}
function renderCourseMeta(course: Course): string {
  return `<span class="course-meta"><span class="course-teacher" title="授课教师">${icon('teacher')}<span>${e(course.teacherName || '教师待同步')}</span></span><span class="course-meta-separator" aria-hidden="true">—</span><span class="course-students" title="学生人数">${icon('users')}<span>${course.studentCount === undefined ? '人数待同步' : `${course.studentCount.toLocaleString('zh-CN')} 人`}</span></span></span>`;
}
function courseWorkEvidence(): Map<string, CourseWorkEvidence> {
  const evidence = new Map<string, CourseWorkEvidence>();
  for (const course of state.courses) {
    const cached = courseCache.peekHomeworks(course.identifier);
    if (cached) evidence.set(course.identifier, { ...cached, sync: homeworkSync.get(course.identifier)?.state ?? 'ready', observedTerminalVersions: homeworkJobObservations.get(course.identifier) ?? new Set() });
  }
  return evidence;
}
function historicalCourseRecords(): Set<string> {
  const ids = historicalCourseRecordIds(state.jobs, courseWorkEvidence());
  for (const id of jobActions.keys()) ids.delete(id);
  return ids;
}
function consumerCurrentJobs(): AnswerJob[] { return currentServiceJobs(state.jobs, historicalCourseRecords()); }
function renderShell(): string {
  const admin = state.page.startsWith('admin-') && isAdmin();
  const consumerLinks = `${navItem('courses', '我的课程', 'courses')}${navItem('progress', '完成进度', 'progress')}${navItem('support', '问题反馈', 'support')}${isAdmin() ? navItem('admin-overview', '管理后台', 'overview') : ''}`;
  const adminLinks = `${navItem('admin-overview', '服务概览', 'overview')}${navItem('admin-users', '用户管理', 'users')}${navItem('admin-operations', '任务运营', 'progress')}${navItem('admin-support', '反馈工单', 'support')}${navItem('admin-audit', '操作审计', 'audit')}`;
  const adminSideLinks = adminLinks.replaceAll('class="nav-link nav-item', 'class="side-link nav-item');
  const links = admin ? adminLinks : consumerLinks;
  const title = admin ? ({ 'admin-overview': '服务概览', 'admin-users': '用户管理', 'admin-operations': '任务运营', 'admin-audit': '操作审计', 'admin-support': '反馈工单' } as Record<string, string>)[state.page] : state.page === 'courses' ? '我的课程' : '完成进度';
  const selectedCount = state.selected.length;
  const current = consumerCurrentJobs();
  const active = current.filter(pendingJob).length;
  const followUp = current.filter(job => serviceStatus(job) === 'support').length;
  const completed = state.jobs.filter(job => serviceJob(job) && serviceStatus(job) === 'completed').length;
  return `<div id="view-app" class="app-shell ${admin ? 'admin-app' : 'consumer-app'}"><header class="global-nav topbar ${admin ? 'admin-topbar' : ''}"><div class="top-left"><button class="nav-brand" type="button" data-nav="${admin ? 'admin-overview' : 'courses'}" aria-label="头歌妈妈首页">${brandLogo()}<span class="name">头歌妈妈</span><span class="domain">touge.mom</span></button>${admin ? '<span class="admin-chip">服务管理</span>' : ''}</div><nav class="nav-links" aria-label="${admin ? '管理员导航' : '主导航'}">${links}</nav><div class="nav-user">${avatar()}<span class="user-name">${e(state.user?.name || '同学')}</span><button class="icon-button" data-action="logout" aria-label="退出登录" title="退出登录">${icon('logout')}</button></div></header><div class="mobile-nav">${links}${isAdmin() ? navItem(admin ? 'courses' : 'admin-overview', admin ? '用户端' : '管理后台', admin ? 'courses' : 'overview') : ''}</div><div class="${admin ? 'shell admin-shell' : 'workspace consumer-workspace'}">${admin ? `<aside class="side admin-side" aria-label="管理员导航"><div class="side-label">服务管理</div><nav class="side-nav">${adminSideLinks}</nav><div class="side-foot"><p class="t">服务工作台</p><p>异常与执行记录只在管理端查看，用户端只展示完成进度。</p><button class="btn ghost text-button" data-nav="courses">${icon('courses')}返回用户端</button></div></aside>` : `<aside class="side consumer-side" aria-label="侧栏概况"><div class="side-card"><h3>当前概况</h3><div class="stat-row"><span class="l">我的课程</span><span class="n">${state.courses.length}</span></div><div class="stat-row"><span class="l">已选中</span><span class="n">${selectedCount}</span></div><div class="stat-row"><span class="l">处理中</span><span class="n">${active}</span></div><div class="stat-row"><span class="l">待跟进</span><span class="n">${followUp}</span></div><div class="stat-row"><span class="l">已完成任务</span><span class="n">${completed}</span></div></div>${renderTodaySummary()}<div class="consumer-side-bottom">${renderQuestionBank('sidebar')}</div></aside>`}<main class="main ${admin ? 'admin-main' : 'consumer-main'}">${state.notice ? `<div class="service-notice" role="status">${e(state.notice)}</div>` : ''}${state.page === 'admin-support' ? renderTickets(adminTickets, true) : admin ? renderAdmin(state.page.replace('admin-', '') as AdminPage, state.admin, adminJobActions) : state.page === 'support' ? renderTickets(userTickets, false) : state.page === 'courses' ? renderCourses() : renderProgress()}${admin ? '' : `<div class="bank-mobile"><div class="mobile-service-summary">${renderTodaySummary()}</div>${renderQuestionBank('mobile')}</div>`}<footer class="page-footer">头歌妈妈 · 把任务交给我们${appVersion ? ` · <span class="app-version">v${e(appVersion)}</span>` : ''}</footer></main></div></div>`;
}
function matchingCourseJobs(course: string): AnswerJob[] {
  return consumerCurrentJobs().filter(job => job.request.scope !== 'exercise' && (job.request.scope === 'all' || job.request.course === course));
}
function blocksCourseScheduling(job: AnswerJob): boolean {
  if (!jobActions.has(job.id) && courseRecordIsHistorical(job, courseWorkEvidence(), true)) return false;
  return jobActions.has(job.id) || pendingJob(job) || serviceStatus(job) === 'support' && !canRetryJob(job);
}
interface HomeworkSummary {
  ready: boolean;
  count: number;
  runnableCount: number;
  commonPending: number;
  commonSubmitted: number;
  commonTotal: number;
  sync: 'loading' | 'ready' | 'error';
  stale: boolean;
  hasRequired: boolean;
}
function homeworkSummary(course: Course): HomeworkSummary {
  const cached = courseCache.peekHomeworks(course.identifier);
  const sync = homeworkSync.get(course.identifier);
  if (!cached) return { ready: false, count: 0, runnableCount: 0, commonPending: 0, commonSubmitted: 0, commonTotal: 0, sync: sync?.state ?? 'loading', stale: false, hasRequired: false };
  const summary = summarizeCourseWork(cached.data);
  return { ready: true, count: summary.pending, runnableCount: summary.shixunPending, commonPending: summary.commonPending, commonSubmitted: summary.commonSubmitted, commonTotal: summary.commonTotal, hasRequired: summary.hasRequired, sync: sync?.state ?? 'ready', stale: sync?.state === 'error' || cached.stale };
}
function courseSelectable(course: string): boolean {
  const item = state.courses.find(value => value.identifier === course);
  if (!item || coursePending(course)) return false;
  const summary = homeworkSummary(item);
  // Common homework is display-only until the server exposes a supported
  // mutation. It must keep the course visibly pending without creating a job.
  return !summary.ready || summary.stale || summary.runnableCount > 0;
}
function coursePending(course: string): boolean {
  return currentServiceJobs(state.jobs).filter(job => job.request.scope !== 'exercise' && (job.request.scope === 'all' || job.request.course === course)).some(blocksCourseScheduling);
}
function courseDisplayStatus(course: Course, summary: HomeworkSummary): { label: string; tone: string } {
  const matching = matchingCourseJobs(course.identifier);
  const active = matching.find(pendingJob);
  if (active) return { label: serviceLabel(active), tone: serviceStatus(active) };
  if (matching.some(job => jobActions.has(job.id))) return { label: '正在更新任务状态', tone: 'waiting' };
  if (summary.ready && !summary.stale && summary.sync === 'ready' && summary.commonPending > 0 && summary.runnableCount === 0) return { label: '图文作业待处理', tone: 'waiting' };
  if (summary.ready && !summary.stale && summary.sync === 'ready' && summary.count === 0) return { label: summary.hasRequired ? '已完成' : '暂无待办', tone: 'completed' };
  if (summary.sync === 'loading') return { label: summary.ready ? '正在更新' : '同步中', tone: 'waiting' };
  if (summary.ready && summary.stale) return { label: summary.sync === 'error' ? '更新未完成' : '待更新', tone: 'waiting' };
  if (summary.sync === 'error' && !summary.ready) return { label: '未同步', tone: 'waiting' };
  if (matching.some(job => serviceStatus(job) === 'support')) return { label: '待跟进', tone: 'support' };
  return { label: summary.ready ? '可处理' : '同步中', tone: 'waiting' };
}
function allServicePending(): boolean {
  return currentServiceJobs(state.jobs).some(job => job.request.scope !== 'exercise' && blocksCourseScheduling(job));
}
function prefetchCourseDetails(courses: Course[], force = false): void {
  const identifiers = courses.map(course => course.identifier).filter(Boolean);
  if (!force) {
    courseCache.prefetch(identifiers);
    return;
  }
  for (const identifier of new Set(identifiers)) {
    void courseCache.getHomeworks(identifier, true).catch(() => {});
    void courseCache.getExercises(identifier, true).catch(() => {});
  }
}
function revalidateVisibleCourseWork(): void {
  if (!state.user || document.visibilityState === 'hidden' || !['courses', 'progress'].includes(state.page)) return;
  courseCache.revalidateHomeworks(state.courses.map(course => course.identifier));
}

function renderCourses(): string {
  const visible = state.courses.filter(course => !state.search || course.name.toLowerCase().includes(state.search.toLowerCase()));
  const active = activeJobs();
  const recent = state.jobs.filter(serviceJob).sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
  const latest = active[0] ?? recent[0];
  const latestHistorical = Boolean(latest && historicalCourseRecords().has(latest.id));
  const completed = state.jobs.filter(job => serviceJob(job) && serviceStatus(job) === 'completed').length;
  const examSubmitted = latest?.request.scope === 'exercise' && ['submitted', 'awaiting_review', 'graded'].includes(serviceStatus(latest));
  const selectable = state.courses.filter(course => courseSelectable(course.identifier));
  const selectedAll = selectable.length > 0 && selectable.every(course => state.selected.includes(course.identifier));
  const actionableCourses = state.courses.filter(course => courseSelectable(course.identifier));
  const latestProgress = latest ? jobProgress(latest) : null;
  return `<div class="page-head page-heading"><div><span class="eyebrow">我的学习</span><h1>今天，也轻松一点。</h1><p>${e(state.user?.name || '同学')}，选好课程，我们来完成。</p></div><div class="head-actions"><button class="btn button" data-action="refresh" ${state.loading ? 'disabled' : ''}>${icon('refresh')}刷新课程</button><button class="btn accent button primary" data-action="start-all" ${state.loading || state.creating || !state.courses.length || !actionableCourses.length || !accessible() || allServicePending() ? 'disabled' : ''}>一键完成全部未完成作业 ${icon('arrow')}</button></div></div>${!accessible() ? '<div class="service-notice">你的服务暂未开通，请联系服务人员。已有完成进度仍可查看。</div>' : ''}<section class="strip" aria-label="课程摘要"><div class="strip-item"><div class="k">我的课程</div><div class="v">${state.courses.length}</div><div class="h">头歌平台同步</div></div><div class="strip-item"><div class="k">正在处理</div><div class="v">${active.length}</div><div class="h">仅统计排队与执行中的任务</div></div><div class="strip-item"><div class="k">已选中</div><div class="v">${selectedCount(state.selected)}</div><div class="h">选中后开始处理</div></div><div class="strip-item"><div class="k">${active.length ? '当前任务进度' : '最近任务进度'}</div><div class="v">${latestProgress?.percent ?? (completed ? 100 : 0)}%</div><div class="h">${latestHistorical ? '历史执行记录' : '当前服务记录'}</div></div></section><div class="content-grid"><section class="panel-box course-panel"><div class="panel-toolbar"><div><h2>我的课程 <span class="count">${state.courses.length}</span></h2><p class="muted">选择课程，处理其中尚未完成的必做作业。</p></div><div class="toolbar-row"><label class="search-input"><span class="sr-only">搜索课程</span><input data-search placeholder="搜索课程" value="${e(state.search)}"></label><button class="btn button" data-action="select-all" ${state.creating || !accessible() ? 'disabled' : ''}>${selectedAll ? '清空选择' : '全选可处理'}</button></div></div>${state.loading ? '<div class="loading" role="status"><span class="spinner"></span>正在同步你的课程…</div>' : visible.length ? `<div class="course-table-wrap"><table class="course-table" aria-label="我的课程列表"><thead><tr><th class="select-col">选</th><th>课程</th><th>未完成作业</th><th>图文作业</th><th>状态</th><th></th></tr></thead><tbody>${visible.map((course, index) => { const summary = homeworkSummary(course); const displayStatus = courseDisplayStatus(course, summary); const selectableCourse = courseSelectable(course.identifier); const selected = state.selected.includes(course.identifier); const countLabel = summary.ready ? String(summary.runnableCount) : summary.sync === 'error' ? '未同步' : '同步中…'; const commonLabel = summary.ready ? String(summary.commonPending) : summary.sync === 'error' ? '未同步' : '同步中…'; const countTitle = summary.stale ? (summary.sync === 'error' ? '更新未完成，显示上次同步结果' : '显示上次同步结果，正在核对') : summary.ready ? summary.runnableCount === 0 ? '已核验没有未完成的可自动处理实验' : '可自动处理的未完成实验' : summary.sync === 'error' ? '作业列表同步失败，请刷新重试' : '正在同步作业列表'; const commonTitle = summary.ready ? summary.commonPending === 0 ? '图文作业均已提交或无需提交' : '图文作业待处理；当前不支持自动提交' : countTitle; return `<tr class="course-row ${selected ? 'selected' : ''}" data-course-row="${e(course.identifier)}"><td class="select-col"><label class="course-select"><input type="checkbox" data-select-course="${e(course.identifier)}" ${selected ? 'checked' : ''} ${!selectableCourse || state.creating || !accessible() ? 'disabled' : ''}><span class="sr-only">选择${e(course.name)}</span></label></td><td><div class="course-name"><span class="course-index">${String(index + 1).padStart(2, '0')}</span><div><strong>${e(course.name)}</strong>${renderCourseMeta(course)}</div></div></td><td><span class="table-number homework-count${summary.stale ? ' stale' : ''}" title="${e(countTitle)}">${e(countLabel)}</span></td><td><span class="table-number common-count${summary.stale ? ' stale' : ''}" title="${e(commonTitle)}">${e(commonLabel)}</span></td><td><span class="badge ${displayStatus.tone}" title="${e(commonTitle)}">${displayStatus.label}</span></td><td><button class="text-button" data-course-detail="${e(course.identifier)}">查看详情 ${icon('arrow')}</button></td></tr>`; }).join('')}</tbody></table></div>` : `<div class="panel empty"><h3>${state.search ? '没有找到这门课程' : '还没有课程'}</h3><p>${state.search ? '试试其他课程名称。' : '加入头歌课堂后，刷新这里即可同步。'}</p></div>`}<div class="selection-bar"><span class="selection-summary">已选 ${selectedCount(state.selected)} 门课程</span><button class="button primary" data-action="start-selected" ${!state.selected.length || state.creating || !accessible() ? 'disabled' : ''}>${state.creating ? '正在创建任务…' : '完成所选课程'}</button></div></section><aside class="status-rail side-card"><section class="current-service"><div class="rail-label"><i></i> 我的完成进度</div><h2>${latestHistorical ? '历史执行记录' : latest ? serviceLabel(latest) : completed ? '任务已完成' : '一切准备就绪'}</h2><p>${latest ? e(jobTitle(latest, state.courses)) : '选好课程后，我们会在这里跟进。'}</p>${latest && examSubmitted ? `<p class="rail-exam-status">已交卷${jobProgress(latest).total ? ` · 共 ${jobProgress(latest).total} 道题` : ''}</p>` : latest ? `<div class="large-progress"><strong>${jobProgress(latest).completed}</strong><span>/ ${jobProgress(latest).total || '—'} ${jobProgress(latest).unit}</span></div><div class="rail-track"><span style="width:${jobProgress(latest).percent}%"></span></div>` : '<div class="ready-mark" aria-hidden="true">✓</div>'}<p class="rail-note">${latestHistorical ? '当前课程已无对应待办，这里保留当次执行进度。' : latest && canRetryJob(latest) ? '本次任务已停止处理，可在完成进度中重新执行。' : latest && serviceStatus(latest) === 'support' ? '本次处理已暂停，已完成进度保留，剩余内容待跟进。课程状态以最新同步结果为准。' : '关闭页面也没关系，进度会为你保留。'}</p><button class="text-button" data-nav="progress">查看全部进度 ${icon('arrow')}</button></section><section class="panel reassurance"><h3>只需要三步</h3><ol><li><b>选择课程</b><p>按需要选择，或一键处理全部。</p></li><li><b>交给我们</b><p>处理过程中无需守候。</p></li><li><b>查看进度</b><p>完成后，结果清楚可见。</p></li></ol></section></aside></div>`;
}
function selectedCount(values: string[]): number { return values.length; }
function renderProgress(): string {
  return `<div class="page-head page-heading"><div><span class="eyebrow">服务记录</span><h1>完成进度</h1><p>查看处理中、待跟进和已完成的任务。</p></div><div class="head-actions"><button class="btn button" data-action="jobs-refresh">${icon('refresh')}刷新进度</button></div></div>${renderJobs(state.jobs, state.courses, state.progressHistoryOpen, jobActions, historicalCourseRecords())}`;
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
    default: return exercise.available && exercise.actionRequired !== true ? '确认后由服务人员完成整场考试。' : '当前不可处理，请在头歌查看处理入口。';
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
function commonHomeworkLink(course: Course, homework: Homework): string {
  const path = `/classrooms/${encodeURIComponent(course.identifier)}/common_homework/${encodeURIComponent(String(homework.id))}/detail`;
  let url = `https://www.educoder.net${path}`;
  try {
    const source = new URL(homework.detailUrl || url);
    if (source.protocol === 'https:' && ['www.educoder.net', 'educoder.net'].includes(source.hostname) && !source.username && !source.password && !source.port
      && (source.pathname === path || source.pathname.startsWith(`${path}/`))) url = `${source.origin}${source.pathname}${source.search}`;
  } catch { /* Keep the verified classroom common-homework route. */ }
  return `<a class="text-button" href="${e(url)}" target="_blank" rel="noopener noreferrer">在头歌查看详情 ${icon('arrow')}</a>`;
}
function commonHomeworkStatus(homework: Homework): { label: string; tone: string } {
  if (homework.submissionStatus === 'submitted') return { label: '已提交 · 待批阅', tone: 'processing' };
  if (homework.submissionStatus === 'not_submitted') return { label: '待提交', tone: 'waiting' };
  return { label: '状态待确认', tone: 'waiting' };
}
function renderCourseDetail(dialog: CourseDialog): string {
  const { course, homeworks, exercises, tab } = dialog;
  const pending = '<div class="loading" role="status"><span class="spinner"></span>正在同步列表…</div>';
  const unavailable = '<p class="detail-sync-message" role="status">列表暂未同步，请点击右上角刷新。</p>';
  const shixun = (homeworks ?? []).filter(homework => homework.homeworkKind !== 'common');
  const common = (homeworks ?? []).filter(homework => homework.homeworkKind === 'common');
  const homeworkBody = homeworks === null ? (dialog.failed.homework ? unavailable : pending) : `<p class="subtext">${shixun.length} 份课程实验，只处理必做关卡；当前仅自动处理课程实验，已完成的会自动跳过。</p><ul class="completion-list">${shixun.map(homework => {
    const complete = Boolean(homework.requiredProgressKnown && homework.requiredChallengeCount !== undefined && (homework.finishedRequiredChallengeCount ?? 0) >= homework.requiredChallengeCount);
    return `<li><span class="completion-icon ${complete ? 'done' : ''}">${complete ? '✓' : '·'}</span><div><strong>${e(homework.name)}</strong><p>${homework.requiredProgressKnown ? `${complete ? '已完成' : '待完成'} · ${count(homework.finishedRequiredChallengeCount)} / ${count(homework.requiredChallengeCount)} 必做关卡` : '开始处理时核验必做进度'}</p></div></li>`;
  }).join('') || '<li>这门课程暂无待处理的课程实验。</li>'}</ul>`;
  const commonBody = homeworks === null ? (dialog.failed.homework ? unavailable : pending) : `<p class="subtext">图文作业只展示状态，当前不支持自动提交；提交后由头歌教师批阅。</p><ul class="completion-list">${common.map(homework => { const status = commonHomeworkStatus(homework); return `<li><span class="completion-icon ${status.tone === 'completed' ? 'done' : ''}">${status.tone === 'completed' ? '✓' : '·'}</span><div class="exercise-row"><strong>${e(homework.name)}</strong><p>${status.label}${homework.deadline ? ` · 截止 ${e(homework.deadline)}` : ''}</p><p>${homework.status ? `作业状态：${e(homework.status)}` : '状态待确认'}</p><div class="exercise-actions">${commonHomeworkLink(course, homework)}</div></div></li>`; }).join('') || '<li>这门课程暂无图文作业。</li>'}</ul>`;
  const examBody = exercises === null ? (dialog.failed.exercise ? unavailable : pending) : exercises.length ? `<p class="subtext">在线考试需单独处理，不包含在一键完成课程作业中；已交卷的考试不会重复提交。</p><ul class="completion-list">${exercises.map(exercise => { const status = exerciseStatus(exercise); return `<li><span class="completion-icon ${status.tone === 'completed' ? 'done' : ''}">${status.tone === 'completed' ? '✓' : '·'}</span><div class="exercise-row"><strong>${e(exercise.name)}</strong><p>${status.label}${exercise.totalQuestions ? ` · ${exercise.totalQuestions} 道题` : ''}${exercise.deadline ? ` · 截止 ${e(exercise.deadline)}` : ''}</p><p>${e(exerciseReason(exercise))}</p><div class="exercise-actions">${exercise.submitted || !exercise.available || exercise.actionRequired === true ? '' : `<button class="button primary compact" data-exam-submit="${exercise.id}">开始处理考试</button>`}${exerciseLink(course, exercise)}</div></div></li>`; }).join('')}</ul>` : '<div class="empty"><h3>暂无在线考试</h3><p>这门课程当前没有可查看的在线考试。</p></div>';
  const hasData = tab === 'exercise' ? exercises !== null : homeworks !== null;
  const refreshed = dialog.updatedAt[tab] ? new Date(dialog.updatedAt[tab]).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit' }) : '';
  const message = dialog.loading[tab] ? (hasData ? '正在更新列表…' : '') : dialog.failed[tab] && hasData ? '暂未更新，继续显示上次结果。' : refreshed ? `更新于 ${refreshed}` : '';
  return `<h2 id="modal-title" class="course-detail-title">${e(course.name)}</h2><button class="icon-button course-detail-refresh ${dialog.loading.homework || dialog.loading.exercise ? 'refreshing' : ''}" type="button" data-action="detail-refresh" aria-label="刷新作业和考试" title="刷新作业和考试" ${dialog.loading.homework || dialog.loading.exercise ? 'disabled' : ''}>${icon('refresh')}</button><p class="detail-sync-message" role="status">${e(message)}</p><div class="detail-tabs" role="tablist"><button class="detail-tab ${tab === 'homework' ? 'selected' : ''}" data-detail-tab="homework" role="tab" aria-selected="${tab === 'homework'}">课程实验</button><button class="detail-tab ${tab === 'common' ? 'selected' : ''}" data-detail-tab="common" role="tab" aria-selected="${tab === 'common'}">图文作业</button><button class="detail-tab ${tab === 'exercise' ? 'selected' : ''}" data-detail-tab="exercise" role="tab" aria-selected="${tab === 'exercise'}">在线考试</button></div>${tab === 'homework' ? homeworkBody : tab === 'common' ? commonBody : examBody}<div class="modal-actions"><button class="button" data-action="close-modal">关闭</button></div>`;
}
function render(): void {
  if (jobDialog && state.modalKind === 'job') {
    const job = state.jobs.find(item => item.id === jobDialog?.id);
    if (job) state.modal = jobDetailContent(job, jobDialog.historical);
  }
  if (adminJobDialog && state.modalKind === 'admin-job') {
    const job = state.admin.jobs.find(item => item.id === adminJobDialog?.id);
    if (job) state.modal = adminJobDetailContent(job, adminJobDialog.historical);
  }
  // Draft values live only in the form controls. Preserve them across opening a
  // read-only dialog without serializing them into markup or persistent state.
  const draft = !state.user && !state.signingIn ? {
    login: document.querySelector<HTMLInputElement>('#login')?.value ?? '',
    password: document.querySelector<HTMLInputElement>('#password')?.value ?? '',
  } : null;
  const createDraft = state.user ? captureTicketForm('#ticket-create-form') : null;
  const replyDraft = state.user ? captureTicketForm('#ticket-reply-form') : null;
  if (createDraft) ticketFormDrafts.set('create:user', createDraft);
  if (replyDraft) ticketFormDrafts.set(`reply:${replyDraft.scope}:${replyDraft.id}`, replyDraft);
  root!.innerHTML = `${renderAssetUpdateNotice()}${state.user ? renderShell() : renderLogin()}${state.modal ? `<div class="modal-backdrop"><section class="modal${state.modalKind === 'guidelines' ? ' guidelines-modal' : state.modalKind === 'ticket' ? ' ticket-modal' : ''}" data-ticket-scope="${state.modalKind === 'ticket' && ticketDialog ? ticketDialog.admin ? 'admin' : 'user' : ''}" role="dialog" aria-modal="true" aria-labelledby="modal-title" tabindex="-1"><button class="icon-button modal-close" type="button" data-action="close-modal" aria-label="关闭">${icon('close')}</button><div class="modal-content">${state.modal}</div></section></div>` : ''}`;
  if (draft) {
    const account = document.querySelector<HTMLInputElement>('#login');
    const password = document.querySelector<HTMLInputElement>('#password');
    if (account) account.value = draft.login;
    if (password) password.value = draft.password;
  }
  restoreTicketForm(ticketFormDrafts.get('create:user') ?? null);
  const replyForm = document.querySelector<HTMLFormElement>('#ticket-reply-form');
  const replyScope = document.querySelector<HTMLElement>('[data-ticket-scope]')?.dataset.ticketScope ?? '';
  restoreTicketForm(replyForm ? ticketFormDrafts.get(`reply:${replyScope}:${replyForm.dataset.ticketId ?? ''}`) ?? null : null);
  bindEvents();
}
interface TicketFormDraft {
  selector: string; id: string; scope: string; values: Record<string, string>;
  focused?: { name: string; start: number | null; end: number | null; direction: 'forward' | 'backward' | 'none' | null };
}
function captureTicketForm(selector: string): TicketFormDraft | null {
  const form = document.querySelector<HTMLFormElement>(selector); if (!form) return null;
  const names = selector === '#ticket-create-form' ? ['subject', 'category', 'body'] : ['body'];
  const values: Record<string, string> = {};
  for (const name of names) values[name] = (form.elements.namedItem(name) as HTMLInputElement | null)?.value ?? '';
  const focusedName = names.find(name => form.elements.namedItem(name) === document.activeElement);
  const field = focusedName ? form.elements.namedItem(focusedName) as HTMLInputElement : null;
  const focused = field && focusedName ? { name: focusedName, start: field.selectionStart ?? null, end: field.selectionEnd ?? null, direction: field.selectionDirection ?? null } : undefined;
  return { selector, id: form.dataset.ticketId ?? '', scope: selector === '#ticket-create-form' ? 'user' : document.querySelector<HTMLElement>('[data-ticket-scope]')?.dataset.ticketScope ?? '', values, focused };
}
function restoreTicketForm(draft: TicketFormDraft | null): void {
  if (!draft) return; const form = document.querySelector<HTMLFormElement>(draft.selector); if (!form) return;
  const scope = draft.selector === '#ticket-create-form' ? 'user' : document.querySelector<HTMLElement>('[data-ticket-scope]')?.dataset.ticketScope ?? '';
  if ((form.dataset.ticketId ?? '') !== draft.id || scope !== draft.scope) return;
  for (const [name, value] of Object.entries(draft.values)) {
    const field = form.elements.namedItem(name) as HTMLInputElement | null; if (field) field.value = value;
  }
  if (draft.focused) {
    const field = form.elements.namedItem(draft.focused.name) as HTMLInputElement | null;
    if (field && !field.disabled) {
      field.focus();
      if (draft.focused.start !== null && draft.focused.end !== null && typeof field.setSelectionRange === 'function') {
        field.setSelectionRange(draft.focused.start, draft.focused.end, draft.focused.direction ?? 'none');
      }
    }
  }
}
function showModal(content: string, action: (() => Promise<void> | void) | null = null, kind: '' | 'guidelines' | 'ticket' | 'job' | 'admin-job' | 'course' = ''): void {
  if (kind !== 'course') courseDialog = null;
  if (kind !== 'ticket') ticketDialog = null;
  if (kind !== 'job') jobDialog = null;
  if (kind !== 'admin-job') adminJobDialog = null;
  previousFocus = document.activeElement as HTMLElement | null; modalRevision++;
  state.modal = content; state.modalAction = action; state.modalKind = kind; render();
  document.querySelector<HTMLButtonElement>('[data-action="close-modal"]')?.focus();
}
function closeModal(): void {
  const guidelines = state.modalKind === 'guidelines';
  const closedTicket = ticketDialog?.id;
  state.modal = ''; state.modalKind = ''; state.modalAction = null; modalRevision++; courseDialog = null; ticketDialog = null; jobDialog = null; adminJobDialog = null; render();
  if (guidelines) document.querySelector<HTMLButtonElement>(`[data-guidelines-source="${guidelinesSource}"]`)?.focus();
  else if (closedTicket) [...document.querySelectorAll<HTMLButtonElement>('[data-ticket-open]')].find(button => button.dataset.ticketOpen === closedTicket)?.focus();
  else if (previousFocus?.isConnected) previousFocus.focus();
}
function scheduleJobs(): void {
  stopPoll(); if (!state.user) return;
  pollTimer = setTimeout(() => {
    pollTimer = undefined;
    if (document.visibilityState === 'hidden') { scheduleJobs(); return; }
    revalidateVisibleCourseWork();
    void loadJobs(); void loadQuestionBank(); void loadTodaySummary();
  }, activeJobs().length ? 3000 : 15000);
}
async function loadJobs(): Promise<void> {
  if (!state.user) return; const revision = ++jobsRevision; const session = generation;
  try {
    const result = await api.jobs();
    if (session !== generation || revision !== jobsRevision) return;
    const changed = result.jobs.filter(job => serviceJob(job) && !pendingJob(job) && !state.jobs.some(previous => courseTerminalVersion(previous) === courseTerminalVersion(job)));
    state.jobs = result.jobs;
    const noticedRecovery = state.jobs.find(job => job.id === recoveryNoticeID);
    if (noticedRecovery) state.notice = recoveryNotice(noticedRecovery) || state.notice;
    for (const job of state.jobs) if (jobActions.get(job.id) === 'stopping' && !pendingJob(job)) jobActions.delete(job.id);
    state.selected = state.selected.filter(id => courseSelectable(id));
    if (jobDialog && state.modalKind === 'job') {
      const job = state.jobs.find(item => item.id === jobDialog?.id);
      if (job) state.modal = jobDetailContent(job, jobDialog.historical);
      render();
    } else if (!state.modal) render();
    const refreshed = new Set<string>();
    for (const job of changed) refreshJobCourseCache(job, refreshed);
  } catch (error) {
    if (session !== generation || revision !== jobsRevision) return;
    if (handleError(error)) return;
  }
  scheduleJobs();
}
async function loadQuestionBank(force = false): Promise<void> {
  if (!state.user || state.bankLoading || !force && bankLoadedAt > 0 && Date.now() - bankLoadedAt < 60000) return;
  const session = generation; const revision = ++bankRevision;
  state.bankLoading = true; if (!state.modal) render();
  try {
    const summary = await api.questionBankSummary();
    if (session !== generation || revision !== bankRevision) return;
    state.questionBank = summary; state.bankUnavailable = false;
  } catch (error) {
    if (session !== generation || revision !== bankRevision) return;
    if (handleError(error)) return;
    state.questionBank = null; state.bankUnavailable = true;
  }
  if (session !== generation || revision !== bankRevision) return;
  bankLoadedAt = Date.now(); state.bankLoading = false; if (!state.modal) render();
}
async function loadTodaySummary(): Promise<void> {
  const date = serviceDate();
  if (!state.user || state.todayLoading || todayLoadedDate === date && todayLoadedAt > 0 && Date.now() - todayLoadedAt < 60000) return;
  const session = generation; const revision = ++todayRevision;
  state.todayLoading = true;
  try {
    const summary = await api.todayServiceSummary();
    if (session !== generation || revision !== todayRevision) return;
    state.todaySummary = summary; state.todayUnavailable = summary.date !== serviceDate();
  } catch (error) {
    if (session !== generation || revision !== todayRevision) return;
    if (handleError(error)) return;
    state.todaySummary = null; state.todayUnavailable = true;
  }
  if (session !== generation || revision !== todayRevision) return;
  todayLoadedAt = Date.now(); todayLoadedDate = serviceDate(); state.todayLoading = false; if (!state.modal) render();
}
async function loadCourses(forceDetails = false): Promise<void> {
  const revision = ++coursesRevision; const session = generation; state.loading = true; render();
  try {
    const courses = await api.courses();
    if (session !== generation || revision !== coursesRevision) return;
    state.courses = courses;
    for (const id of [...homeworkSync.keys()]) if (!courses.some(course => course.identifier === id)) homeworkSync.delete(id);
    prefetchCourseDetails(courses, forceDetails);
    state.selected = state.selected.filter(id => courseSelectable(id));
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
  const account = value('login').trim(); const password = value('password');
  if (!account || !password) { state.loginError = '请输入头歌账号和密码。'; render(); return; }
  form.reset(); const session = ++generation; courseCache.clear(); state.signingIn = true; state.loginError = ''; render();
  try {
    const user = await api.login(account, password);
    if (session !== generation) return;
    state.user = user; state.signingIn = false; render(); void loadJobs(); void loadCourses(); void loadQuestionBank(); void loadTodaySummary();
  } catch (error) {
    if (session !== generation) return;
    const code = error instanceof ApiRequestError ? error.code : '';
    const messages: Record<string, string> = { AUTH_FAILED: '账号或密码不正确，请检查后重新登录。', USER_SUSPENDED: '账号服务暂不可用，请联系服务人员。', ACCOUNT_DISABLED: '账号服务暂不可用，请联系服务人员。', LOGIN_RATE_LIMITED: '登录有点频繁，请稍后再试。' };
    state.signingIn = false; state.loginError = messages[code || ''] || '登录暂未完成，请稍后再试或联系服务人员。'; render();
  }
}
async function logout(): Promise<void> { resetSession(); try { await api.logout(); } catch { /* The local account context has already been cleared. */ } }
function confirmStart(all: boolean): void {
  if (state.creating || !accessible() || all && allServicePending()) return;
  const selected = [...state.selected].filter(id => !coursePending(id));
  if (!all && !selected.length) return;
  const names = all ? state.courses.map(course => course.name) : state.courses.filter(course => selected.includes(course.identifier)).map(course => course.name);
  showModal(`<h2 id="modal-title">${all ? '完成全部未完成作业？' : '把这些课程交给我们？'}</h2><p class="subtext">只处理必做关卡，已完成和选做内容会自动跳过。</p><ul class="confirm-courses">${names.map(name => `<li>${icon('check')}${e(name)}</li>`).join('')}</ul><div class="detail-note">处理期间无需守候。完成进度随时可查，遇到问题会由服务人员跟进。</div><div class="modal-actions"><button class="button" data-action="close-modal">再看看</button><button class="button primary" data-action="modal-confirm">确认开始处理</button></div>`, () => startService(all, selected));
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
  state.notice = accepted === responses.length ? '已收到你的请求，接下来交给我们。完成进度会自动更新。' : '你的请求正在核对，请先查看完成进度，无需重复提交。';
  state.page = 'progress'; render(); void loadJobs();
}
function jobDetailContent(job: AnswerJob, historical: boolean): string {
  const archived = historical || !consumerCurrentJobs().some(current => current.id === job.id);
  return `<div id="modal-title">${renderServiceDetail(job, state.courses, archived, jobActions.get(job.id))}</div>`;
}
function currentJob(id: string): AnswerJob | undefined { return consumerCurrentJobs().find(job => job.id === id); }
function refreshJobCourseCache(job: AnswerJob, refreshed = new Set<string>()): void {
  if (job.request.scope === 'exercise') return;
  const identifiers = job.request.scope === 'all' ? state.courses.map(course => course.identifier) : job.request.course ? [job.request.course] : [];
  for (const id of identifiers) {
    if (refreshed.has(id)) continue;
    refreshed.add(id);
    void courseCache.refreshHomeworksAfterPending(id).catch(() => {});
  }
}
function confirmJobAction(id: string, action: 'retry' | 'recover' | 'cancel'): void {
  const job = currentJob(id);
  if (!job || jobActions.has(id) || !accessible() || !(action === 'retry' ? canRetryJob(job) : action === 'recover' ? canRecoverJob(job) : canCancelJob(job))) return;
  const retry = action === 'retry', recover = action === 'recover'; courseDialog = null;
  showModal(`<h2 id="modal-title">${recover ? '复核并继续这项任务？' : retry ? '重新执行这项任务？' : '停止这个任务？'}</h2><p class="subtext">${e(jobTitle(job, state.courses))}</p><div class="detail-note">${recover ? '先核对上次任务的完成结果，再继续剩余的必做内容。已核验通过的关卡会自动跳过；结果仍未明确时保留进度，不重复提交。' : retry ? '继续处理原来的范围，只处理尚未完成的必做内容。已核验通过的关卡会自动跳过，选做内容不会处理。' : '会保留已完成的结果，停止后续处理。请等待任务确认停止，之后可以重新执行未完成的内容。'}</div><div class="modal-actions"><button class="button" data-action="close-modal">再看看</button><button class="button primary" data-action="modal-confirm">${recover ? '确认复核并继续' : retry ? '确认重新执行' : '确认停止'}</button></div>`, () => performJobAction(id, action));
}
async function performJobAction(id: string, action: 'retry' | 'recover' | 'cancel'): Promise<void> {
  const source = currentJob(id);
  if (!source || jobActions.has(id) || !accessible() || !(action === 'retry' ? canRetryJob(source) : action === 'recover' ? canRecoverJob(source) : canCancelJob(source))) { closeModal(); return; }
  const session = generation; jobsRevision++; stopPoll(); jobActions.set(id, action); closeModal(); state.notice = ''; render();
  try {
    const job = action === 'retry' ? await api.retryJob(id) : action === 'recover' ? await api.recoverJob(id) : await api.cancelJob(id);
    if (session !== generation) return;
    if (!job || typeof job.id !== 'string' || !job.id || typeof job.status !== 'string' || !job.request) throw new Error('Invalid job response');
    state.jobs = [job, ...state.jobs.filter(previous => previous.id !== job.id)];
    if (action === 'cancel' && pendingJob(job)) jobActions.set(id, 'stopping'); else jobActions.delete(id);
    state.page = 'progress'; if (action === 'recover') recoveryNoticeID = job.id; state.selected = state.selected.filter(course => courseSelectable(course));
    state.notice = action === 'recover' ? recoveryNotice(job) || '已提交复核，确认上次结果后继续剩余内容。' : action === 'retry' ? pendingJob(job) ? '已重新开始处理，已完成的关卡会自动跳过。' : '已找到本次重试结果，请查看完成进度。' : pendingJob(job) ? '已请求停止，正在等待任务确认。停止后可重新执行。' : '任务已停止，已完成的结果会保留。'
    render(); refreshJobCourseCache(source); scheduleJobs();
  } catch (error) {
    if (session !== generation || handleError(error)) return;
    jobActions.delete(id); state.notice = action === 'recover' ? '暂未确认复核结果，请刷新进度后重试。' : action === 'retry' ? '暂未确认重新执行结果，请刷新进度后重试。' : '暂未确认停止结果，请刷新进度查看任务状态。'; render(); void loadJobs();
  }
}
function cachedCourseDialog(course: Course, tab: DetailTab): CourseDialog {
  const homeworks = courseCache.peekHomeworks(course.identifier);
  const exercises = courseCache.peekExercises(course.identifier);
  return { course, tab, homeworks: homeworks?.data ?? null, exercises: exercises?.data ?? null,
    loading: { homework: false, common: false, exercise: false }, failed: { homework: false, common: false, exercise: false },
    updatedAt: { homework: homeworks?.updatedAt ?? 0, common: homeworks?.updatedAt ?? 0, exercise: exercises?.updatedAt ?? 0 } };
}
function currentAdminJob(id: string, action: AdminJobAction): AdminJob | undefined {
  if (!isAdmin() || adminJobActions.has(id)) return undefined;
  const job = partitionAdminJobs(state.admin.jobs).current.find(value => value.id === id);
  if (!job || (action === 'cancel' ? !['queued', 'running'].includes(job.status) : adminJobResumeAction(job) !== action)) return undefined;
  return job;
}
function adminJobDetailContent(job: AdminJob, historical: boolean): string {
  const archived = historical || partitionAdminJobs(state.admin.jobs).history.some(item => item.id === job.id);
  return `<div id="modal-title">${renderAdminJobDetail(job, archived, adminJobActions.get(job.id))}</div>`;
}
function confirmAdminJobAction(id: string, action: AdminJobAction): void {
  const job = currentAdminJob(id, action);
  if (!job) return;
  const label = action === 'retry' ? '重新执行' : action === 'recover' ? '复核并继续' : '停止';
  const note = action === 'retry' ? '使用原用户当前有效的登录连接，继续处理原任务范围内尚未完成的必做内容。已核验通过的关卡自动跳过，选做内容不会处理。' : action === 'recover' ? '使用原用户当前有效的登录连接，先核对上次提交状态，再继续原范围内剩余内容。复核不通过时不会发送新的提交。' : '会保留已完成的结果，停止后续处理。请等待任务确认停止后再重新执行。';
  showModal(`<h2 id="modal-title">${label}该用户的任务？</h2><p class="subtext">${e(adminJobTarget(job))}</p><p class="detail-note">${note}此操作记录到管理审计。</p><div class="modal-actions"><button class="button" data-action="close-modal">取消</button><button class="button primary" data-action="modal-confirm">确认${label}</button></div>`, () => performAdminJobAction(id, action));
}
async function performAdminJobAction(id: string, action: AdminJobAction): Promise<void> {
  if (!currentAdminJob(id, action)) { closeModal(); return; }
  const session = generation;
  adminJobActions.set(id, action); closeModal(); state.notice = ''; render();
  try {
    const result = action === 'retry' ? await api.adminRetryJob(id) : action === 'recover' ? await api.adminRecoverJob(id) : await api.adminCancelJob(id);
    if (session !== generation) return;
    if (action !== 'cancel' && (!result || typeof result.id !== 'string' || !result.id || typeof result.status !== 'string')) throw new Error('Invalid admin action response');
    state.notice = action === 'retry' ? '已重新安排该用户的任务，已核验通过的关卡会自动跳过。' : action === 'recover' ? '已提交复核，确认上次结果后继续剩余内容。' : '已提交停止请求，请刷新确认停止状态。';
  } catch (error) {
    if (session !== generation || handleError(error)) return;
    state.notice = error instanceof ApiRequestError && error.code === 'OWNER_LOGIN_REQUIRED'
      ? `该用户的登录连接已过期，请用户重新登录后再${action === 'retry' ? '重新执行' : '复核继续'}。`
      : error instanceof ApiRequestError && error.code === 'OWNER_ACCESS_REVOKED'
        ? '该用户的服务已暂停，请先恢复用户服务，再刷新任务记录。'
        : `暂未确认${action === 'retry' ? '重新执行' : action === 'recover' ? '复核' : '停止'}结果，请刷新任务记录后重试。`;
  } finally {
    if (session === generation) {
      await loadAdmin();
      if (session === generation) { adminJobActions.delete(id); render(); }
    }
  }
}
function renderCourseDialog(): void {
  if (!courseDialog || state.modalKind !== 'course') return;
  const focus = document.activeElement as HTMLElement | null;
  const tabFocus = focus?.dataset.detailTab; const actionFocus = focus?.dataset.action; const examFocus = focus?.dataset.examSubmit;
  state.modal = renderCourseDetail(courseDialog); render();
  if (tabFocus) document.querySelector<HTMLButtonElement>(`[data-detail-tab="${tabFocus}"]`)?.focus();
  else if (actionFocus === 'detail-refresh') document.querySelector<HTMLButtonElement>(`[data-detail-tab="${courseDialog.tab}"]`)?.focus();
  else if (actionFocus === 'close-modal') document.querySelector<HTMLButtonElement>('[data-action="close-modal"]')?.focus();
  else if (examFocus) document.querySelector<HTMLButtonElement>(`[data-exam-submit="${examFocus}"]`)?.focus();
}
async function loadDialogTab(tab: DetailTab, force: boolean, revision: number, session: number): Promise<void> {
  const dialog = courseDialog; if (!dialog || session !== generation || revision !== modalRevision) return;
  const id = dialog.course.identifier;
  const cached = tab === 'homework' || tab === 'common' ? courseCache.peekHomeworks(id) : courseCache.peekExercises(id);
  if (!force && cached && !cached.stale) return;
  dialog.loading[tab] = true; dialog.failed[tab] = false; renderCourseDialog();
  try {
    if (tab === 'homework' || tab === 'common') {
      const data = await courseCache.getHomeworks(id, force);
      if (session !== generation || revision !== modalRevision || courseDialog !== dialog) return;
      dialog.homeworks = data; const updatedAt = courseCache.peekHomeworks(id)?.updatedAt ?? 0; dialog.updatedAt.homework = updatedAt; dialog.updatedAt.common = updatedAt;
    } else {
      const data = await courseCache.getExercises(id, force);
      if (session !== generation || revision !== modalRevision || courseDialog !== dialog) return;
      dialog.exercises = data; dialog.updatedAt.exercise = courseCache.peekExercises(id)?.updatedAt ?? 0;
    }
  } catch (error) {
    if (session !== generation || revision !== modalRevision || courseDialog !== dialog) return;
    if (handleError(error)) return;
    dialog.failed[tab] = true;
  }
  if (session !== generation || revision !== modalRevision || courseDialog !== dialog) return;
  dialog.loading[tab] = false; renderCourseDialog();
}
async function courseDetail(id: string): Promise<void> {
  const course = state.courses.find(item => item.identifier === id); if (!course) return;
  courseDialog = cachedCourseDialog(course, 'homework');
  showModal(renderCourseDetail(courseDialog), null, 'course');
  await loadDialogTab('homework', false, modalRevision, generation);
}
async function switchCourseDetailTab(tab: DetailTab): Promise<void> {
  if (!courseDialog) return;
  const course = courseDialog.course; const revision = ++modalRevision; const session = generation;
  courseDialog = cachedCourseDialog(course, tab); renderCourseDialog();
  await loadDialogTab(tab, false, revision, session);
}
function refreshCourseDetail(): void {
  if (!courseDialog || courseDialog.loading.homework || courseDialog.loading.common || courseDialog.loading.exercise) return;
  const revision = ++modalRevision; const session = generation;
  const workTab: DetailTab = courseDialog.tab === 'common' ? 'common' : 'homework';
  void loadDialogTab(workTab, true, revision, session);
  void loadDialogTab('exercise', true, revision, session);
}
function confirmExercise(course: Course, exercise: Exercise): void {
  if (state.creating || !accessible() || exercise.submitted || !exercise.available || exercise.actionRequired === true) return;
  const existing = (): boolean => currentServiceJobs(state.jobs).some(job => job.request.scope === 'exercise' && job.request.course === course.identifier && 'exercise_id' in job.request && job.request.exercise_id === exercise.id && (pendingJob(job) || serviceStatus(job) === 'support' || ['submitted', 'awaiting_review', 'graded'].includes(serviceStatus(job))));
  if (existing()) { closeModal(); state.page = 'progress'; render(); return; }
  showModal(`<h2 id="modal-title">开始处理这场考试？</h2><p class="subtext">${e(exercise.name)}</p><div class="detail-note">本次只处理这场考试，完成后会提交答案并核对交卷状态。需要在头歌处理的入场事项会单独提示。</div><div class="modal-actions"><button class="button" data-action="close-modal">取消</button><button class="button primary" data-action="modal-confirm">确认开始处理</button></div>`, async () => {
    if (state.creating || !accessible() || existing()) { closeModal(); return; }
    const session = generation; state.creating = true; closeModal();
    try {
      const response = await api.createJob({ scope: 'exercise', course: course.identifier, exercise_id: exercise.id, mode: 'submit', idempotency_key: crypto.randomUUID() });
      if (session !== generation) return;
      state.jobs = [response, ...state.jobs.filter(job => job.id !== response.id)]; state.page = 'progress'; state.notice = '考试任务已创建，完成进度会自动更新。';
    } catch (error) { if (session !== generation || handleError(error)) return; state.notice = '考试任务正在核对，请查看完成进度，无需重复提交。'; }
    if (session === generation) { state.creating = false; render(); void loadJobs(); }
  });
}
const ticketScreen = (admin: boolean): TicketScreen => admin ? adminTickets : userTickets;
const ticketVisible = (admin: boolean): boolean => Boolean(state.user) && state.page === (admin ? 'admin-support' : 'support') && (!admin || isAdmin());
function ticketErrorText(error: unknown): string {
  const code = error instanceof ApiRequestError ? error.code : '';
  const messages: Record<string, string> = {
    TICKET_INVALID: '请检查标题、分类和内容，勿填写密码、Cookie或密钥。',
    TICKET_CONFLICT: '反馈状态可能已经变化，请刷新后再试。',
    TICKET_RATE_LIMITED: '反馈较频繁或数量达到上限，请稍后再试。',
    NOT_FOUND: '这条反馈暂不可见，请刷新记录后查看。',
    ADMIN_REQUIRED: '当前账号未获反馈管理授权。',
  };
  return messages[code ?? ''] ?? '反馈暂未同步，请稍后重试；重复发送相同内容会核对已有记录。';
}
async function loadTickets(admin: boolean, more = false): Promise<void> {
  if (!state.user || admin && !isAdmin()) return;
  const screen = ticketScreen(admin), session = generation, revision = ++screen.revision;
  const cursor = more ? screen.nextCursor : undefined; if (more && !cursor) return;
  const filter = screen.filter ?? 'all'; screen.loading = true; screen.error = '';
  if (ticketVisible(admin)) render();
  try {
    const options = { limit: 30, ...(cursor ? { cursor } : {}), ...(filter === 'all' ? {} : { status: filter }) };
    const result = await (admin ? api.adminTickets(options) : api.tickets(options));
    if (session !== generation || revision !== screen.revision) return;
    screen.items = more ? [...new Map([...screen.items, ...result.items].map(item => [item.id, item])).values()] : result.items;
    screen.nextCursor = result.next_cursor;
  } catch (error) {
    if (session !== generation || revision !== screen.revision || handleError(error)) return;
    screen.error = ticketErrorText(error);
  }
  if (session !== generation || revision !== screen.revision) return;
  screen.loading = false; if (ticketVisible(admin)) render();
}
function renderTicketDialog(): void {
  const dialog = ticketDialog; if (!dialog || state.modalKind !== 'ticket') return;
  const screen = ticketScreen(dialog.admin), detail = screen.detail?.ticket.id === dialog.id ? screen.detail : null;
  const active = document.activeElement as HTMLElement | null;
  const focused = active?.id, refreshFocused = active?.dataset.ticketDetailRefresh;
  state.modal = `${screen.detailError ? `<div class="form-feedback" role="alert">${e(screen.detailError)}</div>` : ''}${detail ? renderTicketDetail(detail, dialog.admin, screen.submitting) : `<h2 id="modal-title">反馈详情</h2><p class="subtext" role="status">${screen.detailLoading ? '正在读取反馈记录…' : '暂未读取反馈详情，请刷新后查看。'}</p><button class="button" data-ticket-detail-refresh="${e(dialog.id)}" ${screen.detailLoading ? 'disabled' : ''}>${icon('refresh')}刷新详情</button>`}`;
  render();
  if (focused === 'ticket-reply-body') document.querySelector<HTMLTextAreaElement>('#ticket-reply-body')?.focus();
  else if (refreshFocused) document.querySelector<HTMLButtonElement>('[data-ticket-detail-refresh]')?.focus();
  else document.querySelector<HTMLButtonElement>('[data-action="close-modal"]')?.focus();
}
async function loadTicketDetail(dialog: { admin: boolean; id: string }): Promise<void> {
  if (!state.user || dialog.admin && !isAdmin() || ticketDialog !== dialog) return;
  const screen = ticketScreen(dialog.admin), session = generation, modal = modalRevision, revision = ++screen.detailRevision;
  screen.detailLoading = true; screen.detailError = ''; renderTicketDialog();
  try {
    const result = await (dialog.admin ? api.adminTicket(dialog.id) : api.ticket(dialog.id));
    if (session !== generation || modal !== modalRevision || revision !== screen.detailRevision || ticketDialog !== dialog) return;
    screen.detail = result;
  } catch (error) {
    if (session !== generation || modal !== modalRevision || revision !== screen.detailRevision || ticketDialog !== dialog || handleError(error)) return;
    screen.detailError = ticketErrorText(error);
  }
  if (session !== generation || modal !== modalRevision || revision !== screen.detailRevision || ticketDialog !== dialog) return;
  screen.detailLoading = false; renderTicketDialog();
}
function openTicket(admin: boolean, id: string): void {
  if (!ticketVisible(admin) || !id) return;
  const screen = ticketScreen(admin); screen.detail = null; screen.detailError = ''; screen.detailLoading = true;
  courseDialog = null; ticketDialog = { admin, id };
  showModal('<h2 id="modal-title">反馈详情</h2><p class="subtext">正在读取反馈记录…</p>', null, 'ticket');
  void loadTicketDetail(ticketDialog);
}
function retryTicketKey(target: string, body: object): string {
  const payload = JSON.stringify(body);
  let attempts = ticketRetryKeys.get(target);
  if (!attempts) { attempts = new Map(); ticketRetryKeys.set(target, attempts); }
  const previous = attempts.get(payload); if (previous) return previous;
  const key = crypto.randomUUID(); attempts.set(payload, key); return key;
}
function confirmTicketKey(target: string, body: object): void {
  const attempts = ticketRetryKeys.get(target); attempts?.delete(JSON.stringify(body));
  if (!attempts?.size) ticketRetryKeys.delete(target);
}
function updateTicketList(screen: TicketScreen, detail: SupportTicketDetail): void {
  const others = screen.items.filter(item => item.id !== detail.ticket.id);
  screen.items = screen.filter === 'all' || !screen.filter || screen.filter === detail.ticket.status ? [detail.ticket, ...others] : others;
}
async function createTicket(event: Event): Promise<void> {
  event.preventDefault(); if (!ticketVisible(false) || userTickets.submitting) return;
  const form = event.currentTarget as HTMLFormElement;
  const value = (name: string): string => (form.elements.namedItem(name) as HTMLInputElement | null)?.value ?? '';
  const input = { subject: value('subject').trim(), category: value('category') as TicketCategory, body: value('body').trim() };
  if (!input.subject || !input.body || !['task', 'account', 'data', 'suggestion', 'other'].includes(input.category)) { userTickets.error = '请填写问题标题、分类和详细说明。'; render(); return; }
  const session = generation, modal = modalRevision, screen = userTickets, target = 'create:user';
  const key = retryTicketKey(target, input); screen.submitting = true; screen.error = ''; screen.revision++; screen.loading = false; render();
  try {
    const detail = await api.createTicket({ ...input, idempotency_key: key });
    if (session !== generation) return;
    confirmTicketKey(target, input); ticketFormDrafts.delete('create:user'); screen.submitting = false; updateTicketList(screen, detail);
    if (ticketVisible(false)) document.querySelector<HTMLFormElement>('#ticket-create-form')?.reset();
    if (ticketVisible(false) && modal === modalRevision && !state.modal) {
      screen.detail = detail; screen.detailError = ''; screen.detailLoading = false; ticketDialog = { admin: false, id: detail.ticket.id };
      showModal(renderTicketDetail(detail, false), null, 'ticket');
    } else if (ticketVisible(false)) render();
    void loadTickets(false);
  } catch (error) {
    if (session !== generation || handleError(error)) return;
    screen.submitting = false; screen.error = ticketErrorText(error); if (ticketVisible(false)) render();
  }
}
async function mutateTicket(kind: 'reply' | 'status', status?: TicketStatus, event?: Event): Promise<void> {
  event?.preventDefault(); const dialog = ticketDialog;
  if (!dialog || !ticketVisible(dialog.admin)) return;
  const screen = ticketScreen(dialog.admin); if (screen.submitting || screen.detail?.ticket.id !== dialog.id) return;
  if (kind === 'status' && (!status || !['open', 'processing', 'resolved', 'closed'].includes(status) || !dialog.admin && !['open', 'closed'].includes(status))) return;
  const form = event?.currentTarget as HTMLFormElement | undefined;
  const body = (form?.elements.namedItem('body') as HTMLTextAreaElement | null)?.value.trim() ?? '';
  if (kind === 'reply' && !body) return;
  const session = generation, modal = modalRevision, target = `reply:${dialog.admin ? 'admin' : 'user'}:${dialog.id}`;
  const key = kind === 'reply' ? retryTicketKey(target, { body }) : '';
  screen.submitting = true; screen.detailError = ''; screen.revision++; screen.loading = false;
  screen.detailRevision++; screen.detailLoading = false; renderTicketDialog();
  try {
    const detail = kind === 'reply' ? await (dialog.admin ? api.adminReplyTicket(dialog.id, { body, idempotency_key: key }) : api.replyTicket(dialog.id, { body, idempotency_key: key }))
      : await (dialog.admin ? api.adminSetTicketStatus(dialog.id, status!) : api.setTicketStatus(dialog.id, status as 'open' | 'closed'));
    if (session !== generation) return;
    if (kind === 'reply') { confirmTicketKey(target, { body }); ticketFormDrafts.delete(target); }
    screen.submitting = false; updateTicketList(screen, detail);
    if (ticketDialog === dialog && modal === modalRevision) {
      if (kind === 'reply') document.querySelector<HTMLFormElement>('#ticket-reply-form')?.reset();
      screen.detail = detail; renderTicketDialog();
    } else if (ticketDialog?.admin === dialog.admin && state.modalKind === 'ticket') renderTicketDialog();
    else if (ticketVisible(dialog.admin) && !state.modal) render();
    void loadTickets(dialog.admin);
  } catch (error) {
    if (session !== generation || handleError(error)) return;
    screen.submitting = false;
    if (ticketDialog === dialog && modal === modalRevision) { screen.detailError = ticketErrorText(error); renderTicketDialog(); }
    else if (ticketDialog?.admin === dialog.admin && state.modalKind === 'ticket') renderTicketDialog();
    else if (ticketVisible(dialog.admin)) { screen.error = ticketErrorText(error); render(); }
  }
}
async function loadAdmin(): Promise<void> {
  if (!isAdmin()) return; const session = generation; const revision = ++adminRevision;
  state.admin.loading = true; state.admin.error = false; render();
  try {
    const [overview, users, jobs, audit] = await Promise.all([api.adminOverview(), api.adminUsers(), api.adminJobs(), api.adminAudit()]);
    if (session !== generation || revision !== adminRevision) return;
    Object.assign(state.admin, { overview, users, jobs, audit, loading: false, error: false }); render();
  } catch (error) {
    if (session !== generation || revision !== adminRevision) return;
    if (handleError(error)) return;
    if (error instanceof ApiRequestError && error.status === 403) { state.page = 'courses'; state.notice = '当前账号未获管理授权。'; }
    state.admin.loading = false; state.admin.error = true; render();
  }
}
function navigate(page: string): void {
  if (!['courses', 'progress', 'support', 'admin-overview', 'admin-users', 'admin-operations', 'admin-audit', 'admin-support'].includes(page)) return;
  if (page.startsWith('admin-') && !isAdmin()) return;
  if (state.modal) closeModal();
  state.page = page as Page; state.notice = ''; render();
  revalidateVisibleCourseWork();
  if (page === 'support' || page === 'admin-support') void loadTickets(page === 'admin-support');
  else if (page.startsWith('admin-')) void loadAdmin(); else if (page === 'progress') void loadJobs();
}

function resumeVisibleCourseWork(): void {
  if (!state.user || document.visibilityState === 'hidden') return;
  revalidateVisibleCourseWork();
  stopPoll(); void loadJobs(); void loadQuestionBank(); void loadTodaySummary();
}
document.addEventListener('visibilitychange', resumeVisibleCourseWork);
window.addEventListener?.('focus', resumeVisibleCourseWork);
function bindEvents(): void {
  document.querySelector<HTMLFormElement>('#login-form')?.addEventListener('submit', event => { void login(event); });
  document.querySelector<HTMLFormElement>('#ticket-create-form')?.addEventListener('submit', event => { void createTicket(event); });
  document.querySelector<HTMLFormElement>('#ticket-reply-form')?.addEventListener('submit', event => { void mutateTicket('reply', undefined, event); });
  const ticketAdmin = state.page === 'admin-support';
  document.querySelectorAll<HTMLButtonElement>('[data-ticket-open]').forEach(button => button.addEventListener('click', () => openTicket(ticketAdmin, button.dataset.ticketOpen!)));
  document.querySelectorAll<HTMLButtonElement>('[data-ticket-refresh]').forEach(button => button.addEventListener('click', () => { if (ticketVisible(ticketAdmin)) void loadTickets(ticketAdmin); }));
  document.querySelectorAll<HTMLButtonElement>('[data-ticket-more]').forEach(button => button.addEventListener('click', () => {
    if (ticketVisible(ticketAdmin) && !ticketScreen(ticketAdmin).loading) void loadTickets(ticketAdmin, true);
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-ticket-filter]').forEach(button => button.addEventListener('click', () => {
    if (!ticketVisible(ticketAdmin)) return;
    const filter = button.dataset.ticketFilter;
    if (!filter || !['all', 'open', 'processing', 'resolved', 'closed'].includes(filter)) return;
    const screen = ticketScreen(ticketAdmin); screen.filter = filter as 'all' | TicketStatus; screen.items = []; screen.nextCursor = '';
    void loadTickets(ticketAdmin);
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-ticket-detail-refresh]').forEach(button => button.addEventListener('click', () => {
    const dialog = ticketDialog;
    if (dialog && dialog.id === button.dataset.ticketDetailRefresh && !ticketScreen(dialog.admin).submitting) void loadTicketDetail(dialog);
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-ticket-status]').forEach(button => button.addEventListener('click', () => {
    if (ticketDialog?.id === button.dataset.ticketId) void mutateTicket('status', button.dataset.ticketStatus as TicketStatus);
  }));
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
    void switchCourseDetailTab(button.dataset.detailTab === 'exercise' ? 'exercise' : button.dataset.detailTab === 'common' ? 'common' : 'homework');
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-exam-submit]').forEach(button => button.addEventListener('click', () => {
    const exerciseID = Number(button.dataset.examSubmit);
    const exercise = courseDialog?.exercises?.find(item => item.id === exerciseID);
    if (courseDialog && exercise) confirmExercise(courseDialog.course, exercise);
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-job-detail]').forEach(button => button.addEventListener('click', () => {
    const job = state.jobs.find(item => item.id === button.dataset.jobDetail);
    if (job) {
      courseDialog = null; jobDialog = { id: job.id, historical: button.dataset.jobHistory === 'true' };
      showModal(jobDetailContent(job, jobDialog.historical), null, 'job');
    }
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-job-retry]').forEach(button => button.addEventListener('click', () => confirmJobAction(button.dataset.jobRetry!, 'retry')));
  document.querySelectorAll<HTMLButtonElement>('[data-admin-retry]').forEach(button => button.addEventListener('click', () => confirmAdminJobAction(button.dataset.adminRetry!, 'retry')));
  document.querySelectorAll<HTMLButtonElement>('[data-admin-recover]').forEach(button => button.addEventListener('click', () => confirmAdminJobAction(button.dataset.adminRecover!, 'recover')));
  document.querySelectorAll<HTMLButtonElement>('[data-job-recover]').forEach(button => button.addEventListener('click', () => confirmJobAction(button.dataset.jobRecover!, 'recover')));
  document.querySelectorAll<HTMLButtonElement>('[data-job-cancel]').forEach(button => button.addEventListener('click', () => confirmJobAction(button.dataset.jobCancel!, 'cancel')));
  document.querySelector<HTMLDetailsElement>('[data-service-history]')?.addEventListener('toggle', event => {
    state.progressHistoryOpen = (event.currentTarget as HTMLDetailsElement).open;
  });
  document.querySelectorAll<HTMLButtonElement>('[data-admin-job-detail]').forEach(button => button.addEventListener('click', () => {
    if (!isAdmin()) return;
    const job = state.admin.jobs.find(item => item.id === button.dataset.adminJobDetail);
    if (job) {
      adminJobDialog = { id: job.id, historical: partitionAdminJobs(state.admin.jobs).history.some(item => item.id === job.id) };
      showModal(adminJobDetailContent(job, adminJobDialog.historical), null, 'admin-job');
    }
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
    confirmAdminJobAction(button.dataset.adminCancel!, 'cancel');
  }));
  document.querySelectorAll<HTMLElement>('[data-action]').forEach(button => button.addEventListener(button instanceof HTMLInputElement ? 'change' : 'click', () => {
    switch (button.dataset.action) {
      case 'logout': void logout(); break;
      case 'refresh': void loadCourses(true); break;
      case 'jobs-refresh': void loadJobs(); break;
      case 'user-guidelines': guidelinesSource = button.dataset.guidelinesSource || 'login'; showModal(renderUserGuidelines(), null, 'guidelines'); break;
      case 'detail-refresh': refreshCourseDetail(); break;
      case 'bank-refresh': void loadQuestionBank(true); break;
      case 'admin-refresh': void loadAdmin(); break;
      case 'start-all': confirmStart(true); break;
      case 'start-selected': confirmStart(false); break;
      case 'select-all': {
        const checked = button instanceof HTMLInputElement ? button.checked : !(state.selected.length && state.selected.length === state.courses.filter(course => courseSelectable(course.identifier)).length);
        state.selected = checked ? state.courses.filter(course => courseSelectable(course.identifier)).map(course => course.identifier) : [];
        render(); break;
      }
      case 'close-modal': closeModal(); break;
      case 'modal-confirm': { const action = state.modalAction; state.modalAction = null; if (action) void action(); break; }
      case 'asset-update-refresh': reloadForAssetUpdate(); break;
    }
  }));
  document.querySelector<HTMLImageElement>('[data-avatar]')?.addEventListener('error', () => { if (state.user) { state.user.avatarUrl = undefined; render(); } });
}
async function adminAction(action: () => Promise<void>): Promise<void> {
  const session = generation; closeModal();
  try { await action(); if (session === generation) await loadAdmin(); }
  catch (error) { if (session !== generation || handleError(error)) return; state.notice = '管理操作暂未完成，请刷新数据确认状态。'; render(); }
}
document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && state.modal) closeModal();
  if (event.key === 'Tab' && state.modal) {
    const items = [...document.querySelectorAll<HTMLElement>('.modal button, .modal input, .modal textarea, .modal select, .modal a[href]')].filter(item => !(item as HTMLButtonElement).disabled);
    const first = items[0]; const last = items[items.length - 1];
    if (!items.includes(document.activeElement as HTMLElement)) { event.preventDefault(); (event.shiftKey ? last : first)?.focus(); }
    else if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  }
});
async function restore(): Promise<void> {
  const session = generation;
  try { const user = await api.me(); if (session !== generation) return; state.user = user; render(); void loadJobs(); void loadCourses(); void loadQuestionBank(); void loadTodaySummary(); }
  catch { /* Keep the existing login form intact, including fields currently being entered. */ }
}
render(); void restore();

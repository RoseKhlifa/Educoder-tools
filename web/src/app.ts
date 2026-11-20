import { ApiRequestError, BrowserApi } from './api.js';
import { adminJobResumeAction, adminJobTarget, partitionAdminJobs, renderAdmin, renderAdminJobDetail, type AdminPage, type AdminState } from './admin.js';
import { canCancelJob, canRetryJob, canRecoverJob, currentStage, homeworkSquares, jobHomeworks, recoveryNotice, type JobActionState, currentServiceJobs, jobTitle, pendingJob, renderJobs, renderServiceDetail, serviceJob, serviceStatus } from './jobs.js';
import { cn, count, deadline, duration, escapeHtml as e, icon, record } from './ui.js';
import { progressCounts, progressTone } from './progress.js';
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
  selected: [] as string[], loading: false, signingIn: false, sendingCode: false, creating: false, notice: '', loginError: '', loginNotice: '', search: '',
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
let loginMode: 'password' | 'sms' | 'wechat' = 'password';
let smsCooldownUntil = 0;
let smsCooldownTimer: ReturnType<typeof setTimeout> | undefined;
let loginRevision = 0;
type WechatStatus = 'idle' | 'starting' | 'waiting' | 'scanned' | 'canceled' | 'expired' | 'failed';
let wechatStatus: WechatStatus = 'idle';
let wechatRevision = 0, wechatImageVersion = 0, wechatExpiresAt = 0;
let wechatPollTimer: ReturnType<typeof setTimeout> | undefined;
let wechatPollController: AbortController | undefined;
let wechatStartTask: Promise<void> | undefined;
let wechatCancelTask: Promise<void> | undefined;
let wechatAttemptActive = false, wechatNeedsCancel = false;
let wechatCancelPending = false;
// Detailed single-job responses for the job the user is currently watching.
// The list projection omits per-level detail, so it is kept separately.
const jobDetails = new Map<string, AnswerJob>();
// The operator list projects jobs without their per-level rows, so rebuilding
// an open dialog from a list row blanked its level chips until the next
// single-job response. Keep the detailed copy until its replacement arrives.
const adminJobDetails = new Map<string, AdminJob>();
let focusTimer: ReturnType<typeof setTimeout> | undefined;
let adminTimer: ReturnType<typeof setTimeout> | undefined;
let previousFocus: HTMLElement | null = null;
let renderedHTML = '';
// Whether the password field is revealed. It lives here, not in the DOM, so a
// repaint (a login error, or the sign-in state) does not silently re-hide what
// the user asked to see.
let passwordVisible = false;
// Tracks whether a dialog was already on screen, so only the paint that opens
// one carries its entry animation.
let modalWasOpen = false;
// A poll repaints the whole page. If that happens between a user's press and
// release, the click lands on a node that no longer exists and is lost, so
// background repaints wait for the pointer to come up.
let pointerHeld = false;
let deferredRender = false;
let pointerHeldAt = 0;
// A press released outside the window delivers no pointerup, so the guard is
// also lifted on blur and on hiding, and never holds a repaint for longer
// than a press plausibly lasts.
const pointerHoldLimit = 1500;
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
// Retain an unconfirmed single-homework intent only for this login session.
// A retry after a lost response must resolve the same job, not create another.
const homeworkRetryKeys = new Map<string, string>();
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
// stopPoll owns the consumer timers only. It is called by scheduleJobs on
// every cycle, so cancelling the operator refresh here left the admin pages
// frozen after their first load: nothing re-armed that timer.
function stopPoll(): void { if (pollTimer !== undefined) clearTimeout(pollTimer); pollTimer = undefined; stopFocusPoll(); }
function stopSmsCooldown(): void { if (smsCooldownTimer !== undefined) clearTimeout(smsCooldownTimer); smsCooldownTimer = undefined; }
function scheduleSmsCooldown(): void {
  stopSmsCooldown();
  if (state.user || loginMode !== 'sms' || document.visibilityState === 'hidden' || smsCooldownUntil <= Date.now()) return;
  smsCooldownTimer = setTimeout(() => {
    smsCooldownTimer = undefined;
    render();
    scheduleSmsCooldown();
  }, Math.min(1000, smsCooldownUntil - Date.now()));
}
function resetSession(message = ''): void {
  loginRevision++; void stopWechatAttempt().catch(() => {});
  jobActions.clear(); recoveryNoticeID = ''; adminJobActions.clear(); homeworkRetryKeys.clear(); jobDialog = null; adminJobDialog = null; homeworkSync.clear(); homeworkJobObservations.clear(); jobDetails.clear(); adminJobDetails.clear();
  userTickets = emptyTickets(); adminTickets = emptyTickets(); ticketDialog = null; ticketRetryKeys.clear(); ticketFormDrafts.clear();
  renderedHTML = ''; stopAdminPoll(); generation++; jobsRevision++; coursesRevision++; adminRevision++; modalRevision++; bankRevision++; bankLoadedAt = 0; todayRevision++; todayLoadedAt = 0; todayLoadedDate = ''; guidelinesSource = 'login'; passwordVisible = false; loginMode = 'password'; smsCooldownUntil = 0; stopSmsCooldown(); stopPoll(); courseDialog = null; courseCache.clear();
  Object.assign(state, { user: null, page: 'courses', courses: [], jobs: [], selected: [], loading: false, signingIn: false, sendingCode: false, creating: false, notice: '', loginError: message, loginNotice: '', modal: '', modalKind: '', modalAction: null, search: '', progressHistoryOpen: false, questionBank: null, bankLoading: false, bankUnavailable: false, todaySummary: null, todayLoading: false, todayUnavailable: false, admin: { overview: {}, users: [], jobs: [], audit: [], loading: false, error: false, userSearch: '', userFilter: 'all' } });
  render();
}
function handleError(error: unknown): boolean {
  if (error instanceof ApiRequestError && error.isSessionExpired) { resetSession('登录已过期，请重新登录。你的任务进度会保留。'); return true; }
  return false;
}
function renderAssetUpdateNotice(): string {
  if (!assetUpdateAvailable) return '';
  return '<div class="update-banner" role="status"><span>头歌妈妈有了新版本。</span><button class="btn sm" type="button" data-action="asset-update-refresh">刷新页面</button></div>';
}
function reloadForAssetUpdate(): void {
  if (typeof window !== 'undefined' && typeof window.location?.reload === 'function') window.location.reload();
}
function brandLogo(): string {
  return '<img class="brand-logo" src="/assets/brand-logo.png?v=0.1.50" alt="" width="34" height="34" decoding="async">';
}

function avatar(): string {
  return state.user?.avatarUrl ? `<img class="avatar" src="${e(state.user.avatarUrl)}" alt="个人头像" data-avatar>` : `<span class="avatar" aria-hidden="true">${e(state.user?.name.slice(0, 1) || '同')}</span>`;
}
function questionBankCredit(): string {
  return '题库来源 <a href="https://github.com/ppxxforever/fuck-educoder" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">ppxxforever/fuck-educoder</a>';
}
/**
 * renderLoginHero draws the product at work out of the same components the
 * signed-in pages use: a stack of exercise sheets, red-pen ticks in the
 * margin, a 田字格 strip and the square seal. It is decorative and its content
 * is sample material, never anybody's data, so it is hidden from assistive
 * technology rather than read out.
 */
function renderLoginHero(): string {
  const rows: Array<[string, string, 'done' | 'run']> = [
    ['顺序表与链表', '3 / 3 关', 'done'],
    ['栈与队列应用', '6 / 6 关', 'done'],
    ['图的遍历', '2 / 4 关', 'run'],
  ];
  const cells = ['done', 'done', 'done', 'done', 'run', ''];
  return `<div class="login-hero" aria-hidden="true">
    <div class="hero-stack">
      <div class="hero-sheet hero-back"></div>
      <div class="hero-sheet hero-mid"></div>
      <article class="hero-sheet hero-front">
        <p class="hero-eyebrow">完成进度</p>
        <h3 class="hero-title">数据结构 · 第 6 章</h3>
        <ul class="hero-rows">${rows.map(([name, count, state]) => `<li><span class="${state === 'done' ? 'tick' : 'hero-run'}">${state === 'done' ? icon('tick') : '<span class="pulse"></span>'}</span><span class="hero-name">${name}</span><span class="hero-count num">${count}</span></li>`).join('')}</ul>
        <ol class="tzg hero-grid">${cells.map((state, index) => `<li class="${state}"><span class="n num">${index + 1}</span></li>`).join('')}</ol>
        <span class="seal square hero-seal"><span>全</span><span>部</span><span>完</span><span>成</span></span>
      </article>
      <div class="hero-note">16:04 自动重试<br><small>做好的关卡不会重做</small></div>
    </div>
  </div>`;
}
function renderLogin(): string {
  const steps: Array<[string, string, string]> = [
    ['book', '选课程', '挑一门，或者一次做完全部。'],
    ['tasks', '后台完成', '关掉页面也不会中断。'],
    ['grid', '看每一关', '每份作业、每个关卡都看得到。'],
  ];
  const trust = ['头歌账号直接登录，不用注册', '成绩以头歌为准', '做题数据用于题目与答案复用'];
  const reveal = passwordVisible;
  const sms = loginMode === 'sms';
  const wechat = loginMode === 'wechat';
  const secondsRemaining = Math.max(0, Math.ceil((smsCooldownUntil - Date.now()) / 1000));
  const wechatCopy: Record<WechatStatus, [string, string]> = {
    idle: ['微信扫码登录', '请获取二维码后，用已绑定头歌账号的微信扫一扫。'],
    starting: ['正在获取二维码…', '请稍候。'],
    waiting: ['请用微信扫一扫', '使用已绑定头歌账号的微信扫码，并在手机上确认登录。'],
    scanned: ['已扫码，请在手机上确认', '确认后将自动进入你的课程。'],
    canceled: ['本次微信登录已取消', '可以重新获取二维码，或选择其他登录方式。'],
    expired: ['二维码已过期', '请重新获取二维码后扫码。'],
    failed: ['微信登录暂未完成', '可以重新获取二维码，或选择其他登录方式。'],
  };
  const fields = wechat ? `<div class="wechat-login-status" role="status"><b>${wechatCopy[wechatStatus][0]}</b><p>${wechatCopy[wechatStatus][1]}</p>
      ${['waiting', 'scanned'].includes(wechatStatus) ? `<img class="wechat-qr" data-wechat-image src="/api/auth/wechat/image?v=${wechatImageVersion}" alt="微信登录二维码" width="200" height="200">` : ''}
      <button class="btn full" type="button" data-action="refresh-wechat" ${wechatStatus === 'starting' ? 'disabled' : ''}>${wechatStatus === 'idle' ? '获取二维码' : wechatStatus === 'starting' ? '正在获取…' : '重新获取二维码'}</button>
      </div>` : sms ? `
      <div class="field"><label for="login">头歌绑定手机号</label>
        <span class="input-wrap">${icon('user')}<input id="login" name="login" type="tel" inputmode="tel" autocomplete="tel" placeholder="输入头歌账号绑定的手机号" required maxlength="11"></span></div>
      <div class="code-entry"><div class="field"><label for="sms-code">短信验证码</label>
        <span class="input-wrap">${icon('doc')}<input id="sms-code" name="code" type="text" inputmode="numeric" autocomplete="one-time-code" placeholder="输入短信验证码" required maxlength="128"></span></div>
        <button class="btn code-send" type="button" data-action="send-phone-code" ${state.sendingCode || state.signingIn || secondsRemaining > 0 ? 'disabled' : ''}>${state.sendingCode ? '正在发送' : secondsRemaining > 0 ? `${secondsRemaining} 秒后重发` : '获取验证码'}</button></div>` : `
      <div class="field"><label for="login">头歌账号</label>
        <span class="input-wrap">${icon('user')}<input id="login" name="login" autocomplete="username" placeholder="手机号 / 邮箱 / 学号" required maxlength="120"></span></div>
      <div class="field"><label for="password">头歌密码</label>
        <span class="input-wrap">${icon('lock')}<input id="password" name="password" type="${reveal ? 'text' : 'password'}" autocomplete="current-password" placeholder="输入头歌密码" required maxlength="512"><button class="reveal" type="button" data-action="toggle-password" aria-pressed="${reveal}" aria-label="${reveal ? '隐藏密码' : '显示密码'}" title="${reveal ? '隐藏密码' : '显示密码'}">${icon(reveal ? 'eye-off' : 'eye')}</button></span></div>`;
  const submit = wechat ? '' : `<button class="btn red full" type="submit" ${state.signingIn || state.sendingCode ? 'disabled' : ''}>${state.signingIn ? '正在登录…' : '登录'}</button>`;
  return `<main id="view-login" class="login">
    <header class="login-top">
      <div class="brand login-brand-mark"><img class="brand-logo" src="/assets/brand-logo.png?v=0.1.50" alt="" width="64" height="64" decoding="async"><span class="brand-name">头歌妈妈</span><span class="brand-domain">touge.mom</span></div>
      <h1>选好课程，<br>剩下交给我们。</h1>
      <p class="lead">用头歌账号登录，挑出没做完的作业。每份作业、每个关卡的进度，回来就能看见。</p>
    </header>
    ${renderLoginHero()}
    <ol class="login-steps">${steps.map(([name, title, text], index) => `<li><span class="step-no num">${index + 1}</span><span class="step-mark">${icon(name)}</span><div><b>${title}</b><span>${text}</span></div></li>`).join('')}</ol>
    <ul class="login-trust">${trust.map(item => `<li><span class="tick">${icon('tick')}</span>${item}</li>`).join('')}</ul>
    <section class="login-panel"><form class="login-card" id="login-form">
      <span class="washi" aria-hidden="true"></span>
      <h2>登录</h2>
      <p class="sub">头歌账号就是这里的账号，不用另外注册。</p>
      <div class="seg auth-methods" role="tablist" aria-label="登录方式">
        <button class="${loginMode === 'password' ? 'on' : ''}" type="button" role="tab" data-auth-mode="password" aria-selected="${loginMode === 'password'}" ${state.signingIn || state.sendingCode ? 'disabled' : ''}>账号密码</button>
        <button class="${sms ? 'on' : ''}" type="button" role="tab" data-auth-mode="sms" aria-selected="${sms}" ${state.signingIn || state.sendingCode ? 'disabled' : ''}>手机验证码</button>
        <button class="${wechat ? 'on' : ''}" type="button" role="tab" data-auth-mode="wechat" aria-selected="${wechat}" ${state.signingIn || state.sendingCode ? 'disabled' : ''}>微信</button>
      </div>
      ${fields}
      ${state.loginError ? `<div class="form-feedback" role="alert">${e(state.loginError)}</div>` : ''}
      ${state.loginNotice ? `<div class="login-success" role="status">${e(state.loginNotice)}</div>` : ''}
      ${submit}
      <p class="fine">登录前请看<button class="link" type="button" data-action="user-guidelines" data-guidelines-source="login">《用户准则》</button>。平台会收集做题数据，用于题目与答案复用。</p>
    </form>
    <p class="login-aside">独立服务，成绩以头歌为准。<br>遇到问题，登录后可以提交反馈。</p></section></main>`;
}
function navItem(page: Page, label: string, name: string, badge = 0): string {
  const on = state.page === page;
  return `<button class="nav-item ${on ? 'on' : ''}" data-nav="${page}" ${on ? 'aria-current="page"' : ''}>${label}${badge ? `<span class="badge-num num">${badge}</span>` : ''}</button>`;
}
function tabItem(page: Page, label: string, name: string, badge = 0): string {
  const on = state.page === page;
  return `<button class="${on ? 'on' : ''}" data-nav="${page}" ${on ? 'aria-current="page"' : ''}>${icon(name)}<span class="lbl">${label}</span>${badge ? `<span class="badge-num num">${badge}</span>` : ''}</button>`;
}
function sideItem(page: Page, label: string, name: string, badge = 0): string {
  const on = state.page === page;
  return `<button class="side-item ${on ? 'on' : ''}" data-nav="${page}" ${on ? 'aria-current="page"' : ''}>${icon(name)}<span class="lbl">${label}</span>${badge ? `<span class="badge-num num">${badge}</span>` : ''}</button>`;
}
function renderQuestionBank(): string {
  const summary = state.questionBank;
  return `<section class="card note" aria-label="题库">
    <h3>题库<button class="btn ghost sm square" type="button" data-action="bank-refresh" aria-label="刷新题库" style="width:28px;height:28px" ${state.bankLoading ? 'disabled' : ''}>${icon('refresh')}</button></h3>
    ${summary ? `<p class="big"><b class="num">${cn(summary.total_questions)}</b>道题目</p>
    <dl class="ledger" style="margin-top:8px">
      <div><dt>客观题</dt><span class="dots"></span><dd>${cn(summary.objective_questions)}</dd></div>
      <div><dt>编程题</dt><span class="dots"></span><dd>${cn(summary.programming_problems)}</dd></div>
      <div><dt>绘图题</dt><span class="dots"></span><dd>${cn(summary.graphic_problems)}</dd></div>
    </dl>` : `<p class="small" role="status">${state.bankUnavailable ? '题库还没同步' : '正在同步题库…'}</p>`}
    <p class="small" title="按贡献记录去重统计，非全站首次新增题目数">${summary?.contributed_questions === undefined ? '正在核对你的贡献。' : `你已贡献 <mark>${cn(summary.contributed_questions)} 道</mark>，谢谢你。`}</p>
  </section>`;
}
function serviceDate(): string {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date());
  const field = (type: string): string => parts.find(part => part.type === type)?.value || '';
  return `${field('year')}-${field('month')}-${field('day')}`;
}
/** beijing reads the wall clock the service runs on, whatever the device says. */
function beijing(): { month: string; day: string; weekday: string; hour: number } {
  const parts = new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', month: 'numeric', day: 'numeric', weekday: 'long', hour: 'numeric', hour12: false }).formatToParts(new Date());
  const field = (type: string): string => parts.find(part => part.type === type)?.value || '';
  return { month: field('month'), day: field('day'), weekday: field('weekday'), hour: Number(field('hour')) || 0 };
}
function greetingText(): string {
  const hour = beijing().hour;
  return hour < 6 ? '夜深了' : hour < 11 ? '早上好' : hour < 13 ? '中午好' : hour < 18 ? '下午好' : '晚上好';
}
function todayLine(): string {
  const now = beijing();
  return now.month && now.day ? `${now.month}月${now.day}日 · ${now.weekday}` : '';
}
function renderTodaySummary(): string {
  const date = serviceDate(); const summary = state.todaySummary?.date === date ? state.todaySummary : null;
  return `<section class="card note" aria-label="累计服务">
    <h3>累计服务<small>自建站以来</small></h3>
    ${summary ? `<p class="big"><b class="num">${cn(summary.total_completed_questions)}</b>道题</p>
    <p class="small">累计 ${cn(summary.total_served_students)} 位同学 · 今天 <b>${cn(summary.completed_students)}</b> 位 · <b>${cn(summary.completed_questions)}</b> 道</p>
    <p class="small">北京时间 · 同一同学的同一题只记一次</p>`
    : `<p class="small" role="status">${state.todayUnavailable || state.todaySummary ? '统计暂时读不到' : '正在同步统计…'}</p>`}
  </section>`;
}
function renderCourseMeta(course: Course): string {
  // Missing upstream metadata stays explicitly unknown rather than blank.
  const teacher = course.teacherName || '教师待同步';
  const students = course.studentCount === undefined ? '人数待同步' : `${cn(course.studentCount)} 人`;
  return `<p class="meta" title="授课教师 · 学生人数">${e(`${teacher} · ${students}`)}</p>`;
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
function pageFooter(): string {
  return `<footer class="page-footer">头歌妈妈 · 把任务交给我们${appVersion ? ` · <span class="app-version">v${e(appVersion)}</span>` : ''}</footer>`;
}
function renderShell(): string {
  const admin = state.page.startsWith('admin-') && isAdmin();
  const current = consumerCurrentJobs();
  const active = current.filter(pendingJob).length;
  const followUp = current.filter(job => serviceStatus(job) === 'support').length;
  const completed = state.jobs.filter(job => serviceJob(job) && serviceStatus(job) === 'completed').length;
  const openTickets = userTickets.items.filter(ticket => ticket.status !== 'closed' && ticket.status !== 'resolved').length;
  const adminOpen = adminTickets.items.filter(ticket => ticket.status === 'open' || ticket.status === 'processing').length;
  const pendingWork = pendingHomeworkTotal();
  const consumerNav = `${navItem('courses', '我的课程', 'book')}${navItem('progress', '完成进度', 'tasks', active + followUp)}${navItem('support', '问题反馈', 'chat', openTickets)}`;
  const adminNav = `${sideItem('admin-overview', '服务概览', 'home')}${sideItem('admin-users', '用户管理', 'users')}${sideItem('admin-operations', '任务运营', 'tasks')}${sideItem('admin-support', '反馈工单', 'chat', adminOpen)}${sideItem('admin-audit', '操作审计', 'doc')}`;
  const page = state.page === 'admin-support' ? renderTickets(adminTickets, true)
    : admin ? renderAdmin(state.page.replace('admin-', '') as AdminPage, state.admin, adminJobActions)
      : state.page === 'support' ? renderTickets(userTickets, false)
        : state.page === 'courses' ? renderCourses() : renderProgress();
  if (admin) {
    return `<div id="view-app" class="app-shell admin-app"><div class="shell">
      <aside class="admin-side" aria-label="管理员导航">
        <button class="brand" type="button" data-nav="admin-overview" aria-label="头歌妈妈服务概览">${brandLogo()}<span class="brand-name">头歌妈妈</span><span class="chip">管理</span></button>
        ${adminNav}
        <div class="foot">
          <button class="side-item" data-nav="courses">${icon('back')}<span class="lbl">返回用户端</span></button>
          <div class="side-me">${avatar()}<div>${e(state.user?.name || '管理员')}<small>管理员</small></div></div>
          <button class="side-item" data-action="logout">${icon('out')}<span class="lbl">退出登录</span></button>
        </div>
      </aside>
      <main class="main"><div class="main-in admin-main-in">${state.notice ? `<div class="sticky-note" role="status"><div>${e(state.notice)}</div></div>` : ''}${page}${pageFooter()}</div></main>
    </div></div>`;
  }
  return `<div id="view-app" class="app-shell consumer-app">
    <header class="topbar">
      <button class="brand" type="button" data-nav="courses" aria-label="头歌妈妈首页">${brandLogo()}<span class="brand-name">头歌妈妈</span><span class="brand-domain">touge.mom</span></button>
      <nav class="nav" aria-label="主导航">${consumerNav}</nav>
      <div class="top-end">
        ${isAdmin() ? `<button class="btn ghost sm admin-link" data-nav="admin-overview">${icon('grid')}管理后台</button>` : ''}
        <span class="me">${avatar()}<span class="nm">${e(state.user?.name || '同学')}</span></span>
        <button class="btn ghost square" data-action="logout" aria-label="退出登录" title="退出登录">${icon('out')}</button>
      </div>
    </header>
    <div class="shell">
      <main class="main"><div class="main-in">${state.notice ? `<div class="sticky-note" role="status"><div>${e(state.notice)}</div></div>` : ''}${page}${pageFooter()}</div></main>
      <aside class="side" aria-label="概况">
        <section class="card note">
          <h3>当前概况</h3>
          <dl class="ledger">
            <div><dt>我的课程</dt><span class="dots"></span><dd data-stat="courses">${state.courses.length}</dd></div>
            <div><dt>待完成作业</dt><span class="dots"></span><dd class="${pendingWork.total ? 'hot' : ''}" data-stat="pending">${pendingWork.known ? pendingWork.total : '—'}</dd></div>
            <div><dt>正在处理</dt><span class="dots"></span><dd data-stat="running">${active}</dd></div>
            <div><dt>需要跟进</dt><span class="dots"></span><dd class="${followUp ? 'hot' : ''}" data-stat="followup">${followUp}</dd></div>
            <div><dt>已完成任务</dt><span class="dots"></span><dd data-stat="completed">${completed}</dd></div>
          </dl>
        </section>
        ${renderTodaySummary()}
        ${renderQuestionBank()}
        <div class="side-foot">独立服务，成绩以头歌为准，不承诺成绩或完成时限。<br><button class="link" type="button" data-action="user-guidelines" data-guidelines-source="sidebar">用户准则与数据说明</button> · ${questionBankCredit()}</div>
      </aside>
    </div>
    <nav class="bottom-nav" aria-label="主导航">${tabItem('courses', '我的课程', 'book')}${tabItem('progress', '完成进度', 'tasks', active + followUp)}${tabItem('support', '问题反馈', 'chat', openTickets)}</nav>
    ${renderSelectionBar()}
  </div>`;
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
// Revalidating the course lists is the only background polling that reaches
// Educoder, and it goes out on the same account and address as that user's
// jobs. A tab left open but abandoned would re-read every course forever, so
// it pauses once the person has been idle for a while. Job polling is
// deliberately not touched: someone watching a running job still sees it.
const idleRevalidateAfter = 15 * 60 * 1000;
let lastInputAt = Date.now();
const userIsIdle = (): boolean => Date.now() - lastInputAt >= idleRevalidateAfter;
/** noteUserInput records activity and refreshes once when idleness ends. */
function noteUserInput(): void {
  const resuming = userIsIdle();
  lastInputAt = Date.now();
  // The first input after an idle stretch revalidates immediately, exactly as
  // returning to the tab does.
  if (resuming) revalidateVisibleCourseWork();
}
function revalidateVisibleCourseWork(): void {
  if (!state.user || document.visibilityState === 'hidden' || !['courses', 'progress'].includes(state.page)) return;
  if (userIsIdle()) return;
  courseCache.revalidateHomeworks(state.courses.map(course => course.identifier));
}

/**
 * pendingHomeworkTotal counts the required assignments still to do. `known` is
 * false until every course's list has arrived, so the page can say "still
 * counting" instead of printing half a number as if it were the whole.
 */
function pendingHomeworkTotal(): { total: number; courses: number; known: boolean } {
  let total = 0, courses = 0, known = true;
  for (const course of state.courses) {
    const summary = homeworkSummary(course);
    if (!summary.ready) { known = false; continue; }
    total += summary.runnableCount;
    if (summary.runnableCount > 0) courses++;
  }
  return { total, courses, known };
}
/** homeworkPastDeadline reads the classroom deadline as Beijing wall time. */
function homeworkPastDeadline(homework: Homework): boolean {
  if (/已截止|已结束|closed|expired/i.test(homework.status)) return true;
  const deadline = homework.deadline?.trim() ?? '';
  if (!deadline) return false;
  const explicitZone = /^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(?::\d{2})?$/.test(deadline) ? `${deadline.replace(' ', 'T')}+08:00` : deadline;
  const endsAt = Date.parse(explicitZone);
  return Number.isFinite(endsAt) && endsAt <= Date.now();
}
/**
 * homeworkAttention names the assignments a settled job left needing
 * attention, so a card's squares carry the same states as the task sheet
 * instead of showing plain empty boxes beside "需要跟进". The consumer
 * projection sometimes redacts the assignment id, so titles match too.
 */
function homeworkAttention(course: string): { ids: Set<number>; titles: Set<string> } {
  const ids = new Set<number>(), titles = new Set<string>();
  for (const job of matchingCourseJobs(course)) {
    if (pendingJob(job)) continue;
    for (const item of jobHomeworks(job)) {
      if (progressTone(item.status) !== 'support') continue;
      const id = Number((item as { homework_id?: unknown }).homework_id);
      if (Number.isSafeInteger(id) && id > 0) ids.add(id);
      const title = String(item.title ?? '').trim();
      if (title) titles.add(title);
    }
  }
  return { ids, titles };
}
/**
 * courseSquares draws the exercise book: one square per required assignment,
 * ticked when it is done, pulsing while it runs, marked when a recent task
 * left it needing attention, hatched once its deadline has passed. It needs
 * the cached assignment list, so it reports a skeleton row until that arrives
 * rather than inventing a count.
 */
function courseSquares(course: Course): string {
  const cached = courseCache.peekHomeworks(course.identifier);
  const summary = homeworkSummary(course);
  if (!cached) {
    return summary.sync === 'error'
      ? '<p class="book-note">作业列表没同步上，刷新再试。</p>'
      : '<span class="skel" style="width:72%;height:19px"></span>';
  }
  const shixun = cached.data.filter(homework => homework.homeworkKind !== 'common');
  if (!shixun.length) return '<p class="book-note">这门课没有必做作业</p>';
  const attention = homeworkAttention(course.identifier);
  const cells = shixun.slice(0, 60).map(homework => {
    if (homeworkComplete(homework)) return '<i class="done"></i>';
    if (homeworkPending(course.identifier, homework.id)) return '<i class="run"></i>';
    if (attention.ids.has(homework.id) || attention.titles.has(homework.name.trim())) return '<i class="warn"></i>';
    return homeworkPastDeadline(homework) ? '<i class="past"></i>' : '<i></i>';
  }).join('');
  const done = shixun.filter(homeworkComplete).length;
  return `<div class="squares" role="img" aria-label="${e(`${shixun.length} 份作业，完成 ${done} 份`)}">${cells}</div>`;
}
/**
 * courseFootState is the one line under a course's squares. Its precedence is
 * deliberate: live work first, then whether the list can be trusted, and only
 * then a completion claim. A course never says "done" while a read that could
 * contradict it is still in flight.
 */
function courseFootState(course: Course, summary: HomeworkSummary): string {
  const cached = courseCache.peekHomeworks(course.identifier);
  const shixun = cached ? cached.data.filter(homework => homework.homeworkKind !== 'common') : [];
  const done = shixun.filter(homeworkComplete).length;
  const tally = shixun.length ? ` · ${done} / ${shixun.length} 份` : '';
  const matching = matchingCourseJobs(course.identifier);
  const running = matching.find(pendingJob);
  if (running) return `<span class="state run"><span class="pulse"></span>正在处理${tally}</span>`;
  if (matching.some(job => jobActions.has(job.id))) return '<span class="state run"><span class="pulse"></span>正在更新</span>';
  if (!cached) return `<span class="state muted">${summary.sync === 'error' ? '没能同步' : '正在同步作业…'}</span>`;
  // A read that could contradict the list is still in flight, so nothing here
  // may claim the course is finished yet.
  if (summary.sync === 'loading') return `<span class="state">正在核对${tally}</span>`;
  if (summary.stale) return `<span class="state">${summary.sync === 'error' ? '显示上次结果' : '正在核对'}${tally}</span>`;
  if (!shixun.length) return `<span class="state muted">${summary.commonPending > 0 ? '图文作业待提交' : '无需处理'}</span>`;
  if (summary.runnableCount === 0) {
    if (summary.commonPending > 0) return `<span class="state">图文作业待提交${tally}</span>`;
    return `<span class="state done"><span class="tick">${icon('tick')}</span>全部完成 · ${shixun.length} 份</span>`;
  }
  if (matching.some(job => serviceStatus(job) === 'support')) return `<span class="state run">${icon('bang')}需要跟进${tally}</span>`;
  return `<span class="state">还差 ${summary.runnableCount} 份${tally}</span>`;
}
/** renderSelectionBar is the dark ink bar that appears once courses are picked. */
function renderSelectionBar(): string {
  if (state.page !== 'courses' || !state.selected.length) return '';
  const picked = state.courses.filter(course => state.selected.includes(course.identifier));
  let pending = 0, known = true;
  for (const course of picked) {
    const summary = homeworkSummary(course);
    if (summary.ready) pending += summary.runnableCount; else known = false;
  }
  return `<div class="selbar" role="status">
    <span>已选 <b class="num">${picked.length}</b> 门课程${known && pending ? `<span class="sub"> · <span class="num">${pending}</span> 份待完成</span>` : ''}</span>
    <span class="acts"><button class="btn sm cancel" data-action="select-clear">取消</button><button class="btn sm red" data-action="start-selected" ${state.creating || !accessible() ? 'disabled' : ''}>${state.creating ? '正在安排…' : '开始完成'}</button></span>
  </div>`;
}
function renderCourses(): string {
  const visible = state.courses.filter(course => !state.search || course.name.toLowerCase().includes(state.search.toLowerCase()));
  const active = activeJobs();
  const live = active[0];
  const selectable = state.courses.filter(course => courseSelectable(course.identifier));
  const selectedAll = selectable.length > 0 && selectable.every(course => state.selected.includes(course.identifier));
  const work = pendingHomeworkTotal();
  const lead = !state.courses.length ? (state.loading ? '正在同步你的课程…' : '加入头歌课堂后，刷新这里就能同步。')
    : !work.known ? '正在核对每门课还剩多少作业…'
      : work.total ? `还有 <mark>${work.total} 份</mark>必做作业没完成，在 ${work.courses} 门课程里。`
        : '必做作业都完成了。';
  return `<div class="head">
    <div><p class="date">${e(todayLine())}</p><h1>${e(greetingText())}，${e(state.user?.name || '同学')}</h1><p class="lead">${lead}</p></div>
    <div class="actions">
      <button class="btn square" data-action="refresh" aria-label="刷新课程" title="刷新课程" ${state.loading ? 'disabled' : ''}>${icon('refresh')}</button>
      <button class="btn red" data-action="start-all" ${state.loading || state.creating || !state.courses.length || !selectable.length || !accessible() || allServicePending() ? 'disabled' : ''}>完成全部作业</button>
    </div></div>
    ${!accessible() ? '<div class="sticky-note" role="status">' + icon('alert') + '<div>你的服务暂时停用了，已有进度还能看。需要恢复请发问题反馈。</div></div>' : ''}
    ${live ? renderLiveSlip(live) : ''}
    <div class="section-head">
      <h2>我的课程<span>${state.courses.length} 门</span></h2>
      <label class="search">${icon('search')}<span class="sr-only">搜索课程</span><input data-search type="search" placeholder="搜索课程" value="${e(state.search)}"></label>
      <button class="link pick-all" data-action="select-all" ${state.creating || !accessible() || !selectable.length ? 'disabled' : ''}>${selectedAll ? '清空选择' : '全选可处理'}</button>
    </div>
    ${state.loading && !state.courses.length ? `<div class="card loading" role="status"><span class="skel" style="width:40%"></span>正在同步你的课程…</div>`
      : visible.length ? `<div class="books">${visible.map(course => renderCourseCard(course)).join('')}</div>`
        : `<div class="card empty"><h3>${state.search ? '没有这门课' : '还没有课程'}</h3><p>${state.search ? '换个名字再搜。' : '加入头歌课堂后，刷新这里就能同步。'}</p></div>`}`;
}
/** renderLiveSlip is the "what is running right now" card on the courses page. */
function renderLiveSlip(job: AnswerJob): string {
  const items = jobHomeworks(job);
  const counts = progressCounts(items);
  const stage = currentStage(job);
  const focus = (typeof record(job.result).current_title === 'string' ? String(record(job.result).current_title).trim() : '')
    || items.filter(item => progressTone(item.status) === 'processing').map(item => String(item.title ?? '').trim()).filter(Boolean)[0] || '';
  const started = job.started_at || job.created_at;
  const elapsed = duration(started, new Date().toISOString());
  const tally = items.length ? `${counts.completed} / ${items.length} 份作业` : '';
  return `<section class="card live" aria-label="正在处理">
    <div class="live-title"><span class="pulse"></span><b>正在处理</b><span>${e(jobTitle(job, state.courses))}</span></div>
    <button class="btn sm" data-job-detail="${e(job.id)}">查看进度${icon('right')}</button>
    <p class="live-stage">${focus ? `正在做「${e(focus)}」` : '正在安排处理'}${stage ? ` · <mark>${e(stage)}</mark>` : ''}</p>
    ${homeworkSquares(items, true, true)}
    <div class="live-meta num">${e([tally, elapsed ? `已用时 ${elapsed}` : ''].filter(Boolean).join(' · '))}</div>
  </section>`;
}
function renderCourseCard(course: Course): string {
  const summary = homeworkSummary(course);
  const cached = courseCache.peekHomeworks(course.identifier);
  const shixun = cached ? cached.data.filter(homework => homework.homeworkKind !== 'common') : [];
  const selectableCourse = courseSelectable(course.identifier);
  const selected = state.selected.includes(course.identifier);
  const allDone = Boolean(cached && shixun.length > 0 && summary.runnableCount === 0 && !summary.stale && summary.sync === 'ready');
  // The box stays on the card even when the course cannot be scheduled: a
  // disabled box says "not this one, not now", where a missing box would read
  // as if selection did not exist.
  // The box must follow the input directly: the tick is drawn by an adjacent
  // sibling rule on :checked.
  const pick = `<label class="pick" title="${allDone ? '这门课已全部完成' : selectableCourse ? '选择这门课' : '现在不能选这门课'}"><input type="checkbox" data-select-course="${e(course.identifier)}" aria-label="${e(`选择${course.name}`)}" ${selected ? 'checked' : ''} ${!selectableCourse || state.creating || !accessible() ? 'disabled' : ''}><span class="box"></span></label>`;
  const corner = `${allDone ? '<span class="seal" role="img" aria-label="全部完成">完<br>成</span>' : ''}${pick}`;
  return `<article class="card book ${selected ? 'picked' : ''}" data-course-row="${e(course.identifier)}" data-pending="${summary.ready ? String(summary.runnableCount) : ''}" data-common="${summary.ready ? String(summary.commonPending) : ''}" data-sync="${summary.sync}"${summary.stale ? ' data-stale="true"' : ''}>
    <div class="book-top"><div class="book-id"><h3>${e(course.name)}</h3>${renderCourseMeta(course)}</div>${corner}</div>
    ${courseSquares(course)}
    <div class="book-foot">${courseFootState(course, summary)}<button class="link" data-course-detail="${e(course.identifier)}">详情${icon('right')}</button></div>
  </article>`;
}
function renderProgress(): string {
  const current = consumerCurrentJobs();
  const active = current.filter(pendingJob).length;
  const followUp = current.filter(job => serviceStatus(job) === 'support').length;
  const title = active ? `正在处理 ${active} 项` : followUp ? `${followUp} 项需要跟进` : '没有在处理的任务';
  const lead = active ? '进度会自动更新，关掉页面也不会中断。' : followUp ? '下面写了每项停在哪里，和现在能做的一件事。' : '做过的任务都收在历史记录里。';
  return `<div class="head"><div><p class="date">完成进度</p><h1>${e(title)}</h1><p class="lead">${e(lead)}</p></div>
    <div class="actions"><button class="btn square" data-action="jobs-refresh" aria-label="刷新进度" title="刷新进度">${icon('refresh')}</button></div></div>
    ${renderJobs(state.jobs, state.courses, state.progressHistoryOpen, jobActions, historicalCourseRecords())}`;
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
  return `<a class="link" href="${e(url)}" target="_blank" rel="noopener noreferrer">在头歌查看${icon('right')}</a>`;
}
function commonHomeworkLink(course: Course, homework: Homework): string {
  const path = `/classrooms/${encodeURIComponent(course.identifier)}/common_homework/${encodeURIComponent(String(homework.id))}/detail`;
  let url = `https://www.educoder.net${path}`;
  try {
    const source = new URL(homework.detailUrl || url);
    if (source.protocol === 'https:' && ['www.educoder.net', 'educoder.net'].includes(source.hostname) && !source.username && !source.password && !source.port
      && (source.pathname === path || source.pathname.startsWith(`${path}/`))) url = `${source.origin}${source.pathname}${source.search}`;
  } catch { /* Keep the verified classroom common-homework route. */ }
  return `<a class="link" href="${e(url)}" target="_blank" rel="noopener noreferrer">在头歌查看${icon('right')}</a>`;
}
function commonHomeworkStatus(homework: Homework): { label: string; tone: string } {
  if (homework.submissionStatus === 'submitted') return { label: '已提交 · 待批阅', tone: 'processing' };
  if (homework.submissionStatus === 'not_submitted') return { label: '待提交', tone: 'waiting' };
  return { label: '状态待确认', tone: 'waiting' };
}
function homeworkComplete(homework: Homework): boolean {
  return Boolean(homework.requiredProgressKnown && homework.requiredChallengeCount !== undefined && (homework.finishedRequiredChallengeCount ?? 0) >= homework.requiredChallengeCount);
}
function homeworkManualEligible(course: Course, homework: Homework): boolean {
  if (course.ended || homework.homeworkKind !== 'shixun' || homework.automaticSupported !== true || homework.canEnter === false
    || !homework.requiredProgressKnown || homework.requiredChallengeCount === undefined || homework.finishedRequiredChallengeCount === undefined
    || homeworkComplete(homework) || /已截止|已结束|未发布|未开始|closed|expired/i.test(homework.status)) return false;
  // Naive upstream classroom timestamps use Beijing time. Undated entries
  // still come from the server's open-homework catalog and are checked again.
  const deadline = homework.deadline?.trim() ?? '';
  const explicitZone = /^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(?::\d{2})?$/.test(deadline)
    ? `${deadline.replace(' ', 'T')}+08:00` : deadline;
  const endsAt = Date.parse(explicitZone);
  return !Number.isFinite(endsAt) || endsAt > Date.now();
}
function homeworkPending(course: string, homeworkID: number): boolean {
  return state.jobs.some(job => serviceJob(job) && pendingJob(job) && (job.request.scope === 'all'
    || job.request.course === course && (job.request.scope === 'course' || job.request.scope === 'homework' && job.request.homework_id === homeworkID)));
}
/**
 * renderCourseDetail is the course sheet: the three catalogues are folder tabs
 * attached to the sheet, and each assignment is a ruled row with its mark in
 * the red margin.
 */
function renderCourseDetail(dialog: CourseDialog): string {
  const { course, homeworks, exercises, tab } = dialog;
  const shixun = (homeworks ?? []).filter(homework => homework.homeworkKind !== 'common');
  const common = (homeworks ?? []).filter(homework => homework.homeworkKind === 'common');
  const summary = homeworkSummary(course);
  const pendingRows = (): string => '<div class="loading" role="status"><span class="skel" style="width:38%"></span>正在读取…</div>';
  const unavailable = (): string => '<p class="muted sheet-note" role="status">没读到列表，点右上角刷新。</p>';
  const row = (mark: string, name: string, meta: string, right: string, cls = ''): string =>
    `<li class="${cls}">${mark}<div><span class="hw-name">${name}</span>${meta ? `<span class="hw-meta">${meta}</span>` : ''}</div><span class="row-act">${right}</span></li>`;
  const homeworkBody = homeworks === null ? (dialog.failed.homework ? unavailable() : pendingRows())
    : shixun.length ? `<p class="muted sheet-note">只处理必做关卡，做完的会跳过。</p><ul class="plain">${shixun.map(homework => {
      const complete = homeworkComplete(homework);
      const running = homeworkPending(course.identifier, homework.id);
      const past = !complete && homeworkPastDeadline(homework);
      const mark = complete ? `<span class="mark done">${icon('tick')}</span>`
        : running ? '<span class="mark"><span class="pulse"></span></span>'
          : past ? '<span class="mark todo"></span>' : `<span class="mark todo">${icon('ring')}</span>`;
      const known = homework.requiredProgressKnown === true && homework.requiredChallengeCount !== undefined;
      // Unknown required progress stays unknown: the classroom's own level
      // total is not evidence of what still has to be done. The status word
      // belongs to the right-hand column, so the meta line never repeats it.
      const levels = known ? `${count(homework.finishedRequiredChallengeCount)} / ${count(homework.requiredChallengeCount)} 关` : '';
      const due = deadline(homework.deadline);
      const meta = [levels, due ? `${e(due)} ${past ? '已截止' : '截止'}` : ''].filter(Boolean).join(' · ');
      const label = complete ? '<span class="state done">已完成</span>'
        : running ? '<span class="state run">正在处理</span>'
          : past ? '<span class="state muted">已截止</span>' : known ? '<span class="state">待完成</span>' : '<span class="state">关卡待核验</span>';
      const action = homeworkManualEligible(course, homework)
        ? `<button class="btn sm" data-homework-submit="${homework.id}" ${running || state.creating || !accessible() ? 'disabled' : ''}>${running ? '处理中' : '只做这份'}</button>` : '';
      return row(mark, e(homework.name), meta, `${label}${action}`, past ? 'past' : '');
    }).join('')}</ul>` : '<div class="empty"><h3>没有课程实验</h3><p>这门课现在没有要处理的实验。</p></div>';
  const commonBody = homeworks === null ? (dialog.failed.homework ? unavailable() : pendingRows())
    : common.length ? `<p class="muted sheet-note">图文作业只看状态，不代做；提交后由头歌老师批阅。</p><ul class="plain">${common.map(homework => {
      const submitted = homework.submissionStatus === 'submitted';
      const mark = submitted ? `<span class="mark done">${icon('tick')}</span>` : `<span class="mark todo">${icon('ring')}</span>`;
      const due = deadline(homework.deadline);
      const meta = [homework.status ? e(homework.status) : '', due ? `${e(due)} 截止` : ''].filter(Boolean).join(' · ');
      const label = submitted ? '<span class="state done">已提交</span>' : homework.submissionStatus === 'not_submitted' ? '<span class="state">待提交</span>' : '<span class="state muted">状态待确认</span>';
      return row(mark, e(homework.name), meta, `${label}${commonHomeworkLink(course, homework)}`);
    }).join('')}</ul>` : '<div class="empty"><h3>没有图文作业</h3><p>这门课现在没有图文作业。</p></div>';
  const examBody = exercises === null ? (dialog.failed.exercise ? unavailable() : pendingRows())
    : exercises.length ? `<p class="muted sheet-note">在线考试要单独开始，不包含在「完成全部作业」里。</p><ul class="plain">${exercises.map(exercise => {
      const status = exerciseStatus(exercise);
      const mark = status.tone === 'completed' ? `<span class="mark done">${icon('tick')}</span>` : status.tone === 'waiting' ? `<span class="mark warn">${icon('bang')}</span>` : `<span class="mark todo">${icon('ring')}</span>`;
      const due = deadline(exercise.deadline);
      const meta = [exercise.totalQuestions ? `${exercise.totalQuestions} 道题` : '', due ? `${e(due)} 截止` : '', e(exerciseReason(exercise))].filter(Boolean).join(' · ');
      const action = exercise.submitted || !exercise.available || exercise.actionRequired === true ? exerciseLink(course, exercise)
        : `<button class="btn sm" data-exam-submit="${exercise.id}">开始</button>${exerciseLink(course, exercise)}`;
      return row(mark, e(exercise.name), meta, `<span class="state ${status.tone === 'completed' ? 'done' : ''}">${status.label}</span>${action}`);
    }).join('')}</ul>` : '<div class="empty"><h3>没有在线考试</h3><p>这门课现在没有可查看的考试。</p></div>';
  const refreshing = dialog.loading.homework || dialog.loading.common || dialog.loading.exercise;
  const hasData = tab === 'exercise' ? exercises !== null : homeworks !== null;
  const refreshed = dialog.updatedAt[tab] ? new Date(dialog.updatedAt[tab]).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false }) : '';
  const sync = dialog.loading[tab] ? (hasData ? '正在更新…' : '') : dialog.failed[tab] && hasData ? '没更新上，还是上次的结果' : refreshed ? `${refreshed} 同步` : '';
  const verdict = summary.ready
    ? summary.runnableCount ? `还差 <b>${summary.runnableCount} 份</b>必做作业` : shixun.length ? '<b>必做作业都完成了</b>' : '没有必做作业'
    : '正在核对作业';
  const meta = [course.teacherName || '', course.studentCount === undefined ? '' : `${cn(course.studentCount)} 人`].filter(Boolean).join(' · ');
  const tabs: Array<[DetailTab, string, number | null]> = [['homework', '课程实验', homeworks === null ? null : shixun.length], ['common', '图文作业', homeworks === null ? null : common.length], ['exercise', '在线考试', exercises === null ? null : exercises.length]];
  return `<header class="sheet-head"><div class="eyebrow">课程</div>
    <h2 id="modal-title">${e(course.name)}</h2>
    <p class="verdict">${meta ? `${e(meta)} · ` : ''}${verdict}</p></header>
    <div class="folder">
      <div class="folder-tabs" role="tablist">${tabs.map(([value, label, n]) => `<button class="folder-tab ${tab === value ? 'on' : ''}" data-detail-tab="${value}" role="tab" aria-selected="${tab === value}">${label}${n === null ? '' : `<span class="n num">${n}</span>`}</button>`).join('')}</div>
      <div class="folder-body"><div class="folder-sync"><span role="status">${e(sync)}</span><button class="link" data-action="detail-refresh" ${refreshing ? 'disabled' : ''}>${icon('refresh')}刷新</button></div></div>
    </div>
    <div class="sheet-body">${tab === 'homework' ? homeworkBody : tab === 'common' ? commonBody : examBody}</div>
    <footer class="sheet-foot"><button class="btn" data-action="close-modal">关闭</button>${summary.ready && summary.runnableCount > 0 && courseSelectable(course.identifier) && accessible() ? `<button class="btn red" data-course-start="${e(course.identifier)}" ${state.creating ? 'disabled' : ''}>完成这门课的作业</button>` : ''}</footer>`;
}
/**
 * focusKey identifies a control across a re-render. The page is repainted by
 * replacing its markup, so the focused node is a different object afterwards;
 * without a stable key a keyboard user is dropped onto <body> every poll and
 * cannot hold a button such as "stop" or "retry" on a running job.
 */
function focusKey(element: Element | null): string {
  const el = element as HTMLElement | null;
  if (!el || el === document.body || !el.tagName) return '';
  if (el.id) return `id:${el.id}`;
  const data = el.dataset;
  if (!data) return '';
  for (const name of Object.keys(data)) {
    const value = data[name];
    if (value !== undefined) return `${el.tagName}:${name}=${value}`;
  }
  return '';
}
function findByFocusKey(key: string): HTMLElement | null {
  if (!key) return null;
  const candidates = document.querySelectorAll<HTMLElement>('button, input, textarea, select, a[href], summary, [tabindex]');
  for (const el of candidates) {
    if (focusKey(el) === key) return el;
  }
  return null;
}
interface FocusSnapshot {
  key: string;
  start: number | null;
  end: number | null;
  modalScroll: number;
  pageScroll: number;
  historyOpen: boolean;
}
function captureFocus(): FocusSnapshot {
  const active = document.activeElement as HTMLInputElement | null;
  const text = active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA');
  return {
    key: focusKey(active),
    start: text ? active!.selectionStart : null,
    end: text ? active!.selectionEnd : null,
    modalScroll: document.querySelector<HTMLElement>('.modal')?.scrollTop ?? 0,
    pageScroll: typeof window.scrollY === 'number' ? window.scrollY : 0,
    historyOpen: document.querySelector<HTMLDetailsElement>('[data-service-history]')?.open ?? false,
  };
}
function restoreFocus(snapshot: FocusSnapshot): void {
  const modal = document.querySelector<HTMLElement>('.modal');
  if (modal && snapshot.modalScroll) modal.scrollTop = snapshot.modalScroll;
  if (snapshot.pageScroll && typeof window.scrollTo === 'function') window.scrollTo({ top: snapshot.pageScroll });
  const history = document.querySelector<HTMLDetailsElement>('[data-service-history]');
  if (history && snapshot.historyOpen) history.open = true;
  const target = findByFocusKey(snapshot.key);
  if (!target || target === document.activeElement || (target as HTMLButtonElement).disabled || typeof target.focus !== 'function') return;
  target.focus({ preventScroll: true });
  if (snapshot.start !== null && snapshot.end !== null && typeof (target as HTMLInputElement).setSelectionRange === 'function') {
    (target as HTMLInputElement).setSelectionRange(snapshot.start, snapshot.end);
  }
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
  const draft = !state.user ? {
    login: document.querySelector<HTMLInputElement>('#login')?.value ?? '',
    password: document.querySelector<HTMLInputElement>('#password')?.value ?? '',
    code: document.querySelector<HTMLInputElement>('#sms-code')?.value ?? '',
  } : null;
  const createDraft = state.user ? captureTicketForm('#ticket-create-form') : null;
  const replyDraft = state.user ? captureTicketForm('#ticket-reply-form') : null;
  if (createDraft) ticketFormDrafts.set('create:user', createDraft);
  if (replyDraft) ticketFormDrafts.set(`reply:${replyDraft.scope}:${replyDraft.id}`, replyDraft);
  // A record opens as a side sheet; a question or a reading opens as a centred
  // slip. The entry animation is marked only on the paint that opens it, so a
  // live job repainting every second does not re-slide the sheet.
  const sheet = ['job', 'admin-job', 'course', 'ticket'].includes(state.modalKind);
  const entering = Boolean(state.modal) && !modalWasOpen;
  modalWasOpen = Boolean(state.modal);
  const html = `${renderAssetUpdateNotice()}${state.user ? renderShell() : renderLogin()}${state.modal ? `<div class="modal-backdrop${sheet ? '' : ' centered'}"><section class="modal${sheet ? '' : ' dialog'}${state.modalKind === 'guidelines' ? ' wide' : ''}${entering ? ' enter' : ''}" data-ticket-scope="${state.modalKind === 'ticket' && ticketDialog ? ticketDialog.admin ? 'admin' : 'user' : ''}" role="dialog" aria-modal="true" aria-labelledby="modal-title" tabindex="-1"><button class="btn ghost square modal-close" type="button" data-action="close-modal" aria-label="关闭">${icon('close')}</button><div class="modal-content">${state.modal}</div></section></div>` : ''}`;
  // Polling re-renders the page every second or two. Replacing identical
  // markup would drop focus, text selection and in-flight clicks for nothing,
  // so an unchanged render is skipped entirely.
  if (html === renderedHTML && root!.firstChild) return;
  // Replacing the markup discards the focused node, the caret and both scroll
  // positions. Carry them across so a live-updating page stays usable with a
  // keyboard and does not scroll itself back to the top every poll.
  const focus = captureFocus();
  renderedHTML = html;
  root!.innerHTML = html;
  restoreFocus(focus);
  if (draft) {
    const account = document.querySelector<HTMLInputElement>('#login');
    const password = document.querySelector<HTMLInputElement>('#password');
    const code = document.querySelector<HTMLInputElement>('#sms-code');
    if (account) account.value = draft.login;
    if (password) password.value = draft.password;
    if (code) code.value = draft.code;
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
/** renderLive repaints for background data, never mid-click. */
function renderLive(): void {
  if (pointerHeld && Date.now() - pointerHeldAt < pointerHoldLimit) { deferredRender = true; return; }
  pointerHeld = false;
  deferredRender = false;
  render();
}
function releasePointer(): void {
  pointerHeld = false;
  if (deferredRender) { deferredRender = false; render(); }
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
  state.modal = ''; state.modalKind = ''; state.modalAction = null; modalRevision++; courseDialog = null; ticketDialog = null; jobDialog = null; adminJobDialog = null; adminJobDetails.clear(); render();
  scheduleFocusedJob();
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
/**
 * focusedJobID is the one job whose stage the user is actually watching: the
 * open detail dialog, or the single running job on the progress page. Only
 * that job is polled closely; the list keeps its own slower cadence.
 */
function focusedJobID(): string {
  if (jobDialog && state.modalKind === 'job' && !jobDialog.historical) {
    const job = state.jobs.find(item => item.id === jobDialog?.id);
    if (job && pendingJob(job)) return job.id;
  }
  if (state.modal || state.page !== 'progress') return '';
  const active = consumerCurrentJobs().filter(pendingJob);
  return active.length === 1 ? active[0]!.id : '';
}
function stopFocusPoll(): void { if (focusTimer !== undefined) clearTimeout(focusTimer); focusTimer = undefined; }
/**
 * scheduleFocusedJob refreshes one job about once a second. The server answers
 * a single running job from the job manager's memory, so this costs one small
 * request and no extra database read or write.
 */
function scheduleFocusedJob(): void {
  stopFocusPoll(); if (!state.user) return;
  const id = focusedJobID();
  if (!id) return;
  focusTimer = setTimeout(() => {
    focusTimer = undefined;
    if (document.visibilityState === 'hidden') { scheduleFocusedJob(); return; }
    void loadJobDetail(id);
  }, 1200);
}
async function loadJobDetail(id: string): Promise<void> {
  if (!state.user || !id) return;
  const session = generation;
  try {
    const job = await api.job(id);
    if (session !== generation || !job || job.id !== id) return;
    jobDetails.set(id, job);
    // Keep only the job being watched; a stale detail must never outlive it.
    for (const key of [...jobDetails.keys()]) if (key !== id) jobDetails.delete(key);
    state.jobs = state.jobs.map(item => item.id === id ? job : item);
    if (jobDialog && state.modalKind === 'job' && jobDialog.id === id) {
      state.modal = jobDetailContent(job, jobDialog.historical);
      renderLive();
    } else if (!state.modal) renderLive();
  } catch (error) {
    if (session !== generation || handleError(error)) return;
    // A failed detail poll is not an error state: the list poll still updates.
  }
  if (session === generation) scheduleFocusedJob();
}
async function loadJobs(): Promise<void> {
  if (!state.user) return; const revision = ++jobsRevision; const session = generation;
  try {
    const result = await api.jobs();
    if (session !== generation || revision !== jobsRevision) return;
    const changed = result.jobs.filter(job => serviceJob(job) && !pendingJob(job) && !state.jobs.some(previous => courseTerminalVersion(previous) === courseTerminalVersion(job)));
    // The list projection omits per-level detail. Keep the detailed copy of the
    // watched job while it is at least as fresh as the row from the list.
    state.jobs = result.jobs.map(job => {
      const detail = jobDetails.get(job.id);
      if (!detail) return job;
      if (Date.parse(detail.updated_at) >= Date.parse(job.updated_at)) return detail;
      jobDetails.delete(job.id);
      return job;
    });
    const noticedRecovery = state.jobs.find(job => job.id === recoveryNoticeID);
    if (noticedRecovery) state.notice = recoveryNotice(noticedRecovery) || state.notice;
    for (const job of state.jobs) if (jobActions.get(job.id) === 'stopping' && !pendingJob(job)) jobActions.delete(job.id);
    state.selected = state.selected.filter(id => courseSelectable(id));
    if (jobDialog && state.modalKind === 'job') {
      const job = state.jobs.find(item => item.id === jobDialog?.id);
      if (job) state.modal = jobDetailContent(job, jobDialog.historical);
      renderLive();
    } else if (!state.modal) renderLive();
    const refreshed = new Set<string>();
    for (const job of changed) refreshJobCourseCache(job, refreshed);
  } catch (error) {
    if (session !== generation || revision !== jobsRevision) return;
    if (handleError(error)) return;
  }
  scheduleJobs();
  scheduleFocusedJob();
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
function stopWechatPoll(): void {
  if (wechatPollTimer !== undefined) clearTimeout(wechatPollTimer);
  wechatPollTimer = undefined;
}
// A canceled request can still have reached the service. Wait for its start
// response and the server's cancellation before allowing another login to set
// cookies; ignoring a stale UI response alone does not close that race.
function stopWechatAttempt(): Promise<void> {
  wechatRevision++; stopWechatPoll(); wechatPollController?.abort();
  wechatPollController = undefined;
  const starting = wechatStartTask, previousCancel = wechatCancelTask;
  wechatStartTask = undefined; wechatStatus = 'idle'; wechatExpiresAt = 0;
  if (!wechatAttemptActive && !starting && !wechatNeedsCancel) return previousCancel ?? Promise.resolve();
  if (!wechatAttemptActive && !starting && wechatCancelPending) return previousCancel!;
  wechatNeedsCancel = true; wechatAttemptActive = false;
  wechatCancelPending = true;
  let cancel!: Promise<void>;
  cancel = (async () => {
    try {
      await previousCancel?.catch(() => {});
      await starting?.catch(() => {});
      await api.cancelWechatLogin();
      wechatNeedsCancel = false;
    } finally { if (wechatCancelTask === cancel) wechatCancelPending = false; }
  })();
  wechatCancelTask = cancel;
  return cancel;
}
function wechatCurrent(revision: number, session: number): boolean {
  return revision === wechatRevision && session === generation && loginMode === 'wechat' && !state.user;
}
function wechatError(error: unknown): string {
  const code = error instanceof ApiRequestError ? error.code : '';
  const messages: Record<string, string> = {
    WECHAT_BINDING_REQUIRED: '该微信尚未关联可用的头歌账号，请先在头歌官网完成绑定，再重新扫码。',
    WECHAT_RESTRICTED: '头歌暂不允许该账号使用微信登录，请使用账号密码或手机验证码登录。',
    WECHAT_LOGIN_EXPIRED: '二维码已过期，请重新获取后扫码。',
    WECHAT_LOGIN_UNAVAILABLE: '微信登录暂时不可用，请稍后重新获取二维码或选择其他登录方式。',
    WECHAT_LOGIN_FAILED: '微信登录暂未完成，请重新获取二维码。',
    LOGIN_RATE_LIMITED: '获取二维码或登录太频繁，请稍后再试。',
    ACCOUNT_DISABLED: '账号服务暂不可用，请联系服务人员。',
  };
  return messages[code || ''] || '微信登录暂未完成，请重新获取二维码。';
}
function completeLogin(user: User): void {
  stopSmsCooldown(); stopWechatPoll(); wechatPollController?.abort(); wechatPollController = undefined;
  wechatAttemptActive = false; wechatStatus = 'idle'; wechatRevision++;
  state.user = user; state.signingIn = false; state.sendingCode = false; state.loginError = ''; state.loginNotice = '';
  render(); void loadJobs(); void loadCourses(); void loadQuestionBank(); void loadTodaySummary();
}
function scheduleWechatPoll(delay = 1000): void {
  stopWechatPoll();
  if (state.user || loginMode !== 'wechat' || document.visibilityState === 'hidden' || !['waiting', 'scanned'].includes(wechatStatus) || wechatPollController) return;
  wechatPollTimer = setTimeout(() => { wechatPollTimer = undefined; void pollWechatLogin(); }, delay);
}
async function pollWechatLogin(): Promise<void> {
  if (state.user || loginMode !== 'wechat' || document.visibilityState === 'hidden' || wechatPollController || !['waiting', 'scanned'].includes(wechatStatus)) return;
  if (Date.now() >= wechatExpiresAt) { wechatStatus = 'expired'; render(); return; }
  const revision = wechatRevision, session = generation, controller = new AbortController();
  wechatPollController = controller;
  try {
    const result = await api.pollWechatLogin(controller.signal);
    if (controller.signal.aborted || !wechatCurrent(revision, session)) return;
    if (result.status === 'succeeded') { completeLogin(result.user); return; }
    wechatStatus = result.status; render();
  } catch (error) {
    if (controller.signal.aborted || !wechatCurrent(revision, session)) return;
    wechatStatus = error instanceof ApiRequestError && error.code === 'WECHAT_LOGIN_EXPIRED' ? 'expired' : 'failed';
    state.loginError = wechatError(error); render();
  } finally {
    if (wechatPollController === controller) wechatPollController = undefined;
    if (wechatCurrent(revision, session)) scheduleWechatPoll();
  }
}
function startWechatLogin(): void {
  if (state.user || loginMode !== 'wechat' || wechatStatus === 'starting') return;
  const canceled = stopWechatAttempt(), revision = wechatRevision, session = generation;
  wechatStatus = 'starting'; state.loginError = ''; state.loginNotice = ''; render();
  let task!: Promise<void>;
  task = (async () => {
    try {
      await canceled;
      if (!wechatCurrent(revision, session)) return;
      wechatAttemptActive = true;
      const result = await api.startWechatLogin();
      if (!wechatCurrent(revision, session)) return;
      wechatExpiresAt = Date.now() + result.expiresInSeconds * 1000;
      wechatImageVersion++; wechatStatus = 'waiting'; render(); scheduleWechatPoll();
    } catch (error) {
      if (!wechatCurrent(revision, session)) return;
      wechatStatus = 'failed'; state.loginError = wechatError(error); render();
    } finally {
      if (wechatStartTask === task) wechatStartTask = undefined;
    }
  })();
  wechatStartTask = task;
}
async function login(event: Event): Promise<void> {
  event.preventDefault(); if (state.signingIn || state.sendingCode) return;
  if (loginMode === 'wechat') return;
  const form = event.currentTarget as HTMLFormElement;
  const value = (name: string): string => (form.elements.namedItem(name) as HTMLInputElement).value;
  const account = value('login').trim();
  const mode = loginMode;
  const password = mode === 'password' ? value('password') : '';
  const code = mode === 'sms' ? value('code').trim() : '';
  if (!account || loginMode === 'password' && !password) { state.loginError = '请输入头歌账号和密码。'; render(); return; }
  if (loginMode === 'sms' && (!/^1\d{10}$/.test(account) || !code)) { state.loginError = '请输入手机号和短信验证码。'; render(); return; }
  if (loginMode === 'password') form.reset();
  else { const codeInput = form.elements.namedItem('code') as HTMLInputElement | null; if (codeInput) codeInput.value = ''; }
  const session = ++generation, revision = ++loginRevision; courseCache.clear(); state.signingIn = true; state.loginError = ''; state.loginNotice = ''; render();
  try {
    if (wechatNeedsCancel) await stopWechatAttempt(); else await wechatCancelTask;
    if (session !== generation || revision !== loginRevision) return;
    const user = mode === 'sms' ? await api.loginWithPhone(account, code) : await api.login(account, password);
    if (session !== generation || revision !== loginRevision) return;
    completeLogin(user);
  } catch (error) {
    if (session !== generation || revision !== loginRevision) return;
    const code = error instanceof ApiRequestError ? error.code : '';
    const messages: Record<string, string> = { AUTH_FAILED: mode === 'sms' ? '手机号或验证码不正确，或验证码已失效。' : '账号或密码不正确，请检查后重新登录。', SMS_BINDING_REQUIRED: '该手机号需要先在头歌官网完成账号绑定，再使用验证码登录。', SMS_RESTRICTED: '头歌暂不允许该账号使用验证码登录，请前往头歌官网处理登录限制。', USER_SUSPENDED: '账号服务暂不可用，请联系服务人员。', ACCOUNT_DISABLED: '账号服务暂不可用，请联系服务人员。', LOGIN_RATE_LIMITED: '登录有点频繁，请稍后再试。' };
    state.signingIn = false; state.loginError = messages[code || ''] || '登录暂未完成，请稍后再试或联系服务人员。'; render();
  }
}
async function sendPhoneCode(): Promise<void> {
  if (state.sendingCode || state.signingIn || loginMode !== 'sms') return;
  const phone = document.querySelector<HTMLInputElement>('#login')?.value.trim() ?? '';
  if (!/^1\d{10}$/.test(phone)) { state.loginError = '请输入 11 位头歌绑定手机号。'; state.loginNotice = ''; render(); return; }
  if (smsCooldownUntil > Date.now()) { scheduleSmsCooldown(); return; }
  const session = generation, revision = loginRevision;
  state.sendingCode = true; state.loginError = ''; state.loginNotice = ''; render();
  try {
    await api.sendPhoneCode(phone);
    if (session !== generation || revision !== loginRevision || loginMode !== 'sms') return;
    smsCooldownUntil = Date.now() + 60_000;
    state.loginNotice = document.querySelector<HTMLInputElement>('#login')?.value.trim() === phone ? '验证码已发送，请查收短信。' : '验证码已发送至之前填写的手机号，请核对后登录。';
  } catch (error) {
    if (session !== generation || revision !== loginRevision || loginMode !== 'sms') return;
    const code = error instanceof ApiRequestError ? error.code : '';
    const uncertain = '验证码发送结果未确认，请先查看短信，稍后再试。';
    const messages: Record<string, string> = { LOGIN_RATE_LIMITED: '获取验证码太频繁，请稍后再试。', PHONE_NOT_REGISTERED: '该手机号尚未关联头歌账号。', SMS_SEND_FAILED: uncertain };
    if (code !== 'LOGIN_RATE_LIMITED' && code !== 'PHONE_NOT_REGISTERED') smsCooldownUntil = Date.now() + 60_000;
    state.loginError = messages[code || ''] || uncertain;
  }
  state.sendingCode = false;
  render(); scheduleSmsCooldown();
}
function changeLoginMode(mode: string): void {
  if (state.signingIn || state.sendingCode || !['password', 'sms', 'wechat'].includes(mode) || mode === loginMode) return;
  loginRevision++; generation++;
  document.querySelector<HTMLFormElement>('#login-form')?.reset();
  if (loginMode === 'wechat') void stopWechatAttempt().catch(() => {});
  loginMode = mode as 'password' | 'sms' | 'wechat';
  state.loginError = ''; state.loginNotice = ''; state.sendingCode = false; passwordVisible = false;
  render(); scheduleSmsCooldown();
  if (loginMode === 'wechat') startWechatLogin();
}
async function logout(): Promise<void> { resetSession(); try { await api.logout(); } catch { /* The local account context has already been cleared. */ } }
function confirmStart(all: boolean): void {
  if (state.creating || !accessible() || all && allServicePending()) return;
  const selected = [...state.selected].filter(id => !coursePending(id));
  if (!all && !selected.length) return;
  const picked = all ? state.courses : state.courses.filter(course => selected.includes(course.identifier));
  let pending = 0, known = true;
  for (const course of picked) {
    const summary = homeworkSummary(course);
    if (summary.ready) pending += summary.runnableCount; else known = false;
  }
  showModal(`<h2 id="modal-title">${all ? '完成全部作业？' : `完成这 ${picked.length} 门课的作业？`}</h2>
    <p class="subtext">${known && pending ? `共 ${pending} 份必做作业。` : ''}只做必做关卡，做完的和选做的会跳过。</p>
    <ul class="dialog-list">${picked.map(course => `<li><span class="tick">${icon('tick')}</span>${e(course.name)}</li>`).join('')}</ul>
    <p class="dialog-note">不用守着，关掉页面也会接着跑。进度随时在「完成进度」里看。</p>
    <div class="dialog-actions"><button class="btn" data-action="close-modal">再看看</button><button class="btn red" data-action="modal-confirm">开始</button></div>`, () => startService(all, selected));
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
  // The sheet supplies its own labelled heading and its own head/body/foot
  // structure; an extra wrapper would break the dialog's flex layout.
  return renderServiceDetail(job, state.courses, archived, jobActions.get(job.id));
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
  showModal(`<h2 id="modal-title">${recover ? '复核后继续？' : retry ? '现在重试？' : '停止这个任务？'}</h2>
    <p class="subtext">${e(jobTitle(job, state.courses))}</p>
    <p class="dialog-note">${recover ? '先核对上次做到哪，再接着做剩下的必做内容。已通过的关卡会跳过，结果不明确时保留进度，不重复提交。' : retry ? '接着做原来范围里没完成的必做内容。已通过的关卡会跳过，选做的不做。' : '做好的结果保留，后面的不再做。等任务确认停下来，之后可以重新开始。'}</p>
    <div class="dialog-actions"><button class="btn" data-action="close-modal">再看看</button><button class="btn red" data-action="modal-confirm">${recover ? '复核并继续' : retry ? '重试' : '停止'}</button></div>`, () => performJobAction(id, action));
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
  // The operator sheet supplies its own labelled heading and head/body/foot
  // structure; an extra wrapper would break the dialog's flex layout.
  return renderAdminJobDetail(job, archived, adminJobActions.get(job.id));
}
function confirmAdminJobAction(id: string, action: AdminJobAction): void {
  const job = currentAdminJob(id, action);
  if (!job) return;
  const label = action === 'retry' ? '重新执行' : action === 'recover' ? '复核并继续' : '停止';
  const note = action === 'retry' ? '用该用户当前有效的登录连接，继续处理原任务范围里没完成的必做内容。已核验通过的关卡自动跳过，选做内容不处理。' : action === 'recover' ? '用该用户当前有效的登录连接，先核对上次提交状态，再继续原范围里剩下的内容。复核不通过时不会发新的提交。' : '保留已完成的结果，停止后续处理。等任务确认停止后再重新执行。';
  showModal(`<h2 id="modal-title">${label}该用户的任务？</h2><p class="subtext">${e(adminJobTarget(job))}</p>
    <p class="dialog-note">${note}此操作记录到管理审计。</p>
    <div class="dialog-actions"><button class="btn" data-action="close-modal">取消</button><button class="btn red" data-action="modal-confirm">确认${label}</button></div>`, () => performAdminJobAction(id, action));
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
  showModal(`<h2 id="modal-title">开始这场考试？</h2><p class="subtext">${e(exercise.name)}</p>
    <p class="dialog-note">只做这一场考试，做完会提交答案并核对交卷状态。需要在头歌处理的入场事项会单独提示。</p>
    <div class="dialog-actions"><button class="btn" data-action="close-modal">取消</button><button class="btn red" data-action="modal-confirm">开始</button></div>`, async () => {
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
function confirmHomework(course: Course, homework: Homework): void {
  if (state.creating || !accessible() || !homeworkManualEligible(course, homework) || homeworkPending(course.identifier, homework.id)) return;
  showModal(`<h2 id="modal-title">只做这一份？</h2><p class="subtext">${e(course.name)} · ${e(homework.name)}</p>
    <p class="dialog-note">只做这份作业里没完成的必做关卡，不动整门课。已通过的关卡会跳过；上次提交结果还不明确时，先核验，不重复提交。</p>
    <div class="dialog-actions"><button class="btn" data-action="close-modal">再看看</button><button class="btn red" data-action="modal-confirm">开始</button></div>`, () => startHomework(course.identifier, homework.id));
}
async function startHomework(courseID: string, homeworkID: number): Promise<void> {
  const course = state.courses.find(item => item.identifier === courseID);
  const homework = courseCache.peekHomeworks(courseID)?.data.find(item => item.id === homeworkID);
  if (state.creating || !accessible() || !course || !homework || !homeworkManualEligible(course, homework) || homeworkPending(courseID, homeworkID)) return;
  const target = JSON.stringify([courseID, homeworkID]);
  const key = homeworkRetryKeys.get(target) ?? crypto.randomUUID();
  homeworkRetryKeys.set(target, key);
  const session = generation; jobsRevision++; stopPoll(); state.creating = true; closeModal();
  try {
    const response = await api.createJob({ scope: 'homework', course: courseID, homework_id: homeworkID, mode: 'submit', idempotency_key: key });
    if (session !== generation) return;
    if (!response || typeof response.id !== 'string' || !response.id || typeof response.status !== 'string') throw new Error('Invalid homework job response');
    homeworkRetryKeys.delete(target);
    state.jobs = [response, ...state.jobs.filter(job => job.id !== response.id)]; state.page = 'progress';
    state.notice = '单份作业任务已创建，已完成关卡会自动跳过，完成进度会自动更新。'; closeModal();
  } catch (error) {
    if (session !== generation || handleError(error)) return;
    state.notice = '尚未确认单份作业任务，请先查看完成进度后重试；重复操作会核对同一请求。';
  } finally {
    if (session === generation) { state.creating = false; render(); void loadJobs(); }
  }
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
  state.modal = `${screen.detailError ? `<div class="form-feedback" role="alert">${e(screen.detailError)}</div>` : ''}${detail ? renderTicketDetail(detail, dialog.admin, screen.submitting) : `<header class="sheet-head"><div class="eyebrow">问题反馈</div><h2 id="modal-title">反馈详情</h2><p class="verdict" role="status">${screen.detailLoading ? '正在读取…' : '没读到这条反馈，刷新再看。'}</p></header><div class="sheet-body"></div><footer class="sheet-foot"><button class="btn" data-ticket-detail-refresh="${e(dialog.id)}" ${screen.detailLoading ? 'disabled' : ''}>${icon('refresh')}刷新</button><button class="btn" data-action="close-modal">关闭</button></footer>`}`;
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
  showModal('<header class="sheet-head"><div class="eyebrow">问题反馈</div><h2 id="modal-title">反馈详情</h2><p class="verdict" role="status">正在读取…</p></header><div class="sheet-body"></div><footer class="sheet-foot"><button class="btn" data-action="close-modal">关闭</button></footer>', null, 'ticket');
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
/**
 * loadAdminJobDetail fetches one job's full projection, including the level
 * rows the operator list leaves out, and refreshes the open dialog with it.
 */
async function loadAdminJobDetail(id: string): Promise<void> {
  if (!isAdmin() || !id) return;
  const session = generation;
  try {
    const job = await api.adminJob(id);
    if (session !== generation || !job || job.id !== id) return;
    adminJobDetails.set(id, job);
    for (const key of [...adminJobDetails.keys()]) if (key !== id) adminJobDetails.delete(key);
    state.admin.jobs = state.admin.jobs.map(item => item.id === id ? job : item);
    if (adminJobDialog && state.modalKind === 'admin-job' && adminJobDialog.id === id) {
      state.modal = adminJobDetailContent(job, adminJobDialog.historical);
      renderLive();
    }
  } catch (error) {
    if (session !== generation) return;
    handleError(error);
  }
}
function stopAdminPoll(): void { if (adminTimer !== undefined) clearTimeout(adminTimer); adminTimer = undefined; }
/**
 * scheduleAdmin refreshes the operator pages while work is in flight. The
 * admin endpoints are heavier than the consumer list and only a few operators
 * are signed in, so this stays well above the consumer cadence and stops
 * entirely once nothing is queued or running.
 */
function scheduleAdmin(): void {
  stopAdminPoll();
  if (!state.user || !isAdmin() || !state.page.startsWith('admin-') || state.page === 'admin-support') return;
  // Running jobs refresh every 6 s. An idle page still refreshes every 20 s so
  // a job a user has just started shows up without a manual refresh; these
  // reads stay on this server and never reach Educoder.
  const active = state.admin.jobs.some(job => ['queued', 'running'].includes(job.status));
  adminTimer = setTimeout(() => {
    adminTimer = undefined;
    if (document.visibilityState === 'hidden') { scheduleAdmin(); return; }
    void loadAdmin(true);
  }, active ? 6000 : 20000);
}
async function loadAdmin(background = false): Promise<void> {
  if (!isAdmin()) return; const session = generation; const revision = ++adminRevision;
  stopAdminPoll();
  // A background refresh keeps the rendered page in place: a spinner every few
  // seconds would make the operator view feel broken rather than live.
  if (!background) { state.admin.loading = true; state.admin.error = false; render(); }
  try {
    const [overview, users, jobs, audit] = await Promise.all([api.adminOverview(), api.adminUsers(), api.adminJobs(), api.adminAudit()]);
    if (session !== generation || revision !== adminRevision) return;
    // Substitute the detailed copy of the job being watched before anything
    // renders from this list; its replacement is requested right after.
    // Only while its dialog is open: once closed, the list row is the fresher
    // record, and a kept copy would freeze that job in the list.
    Object.assign(state.admin, { overview, users, jobs: jobs.map(job => adminJobDialog?.id === job.id ? adminJobDetails.get(job.id) ?? job : job), audit, loading: false, error: false });
    if (adminJobDialog) void loadAdminJobDetail(adminJobDialog.id);
    if (adminJobDialog && state.modalKind === 'admin-job') {
      const job = state.admin.jobs.find(item => item.id === adminJobDialog?.id);
      if (job) state.modal = adminJobDetailContent(job, adminJobDialog.historical);
    }
    if (background) renderLive(); else render();
  } catch (error) {
    if (session !== generation || revision !== adminRevision) return;
    if (handleError(error)) return;
    if (error instanceof ApiRequestError && error.status === 403) { state.page = 'courses'; state.notice = '当前账号未获管理授权。'; }
    state.admin.loading = false; state.admin.error = !background; render();
  }
  if (session === generation) scheduleAdmin();
}
function navigate(page: string): void {
  if (!['courses', 'progress', 'support', 'admin-overview', 'admin-users', 'admin-operations', 'admin-audit', 'admin-support'].includes(page)) return;
  if (page.startsWith('admin-') && !isAdmin()) return;
  if (state.modal) closeModal();
  state.page = page as Page; state.notice = ''; render();
  revalidateVisibleCourseWork(); scheduleFocusedJob();
  if (page === 'support' || page === 'admin-support') { stopAdminPoll(); void loadTickets(page === 'admin-support'); }
  else if (page.startsWith('admin-')) void loadAdmin(); else { stopAdminPoll(); if (page === 'progress') void loadJobs(); }
}

function resumeVisibleCourseWork(): void {
  if (!state.user || document.visibilityState === 'hidden') return;
  lastInputAt = Date.now();
  revalidateVisibleCourseWork();
  stopPoll(); void loadJobs(); void loadQuestionBank(); void loadTodaySummary();
  if (state.page.startsWith('admin-')) void loadAdmin(true);
}
document.addEventListener('pointerdown', () => { pointerHeld = true; pointerHeldAt = Date.now(); noteUserInput(); }, true);
for (const event of ['wheel', 'touchstart']) document.addEventListener(event, noteUserInput, { capture: true, passive: true });
for (const event of ['pointerup', 'pointercancel']) document.addEventListener(event, releasePointer, true);
window.addEventListener('blur', releasePointer);
// One handler per event: hiding the page releases a press that will never
// deliver its pointerup, and showing it again resumes the visible work.
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'hidden') { releasePointer(); stopSmsCooldown(); stopWechatPoll(); wechatPollController?.abort(); return; }
  scheduleSmsCooldown(); scheduleWechatPoll();
  resumeVisibleCourseWork();
});
window.addEventListener?.('focus', resumeVisibleCourseWork);
function bindEvents(): void {
  document.querySelector<HTMLFormElement>('#login-form')?.addEventListener('submit', event => { void login(event); });
  document.querySelectorAll<HTMLButtonElement>('[data-auth-mode]').forEach(button => button.addEventListener('click', () => changeLoginMode(button.dataset.authMode ?? '')));
  const imageRevision = wechatRevision;
  document.querySelector<HTMLImageElement>('[data-wechat-image]')?.addEventListener('error', () => {
    if (imageRevision !== wechatRevision || loginMode !== 'wechat' || !['waiting', 'scanned'].includes(wechatStatus)) return;
    stopWechatPoll(); wechatPollController?.abort(); wechatStatus = 'failed';
    state.loginError = '二维码暂未加载，请重新获取二维码。'; render();
  });
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
  document.querySelectorAll<HTMLButtonElement>('[data-course-start]').forEach(button => button.addEventListener('click', () => {
    const id = button.dataset.courseStart!;
    if (!courseSelectable(id)) return;
    state.selected = [...new Set([...state.selected, id])];
    confirmStart(false);
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-detail-tab]').forEach(button => button.addEventListener('click', () => {
    if (!courseDialog) return;
    void switchCourseDetailTab(button.dataset.detailTab === 'exercise' ? 'exercise' : button.dataset.detailTab === 'common' ? 'common' : 'homework');
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-exam-submit]').forEach(button => button.addEventListener('click', () => {
    const exerciseID = Number(button.dataset.examSubmit);
    const exercise = courseDialog?.exercises?.find(item => item.id === exerciseID);
    if (courseDialog && exercise) confirmExercise(courseDialog.course, exercise);
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-homework-submit]').forEach(button => button.addEventListener('click', () => {
    const homework = courseDialog?.homeworks?.find(item => item.id === Number(button.dataset.homeworkSubmit));
    if (courseDialog && homework) confirmHomework(courseDialog.course, homework);
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-job-detail]').forEach(button => button.addEventListener('click', () => {
    const job = state.jobs.find(item => item.id === button.dataset.jobDetail);
    if (job) {
      courseDialog = null; jobDialog = { id: job.id, historical: button.dataset.jobHistory === 'true' };
      showModal(jobDetailContent(job, jobDialog.historical), null, 'job');
      // Fetch the detailed projection at once: the list projection a card is
      // built from has no per-level rows, running or finished.
      void loadJobDetail(job.id);
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
      void loadAdminJobDetail(job.id);
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
    showModal(`<h2 id="modal-title">${status === 'active' ? '恢复' : '暂停'}该用户的服务？</h2><p class="subtext">此操作会记录到管理审计。不会改动用户的头歌账号。</p>
      <div class="dialog-actions"><button class="btn" data-action="close-modal">取消</button><button class="btn red" data-action="modal-confirm">确认${status === 'active' ? '恢复' : '暂停'}</button></div>`, async () => { await adminAction(() => api.adminUserStatus(id, status)); });
  }));
  document.querySelectorAll<HTMLButtonElement>('[data-admin-cancel]').forEach(button => button.addEventListener('click', () => {
    confirmAdminJobAction(button.dataset.adminCancel!, 'cancel');
  }));
  document.querySelectorAll<HTMLElement>('[data-action]').forEach(button => button.addEventListener(button instanceof HTMLInputElement ? 'change' : 'click', () => {
    switch (button.dataset.action) {
      case 'logout': void logout(); break;
      case 'send-phone-code': void sendPhoneCode(); break;
      case 'refresh-wechat': startWechatLogin(); break;
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
      case 'select-clear': state.selected = []; render(); break;
      case 'toggle-password': {
        // Flip the field in place: a re-render here would drop the caret in
        // the middle of typing a password.
        passwordVisible = !passwordVisible;
        const field = document.querySelector<HTMLInputElement>('#password');
        const label = passwordVisible ? '隐藏密码' : '显示密码';
        if (field) {
          const start = field.selectionStart, end = field.selectionEnd;
          field.type = passwordVisible ? 'text' : 'password';
          field.focus();
          if (start !== null && end !== null && typeof field.setSelectionRange === 'function') field.setSelectionRange(start, end);
        }
        button.setAttribute('aria-pressed', String(passwordVisible));
        button.setAttribute('aria-label', label);
        button.setAttribute('title', label);
        button.innerHTML = icon(passwordVisible ? 'eye-off' : 'eye');
        break;
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
  noteUserInput();
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

import { ApiRequestError, BrowserApi, type EducoderApi } from './api.js';
import type { AppState, Course, Homework } from './types.js';

const appRoot = document.querySelector<HTMLDivElement>('#app');
if (!appRoot) throw new Error('缺少 #app 容器');
const root = appRoot;

const api: EducoderApi = new BrowserApi();
const state: AppState = {
  view: 'login', user: null, courses: [], activeCourse: null, homeworks: [], loading: false,
  error: null, taskLoadingId: null, enteredTask: null, drawerOpen: false, filter: '',
};

function escapeHtml(value: unknown): string {
  return String(value ?? '').replace(/[&<>'"]/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' }[char] as string));
}

function initials(name: string): string {
  const clean = name.trim();
  return clean.slice(0, 2) || '同学';
}

function dateLabel(value?: string): string {
  if (!value) return '未设置截止时间';
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : `截止 ${parsed.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })}`;
}

function setState(next: Partial<AppState>): void {
  Object.assign(state, next);
  render();
}

function isExpired(error: unknown): boolean { return error instanceof ApiRequestError && error.isSessionExpired; }

function showError(error: unknown): void {
  if (isExpired(error)) {
    Object.assign(state, { view: 'login', user: null, courses: [], activeCourse: null, homeworks: [], enteredTask: null, drawerOpen: false, error: { code: 'UNAUTHENTICATED', message: '登录已过期，请重新登录' } });
  } else {
    setState({ error: { code: 'UPSTREAM_ERROR', message: error instanceof Error ? error.message : '请求失败，请稍后重试' }, loading: false, taskLoadingId: null });
  }
  render();
}

async function loadCourses(): Promise<void> {
  setState({ loading: true, error: null });
  try {
    const courses = await api.courses();
    setState({ courses, activeCourse: courses[0] ?? null, loading: false, view: 'courses' });
    if (courses[0]) await loadHomeworks(courses[0]);
  } catch (error) { showError(error); }
}

async function loadHomeworks(course: Course): Promise<void> {
  setState({ activeCourse: course, view: 'homeworks', loading: true, error: null, homeworks: [] });
  try { setState({ homeworks: await api.homeworks(course.identifier), loading: false }); }
  catch (error) { showError(error); }
}

async function enterHomework(homework: Homework): Promise<void> {
  if (!state.activeCourse) return;
  setState({ taskLoadingId: homework.id, error: null, drawerOpen: true, enteredTask: null });
  try { setState({ enteredTask: await api.enter(state.activeCourse.identifier, homework.id), taskLoadingId: null }); }
  catch (error) { showError(error); }
}

async function handleLogin(event: Event): Promise<void> {
  event.preventDefault();
  const form = event.currentTarget as HTMLFormElement;
  const login = (form.elements.namedItem('login') as HTMLInputElement).value.trim();
  const password = (form.elements.namedItem('password') as HTMLInputElement).value;
  if (!login || !password) { setState({ error: { code: 'VALIDATION_ERROR', message: '请输入账号和密码' } }); return; }
  setState({ loading: true, error: null });
  try {
    const user = await api.login(login, password);
    form.reset();
    setState({ user, loading: false, error: null });
    await loadCourses();
  } catch (error) { showError(error); }
}

async function restoreSession(): Promise<void> {
  try {
    const user = await api.me();
    setState({ user, error: null });
    await loadCourses();
  } catch { render(); }
}

async function logout(): Promise<void> {
  try { await api.logout(); } catch { /* local state still clears */ }
  Object.assign(state, { view: 'login', user: null, courses: [], activeCourse: null, homeworks: [], enteredTask: null, drawerOpen: false, error: null });
  render();
}

function renderLogin(): string {
  const error = state.error ? `<div class="alert alert-error" role="alert"><span class="alert-dot"></span>${escapeHtml(state.error.message)}</div>` : '';
  return `<main class="auth-page">
    <section class="auth-panel">
      <div class="brand-lockup"><span class="brand-mark">头</span><div><strong>头歌</strong><span>实践教学平台</span></div></div>
      <div class="auth-copy"><p class="eyebrow">EDUCODER STUDY DESK</p><h1>开始你的实践学习</h1><p>登录后查看课堂、作业与实训任务。</p></div>
      ${error}
      <form class="auth-form" id="login-form" autocomplete="off">
        <label for="login">账号</label><input id="login" name="login" type="text" autocomplete="username" placeholder="请输入手机号或用户名" required />
        <label for="password">密码</label><input id="password" name="password" type="password" autocomplete="current-password" placeholder="请输入密码" required />
        <button class="button button-primary button-block" type="submit" ${state.loading ? 'disabled' : ''}>${state.loading ? '<span class="spinner"></span>正在登录…' : '登录'}</button>
      </form>
      <p class="auth-footnote">账号信息仅用于本次登录，会话由服务端安全保管。</p>
    </section>
    <aside class="auth-aside"><div class="aside-grid"></div><div class="aside-content"><span class="aside-kicker">学习工作台</span><h2>把课堂进度<br /><em>掌握在手中</em></h2><p>从课堂到任务，一处查看你的实践学习安排。</p></div><div class="aside-meta">Explore · Practice · Grow</div></aside>
  </main>`;
}

function renderHeader(): string {
  const user = state.user;
  return `<header class="topbar"><div class="topbar-inner"><div class="brand-lockup brand-light"><span class="brand-mark">头</span><div><strong>头歌</strong><span>实践教学平台</span></div></div><nav class="topnav"><button class="nav-item active" data-action="courses">我的课堂</button><button class="nav-item" type="button">学习资源</button></nav><div class="topbar-user"><span class="user-name">${escapeHtml(user?.name ?? '')}</span><span class="avatar">${escapeHtml(initials(user?.name ?? '同学'))}</span><button class="icon-button" title="退出登录" aria-label="退出登录" data-action="logout">↗</button></div></div></header>`;
}

function renderSidebar(): string {
  return `<aside class="sidebar"><div class="side-section"><p class="side-label">学习中心</p><button class="side-link active" data-action="courses"><span class="side-icon">▦</span>我的课堂<span class="side-count">${state.courses.length}</span></button><button class="side-link" type="button"><span class="side-icon">◷</span>学习记录</button><button class="side-link" type="button"><span class="side-icon">☆</span>我的收藏</button></div><div class="side-divider"></div><div class="side-section"><p class="side-label">快捷入口</p><button class="side-link" type="button"><span class="side-icon">?</span>帮助中心</button></div><div class="sidebar-bottom"><div class="help-card"><span class="help-icon">?</span><div><strong>需要帮助？</strong><span>查看使用指南</span></div></div></div></aside>`;
}

function renderCourseCard(course: Course): string {
  const active = state.activeCourse?.identifier === course.identifier;
  return `<button class="course-card ${active ? 'selected' : ''}" data-course="${escapeHtml(course.identifier)}"><span class="course-cover cover-${Math.abs([...course.identifier].reduce((sum, char) => sum + char.charCodeAt(0), 0)) % 5}"><span class="cover-code">${escapeHtml(course.identifier.slice(0, 3))}</span></span><span class="course-info"><strong>${escapeHtml(course.name)}</strong><span>${course.ended ? '已结束' : '进行中'}${course.membersCount ? ` · ${course.membersCount} 人` : ''}</span></span><span class="course-arrow">›</span></button>`;
}

function renderCourseRail(): string {
  return `<section class="course-rail"><div class="section-heading"><div><p class="eyebrow">MY CLASSROOMS</p><h2>我的课堂</h2></div><span class="muted">${state.courses.length} 个课堂</span></div><div class="course-list">${state.courses.length ? state.courses.map(renderCourseCard).join('') : '<div class="empty-mini">还没有课堂</div>'}</div></section>`;
}

function statusClass(status: string): string {
  const value = status.toLowerCase();
  if (value.includes('完成') || value.includes('通过')) return 'status-done';
  if (value.includes('截止') || value.includes('结束')) return 'status-closed';
  return 'status-progress';
}

function renderHomeworkRow(homework: Homework): string {
  const progress = homework.challengeCount ? Math.round(((homework.finishedChallengeCount ?? 0) / homework.challengeCount) * 100) : 0;
  return `<article class="homework-row"><div class="homework-index">${String(homework.id).slice(-2).padStart(2, '0')}</div><div class="homework-main"><h3>${escapeHtml(homework.name)}</h3><div class="homework-meta"><span>${homework.challengeCount ? `${homework.finishedChallengeCount ?? 0}/${homework.challengeCount} 个关卡` : '实训任务'}</span><span>${escapeHtml(dateLabel(homework.deadline))}</span></div>${homework.challengeCount ? `<div class="progress-track"><span style="width:${progress}%"></span></div>` : ''}</div><span class="status-pill ${statusClass(homework.status)}">${escapeHtml(homework.status || '待开始')}</span><button class="button button-outline homework-action" data-homework="${homework.id}" ${state.taskLoadingId === homework.id ? 'disabled' : ''}>${state.taskLoadingId === homework.id ? '<span class="spinner spinner-blue"></span>加载中' : '进入任务'}<span>›</span></button></article>`;
}

function renderTaskDrawer(): string {
  if (!state.drawerOpen) return '';
  const task = state.enteredTask;
  return `<div class="drawer-backdrop" data-action="close-drawer"></div><aside class="task-drawer" role="dialog" aria-modal="true" aria-label="任务详情"><div class="drawer-head"><div><p class="eyebrow">TASK DETAIL</p><h2>任务详情</h2></div><button class="close-button" data-action="close-drawer" aria-label="关闭">×</button></div>${state.taskLoadingId ? '<div class="drawer-loading"><span class="spinner spinner-blue"></span><p>正在打开任务…</p></div>' : task ? `<div class="drawer-body"><div class="task-status-line"><span class="status-pill status-progress">${escapeHtml(task.requiredStatus || '待开始')}</span><span class="muted">任务 ${task.homeworkId}</span></div><h3 class="task-title">${escapeHtml(task.challenge.subject || task.shixun.name || '实训任务')}</h3><p class="task-path">${escapeHtml(task.challenge.path || '按顺序完成实训关卡，提交后查看结果。')}</p><dl class="task-facts"><div><dt>当前关卡</dt><dd>${task.challenge.position ? `第 ${task.challenge.position} 关` : '—'}</dd></div><div><dt>实训名称</dt><dd>${escapeHtml(task.shixun.name || '—')}</dd></div><div><dt>关卡总数</dt><dd>${task.shixun.challengesCount ?? '—'}</dd></div></dl><div class="task-callout"><span>⌁</span><div><strong>准备开始实践</strong><p>进入官方实训页面完成你的代码练习。</p></div></div><button class="button button-primary button-block" data-action="open-official">打开实训任务 <span>↗</span></button><p class="drawer-note">任务页面将在新标签页打开，当前课堂进度会保留。</p></div>` : '<div class="drawer-empty"><span class="empty-icon">!</span><p>任务详情暂时无法加载</p></div>'}</aside>`;
}

function renderWorkspace(): string {
  const active = state.activeCourse;
  const tasks = state.filter ? state.homeworks.filter(item => `${item.name} ${item.status}`.toLowerCase().includes(state.filter.toLowerCase())) : state.homeworks;
  return `<div class="app-shell">${renderHeader()}<div class="workspace"><div class="sidebar-wrap">${renderSidebar()}</div><main class="main-content"><div class="mobile-context"><button class="mobile-back" data-action="courses">‹</button><span>${escapeHtml(active?.name ?? '我的课堂')}</span></div>${renderCourseRail()}<section class="task-section"><div class="section-heading task-heading"><div><p class="eyebrow">${active ? 'CLASSROOM TASKS' : 'SELECT A CLASSROOM'}</p><h2>${escapeHtml(active?.name ?? '选择一个课堂')}</h2></div>${active ? `<label class="search-box"><span>⌕</span><input data-role="filter" value="${escapeHtml(state.filter)}" placeholder="搜索任务" aria-label="搜索任务" /></label>` : ''}</div>${state.loading ? '<div class="state-panel"><span class="spinner spinner-blue"></span><p>正在加载课堂内容…</p></div>' : !active ? '<div class="state-panel"><span class="state-icon">▦</span><p>从左侧选择一个课堂查看任务</p></div>' : !tasks.length ? `<div class="state-panel"><span class="state-icon">✓</span><h3>${state.filter ? '没有找到相关任务' : '这个课堂还没有任务'}</h3><p>${state.filter ? '换个关键词试试。' : '任务发布后会显示在这里。'}</p></div>` : `<div class="homework-list">${tasks.map(renderHomeworkRow).join('')}</div>`}</section>${state.error ? `<div class="alert alert-error floating-alert" role="alert"><span class="alert-dot"></span>${escapeHtml(state.error.message)}<button data-action="dismiss-error" aria-label="关闭提示">×</button></div>` : ''}</main></div>${renderTaskDrawer()}</div>`;
}

function render(): void {
  root.innerHTML = state.view === 'login' ? renderLogin() : renderWorkspace();
  bindEvents();
}

function bindEvents(): void {
  document.querySelector<HTMLFormElement>('#login-form')?.addEventListener('submit', event => { void handleLogin(event); });
  document.querySelectorAll<HTMLElement>('[data-course]').forEach(element => element.addEventListener('click', () => {
    const course = state.courses.find(item => item.identifier === element.dataset.course);
    if (course) void loadHomeworks(course);
  }));
  document.querySelectorAll<HTMLElement>('[data-homework]').forEach(element => element.addEventListener('click', () => {
    const homework = state.homeworks.find(item => item.id === Number(element.dataset.homework));
    if (homework) void enterHomework(homework);
  }));
  document.querySelectorAll<HTMLElement>('[data-action="logout"]').forEach(element => element.addEventListener('click', () => { void logout(); }));
  document.querySelectorAll<HTMLElement>('[data-action="courses"]').forEach(element => element.addEventListener('click', () => {
    setState({ view: 'homeworks', activeCourse: state.activeCourse, error: null });
  }));
  document.querySelectorAll<HTMLElement>('[data-action="close-drawer"]').forEach(element => element.addEventListener('click', () => setState({ drawerOpen: false })));
  document.querySelectorAll<HTMLElement>('[data-action="dismiss-error"]').forEach(element => element.addEventListener('click', () => setState({ error: null })));
  document.querySelectorAll<HTMLElement>('[data-action="open-official"]').forEach(element => element.addEventListener('click', () => {
    const target = state.enteredTask?.url;
    if (target) window.open(target, '_blank', 'noopener,noreferrer');
    else window.open('https://www.educoder.net', '_blank', 'noopener,noreferrer');
  }));
  const filter = document.querySelector<HTMLInputElement>('[data-role="filter"]');
  filter?.addEventListener('input', event => { state.filter = (event.target as HTMLInputElement).value; render(); const next = document.querySelector<HTMLInputElement>('[data-role="filter"]'); next?.focus(); next?.setSelectionRange(state.filter.length, state.filter.length); });
}

render();
void restoreSession();

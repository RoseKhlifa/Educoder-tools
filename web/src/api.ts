import type {
  ApiError,
  AdminUser, AdminAudit, AdminJob, AdminOverview,
  AnswerJob,
  Course,
  Exercise,
  EnteredTask,
  Homework,
  JobList,
  JobRequest,
  User,
} from './types.js';

export class ApiRequestError extends Error {
  readonly code?: string;
  readonly status: number;
  readonly requestId?: string;

  constructor(message: string, status: number, code?: string, requestId?: string) {
    super(message);
    this.name = 'ApiRequestError';
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }

  get isSessionExpired(): boolean {
    if (this.code === 'AUTH_FAILED') return false;
    return this.status === 401 || this.code === 'UNAUTHENTICATED' || this.code === 'UPSTREAM_AUTH_EXPIRED';
  }
}

type Raw = Record<string, unknown>;

function asRecord(value: unknown): Raw {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Raw : {};
}

function text(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : value == null ? fallback : String(value);
}

function number(value: unknown): number | undefined {
  const result = typeof value === 'number' ? value : Number(value);
  return Number.isFinite(result) ? result : undefined;
}

function unwrap<T>(payload: unknown, status: number, requestId?: string): T {
  const root = asRecord(payload);
  const envelope = Object.prototype.hasOwnProperty.call(root, 'data') || Object.prototype.hasOwnProperty.call(root, 'error');
  if (envelope) {
    const error = root.error ? asRecord(root.error) : null;
    if (error) {
      throw new ApiRequestError(text(error.message, '请求失败'), status, text(error.code) || undefined, text(root.requestId) || requestId);
    }
    return root.data as T;
  }
  return payload as T;
}

function errorFromPayload(payload: unknown, status: number, requestId?: string): ApiRequestError {
  const root = asRecord(payload);
  const error = asRecord(root.error);
  return new ApiRequestError(
    text(error.message, status === 401 ? '登录已过期，请重新登录' : '请求失败，请稍后重试'),
    status,
    text(error.code) || undefined,
    text(root.requestId) || requestId,
  );
}

function normalizeUser(value: unknown): User {
  const item = asRecord(value);
  const nested = asRecord(item.user);
  const platform = asRecord(item.platform);
  const source = Object.keys(nested).length ? nested : item;
  const login = text(source.login || source.username || source.user_login);
  const avatarUrl = text(source.avatarUrl);
  return {
    login,
    name: text(source.name || source.real_name || source.username, login || '同学'),
    id: number(source.id || source.user_id),
    role: (platform.role ?? source.role) === 'admin' ? 'admin' : 'user',
    permissions: Array.isArray(source.permissions) ? source.permissions.filter((value): value is string => typeof value === 'string') : [],
    service_access: typeof source.service_access === 'boolean' || typeof source.service_access === 'string' ? source.service_access : undefined,
    status: text(platform.status ?? source.status) || undefined,
    avatarUrl: avatarUrl === '/api/avatar' ? avatarUrl : undefined,
  };
}

function normalizeCourse(value: unknown): Course {
  const item = asRecord(value);
  const url = text(item.first_category_url || item.firstCategoryUrl);
  const urlIdentifier = url.match(/\/classrooms\/([^/]+)/)?.[1];
  return {
    identifier: text(item.identifier || item.course_identifier || urlIdentifier || item.id),
    name: text(item.name || item.course_name, '未命名课堂'),
    homeworkCount: number(item.homeworkCount || item.homework_count || item.homework_commons_count),
    membersCount: number(item.membersCount || item.members_count),
    ended: Boolean(item.ended ?? item.is_end),
  };
}

function normalizeHomework(value: unknown): Homework {
  const item = asRecord(value);
  const statusValue = item.status;
  const status = Array.isArray(statusValue)
    ? statusValue.map(value => text(value)).filter(Boolean).join('、')
    : text(statusValue, text(item.status_text));
  return {
    id: number(item.id || item.homework_id) ?? 0,
    name: text(item.name || item.homework_name, '未命名任务'),
    shixunIdentifier: text(item.shixunIdentifier || item.shixun_identifier) || undefined,
    status,
    challengeCount: number(item.challengeCount || item.challenge_count),
    finishedChallengeCount: number(item.finishedChallengeCount || item.finished_challenge_count),
    requiredChallengeCount: number(item.requiredChallengeCount),
    finishedRequiredChallengeCount: number(item.finishedRequiredChallengeCount),
    requiredProgressKnown: item.requiredProgressKnown === true,
    deadline: text(item.deadline || item.end_time) || undefined,
    canEnter: item.canEnter === undefined && item.is_enter_shixun === undefined
      ? undefined
      : Boolean(item.canEnter ?? item.is_enter_shixun),
  };
}

function normalizeExercise(value: unknown): Exercise {
  const item = asRecord(value);
  const score = typeof item.score === 'string' || typeof item.score === 'number' ? item.score : undefined;
  const maxValue = item.maxScore ?? item.max_score;
  const maxScore = typeof maxValue === 'string' || typeof maxValue === 'number' ? maxValue : undefined;
  return {
    id: number(item.id || item.exercise_id) ?? 0,
    name: text(item.name || item.exercise_name, '未命名考试'),
    status: text(item.status || item.status_text, '待开始'),
    submitted: Boolean(item.submitted ?? item.commit_status === 2),
    scoreKnown: Boolean(item.scoreKnown ?? item.score_known ?? false),
    score,
    maxScore,
    totalQuestions: number(item.totalQuestions || item.total_questions || item.question_count),
    deadline: text(item.deadline || item.end_time) || undefined,
    available: item.available === true,
    actionRequired: item.actionRequired === undefined ? undefined : Boolean(item.actionRequired),
    reasonCode: text(item.reasonCode || item.reason_code) || undefined,
    url: text(item.url) || undefined,
  };
}

function normalizeEnteredTask(value: unknown): EnteredTask {
  const item = asRecord(value);
  const challenge = asRecord(item.challenge);
  const game = asRecord(item.game);
  const shixun = asRecord(item.shixun);
  return {
    courseIdentifier: text(item.courseIdentifier || item.course_identifier),
    kind: item.kind === 'programming' || item.kind === 'choice' ? item.kind : 'unknown',
    homeworkId: number(item.homeworkId || item.homework_id || item.homework_common_id) ?? 0,
    gameCount: number(item.gameCount || item.game_count),
    gameIdentifier: text(item.gameIdentifier || item.game_identifier || game.identifier),
    challenge: {
      id: number(challenge.id),
      subject: text(challenge.subject || challenge.name) || undefined,
      description: text(challenge.description) || undefined,
      position: number(challenge.position),
      path: text(challenge.path) || undefined,
    },
    game: { id: number(game.id), status: number(game.status) },
    shixun: {
      identifier: text(shixun.identifier) || undefined,
      name: text(shixun.name) || undefined,
      challengesCount: number(shixun.challengesCount || shixun.challenges_count),
    },
    myshixunIdentifier: text(item.myshixunIdentifier || item.myshixun_identifier) || undefined,
    nextGame: item.nextGame === null || item.next_game === null ? null : text(item.nextGame || item.next_game) || undefined,
    requiredStatus: text(item.requiredStatus || item.required_status || item.status) || undefined,
    url: text(item.url) || undefined,
  };
}

export interface EducoderApi {
  login(login: string, password: string): Promise<User>;
  me(): Promise<User>;
  logout(): Promise<void>;
  courses(): Promise<Course[]>;
  homeworks(courseIdentifier: string): Promise<Homework[]>;
  exercises(courseIdentifier: string): Promise<{ items: Exercise[]; courseIdentifier?: string }>;
  enter(courseIdentifier: string, homeworkId: number): Promise<EnteredTask>;
  jobs(): Promise<JobList>;
  job(id: string): Promise<AnswerJob>;
  createJob(request: JobRequest): Promise<AnswerJob>;
  cancelJob(id: string): Promise<AnswerJob>;
  adminOverview(): Promise<AdminOverview>;
  adminUsers(): Promise<AdminUser[]>;
  adminJobs(): Promise<AdminJob[]>;
  adminAudit(): Promise<AdminAudit[]>;
  adminUserStatus(id: string | number, status: 'active' | 'disabled'): Promise<void>;
  adminCancelJob(id: string): Promise<void>;
}

export class BrowserApi implements EducoderApi {
  constructor(private readonly baseUrl = '') {}

  private async request<T>(path: string, options: RequestInit = {}): Promise<T> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      ...options,
      credentials: 'include',
      headers: { Accept: 'application/json', ...(options.body ? { 'Content-Type': 'application/json' } : {}), ...options.headers },
    });
    const requestId = response.headers.get('X-Request-Id') ?? undefined;
    let payload: unknown = null;
    try { payload = await response.json(); } catch {
      throw new ApiRequestError('服务返回了无效响应，请稍后重试', response.status, 'INVALID_RESPONSE', requestId);
    }
    if (!response.ok) throw errorFromPayload(payload, response.status, requestId);
    return unwrap<T>(payload, response.status, requestId);
  }

  async login(login: string, password: string): Promise<User> {
    const payload = await this.request<unknown>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ login: login.trim(), password }),
    });
    return normalizeUser(payload);
  }

  async me(): Promise<User> { return normalizeUser(await this.request<unknown>('/api/auth/me')); }

  async logout(): Promise<void> { await this.request<unknown>('/api/auth/logout', { method: 'POST' }); }

  async courses(): Promise<Course[]> {
    const raw = await this.request<unknown>('/api/courses');
    const payload = asRecord(raw);
    const rows = Array.isArray(raw) ? raw : (Array.isArray(payload.courses) ? payload.courses : Array.isArray(payload.items) ? payload.items : []);
    return rows.map(normalizeCourse).filter(course => course.identifier);
  }

  async homeworks(courseIdentifier: string): Promise<Homework[]> {
    const raw = await this.request<unknown>(`/api/courses/${encodeURIComponent(courseIdentifier)}/homeworks`);
    const payload = asRecord(raw);
    const rows = Array.isArray(raw) ? raw : (Array.isArray(payload.homeworks) ? payload.homeworks : Array.isArray(payload.items) ? payload.items : []);
    return rows.map(normalizeHomework).filter(homework => homework.id > 0);
  }

  async exercises(courseIdentifier: string): Promise<{ items: Exercise[]; courseIdentifier?: string }> {
    const raw = await this.request<unknown>(`/api/courses/${encodeURIComponent(courseIdentifier)}/exercises`);
    const payload = asRecord(raw);
    const rows = Array.isArray(raw) ? raw : (Array.isArray(payload.items) ? payload.items : Array.isArray(payload.exercises) ? payload.exercises : []);
    return { items: rows.map(normalizeExercise).filter(exercise => exercise.id > 0), courseIdentifier: text(payload.courseIdentifier || payload.course_identifier) || undefined };
  }

  async enter(courseIdentifier: string, homeworkId: number): Promise<EnteredTask> {
    return normalizeEnteredTask(await this.request<unknown>(`/api/courses/${encodeURIComponent(courseIdentifier)}/homeworks/${encodeURIComponent(String(homeworkId))}/enter`));
  }

  async jobs(): Promise<JobList> { return this.request<JobList>('/api/jobs'); }
  async job(id: string): Promise<AnswerJob> { return this.request<AnswerJob>(`/api/jobs/${encodeURIComponent(id)}`); }
  async createJob(request: JobRequest): Promise<AnswerJob> {
    return this.request<AnswerJob>('/api/jobs', { method: 'POST', body: JSON.stringify(request) });
  }
  async cancelJob(id: string): Promise<AnswerJob> {
    return this.request<AnswerJob>(`/api/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' });
  }

  async adminOverview(): Promise<AdminOverview> { return this.request<AdminOverview>('/api/admin/overview'); }
  async adminUsers(): Promise<AdminUser[]> {
    const value = await this.request<unknown>('/api/admin/users');
    const row = asRecord(value); return (Array.isArray(value) ? value : row.users ?? row.items ?? []) as AdminUser[];
  }
  async adminJobs(): Promise<AdminJob[]> {
    const value = await this.request<unknown>('/api/admin/jobs');
    const row = asRecord(value); return (Array.isArray(value) ? value : row.jobs ?? row.items ?? []) as AdminJob[];
  }
  async adminAudit(): Promise<AdminAudit[]> {
    const value = await this.request<unknown>('/api/admin/audit');
    const row = asRecord(value); return (Array.isArray(value) ? value : row.audit ?? row.events ?? row.items ?? []) as AdminAudit[];
  }
  async adminUserStatus(id: string | number, status: 'active' | 'disabled'): Promise<void> {
    await this.request(`/api/admin/users/${encodeURIComponent(id)}/status`, { method: 'POST', body: JSON.stringify({ status }) });
  }
  async adminCancelJob(id: string): Promise<void> {
    await this.request(`/api/admin/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' });
  }
}

export function toApiError(error: unknown): ApiError {
  if (error instanceof ApiRequestError) return { code: error.code, message: error.message };
  return { code: 'INTERNAL_ERROR', message: error instanceof Error ? error.message : '请求失败，请稍后重试' };
}

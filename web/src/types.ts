export type ApiErrorCode =
  | 'UNAUTHENTICATED'
  | 'UPSTREAM_AUTH_EXPIRED'
  | 'UPSTREAM_ERROR'
  | 'VALIDATION_ERROR'
  | 'INTERNAL_ERROR'
  | string;

export interface ApiError {
  code?: ApiErrorCode;
  message: string;
}

export interface ApiEnvelope<T> {
  data: T | null;
  error: ApiError | null;
  requestId?: string;
}

export interface User {
  login: string;
  name: string;
  id?: number;
  avatarUrl?: string;
  role?: 'admin' | 'user';
  permissions?: string[];
  service_access?: boolean | string;
  status?: string;
}

export interface Course {
  identifier: string;
  name: string;
  homeworkCount?: number;
  membersCount?: number;
  ended?: boolean;
}

export interface Homework {
  id: number;
  name: string;
  shixunIdentifier?: string;
  status: string;
  challengeCount?: number;
  finishedChallengeCount?: number;
  requiredChallengeCount?: number;
  finishedRequiredChallengeCount?: number;
  requiredProgressKnown?: boolean;
  deadline?: string;
  canEnter?: boolean;
}

export interface EnteredTask {
  kind: 'programming' | 'choice' | 'unknown';
  courseIdentifier: string;
  homeworkId: number;
  gameCount?: number;
  gameIdentifier: string;
  challenge: {
    id?: number;
    subject?: string;
    description?: string;
    position?: number;
    path?: string;
  };
  game: { id?: number; status?: number };
  shixun: {
    identifier?: string;
    name?: string;
    challengesCount?: number;
  };
  myshixunIdentifier?: string;
  nextGame?: string | null;
  requiredStatus?: string;
  url?: string;
}

export type View = 'login' | 'courses' | 'homeworks';

export type JobMode = 'generate_only' | 'selftest' | 'submit';
export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'canceled' | 'uncertain' | 'interrupted' | 'waiting' | 'processing' | 'support' | 'completed';
export interface JobRequest {
  course?: string;
  homework_id?: number;
  scope?: 'homework' | 'current' | 'course' | 'all';
  game?: string;
  mode: JobMode;
  idempotency_key?: string;
}
// Login-triggered collection is a read-only job, not an answer execution mode.
export interface CollectionJobRequest {
  mode: 'collect_verified';
  scope?: never;
  course?: '';
  homework_id?: 0;
  game?: '';
  idempotency_key?: string;
}
export interface CollectionSummary {
  courses?: number;
  homeworks?: number;
  levels?: number;
  collected?: number;
  duplicates?: number;
  skipped?: number;
  unconfirmed?: number;
  failed?: number;
  limit_reached?: boolean;
  reasons?: Record<string, number>;
}
export interface AnswerJob {
  id: string;
  status: JobStatus;
  phase: string;
  kind: string;
  request: JobRequest | CollectionJobRequest;
  created_at: string;
  updated_at: string;
  started_at?: string;
  finished_at?: string;
  error_code?: string;
  message?: string;
  progress?: HomeworkProgress;
  result?: Record<string, unknown>;
  events?: Array<{ phase: string; kind?: string; message: string; at: string; progress?: HomeworkProgress }>;
}
export interface HomeworkProgress {
  service_status?: string;
  total_homeworks?: number;
  completed_homeworks?: number;
  total_levels?: number;
  completed_levels?: number;
  skipped_levels?: number;
  failed_levels?: number;
  current_position?: number;
  current_title?: string;
}
export interface JobCapacity {
  workers: number;
  queue_size: number;
  queued: number;
  running: number;
  per_owner_pending: number;
  per_owner_running: number;
  model_concurrency: number;
  model_configured: boolean;
}
export interface JobList { jobs: AnswerJob[]; capacity?: JobCapacity }

export interface AdminUser {
  id: string | number;
  upstream_id?: number;
  name: string;
  login: string;
  role?: string;
  status: string;
  last_login_at?: string;
  updated_at?: string;
}
export interface AdminAudit {
  id?: number;
  actor_id?: number | string;
  action?: string;
  target_id?: string | number;
  created_at?: string;
  outcome?: string;
}
export interface AccessCard { id: string; masked_code?: string; label?: string; status?: string; bound_user_id?: string | number; reusable?: boolean; created_at?: string; expires_at?: string }
export interface AdminJobContext {
  user?: { id?: string | number; name?: string; login?: string };
  course?: { identifier?: string; name?: string };
  homework?: { id?: number; name?: string };
  level?: { title?: string; position?: number; score?: string; max_score?: string };
  scope_label?: string;
  summary?: { reason?: string; reason_code?: string; message?: string; diagnostics?: string[]; progress_label?: string; attention_homeworks?: Array<{ id?: number; name?: string; course_identifier?: string; course_name?: string; reason?: string; reason_code?: string; diagnostics?: string[]; level_title?: string; level_position?: number }> };
}
export interface AdminJob extends AnswerJob { owner_id?: string | number; owner?: string; context?: AdminJobContext }
export type AdminOverview = Record<string, unknown>;

export interface AppState {
  view: View;
  user: User | null;
  courses: Course[];
  activeCourse: Course | null;
  homeworks: Homework[];
  loading: boolean;
  error: ApiError | null;
  taskLoadingId: number | null;
  enteredTask: EnteredTask | null;
  drawerOpen: boolean;
  filter: string;
  jobs: AnswerJob[];
  jobCapacity: JobCapacity | null;
  jobsLoading: boolean;
  jobsError: string | null;
  creatingJob: boolean;
  cancelingJobs: string[];
  jobMode: JobMode;
}

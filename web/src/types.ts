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
export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'canceled' | 'uncertain' | 'interrupted';
export interface JobRequest {
  course: string;
  homework_id: number;
  game?: string;
  mode: JobMode;
  idempotency_key?: string;
}
export interface AnswerJob {
  id: string;
  status: JobStatus;
  phase: string;
  kind: string;
  request: JobRequest;
  created_at: string;
  updated_at: string;
  started_at?: string;
  finished_at?: string;
  error_code?: string;
  message?: string;
  result?: Record<string, unknown>;
  events?: Array<{ phase: string; kind?: string; message: string; at: string }>;
}
export interface JobCapacity {
  workers: number;
  queue_size: number;
  queued: number;
  running: number;
  per_owner_pending: number;
  model_concurrency: number;
  model_configured: boolean;
}
export interface JobList { jobs: AnswerJob[]; capacity: JobCapacity }

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

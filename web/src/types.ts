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
  courseIdentifier: string;
  homeworkId: number;
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
}

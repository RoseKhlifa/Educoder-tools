import type { Exercise, Homework } from './types.js';

export interface CourseDetailsLoaders {
  homeworks: (identifier: string) => Promise<Homework[]>;
  exercises: (identifier: string) => Promise<Exercise[]>;
}

export interface CourseDetailsCacheOptions {
  clock?: () => number;
  ttlMs?: number;
  onChange?: (event: CourseCacheEvent) => void;
}

export interface CourseCacheEvent {
  kind: 'homeworks' | 'exercises';
  identifier: string;
  state: 'loading' | 'success' | 'error';
  updatedAt?: number;
  hasPreviousData: boolean;
}

export interface CachedCourseDetails<T> {
  data: T[];
  updatedAt: number;
  stale: boolean;
}

/** An old session's callers must not receive the next session's course data. */
export class CourseCacheClearedError extends Error {
  constructor() {
    super('Course details session changed');
    this.name = 'CourseCacheClearedError';
  }
}

type Kind = keyof CourseDetailsLoaders;
type Data = Homework[] | Exercise[];
type Lane = 'background' | 'foreground';

interface Entry {
  data: Data;
  updatedAt: number;
}

interface Task {
  key: string;
  identifier: string;
  kind: Kind;
  lane: Lane;
  status: 'queued' | 'running' | 'settled';
  promise: Promise<Data>;
  resolve: (data: Data) => void;
  reject: (reason: unknown) => void;
  refreshPromise?: Promise<Data>;
}

interface Session {
  entries: Map<string, Entry>;
  pending: Map<string, Task>;
  foreground: Task[];
  background: Task[];
  active: number;
  activeBackground: number;
}

const newSession = (): Session => ({
  entries: new Map(), pending: new Map(), foreground: [], background: [],
  active: 0, activeBackground: 0,
});

/** In-memory, session-scoped read cache. Its loaders only retrieve course lists. */
export class CourseDetailsCache {
  private session = newSession();
  private readonly clock: () => number;
  private readonly ttlMs: number;
  private readonly onChange?: (event: CourseCacheEvent) => void;

  constructor(private readonly loaders: CourseDetailsLoaders, options: CourseDetailsCacheOptions = {}) {
    this.clock = options.clock ?? Date.now;
    this.ttlMs = Math.max(0, options.ttlMs ?? 120_000);
    this.onChange = options.onChange;
  }

  getHomeworks(identifier: string, force = false): Promise<Homework[]> {
    return this.get('homeworks', identifier, force, 'foreground') as Promise<Homework[]>;
  }

  /** Revalidate after any read that may have started before a job completed. */
  refreshHomeworksAfterPending(identifier: string): Promise<Homework[]> {
    return this.get('homeworks', identifier, true, 'foreground', true) as Promise<Homework[]>;
  }

  getExercises(identifier: string, force = false): Promise<Exercise[]> {
    return this.get('exercises', identifier, force, 'foreground') as Promise<Exercise[]>;
  }

  peekHomeworks(identifier: string): CachedCourseDetails<Homework> | undefined {
    return this.peek('homeworks', identifier) as CachedCourseDetails<Homework> | undefined;
  }

  peekExercises(identifier: string): CachedCourseDetails<Exercise> | undefined {
    return this.peek('exercises', identifier) as CachedCourseDetails<Exercise> | undefined;
  }

  prefetch(identifiers: string[]): void {
    for (const identifier of new Set(identifiers)) {
      if (!identifier) continue;
      for (const kind of ['homeworks', 'exercises'] as const) {
        // A failed background read leaves the entry absent (or the old data intact).
        void this.get(kind, identifier, false, 'background').catch(() => {});
      }
    }
  }

  clear(): void {
    const previous = this.session;
    this.session = newSession();
    previous.entries.clear();
    previous.foreground.length = 0;
    previous.background.length = 0;
    for (const task of previous.pending.values()) {
      task.status = 'settled';
      task.reject(new CourseCacheClearedError());
    }
    previous.pending.clear();
  }

  private peek(kind: Kind, identifier: string): CachedCourseDetails<Homework | Exercise> | undefined {
    const entry = this.session.entries.get(`${kind}:${identifier}`);
    if (!entry) return undefined;
    return { ...entry, stale: this.clock() - entry.updatedAt >= this.ttlMs };
  }

  private get(kind: Kind, identifier: string, force: boolean, lane: Lane, afterPending = false): Promise<Data> {
    const session = this.session;
    const key = `${kind}:${identifier}`;
    const pending = session.pending.get(key);
    if (pending) {
      if (lane === 'foreground' && pending.status === 'queued' && pending.lane === 'background') {
        pending.lane = 'foreground';
        session.foreground.push(pending);
        this.drain(session);
      }
      if (force && kind === 'homeworks') {
        // A terminal job event must not reuse a forced read started before that
        // event. Ordinary duplicate refresh clicks still share the current read.
        if (afterPending && pending.refreshPromise === pending.promise) pending.refreshPromise = undefined;
        const refresh = (): Promise<Data> => {
          if (session !== this.session) throw new CourseCacheClearedError();
          return this.get(kind, identifier, true, lane);
        };
        pending.refreshPromise ??= pending.promise.then(refresh, refresh);
        return pending.refreshPromise;
      }
      return pending.promise;
    }
    const entry = this.peek(kind, identifier);
    if (!force && entry && !entry.stale) return Promise.resolve(entry.data);

    let resolve!: Task['resolve'];
    let reject!: Task['reject'];
    const promise = new Promise<Data>((done, fail) => { resolve = done; reject = fail; });
    const task: Task = { key, identifier, kind, lane, status: 'queued', promise, resolve, reject, ...(force && kind === 'homeworks' ? { refreshPromise: promise } : {}) };
    session.pending.set(key, task);
    session[lane].push(task);
    this.onChange?.({ kind, identifier, state: 'loading', hasPreviousData: Boolean(entry) });
    this.drain(session);
    return promise;
  }

  private drain(session: Session): void {
    if (session !== this.session) return;
    // Background reads use at most two slots. A third slot remains available to
    // clicks; queued clicks also run before the next background read.
    while (session.active < 3) {
      const foreground = this.next(session.foreground, 'foreground');
      if (foreground) { this.start(session, foreground); continue; }
      if (session.activeBackground >= 2) break;
      const background = this.next(session.background, 'background');
      if (!background) break;
      this.start(session, background);
    }
  }

  private next(queue: Task[], lane: Lane): Task | undefined {
    while (queue.length) {
      const task = queue.shift()!;
      if (task.status === 'queued' && task.lane === lane) return task;
    }
    return undefined;
  }

  private start(session: Session, task: Task): void {
    task.status = 'running';
    session.active++;
    if (task.lane === 'background') session.activeBackground++;
    void Promise.resolve(undefined).then((): Promise<Data> => {
      if (session !== this.session) throw new CourseCacheClearedError();
      const loader = task.kind === 'homeworks' ? this.loaders.homeworks : this.loaders.exercises;
      return loader(task.identifier);
    }).then(
      data => {
        const current = session === this.session;
        const updatedAt = this.clock();
        if (current) {
          session.entries.set(task.key, { data, updatedAt });
          this.onChange?.({ kind: task.kind, identifier: task.identifier, state: 'success', updatedAt, hasPreviousData: true });
        }
        this.finish(session, task);
        if (current) task.resolve(data);
      },
      error => {
        const current = session === this.session;
        if (current) this.onChange?.({ kind: task.kind, identifier: task.identifier, state: 'error', hasPreviousData: Boolean(session.entries.get(task.key)) });
        this.finish(session, task);
        if (current) task.reject(error);
      },
    );
  }

  private finish(session: Session, task: Task): void {
    task.status = 'settled';
    session.pending.delete(task.key);
    session.active--;
    if (task.lane === 'background') session.activeBackground--;
    this.drain(session);
  }
}

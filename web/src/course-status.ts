import type { AnswerJob, Homework } from './types.js';
import { pendingJob, serviceJob } from './jobs.js';
import { record } from './ui.js';

export interface CourseWorkEvidence {
  data: Homework[];
  requestedAt: number;
  updatedAt: number;
  stale: boolean;
  sync: 'loading' | 'ready' | 'error';
  observedTerminalVersions: ReadonlySet<string>;
}

// Compare an observed server record version, not just browser/server wall clocks.
export function courseTerminalVersion(job: AnswerJob): string {
  return JSON.stringify([job.id, job.status, job.created_at, job.updated_at, job.finished_at]);
}

function knownRequired(homework: Homework): boolean {
  const total = homework.requiredChallengeCount, finished = homework.finishedRequiredChallengeCount;
  return homework.homeworkKind !== 'common' && homework.requiredProgressKnown === true && Number.isSafeInteger(total) && Number.isSafeInteger(finished)
    && (total as number) >= 0 && (finished as number) >= 0 && (finished as number) <= (total as number);
}

function commonPending(homework: Homework): boolean {
  return homework.homeworkKind === 'common' && homework.submissionStatus !== 'submitted';
}

export interface CourseWorkSummary {
  pending: number;
  shixunPending: number;
  commonPending: number;
  commonSubmitted: number;
  commonTotal: number;
  hasRequired: boolean;
}

export function summarizeCourseWork(homeworks: Homework[]): CourseWorkSummary {
  const shixun = homeworks.filter(homework => homework.homeworkKind !== 'common');
  const common = homeworks.filter(homework => homework.homeworkKind === 'common');
  const shixunPending = shixun.filter(homework => !knownRequired(homework) || homework.finishedRequiredChallengeCount! < homework.requiredChallengeCount!).length;
  const commonPendingCount = common.filter(commonPending).length;
  return {
    pending: shixunPending + commonPendingCount,
    shixunPending,
    commonPending: commonPendingCount,
    commonSubmitted: common.filter(homework => homework.submissionStatus === 'submitted').length,
    commonTotal: common.length,
    hasRequired: shixun.some(homework => knownRequired(homework) && homework.requiredChallengeCount! > 0),
  };
}

// This is only a student display projection. An absent target can have closed
// or become elective; neither it nor a historical attempt is promoted to passed.
// Backend records, administrator review and mutation capabilities stay intact.
export function courseRecordIsHistorical(job: AnswerJob, evidence: ReadonlyMap<string, CourseWorkEvidence>, freshOnly = false): boolean {
  if (!serviceJob(job) || pendingJob(job) || job.request.mode !== 'submit'
    || !['failed', 'support', 'uncertain', 'interrupted', 'canceled'].includes(job.status)
    || !['course', 'homework', 'all'].includes(job.request.scope ?? '')) return false;
  const dates = [job.created_at, job.updated_at, job.finished_at].filter(value => value !== undefined && value !== null)
    .map(value => typeof value === 'string' ? Date.parse(value) : NaN);
  if (!dates.length || dates.some(value => !Number.isFinite(value))) return false;
  const terminalAt = Math.max(...dates);
  const proof = (course: string): CourseWorkEvidence | undefined => {
    const value = evidence.get(course);
    if (!value || !value.observedTerminalVersions.has(courseTerminalVersion(job))
      || !Number.isFinite(value.requestedAt) || value.requestedAt < terminalAt
      || freshOnly && (value.stale || value.sync !== 'ready')) return undefined;
    return value;
  };
  const noCurrentWork = (course: string, homeworkId: number): boolean => {
    const value = proof(course);
    if (!value) return false;
    const homework = value.data.find(item => item.id === homeworkId);
    if (!homework) return true;
    if (homework.homeworkKind === 'common') return homework.submissionStatus === 'submitted';
    return knownRequired(homework) && homework.finishedRequiredChallengeCount === homework.requiredChallengeCount;
  };
  const scope = job.request.scope;
  const course = job.request.course ?? '';
  if (scope === 'homework') {
    const id = job.request.homework_id;
    return Number.isSafeInteger(id) && id! > 0 && noCurrentWork(course, id!);
  }
  // Prefer the actual original plan when available: a newer unrelated task
  // does not make an older completed/closed target require follow-up again.
  const targets = record(job.result).homeworks;
  if (Array.isArray(targets) && targets.length) {
    const redacted = targets.every(target => {
      const row = record(target);
      return !('course' in row) && !('homework_id' in row);
    });
    // Consumer summaries intentionally expose only title/status. For a single
    // course use its whole current catalog; never infer all-course coverage.
    if (!redacted) {
      const total = record(job.result).total_homeworks;
      const unique = new Set(targets.map(target => {
        const row = record(target);
        return JSON.stringify([row.course ?? course, row.homework_id]);
      }));
      if (!Number.isSafeInteger(total) || Number(total) <= 0
        || total !== targets.length || unique.size !== targets.length) return false;
      return targets.every(target => {
        const row = record(target), targetCourse = typeof row.course === 'string' ? row.course : course;
        return Boolean(targetCourse) && (scope === 'all' || targetCourse === course)
          && Number.isSafeInteger(row.homework_id) && Number(row.homework_id) > 0
          && noCurrentWork(targetCourse, Number(row.homework_id));
      });
    }
  }
  // A single course cannot establish completion of an unrecorded all-course plan.
  if (scope === 'all') return false;
  const value = proof(course);
  return Boolean(value && summarizeCourseWork(value.data).pending === 0);
}

export function historicalCourseRecordIds(jobs: AnswerJob[], evidence: ReadonlyMap<string, CourseWorkEvidence>): Set<string> {
  return new Set(jobs.filter(job => courseRecordIsHistorical(job, evidence)).map(job => job.id));
}

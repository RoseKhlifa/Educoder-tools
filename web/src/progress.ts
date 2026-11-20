import { escapeHtml as e } from './ui.js';
import type { AutoRetry, ProgressItem } from './types.js';

// Shared progress vocabulary and renderer. The consumer and operator pages use
// the same component so a job looks the same on both sides, with only the
// operator view falling back to the raw reason code when a label is missing.

export type { AutoRetry, ProgressItem } from './types.js';

export type ProgressStatus = 'waiting' | 'processing' | 'completed' | 'support';

// stageLabels name the step a level is on right now. Keys are the execution
// phases the server already reports; an unknown phase shows no step at all
// rather than leaking an internal name.
export const stageLabels: Record<string, string> = {
  queued: '排队中', starting: '准备执行', preparing: '准备执行', preflight: '核对关卡清单',
  initializing: '准备关卡', loading: '读取题目', loading_resources: '读取题目图片',
  bank_lookup: '查询题库答案', bank_saved: '保存已验证答案',
  generating: '生成答案', answering: '生成答案', solving: '生成答案', repairing: '按反馈修复答案',
  validating: '校验答案格式', saving: '保存代码',
  selftesting: '公开自测', selftest: '公开自测', environment_retry: '重试自测环境',
  submitting: '正式提交', evaluating: '正式评测', polling: '读取评测结果',
  verifying: '核验成绩', verified: '已核验成绩', reconciling: '核对提交结果',
  service_pending: '等待跟进', running: '正在处理', processing: '正在处理',
  completed: '处理结束', failed: '处理结束', canceled: '已停止', working: '正在处理',
};

// progressReasons explain a non-pass in plain language. Keys are the consumer
// vocabulary the server publishes: a small closed set of categories, never an
// internal execution code.
export const progressReasons: Record<string, string> = {
  service_followup: '这部分需要人工跟进，我们会继续核对',
  already_passed: '已是满分，自动跳过',
  no_required_levels: '没有需要完成的必做关卡',
  deadline_passed: '作业已截止，不再安排处理',
  platform_busy: '头歌平台资源紧张，可稍后重新执行',
  platform_unavailable: '头歌平台暂时读取不到内容，可稍后重新执行',
  needs_review: '上次提交结果尚未确认，请使用“复核并继续”',
  retry_later: '提交尚未发出，可安全重新执行',
  answer_unavailable: '这次没能生成通过的答案，我们会继续跟进',
  question_conflict: '题目要求与公开样例存在矛盾，待课程方核对后继续',
  unsupported: '这部分内容暂不支持自动完成',
  quota_reached: '已达到本次处理上限，剩余内容可稍后重新执行',
  paused_cooldown: '上次失败后没有新进展，已暂停重复尝试',
  stopped: '已按请求停止，已完成进度保留',
  interrupted: '处理被中断，需要先核对已完成结果',
};

// internalReasonLabels are the operator vocabulary: exact execution codes
// with their Chinese explanations. Only the admin views use them.
export const internalReasonLabels: Record<string, string> = {
  public_contract_conflict: '题面要求与公开评测样例存在明确矛盾，已停止无效生成，请课程方核对题目配置', context_unavailable: '公开调用代码读取未确认，已暂停模型生成，请核对读取诊断',
  answer_readback_unconfirmed: '保存后的答案回读尚未确认', answer_save_unconfirmed: '答案保存响应尚未确认', paper_changed: '题卷或选项已变化，需要重新核对', checkpoint_persistence_failed: '执行记录保存失败', commit_readiness_unconfirmed: '交卷前检查尚未确认', unanswered_questions: '仍有未作答项目', model_provider_missing: '考试模型尚未配置', model_generation_failed: '考试答案生成失败', model_budget_exhausted: '达到本次内部用量上限', previous_execution_interrupted: '上次执行已中断，需要先核对结果', submitted_state_unconfirmed: '交卷状态尚未确认', exercise_candidate_contract: '生成答案与当前题卷不匹配', exercise_media_unverified: '题目中的媒体内容尚未核验',
  action_required: '考试入场事项需要在头歌处理', locked: '考试已锁定，需要教师解锁', expired: '考试已截止', already_submitted: '考试已经交卷，未重复提交', unsupported_question: '考试包含尚未适配的题型', identity_mismatch: '考试或题目身份不一致', answer_invalid: '考试答案未通过格式核验', grading_pending: '考试已交卷，成绩尚未公布', not_started: '考试尚未开始', proctoring_required: '考试需要原站监考或入场确认',
  runtime_initialization_failed: '后续关卡尚未成功进入，请检查原站顺序限制或入口响应', precheck_failed: '本关预检未通过，已隔离并继续其他关卡', runtime_roster_ambiguous: '运行环境身份不明确，已隔离本关', task_identity_mismatch: '题目身份与作业清单不一致，已隔离本关', unsupported_question_kind: '当前关卡题型尚未适配，已隔离本关', blocker_state_unavailable: '历史阻塞状态暂时无法读取，请稍后重试', unchanged_failure_cooldown: '本关暂无新进展，已进入短暂冷却', generation_stalled: '模型重复返回已失败或不完整的答案，已停止无效消耗，需补充候选',
  editor_changed: '保存后的源码与候选答案不一致，尚未确认评测通过',
  model_error: '模型服务调用失败', model_timeout: '模型响应超时', model_network_error: '模型服务连接失败', model_provider_payment_required: '模型供应商账户余额或额度不足，需处理供应商配置', collection_stopped: '只读题库采集已停止，请查看采集统计和跳过原因', service_restarted: '服务重启中断了任务，需要核对已完成结果',
  verification_unconfirmed: '头歌返回的完成或分值信息不足，尚未核验通过',
  resource_unavailable: '头歌评测资源紧张，本次未启动评测，可稍后重试',
  progress_unavailable: '头歌作业成绩读取失败，未进入执行',
  assignment_verification_unconfirmed: '整份作业的满分证据尚未确认',
  assignment_roster_incomplete: '关卡清单与成绩不完全对应，尚未确认通过',
  blocker_state_persistence_failed: '失败状态保存失败，未写入冷却记录',
  save_error: '代码保存失败，尚未提交评测',
  uncertain: '结果未确认，需要先读取头歌结果',
  needs_service: '需要服务人员跟进',
  already_passed: '已确认满分，已跳过',
  skipped: '已满分或无需安排，已跳过', submission_uncertain: '提交结果尚未确认，需要先读取头歌结果', prewrite_error: '图形环境在写入前失败，可安全重试', saved_not_submitted: '图形文件已保存但未提交评测，可安全重试', save_unverified: '图形文件保存结果未确认，未发起评测', attempts_exhausted: '已达到本次修复次数，仍未通过', budget_exhausted: '已达到本次任务费用上限', budget_unavailable: '执行预算暂不可用', selftest_failed: '代码自测未通过', selftest_connection_failed: '自测连接失败', selftest_stream_unconfirmed: '自测输出未完整确认', invalid_candidate: '生成的答案未满足提交要求', invalid_output_json: '模型返回内容格式不正确', model_response_invalid: '模型返回内容格式不正确', model_output_truncated: '模型返回内容被截断', model_rate_limited: '模型请求受到频率限制', execution_failed: '执行未完成，需要检查原始响应', unsupported_multifile: '当前作业的多文件结构尚未支持', unsupported_roster: '关卡清单不完整，暂停执行', unsupported_homework: '当前作业类型尚未支持', usage_persistence_failed: '执行用量保存失败', verification_persistence_failed: '完成核验结果保存失败', course_sync_failed: '课程列表读取失败', homework_sync_failed: '作业列表读取失败', course_identity_invalid: '课程标识不完整，暂停执行', homework_identity_invalid: '作业标识不完整，暂停执行', course_limit: '课程数量达到本次任务上限', homework_limit: '作业数量达到本次任务上限', deadline_passed: '作业已经截止', no_required_levels: '没有需要执行的必做关卡', deadline_exceeded: '整批执行达到时间上限，尚未开始此作业', time_limit: '整批执行达到时间上限，尚未开始此作业', context_deadline_exceeded: '整批执行达到时间上限，尚未开始此作业', collection_failed: '题库采集未完成，请检查采集明细', collect_failed: '题库采集未完成，请检查采集明细', request_byte_or_time_limit: '题库采集达到读取量或时间上限', limit_reached: '题库采集达到本次上限', service_required: '存在尚未完成的作业，需跟进', canceled: '管理员或用户已停止执行', interrupted: '服务重启中断了执行，需要核对已完成结果',
};
const statusTone: Record<string, ProgressStatus> = {
  completed: 'completed', passed: 'completed', skipped: 'completed',
  processing: 'processing', running: 'processing',
  waiting: 'waiting', planned: 'waiting', pending: 'waiting',
};

export const progressTone = (status?: string): ProgressStatus => statusTone[String(status ?? '')] ?? 'support';

const levelLabels: Record<ProgressStatus, string> = { completed: '已完成', processing: '正在处理', waiting: '待处理', support: '待跟进' };
const historyLabels: Record<ProgressStatus, string> = { completed: '当次已完成', processing: '当次未完成', waiting: '当次未开始', support: '当次未完成' };

export function toneLabel(tone: ProgressStatus, historical = false): string {
  return historical ? historyLabels[tone] : levelLabels[tone];
}
export function progressStatusLabel(status?: string, historical = false): string {
  return toneLabel(progressTone(status), historical);
}
/**
 * settledTone keeps a stopped job honest: once the job is no longer active,
 * anything it had not finished is work that still needs attention, never work
 * that is still in progress.
 */
export function settledTone(status: string | undefined, active: boolean): ProgressStatus {
  const tone = progressTone(status);
  return active || tone === 'completed' ? tone : 'support';
}

export function stageLabel(stage?: string): string {
  const key = String(stage ?? '').trim();
  return key ? stageLabels[key] ?? '' : '';
}

// reasonText keeps unknown codes out of the consumer page. The operator view
// shows the code itself so a new state is still traceable without a deploy.
export function reasonText(reason?: string, admin = false): string {
  const code = String(reason ?? '').trim().toLowerCase();
  if (!code) return '';
  const label = progressReasons[code] ?? (admin ? internalReasonLabels[code] : undefined);
  if (label) return label;
  return admin ? `原因代码：${code}` : progressReasons.service_followup;
}

export function clockTime(value?: string): string {
  if (!value) return '';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false });
}

// autoRetryText renders a scheduled automatic retry. The server validates the
// schedule, so a missing or unparseable time simply shows nothing.
export function autoRetryText(value?: AutoRetry | null): string {
  if (!value || typeof value !== 'object') return '';
  const at = clockTime(value.next_at);
  if (!at) return '';
  const attempt = Number(value.attempt), max = Number(value.max);
  const round = Number.isSafeInteger(attempt) && attempt > 0 && Number.isSafeInteger(max) && max > 0 ? `（第 ${attempt}/${max} 次）` : '';
  return `将于 ${at} 自动重试${round}`;
}

export interface ProgressTreeOptions {
  historical?: boolean;
  admin?: boolean;
  /** Levels are shown for the job the user opened, not in list cards. */
  levels?: boolean;
  /**
   * parentActive is false once the job itself has stopped. A stored snapshot
   * can still name a level as running; the page must not claim that work is
   * in progress after its job ended.
   */
  parentActive?: boolean;
  emptyText?: string;
}

function levelCountText(item: ProgressItem): string {
  const total = Number(item.total_levels) || 0;
  if (!total) return '';
  const done = Math.min(total, Number(item.completed_levels) || 0);
  const skipped = Number(item.skipped_levels) || 0;
  return `${done} / ${total} 个关卡${skipped > 0 ? ` · 其中 ${skipped} 个已是满分` : ''}`;
}

function currentLevelText(item: ProgressItem): string {
  const title = String(item.current_title ?? '').trim();
  const position = Number(item.current_position) || 0;
  if (!position && !title) return '';
  const stage = stageLabel(item.stage);
  const name = `${position ? `第 ${position} 关` : '当前关卡'}${title ? ` · ${title}` : ''}`;
  return stage ? `${name} · ${stage}` : name;
}

function renderLevels(items: ProgressItem[], historical: boolean, active: boolean): string {
  if (!items.length) return '';
  return `<ol class="level-track">${items.map(level => {
    const tone = settledTone(level.status, active);
    const position = Number(level.position) || 0;
    return `<li class="level-chip tone-${tone}" title="${e(`${position ? `第 ${position} 关 · ` : ''}${level.title ?? ''} · ${toneLabel(tone, historical)}`)}"><span class="level-mark" aria-hidden="true">${tone === 'completed' ? '✓' : tone === 'processing' ? '•' : tone === 'support' ? '!' : ''}</span><span class="level-index">${position || '·'}</span><span class="level-name">${e(String(level.title ?? '').trim() || '关卡')}</span><span class="level-state">${toneLabel(tone, historical)}</span></li>`;
  }).join('')}</ol>`;
}

/**
 * renderProgressTree lists every assignment of a job with its own state, level
 * counters, current level and reason. It is the single component behind the
 * consumer detail view, the consumer card and the operator job detail.
 */
export function renderProgressTree(items: ProgressItem[], options: ProgressTreeOptions = {}): string {
  const { historical = false, admin = false, levels = false, parentActive = true } = options;
  if (!Array.isArray(items) || !items.length) {
    return options.emptyText ? `<p class="muted progress-tree-empty">${e(options.emptyText)}</p>` : '';
  }
  return `<ol class="progress-tree">${items.slice(0, 100).map((item, index) => {
    const tone = settledTone(item.status, parentActive);
    const reason = tone === 'completed' && item.reason !== 'already_passed' && item.reason !== 'no_required_levels' ? '' : reasonText(item.reason, admin);
    const counts = levelCountText(item);
    const current = tone === 'processing' ? currentLevelText(item) : '';
    const started = clockTime(item.started_at);
    const updated = clockTime(item.updated_at);
    const times = [started ? `开始 ${started}` : '', updated && updated !== started ? `更新 ${updated}` : ''].filter(Boolean).join(' · ');
    return `<li class="progress-node tone-${tone}">
      <span class="node-mark" aria-hidden="true">${tone === 'completed' ? '✓' : tone === 'processing' ? '◌' : tone === 'support' ? '!' : index + 1}</span>
      <div class="node-body">
        <div class="node-head"><strong>${e(String(item.title ?? '').trim() || `作业 ${index + 1}`)}</strong><span class="badge ${tone}">${toneLabel(tone, historical)}</span></div>
        ${item.course_name ? `<p class="node-course">${e(item.course_name)}</p>` : ''}
        ${counts ? `<p class="node-counts">${e(counts)}</p>` : ''}
        ${current ? `<p class="node-current">${e(current)}</p>` : ''}
        ${reason ? `<p class="node-reason">${e(reason)}</p>` : ''}
        ${times ? `<p class="node-time">${e(times)}</p>` : ''}
        ${levels && Array.isArray(item.levels) ? renderLevels(item.levels.slice(0, 100), historical, parentActive && tone === 'processing') : ''}
      </div>
    </li>`;
  }).join('')}</ol>`;
}

/** progressCounts groups a list by display state for the summary strips. */
export function progressCounts(items: ProgressItem[]): Record<ProgressStatus, number> {
  const out: Record<ProgressStatus, number> = { waiting: 0, processing: 0, completed: 0, support: 0 };
  for (const item of Array.isArray(items) ? items : []) out[progressTone(item.status)]++;
  return out;
}

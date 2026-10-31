import type { AnswerJob, AppState, CollectionSummary, HomeworkProgress, JobMode, JobStatus } from './types.js';

const escape = (value: unknown): string => String(value ?? '').replace(/[&<>'"]/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' }[char] as string));
export const pendingJob = (job: AnswerJob): boolean => job.status === 'queued' || job.status === 'running';
export const isHomeworkJob = (job: AnswerJob): boolean => job.request.mode !== 'collect_verified' && job.request.scope === 'homework';
export function taskHasPendingJob(state: AppState): boolean {
  const task = state.enteredTask;
  return Boolean(task && state.jobs.some(job => pendingJob(job) && job.request.mode !== 'collect_verified' && job.request.course === task.courseIdentifier && job.request.homework_id === task.homeworkId && (isHomeworkJob(job) || state.jobMode === 'submit' || job.request.game === task.gameIdentifier)));
}
const statusLabels: Record<JobStatus, string> = { queued: '排队中', running: '执行中', succeeded: '流程已完成', failed: '执行失败', canceled: '已停止', uncertain: '平台结果待复核', interrupted: '执行已中断' };
const modes: Record<JobMode, string> = { generate_only: '仅生成答案', selftest: '自测当前关卡', submit: '提交并验证' };
const phases: Record<string, string> = {
  queued: '等待执行', starting: '准备任务', snapshot: '读取题目', loading: '读取题目', fetching: '读取题目', loading_resources: '读取题干与选项图片',
  generating: '生成答案', generation: '生成答案', model: '生成答案', validating: '检查答案',
  saving: '保存答案', selftest: '执行自测', testing: '执行自测', repairing: '根据反馈修复',
  submitting: '提交答案', submit: '提交答案', polling: '等待评测', verifying: '复核平台状态',
  recording: '记录题库', completed: '流程结束', done: '流程结束', canceled: '停止执行',
  selftesting: '执行自测', evaluating: '等待正式评测', bank_lookup: '查询题库', bank_saved: '记录题库验证',
  listing_levels: '读取全部关卡', checking_levels: '核验已完成关卡', homework_loading: '读取作业关卡', level_starting: '准备下一关',
  retrying: '正在自动重试', generation_retry: '重新生成答案', generating_retry: '重新生成答案', selftest_retry: '重试自测连接', reconciling: '重新读取评测结果',
};

// Only these public codes may turn backend details into visible explanations.
// In particular, never render arbitrary diagnostics, provider output or reason text.
const failureReasons: Record<string, { summary: string; next: string }> = {
  objective_image_unavailable: { summary: '题干或选项图片未能完整读取', next: '尚未向模型或平台提交不完整的图题，请重新读取资源后继续。' },
  model_output_truncated: { summary: '模型输出被截断，答案未完整返回', next: '本次自动重新生成已结束，未继续提交不完整答案。' },
  model_response_invalid: { summary: '模型答案格式未通过检查', next: '本次自动重新生成已结束，未继续提交格式错误的答案。' },
  model_rate_limited: { summary: '模型服务触发请求限流', next: '本次限流重试已结束，请稍后继续。' },
  model_timeout: { summary: '模型响应超时', next: '本次超时重试已结束，请稍后继续。' },
  model_network_error: { summary: '连接模型服务失败', next: '本次连接重试已结束，请稍后继续。' },
  model_error: { summary: '模型生成失败', next: '本次自动重新生成已结束，后续关卡尚未执行。' },
  selftest_failed: { summary: '当前代码未通过公开样例自测', next: '本次自动修复已结束，当前关卡未计为完成。' },
  selftest_connection_failed: { summary: '自测连接失败，未取得自测结果', next: '本次连接重试已结束，当前关卡未计为完成。' },
  selftest_stream_unconfirmed: { summary: '自测结果流中断，未取得完整结果', next: '当前关卡未计为完成，需先重新读取平台状态。' },
  editor_changed: { summary: '平台编辑器内容发生变化', next: '本次执行已停止，需先确认当前关卡代码。' },
  attempts_exhausted: { summary: '已达到本次自动修复次数上限，仍未通过', next: '当前关卡未计为完成，后续关卡尚未执行。' },
  verification_unconfirmed: { summary: '平台尚未返回可核验的满分结果', next: '需先重新读取评测结果，确认后再继续，避免重复提交。' },
  submission_uncertain: { summary: '提交可能已被平台受理，最终结果尚未确认', next: '需先重新读取评测结果，确认后再继续，避免重复提交。' },
  budget_exhausted: { summary: '已达到本次模型调用或费用上限', next: '已完成的关卡会保留，后续关卡尚未执行。' },
  canceled: { summary: '用户停止了后续步骤', next: '已完成的关卡会保留，已送达平台的提交不会撤回。' },
  unsupported_roster: { summary: '作业关卡清单未通过完整性或身份检查', next: '尚未开始处理剩余关卡，请重新加载作业信息。' },
  execution_failed: { summary: '当前关卡执行失败', next: '已完成的关卡会保留，后续关卡尚未执行。' },
};
const legacyFailureCodes: Record<string, string> = {
  'model provider truncated_output': 'model_output_truncated',
  truncated_output: 'model_output_truncated',
  'model provider invalid_output_json': 'model_response_invalid',
  'model provider invalid_candidate': 'model_response_invalid',
  'model provider objective_candidate_contract': 'model_response_invalid',
  'model provider rate_limited': 'model_rate_limited',
  'model provider timeout': 'model_timeout',
  'model provider network_error': 'model_network_error',
};
const progressMessages: Record<string, string> = {
  '答案与已知未满分尝试重复，已跳过重复提交并继续修复': '模型返回了此前未满分的相同答案，已跳过重复提交，继续复核题目。',
  '模型输出未完成，正在自动重试生成': '模型输出未完成，正在自动重新生成答案。',
  '公开自测未通过，正在根据反馈自动修复': '公开自测未通过，正在根据反馈自动修复。',
  '当前关卡未满分，正在根据得分自动修复': '当前关卡未满分，正在根据得分自动修复。',
};
function progressMessage(job: AnswerJob): string | undefined {
  for (const value of [job.message, job.events?.at(-1)?.message]) {
    if (typeof value === 'string' && Object.hasOwn(progressMessages, value)) return progressMessages[value];
  }
  return undefined;
}
function failureCode(value: Record<string, unknown> | undefined): string | undefined {
  if (!value) return undefined;
  if (typeof value.reason_code === 'string' && Object.hasOwn(failureReasons, value.reason_code)) return value.reason_code;
  if (Array.isArray(value.diagnostics)) {
    for (let index = value.diagnostics.length - 1; index >= 0; index--) {
      const diagnostic: unknown = value.diagnostics[index];
      if (typeof diagnostic === 'string' && Object.hasOwn(legacyFailureCodes, diagnostic)) return legacyFailureCodes[diagnostic];
    }
  }
  if (typeof value.status === 'string' && Object.hasOwn(failureReasons, value.status)) return value.status;
  if (value.status === 'uncertain') return 'submission_uncertain';
  if (value.status === 'unconfirmed') return 'verification_unconfirmed';
  return undefined;
}
const nonnegativeCount = (value: unknown): number | undefined => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;

function homeworkProgress(job: AnswerJob): HomeworkProgress {
  const latest = job.events?.filter(event => event.progress).at(-1)?.progress;
  const value = !pendingJob(job) && job.result ? job.result : job.progress ?? latest ?? job.result;
  const progress: HomeworkProgress = {};
  for (const key of ['total_levels', 'completed_levels', 'skipped_levels', 'failed_levels', 'current_position'] as const) {
    const count = value?.[key];
    if (typeof count === 'number' && Number.isSafeInteger(count) && count >= 0) progress[key] = count;
  }
  if (typeof value?.current_title === 'string') progress.current_title = value.current_title;
  return progress;
}

function homeworkCard(job: AnswerJob, canceling: boolean): string {
  const pending = pendingJob(job);
  const progress = homeworkProgress(job);
  const allPassed = job.status === 'succeeded' && job.result?.status === 'passed' && typeof progress.total_levels === 'number' && progress.total_levels > 0 && progress.completed_levels === progress.total_levels && progress.failed_levels === 0;
  const levels = Array.isArray(job.result?.levels) ? job.result.levels.filter((value): value is Record<string, unknown> => Boolean(value) && typeof value === 'object' && !Array.isArray(value)) : [];
  const stoppedLevel = levels.find(value => value.position === progress.current_position && failureCode(value)) ?? levels.find(value => failureCode(value));
  const explicitLevelCode = typeof stoppedLevel?.reason_code === 'string' && Object.hasOwn(failureReasons, stoppedLevel.reason_code) ? stoppedLevel.reason_code : undefined;
  const code = explicitLevelCode ?? failureCode(job.result) ?? failureCode(stoppedLevel);
  const failure = code ? failureReasons[code] : undefined;
  const needsReview = code === 'submission_uncertain' || code === 'verification_unconfirmed' || code === 'selftest_stream_unconfirmed';
  const label = allPassed ? '全部关卡已核验通过' : job.status === 'succeeded' ? '执行已结束，仍有关卡待核验' : job.status === 'running' ? '正在完成整份作业' : job.status === 'uncertain' && failure ? needsReview ? '评测结果待复核' : '执行未完成' : statusLabels[job.status];
  const tone = allPassed ? 'status-done' : pending ? 'status-progress' : 'status-attention';
  const counters: string[] = [];
  if (progress.completed_levels !== undefined) counters.push(`已完成 ${progress.completed_levels}${progress.total_levels !== undefined ? `/${progress.total_levels}` : ''} 关`);
  else if (progress.total_levels !== undefined) counters.push(`共 ${progress.total_levels} 关`);
  if (progress.skipped_levels !== undefined) counters.push(`跳过已满分 ${progress.skipped_levels} 关（计入已完成）`);
  if (progress.failed_levels !== undefined && progress.failed_levels > 0) counters.push(`未通过 ${progress.failed_levels} 关`);
  const current = pending && progress.current_position ? `<p class="job-message">正在处理第 ${progress.current_position} 关${progress.current_title ? `：${escape(progress.current_title)}` : ''}</p>` : '';
  const retryMessage = pending ? progressMessage(job) : undefined;
  const levelLabels: Record<string, string> = { passed: '已核验通过', skipped: '已满分，跳过', already_passed: '已满分，跳过', failed: '未通过，已停止', generated: '仅生成，未提交', unconfirmed: '结果待核验', uncertain: '平台结果待复核', verification_unconfirmed: '满分结果待核验', canceled: '后续步骤已停止', unsupported: '暂不支持此题型', unsupported_roster: '关卡信息未通过检查', stopped: '已停止', pending: '尚未执行', planned: '尚未执行', running: '正在执行', retrying: '正在自动重试', model_error: '模型生成失败', selftest_failed: '公开自测未通过', attempts_exhausted: '自动修复次数已用尽', budget_exhausted: '本次调用或费用上限已到' };
  const rows = levels.map(level => {
    const position = typeof level.position === 'number' && Number.isSafeInteger(level.position) && level.position > 0 ? `第 ${level.position} 关` : '关卡';
    const title = typeof level.title === 'string' ? ` · ${escape(level.title)}` : '';
    const status = typeof level.status === 'string' && Object.hasOwn(levelLabels, level.status) ? levelLabels[level.status] : '结果待核验';
    const score = (typeof level.score === 'string' || typeof level.score === 'number') && (typeof level.max_score === 'string' || typeof level.max_score === 'number') ? ` · ${escape(level.score)} / ${escape(level.max_score)}` : '';
    const levelCode = level === stoppedLevel ? code : failureCode(level);
    const reason = levelCode ? failureReasons[levelCode]?.summary : '';
    const calls = nonnegativeCount(level.model_calls);
    const maxCalls = nonnegativeCount(level.max_model_calls);
    const attempts = calls !== undefined && calls > 0 ? `模型调用 ${calls}${maxCalls !== undefined && maxCalls >= calls ? ` / ${maxCalls}` : ''} 次` : '';
    return `<li><span>${position}${title}</span><strong>${status}${score}</strong>${reason ? `<p class="job-note">原因：${reason}</p>` : ''}${attempts ? `<p class="job-result">${attempts}</p>` : ''}</li>`;
  });
  const stamp = new Date(job.created_at);
  const time = Number.isNaN(stamp.getTime()) ? '' : ` · ${stamp.toLocaleString('zh-CN')}`;
  const stopPosition = nonnegativeCount(stoppedLevel?.position) ?? progress.current_position;
  const stopTitle = typeof stoppedLevel?.title === 'string' ? stoppedLevel.title : progress.current_title;
  const stopContext = !pending && !allPassed && stopPosition ? `<p class="job-message">停止位置：第 ${stopPosition} 关${stopTitle ? ` · ${escape(stopTitle)}` : ''}</p>` : '';
  const note = !pending && !allPassed && failure ? `原因：${failure.summary}。${failure.next}`
    : job.status === 'uncertain' ? '当前关卡的执行结果尚未确认，后续关卡已停止；请先核对平台状态再决定是否重试。'
    : job.status === 'canceled' ? '后续关卡已停止；已经完成的关卡会保留，已送达平台的提交不会撤回。'
    : job.status === 'interrupted' ? '服务重启中断了整份作业任务，不会自动重放；请先核对已完成关卡。'
    : !pending && !allPassed ? '整份作业尚未全部核验通过。后续关卡未继续执行，请查看当前关卡的结果。' : '';
  const calls = job.result?.model_calls;
  const cost = job.result?.cost_micro_usd;
  const usage = [typeof job.result?.score === 'number' || typeof job.result?.score === 'string' && job.result.score.length ? `平台得分 ${escape(job.result.score)}${typeof job.result?.max_score === 'number' || typeof job.result?.max_score === 'string' && job.result.max_score.length ? ` / ${escape(job.result.max_score)}` : ''}` : '', typeof calls === 'number' && Number.isSafeInteger(calls) && calls >= 0 ? `模型调用 ${calls} 次` : '', typeof cost === 'number' && Number.isFinite(cost) && cost >= 0 ? `模型费用 $${(cost / 1_000_000).toFixed(6)}` : ''].filter(Boolean).join(' · ');
  return `<article class="job-card job-homework" data-job-id="${escape(job.id)}"><div class="job-card-top"><strong>作业 ${escape(job.request.homework_id)} · 完成整份作业</strong><span class="status-pill ${tone}">${escape(label)}</span></div><p class="job-context">${escape(job.request.course)}${escape(time)}</p><p class="job-phase">${pending ? '<span class="spinner spinner-blue"></span>' : ''}${escape(phases[job.phase] ?? (pending ? '按顺序处理关卡' : '本次执行已结束'))}</p>${counters.length ? `<p class="job-result">${counters.join(' · ')}</p>` : '<p class="job-result">等待读取这份作业的完整关卡列表。</p>'}${current}${retryMessage ? `<p class="job-message">${retryMessage}</p>` : ''}${stopContext}${usage ? `<p class="job-result">${usage}</p>` : ''}${rows.length ? `<details class="homework-levels"${!allPassed && !pending ? ' open' : ''}><summary>各关卡结果</summary><ol>${rows.join('')}</ol></details>` : ''}${note ? `<p class="job-note">${note}</p>` : ''}${pending ? `<button class="button button-outline job-cancel" data-cancel-job="${escape(job.id)}" ${canceling ? 'disabled' : ''}>${canceling ? '正在请求停止…' : '停止后续关卡'}</button>` : ''}</article>`;
}
const resultStatuses: Record<string, string> = {
  generated: '答案已生成，未提交', passed: '平台评测已通过', selftest_passed: '公开样例自测通过，未正式提交',
  selftest_skipped: '无公开样例，未执行自测', selftest_failed: '公开自测未通过',
  uncertain: '平台结果待复核', unconfirmed: '平台分数待确认', canceled: '执行已停止',
  needs_provider: '需要配置模型', budget_exhausted: '已达到本次费用或调用上限',
  budget_unavailable: '费用预算暂不可用', attempts_exhausted: '已达到本次修复上限',
  model_error: '模型请求失败', unsupported_multifile: '暂不支持此多文件任务',
  verification_persistence_failed: '评测证据未完整入库', usage_persistence_failed: '模型用量未完整记录',
};
const collectionReasons: Record<string, string> = {
  no_readonly_game_locator: '未取得只读关卡入口',
  no_confirmed_full_score: '未取得可核验的满分记录',
  target_not_confirmed_full_score: '目标关卡缺少可核验的满分记录',
  classroom_identifier_missing: '课堂标识缺失',
  unsupported_homework_kind: '暂不支持此作业类型',
  unsupported_task_kind: '暂不支持此关卡题型',
  ambiguous_challenge_list: '关卡列表缺失或存在重复标识',
  target_identity_mismatch: '题目与目标关卡不匹配',
  programming_evaluation_unconfirmed: '编程题正式评测记录尚未确认',
  programming_submitted_source_unproven: '当前代码与通过版本的对应关系尚未确认',
  score_changed_or_unconfirmed: '复核时分数发生变化或未能确认',
  editor_changed: '两次读取的编辑器内容发生变化',
  objective_status_unconfirmed: '客观题通过状态尚未确认',
  objective_answer_evidence_missing: '客观题缺少逐题正确答案记录',
  unsupported_objective_question_kind: '暂不支持此客观题类型',
  objective_question_identity_mismatch: '客观题题目或选项标识不匹配',
  objective_media_content_unproven: '客观题含有尚未核验的图片或媒体内容',
  objective_result_mismatch: '客观题评测结果与题目不匹配',
  objective_actual_answer_missing: '客观题评测记录未返回作答内容',
  objective_actual_answer_invalid: '客观题作答内容不符合题目要求',
  objective_result_position_mismatch: '客观题评测记录与题目顺序不匹配',
  objective_contract_invalid: '客观题题目与答案结构未通过检查',
  objective_answer_changed_or_mismatched: '复核时客观题题目或答案发生变化',
  course_read_failed: '课堂列表读取失败',
  homework_read_failed: '作业列表读取失败',
  progress_read_failed: '作业进度读取失败',
  task_read_failed: '关卡内容读取失败',
  answer_capture_failed: '答案读取或入库失败',
  request_byte_or_time_limit: '达到请求流量或扫描时长上限',
  homework_page_limit: '达到作业分页扫描上限',
  course_page_limit: '达到课堂分页扫描上限',
};

function collectionSummary(value: AnswerJob['result']): CollectionSummary | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const result: CollectionSummary = {};
  for (const key of ['courses', 'homeworks', 'levels', 'collected', 'duplicates', 'skipped', 'unconfirmed', 'failed'] as const) {
    const count = value[key];
    if (typeof count === 'number' && Number.isSafeInteger(count) && count >= 0) result[key] = count;
  }
  if (typeof value.limit_reached === 'boolean') result.limit_reached = value.limit_reached;
  if (value.reasons && typeof value.reasons === 'object' && !Array.isArray(value.reasons)) {
    result.reasons = {};
    for (const [key, count] of Object.entries(value.reasons)) {
      if (typeof count === 'number' && Number.isSafeInteger(count) && count > 0) result.reasons[key] = count;
    }
  }
  return Object.keys(result).length ? result : null;
}

function collectionCard(job: AnswerJob, canceling: boolean): string {
  const pending = pendingJob(job);
  const result = collectionSummary(job.result);
  const collected = result?.collected;
  const label = job.status === 'succeeded'
    ? collected === undefined ? '扫描已结束，统计待获取' : collected > 0 ? `扫描结束，新增 ${collected} 条答案` : '扫描结束，未新增答案'
    : ({ queued: '等待扫描', running: '扫描中', failed: '扫描未完成', canceled: '扫描已停止', interrupted: '扫描已中断', uncertain: '扫描结果待确认' } as const)[job.status];
  const tone = job.status === 'succeeded' && collected !== undefined && collected > 0 && !result?.limit_reached && !result?.failed ? 'status-done' : pending ? 'status-progress' : 'status-attention';
  const stamp = new Date(job.created_at);
  const time = Number.isNaN(stamp.getTime()) ? '' : ` · ${stamp.toLocaleString('zh-CN')}`;
  const phase = job.status === 'queued' ? '等待后台扫描' : pending ? '核验已通过任务' : '采集扫描已结束';
  const coverage: string[] = [];
  if (result?.courses !== undefined) coverage.push(`已扫描课堂 ${result.courses}`);
  if (result?.homeworks !== undefined) coverage.push(`作业 ${result.homeworks}`);
  if (result?.levels !== undefined) coverage.push(`关卡读取尝试 ${result.levels}`);
  const counts = ([['collected', '新增答案'], ['duplicates', '题库已有'], ['skipped', '跳过'], ['failed', '读取或入库失败']] as const)
    .filter(([key]) => result?.[key] !== undefined)
    .map(([key, name]) => `<div><dt>${name}</dt><dd>${result![key]}</dd></div>`).join('');
  const reasons: string[] = [];
  let other = 0;
  for (const [key, count] of Object.entries(result?.reasons ?? {})) {
    if (Object.hasOwn(collectionReasons, key)) reasons.push(`<li>${escape(collectionReasons[key])}：${count} 项</li>`);
    else other += count;
  }
  if (other) reasons.push(`<li>其他未能确认或读取的记录：${other} 项</li>`);
  const note = job.status === 'canceled' ? '后续扫描已停止；已存入本地题库的记录会保留。'
    : job.status === 'interrupted' ? '服务重启中断了扫描；已存入本地题库的记录会保留，本次扫描不会自动重放。'
    : job.status === 'failed' ? '本次扫描未完成；以下仅显示已经返回的统计。'
    : job.status === 'uncertain' ? '扫描结果尚未确认，请刷新记录查看。' : '';
  return `<article class="job-card job-collection" data-job-id="${escape(job.id)}"><div class="job-card-top"><strong>已通过任务采集</strong><span class="status-pill ${tone}">${escape(label)}</span></div><p class="job-context">登录后自动扫描${escape(time)}</p><p class="job-phase">${pending ? '<span class="spinner spinner-blue"></span>' : ''}${phase}</p><p class="job-message">只读核验已通过的任务，不调用模型，也不向平台保存或提交答案。</p>${coverage.length ? `<p class="job-result">${coverage.join(' · ')}</p>` : ''}${counts ? `<dl class="collection-counts">${counts}</dl>` : '<p class="job-note">统计尚未返回，请刷新记录查看。</p>'}${result?.unconfirmed !== undefined ? `<p class="job-result">未确认 ${result.unconfirmed} 条（包含在跳过中）</p>` : ''}${result?.levels === 0 && !pending ? '<p class="job-note">未能读取任何关卡，尚未获取到可入库的题目与答案。</p>' : ''}${result?.limit_reached ? '<p class="job-note">已达到本次扫描上限，以上仅为部分结果，尚未遍历全部任务。</p>' : ''}${reasons.length ? `<details class="collection-details" open><summary>未入库原因</summary><ul class="collection-reasons">${reasons.join('')}</ul></details>` : ''}${note ? `<p class="job-note">${note}</p>` : ''}${pending ? `<button class="button button-outline job-cancel" data-cancel-job="${escape(job.id)}" ${canceling ? 'disabled' : ''}>${canceling ? '正在请求停止…' : '停止扫描'}</button>` : ''}</article>`;
}

function resultLabel(job: AnswerJob): string {
  const result = job.result;
  if (!result) return '';
  const parts: string[] = [];
  if (typeof result.status === 'string') parts.push(`结果：${resultStatuses[result.status] ?? result.status}`);
  if (typeof result.model_calls === 'number') parts.push(`模型调用 ${result.model_calls} 次`);
  if (typeof result.score === 'number' || typeof result.score === 'string' && result.score.length) {
    parts.push(`平台得分 ${result.score}${typeof result.max_score === 'string' && result.max_score ? ` / ${result.max_score}` : ''}${result.score_known === false ? '（待核实）' : ''}`);
  }
  if (result.score_known === true && result.score_check === 'full_score') parts.push('已核验满分');
  if (typeof result.cost_micro_usd === 'number' && result.cost_micro_usd >= 0) parts.push(`模型费用 $${(result.cost_micro_usd / 1_000_000).toFixed(6)}`);
  if (result.programming && typeof result.programming === 'object') {
    const programming = result.programming as Record<string, unknown>;
    if (programming.progress_error) parts.push('课堂进度待同步，请复核平台状态');
    if (programming.self_test_status === 'skipped') parts.push('本关无公开样例，自测已跳过');
  }
  return parts.length ? `<p class="job-result">${parts.map(escape).join(' · ')}</p>` : '';
}

function jobCard(job: AnswerJob, canceling: boolean): string {
  if (job.request.mode === 'collect_verified') return collectionCard(job, canceling);
  if (isHomeworkJob(job)) return homeworkCard(job, canceling);
  const latest = job.events?.at(-1);
  const pending = pendingJob(job);
  const tone = job.status === 'succeeded' ? 'status-done' : job.status === 'failed' || job.status === 'uncertain' || job.status === 'interrupted' ? 'status-attention' : 'status-progress';
  const stamp = new Date(job.created_at);
  const time = Number.isNaN(stamp.getTime()) ? '' : stamp.toLocaleString('zh-CN');
  const statusNote = job.status === 'uncertain' ? '上游可能已受理，请先核对平台状态后再决定是否重试。' : job.status === 'interrupted' ? '服务重启中断了任务。请核对平台状态，再手动创建新任务。' : job.status === 'canceled' ? '后续步骤已停止，已送达平台的保存或提交不会撤回。' : '';
  return `<article class="job-card" data-job-id="${escape(job.id)}"><div class="job-card-top"><strong>作业 ${escape(job.request.homework_id)} · ${escape(modes[job.request.mode] ?? job.request.mode)}</strong><span class="status-pill ${tone}">${escape(statusLabels[job.status] ?? job.status)}</span></div><p class="job-context">${escape(job.request.course)}${job.request.game ? ` · 关卡 ${escape(job.request.game)}` : ''} · ${escape(time)}</p><p class="job-phase">${pending ? '<span class="spinner spinner-blue"></span>' : ''}${escape(phases[job.phase] ?? (job.phase || '准备任务'))}</p>${job.message || latest?.message ? `<p class="job-message">${escape(job.message || latest?.message)}</p>` : ''}${resultLabel(job)}${statusNote ? `<p class="job-note">${statusNote}</p>` : ''}${pending ? `<button class="button button-outline job-cancel" data-cancel-job="${escape(job.id)}" ${canceling ? 'disabled' : ''}>${canceling ? '正在请求停止…' : '停止后续步骤'}</button>` : ''}</article>`;
}

export function renderJobs(state: AppState): string {
  const capacity = state.jobCapacity;
  return `<section class="jobs-section" id="answer-jobs" aria-label="答题任务记录"><div class="section-heading"><div><p class="eyebrow">ANSWER JOBS</p><h2>答题任务记录</h2></div><button class="button button-outline" data-action="refresh-jobs" ${state.jobsLoading ? 'disabled' : ''}>${state.jobsLoading ? '更新中…' : '刷新记录'}</button></div><p class="jobs-intro">后台异步执行，切换课堂或关闭页面后任务仍会继续。默认完成所选作业的全部关卡，已通过的关卡自动跳过；生成出错或自测失败会在本次上限内自动重试和修复；达到上限或平台结果仍未确认时停止后续关卡。登录后的题库采集单独记录。</p>${capacity ? `<p class="jobs-capacity">执行中 ${escape(capacity.running)} · 排队 ${escape(capacity.queued)} · 全站并行上限 ${escape(capacity.workers)} · 本账号并行上限 ${escape(capacity.per_owner_running ?? 1)} · 模型并发 ${escape(capacity.model_concurrency ?? 1)}</p>${!capacity.model_configured ? '<p class="job-config-note">服务端尚未配置模型，暂时不能创建答题任务。请联系管理员配置模型后刷新。</p>' : ''}` : ''}${state.jobsError ? `<div class="alert alert-error jobs-alert" role="alert">${escape(state.jobsError)}</div>` : ''}${state.jobs.length ? `<div class="job-list">${state.jobs.map(job => jobCard(job, state.cancelingJobs.includes(job.id))).join('')}</div>` : `<p class="empty-mini">${state.jobsLoading ? '正在读取任务记录…' : '暂无答题任务。从任务详情中选择执行方式即可开始。'}</p>`}</section>`;
}

export function renderJobControls(state: AppState): string {
  const task = state.enteredTask;
  if (!task) return '';
  const kind = task.kind;
  const mode = kind === 'choice' && state.jobMode === 'selftest' ? 'generate_only' : state.jobMode;
  const busy = state.creatingJob || taskHasPendingJob(state);
  const ready = Boolean(state.jobCapacity?.model_configured) && (mode === 'submit' || (kind !== 'unknown' && Boolean(task.gameIdentifier)));
  const label = mode === 'submit' ? '完成整份作业' : modes[mode];
  const modeLabel = (value: JobMode): string => value === 'submit' ? '完成整份作业并核验满分' : value === 'generate_only' ? '仅生成当前关答案' : '仅自测当前关卡';
  const modesForTask: JobMode[] = ['submit', ...(kind !== 'unknown' ? ['generate_only' as const] : []), ...(kind === 'programming' ? ['selftest' as const] : [])];
  return `<section class="job-controls" aria-label="执行作业"><h4>执行这份作业 <span class="muted">${kind === 'programming' ? '当前关为编程题' : kind === 'choice' ? '当前关为客观题' : '当前关题型待识别，后台将逐关判断'}</span></h4><div class="job-modes">${modesForTask.map(value => `<label><input type="radio" name="job-mode" data-job-mode="${value}" value="${value}" ${mode === value ? 'checked' : ''} ${busy ? 'disabled' : ''} /><span>${modeLabel(value)}</span></label>`).join('')}</div><p class="job-control-note">${mode === 'generate_only' ? '只生成当前关卡的答案并记录候选，不提交，也不继续下一关。' : mode === 'selftest' ? '仅保存并自测当前关卡的公开样例，不正式提交，也不继续下一关。' : '一次点击，后台按顺序完成这份作业的全部关卡，自动跳过已核验满分的关卡。生成出错或自测失败会自动重试和修复；达到本次上限或平台结果仍未确认时停止，不影响其他作业。'}</p><button class="button button-primary button-block" data-action="create-job" ${!ready || busy ? 'disabled' : ''}>${state.creatingJob ? '正在创建任务…' : busy ? '这份作业正在执行' : label}</button>${kind === 'unknown' ? '<p class="job-config-note">当前关卡题型尚未识别；整份作业仍可执行，服务端会逐关判断题型。仅生成或自测当前关卡需要先识别题型。</p>' : !state.jobCapacity ? '<p class="job-config-note">请先刷新下方任务记录，确认服务可用。</p>' : !state.jobCapacity.model_configured ? '<p class="job-config-note">服务端尚未配置模型，请联系管理员配置后刷新。</p>' : ''}<p class="job-control-note">任务只处理当前所选的这份作业，不会批量处理整课堂。在下方记录查看各关进度或停止后续关卡，关闭页面后任务仍会继续。</p></section>`;
}

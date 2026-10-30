import { extname } from 'node:path/posix';
import { setTimeout as delay } from 'node:timers/promises';
import { EducoderClient, HarTransport, harResponse, type HarEntry, type JsonObject } from './client.ts';
import { isNotModified, type ProtocolConstants } from './protocol.ts';

export interface CodingTask {
  kind: 'coding'; st: 0; challengeSt: 0; withCodeFile: true;
  gameIdentifier: string; gameId: number; myshixunIdentifier: string; myshixunId: number;
  challengeId: number; homeworkId: string; userId: number;
  subject: string; taskMarkdown: string; paths: string[];
  environmentId: number; tabType: number; gameCount?: number; challengesCount?: number;
  nextGame?: string | null; testSets: JsonObject[];
}
export interface CodeFile {
  path: string; filename: string; extension: string; language: string; fileType: string;
  currentCode: string; size?: number;
  provenance: 'recorded-current-workspace';
}
export interface SavedCode {
  gameId: number; evaluate: 0 | 1; commitId: string; resubmit: string;
  secKey: string; contentModified: number;
}
export interface EvaluationStart { gameId: number; port: number }
export interface SelfTestResult { stdout: string; finished: true; closeCode: 1000; matchesExpected?: boolean }
export interface LogOptions { timeoutMs?: number; maxOutputBytes?: number }
export interface LogRunner { run(url: string, myshixunId: number, options?: LogOptions): Promise<SelfTestResult> }
export interface EvaluationResult {
  complete: true; passed: boolean; status: number; testSetsCount: number; setsErrorCount: number;
  testSets: JsonObject[]; nextGame?: string | null; lastCompileOutput?: string;
  pollCount: number; notModifiedCount: number;
}
export interface HomeworkProgress {
  passedCount: number; challenges: Array<{ challengeId: number; name: string; challengeSt: number; passedStatus: number }>;
}

function record(value: unknown, label: string): JsonObject {
  if (!value || typeof value !== 'object' || Array.isArray(value) || isNotModified(value)) throw new Error(`${label} 缺少 JSON 对象`);
  return value as JsonObject;
}
function positive(value: unknown, label: string): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0) throw new Error(`${label} 缺少有效整数`);
  return value;
}
function text(value: unknown, label: string): string {
  if (typeof value !== 'string' || !value) throw new Error(`${label} 缺少字符串`);
  return value;
}
function decodeCode(value: unknown): string {
  if (typeof value !== 'string' || !/^[A-Za-z0-9+/]*={0,2}$/.test(value.replace(/\s/g, '')) || value.replace(/\s/g, '').length % 4 === 1) throw new Error('代码内容缺少有效 Base64');
  return new TextDecoder('utf-8', { fatal: true }).decode(Buffer.from(value, 'base64'));
}
function decodeLogFrame(value: string, decoder: TextDecoder): string {
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(value) || value.length % 4 === 1) throw new Error('自测返回无效 Base64');
  return decoder.decode(Buffer.from(value, 'base64'), { stream: true });
}
export function classifyTask(value: unknown): 'coding' | 'unsupported' {
  const data = record(value, 'tasks');
  return data.st === 0 && data.challenge?.st === 0 && data.challenge?.with_code_file === true ? 'coding' : 'unsupported';
}
export function normalizeLineEndings(value: string): string { return value.replace(/\r\n/g, '\n'); }
function limits(options: LogOptions): { timeoutMs: number; maxOutputBytes: number } {
  const timeoutMs = options.timeoutMs ?? 30_000, maxOutputBytes = options.maxOutputBytes ?? 1024 * 1024;
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 300_000 || !Number.isSafeInteger(maxOutputBytes) || maxOutputBytes < 1 || maxOutputBytes > 16 * 1024 * 1024) throw new Error('自测超时/输出上限无效');
  return { timeoutMs, maxOutputBytes };
}

/** The captured frontend sends plain JSON and Base64-decodes each incoming log message once. */
export class WebSocketLogRunner implements LogRunner {
  private readonly create: (url: string) => WebSocket;
  constructor(create: (url: string) => WebSocket = url => new WebSocket(url)) { this.create = create; }
  async run(url: string, myshixunId: number, options: LogOptions = {}): Promise<SelfTestResult> {
    const { timeoutMs, maxOutputBytes } = limits(options);
    const target = new URL(url);
    if (target.protocol !== 'wss:' && target.protocol !== 'ws:') throw new Error('自测缺少 WebSocket 地址');
    const socket = this.create(target.href);
    socket.binaryType = 'arraybuffer';
    return await new Promise<SelfTestResult>((resolve, reject) => {
      let output = '', settled = false;
      const decoder = new TextDecoder('utf-8', { fatal: true });
      const timer = setTimeout(() => finish(new Error('自测等待 ProcessFinished 超时')), timeoutMs);
      const finish = (error?: Error) => {
        if (settled) return;
        settled = true; clearTimeout(timer);
        socket.close(1000);
        if (error) reject(error); else resolve({ stdout: output, finished: true, closeCode: 1000 });
      };
      socket.addEventListener('open', () => socket.send(JSON.stringify({ tp: 'log', data: myshixunId })));
      socket.addEventListener('message', event => {
        if (settled) return;
        try {
          const frame = typeof event.data === 'string' ? event.data : new TextDecoder('utf-8', { fatal: true }).decode(event.data as ArrayBuffer);
          const decoded = decodeLogFrame(frame, decoder);
          if (decoded === 'ProcessFinished') { finish(); return; }
          output += decoded;
          if (Buffer.byteLength(output, 'utf8') > maxOutputBytes) { finish(new Error('自测输出超过上限')); return; }
        } catch { finish(new Error('自测返回无效 UTF-8')); }
      });
      socket.addEventListener('error', () => finish(new Error('自测 WebSocket 连接失败')));
      socket.addEventListener('close', () => { if (!settled) finish(new Error('自测结束前 WebSocket 已关闭')); });
    });
  }
}

/** Reqable preserves the same Base64 log text consumed by the frontend; decode it exactly once. */
export class HarLogRunner implements LogRunner {
  private readonly entries: readonly HarEntry[];
  private readonly consumed = new Set<HarEntry>();
  constructor(entries: readonly HarEntry[]) { this.entries = entries; }
  async run(url: string, myshixunId: number, options: LogOptions = {}): Promise<SelfTestResult> {
    const { timeoutMs, maxOutputBytes } = limits(options);
    const entry = this.entries.find(e => !this.consumed.has(e) && e._webSocketMessages && new URL(e.request.url).href === new URL(url).href);
    if (!entry) throw new Error('HAR 未录制或已消费该自测 WebSocket');
    this.consumed.add(entry);
    const messages = entry._webSocketMessages!;
    const sent = messages.find(m => m.flow === 0 && m.payload.text);
    let command: unknown;
    try { command = JSON.parse(sent?.payload.text ?? ''); } catch { throw new Error('HAR 自测缺少发送命令'); }
    if (JSON.stringify(command) !== JSON.stringify({ tp: 'log', data: myshixunId })) throw new Error('HAR 自测 myshixun 标识不匹配');
    let output = '', finished = false;
    const firstTime = sent?.timestamp;
    const decoder = new TextDecoder('utf-8', { fatal: true });
    for (const message of messages) {
      if (message.flow !== 1 || message.payload.type === 6) continue;
      if (firstTime !== undefined && message.timestamp !== undefined && message.timestamp - firstTime > timeoutMs) throw new Error('HAR 自测超过超时上限');
      const payload = message.payload;
      const decoded = decodeLogFrame(payload.text ?? '', decoder);
      if (decoded === 'ProcessFinished') { finished = true; break; }
      output += decoded;
      if (Buffer.byteLength(output, 'utf8') > maxOutputBytes) throw new Error('HAR 自测输出超过上限');
    }
    if (!finished) throw new Error('HAR 自测缺少 ProcessFinished');
    if (!messages.some(m => m.flow === 0 && m.payload.type === 6 && m.payload.code === 1000)) throw new Error('HAR 自测缺少 close 1000');
    return { stdout: output, finished: true, closeCode: 1000 };
  }
}

export class ProgrammingClient {
  readonly client: EducoderClient;
  readonly logs: LogRunner;
  private readonly builds = new WeakMap<SavedCode, EvaluationStart>();
  constructor(client: EducoderClient, options: { logs?: LogRunner } = {}) {
    this.client = client; this.logs = options.logs ?? new WebSocketLogRunner();
  }
  private login(): string { return text(this.client.user?.login, '当前用户'); }
  private assertCoding(task: CodingTask): void {
    if (task.kind !== 'coding' || task.st !== 0 || task.challengeSt !== 0 || task.withCodeFile !== true) throw new Error('题型未核实为代码题');
    positive(task.gameId, 'game.id'); positive(task.myshixunId, 'myshixun.id'); positive(task.challengeId, 'challenge.id');
    positive(task.userId, 'user.id'); positive(task.environmentId, 'environment.id'); positive(task.tabType, 'tab_type');
    text(task.homeworkId, 'homework.id'); text(task.gameIdentifier, 'game.identifier'); text(task.myshixunIdentifier, 'myshixun.identifier');
  }
  private extras(task: CodingTask): JsonObject {
    return { exercise_id: '', question_id: '', challenge_id: task.challengeId, subject_id: '',
      smart_plan_page_item_bank_id: '', homework_common_id: task.homeworkId, competition_entry_id: '', currentUserId: task.userId };
  }
  async task(gameIdentifier: string, homeworkId: string | number): Promise<CodingTask> {
    const d = record(await this.client.transport.request('GET', `/api/tasks/${encodeURIComponent(gameIdentifier)}.json`, {
      homework_common_id: homeworkId, zzud: this.login(),
    }), 'tasks');
    if (classifyTask(d) !== 'coding') throw new Error('当前题型未观察为代码题（st=0、challenge.st=0、with_code_file=true）');
    const challenge = record(d.challenge, 'challenge'), game = record(d.game, 'game'), myshixun = record(d.myshixun, 'myshixun');
    const editorEnvironmentId = positive(d.code_editor?.shixun_environment_id, 'code_editor.shixun_environment_id');
    const environment = (d.shixun_environments as JsonObject[] | undefined)?.find(e => e.tab_type === 1 && e.shixun_environment_id === editorEnvironmentId);
    if (!environment) throw new Error('tasks 缺少代码文件环境');
    const paths = [...new Set(text(challenge.path, 'challenge.path').split('；').map((path: string) => path.trim()).filter(Boolean))];
    const result: CodingTask = { kind: 'coding', st: 0, challengeSt: 0, withCodeFile: true,
      gameIdentifier: text(game.identifier, 'game.identifier'), gameId: positive(game.id, 'game.id'),
      myshixunIdentifier: text(myshixun.identifier, 'myshixun.identifier'), myshixunId: positive(myshixun.id, 'myshixun.id'),
      challengeId: positive(challenge.id, 'challenge.id'), homeworkId: String(homeworkId), userId: positive(d.user?.user_id ?? this.client.user?.id ?? game.user_id, 'user.user_id'),
      subject: text(challenge.subject, 'challenge.subject'), taskMarkdown: text(challenge.task_pass, 'challenge.task_pass'), paths,
      environmentId: positive(environment.shixun_environment_id, 'shixun_environment_id'), tabType: positive(environment.tab_type, 'tab_type'),
      gameCount: d.game_count, challengesCount: d.shixun?.challenges_count, nextGame: d.next_game,
      testSets: Array.isArray(d.test_sets) ? d.test_sets : [],
    };
    if (result.gameIdentifier !== gameIdentifier || !paths.length || (d.homework_common_id !== undefined && String(d.homework_common_id) !== result.homeworkId)
      || (game.user_id !== undefined && game.user_id !== result.userId)
      || (this.client.user?.id !== undefined && this.client.user.id !== result.userId)
      || (myshixun.user_id !== undefined && myshixun.user_id !== result.userId)
      || (game.challenge_id !== undefined && game.challenge_id !== result.challengeId) || (game.myshixun_id !== undefined && game.myshixun_id !== result.myshixunId)) throw new Error('tasks 上下文标识不一致');
    this.assertCoding(result); return result;
  }
  async readFile(task: CodingTask, path: string): Promise<CodeFile> {
    this.assertCoding(task);
    if (!task.paths.includes(path)) throw new Error('文件路径不属于当前关卡');
    const d = record(await this.client.transport.request('GET', `/api/tasks/${encodeURIComponent(task.gameIdentifier)}/rep_content.json`, {
      path, homework_common_id: task.homeworkId, exercise_id: '', zzud: this.login(),
    }), 'rep_content');
    const content = record(d.content, 'rep_content.content');
    const filename = text(d.filename, 'filename');
    return { path, filename, extension: extname(filename), language: typeof d.language === 'string' ? d.language : '',
      fileType: typeof d.file_type === 'string' ? d.file_type : '', currentCode: decodeCode(content.content), size: content.size,
      provenance: 'recorded-current-workspace' };
  }
  async save(task: CodingTask, path: string, content: string, evaluate: 0 | 1 = 0): Promise<SavedCode> {
    this.assertCoding(task);
    if (!task.paths.includes(path) || typeof content !== 'string' || (evaluate !== 0 && evaluate !== 1)) throw new Error('保存的路径、内容或 evaluate 无效');
    const d = record(await this.client.transport.request('POST', `/api/myshixuns/${encodeURIComponent(task.myshixunIdentifier)}/update_file.json`, { zzud: this.login() }, {
      path, evaluate, content, game_id: task.gameId, tab_type: task.tabType, exercise_id: null,
      homework_common_id: task.homeworkId, extras: this.extras(task),
    }), 'update_file');
    if (typeof d.resubmit !== 'string' || typeof d.sec_key !== 'string' || (evaluate === 1 && !d.sec_key) || !Number.isInteger(d.content_modified)) throw new Error('保存响应缺少 resubmit/content_modified/提交密钥');
    return { gameId: task.gameId, evaluate, commitId: text(d.content?.commitID, 'commitID'), resubmit: d.resubmit,
      secKey: d.sec_key, contentModified: d.content_modified };
  }
  async selfTest(task: CodingTask, stdin: string, expectedOutput?: string, options: LogOptions = {}): Promise<SelfTestResult> {
    this.assertCoding(task);
    const d = record(await this.client.transport.request('POST', `/api/tasks/${encodeURIComponent(task.gameIdentifier)}/log_output`, { zzud: this.login() }, {
      test_case_content: stdin, shixun_environment_id: task.environmentId, tab_type: task.tabType,
      extras: { homework_common_id: task.homeworkId },
    }), 'log_output');
    const wsBase = text(d.data?.wss_url, 'log_output WebSocket');
    const result = await this.logs.run(`${wsBase.replace(/\/$/, '')}/log`, task.myshixunId, options);
    return expectedOutput === undefined ? result : { ...result, matchesExpected: normalizeLineEndings(result.stdout) === normalizeLineEndings(expectedOutput) };
  }
  async startEvaluation(task: CodingTask, saved: SavedCode): Promise<EvaluationStart> {
    this.assertCoding(task);
    if (saved.gameId !== task.gameId || saved.evaluate !== 1) throw new Error('提交需要本关 evaluate=1 的保存结果');
    const d = record(await this.client.transport.request('POST', `/api/tasks/${encodeURIComponent(task.gameIdentifier)}/game_build.json`, { zzud: this.login() }, {
      sec_key: saved.secKey, resubmit: saved.resubmit, first: 1, content_modified: saved.contentModified,
      shixun_environment_id: task.environmentId, tab_type: task.tabType, extras: { ...this.extras(task), commitID: saved.commitId },
    }), 'game_build');
    if (d.status !== 1) throw new Error('评测未返回启动状态 1');
    if (!Number.isSafeInteger(d.port) || d.port < 0) throw new Error('评测启动缺少有效 port');
    const started = { gameId: task.gameId, port: d.port };
    this.builds.set(saved, started); return started;
  }
  async pollEvaluation(task: CodingTask, saved: SavedCode, options: { maxPolls?: number; intervalMs?: number; timeoutMs?: number; sleep?: (ms: number) => Promise<void> } = {}): Promise<EvaluationResult> {
    this.assertCoding(task);
    if (saved.gameId !== task.gameId || saved.evaluate !== 1) throw new Error('轮询需要本关提交保存结果');
    const started = this.builds.get(saved);
    if (!started || started.gameId !== task.gameId) throw new Error('轮询需要本次 game_build 启动结果');
    const maxPolls = options.maxPolls ?? 60, intervalMs = options.intervalMs ?? 1000, timeoutMs = options.timeoutMs ?? 120_000;
    if (!Number.isInteger(maxPolls) || maxPolls < 1 || maxPolls > 1000 || !Number.isFinite(intervalMs) || intervalMs < 0 || !Number.isFinite(timeoutMs) || timeoutMs <= 0) throw new Error('轮询上限无效');
    const deadline = Date.now() + timeoutMs;
    let notModifiedCount = 0;
    for (let pollCount = 1; pollCount <= maxPolls && Date.now() < deadline; pollCount++) {
      const value = await this.client.transport.request('GET', `/api/tasks/${encodeURIComponent(task.gameIdentifier)}/game_status.json`, {
        resubmit: saved.resubmit, time_out: 'false', port: String(started.port), sec_key: saved.secKey, challenge_id: task.challengeId,
        subject_id: '', homework_common_id: task.homeworkId, zzud: this.login(),
      });
      if (isNotModified(value)) notModifiedCount++;
      else {
        const d = record(value, 'game_status');
        if (d.running_code_status !== 1 && d.running_code_status !== 2) {
          if (typeof d.status !== 'number' || typeof d.test_sets_count !== 'number' || typeof d.sets_error_count !== 'number' || !Array.isArray(d.test_sets)) throw new Error('评测终态缺少状态/测试集统计');
          return { complete: true, passed: d.status === 2 && d.test_sets_count > 0 && d.test_sets_count === d.test_sets.length && d.sets_error_count === 0
              && d.test_sets.every((s: JsonObject) => s.result === true && s.compile_success === 1),
            status: d.status, testSetsCount: d.test_sets_count, setsErrorCount: d.sets_error_count, testSets: d.test_sets,
            nextGame: d.next_game, lastCompileOutput: d.last_compile_output, pollCount, notModifiedCount };
        }
      }
      if (pollCount < maxPolls && Date.now() < deadline) await (options.sleep ?? (ms => delay(ms)))(Math.min(intervalMs, Math.max(0, deadline - Date.now())));
    }
    throw new Error('评测轮询达到上限，当前评测仍未完成');
  }
  async progress(homeworkId: string | number): Promise<HomeworkProgress> {
    const d = record(await this.client.transport.request('GET', `/api/homework_commons/${encodeURIComponent(String(homeworkId))}/shixun_challenge_data.json`), 'challenge_data');
    if (!Array.isArray(d.data?.challenge_settings) || typeof d.data.passed_count !== 'number') throw new Error('作业进度缺少关卡状态');
    return { passedCount: d.data.passed_count, challenges: d.data.challenge_settings.map((x: JsonObject) => ({ challengeId: x.challenge_id, name: x.challenge_name, challengeSt: x.challenge_st, passedStatus: x.passed_status })) };
  }
}

function requestBody(entry: HarEntry): JsonObject { return record(JSON.parse(entry.request.postData?.text ?? 'null'), 'HAR request body'); }
function queryOf(entry: HarEntry): Record<string, string> { return Object.fromEntries(new URL(entry.request.url).searchParams); }

/** Replays only observed coding operations, using the recording's submitted code as evidence. No answers are generated. */
export async function replayCodingHar(path: string, constants: ProtocolConstants): Promise<JsonObject> {
  const transport = await HarTransport.load(path);
  const client = new EducoderClient(transport, constants);
  const coding = new ProgrammingClient(client, { logs: new HarLogRunner(transport.entries) });
  const api = transport.entries.filter(e => new URL(e.request.url).hostname === transport.apiHost && e.request.method !== 'OPTIONS');
  const tasks = api.filter(e => e.request.method === 'GET' && /^\/api\/tasks\/[^/]+\.json$/.test(new URL(e.request.url).pathname) && e.response.status === 200);
  if (!tasks.length) throw new Error('HAR 未观察到可用 tasks 响应（304 不生成内容）');
  const firstQuery = queryOf(tasks[0]);
  const recordedLogin = text(firstQuery.zzud, 'HAR tasks 用户上下文');
  client.user = transport.restoreRecordedUser();
  if (client.user?.login !== recordedLogin) throw new Error('HAR 用户与任务上下文不一致');
  const homeworkId = text(firstQuery.homework_common_id, 'HAR homework_common_id');
  const progressEntries = api.filter(e => new URL(e.request.url).pathname === `/api/homework_commons/${homeworkId}/shixun_challenge_data.json` && e.request.method === 'GET');
  if (progressEntries.length < 2) throw new Error('HAR 缺少挑战前后进度响应');
  const before = await coding.progress(homeworkId);
  const rows: JsonObject[] = [];
  for (let index = 0; index < tasks.length; index++) {
    const entry = tasks[index], taskStart = api.indexOf(entry), taskEnd = index + 1 < tasks.length ? api.indexOf(tasks[index + 1]) : api.length;
    const segment = api.slice(taskStart + 1, taskEnd);
    const gameIdentifier = new URL(entry.request.url).pathname.split('/')[3].replace(/\.json$/, '');
    const task = await coding.task(gameIdentifier, homeworkId);
    const files: CodeFile[] = [];
    for (const rep of segment.filter(e => e.request.method === 'GET' && new URL(e.request.url).pathname === `/api/tasks/${gameIdentifier}/rep_content.json`)) files.push(await coding.readFile(task, queryOf(rep).path));
    if (!files.length) throw new Error('HAR 缺少当前代码文件');
    let submitted: SavedCode | undefined, evaluation: EvaluationResult | undefined, selfTest: SelfTestResult | undefined;
    let saves = 0, autoSaves = 0, autoSaveMissingBodies = 0, buildCount = 0, initialEqualsSubmitted: boolean | undefined;
    for (const observed of segment) {
      const route = new URL(observed.request.url).pathname;
      if (observed.request.method !== 'POST') continue;
      if (route === `/api/myshixuns/${task.myshixunIdentifier}/update_file.json`) {
        const body = requestBody(observed);
        if (body.game_id !== task.gameId) throw new Error('HAR 保存关联到其他关卡');
        if (body.extras && Object.hasOwn(body, 'exercise_id')) {
          const saved = await coding.save(task, body.path, body.content, body.evaluate);
          saves++;
          if (body.evaluate === 1) { submitted = saved; initialEqualsSubmitted = files.find(f => f.path === body.path)?.currentCode === body.content; }
        } else {
          // The UI also records a reduced autosave on leaving the level. Keep its exact recorded shape.
          autoSaves++;
          if (observed.response.content.text) await transport.request('POST', route, queryOf(observed), body);
          else autoSaveMissingBodies++;
        }
      } else if (route === `/api/tasks/${gameIdentifier}/log_output`) {
        const body = requestBody(observed);
        const expected = task.testSets.find(s => s.input === body.test_case_content && typeof s.output === 'string')?.output;
        selfTest = await coding.selfTest(task, body.test_case_content, expected);
        if (selfTest.matchesExpected === false) throw new Error('HAR 自测输出与该输入的预期不一致');
      } else if (route === `/api/tasks/${gameIdentifier}/game_build.json`) {
        if (!submitted) throw new Error('HAR build 缺少 evaluate=1 的保存');
        await coding.startEvaluation(task, submitted); buildCount++;
        evaluation = await coding.pollEvaluation(task, submitted, { intervalMs: 0, maxPolls: 100 });
      }
    }
    if (!selfTest || !evaluation || !evaluation.passed || buildCount !== 1) throw new Error('HAR 缺少完整自测/提交/通过链路');
    rows.push({ subject: task.subject, type: task.kind, taskMarkdownBytes: Buffer.byteLength(task.taskMarkdown),
      files: files.map(file => ({ path: file.path, filename: file.filename, extension: file.extension, language: file.language,
        codeBytes: Buffer.byteLength(file.currentCode), provenance: file.provenance })),
      initialEqualsSubmitted, saves, leaveAutosaves: autoSaves, leaveAutosaveMissingBodies: autoSaveMissingBodies, selfTest: { finished: selfTest.finished, closeCode: selfTest.closeCode,
        outputBytes: Buffer.byteLength(selfTest.stdout), matchesExpected: selfTest.matchesExpected ?? 'no captured matching expected output' },
      evaluation: { passed: evaluation.passed, status: evaluation.status, testSetsCount: evaluation.testSetsCount, setsErrorCount: evaluation.setsErrorCount,
        polls: evaluation.pollCount, notModified: evaluation.notModifiedCount }, gameCount: task.gameCount, shixunChallengesCount: task.challengesCount });
  }
  const after = await coding.progress(homeworkId);
  for (const challenge of after.challenges) {
    if (challenge.challengeSt === 0 && challenge.passedStatus !== 2) throw new Error('刷新后存在未通过代码关卡');
  }
  return { mode: 'offline-har', networkRequests: 0, taskCount: rows.length, beforePassedCount: before.passedCount,
    afterPassedCount: after.passedCount, refreshedStatuses: after.challenges.map(x => x.passedStatus), tasks: rows };
}
